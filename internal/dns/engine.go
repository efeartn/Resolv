// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

// Package dns implements Resolv' pure-Go DNS benchmarking engine. It speaks
// the DNS protocol directly (UDP, TCP, DoH, DoT) via github.com/miekg/dns and
// never relies on ICMP.
package dns

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	mdns "github.com/miekg/dns"
)

// Options controls a benchmark run. Zero values are replaced by sane defaults.
type Options struct {
	Queries        int      `json:"queries"`        // queries per server, per protocol, per kind (cached/uncached)
	TimeoutMs      int      `json:"timeoutMs"`      // per-query timeout
	Workers        int      `json:"workers"`        // bounded worker pool size (max in-flight queries)
	StaggerMs      int      `json:"staggerMs"`      // delay between query dispatches
	Protocols      []string `json:"protocols"`      // subset of udp,tcp,doh,dot
	CachedDomains  []string `json:"cachedDomains"`  // high-traffic domains (edge cache hits)
	UncachedBase   string   `json:"uncachedBase"`   // random <uuid>.<base> forces recursion
	CheckIntegrity bool     `json:"checkIntegrity"` // DNSSEC + hijack detection
}

// DefaultOptions returns the defaults used by the UI.
func DefaultOptions() Options {
	return Options{
		Queries:        10,
		TimeoutMs:      2000,
		Workers:        12,
		StaggerMs:      12,
		Protocols:      []string{string(ProtoUDP), string(ProtoDoH)},
		CachedDomains:  []string{"cloudflare.com", "google.com", "apple.com"},
		UncachedBase:   "example.com",
		CheckIntegrity: true,
	}
}

// Normalize clamps out-of-range option values to safe defaults.
func (o *Options) Normalize() {
	d := DefaultOptions()
	if o.Queries <= 0 {
		o.Queries = d.Queries
	}
	if o.Queries > 50 {
		o.Queries = 50
	}
	if o.TimeoutMs < 250 {
		o.TimeoutMs = d.TimeoutMs
	}
	if o.TimeoutMs > 10000 {
		o.TimeoutMs = 10000
	}
	if o.Workers <= 0 {
		o.Workers = d.Workers
	}
	if o.Workers > 64 {
		o.Workers = 64
	}
	if o.StaggerMs < 0 {
		o.StaggerMs = d.StaggerMs
	}
	if len(o.CachedDomains) == 0 {
		o.CachedDomains = d.CachedDomains
	}
	o.UncachedBase = strings.Trim(strings.TrimSpace(o.UncachedBase), ".")
	if o.UncachedBase == "" {
		o.UncachedBase = d.UncachedBase
	}
	var protos []string
	seen := map[string]bool{}
	for _, p := range o.Protocols {
		p = strings.ToLower(strings.TrimSpace(p))
		switch Protocol(p) {
		case ProtoUDP, ProtoTCP, ProtoDoH, ProtoDoT:
			if !seen[p] {
				seen[p] = true
				protos = append(protos, p)
			}
		}
	}
	if len(protos) == 0 {
		protos = d.Protocols
	}
	o.Protocols = protos
}

func (o Options) timeout() time.Duration { return time.Duration(o.TimeoutMs) * time.Millisecond }

// ProtocolResult holds measurements for one transport of one server.
type ProtocolResult struct {
	Protocol Protocol       `json:"protocol"`
	Endpoint string         `json:"endpoint"`
	Cached   Stats          `json:"cached"`
	Uncached Stats          `json:"uncached"`
	Combined Stats          `json:"combined"`
	Failures map[string]int `json:"failures,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// ServerResult is the complete outcome for a single resolver.
type ServerResult struct {
	Resolver  Resolver                     `json:"resolver"`
	Rank      int                          `json:"rank"`
	Status    string                       `json:"status"` // ok | degraded | failed | skipped | pending
	Score     float64                      `json:"score"`
	Primary   Protocol                     `json:"primary"`
	Overall   Stats                        `json:"overall"`
	Cached    Stats                        `json:"cached"`
	Uncached  Stats                        `json:"uncached"`
	Protocols map[Protocol]*ProtocolResult `json:"protocols"`
	DNSSEC    string                       `json:"dnssec"` // validating | not-validating | unknown
	Filtering FilteringResult              `json:"filtering"`
	Hijack    HijackResult                 `json:"hijack"`
}

// Recommendation is the plain-English verdict shown in Simple mode.
type Recommendation struct {
	ResolverID       string  `json:"resolverId"`
	Name             string  `json:"name"`
	AvgMs            float64 `json:"avgMs"`
	CurrentAvgMs     float64 `json:"currentAvgMs"`
	ImprovementPct   float64 `json:"improvementPct"`
	CurrentIsFastest bool    `json:"currentIsFastest"`
	Headline         string  `json:"headline"`
	Detail           string  `json:"detail"`
}

// Report is the full result of a benchmark run.
type Report struct {
	Version        string          `json:"version"`
	StartedAt      time.Time       `json:"startedAt"`
	FinishedAt     time.Time       `json:"finishedAt"`
	DurationMs     int64           `json:"durationMs"`
	Options        Options         `json:"options"`
	Results        []*ServerResult `json:"results"`
	Recommendation *Recommendation `json:"recommendation,omitempty"`
	Network        NetworkIntegrity `json:"network"`
	Cancelled      bool            `json:"cancelled"`
}

// Progress is emitted while a benchmark runs.
type Progress struct {
	Phase   string          `json:"phase"` // warmup | benchmark | integrity | done
	Done    int             `json:"done"`
	Total   int             `json:"total"`
	Message string          `json:"message"`
	Partial []*ServerResult `json:"partial,omitempty"`
}

// ProgressFunc receives progress updates. It must be safe for concurrent use.
type ProgressFunc func(Progress)

type queryKind uint8

const (
	kindCached queryKind = iota
	kindUncached
)

type job struct {
	server int
	proto  Protocol
	kind   queryKind
	seq    int
}

type sampleSet struct {
	cached, uncached, all    []float64
	sentCached, sentUncached int
	failures                 map[string]int
}

type serverState struct {
	resolver Resolver
	queriers map[Protocol]querier
	errs     map[Protocol]string
	mu       sync.Mutex
	samples  map[Protocol]*sampleSet
}

// Run benchmarks the given resolvers. It always returns a report; if ctx is
// cancelled the report contains whatever was measured so far.
func Run(ctx context.Context, resolvers []Resolver, opts Options, version string, progress ProgressFunc) *Report {
	opts.Normalize()
	if progress == nil {
		progress = func(Progress) {}
	}
	report := &Report{Version: version, StartedAt: time.Now(), Options: opts}
	timeout := opts.timeout()

	dohTransport := newDoHTransport(timeout)
	defer dohTransport.CloseIdleConnections()
	dohClient := &http.Client{Transport: dohTransport, Timeout: timeout}

	// Build per-server queriers.
	servers := make([]*serverState, len(resolvers))
	for i, r := range resolvers {
		st := &serverState{
			resolver: r,
			queriers: map[Protocol]querier{},
			errs:     map[Protocol]string{},
			samples:  map[Protocol]*sampleSet{},
		}
		for _, ps := range opts.Protocols {
			p := Protocol(ps)
			q, err := newQuerier(r, p, timeout, dohClient)
			if err != nil {
				continue // protocol not offered by this resolver
			}
			st.queriers[p] = q
			st.samples[p] = &sampleSet{failures: map[string]int{}}
		}
		servers[i] = st
	}
	defer func() {
		for _, s := range servers {
			for _, q := range s.queriers {
				q.close()
			}
		}
	}()

	// Phase 1: warm-up. Establishes TCP/TLS/HTTP2 sessions and primes each
	// resolver's cache with the "cached" domains. Not measured.
	warmup(ctx, servers, opts, progress)

	// Phase 2: interleaved, staggered benchmark over a bounded worker pool.
	jobs := buildJobs(servers, opts)
	runJobs(ctx, servers, jobs, opts, progress)

	// Phase 3: integrity (DNSSEC + hijack detection).
	results := make([]*ServerResult, len(servers))
	for i, s := range servers {
		results[i] = s.summarise(false)
	}
	if opts.CheckIntegrity && ctx.Err() == nil {
		report.Network = checkIntegrity(ctx, servers, results, opts, progress)
	}

	rank(results)
	report.Results = results
	report.Recommendation = recommend(results)
	report.Cancelled = ctx.Err() != nil
	report.FinishedAt = time.Now()
	report.DurationMs = report.FinishedAt.Sub(report.StartedAt).Milliseconds()
	progress(Progress{Phase: "done", Done: 1, Total: 1, Message: "Benchmark complete"})
	return report
}

func warmup(ctx context.Context, servers []*serverState, opts Options, progress ProgressFunc) {
	type wjob struct {
		q querier
	}
	var list []wjob
	for _, s := range servers {
		for _, q := range s.queriers {
			list = append(list, wjob{q})
		}
	}
	total := len(list)
	var done int64
	progress(Progress{Phase: "warmup", Done: 0, Total: total, Message: "Warming up connections"})

	sem := make(chan struct{}, opts.Workers)
	var wg sync.WaitGroup
	for _, w := range list {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(q querier) {
			defer wg.Done()
			defer func() { <-sem }()
			for _, d := range opts.CachedDomains {
				qctx, cancel := context.WithTimeout(ctx, opts.timeout())
				_, _, _ = q.exchange(qctx, newQuery(d, mdns.TypeA, false))
				cancel()
			}
			n := atomic.AddInt64(&done, 1)
			progress(Progress{Phase: "warmup", Done: int(n), Total: total, Message: "Warming up connections"})
		}(w.q)
	}
	wg.Wait()
}

// buildJobs interleaves queries round-robin across servers so every server
// sees the same network conditions and no single server receives a burst.
func buildJobs(servers []*serverState, opts Options) []job {
	var jobs []job
	for seq := 0; seq < opts.Queries; seq++ {
		for _, kind := range []queryKind{kindCached, kindUncached} {
			for _, ps := range opts.Protocols {
				p := Protocol(ps)
				for i, s := range servers {
					if _, ok := s.queriers[p]; ok {
						jobs = append(jobs, job{server: i, proto: p, kind: kind, seq: seq})
					}
				}
			}
		}
	}
	return jobs
}

func runJobs(ctx context.Context, servers []*serverState, jobs []job, opts Options, progress ProgressFunc) {
	total := len(jobs)
	if total == 0 {
		return
	}
	var done int64
	var lastSnapshotMs atomic.Int64
	lastSnapshotMs.Store(time.Now().UnixMilli())
	progress(Progress{
		Phase:   "benchmark",
		Done:    0,
		Total:   total,
		Message: "Starting benchmark queries...",
		Partial: snapshotResults(servers, true),
	})

	ch := make(chan job)
	var wg sync.WaitGroup

	worker := func() {
		defer wg.Done()
		for j := range ch {
			s := servers[j.server]
			q := s.queriers[j.proto]

			var name string
			if j.kind == kindCached {
				name = opts.CachedDomains[j.seq%len(opts.CachedDomains)]
			} else {
				name = uuid.NewString() + "." + opts.UncachedBase
			}
			m := newQuery(name, mdns.TypeA, false)

			qctx, cancel := context.WithTimeout(ctx, opts.timeout())
			r, rtt, err := q.exchange(qctx, m)
			cancel()

			failure := ""
			switch {
			case err != nil:
				failure = classifyError(err)
			case r.Rcode != mdns.RcodeSuccess && r.Rcode != mdns.RcodeNameError:
				failure = strings.ToLower(mdns.RcodeToString[r.Rcode])
			}
			if ctx.Err() != nil && err != nil {
				continue // cancelled: don't record as loss
			}

			ms := float64(rtt.Microseconds()) / 1000
			s.mu.Lock()
			set := s.samples[j.proto]
			if j.kind == kindCached {
				set.sentCached++
				if failure == "" {
					set.cached = append(set.cached, ms)
					set.all = append(set.all, ms)
				}
			} else {
				set.sentUncached++
				if failure == "" {
					set.uncached = append(set.uncached, ms)
					set.all = append(set.all, ms)
				}
			}
			if failure != "" {
				set.failures[failure]++
			}
			s.mu.Unlock()

			n := atomic.AddInt64(&done, 1)
			var partial []*ServerResult
			nowMs := time.Now().UnixMilli()
			prevMs := lastSnapshotMs.Load()
			if int(n) == total || (nowMs-prevMs >= 220 && lastSnapshotMs.CompareAndSwap(prevMs, nowMs)) {
				partial = snapshotResults(servers, true)
			}
			progress(Progress{
				Phase:   "benchmark",
				Done:    int(n),
				Total:   total,
				Message: fmt.Sprintf("Querying %s over %s", s.resolver.Name, strings.ToUpper(string(j.proto))),
				Partial: partial,
			})
		}
	}

	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go worker()
	}

	stagger := time.Duration(opts.StaggerMs) * time.Millisecond
	var tick <-chan time.Time
	if stagger > 0 {
		t := time.NewTicker(stagger)
		defer t.Stop()
		tick = t.C
	}
dispatch:
	for _, j := range jobs {
		if tick != nil {
			select {
			case <-ctx.Done():
				break dispatch
			case <-tick:
			}
		}
		select {
		case <-ctx.Done():
			break dispatch
		case ch <- j:
		}
	}
	close(ch)
	wg.Wait()
}

func newQuery(name string, qtype uint16, dnssec bool) *mdns.Msg {
	m := new(mdns.Msg)
	m.SetQuestion(mdns.Fqdn(name), qtype)
	m.RecursionDesired = true
	m.SetEdns0(1232, dnssec)
	if dnssec {
		m.AuthenticatedData = true // RFC 6840 §5.7: request AD bit
	}
	return m
}

func snapshotResults(servers []*serverState, pendingAllowed bool) []*ServerResult {
	out := make([]*ServerResult, len(servers))
	for i, s := range servers {
		out[i] = s.summarise(pendingAllowed)
	}
	rank(out)
	return out
}

func (s *serverState) summarise(pendingAllowed bool) *ServerResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	res := &ServerResult{
		Resolver:  s.resolver,
		Protocols: map[Protocol]*ProtocolResult{},
		DNSSEC:    "unknown",
		Hijack:    HijackResult{Status: HijackUnknown},
	}
	totalSent := 0
	for _, p := range AllProtocols {
		set, ok := s.samples[p]
		if !ok {
			continue
		}
		pr := &ProtocolResult{
			Protocol: p,
			Endpoint: endpointLabel(s.resolver, p),
			Cached:   computeStats(set.cached, set.sentCached),
			Uncached: computeStats(set.uncached, set.sentUncached),
			Combined: computeStats(set.all, set.sentCached+set.sentUncached),
		}
		totalSent += pr.Combined.Sent
		if len(set.failures) > 0 {
			clonedFailures := make(map[string]int, len(set.failures))
			for k, v := range set.failures {
				clonedFailures[k] = v
			}
			pr.Failures = clonedFailures
		}
		if !pr.Combined.Valid() && pr.Combined.Sent > 0 {
			pr.Error = dominantFailure(pr.Failures)
		}
		res.Protocols[p] = pr
	}

	if len(res.Protocols) == 0 {
		res.Status = "skipped"
		return res
	}
	if pendingAllowed && totalSent == 0 {
		res.Status = "pending"
		return res
	}

	// Primary protocol: plain UDP when it works (what the OS will use),
	// otherwise the best encrypted transport.
	for _, p := range []Protocol{ProtoUDP, ProtoDoH, ProtoDoT, ProtoTCP} {
		if pr, ok := res.Protocols[p]; ok && pr.Combined.Valid() {
			res.Primary = p
			res.Overall, res.Cached, res.Uncached = pr.Combined, pr.Cached, pr.Uncached
			break
		}
	}
	if res.Primary == "" {
		if pendingAllowed {
			res.Status = "pending"
		} else {
			res.Status = "failed"
		}
		for _, p := range []Protocol{ProtoUDP, ProtoDoH, ProtoDoT, ProtoTCP} {
			if pr, ok := res.Protocols[p]; ok {
				res.Primary = p
				res.Overall = pr.Combined
				break
			}
		}
		return res
	}
	switch {
	case res.Overall.Loss > 10:
		res.Status = "degraded"
	default:
		res.Status = "ok"
	}
	// Score: average latency penalised by loss (each 1% loss adds 3%) and
	// half the jitter. Lower is better.
	res.Score = round2(res.Overall.Avg*(1+res.Overall.Loss*0.03) + res.Overall.Jitter*0.5)
	return res
}

func endpointLabel(r Resolver, p Protocol) string {
	switch p {
	case ProtoUDP, ProtoTCP:
		return r.PrimaryAddr() + ":53"
	case ProtoDoT:
		return r.DoT + ":853"
	case ProtoDoH:
		return r.DoH
	}
	return ""
}

func dominantFailure(f map[string]int) string {
	best, n := "no response", 0
	for k, v := range f {
		if v > n {
			best, n = k, v
		}
	}
	return best
}

func rank(results []*ServerResult) {
	order := map[string]int{"ok": 0, "degraded": 1, "pending": 2, "failed": 3, "skipped": 4}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if order[a.Status] != order[b.Status] {
			return order[a.Status] < order[b.Status]
		}
		if a.Status == "failed" || a.Status == "skipped" || a.Status == "pending" {
			return a.Resolver.Name < b.Resolver.Name
		}
		return a.Score < b.Score
	})
	for i, r := range results {
		r.Rank = i + 1
	}
}

func recommend(results []*ServerResult) *Recommendation {
	var best, current *ServerResult
	for _, r := range results {
		if r.Status != "ok" && r.Status != "degraded" {
			continue
		}
		if current == nil && r.Resolver.Source == SourceSystem {
			current = r
		}
		if best == nil && r.Resolver.Applicable() &&
			(r.Resolver.Source == SourceBuiltin || r.Resolver.Source == SourceCustom) &&
			r.Resolver.Category != CategoryFamily {
			best = r
		}
	}
	if best == nil {
		return nil
	}
	rec := &Recommendation{ResolverID: best.Resolver.ID, Name: best.Resolver.Name, AvgMs: best.Overall.Avg}

	alreadyUsing := current != nil && sameAddress(current.Resolver, best.Resolver)
	if current != nil {
		rec.CurrentAvgMs = current.Overall.Avg
	}
	switch {
	case alreadyUsing:
		rec.CurrentIsFastest = true
		rec.Headline = fmt.Sprintf("You're already using %s — the fastest resolver on your network", best.Resolver.Name)
	case current != nil && current.Score <= best.Score:
		rec.CurrentIsFastest = true
		rec.Headline = "Your current DNS is already the fastest option"
		rec.Detail = fmt.Sprintf("%s is the best public alternative at %.1f ms (yours: %.1f ms).", best.Resolver.Name, best.Overall.Avg, current.Overall.Avg)
	case current != nil && current.Overall.Avg > 0:
		pct := (current.Overall.Avg - best.Overall.Avg) / current.Overall.Avg * 100
		rec.ImprovementPct = round2(pct)
		if pct >= 1 {
			rec.Headline = fmt.Sprintf("%s is %.0f%% faster than your current DNS", best.Resolver.Name, pct)
		} else {
			rec.Headline = fmt.Sprintf("%s is the most consistent resolver for your connection", best.Resolver.Name)
		}
	default:
		rec.Headline = fmt.Sprintf("%s is the fastest resolver for your connection", best.Resolver.Name)
	}
	if rec.Detail == "" {
		rec.Detail = fmt.Sprintf("Average %.1f ms · jitter %.1f ms · %.0f%% packet loss · %s",
			best.Overall.Avg, best.Overall.Jitter, best.Overall.Loss, best.Resolver.Category)
	}
	return rec
}

func sameAddress(a, b Resolver) bool {
	for _, x := range append(append([]string{}, a.IPv4...), a.IPv6...) {
		for _, y := range append(append([]string{}, b.IPv4...), b.IPv6...) {
			if x == y {
				return true
			}
		}
	}
	return false
}

// QuickVerify runs a fast 2-cached + 2-uncached live latency check against a single resolver.
func QuickVerify(ctx context.Context, r Resolver, opts Options) *ServerResult {
	opts.Normalize()
	timeout := opts.timeout()
	if timeout > 2500*time.Millisecond {
		timeout = 2500 * time.Millisecond
	}

	var protos []Protocol
	if r.PrimaryAddr() != "" {
		protos = append(protos, ProtoUDP)
	}
	if r.DoH != "" && len(protos) == 0 {
		protos = append(protos, ProtoDoH)
	}
	if len(protos) == 0 {
		for _, ps := range opts.Protocols {
			protos = append(protos, Protocol(ps))
		}
	}

	dohTransport := newDoHTransport(timeout)
	defer dohTransport.CloseIdleConnections()
	dohClient := &http.Client{Transport: dohTransport, Timeout: timeout}

	st := &serverState{
		resolver: r,
		queriers: map[Protocol]querier{},
		errs:     map[Protocol]string{},
		samples:  map[Protocol]*sampleSet{},
	}
	var protoStrings []string
	for _, p := range protos {
		q, err := newQuerier(r, p, timeout, dohClient)
		if err != nil {
			continue
		}
		st.queriers[p] = q
		st.samples[p] = &sampleSet{failures: map[string]int{}}
		protoStrings = append(protoStrings, string(p))
	}
	defer func() {
		for _, q := range st.queriers {
			q.close()
		}
	}()

	quickOpts := opts
	quickOpts.Queries = 2
	quickOpts.Workers = 4
	quickOpts.StaggerMs = 0
	quickOpts.Protocols = protoStrings

	servers := []*serverState{st}
	warmup(ctx, servers, quickOpts, func(Progress) {})
	jobs := buildJobs(servers, quickOpts)
	runJobs(ctx, servers, jobs, quickOpts, func(Progress) {})

	res := st.summarise(false)
	res.Rank = 1
	return res
}
