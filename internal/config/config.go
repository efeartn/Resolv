// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

// Package config manages persistent user settings, custom resolvers, and
// benchmark history inside the OS user configuration directory so application
// upgrades never overwrite user data.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"resolv/internal/dns"
)

const (
	appDirName        = "Resolv"
	settingsFile      = "settings.json"
	customFile        = "custom_resolvers.json"
	historyFile       = "history.json"
	defaultMaxHistory = 25
)

// Settings stores all persistent user preferences and benchmark defaults.
type Settings struct {
	ViewMode         string      `json:"viewMode"`         // "simple" | "pro"
	CategoryFilter   string      `json:"categoryFilter"`   // "all" | category
	SortBy           string      `json:"sortBy"`           // "rank" | "avg" | "p95" | "cached" | "uncached" | "jitter"
	Options          dns.Options `json:"options"`
	AutoCheckUpdates bool        `json:"autoCheckUpdates"`
}

// HistoryRow summarises one top resolver from a historical run.
type HistoryRow struct {
	Name     string  `json:"name"`
	Category string  `json:"category"`
	AvgMs    float64 `json:"avgMs"`
	P95Ms    float64 `json:"p95Ms"`
	JitterMs float64 `json:"jitterMs"`
	LossPct  float64 `json:"lossPct"`
}

// HistoryEntry records a compact snapshot of a completed benchmark run.
type HistoryEntry struct {
	ID             string       `json:"id"`
	Timestamp      time.Time    `json:"timestamp"`
	AdapterName    string       `json:"adapterName"`
	SystemDNS      []string     `json:"systemDns,omitempty"`
	DurationMs     int64        `json:"durationMs"`
	WinnerID       string       `json:"winnerId,omitempty"`
	WinnerName     string       `json:"winnerName,omitempty"`
	WinnerAvgMs    float64      `json:"winnerAvgMs,omitempty"`
	CurrentAvgMs   float64      `json:"currentAvgMs,omitempty"`
	ImprovementPct float64      `json:"improvementPct,omitempty"`
	Intercepted    bool         `json:"intercepted"`
	TopResults     []HistoryRow `json:"topResults"`
}

// DefaultSettings returns safe initial settings.
func DefaultSettings() Settings {
	return Settings{
		ViewMode:         "simple",
		CategoryFilter:   "all",
		SortBy:           "rank",
		Options:          dns.DefaultOptions(),
		AutoCheckUpdates: true,
	}
}

// Normalize clamps and validates settings values without mutating user intent.
func (s *Settings) Normalize() {
	if s.ViewMode != "simple" && s.ViewMode != "pro" {
		s.ViewMode = "simple"
	}
	if s.CategoryFilter == "" {
		s.CategoryFilter = "all"
	}
	switch s.SortBy {
	case "rank", "avg", "p95", "cached", "uncached", "jitter":
	default:
		s.SortBy = "rank"
	}
	s.Options.Normalize()
}

var (
	mu          sync.Mutex
	overrideDir string
)

// Dir returns the platform-specific configuration directory and ensures it exists.
func Dir() (string, error) {
	mu.Lock()
	customDir := overrideDir
	mu.Unlock()

	if customDir != "" {
		if err := os.MkdirAll(customDir, 0o755); err != nil {
			return "", err
		}
		return customDir, nil
	}

	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, appDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// SettingsPath returns the absolute path to settings.json.
func SettingsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, settingsFile), nil
}

// CustomResolversPath returns the absolute path to custom_resolvers.json.
func CustomResolversPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, customFile), nil
}

// HistoryPath returns the absolute path to history.json.
func HistoryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, historyFile), nil
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadSettings reads settings.json or returns DefaultSettings when absent.
func LoadSettings() (*Settings, error) {
	mu.Lock()
	defer mu.Unlock()

	s := DefaultSettings()
	p, err := dirFileLocked(settingsFile)
	if err != nil {
		return &s, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return &s, nil
		}
		return &s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		def := DefaultSettings()
		return &def, err
	}
	s.Normalize()
	return &s, nil
}

// SaveSettings atomically persists s to settings.json.
func SaveSettings(s *Settings) error {
	if s == nil {
		def := DefaultSettings()
		s = &def
	} else {
		s.Normalize()
	}
	mu.Lock()
	defer mu.Unlock()

	p, err := dirFileLocked(settingsFile)
	if err != nil {
		return err
	}
	return writeJSONAtomic(p, s)
}

// LoadCustomResolvers reads persisted custom DNS resolvers.
func LoadCustomResolvers() ([]dns.Resolver, error) {
	mu.Lock()
	defer mu.Unlock()

	p, err := dirFileLocked(customFile)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var list []dns.Resolver
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	return list, nil
}

// SaveCustomResolvers atomically persists user-added resolvers.
func SaveCustomResolvers(resolvers []dns.Resolver) error {
	mu.Lock()
	defer mu.Unlock()

	p, err := dirFileLocked(customFile)
	if err != nil {
		return err
	}
	if resolvers == nil {
		resolvers = []dns.Resolver{}
	}
	return writeJSONAtomic(p, resolvers)
}

// LoadHistory returns stored benchmark history entries, newest first.
func LoadHistory() ([]HistoryEntry, error) {
	mu.Lock()
	defer mu.Unlock()
	return loadHistoryLocked()
}

func loadHistoryLocked() ([]HistoryEntry, error) {
	p, err := dirFileLocked(historyFile)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryEntry{}, nil
		}
		return []HistoryEntry{}, err
	}
	var entries []HistoryEntry
	if err := json.Unmarshal(b, &entries); err != nil {
		return []HistoryEntry{}, err
	}
	if entries == nil {
		return []HistoryEntry{}, nil
	}
	return entries, nil
}

// AppendHistory prepends entry to history.json (capped to maxEntries) and
// returns the updated list.
func AppendHistory(entry HistoryEntry, maxEntries int) ([]HistoryEntry, error) {
	if maxEntries <= 0 {
		maxEntries = defaultMaxHistory
	}
	mu.Lock()
	defer mu.Unlock()

	existing, _ := loadHistoryLocked()
	updated := make([]HistoryEntry, 0, len(existing)+1)
	updated = append(updated, entry)
	updated = append(updated, existing...)
	if len(updated) > maxEntries {
		updated = updated[:maxEntries]
	}

	p, err := dirFileLocked(historyFile)
	if err != nil {
		return updated, err
	}
	if err := writeJSONAtomic(p, updated); err != nil {
		return updated, err
	}
	return updated, nil
}

// ClearHistory removes all saved benchmark history entries.
func ClearHistory() error {
	mu.Lock()
	defer mu.Unlock()

	p, err := dirFileLocked(historyFile)
	if err != nil {
		return err
	}
	return writeJSONAtomic(p, []HistoryEntry{})
}

// BuildHistoryEntry builds a HistoryEntry snapshot from a finished Report.
func BuildHistoryEntry(rep *dns.Report, adapterName string, systemDNS []string) HistoryEntry {
	entry := HistoryEntry{
		ID:          rep.StartedAt.UTC().Format("20060102-150405.000"),
		Timestamp:   rep.StartedAt,
		AdapterName: adapterName,
		SystemDNS:   append([]string(nil), systemDNS...),
		DurationMs:  rep.DurationMs,
		Intercepted: rep.Network.Intercepted,
	}
	if rep.Recommendation != nil {
		entry.WinnerID = rep.Recommendation.ResolverID
		entry.WinnerName = rep.Recommendation.Name
		entry.WinnerAvgMs = rep.Recommendation.AvgMs
		entry.CurrentAvgMs = rep.Recommendation.CurrentAvgMs
		entry.ImprovementPct = rep.Recommendation.ImprovementPct
	}
	for _, r := range rep.Results {
		if r.Status == "failed" || r.Status == "skipped" || !r.Overall.Valid() {
			continue
		}
		entry.TopResults = append(entry.TopResults, HistoryRow{
			Name:     r.Resolver.Name,
			Category: string(r.Resolver.Category),
			AvgMs:    r.Overall.Avg,
			P95Ms:    r.Overall.P95,
			JitterMs: r.Overall.Jitter,
			LossPct:  r.Overall.Loss,
		})
		if len(entry.TopResults) >= 5 {
			break
		}
	}
	return entry
}

func dirFileLocked(name string) (string, error) {
	dir := overrideDir
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, appDirName)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
