// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package dns

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Category is a coarse, user-facing classification of a resolver.
type Category string

const (
	CategoryStandard Category = "Standard"
	CategoryPrivacy  Category = "Privacy"
	CategoryAdBlock  Category = "Ad-blocking"
	CategoryFamily   Category = "Family"
	CategorySystem   Category = "System"
	CategoryCustom   Category = "Custom"
)

// Source describes where a resolver entry came from.
type Source string

const (
	SourceBuiltin Source = "builtin"
	SourceSystem  Source = "system"
	SourceGateway Source = "gateway"
	SourceCustom  Source = "custom"
)

// Resolver describes a DNS provider and every endpoint Resolv knows for it.
type Resolver struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    Category `json:"category"`
	Source      Source   `json:"source"`
	Description string   `json:"description,omitempty"`
	IPv4        []string `json:"ipv4,omitempty"`
	IPv6        []string `json:"ipv6,omitempty"`
	DoH         string   `json:"doh,omitempty"` // RFC 8484 endpoint URL
	DoT         string   `json:"dot,omitempty"` // TLS server name (port 853)
	Deprecated  bool     `json:"deprecated,omitempty"`
}

// PrimaryAddr returns the address used for plain DNS (UDP/TCP 53) benchmarks.
func (r Resolver) PrimaryAddr() string {
	if len(r.IPv4) > 0 {
		return r.IPv4[0]
	}
	if len(r.IPv6) > 0 {
		return r.IPv6[0]
	}
	return ""
}

// Applicable reports whether the resolver can be configured as an OS-level
// DNS server (which requires at least one plain IP address).
func (r Resolver) Applicable() bool { return len(r.IPv4)+len(r.IPv6) > 0 && !r.Deprecated }

// Addresses returns the addresses to configure on the OS, IPv4 first.
func (r Resolver) Addresses(includeIPv6 bool) []string {
	out := append([]string{}, limit(r.IPv4, 2)...)
	if includeIPv6 || len(out) == 0 {
		out = append(out, limit(r.IPv6, 2)...)
	}
	return out
}

func limit(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

var builtin = []Resolver{
	{
		ID: "cloudflare", Name: "Cloudflare", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Fast anycast resolver with a strict no-logging policy.",
		IPv4:        []string{"1.1.1.1", "1.0.0.1"},
		IPv6:        []string{"2606:4700:4700::1111", "2606:4700:4700::1001"},
		DoH:         "https://cloudflare-dns.com/dns-query", DoT: "one.one.one.one",
	},
	{
		ID: "cloudflare-security", Name: "Cloudflare Malware", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Cloudflare 1.1.1.2 endpoint that blocks known malware and phishing domains.",
		IPv4:        []string{"1.1.1.2", "1.0.0.2"},
		IPv6:        []string{"2606:4700:4700::1112", "2606:4700:4700::1002"},
		DoH:         "https://security.cloudflare-dns.com/dns-query", DoT: "security.cloudflare-dns.com",
	},
	{
		ID: "cloudflare-family", Name: "Cloudflare Family", Category: CategoryFamily, Source: SourceBuiltin,
		Description: "Cloudflare 1.1.1.3 endpoint blocking both malware and adult content.",
		IPv4:        []string{"1.1.1.3", "1.0.0.3"},
		IPv6:        []string{"2606:4700:4700::1113", "2606:4700:4700::1003"},
		DoH:         "https://family.cloudflare-dns.com/dns-query", DoT: "family.cloudflare-dns.com",
	},
	{
		ID: "google", Name: "Google Public DNS", Category: CategoryStandard, Source: SourceBuiltin,
		Description: "Large global anycast network operated by Google.",
		IPv4:        []string{"8.8.8.8", "8.8.4.4"},
		IPv6:        []string{"2001:4860:4860::8888", "2001:4860:4860::8844"},
		DoH:         "https://dns.google/dns-query", DoT: "dns.google",
	},
	{
		ID: "quad9", Name: "Quad9", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Non-profit resolver with malware-domain blocking and DNSSEC.",
		IPv4:        []string{"9.9.9.9", "149.112.112.112"},
		IPv6:        []string{"2620:fe::fe", "2620:fe::9"},
		DoH:         "https://dns.quad9.net/dns-query", DoT: "dns.quad9.net",
	},
	{
		ID: "quad9-ecs", Name: "Quad9 ECS", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Quad9 malware blocking and DNSSEC with EDNS Client Subnet (ECS) enabled for faster CDN routing.",
		IPv4:        []string{"9.9.9.11", "149.112.112.11"},
		IPv6:        []string{"2620:fe::11", "2620:fe::fe:11"},
		DoH:         "https://dns11.quad9.net/dns-query", DoT: "dns11.quad9.net",
	},
	{
		ID: "adguard", Name: "AdGuard DNS", Category: CategoryAdBlock, Source: SourceBuiltin,
		Description: "Blocks ads, trackers and phishing at the DNS level.",
		IPv4:        []string{"94.140.14.14", "94.140.15.15"},
		IPv6:        []string{"2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff"},
		DoH:         "https://dns.adguard-dns.com/dns-query", DoT: "dns.adguard-dns.com",
	},
	{
		ID: "adguard-family", Name: "AdGuard Family", Category: CategoryFamily, Source: SourceBuiltin,
		Description: "Blocks ads, trackers, adult content and enforces SafeSearch.",
		IPv4:        []string{"94.140.14.15", "94.140.15.16"},
		IPv6:        []string{"2a10:50c0::bad1:ff", "2a10:50c0::bad2:ff"},
		DoH:         "https://family.adguard-dns.com/dns-query", DoT: "family.adguard-dns.com",
	},
	{
		ID: "nextdns", Name: "NextDNS", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Configurable privacy resolver (default, unconfigured profile).",
		IPv4:        []string{"45.90.28.0", "45.90.30.0"},
		IPv6:        []string{"2a07:a8c0::", "2a07:a8c1::"},
		DoH:         "https://dns.nextdns.io", DoT: "dns.nextdns.io",
	},
	{
		ID: "opendns", Name: "OpenDNS", Category: CategoryStandard, Source: SourceBuiltin,
		Description: "Cisco Umbrella public resolver with phishing protection.",
		IPv4:        []string{"208.67.222.222", "208.67.220.220"},
		IPv6:        []string{"2620:119:35::35", "2620:119:53::53"},
		DoH:         "https://doh.opendns.com/dns-query", DoT: "dns.opendns.com",
	},
	{
		ID: "opendns-family", Name: "OpenDNS FamilyShield", Category: CategoryFamily, Source: SourceBuiltin,
		Description: "Cisco OpenDNS family filter pre-configured to block adult content and phishing.",
		IPv4:        []string{"208.67.222.123", "208.67.220.123"},
		IPv6:        []string{"2620:119:35::123", "2620:119:53::123"},
		DoH:         "https://doh.familyshield.opendns.com/dns-query", DoT: "familyshield.opendns.com",
	},
	{
		ID: "controld", Name: "Control D", Category: CategoryStandard, Source: SourceBuiltin,
		Description: "Free unfiltered resolver by Windscribe's Control D.",
		IPv4:        []string{"76.76.2.0", "76.76.10.0"},
		IPv6:        []string{"2606:1a40::", "2606:1a40:1::"},
		DoH:         "https://freedns.controld.com/p0", DoT: "p0.freedns.controld.com",
	},
	{
		ID: "controld-adblock", Name: "Control D AdBlock", Category: CategoryAdBlock, Source: SourceBuiltin,
		Description: "Control D Free DNS filtering ads, trackers and malware.",
		IPv4:        []string{"76.76.2.2", "76.76.10.2"},
		IPv6:        []string{"2606:1a40::2", "2606:1a40:1::2"},
		DoH:         "https://freedns.controld.com/p2", DoT: "p2.freedns.controld.com",
	},
	{
		ID: "dns4eu-protective", Name: "DNS4EU Protective", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Official EU GDPR-compliant resolver blocking malware, phishing and threat domains.",
		IPv4:        []string{"86.54.11.1", "86.54.11.201"},
		IPv6:        []string{"2a13:1001::86:54:11:1", "2a13:1001::86:54:11:201"},
		DoH:         "https://protective.joindns4.eu/dns-query", DoT: "protective.joindns4.eu",
	},
	{
		ID: "dns4eu-unfiltered", Name: "DNS4EU Unfiltered", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Official EU GDPR-compliant anycast resolver with zero filtering and strict no-logging.",
		IPv4:        []string{"86.54.11.100", "86.54.11.200"},
		IPv6:        []string{"2a13:1001::86:54:11:100", "2a13:1001::86:54:11:200"},
		DoH:         "https://unfiltered.joindns4.eu/dns-query", DoT: "unfiltered.joindns4.eu",
	},
	{
		ID: "dns4eu-adblock", Name: "DNS4EU AdBlock", Category: CategoryAdBlock, Source: SourceBuiltin,
		Description: "Official EU GDPR-compliant resolver filtering ads, trackers and malicious domains.",
		IPv4:        []string{"86.54.11.13", "86.54.11.213"},
		IPv6:        []string{"2a13:1001::86:54:11:13", "2a13:1001::86:54:11:213"},
		DoH:         "https://noads.joindns4.eu/dns-query", DoT: "noads.joindns4.eu",
	},
	{
		ID: "dns-sb", Name: "DNS.SB", Category: CategoryPrivacy, Source: SourceBuiltin,
		Description: "Privacy-first global anycast resolver with DNSSEC and zero query logging.",
		IPv4:        []string{"185.222.222.222", "45.11.45.11"},
		IPv6:        []string{"2a09::", "2a11::"},
		DoH:         "https://doh.sb/dns-query", DoT: "dot.sb",
	},
	{
		ID: "cleanbrowsing", Name: "CleanBrowsing Family", Category: CategoryFamily, Source: SourceBuiltin,
		Description: "Family filter: blocks adult content and enforces SafeSearch.",
		IPv4:        []string{"185.228.168.168", "185.228.169.168"},
		IPv6:        []string{"2a0d:2a00:1::", "2a0d:2a00:2::"},
		DoH:         "https://doh.cleanbrowsing.org/doh/family-filter/", DoT: "family-filter-dns.cleanbrowsing.org",
	},
}

// Builtin returns a copy of the curated resolver list.
func Builtin() []Resolver {
	out := make([]Resolver, len(builtin))
	for i, r := range builtin {
		r.IPv4 = append([]string(nil), r.IPv4...)
		r.IPv6 = append([]string(nil), r.IPv6...)
		out[i] = r
	}
	return out
}

// FromAddresses builds a resolver from plain IP addresses (used for the
// auto-detected system and gateway resolvers).
func FromAddresses(id, name string, cat Category, src Source, addrs []string) (Resolver, bool) {
	r := Resolver{ID: id, Name: name, Category: cat, Source: src}
	for _, a := range addrs {
		ip := net.ParseIP(strings.TrimSpace(a))
		if ip == nil || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || isSiteLocalV6(ip) {
			continue
		}
		if ip.To4() != nil {
			r.IPv4 = appendUnique(r.IPv4, ip.String())
		} else {
			r.IPv6 = appendUnique(r.IPv6, ip.String())
		}
	}
	return r, len(r.IPv4)+len(r.IPv6) > 0
}

func isSiteLocalV6(ip net.IP) bool {
	return ip.To4() == nil && len(ip) == net.IPv6len && ip[0] == 0xfe && ip[1]&0xc0 == 0xc0
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// ParseCustom parses user input into a resolver. Input may contain one or more
// tokens separated by commas or whitespace; each token is an IPv4 address,
// an IPv6 address, an https:// DoH URL or a tls:// DoT host name.
func ParseCustom(name, input string) (Resolver, error) {
	r := Resolver{Category: CategoryCustom, Source: SourceCustom}
	tokens := strings.FieldsFunc(input, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' || c == '\n' || c == ';' })
	if len(tokens) == 0 {
		return r, errors.New("enter an IPv4/IPv6 address, a DoH URL (https://…) or a DoT host (tls://…)")
	}
	for _, t := range tokens {
		switch {
		case strings.HasPrefix(strings.ToLower(t), "https://"):
			u, err := url.Parse(t)
			if err != nil || u.Host == "" {
				return r, fmt.Errorf("invalid DoH URL %q", t)
			}
			r.DoH = u.String()
		case strings.HasPrefix(strings.ToLower(t), "tls://"):
			host := strings.TrimSuffix(strings.TrimPrefix(t[len("tls://"):], "/"), ":853")
			if host == "" || strings.ContainsAny(host, "/ ") {
				return r, fmt.Errorf("invalid DoT host %q", t)
			}
			r.DoT = host
		default:
			ip := net.ParseIP(strings.Trim(t, "[]"))
			if ip == nil {
				return r, fmt.Errorf("%q is not a valid IP address, DoH URL or tls:// host", t)
			}
			if ip.To4() != nil {
				r.IPv4 = appendUnique(r.IPv4, ip.String())
			} else {
				r.IPv6 = appendUnique(r.IPv6, ip.String())
			}
		}
	}
	// A DoT endpoint without an IP needs the host for dialing; with an IP we
	// dial the IP and use the host for SNI.
	name = strings.TrimSpace(name)
	if name == "" {
		name = tokens[0]
	}
	if len(name) > 48 {
		name = name[:48]
	}
	r.Name = name
	sum := sha1.Sum([]byte(strings.Join(tokens, "|")))
	r.ID = "custom-" + hex.EncodeToString(sum[:])[:10]
	return r, nil
}
