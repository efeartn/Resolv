// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package dns

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	mdns "github.com/miekg/dns"
)

func TestComputeStatsAndP95(t *testing.T) {
	st := computeStats([]float64{10, 20, 30, 40}, 5)
	if st.Sent != 5 || st.Received != 4 {
		t.Fatalf("unexpected counts: %+v", st)
	}
	if st.Avg != 25 || st.Min != 10 || st.Max != 40 || st.Median != 25 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if st.P95 != 38.5 {
		t.Fatalf("expected P95 38.5, got %.2f", st.P95)
	}
	if len(st.Samples) != 4 || st.Samples[0] != 10 || st.Samples[3] != 40 {
		t.Fatalf("unexpected sample series: %v", st.Samples)
	}
	if st.Loss != 20 {
		t.Fatalf("expected 20%% loss, got %.2f", st.Loss)
	}
	if st.Jitter <= 11.1 || st.Jitter >= 11.2 {
		t.Fatalf("expected jitter ~11.18, got %.2f", st.Jitter)
	}
}

func TestAdBlockSinkholeDetection(t *testing.T) {
	// 1. Sinkhole IP (0.0.0.0)
	mZero := new(mdns.Msg)
	mZero.Rcode = mdns.RcodeSuccess
	mZero.Answer = []mdns.RR{
		&mdns.A{
			Hdr: mdns.RR_Header{Name: "doubleclick.net.", Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
			A:   net.ParseIP("0.0.0.0"),
		},
	}
	if blocked, method := isSinkholed(mZero); !blocked || method != "sinkhole-ip" {
		t.Fatalf("expected 0.0.0.0 to be flagged as sinkhole-ip, got blocked=%v method=%s", blocked, method)
	}

	// 2. NXDOMAIN sinkhole
	mNX := new(mdns.Msg)
	mNX.Rcode = mdns.RcodeNameError
	if blocked, method := isSinkholed(mNX); !blocked || method != "nxdomain" {
		t.Fatalf("expected NXDOMAIN to be flagged, got blocked=%v method=%s", blocked, method)
	}

	// 3. Normal public IP (not blocked)
	mPub := new(mdns.Msg)
	mPub.Rcode = mdns.RcodeSuccess
	mPub.Answer = []mdns.RR{
		&mdns.A{
			Hdr: mdns.RR_Header{Name: "doubleclick.net.", Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
			A:   net.ParseIP("142.250.190.14"),
		},
	}
	if blocked, _ := isSinkholed(mPub); blocked {
		t.Fatalf("expected public IP not to be flagged as blocked")
	}
}

func TestParseCustom(t *testing.T) {
	r, err := ParseCustom("My Custom", "1.1.1.2, 2606:4700:4700::1112 https://security.cloudflare-dns.com/dns-query tls://security.cloudflare-dns.com")
	if err != nil {
		t.Fatalf("ParseCustom failed: %v", err)
	}
	if len(r.IPv4) != 1 || r.IPv4[0] != "1.1.1.2" {
		t.Fatalf("unexpected IPv4: %v", r.IPv4)
	}
	if len(r.IPv6) != 1 || r.IPv6[0] != "2606:4700:4700::1112" {
		t.Fatalf("unexpected IPv6: %v", r.IPv6)
	}
	if r.DoH == "" || r.DoT != "security.cloudflare-dns.com" {
		t.Fatalf("unexpected DoH/DoT: %+v", r)
	}
}

func TestDoHAndExport(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q := new(mdns.Msg)
		if err := q.Unpack(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := new(mdns.Msg)
		resp.SetReply(q)
		if len(q.Question) > 0 {
			rr := &mdns.A{
				Hdr: mdns.RR_Header{Name: q.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
				A:   net.ParseIP("93.184.216.34"),
			}
			resp.Answer = append(resp.Answer, rr)
		}
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(out)
	}))
	defer srv.Close()

	q := &dohQuerier{url: srv.URL, client: srv.Client()}
	msg := newQuery("cloudflare.com", mdns.TypeA, false)
	r, rtt, err := q.exchange(context.Background(), msg)
	if err != nil {
		t.Fatalf("doh exchange failed: %v", err)
	}
	if rtt <= 0 || len(aRecords(r)) != 1 {
		t.Fatalf("unexpected doh response: rtt=%v answers=%v", rtt, aRecords(r))
	}

	rep := &Report{
		Version: "1.0.0",
		Options: DefaultOptions(),
		Results: []*ServerResult{
			{
				Resolver:  Builtin()[0],
				Rank:      1,
				Status:    "ok",
				Primary:   ProtoUDP,
				Overall:   Stats{Sent: 20, Received: 20, Avg: 12.34, P95: 16.5, Min: 9.1, Max: 18.2, Jitter: 1.8},
				Filtering: FilteringResult{Checked: true, BlocksAds: false, TestedCount: 3},
			},
		},
	}
	for _, fmtName := range []string{"json", "csv", "md"} {
		out, err := Export(rep, fmtName)
		if err != nil || !strings.Contains(out, "Cloudflare") {
			t.Fatalf("export %s failed: err=%v out=%s", fmtName, err, out)
		}
	}
}

func TestNormalizeAndSkippedAndLiveProgress(t *testing.T) {
	opts := Options{Queries: -1, TimeoutMs: 50, Protocols: []string{"invalid", "udp"}}
	opts.Normalize()
	if opts.Queries != DefaultOptions().Queries || opts.TimeoutMs != DefaultOptions().TimeoutMs {
		t.Fatalf("Normalize did not restore invalid bounds: %+v", opts)
	}
	if len(opts.Protocols) != 1 || opts.Protocols[0] != "udp" {
		t.Fatalf("Normalize unexpected protocols: %+v", opts.Protocols)
	}

	// UDP-only run on a DoH-only resolver should mark status="skipped", not "failed".
	dohOnly := Resolver{ID: "dohonly", Name: "DoH Only", Category: CategoryPrivacy, Source: SourceBuiltin, DoH: "https://example.com/dns-query"}
	rep := Run(context.Background(), []Resolver{dohOnly}, Options{Protocols: []string{"udp"}, Queries: 1, CheckIntegrity: false}, "1.3.0", nil)
	if len(rep.Results) != 1 || rep.Results[0].Status != "skipped" {
		t.Fatalf("expected skipped status for DoH-only resolver in UDP mode, got %+v", rep.Results)
	}
	if rep.Recommendation != nil {
		t.Fatalf("expected nil recommendation when only skipped resolver ran, got %+v", rep.Recommendation)
	}

	// Verify local DoH mock benchmark emits Partial streaming snapshots and QuickVerify succeeds.
	dohSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		q := new(mdns.Msg)
		if err := q.Unpack(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := new(mdns.Msg)
		resp.SetReply(q)
		if len(q.Question) > 0 {
			resp.Answer = append(resp.Answer, &mdns.A{
				Hdr: mdns.RR_Header{Name: q.Question[0].Name, Rrtype: mdns.TypeA, Class: mdns.ClassINET, Ttl: 60},
				A:   net.ParseIP("1.1.1.1"),
			})
		}
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(out)
	}))
	defer dohSrv.Close()

	mockRes := Resolver{ID: "mockdoh", Name: "Mock DoH", Category: CategoryCustom, Source: SourceCustom, DoH: dohSrv.URL}
	st := &serverState{
		resolver: mockRes,
		queriers: map[Protocol]querier{ProtoUDP: &dohQuerier{}},
		samples:  map[Protocol]*sampleSet{ProtoUDP: {failures: map[string]int{}}},
	}
	pendingSnap := snapshotResults([]*serverState{st}, true)
	if len(pendingSnap) != 1 || pendingSnap[0].Status != "pending" {
		t.Fatalf("expected pending snapshot before samples complete, got %+v", pendingSnap[0])
	}

	var partialCount int
	mockRep := Run(context.Background(), []Resolver{mockRes}, Options{
		Protocols:      []string{"doh"},
		Queries:        2,
		Workers:        2,
		StaggerMs:      0,
		TimeoutMs:      1000,
		CheckIntegrity: false,
	}, "1.3.0", func(p Progress) {
		if len(p.Partial) > 0 {
			partialCount++
		}
	})
	if partialCount == 0 || len(mockRep.Results) != 1 || mockRep.Results[0].Status != "ok" {
		t.Fatalf("expected streaming partial snapshots and ok status, partialCount=%d results=%+v", partialCount, mockRep.Results)
	}

	qv := QuickVerify(context.Background(), mockRes, DefaultOptions())
	if qv.Status != "ok" || qv.Overall.Sent != 4 || qv.Overall.Received != 4 {
		t.Fatalf("QuickVerify unexpected result: %+v", qv)
	}
}

func TestBuiltinResolversIntegrity(t *testing.T) {
	list := Builtin()
	if len(list) == 0 {
		t.Fatal("builtin resolver list must not be empty")
	}
	seenID := make(map[string]bool, len(list))
	seenPrimaryIP := make(map[string]string, len(list))
	for _, r := range list {
		if r.ID == "" || r.Name == "" || r.Description == "" {
			t.Fatalf("builtin resolver missing metadata: %+v", r)
		}
		if r.ID == "mullvad" || r.Deprecated {
			t.Fatalf("discontinued resolver must not ship in active builtin list: %+v", r)
		}
		if seenID[r.ID] {
			t.Fatalf("duplicate builtin resolver ID %q", r.ID)
		}
		seenID[r.ID] = true
		if len(r.IPv4) == 0 || len(r.IPv6) == 0 {
			t.Fatalf("builtin resolver %q must include both IPv4 and IPv6 addresses", r.ID)
		}
		for _, ipStr := range r.IPv4 {
			if ip := net.ParseIP(ipStr); ip == nil || ip.To4() == nil {
				t.Fatalf("builtin resolver %q invalid IPv4 %q", r.ID, ipStr)
			}
		}
		for _, ipStr := range r.IPv6 {
			if ip := net.ParseIP(ipStr); ip == nil || ip.To4() != nil {
				t.Fatalf("builtin resolver %q invalid IPv6 %q", r.ID, ipStr)
			}
		}
		if prev, dup := seenPrimaryIP[r.IPv4[0]]; dup {
			t.Fatalf("duplicate primary IPv4 %q on %q and %q", r.IPv4[0], prev, r.ID)
		}
		seenPrimaryIP[r.IPv4[0]] = r.ID

		u, err := url.Parse(r.DoH)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			t.Fatalf("builtin resolver %q invalid DoH URL %q: %v", r.ID, r.DoH, err)
		}
		if r.DoT == "" || net.ParseIP(r.DoT) != nil {
			t.Fatalf("builtin resolver %q invalid DoT hostname %q", r.ID, r.DoT)
		}
		if !r.Applicable() {
			t.Fatalf("builtin resolver %q should be Applicable()", r.ID)
		}
	}
}

func TestStreamQuerierFallbackAddr(t *testing.T) {
	q, err := newQuerier(Resolver{
		ID:   "controld",
		IPv4: []string{"76.76.2.0"},
		DoT:  "p0.freedns.controld.com",
	}, ProtoDoT, time.Second, nil)
	if err != nil {
		t.Fatalf("newQuerier ProtoDoT: %v", err)
	}
	sq, ok := q.(*streamQuerier)
	if !ok {
		t.Fatalf("expected *streamQuerier, got %T", q)
	}
	if sq.addr != "76.76.2.0:853" || sq.fallbackAddr != "p0.freedns.controld.com:853" {
		t.Fatalf("unexpected DoT addrs: addr=%q fallback=%q", sq.addr, sq.fallbackAddr)
	}

	// Reserve and immediately close a local port so dialing it fails fast with connection refused.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddr := closedLn.Addr().String()
	_ = closedLn.Close()

	// Spin up a live TCP DNS listener to act as fallbackAddr.
	pc, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &mdns.Server{
		Listener: pc,
		Handler: mdns.HandlerFunc(func(w mdns.ResponseWriter, r *mdns.Msg) {
			resp := new(mdns.Msg)
			resp.SetReply(r)
			_ = w.WriteMsg(resp)
		}),
	}
	go func() { _ = srv.ActivateAndServe() }()
	defer func() { _ = srv.Shutdown() }()

	fallbackTester := &streamQuerier{
		addr:         deadAddr,
		fallbackAddr: pc.Addr().String(),
		timeout:      time.Second,
	}
	defer fallbackTester.close()

	resp, _, err := fallbackTester.exchange(context.Background(), newQuery("example.com", mdns.TypeA, false))
	if err != nil {
		t.Fatalf("expected fallback dial to succeed, got %v", err)
	}
	if resp == nil || resp.Rcode != mdns.RcodeSuccess {
		t.Fatalf("unexpected fallback DNS response: %+v", resp)
	}
	if fallbackTester.addr != pc.Addr().String() || fallbackTester.fallbackAddr != "" {
		t.Fatalf("expected working fallback address to be promoted, got addr=%q fallback=%q",
			fallbackTester.addr, fallbackTester.fallbackAddr)
	}
}

func TestUDPQuerierReusesClientAndCheckIntegrityReusesWarmQuerier(t *testing.T) {
	r := Resolver{ID: "udp-reuse", IPv4: []string{"127.0.0.1"}}
	q, err := newQuerier(r, ProtoUDP, time.Second, nil)
	if err != nil {
		t.Fatalf("newQuerier ProtoUDP: %v", err)
	}
	uq, ok := q.(*udpQuerier)
	if !ok || uq.client == nil {
		t.Fatalf("expected *udpQuerier with initialized client, got %T", q)
	}

	dohSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req := new(mdns.Msg)
		if err := req.Unpack(body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := new(mdns.Msg)
		resp.SetReply(req)
		out, _ := resp.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(out)
	}))
	defer dohSrv.Close()

	mockDoH := Resolver{ID: "mockdoh-reuse", Name: "Mock DoH", Category: CategoryCustom, Source: SourceCustom, DoH: dohSrv.URL}
	dq, err := newQuerier(mockDoH, ProtoDoH, time.Second, dohSrv.Client())
	if err != nil {
		t.Fatalf("newQuerier ProtoDoH: %v", err)
	}
	msg := newQuery("example.com", mdns.TypeA, false)
	origID := msg.Id
	resp, _, err := dq.exchange(context.Background(), msg)
	if err != nil {
		t.Fatalf("doh exchange: %v", err)
	}
	if msg.Id != origID || resp.Id != origID {
		t.Fatalf("expected original query ID preserved (%d), msg=%d resp=%d", origID, msg.Id, resp.Id)
	}

	dotRep := &Report{
		Version: "1.3.0",
		Options: Options{Protocols: []string{"udp", "doh", "dot"}, Queries: 5, TimeoutMs: 2000},
		Results: []*ServerResult{{
			Resolver: Builtin()[0],
			Rank:     1,
			Status:   "ok",
			Overall:  Stats{Avg: 12.5, P95: 18.0},
			Protocols: map[Protocol]*ProtocolResult{
				ProtoDoT: {Protocol: ProtoDoT, Combined: Stats{Sent: 5, Received: 5, Avg: 19.4}},
			},
		}},
	}
	md, err := Export(dotRep, "md")
	if err != nil || !strings.Contains(md, "| DoT |") || !strings.Contains(md, "19.40") {
		t.Fatalf("expected Markdown export to include DoT column when ProtoDoT tested, got err=%v md:\n%s", err, md)
	}
}
