// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build linux

package sysnet

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitTerse(t *testing.T) {
	got := splitTerse(`wlp3s0:wifi:connected:Home\:WiFi\:5G`)
	want := []string{"wlp3s0", "wifi", "connected", "Home:WiFi:5G"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitTerse mismatch: got %v, want %v", got, want)
	}
}

func TestParseIPRouteDefault(t *testing.T) {
	dev4, gw4 := parseIPRouteDefault("default via 192.168.1.1 dev eth0 proto dhcp src 192.168.1.42 metric 100")
	if dev4 != "eth0" || gw4 != "192.168.1.1" {
		t.Fatalf("unexpected IPv4 default route parse: dev=%q gw=%q", dev4, gw4)
	}
	dev6, gw6 := parseIPRouteDefault("default via fe80::1 dev wlan0 proto ra metric 600 pref medium")
	if dev6 != "wlan0" || gw6 != "fe80::1" {
		t.Fatalf("unexpected IPv6 default route parse: dev=%q gw=%q", dev6, gw6)
	}
}

func TestParseIPv4AddrOutput(t *testing.T) {
	sample := `1: lo    inet 127.0.0.1/8 scope host lo\       valid_lft forever preferred_lft forever
2: eth0    inet 192.168.0.50/24 brd 192.168.0.255 scope global dynamic eth0\       valid_lft 86000sec preferred_lft 86000sec
3: wlan0    inet 10.0.0.15/24 brd 10.0.0.255 scope global wlan0`
	got := parseIPv4AddrOutput(sample)
	if len(got["lo"]) != 0 {
		t.Fatalf("expected loopback filtered out, got %v", got["lo"])
	}
	if !reflect.DeepEqual(got["eth0"], []string{"192.168.0.50"}) {
		t.Fatalf("eth0 IPv4 mismatch: %v", got["eth0"])
	}
	if !reflect.DeepEqual(got["wlan0"], []string{"10.0.0.15"}) {
		t.Fatalf("wlan0 IPv4 mismatch: %v", got["wlan0"])
	}
}

func TestManualGuideLinux(t *testing.T) {
	nmGuide := manualGuide(&Adapter{Device: "eth0", Connection: "Wired connection 1"}, []string{"1.1.1.1", "1.0.0.1"})
	if !strings.Contains(nmGuide.Command, "nmcli connection modify") || !strings.Contains(nmGuide.Command, "Wired connection 1") {
		t.Fatalf("expected nmcli command when Connection set, got %q", nmGuide.Command)
	}

	resolvedGuide := manualGuide(&Adapter{Device: "eth0"}, []string{"1.1.1.1", "1.0.0.1"})
	if !strings.Contains(resolvedGuide.Command, "resolvectl dns 'eth0'") {
		t.Fatalf("expected resolvectl command when Connection is empty, got %q", resolvedGuide.Command)
	}
}
