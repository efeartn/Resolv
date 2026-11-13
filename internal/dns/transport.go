// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package dns

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	mdns "github.com/miekg/dns"
)

// Protocol identifies a DNS transport.
type Protocol string

const (
	ProtoUDP Protocol = "udp" // RFC 1035, port 53
	ProtoTCP Protocol = "tcp" // RFC 7766, port 53
	ProtoDoH Protocol = "doh" // RFC 8484
	ProtoDoT Protocol = "dot" // RFC 7858, port 853
)

// AllProtocols in display order.
var AllProtocols = []Protocol{ProtoUDP, ProtoTCP, ProtoDoH, ProtoDoT}

// querier performs a single DNS exchange and reports the on-the-wire round
// trip time. Connection setup (TCP/TLS handshakes) is deliberately excluded
// from the measured time so connection-oriented transports are compared on
// query latency, not handshake cost.
type querier interface {
	exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, time.Duration, error)
	close()
}

// ---------------------------------------------------------------- UDP ----

type udpQuerier struct {
	addr    string
	timeout time.Duration
}

func (q *udpQuerier) exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, time.Duration, error) {
	c := &mdns.Client{Net: "udp", Timeout: q.timeout, UDPSize: 1232}
	start := time.Now()
	r, _, err := c.ExchangeContext(ctx, m, q.addr)
	rtt := time.Since(start)
	if err != nil {
		return nil, rtt, err
	}
	return r, rtt, nil
}

func (q *udpQuerier) close() {}

// ----------------------------------------------------------- TCP / DoT ----

// streamQuerier keeps one persistent TCP or TLS connection per server and
// serialises queries over it (RFC 7766 connection reuse).
type streamQuerier struct {
	mu           sync.Mutex
	addr         string
	fallbackAddr string
	tlsConf      *tls.Config // nil for plain TCP
	timeout      time.Duration
	conn         *mdns.Conn
}

func (q *streamQuerier) dialAddr(ctx context.Context, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: q.timeout, KeepAlive: 15 * time.Second}
	if q.tlsConf != nil {
		return (&tls.Dialer{NetDialer: d, Config: q.tlsConf}).DialContext(ctx, "tcp", addr)
	}
	return d.DialContext(ctx, "tcp", addr)
}

func (q *streamQuerier) dial(ctx context.Context) error {
	c, err := q.dialAddr(ctx, q.addr)
	if err != nil && q.fallbackAddr != "" && q.fallbackAddr != q.addr && ctx.Err() == nil {
		// Some providers serve plain :53 and DoT :853 on distinct frontend IPs; fall back to the SNI host.
		if fc, ferr := q.dialAddr(ctx, q.fallbackAddr); ferr == nil {
			q.addr = q.fallbackAddr
			q.fallbackAddr = ""
			c, err = fc, nil
		}
	}
	if err != nil {
		return err
	}
	q.conn = &mdns.Conn{Conn: c, UDPSize: 1232}
	return nil
}

func (q *streamQuerier) reset() {
	if q.conn != nil {
		_ = q.conn.Close()
		q.conn = nil
	}
}

func (q *streamQuerier) roundTrip(ctx context.Context, m *mdns.Msg) (*mdns.Msg, time.Duration, error) {
	deadline := time.Now().Add(q.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = q.conn.SetDeadline(deadline)
	start := time.Now()
	if err := q.conn.WriteMsg(m); err != nil {
		return nil, time.Since(start), err
	}
	for {
		r, err := q.conn.ReadMsg()
		if err != nil {
			return nil, time.Since(start), err
		}
		if r.Id == m.Id {
			return r, time.Since(start), nil
		}
		// Stale answer from an earlier timed-out query: keep reading.
	}
}

func (q *streamQuerier) exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, time.Duration, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	fresh := false
	if q.conn == nil {
		if err := q.dial(ctx); err != nil {
			return nil, 0, err
		}
		fresh = true
	}
	r, rtt, err := q.roundTrip(ctx, m)
	if err == nil {
		return r, rtt, nil
	}
	q.reset()
	if fresh || ctx.Err() != nil {
		return nil, rtt, err
	}
	// The server may have closed an idle connection; redial once.
	if derr := q.dial(ctx); derr != nil {
		return nil, 0, derr
	}
	r, rtt, err = q.roundTrip(ctx, m)
	if err != nil {
		q.reset()
	}
	return r, rtt, err
}

func (q *streamQuerier) close() {
	q.mu.Lock()
	q.reset()
	q.mu.Unlock()
}

// ---------------------------------------------------------------- DoH ----

type dohQuerier struct {
	url    string
	client *http.Client
}

func newDoHTransport(timeout time.Duration) *http.Transport {
	return &http.Transport{
		Proxy: nil, // benchmark the resolver, not a local proxy
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   timeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
}

func (q *dohQuerier) exchange(ctx context.Context, m *mdns.Msg) (*mdns.Msg, time.Duration, error) {
	wire := m.Copy()
	wire.Id = 0 // RFC 8484 §4.1: use ID 0 for cache friendliness
	buf, err := wire.Pack()
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, q.url, bytes.NewReader(buf))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	req.Header.Set("User-Agent", "Resolv")

	start := time.Now()
	resp, err := q.client.Do(req)
	if err != nil {
		return nil, time.Since(start), err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 65535))
	_ = resp.Body.Close()
	rtt := time.Since(start)
	if err != nil {
		return nil, rtt, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, rtt, fmt.Errorf("doh: HTTP %d", resp.StatusCode)
	}
	r := new(mdns.Msg)
	if err := r.Unpack(body); err != nil {
		return nil, rtt, fmt.Errorf("doh: malformed response: %w", err)
	}
	r.Id = m.Id
	return r, rtt, nil
}

func (q *dohQuerier) close() {}

// -------------------------------------------------------------- factory ----

var errNoEndpoint = errors.New("no endpoint for protocol")

// newQuerier builds a querier for the resolver/protocol combination.
func newQuerier(r Resolver, p Protocol, timeout time.Duration, doh *http.Client) (querier, error) {
	switch p {
	case ProtoUDP, ProtoTCP:
		ip := r.PrimaryAddr()
		if ip == "" {
			return nil, errNoEndpoint
		}
		addr := net.JoinHostPort(ip, "53")
		if p == ProtoUDP {
			return &udpQuerier{addr: addr, timeout: timeout}, nil
		}
		return &streamQuerier{addr: addr, timeout: timeout}, nil
	case ProtoDoT:
		if r.DoT == "" {
			return nil, errNoEndpoint
		}
		host := r.PrimaryAddr()
		fallback := ""
		if host == "" {
			host = r.DoT
		} else if net.ParseIP(r.DoT) == nil {
			fallback = net.JoinHostPort(r.DoT, "853")
		}
		addr := net.JoinHostPort(host, "853")
		if fallback == addr {
			fallback = ""
		}
		return &streamQuerier{
			addr:         addr,
			fallbackAddr: fallback,
			timeout:      timeout,
			tlsConf:      &tls.Config{ServerName: r.DoT, MinVersion: tls.VersionTLS12},
		}, nil
	case ProtoDoH:
		if r.DoH == "" {
			return nil, errNoEndpoint
		}
		return &dohQuerier{url: r.DoH, client: doh}, nil
	}
	return nil, fmt.Errorf("unknown protocol %q", p)
}

// classifyError maps transport errors to short, stable failure labels.
func classifyError(err error) string {
	if err == nil {
		return ""
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()) {
		return "timeout"
	}
	var te *tls.CertificateVerificationError
	if errors.As(err, &te) {
		return "tls"
	}
	var oe *net.OpError
	if errors.As(err, &oe) {
		if oe.Op == "dial" {
			return "unreachable"
		}
		return "network"
	}
	return "error"
}
