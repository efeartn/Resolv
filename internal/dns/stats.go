// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package dns

import (
	"math"
	"sort"
)

// Stats summarises a set of latency samples (milliseconds), including P95 and
// the chronological sample series for sparkline micro-charts.
type Stats struct {
	Sent     int       `json:"sent"`
	Received int       `json:"received"`
	Avg      float64   `json:"avgMs"`
	Min      float64   `json:"minMs"`
	Max      float64   `json:"maxMs"`
	Median   float64   `json:"medianMs"`
	P95      float64   `json:"p95Ms"`
	Jitter   float64   `json:"jitterMs"` // population standard deviation (σ)
	Loss     float64   `json:"lossPct"`  // 0-100
	Samples  []float64 `json:"samples,omitempty"`
}

// Valid reports whether at least one sample was received.
func (s Stats) Valid() bool { return s.Received > 0 }

func computeStats(samples []float64, sent int) Stats {
	st := Stats{Sent: sent, Received: len(samples)}
	if sent > 0 {
		st.Loss = round2(float64(sent-len(samples)) / float64(sent) * 100)
	}
	if len(samples) == 0 {
		if sent > 0 {
			st.Loss = 100
		}
		return st
	}

	series := make([]float64, len(samples))
	for i, v := range samples {
		series[i] = round2(v)
	}
	st.Samples = series

	sorted := append([]float64(nil), samples...)
	sort.Float64s(sorted)

	var sum float64
	for _, v := range sorted {
		sum += v
	}
	mean := sum / float64(len(sorted))

	var sq float64
	for _, v := range sorted {
		d := v - mean
		sq += d * d
	}

	n := len(sorted)
	median := sorted[n/2]
	if n%2 == 0 {
		median = (sorted[n/2-1] + sorted[n/2]) / 2
	}

	st.Avg = round2(mean)
	st.Min = round2(sorted[0])
	st.Max = round2(sorted[n-1])
	st.Median = round2(median)
	st.P95 = round2(percentile(sorted, 0.95))
	st.Jitter = round2(math.Sqrt(sq / float64(n)))
	return st
}

// percentile computes the p-th percentile (0 <= p <= 1) of a pre-sorted slice
// using linear interpolation between closest ranks.
func percentile(sorted []float64, p float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 || p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[n-1]
	}
	pos := p * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	weight := pos - float64(lo)
	return sorted[lo]*(1-weight) + sorted[hi]*weight
}

func round2(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*100) / 100
}
