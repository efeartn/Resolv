// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package dns

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	mdns "github.com/miekg/dns"
)

// HijackStatus is the verdict of the tampering checks for one resolver.
type HijackStatus string

const (
	HijackClean      HijackStatus = "clean"
	HijackSuspicious HijackStatus = "suspicious"
	HijackDetected   HijackStatus = "hijacked"
	HijackUnknown    HijackStatus = "unknown"
)

// HijackResult carries the evidence behind a HijackStatus.
type HijackResult struct {
	Status    HijackStatus `json:"status"`
	Findings  []string     `json:"findings,omitempty"`
	UDPEgress string       `json:"udpEgress,omitempty"` // resolver IP seen by an authoritative server
	DoHEgress string       `json:"dohEgress,omitempty"`
}

// FilteringResult reports whether a resolver blocks known ad and tracker domains.
type FilteringResult struct {
	Checked      bool     `json:"checked"`
	BlocksAds    bool     `json:"blocksAds"`
	BlockedCount int      `json:"blockedCount"`
	TestedCount  int      `json:"testedCount"`
	BlockedHosts []string `json:"blockedHosts,omitempty"`
	Method       string   `json:"method,omitempty"` // sinkhole-ip | nxdomain | refused | nodata | none
}

// NetworkIntegrity summarises network-wide interception findings.
type NetworkIntegrity struct {
	Checked       bool   `json:"checked"`
	Intercepted   bool   `json:"intercepted"`   // port 53 is transparently proxied
	ProbeTarget   string `json:"probeTarget"`   // address of the canary probe
	HijackedCount int    `json:"hijackedCount"` // resolvers flagged as hijacked
	Summary       string `json:"summary"`
}

const (
	// A query sent to a TEST-NET-1 address (RFC 5737) must never be
	// answered. If it is, something on the path intercepts port 53.
	canaryAddr = "192.0.2.53"
	// Akamai's authoritative servers answer this name with the IP address
	// of the recursive resolver that asked — i.e. the resolver's identity.
	egressName    = "whoami.akamai.net"
	dnssecSigned  = "cloudflare.com"
	dnssecBroken  = "dnssec-failed.org"
	redirectProbe = "example.com"
)

// Known ad-serving & telemetry domains used for the Privacy & Filtering Probe.
var adCanaryDomains = []string{
	"doubleclick.net",
	"pagead2.googlesyndication.com",
	"app-measurement.com",
}

type integrityProbe struct {
	udp querier
	doh querier
}

func (p integrityProbe) close() {
	if p.udp != nil {
		p.udp.close()
	}
	if p.doh != nil {
		p.doh.close()
	}
}

func checkIntegrity(ctx context.Context, servers []*serverState, results []*ServerResult, opts Options, progress ProgressFunc) NetworkIntegrity {
	timeout := opts.timeout()
	transport := newDoHTransport(timeout)
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}

	total := len(servers) + 1
	var done int64
	step := func(msg string) {
		n := atomic.AddInt64(&done, 1)
		progress(Progress{Phase: "integrity", Done: int(n), Total: total, Message: msg})
	}
	progress(Progress{Phase: "integrity", Done: 0, Total: total, Message: "Running DNSSEC, Ad-Block, and Hijack probes"})

	ni := NetworkIntegrity{Checked: true, ProbeTarget: canaryAddr + ":53"}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ni.Intercepted = canaryAnswered(ctx, timeout)
		step("Probing for transparent DNS proxies")
	}()

	sem := make(chan struct{}, maxInt(2, opts.Workers/2))
	for i := range servers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			r := servers[i].resolver
			if results[i].Status == "skipped" {
				step("Skipped " + r.Name)
				return
			}
			p := integrityProbe{}
			if q, err := newQuerier(r, ProtoUDP, timeout, client); err == nil {
				p.udp = q
			}
			if q, err := newQuerier(r, ProtoDoH, timeout, client); err == nil {
				p.doh = q
			}
			defer p.close()

			results[i].DNSSEC = checkDNSSEC(ctx, p, timeout)
			results[i].Filtering = checkFiltering(ctx, p, timeout)
			results[i].Hijack = checkHijack(ctx, r, p, timeout)
			step("Auditing " + r.Name)
		}(i)
	}
	wg.Wait()

	crossProviderCheck(results)

	for _, res := range results {
		if ni.Intercepted && res.Resolver.PrimaryAddr() != "" && res.Resolver.Source != SourceSystem && res.Resolver.Source != SourceGateway {
			res.Hijack.Status = HijackDetected
			res.Hijack.Findings = append(res.Hijack.Findings,
				fmt.Sprintf("Port 53 is intercepted on this network: the unroutable test address %s answered a DNS query.", canaryAddr))
		}
		if res.Hijack.Status == HijackDetected {
			ni.HijackedCount++
		}
	}
	switch {
	case ni.Intercepted:
		ni.Summary = "Your network or ISP transparently intercepts plain DNS (port 53). Queries to any DNS server are answered by a proxy resolver. Use DoH/DoT to bypass it."
	case ni.HijackedCount > 0:
		ni.Summary = fmt.Sprintf("%d resolver(s) returned tampered or redirected answers over plain DNS. Encrypted DNS (DoH/DoT) is unaffected.", ni.HijackedCount)
	default:
		ni.Summary = "No DNS interception or tampering detected."
	}
	return ni
}

func canaryAnswered(ctx context.Context, timeout time.Duration) bool {
	if timeout > 1500*time.Millisecond {
		timeout = 1500 * time.Millisecond
	}
	q := &udpQuerier{addr: net.JoinHostPort(canaryAddr, "53"), timeout: timeout}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, _, err := q.exchange(qctx, newQuery(redirectProbe, mdns.TypeA, false))
	return err == nil && r != nil
}

func ask(ctx context.Context, q querier, name string, qtype uint16, dnssec bool, timeout time.Duration) (*mdns.Msg, error) {
	if q == nil {
		return nil, errNoEndpoint
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r, _, err := q.exchange(qctx, newQuery(name, qtype, dnssec))
	return r, err
}

// checkDNSSEC: a validating resolver sets AD on signed data and returns
// SERVFAIL for a deliberately broken signature.
func checkDNSSEC(ctx context.Context, p integrityProbe, timeout time.Duration) string {
	q := p.udp
	if q == nil {
		q = p.doh
	}
	if q == nil {
		return "unknown"
	}
	good, err1 := ask(ctx, q, dnssecSigned, mdns.TypeA, true, timeout)
	bad, err2 := ask(ctx, q, dnssecBroken, mdns.TypeA, true, timeout)
	if err1 != nil || err2 != nil {
		return "unknown"
	}
	ad := good.AuthenticatedData
	rejects := bad.Rcode == mdns.RcodeServerFailure
	switch {
	case ad && rejects:
		return "validating"
	case !ad && !rejects:
		return "not-validating"
	case rejects:
		return "validating" // validates but does not echo AD
	default:
		return "partial"
	}
}

// checkFiltering probes known ad/tracker domains to determine whether the
// resolver actively sinkholes or refuses advertising and telemetry hosts.
func checkFiltering(ctx context.Context, p integrityProbe, timeout time.Duration) FilteringResult {
	q := p.udp
	if q == nil {
		q = p.doh
	}
	if q == nil {
		return FilteringResult{Method: "unknown"}
	}
	res := FilteringResult{
		Checked:     true,
		TestedCount: len(adCanaryDomains),
		Method:      "none",
	}
	for _, host := range adCanaryDomains {
		m, err := ask(ctx, q, host, mdns.TypeA, false, timeout)
		if err != nil || m == nil {
			continue
		}
		blocked, method := isSinkholed(m)
		if blocked {
			res.BlockedCount++
			res.BlockedHosts = append(res.BlockedHosts, host)
			if res.Method == "none" {
				res.Method = method
			}
		}
	}
	res.BlocksAds = res.BlockedCount > 0
	return res
}

// isSinkholed inspects a DNS response to see if an ad/tracker domain was blocked.
func isSinkholed(m *mdns.Msg) (bool, string) {
	if m == nil {
		return false, ""
	}
	switch m.Rcode {
	case mdns.RcodeNameError:
		return true, "nxdomain"
	case mdns.RcodeRefused:
		return true, "refused"
	case mdns.RcodeSuccess:
		ips := aRecords(m)
		if len(ips) == 0 && len(m.Answer) == 0 {
			return true, "nodata"
		}
		for _, s := range ips {
			ip := net.ParseIP(s)
			if ip != nil && (ip.IsUnspecified() || ip.IsLoopback()) {
				return true, "sinkhole-ip"
			}
		}
	}
	return false, ""
}

func checkHijack(ctx context.Context, _ Resolver, p integrityProbe, timeout time.Duration) HijackResult {
	res := HijackResult{Status: HijackUnknown}

	if p.udp != nil {
		if m, err := ask(ctx, p.udp, egressName, mdns.TypeA, false, timeout); err == nil {
			res.UDPEgress = firstA(m)
		}
	}
	if p.doh != nil {
		if m, err := ask(ctx, p.doh, egressName, mdns.TypeA, false, timeout); err == nil {
			res.DoHEgress = firstA(m)
		}
	}
	if p.udp == nil || p.doh == nil {
		if p.udp != nil {
			res.Findings = append(res.Findings, "No encrypted endpoint to compare against; only network-wide checks apply.")
		}
		return res
	}

	res.Status = HijackClean

	// 1. NXDOMAIN rewriting: a random name must not resolve.
	ghost := "resolv-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16] + ".com"
	u, errU := ask(ctx, p.udp, ghost, mdns.TypeA, false, timeout)
	d, errD := ask(ctx, p.doh, ghost, mdns.TypeA, false, timeout)
	if errU == nil && errD == nil {
		if d.Rcode == mdns.RcodeNameError && u.Rcode == mdns.RcodeSuccess && len(aRecords(u)) > 0 {
			res.Status = HijackDetected
			res.Findings = append(res.Findings, fmt.Sprintf(
				"NXDOMAIN tampering: plain DNS resolved a non-existent domain to %s while DoH correctly returned NXDOMAIN.",
				strings.Join(aRecords(u), ", ")))
		}
	}

	// 2. Redirection to private/bogon addresses (block pages, captive portals).
	u, errU = ask(ctx, p.udp, redirectProbe, mdns.TypeA, false, timeout)
	d, errD = ask(ctx, p.doh, redirectProbe, mdns.TypeA, false, timeout)
	if errU == nil && errD == nil {
		ua, da := aRecords(u), aRecords(d)
		if anyBogon(ua) && len(da) > 0 && !anyBogon(da) {
			res.Status = HijackDetected
			res.Findings = append(res.Findings, fmt.Sprintf(
				"Redirection: plain DNS answered %s with private address %s (DoH: %s).",
				redirectProbe, strings.Join(ua, ", "), strings.Join(da, ", ")))
		}
	}

	// 3. Resolver identity: informational comparison of egress networks.
	if res.UDPEgress != "" && res.DoHEgress != "" && prefix(res.UDPEgress, 16) != prefix(res.DoHEgress, 16) {
		res.Findings = append(res.Findings, fmt.Sprintf(
			"Plain DNS and DoH were resolved from different networks (%s vs %s). This can be normal for large anycast providers.",
			res.UDPEgress, res.DoHEgress))
	}
	return res
}

// crossProviderCheck flags transparent proxies: if two *different* providers
// are answered by the same upstream resolver over plain DNS, something on the
// path is redirecting port 53 traffic.
func crossProviderCheck(results []*ServerResult) {
	byNet := map[string][]*ServerResult{}
	for _, r := range results {
		if r.Hijack.UDPEgress == "" {
			continue
		}
		key := prefix(r.Hijack.UDPEgress, 24)
		byNet[key] = append(byNet[key], r)
	}
	for key, group := range byNet {
		if len(group) < 2 {
			continue
		}
		for _, r := range group {
			if r.Resolver.Source == SourceSystem || r.Resolver.Source == SourceGateway {
				continue
			}
			var others []string
			for _, o := range group {
				if o == r || sameAddress(o.Resolver, r.Resolver) {
					continue
				}
				others = append(others, o.Resolver.Name)
			}
			if len(others) == 0 {
				continue
			}
			if r.Hijack.DoHEgress != "" && prefix(r.Hijack.DoHEgress, 24) == key {
				continue
			}
			if r.Hijack.DoHEgress != "" {
				r.Hijack.Status = HijackDetected
			} else if r.Hijack.Status != HijackDetected {
				r.Hijack.Status = HijackSuspicious
			}
			r.Hijack.Findings = append(r.Hijack.Findings, fmt.Sprintf(
				"Transparent proxy: plain DNS queries to %s were answered by the same resolver (%s) as %s.",
				r.Resolver.Name, r.Hijack.UDPEgress, strings.Join(others, ", ")))
		}
	}
}

func aRecords(m *mdns.Msg) []string {
	var out []string
	if m == nil {
		return out
	}
	for _, rr := range m.Answer {
		if a, ok := rr.(*mdns.A); ok {
			out = append(out, a.A.String())
		}
	}
	return out
}

func firstA(m *mdns.Msg) string {
	if a := aRecords(m); len(a) > 0 {
		return a[0]
	}
	return ""
}

var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}
var zeroNet = &net.IPNet{IP: net.IPv4(0, 0, 0, 0), Mask: net.CIDRMask(8, 32)}

func anyBogon(ips []string) bool {
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || cgnat.Contains(ip) || zeroNet.Contains(ip) {
			return true
		}
	}
	return false
}

func prefix(ip string, bits int) string {
	p := net.ParseIP(ip)
	if p == nil {
		return ip
	}
	if v4 := p.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(bits, 32)).String()
	}
	return p.Mask(net.CIDRMask(bits*2, 128)).String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
