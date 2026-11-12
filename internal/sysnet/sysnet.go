// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

// Package sysnet abstracts OS-specific network operations: detecting the
// active interface and its DNS servers, applying/resetting DNS servers and
// flushing the resolver cache. Implementations live in net_<os>.go.
package sysnet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var (
	// ErrElevationCancelled is returned when the user dismisses the UAC /
	// macOS / polkit authentication prompt.
	ErrElevationCancelled = errors.New("the administrator permission request was cancelled")
	// ErrNotPermitted is returned when elevation is impossible or denied.
	ErrNotPermitted = errors.New("administrator privileges are required to change DNS settings")
	// ErrNoInterface is returned when no active network interface is found.
	ErrNoInterface = errors.New("no active network interface detected")
	// ErrUnsupported is returned when no supported network backend exists.
	ErrUnsupported = errors.New("no supported network configuration backend found")
)

// Interface describes a network interface/service.
type Adapter struct {
	ID         string   `json:"id"`         // Windows: ifIndex · macOS: service name · Linux: device
	Name       string   `json:"name"`       // human-friendly name
	Device     string   `json:"device"`     // en0, wlp3s0, Wi-Fi …
	Kind       string   `json:"kind"`       // wifi | ethernet | cellular | other
	MAC        string   `json:"mac,omitempty"`
	IPv4       []string `json:"ipv4"`
	IPv6       []string `json:"ipv6"`
	Gateway    string   `json:"gateway,omitempty"`
	DNS        []string `json:"dns"`
	DNSManual  bool     `json:"dnsManual"`            // DNS servers set statically
	Connection string   `json:"connection,omitempty"` // Linux NetworkManager connection
	HasIPv6    bool     `json:"hasIPv6"`              // has a global IPv6 address
}

// Info is a snapshot of the system's network configuration.
type Info struct {
	OS         string      `json:"os"`     // windows | darwin | linux
	OSName     string      `json:"osName"` // "Windows 11 Pro", "macOS 15.1" …
	Arch       string      `json:"arch"`
	Hostname   string      `json:"hostname"`
	Elevated   bool        `json:"elevated"`
	Online     bool        `json:"online"`
	Backend    string      `json:"backend"` // powershell | networksetup | nmcli | resolvectl
	Active     *Adapter  `json:"active"`
	Interfaces []Adapter `json:"interfaces"`
	SystemDNS  []string    `json:"systemDns"`
	Gateway    string      `json:"gateway"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// ManualGuide explains how to apply DNS settings by hand.
type ManualGuide struct {
	Steps   []string `json:"steps"`
	Command string   `json:"command"`
}

// Detect inspects the system network configuration.
func Detect(ctx context.Context) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	info, err := detect(ctx)
	if info == nil {
		info = &Info{}
	}
	info.OS = runtime.GOOS
	info.Arch = runtime.GOARCH
	info.Hostname, _ = os.Hostname()
	info.Elevated = isElevated()
	if info.OSName == "" {
		info.OSName = runtime.GOOS
	}
	for i := range info.Interfaces {
		finalize(&info.Interfaces[i])
	}
	if info.Active != nil {
		finalize(info.Active)
		if info.Gateway == "" {
			info.Gateway = info.Active.Gateway
		}
		if len(info.SystemDNS) == 0 {
			info.SystemDNS = info.Active.DNS
		}
	}
	info.SystemDNS = cleanIPs(info.SystemDNS)
	info.Online = info.Active != nil && (len(info.Active.IPv4) > 0 || len(info.Active.IPv6) > 0 || info.Gateway != "")
	return info, err
}

// ApplyDNS sets the DNS servers of iface and flushes the resolver cache.
func ApplyDNS(ctx context.Context, iface *Adapter, servers []string) error {
	if iface == nil {
		return ErrNoInterface
	}
	clean, err := validateServers(servers)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute) // includes time spent on the auth prompt
	defer cancel()
	return applyDNS(ctx, iface, clean)
}

// ResetDNS restores automatic (DHCP) DNS on iface.
func ResetDNS(ctx context.Context, iface *Adapter) error {
	if iface == nil {
		return ErrNoInterface
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return resetDNS(ctx, iface)
}

// FlushCache flushes the OS DNS cache.
func FlushCache(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	return flushCache(ctx)
}

// Manual returns OS-specific instructions to apply servers by hand.
func Manual(iface *Adapter, servers []string) ManualGuide {
	clean, _ := validateServers(servers)
	return manualGuide(iface, clean)
}

// validateServers strictly parses every address. This is the security
// boundary that keeps untrusted strings out of privileged shell commands.
func validateServers(servers []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, s := range servers {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip == nil {
			return nil, fmt.Errorf("invalid DNS server address %q", s)
		}
		v := ip.String()
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("no DNS server addresses to apply")
	}
	return out, nil
}

func splitFamilies(servers []string) (v4, v6 []string) {
	for _, s := range servers {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
			v4 = append(v4, s)
		} else {
			v6 = append(v6, s)
		}
	}
	return
}

func cleanIPs(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if i := strings.IndexByte(s, '%'); i >= 0 { // strip IPv6 zone
			s = s[:i]
		}
		ip := net.ParseIP(s)
		if ip == nil || ip.IsUnspecified() {
			continue
		}
		v := ip.String()
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// finalize fills addresses from the Go net package where the OS backend did
// not provide them, and normalises fields.
func finalize(iface *Adapter) {
	if iface.Device != "" && (len(iface.IPv4) == 0 && len(iface.IPv6) == 0) {
		if ni, err := net.InterfaceByName(iface.Device); err == nil {
			if addrs, err := ni.Addrs(); err == nil {
				for _, a := range addrs {
					ipn, ok := a.(*net.IPNet)
					if !ok {
						continue
					}
					if ipn.IP.To4() != nil {
						iface.IPv4 = append(iface.IPv4, ipn.IP.String())
					} else if !ipn.IP.IsLinkLocalUnicast() {
						iface.IPv6 = append(iface.IPv6, ipn.IP.String())
					}
				}
			}
			if iface.MAC == "" {
				iface.MAC = ni.HardwareAddr.String()
			}
		}
	}
	for _, s := range iface.IPv6 {
		if ip := net.ParseIP(s); ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			iface.HasIPv6 = true
		}
	}
	if iface.IPv4 == nil {
		iface.IPv4 = []string{}
	}
	if iface.IPv6 == nil {
		iface.IPv6 = []string{}
	}
	iface.DNS = cleanIPs(iface.DNS)
	if iface.DNS == nil {
		iface.DNS = []string{}
	}
	if iface.Kind == "" {
		iface.Kind = guessKind(iface.Name + " " + iface.Device)
	}
}

func guessKind(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "wi-fi"), strings.Contains(s, "wifi"), strings.Contains(s, "wireless"),
		strings.Contains(s, "wlan"), strings.Contains(s, "802.11"), strings.HasPrefix(s, "wl"), strings.Contains(s, "airport"):
		return "wifi"
	case strings.Contains(s, "ethernet"), strings.Contains(s, "802.3"), strings.HasPrefix(s, "eth"),
		strings.HasPrefix(s, "en"), strings.Contains(s, "lan"):
		return "ethernet"
	case strings.Contains(s, "wwan"), strings.Contains(s, "cellular"), strings.Contains(s, "mobile"), strings.Contains(s, "gsm"):
		return "cellular"
	}
	return "other"
}

// shellQuote quotes s for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// cmdError carries the exit code and output of a failed command.
type cmdError struct {
	Cmd    string
	Code   int
	Output string
	Err    error
}

func (e *cmdError) Error() string {
	msg := strings.TrimSpace(e.Output)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	if len(msg) > 400 {
		msg = msg[:400] + "…"
	}
	return fmt.Sprintf("%s failed (exit %d): %s", e.Cmd, e.Code, msg)
}

func (e *cmdError) Unwrap() error { return e.Err }

func exitCode(err error) int {
	var ce *cmdError
	if errors.As(err, &ce) {
		return ce.Code
	}
	return -1
}

// run executes a command without a shell and returns its stdout. On failure
// the returned error is a *cmdError that includes stderr.
func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	prepareCmd(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		combined := strings.TrimSpace(stderr.String() + "\n" + out)
		return out, &cmdError{Cmd: name, Code: code, Output: combined, Err: err}
	}
	return out, nil
}

func have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
