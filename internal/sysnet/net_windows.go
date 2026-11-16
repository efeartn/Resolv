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

// detectScript gathers every "Up" adapter using batched OS queries (minimizing
// WMI/CIM round-trips on low-spec hardware) and falls back cleanly.
const detectScript = `
$ErrorActionPreference = 'SilentlyContinue'
$ProgressPreference = 'SilentlyContinue'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$os = [string](Get-CimInstance -ClassName Win32_OperatingSystem).Caption

$ip4Map = @{}
$ip6Map = @{}
foreach ($ip in @(Get-NetIPAddress)) {
  $k = [int]$ip.InterfaceIndex
  $addr = [string]$ip.IPAddress
  if (-not $addr) { continue }
  if ($ip.AddressFamily -eq 'IPv4' -or $ip.AddressFamily -eq 2) {
    if (-not $ip4Map.ContainsKey($k)) { $ip4Map[$k] = [System.Collections.Generic.List[string]]::new() }
    $ip4Map[$k].Add($addr)
  } elseif (($ip.AddressFamily -eq 'IPv6' -or $ip.AddressFamily -eq 23) -and ($addr -notlike 'fe80*')) {
    if (-not $ip6Map.ContainsKey($k)) { $ip6Map[$k] = [System.Collections.Generic.List[string]]::new() }
    $ip6Map[$k].Add($addr)
  }
}

$routeMap = @{}
foreach ($rt in @(Get-NetRoute -DestinationPrefix '0.0.0.0/0' | Sort-Object -Property RouteMetric)) {
  $k = [int]$rt.InterfaceIndex
  if (-not $routeMap.ContainsKey($k)) { $routeMap[$k] = $rt }
}

$ifMetricMap = @{}
foreach ($ipif in @(Get-NetIPInterface -AddressFamily IPv4)) {
  $ifMetricMap[[int]$ipif.InterfaceIndex] = [int]$ipif.InterfaceMetric
}

$dnsMap = @{}
foreach ($d in @(Get-DnsClientServerAddress)) {
  $k = [int]$d.InterfaceIndex
  foreach ($s in @($d.ServerAddresses)) {
    if ($s -and ($s -notlike 'fec0:*')) {
      if (-not $dnsMap.ContainsKey($k)) { $dnsMap[$k] = [System.Collections.Generic.List[string]]::new() }
      $dnsMap[$k].Add([string]$s)
    }
  }
}

[pscustomobject[]]$adapters = @(Get-NetAdapter | Where-Object { $_.Status -eq 'Up' } | ForEach-Object {
  $a = $_
  $idx = [int]$a.ifIndex
  $regPath = 'HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\' + $a.InterfaceGuid
  $reg = if (Test-Path $regPath) { Get-ItemProperty -Path $regPath } else { $null }
  $rt = $routeMap[$idx]
  $gw = ''
  $metric = 9999
  if ($rt) {
    $gw = [string]$rt.NextHop
    $ifM = if ($ifMetricMap.ContainsKey($idx)) { $ifMetricMap[$idx] } else { 0 }
    $metric = [int]$rt.RouteMetric + $ifM
  }
  $v4 = $null; if ($ip4Map.ContainsKey($idx) -and $ip4Map[$idx].Count -gt 0) { $v4 = $ip4Map[$idx].ToArray() }
  $v6 = $null; if ($ip6Map.ContainsKey($idx) -and $ip6Map[$idx].Count -gt 0) { $v6 = $ip6Map[$idx].ToArray() }
  $dv = $null; if ($dnsMap.ContainsKey($idx) -and $dnsMap[$idx].Count -gt 0) { $dv = $dnsMap[$idx].ToArray() }
  [pscustomobject]@{
    Index       = $idx
    Name        = [string]$a.Name
    Description = [string]$a.InterfaceDescription
    MediaType   = [string]$a.PhysicalMediaType
    Mac         = [string]$a.MacAddress
    Virtual     = [bool]$a.Virtual
    Hardware    = [bool]$a.HardwareInterface
    IPv4        = $v4
    IPv6        = $v6
    Gateway     = $gw
    Metric      = $metric
    DNS         = $dv
    StaticDNS   = if ($reg) { [bool]([string]$reg.NameServer) } else { $false }
  }
})
ConvertTo-Json -InputObject ([pscustomobject]@{ OS = $os; Adapters = $adapters }) -Depth 5 -Compress
`

// winStringSlice handles PowerShell 5.1 ConvertTo-Json quirks where a 0-element
// array may become null/{} and a 1-element array may become a scalar JSON string.
type winStringSlice []string

func (s *winStringSlice) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == "{}" {
		*s = nil
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var single string
		if err := json.Unmarshal(data, &single); err != nil {
			return err
		}
		if single != "" {
			*s = []string{single}
		} else {
			*s = nil
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*s = list
	return nil
}

type winAdapter struct {
	Index       int            `json:"Index"`
	Name        string         `json:"Name"`
	Description string         `json:"Description"`
	MediaType   string         `json:"MediaType"`
	Mac         string         `json:"Mac"`
	Virtual     bool           `json:"Virtual"`
	Hardware    bool           `json:"Hardware"`
	IPv4        winStringSlice `json:"IPv4"`
	IPv6        winStringSlice `json:"IPv6"`
	Gateway     string         `json:"Gateway"`
	Metric      int            `json:"Metric"`
	DNS         winStringSlice `json:"DNS"`
	StaticDNS   bool           `json:"StaticDNS"`
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
			IPv4:      []string(a.IPv4),
			IPv6:      []string(a.IPv6),
			Gateway:   a.Gateway,
			DNS:       []string(a.DNS),
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
	v4 := make([]string, 0, len(servers))
	v6 := make([]string, 0, len(servers))
	for _, s := range servers {
		if strings.Contains(s, ":") {
			v6 = append(v6, s)
		} else {
			v4 = append(v4, s)
		}
	}
	script := fmt.Sprintf(`if (Get-Command Set-DnsClientServerAddress -ErrorAction SilentlyContinue) {
  Set-DnsClientServerAddress -InterfaceIndex %d -ServerAddresses @(%s)
} else {
  $name = (Get-CimInstance Win32_NetworkAdapter -Filter "InterfaceIndex=%d").NetConnectionID
  $v4 = @(%s); $v6 = @(%s)
  if ($v4.Count -gt 0) { & netsh.exe interface ipv4 set dns name="$name" static $v4[0] primary | Out-Null; for ($i=1; $i -lt $v4.Count; $i++) { & netsh.exe interface ipv4 add dns name="$name" $v4[$i] index=($i+1) | Out-Null } }
  if ($v6.Count -gt 0) { & netsh.exe interface ipv6 set dnsservers name="$name" static $v6[0] primary | Out-Null; for ($i=1; $i -lt $v6.Count; $i++) { & netsh.exe interface ipv6 add dnsservers name="$name" $v6[$i] index=($i+1) | Out-Null } }
}
Clear-DnsClientCache -ErrorAction SilentlyContinue; & ipconfig.exe /flushdns | Out-Null`,
		idx, psList(servers), idx, psList(v4), psList(v6))
	return runPrivileged(ctx, script)
}

func resetDNS(ctx context.Context, iface *Adapter) error {
	idx, err := ifIndex(iface)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`if (Get-Command Set-DnsClientServerAddress -ErrorAction SilentlyContinue) {
  Set-DnsClientServerAddress -InterfaceIndex %d -ResetServerAddresses
} else {
  $name = (Get-CimInstance Win32_NetworkAdapter -Filter "InterfaceIndex=%d").NetConnectionID
  & netsh.exe interface ipv4 set dns name="$name" dhcp | Out-Null
  & netsh.exe interface ipv6 set dnsservers name="$name" dhcp | Out-Null
}
Clear-DnsClientCache -ErrorAction SilentlyContinue; & ipconfig.exe /flushdns | Out-Null`, idx, idx)
	return runPrivileged(ctx, script)
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
