// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build darwin

package sysnet

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"regexp"
	"strings"
)

const (
	networksetup = "/usr/sbin/networksetup"
	routeBin     = "/sbin/route"
	scutilBin    = "/usr/sbin/scutil"
	swVers       = "/usr/bin/sw_vers"
	osascript    = "/usr/bin/osascript"
	flushCmd     = "/usr/bin/dscacheutil -flushcache; /usr/bin/killall -HUP mDNSResponder"
)

var (
	reServiceLine = regexp.MustCompile(`^\((\d+|\*)\)\s+(.+)$`)
	reHardware    = regexp.MustCompile(`^\([^:]+:\s*(.*?),\s*[^:]+:\s*([A-Za-z0-9_.-]+)\)$`)
	reNameserver  = regexp.MustCompile(`^\s*nameserver\[\d+\]\s*:\s*(\S+)`)
)

type macService struct {
	name, port, device string
	enabled            bool
}

func defaultRouteDarwin(ctx context.Context) (defDev, gateway string) {
	parseRoute := func(out string) (dev, gw string) {
		for _, line := range strings.Split(out, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "interface":
				dev = strings.TrimSpace(v)
			case "gateway":
				gw = strings.TrimSpace(v)
			}
		}
		return dev, gw
	}
	if out, err := run(ctx, routeBin, "-n", "get", "default"); err == nil {
		defDev, gateway = parseRoute(out)
	}
	if defDev == "" {
		if out, err := run(ctx, routeBin, "-n", "get", "-inet6", "default"); err == nil {
			defDev, gateway = parseRoute(out)
		}
	}
	return defDev, gateway
}

func parseMacServiceOrder(out string) []macService {
	var services []macService
	var cur *macService
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := reServiceLine.FindStringSubmatch(line); m != nil {
			services = append(services, macService{name: m[2], enabled: m[1] != "*"})
			cur = &services[len(services)-1]
			continue
		}
		if m := reHardware.FindStringSubmatch(line); m != nil && cur != nil {
			cur.port, cur.device = strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		}
	}
	return services
}

func parseMacDNSServers(out string) []string {
	return cleanIPs(strings.Fields(out))
}

func detect(ctx context.Context) (*Info, error) {
	info := &Info{Backend: "networksetup"}

	name, _ := run(ctx, swVers, "-productName")
	ver, _ := run(ctx, swVers, "-productVersion")
	info.OSName = strings.TrimSpace(strings.TrimSpace(name) + " " + strings.TrimSpace(ver))

	defDev, gateway := defaultRouteDarwin(ctx)

	// Enabled services (networksetup -listallnetworkservices marks disabled with '*').
	enabled := map[string]bool{}
	if out, err := run(ctx, networksetup, "-listallnetworkservices"); err == nil {
		for i, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if i == 0 || line == "" { // first line is an explanatory header
				continue
			}
			if strings.HasPrefix(line, "*") {
				enabled[strings.TrimSpace(line[1:])] = false
			} else {
				enabled[line] = true
			}
		}
	}

	// Service → hardware port/device mapping.
	out, err := run(ctx, networksetup, "-listnetworkserviceorder")
	if err != nil {
		return info, fmt.Errorf("networksetup failed: %w", err)
	}
	services := parseMacServiceOrder(out)

	for _, s := range services {
		if !s.enabled || (len(enabled) > 0 && !enabled[s.name]) || s.device == "" {
			continue
		}
		ni, err := net.InterfaceByName(s.device)
		if err != nil || ni.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ni.Addrs()
		if len(addrs) == 0 {
			continue
		}
		iface := Adapter{
			ID:     s.name,
			Name:   s.name,
			Device: s.device,
			Kind:   guessKind(s.port),
			MAC:    ni.HardwareAddr.String(),
		}
		if s.device == defDev {
			iface.Gateway = gateway
		}
		if dnsOut, err := run(ctx, networksetup, "-getdnsservers", s.name); err == nil {
			if ips := parseMacDNSServers(dnsOut); len(ips) > 0 {
				iface.DNSManual = true
				iface.DNS = ips
			}
		}
		info.Interfaces = append(info.Interfaces, iface)
	}

	info.SystemDNS = scutilDNS(ctx)
	info.Gateway = gateway

	for i := range info.Interfaces {
		if info.Interfaces[i].Device == defDev {
			info.Active = &info.Interfaces[i]
			break
		}
	}
	if info.Active == nil && len(info.Interfaces) > 0 {
		info.Active = &info.Interfaces[0]
	}
	if info.Active == nil {
		return info, ErrNoInterface
	}
	if len(info.Active.DNS) == 0 {
		info.Active.DNS = info.SystemDNS
	}
	// Return a copy so Interfaces and Active don't alias.
	a := *info.Active
	info.Active = &a
	return info, nil
}

// scutilDNS returns the nameservers of the primary (non-scoped) resolver.
func scutilDNS(ctx context.Context) []string {
	out, err := run(ctx, scutilBin, "--dns")
	if err == nil {
		var servers []string
		inResolver1 := false
		for _, line := range strings.Split(out, "\n") {
			t := strings.TrimSpace(line)
			if strings.HasPrefix(t, "DNS configuration (for scoped queries)") {
				break
			}
			if strings.HasPrefix(t, "resolver #") {
				if inResolver1 && len(servers) > 0 {
					break
				}
				inResolver1 = t == "resolver #1"
				continue
			}
			if inResolver1 {
				if m := reNameserver.FindStringSubmatch(line); m != nil {
					servers = append(servers, m[1])
				}
			}
		}
		if len(servers) > 0 {
			return servers
		}
	}
	return resolvConf("/etc/resolv.conf")
}

// appleScriptString escapes s for inclusion in an AppleScript string literal.
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// runPrivileged runs a shell script as root, prompting with the native macOS
// administrator dialog via osascript when needed.
func runPrivileged(ctx context.Context, script string) error {
	if isElevated() {
		_, err := run(ctx, "/bin/sh", "-c", script)
		return err
	}
	as := fmt.Sprintf(`do shell script %s with prompt %s with administrator privileges`,
		appleScriptString(script),
		appleScriptString("Resolv needs administrator access to change your DNS settings."))
	_, err := run(ctx, osascript, "-e", as)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "-128") || strings.Contains(strings.ToLower(msg), "user canceled") {
			return ErrElevationCancelled
		}
		return err
	}
	return nil
}

func applyDNS(ctx context.Context, iface *Adapter, servers []string) error {
	script := fmt.Sprintf("%s -setdnsservers %s %s && (%s; true)",
		networksetup, shellQuote(iface.ID), strings.Join(servers, " "), flushCmd)
	return runPrivileged(ctx, script)
}

func resetDNS(ctx context.Context, iface *Adapter) error {
	script := fmt.Sprintf("%s -setdnsservers %s Empty && (%s; true)",
		networksetup, shellQuote(iface.ID), flushCmd)
	return runPrivileged(ctx, script)
}

func flushCache(ctx context.Context) error {
	return runPrivileged(ctx, flushCmd)
}

func manualGuide(iface *Adapter, servers []string) ManualGuide {
	svc := "Wi-Fi"
	if iface != nil {
		svc = iface.ID
	}
	return ManualGuide{
		Steps: []string{
			"Open System Settings → Network and select “" + svc + "”.",
			"Click “Details…”, then open the “DNS” tab.",
			"Remove existing entries with “−” and add the addresses below with “+”.",
			"Click “OK”. Your DNS cache is cleared automatically on change.",
		},
		Command: fmt.Sprintf("sudo networksetup -setdnsservers %s %s && sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder",
			shellQuote(svc), strings.Join(servers, " ")),
	}
}
