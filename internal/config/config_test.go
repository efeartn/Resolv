// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package config

import (
	"testing"
	"time"

	"resolv/internal/dns"
)

func withTempConfigDir(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	mu.Lock()
	prev := overrideDir
	overrideDir = tmp
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		overrideDir = prev
		mu.Unlock()
	})
}

func TestSettingsLoadSaveAndNormalize(t *testing.T) {
	withTempConfigDir(t)

	// Initial load returns valid defaults when settings.json doesn't exist yet.
	s, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings initial failed: %v", err)
	}
	if s.ViewMode != "simple" || !s.AutoCheckUpdates || s.Options.Queries != 10 {
		t.Fatalf("unexpected defaults: %+v", s)
	}

	// Modify and persist.
	s.ViewMode = "pro"
	s.CategoryFilter = "Privacy"
	s.SortBy = "p95"
	s.AutoCheckUpdates = false
	s.Options.Queries = 20
	s.Options.Protocols = []string{"udp", "doh", "dot"}
	s.Options.UncachedBase = "cloudflare.com"

	if err := SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings failed: %v", err)
	}

	loaded, err := LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings reload failed: %v", err)
	}
	if loaded.ViewMode != "pro" || loaded.CategoryFilter != "Privacy" || loaded.SortBy != "p95" {
		t.Fatalf("mismatch after reload: %+v", loaded)
	}
	if loaded.AutoCheckUpdates {
		t.Fatalf("expected AutoCheckUpdates=false after reload")
	}
	if loaded.Options.Queries != 20 || len(loaded.Options.Protocols) != 3 || loaded.Options.UncachedBase != "cloudflare.com" {
		t.Fatalf("options mismatch after reload: %+v", loaded.Options)
	}

	s.SortBy = "doh"
	if err := SaveSettings(s); err != nil {
		t.Fatalf("SaveSettings doh sort failed: %v", err)
	}
	reloaded, err := LoadSettings()
	if err != nil || reloaded.SortBy != "doh" {
		t.Fatalf("expected SortBy=\"doh\" to persist across reload, got %+v (err=%v)", reloaded, err)
	}
}

func TestCustomResolversPersistence(t *testing.T) {
	withTempConfigDir(t)

	custom := []dns.Resolver{
		{
			ID:       "custom-abc",
			Name:     "Corporate DNS",
			Category: dns.CategoryCustom,
			Source:   dns.SourceCustom,
			IPv4:     []string{"10.10.0.53"},
			DoH:      "https://dns.corp.internal/dns-query",
		},
	}
	if err := SaveCustomResolvers(custom); err != nil {
		t.Fatalf("SaveCustomResolvers failed: %v", err)
	}

	got, err := LoadCustomResolvers()
	if err != nil {
		t.Fatalf("LoadCustomResolvers failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != "custom-abc" || got[0].Name != "Corporate DNS" {
		t.Fatalf("unexpected custom resolvers: %+v", got)
	}
}

func TestHistoryAppendCapAndClear(t *testing.T) {
	withTempConfigDir(t)

	for i := 0; i < 5; i++ {
		entry := HistoryEntry{
			ID:          time.Now().Add(time.Duration(i) * time.Second).Format("run-150405"),
			Timestamp:   time.Now().Add(time.Duration(i) * time.Second),
			AdapterName: "Wi-Fi",
			DurationMs:  int64(1000 + i),
			WinnerName:  "Cloudflare",
			WinnerAvgMs: float64(10 + i),
		}
		list, err := AppendHistory(entry, 3)
		if err != nil {
			t.Fatalf("AppendHistory %d failed: %v", i, err)
		}
		if i >= 2 && len(list) != 3 {
			t.Fatalf("expected cap of 3 entries, got %d", len(list))
		}
	}

	loaded, err := LoadHistory()
	if err != nil {
		t.Fatalf("LoadHistory failed: %v", err)
	}
	if len(loaded) != 3 || loaded[0].WinnerAvgMs != 14 {
		t.Fatalf("unexpected history ordering/cap: %+v", loaded)
	}

	if err := ClearHistory(); err != nil {
		t.Fatalf("ClearHistory failed: %v", err)
	}
	empty, _ := LoadHistory()
	if len(empty) != 0 {
		t.Fatalf("expected empty history after clear, got %d", len(empty))
	}
}
