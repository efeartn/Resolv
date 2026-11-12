// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv / PulseDNS contributors

package dns

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
