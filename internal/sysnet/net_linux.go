// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build linux

package sysnet

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

const flushScript = "(resolvectl flush-caches 2>/dev/null || systemd-resolve --flush-caches 2>/dev/null || true)"

func detect(ctx context.Context) (*Info, error) {
	info := &Info{OSName: osRelease()}
	defDev, gateway := defaultRoute(ctx)
	info.Gateway = gateway

	useNM := have("nmcli") && nmRunning(ctx)
	switch {
	case useNM:
		info.Backend = "nmcli"
	case have("resolvectl"):
		info.Backend = "resolvectl"
	default:
		info.Backend = "none"
		info.Warnings = append(info.Warnings, "Neither NetworkManager (nmcli) nor systemd-resolved (resolvectl) was found; DNS switching is unavailable — use “Copy DNS Addresses”.")
	}

	if useNM {
		out, err := run(ctx, "nmcli", "-t", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status")
		if err == nil {
			for _, line := range strings.Split(out, "\n") {
				f := splitTerse(line)
				if len(f) < 4 {
					continue
				}
				dev, typ, state, conn := f[0], f[1], f[2], f[3]
				if !strings.HasPrefix(state, "connected") || conn == "" {
					continue
				}
				switch typ {
				case "loopback", "bridge", "tun", "veth", "dummy", "wifi-p2p", "macvlan":
					continue
				}
				iface := Adapter{
					ID: dev, Name: conn, Device: dev, Connection: conn,
					Kind: nmKind(typ),
				}
				iface.DNS = nmDeviceDNS(ctx, dev)
				iface.DNSManual = nmConnManual(ctx, conn)
				if dev == defDev {
					iface.Gateway = gateway
				}
				info.Interfaces = append(info.Interfaces, iface)
			}
		}
	}

	if len(info.Interfaces) == 0 && defDev != "" {
		iface := Adapter{ID: defDev, Name: defDev, Device: defDev, Gateway: gateway}
		if _, err := os.Stat("/sys/class/net/" + defDev + "/wireless"); err == nil {
			iface.Kind = "wifi"
		}
		iface.DNS = resolvectlLinkDNS(ctx, defDev)
		info.Interfaces = append(info.Interfaces, iface)
	}

	for i := range info.Interfaces {
		if info.Interfaces[i].Device == defDev {
			a := info.Interfaces[i]
			info.Active = &a
			break
		}
	}
	if info.Active == nil && len(info.Interfaces) > 0 {
		a := info.Interfaces[0]
		info.Active = &a
	}

	// System DNS: real upstream servers, never the 127.0.0.53 stub.
	if info.Active != nil {
		info.SystemDNS = nonLoopback(info.Active.DNS)
	}
	if len(info.SystemDNS) == 0 && info.Active != nil {
		info.SystemDNS = nonLoopback(resolvectlLinkDNS(ctx, info.Active.Device))
	}
	if len(info.SystemDNS) == 0 {
		info.SystemDNS = nonLoopback(resolvectlGlobalDNS(ctx))
	}
	if len(info.SystemDNS) == 0 {
		info.SystemDNS = nonLoopback(resolvConf("/etc/resolv.conf"))
	}
	if info.Active != nil && len(info.Active.DNS) == 0 {
		info.Active.DNS = info.SystemDNS
	}
	if info.Active == nil {
		return info, ErrNoInterface
	}
	return info, nil
}

func osRelease() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return "Linux"
}

// defaultRoute reads /proc/net/route (no external tools needed), falling
// back to `ip route`.
func defaultRoute(ctx context.Context) (dev, gw string) {
	if f, err := os.Open("/proc/net/route"); err == nil {
		defer f.Close()
		best := int64(-1)
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) < 8 || fl[1] != "00000000" || fl[7] != "00000000" {
				continue
			}
			metric, _ := strconv.ParseInt(fl[6], 10, 64)
			if best >= 0 && metric >= best {
				continue
			}
			v, err := strconv.ParseUint(fl[2], 16, 32)
			if err != nil {
				continue
			}
			best = metric
			dev = fl[0]
			gw = net.IPv4(byte(v), byte(v>>8), byte(v>>16), byte(v>>24)).String()
		}
		if dev != "" {
			return dev, gw
		}
	}
	out, err := run(ctx, "ip", "-4", "route", "show", "default")
	if err != nil {
		return "", ""
	}
	f := strings.Fields(out)
	for i := 0; i+1 < len(f); i++ {
		switch f[i] {
		case "via":
			if gw == "" {
				gw = f[i+1]
			}
		case "dev":
			if dev == "" {
				dev = f[i+1]
			}
		}
	}
	return dev, gw
}

func nmRunning(ctx context.Context) bool {
	out, err := run(ctx, "nmcli", "-t", "-f", "RUNNING", "general")
	return err == nil && strings.TrimSpace(out) == "running"
}

func nmKind(t string) string {
	switch t {
	case "wifi":
		return "wifi"
	case "ethernet":
		return "ethernet"
	case "gsm", "cdma", "modem":
		return "cellular"
	}
	return "other"
}

// splitTerse splits nmcli -t output on unescaped ':' and unescapes values.
func splitTerse(line string) []string {
	var out []string
	var b strings.Builder
	esc := false
	for _, c := range line {
		switch {
		case esc:
			b.WriteRune(c)
			esc = false
		case c == '\\':
			esc = true
		case c == ':':
			out = append(out, b.String())
			b.Reset()
		default:
			b.WriteRune(c)
		}
	}
	return append(out, b.String())
}

func nmDeviceDNS(ctx context.Context, dev string) []string {
	out, err := run(ctx, "nmcli", "-t", "-f", "IP4.DNS,IP6.DNS", "device", "show", dev)
	if err != nil {
		return nil
	}
	var dns []string
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) >= 2 && strings.Contains(f[0], ".DNS") {
			dns = append(dns, strings.Join(f[1:], ":"))
		}
	}
	return dns
}

func nmConnManual(ctx context.Context, conn string) bool {
	out, err := run(ctx, "nmcli", "-t", "-f", "ipv4.dns,ipv4.ignore-auto-dns", "connection", "show", "id", conn)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		f := splitTerse(line)
		if len(f) < 2 {
			continue
		}
		v := strings.TrimSpace(strings.Join(f[1:], ":"))
		if (f[0] == "ipv4.dns" && v != "" && v != "--") || (f[0] == "ipv4.ignore-auto-dns" && v == "yes") {
			return true
		}
	}
	return false
}

func resolvectlLinkDNS(ctx context.Context, dev string) []string {
	if dev == "" || !have("resolvectl") {
		return nil
	}
	out, err := run(ctx, "resolvectl", "dns", dev)
	if err != nil {
		return nil
	}
	if _, after, ok := strings.Cut(out, "):"); ok {
		return strings.Fields(after)
	}
	return nil
}

func resolvectlGlobalDNS(ctx context.Context) []string {
	if !have("resolvectl") {
		return nil
	}
	out, err := run(ctx, "resolvectl", "status")
	if err != nil {
		return nil
	}
	var dns []string
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "Current DNS Server:"); ok {
			dns = append(dns, strings.Fields(v)...)
		}
	}
	return dns
}

func nonLoopback(in []string) []string {
	var out []string
	for _, s := range cleanIPs(in) {
		if ip := net.ParseIP(s); ip != nil && !ip.IsLoopback() {
			out = append(out, s)
		}
	}
	return out
}

// runPrivileged tries the script as the current user first (polkit often
// allows NetworkManager changes for the active session) and falls back to a
// pkexec authentication prompt.
func runPrivileged(ctx context.Context, script string) error {
	_, err := run(ctx, "/bin/sh", "-c", script)
	if err == nil || isElevated() {
		return err
	}
	if !have("pkexec") {
		return errors.Join(ErrNotPermitted, err)
	}
	_, perr := run(ctx, "pkexec", "/bin/sh", "-c", script)
	switch {
	case perr == nil:
		return nil
	case exitCode(perr) == 126:
		return ErrElevationCancelled
	case exitCode(perr) == 127:
		return errors.Join(ErrNotPermitted, perr)
	}
	return perr
}

func applyDNS(ctx context.Context, iface *Adapter, servers []string) error {
	v4, v6 := splitFamilies(servers)
	if iface.Connection != "" && have("nmcli") {
		args := []string{"nmcli", "connection", "modify", "id", shellQuote(iface.Connection)}
		if len(v4) > 0 {
			args = append(args, "ipv4.dns", shellQuote(strings.Join(v4, " ")), "ipv4.ignore-auto-dns", "yes")
		}
		if len(v6) > 0 {
			args = append(args, "ipv6.dns", shellQuote(strings.Join(v6, " ")), "ipv6.ignore-auto-dns", "yes")
		}
		script := fmt.Sprintf("%s && (nmcli device reapply %s || nmcli connection up id %s) && %s",
			strings.Join(args, " "), shellQuote(iface.Device), shellQuote(iface.Connection), flushScript)
		return runPrivileged(ctx, script)
	}
	if have("resolvectl") && iface.Device != "" {
		script := fmt.Sprintf("resolvectl dns %s %s && resolvectl domain %s '~.' && %s",
			shellQuote(iface.Device), strings.Join(servers, " "), shellQuote(iface.Device), flushScript)
		return runPrivileged(ctx, script)
	}
	return ErrUnsupported
}

func resetDNS(ctx context.Context, iface *Adapter) error {
	if iface.Connection != "" && have("nmcli") {
		script := fmt.Sprintf("nmcli connection modify id %s ipv4.dns '' ipv4.ignore-auto-dns no ipv6.dns '' ipv6.ignore-auto-dns no && (nmcli device reapply %s || nmcli connection up id %s) && %s",
			shellQuote(iface.Connection), shellQuote(iface.Device), shellQuote(iface.Connection), flushScript)
		return runPrivileged(ctx, script)
	}
	if have("resolvectl") && iface.Device != "" {
		return runPrivileged(ctx, fmt.Sprintf("resolvectl revert %s && %s", shellQuote(iface.Device), flushScript))
	}
	return ErrUnsupported
}

func flushCache(ctx context.Context) error {
	if !have("resolvectl") && !have("systemd-resolve") {
		return nil // no local caching resolver to flush
	}
	return runPrivileged(ctx, "resolvectl flush-caches 2>/dev/null || systemd-resolve --flush-caches")
}

func manualGuide(iface *Adapter, servers []string) ManualGuide {
	conn, dev := "<connection>", "<device>"
	if iface != nil {
		if iface.Connection != "" {
			conn = iface.Connection
		}
		dev = iface.Device
	}
	v4, v6 := splitFamilies(servers)
	cmd := fmt.Sprintf("nmcli connection modify id %s ipv4.dns %s ipv4.ignore-auto-dns yes", shellQuote(conn), shellQuote(strings.Join(v4, " ")))
	if len(v6) > 0 {
		cmd += fmt.Sprintf(" ipv6.dns %s ipv6.ignore-auto-dns yes", shellQuote(strings.Join(v6, " ")))
	}
	cmd += fmt.Sprintf(" && nmcli connection up id %s && resolvectl flush-caches", shellQuote(conn))
	return ManualGuide{
		Steps: []string{
			"Open your desktop's network settings (GNOME Settings → Network / KDE System Settings → Connections).",
			"Edit the active connection “" + conn + "” (" + dev + ") and open the IPv4 / IPv6 tab.",
			"Turn off automatic DNS and enter the addresses below, separated by commas.",
			"Apply, then reconnect. Alternatively run the terminal command below.",
		},
		Command: cmd,
	}
}
