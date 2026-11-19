// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build darwin

package sysnet

import (
	"reflect"
	"testing"
)

func TestParseMacServiceOrderLocalized(t *testing.T) {
	sample := `An asterisk (*) denotes that a network service is disabled.
(1) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(2) USB 10/100/1000 LAN
(Hardware-Anschluss: USB 10/100/1000 LAN, Gerät: en5)

(*) Thunderbolt Bridge
(Port matériel : Thunderbolt Bridge, Périphérique : bridge0)

(4) iPhone USB
(Donanım Kapısı: iPhone USB, Aygıt: en8)
`
	got := parseMacServiceOrder(sample)
	want := []macService{
		{name: "Wi-Fi", port: "Wi-Fi", device: "en0", enabled: true},
		{name: "USB 10/100/1000 LAN", port: "USB 10/100/1000 LAN", device: "en5", enabled: true},
		{name: "Thunderbolt Bridge", port: "Thunderbolt Bridge", device: "bridge0", enabled: false},
		{name: "iPhone USB", port: "iPhone USB", device: "en8", enabled: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMacServiceOrder mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseMacDNSServersLocalized(t *testing.T) {
	if ips := parseMacDNSServers("There aren't any DNS Servers set on Wi-Fi.\n"); len(ips) != 0 {
		t.Fatalf("expected empty IPs for English notice, got %v", ips)
	}
	if ips := parseMacDNSServers("Wi-Fi üzerinde ayarlı DNS Sunucusu yok."); len(ips) != 0 {
		t.Fatalf("expected empty IPs for Turkish notice, got %v", ips)
	}
	got := parseMacDNSServers("1.1.1.1\n2606:4700:4700::1111\n1.0.0.1\n")
	want := []string{"1.1.1.1", "2606:4700:4700::1111", "1.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMacDNSServers mismatch: got %v, want %v", got, want)
	}
}
