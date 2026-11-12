// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build windows

package sysnet

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// detectScript gathers every "Up" adapter with its addresses, default route,
// DNS servers and whether DNS is static (registry NameServer value).
const detectScript = `
$ErrorActionPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$os = [string](Get-CimInstance -ClassName Win32_OperatingSystem).Caption
$adapters = @(Get-NetAdapter | Where-Object { $_.Status -eq 'Up' } | ForEach-Object {
  $a = $_
  $idx = [int]$a.ifIndex
  $ip4 = @(Get-NetIPAddress -InterfaceIndex $idx -AddressFamily IPv4 | ForEach-Object { [string]$_.IPAddress } | Where-Object { $_ })
  $ip6 = @(Get-NetIPAddress -InterfaceIndex $idx -AddressFamily IPv6 | Where-Object { $_.IPAddress -notlike 'fe80*' } | ForEach-Object { [string]$_.IPAddress })
  $route = Get-NetRoute -InterfaceIndex $idx -DestinationPrefix '0.0.0.0/0' | Sort-Object -Property RouteMetric | Select-Object -First 1
  $ipif = Get-NetIPInterface -InterfaceIndex $idx -AddressFamily IPv4
  $dns = @(Get-DnsClientServerAddress -InterfaceIndex $idx | ForEach-Object { $_.ServerAddresses } | Where-Object { $_ -and ($_ -notlike 'fec0:*') } | ForEach-Object { [string]$_ })
  $reg = Get-ItemProperty -Path ('HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\' + $a.InterfaceGuid)
  $gw = ''
  $metric = 9999
  if ($route) { $gw = [string]$route.NextHop; $metric = [int]$route.RouteMetric + [int]$ipif.InterfaceMetric }
  [pscustomobject]@{
    Index       = $idx
    Name        = [string]$a.Name
    Description = [string]$a.InterfaceDescription
    MediaType   = [string]$a.PhysicalMediaType
    Mac         = [string]$a.MacAddress
    Virtual     = [bool]$a.Virtual
    Hardware    = [bool]$a.HardwareInterface
    IPv4        = $ip4
    IPv6        = $ip6
    Gateway     = $gw
    Metric      = $metric
    DNS         = $dns
    StaticDNS   = [bool]([string]$reg.NameServer)
  }
})
ConvertTo-Json -InputObject ([pscustomobject]@{ OS = $os; Adapters = $adapters }) -Depth 5 -Compress
`

type winAdapter struct {
	Index       int      `json:"Index"`
	Name        string   `json:"Name"`
	Description string   `json:"Description"`
	MediaType   string   `json:"MediaType"`
	Mac         string   `json:"Mac"`
	Virtual     bool     `json:"Virtual"`
	Hardware    bool     `json:"Hardware"`
	IPv4        []string `json:"IPv4"`
	IPv6        []string `json:"IPv6"`
	Gateway     string   `json:"Gateway"`
	Metric      int      `json:"Metric"`
	DNS         []string `json:"DNS"`
	StaticDNS   bool     `json:"StaticDNS"`
}

type winDetect struct {
	OS       string       `json:"OS"`
	Adapters []winAdapter `json:"Adapters"`
}

func isElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// encodePS encodes a script for powershell.exe -EncodedCommand (UTF-16LE base64).
func encodePS(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, len(u)*2)
	for i, v := range u {
		binary.LittleEndian.PutUint16(b[i*2:], v)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func runPS(ctx context.Context, script string) (string, error) {
	return run(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", encodePS(script))
}

func detect(ctx context.Context) (*Info, error) {
	info := &Info{Backend: "powershell"}
	out, err := runPS(ctx, detectScript)
	if err != nil && out == "" {
		return info, fmt.Errorf("network detection failed: %w", err)
	}
	var d winDetect
	if err := json.Unmarshal([]byte(strings.TrimPrefix(out, "\ufeff")), &d); err != nil {
		return info, fmt.Errorf("could not parse adapter information: %w", err)
	}
	info.OSName = strings.TrimPrefix(strings.TrimSpace(d.OS), "Microsoft ")

	// Prefer physical adapters with a default route, lowest metric first.
	sort.SliceStable(d.Adapters, func(i, j int) bool {
		a, b := d.Adapters[i], d.Adapters[j]
		ag, bg := a.Gateway != "", b.Gateway != ""
		if ag != bg {
			return ag
		}
		ap, bp := a.Hardware && !a.Virtual, b.Hardware && !b.Virtual
		if ap != bp {
			return ap
		}
		return a.Metric < b.Metric
	})
	for _, a := range d.Adapters {
		iface := Adapter{
			ID:        strconv.Itoa(a.Index),
			Name:      a.Name,
			Device:    a.Description,
			Kind:      guessKind(a.MediaType + " " + a.Name + " " + a.Description),
			MAC:       strings.ReplaceAll(a.Mac, "-", ":"),
			IPv4:      a.IPv4,
			IPv6:      a.IPv6,
			Gateway:   a.Gateway,
			DNS:       a.DNS,
			DNSManual: a.StaticDNS,
		}
		// Hide disconnected/virtual noise (Hyper-V, VPN taps) unless it is all we have.
		if !(a.Hardware && !a.Virtual) && a.Gateway == "" {
			continue
		}
		info.Interfaces = append(info.Interfaces, iface)
	}
	if len(info.Interfaces) == 0 {
		return info, ErrNoInterface
	}
	active := info.Interfaces[0]
	info.Active = &active
	info.Gateway = active.Gateway
	info.SystemDNS = active.DNS
	return info, nil
}

func psList(servers []string) string {
	q := make([]string, len(servers))
	for i, s := range servers {
		q[i] = "'" + s + "'" // validated IP literals only
	}
	return strings.Join(q, ",")
}

func ifIndex(iface *Adapter) (int, error) {
	idx, err := strconv.Atoi(iface.ID)
	if err != nil || idx <= 0 {
		return 0, fmt.Errorf("invalid interface index %q", iface.ID)
	}
	return idx, nil
}

func applyDNS(ctx context.Context, iface *Adapter, servers []string) error {
	idx, err := ifIndex(iface)
	if err != nil {
		return err
	}
	return runPrivileged(ctx, fmt.Sprintf(
		"Set-DnsClientServerAddress -InterfaceIndex %d -ServerAddresses @(%s)\nClear-DnsClientCache",
		idx, psList(servers)))
}

func resetDNS(ctx context.Context, iface *Adapter) error {
	idx, err := ifIndex(iface)
	if err != nil {
		return err
	}
	return runPrivileged(ctx, fmt.Sprintf(
		"Set-DnsClientServerAddress -InterfaceIndex %d -ResetServerAddresses\nClear-DnsClientCache", idx))
}

func flushCache(ctx context.Context) error {
	// Flushing the resolver cache does not require elevation on Windows.
	if _, err := run(ctx, "ipconfig.exe", "/flushdns"); err != nil {
		_, err2 := runPS(ctx, "Clear-DnsClientCache")
		if err2 != nil {
			return err
		}
	}
	return nil
}

// runPrivileged runs body in an elevated PowerShell. When the app itself is
// not elevated, a UAC prompt is shown via Start-Process -Verb RunAs. Errors
// from the elevated process are relayed through a temporary file.
func runPrivileged(ctx context.Context, body string) error {
	errFile, err := os.CreateTemp("", "resolv-*.err")
	if err != nil {
		return err
	}
	errPath := errFile.Name()
	_ = errFile.Close()
	defer os.Remove(errPath)

	inner := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
try {
%s
  exit 0
} catch {
  Set-Content -LiteralPath '%s' -Value $_.Exception.Message -Encoding UTF8
  exit 1
}`, body, strings.ReplaceAll(errPath, "'", "''"))

	if isElevated() {
		_, err := runPS(ctx, inner)
		if err != nil {
			return relayError(errPath, err)
		}
		return nil
	}

	outer := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
try {
  $p = Start-Process -FilePath 'powershell.exe' -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-EncodedCommand','%s')
  exit $p.ExitCode
} catch {
  $e = $_.Exception
  while ($e) {
    if (($e -is [System.ComponentModel.Win32Exception]) -and ($e.NativeErrorCode -eq 1223)) { exit 1223 }
    $e = $e.InnerException
  }
  [Console]::Error.WriteLine($_.Exception.Message)
  exit 1
}`, encodePS(inner))

	_, err = runPS(ctx, outer)
	if err == nil {
		return nil
	}
	if exitCode(err) == 1223 {
		return ErrElevationCancelled
	}
	return relayError(errPath, err)
}

func relayError(errPath string, fallback error) error {
	if b, rerr := os.ReadFile(errPath); rerr == nil {
		if msg := strings.TrimSpace(strings.TrimPrefix(string(b), "\ufeff")); msg != "" {
			if strings.Contains(strings.ToLower(msg), "access") && strings.Contains(strings.ToLower(msg), "denied") {
				return errors.Join(ErrNotPermitted, errors.New(msg))
			}
			return errors.New(msg)
		}
	}
	return fallback
}

func manualGuide(iface *Adapter, servers []string) ManualGuide {
	name := "your network adapter"
	idx := "<index>"
	if iface != nil {
		name = iface.Name
		idx = iface.ID
	}
	return ManualGuide{
		Steps: []string{
			"Open Settings → Network & internet → " + name + ".",
			"Select “Hardware properties”, then “Edit” next to DNS server assignment.",
			"Choose “Manual”, enable IPv4 (and IPv6 if listed) and enter the addresses below.",
			"Save, then run “ipconfig /flushdns” to clear the cache.",
		},
		Command: fmt.Sprintf("Set-DnsClientServerAddress -InterfaceIndex %s -ServerAddresses %s; ipconfig /flushdns",
			idx, psList(servers)),
	}
}
