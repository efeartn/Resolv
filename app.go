// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package main

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"resolv/internal/config"
	"resolv/internal/dns"
	"resolv/internal/sysnet"
	"resolv/internal/updater"
)

const (
	AppVersion = "1.3.0"
	AppRepo    = "efeartn/Resolv"
	AppRepoURL = "https://github.com/efeartn/Resolv"
	AppLicense = "GNU General Public License v3.0 or later (GPL-3.0-or-later)"
)

// Bootstrap bundles everything the frontend needs on initial load or network refresh.
type Bootstrap struct {
	Version        string          `json:"version"`
	RepoURL        string          `json:"repoUrl"`
	License        string          `json:"license"`
	ConfigDir      string          `json:"configDir,omitempty"`
	Network        *sysnet.Info    `json:"network"`
	Resolvers      []dns.Resolver  `json:"resolvers"`
	DefaultOptions dns.Options     `json:"defaultOptions"`
	Settings       config.Settings `json:"settings"`
	LastReport     *dns.Report     `json:"lastReport,omitempty"`
}

// VerifyResult summarises a fast live latency probe against the current active system DNS.
type VerifyResult struct {
	Success        bool     `json:"success"`
	Message        string   `json:"message"`
	ResolverName   string   `json:"resolverName"`
	Addresses      []string `json:"addresses,omitempty"`
	AvgMs          float64  `json:"avgMs"`
	CachedAvgMs    float64  `json:"cachedAvgMs"`
	UncachedAvgMs  float64  `json:"uncachedAvgMs"`
	P95Ms          float64  `json:"p95Ms"`
	LossPct        float64  `json:"lossPct"`
	PreviousAvgMs  float64  `json:"previousAvgMs,omitempty"`
	ImprovementPct float64  `json:"improvementPct,omitempty"`
}

// ActionResult is returned by DNS mutation operations (Apply, Reset, Flush).
// When privilege elevation is cancelled or unavailable, NeedsManual is true
// and Manual + Addresses provide a one-click copy fallback for the user.
type ActionResult struct {
	Success     bool               `json:"success"`
	Message     string             `json:"message"`
	NeedsManual bool               `json:"needsManual,omitempty"`
	Cancelled   bool               `json:"cancelled,omitempty"`
	Addresses   []string           `json:"addresses,omitempty"`
	Manual      sysnet.ManualGuide `json:"manual,omitempty"`
	Network     *sysnet.Info       `json:"network,omitempty"`
}

// App is the Wails v2 backend controller bound to the frontend.
type App struct {
	ctx           context.Context
	mu            sync.Mutex
	netInfo       *sysnet.Info
	custom        []dns.Resolver
	settings      config.Settings
	lastReport    *dns.Report
	cancelRun     context.CancelFunc
	latestRelease *updater.ReleaseInfo
	updating      bool
}

// NewApp creates a new Resolv application controller.
func NewApp() *App {
	a := &App{
		settings: config.DefaultSettings(),
	}
	if s, err := config.LoadSettings(); err == nil && s != nil {
		a.settings = *s
	}
	if custom, err := config.LoadCustomResolvers(); err == nil && len(custom) > 0 {
		a.custom = custom
	}
	return a
}

// startup is called by Wails when the application window is ready.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// shutdown is called by Wails before the application exits.
func (a *App) shutdown(_ context.Context) {
	a.CancelBenchmark()
}

// GetBootstrap detects the active network interface and returns the initial UI state.
func (a *App) GetBootstrap() (*Bootstrap, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.netInfo == nil {
		info, _ := sysnet.Detect(a.baseCtx())
		a.netInfo = info
	}
	return a.bootstrapLocked(), nil
}

// RefreshNetwork re-detects the OS network interfaces, current DNS and gateway.
func (a *App) RefreshNetwork() (*Bootstrap, error) {
	info, err := sysnet.Detect(a.baseCtx())
	a.mu.Lock()
	defer a.mu.Unlock()
	oldID, newID := "", ""
	if a.netInfo != nil && a.netInfo.Active != nil {
		oldID = a.netInfo.Active.ID
	}
	if info != nil && info.Active != nil {
		newID = info.Active.ID
	}
	if oldID != newID {
		a.lastReport = nil
	}
	a.netInfo = info
	return a.bootstrapLocked(), err
}

// SelectInterface switches the target network interface by its OS identifier.
func (a *App) SelectInterface(id string) (*Bootstrap, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.netInfo == nil {
		return nil, sysnet.ErrNoInterface
	}
	for i := range a.netInfo.Interfaces {
		if a.netInfo.Interfaces[i].ID == id {
			chosen := a.netInfo.Interfaces[i]
			a.netInfo.Active = &chosen
			a.netInfo.SystemDNS = append([]string(nil), chosen.DNS...)
			a.netInfo.Gateway = chosen.Gateway
			a.lastReport = nil
			return a.bootstrapLocked(), nil
		}
	}
	return nil, fmt.Errorf("interface %q not found", id)
}

// AddCustomResolver validates and adds a user-defined DNS resolver.
func (a *App) AddCustomResolver(name, input string) ([]dns.Resolver, error) {
	r, err := dns.ParseCustom(name, input)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, existing := range a.custom {
		if existing.ID == r.ID {
			return a.resolversLocked(), nil
		}
	}
	a.custom = append(a.custom, r)
	_ = config.SaveCustomResolvers(a.custom)
	return a.resolversLocked(), nil
}

// RemoveCustomResolver deletes a user-defined resolver by ID.
func (a *App) RemoveCustomResolver(id string) ([]dns.Resolver, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]dns.Resolver, 0, len(a.custom))
	for _, r := range a.custom {
		if r.ID != id {
			out = append(out, r)
		}
	}
	a.custom = out
	_ = config.SaveCustomResolvers(a.custom)
	return a.resolversLocked(), nil
}

// RunBenchmark executes a multi-protocol DNS benchmark across all active resolvers.
func (a *App) RunBenchmark(opts dns.Options) (*dns.Report, error) {
	a.mu.Lock()
	if a.cancelRun != nil {
		a.cancelRun()
	}
	if a.netInfo == nil {
		info, _ := sysnet.Detect(a.baseCtx())
		a.netInfo = info
	}
	resolvers := a.resolversLocked()
	runCtx, cancel := context.WithCancel(a.baseCtx())
	a.cancelRun = cancel
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		cancel()
		a.cancelRun = nil
		a.mu.Unlock()
	}()

	report := dns.Run(runCtx, resolvers, opts, AppVersion, func(p dns.Progress) {
		a.emit("benchmark:progress", p)
	})

	a.mu.Lock()
	a.lastReport = report
	var adapterName string
	var sysDNS []string
	if a.netInfo != nil {
		if a.netInfo.Active != nil {
			adapterName = a.netInfo.Active.Name
		}
		sysDNS = append([]string(nil), a.netInfo.SystemDNS...)
	}
	a.mu.Unlock()

	if !report.Cancelled {
		entry := config.BuildHistoryEntry(report, adapterName, sysDNS)
		_, _ = config.AppendHistory(entry, 25)
	}

	return report, nil
}

// CancelBenchmark aborts a running benchmark, if any.
func (a *App) CancelBenchmark() {
	a.mu.Lock()
	if a.cancelRun != nil {
		a.cancelRun()
		a.cancelRun = nil
	}
	a.mu.Unlock()
}

// ApplyResolver applies a resolver's DNS addresses to the active network interface.
func (a *App) ApplyResolver(resolverID string) (*ActionResult, error) {
	a.mu.Lock()
	resolvers := a.resolversLocked()
	var iface *sysnet.Adapter
	if a.netInfo != nil && a.netInfo.Active != nil {
		cp := *a.netInfo.Active
		iface = &cp
	}
	a.mu.Unlock()

	var target *dns.Resolver
	for i := range resolvers {
		if resolvers[i].ID == resolverID {
			target = &resolvers[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("resolver %q not found", resolverID)
	}
	if !target.Applicable() {
		return nil, fmt.Errorf("%s does not provide standard IPv4/IPv6 addresses for OS-level switching", target.Name)
	}

	includeV6 := iface != nil && iface.HasIPv6
	addrs := target.Addresses(includeV6)
	return a.applyAddresses(iface, target.Name, addrs)
}

// ApplyAddresses applies an explicit slice of DNS server IP addresses to the active interface.
func (a *App) ApplyAddresses(label string, addresses []string) (*ActionResult, error) {
	a.mu.Lock()
	var iface *sysnet.Adapter
	if a.netInfo != nil && a.netInfo.Active != nil {
		cp := *a.netInfo.Active
		iface = &cp
	}
	a.mu.Unlock()

	if strings.TrimSpace(label) == "" {
		label = strings.Join(addresses, ", ")
	}
	return a.applyAddresses(iface, label, addresses)
}

func (a *App) applyAddresses(iface *sysnet.Adapter, label string, addrs []string) (*ActionResult, error) {
	manual := sysnet.Manual(iface, addrs)
	if iface == nil {
		return &ActionResult{
			Success:     false,
			Message:     "No active network interface detected. Copy the DNS addresses below to configure manually.",
			NeedsManual: true,
			Addresses:   addrs,
			Manual:      manual,
		}, nil
	}

	err := sysnet.ApplyDNS(a.baseCtx(), iface, addrs)
	if err != nil {
		cancelled := errors.Is(err, sysnet.ErrElevationCancelled)
		msg := fmt.Sprintf("Could not apply %s automatically (%v). You can copy the addresses below and apply them manually.", label, err)
		if cancelled {
			msg = fmt.Sprintf("Administrator prompt was cancelled. Copy the %s addresses below if you prefer to configure DNS manually.", label)
		}
		return &ActionResult{
			Success:     false,
			Message:     msg,
			NeedsManual: true,
			Cancelled:   cancelled,
			Addresses:   addrs,
			Manual:      manual,
		}, nil
	}

	info := a.refreshAfterChange()
	return &ActionResult{
		Success:   true,
		Message:   fmt.Sprintf("Applied %s (%s) to %s and flushed DNS cache.", label, strings.Join(addrs, ", "), iface.Name),
		Addresses: addrs,
		Manual:    manual,
		Network:   info,
	}, nil
}

// ResetDNS restores automatic (DHCP) DNS settings on the active interface.
func (a *App) ResetDNS() (*ActionResult, error) {
	a.mu.Lock()
	var iface *sysnet.Adapter
	if a.netInfo != nil && a.netInfo.Active != nil {
		cp := *a.netInfo.Active
		iface = &cp
	}
	a.mu.Unlock()

	if iface == nil {
		return &ActionResult{
			Success: false,
			Message: "No active network interface detected.",
		}, nil
	}

	if err := sysnet.ResetDNS(a.baseCtx(), iface); err != nil {
		cancelled := errors.Is(err, sysnet.ErrElevationCancelled)
		msg := fmt.Sprintf("Could not reset DNS to DHCP (%v).", err)
		if cancelled {
			msg = "Administrator prompt was cancelled; DNS settings were not changed."
		}
		return &ActionResult{
			Success:   false,
			Message:   msg,
			Cancelled: cancelled,
		}, nil
	}

	info := a.refreshAfterChange()
	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Reset %s to automatic (DHCP) DNS and flushed resolver cache.", iface.Name),
		Network: info,
	}, nil
}

// FlushCache flushes the OS DNS resolver cache.
func (a *App) FlushCache() (*ActionResult, error) {
	if err := sysnet.FlushCache(a.baseCtx()); err != nil {
		return &ActionResult{
			Success:   false,
			Message:   fmt.Sprintf("Could not flush DNS cache: %v", err),
			Cancelled: errors.Is(err, sysnet.ErrElevationCancelled),
		}, nil
	}
	return &ActionResult{
		Success: true,
		Message: "OS DNS resolver cache flushed.",
	}, nil
}

// ExportReport serialises the most recent benchmark report as "json", "csv", or "md".
func (a *App) ExportReport(format string) (string, error) {
	a.mu.Lock()
	r := a.lastReport
	a.mu.Unlock()
	return dns.Export(r, format)
}

// SaveReport opens a native save dialog and writes the exported report to disk.
func (a *App) SaveReport(format string) (string, error) {
	content, err := a.ExportReport(format)
	if err != nil {
		return "", err
	}
	if a.ctx == nil {
		return "", errors.New("save dialog is only available in desktop mode")
	}

	ext := strings.ToLower(format)
	filterName := "JSON Report (*.json)"
	filterPattern := "*.json"
	switch ext {
	case "csv":
		filterName = "CSV Spreadsheet (*.csv)"
		filterPattern = "*.csv"
	case "md", "markdown":
		ext = "md"
		filterName = "Markdown Document (*.md)"
		filterPattern = "*.md"
	default:
		ext = "json"
	}

	filename := fmt.Sprintf("resolv-report-%s.%s", time.Now().Format("20060102-150405"), ext)
	target, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export Resolv Benchmark Report",
		DefaultFilename: filename,
		Filters: []runtime.FileFilter{
			{DisplayName: filterName, Pattern: filterPattern},
		},
	})
	if err != nil {
		return "", err
	}
	if target == "" {
		return "", nil
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return "", err
	}
	return target, nil
}

func (a *App) refreshAfterChange() *sysnet.Info {
	a.mu.Lock()
	prevID := ""
	if a.netInfo != nil && a.netInfo.Active != nil {
		prevID = a.netInfo.Active.ID
	}
	prevInfo := a.netInfo
	a.mu.Unlock()

	info, err := sysnet.Detect(a.baseCtx())
	if err != nil || info == nil {
		return prevInfo
	}
	if prevID != "" {
		for i := range info.Interfaces {
			if info.Interfaces[i].ID == prevID {
				chosen := info.Interfaces[i]
				info.Active = &chosen
				if len(chosen.DNS) > 0 {
					info.SystemDNS = append([]string(nil), chosen.DNS...)
				}
				if chosen.Gateway != "" {
					info.Gateway = chosen.Gateway
				}
				break
			}
		}
	}
	a.mu.Lock()
	a.netInfo = info
	a.mu.Unlock()
	return info
}

func (a *App) bootstrapLocked() *Bootstrap {
	cfgDir, _ := config.Dir()
	return &Bootstrap{
		Version:        AppVersion,
		RepoURL:        AppRepoURL,
		License:        AppLicense,
		ConfigDir:      cfgDir,
		Network:        a.netInfo,
		Resolvers:      a.resolversLocked(),
		DefaultOptions: a.settings.Options,
		Settings:       a.settings,
		LastReport:     a.lastReport,
	}
}

// QuickVerifyCurrentDNS runs a fast live latency probe against the active OS DNS.
func (a *App) QuickVerifyCurrentDNS() (*VerifyResult, error) {
	info, err := sysnet.Detect(a.baseCtx())
	a.mu.Lock()
	if err == nil && info != nil {
		a.netInfo = info
	}
	var sysAddrs []string
	if a.netInfo != nil {
		if len(a.netInfo.SystemDNS) > 0 {
			sysAddrs = append([]string(nil), a.netInfo.SystemDNS...)
		} else if a.netInfo.Active != nil && len(a.netInfo.Active.DNS) > 0 {
			sysAddrs = append([]string(nil), a.netInfo.Active.DNS...)
		} else if a.netInfo.Gateway != "" {
			sysAddrs = []string{a.netInfo.Gateway}
		}
	}
	var prevAvg float64
	if a.lastReport != nil {
		for _, r := range a.lastReport.Results {
			if r.Resolver.Source == dns.SourceSystem && r.Overall.Valid() {
				prevAvg = r.Overall.Avg
				break
			}
		}
	}
	opts := a.settings.Options
	a.mu.Unlock()

	if len(sysAddrs) == 0 {
		return &VerifyResult{
			Success: false,
			Message: "No active DNS server detected to verify.",
		}, nil
	}

	sysRes, ok := dns.FromAddresses("verify-current", "Current System DNS", dns.CategorySystem, dns.SourceSystem, sysAddrs)
	if !ok {
		return &VerifyResult{
			Success:   false,
			Message:   "Current DNS addresses cannot be probed directly.",
			Addresses: sysAddrs,
		}, nil
	}
	enrichFromBuiltin(&sysRes)

	qctx, cancel := context.WithTimeout(a.baseCtx(), 6*time.Second)
	defer cancel()
	res := dns.QuickVerify(qctx, sysRes, opts)
	if !res.Overall.Valid() {
		return &VerifyResult{
			Success:      false,
			ResolverName: sysRes.Description,
			Addresses:    sysAddrs,
			LossPct:      res.Overall.Loss,
			Message:      fmt.Sprintf("Verification failed for %s (100%% loss).", strings.Join(sysAddrs, ", ")),
		}, nil
	}

	vr := &VerifyResult{
		Success:       true,
		ResolverName:  sysRes.Description,
		Addresses:     sysAddrs,
		AvgMs:         res.Overall.Avg,
		CachedAvgMs:   res.Cached.Avg,
		UncachedAvgMs: res.Uncached.Avg,
		P95Ms:         res.Overall.P95,
		LossPct:       res.Overall.Loss,
		PreviousAvgMs: prevAvg,
	}
	if prevAvg > 0 && res.Overall.Avg > 0 {
		vr.ImprovementPct = math.Round(((prevAvg-res.Overall.Avg)/prevAvg)*100*10) / 10
	}
	nameLabel := strings.Join(sysAddrs, ", ")
	if sysRes.Description != "" && strings.HasPrefix(sysRes.Description, "Currently using ") {
		nameLabel = strings.TrimSuffix(strings.TrimPrefix(sysRes.Description, "Currently using "), ".")
	}
	vr.ResolverName = nameLabel
	vr.Message = fmt.Sprintf("Verified %s: %.1f ms avg (cached %.1f ms · uncached %.1f ms · %.0f%% loss)",
		nameLabel, vr.AvgMs, vr.CachedAvgMs, vr.UncachedAvgMs, vr.LossPct)
	return vr, nil
}

// GetSettings returns the user's persistent application settings.
func (a *App) GetSettings() (*config.Settings, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cp := a.settings
	return &cp, nil
}

// SaveSettings validates and atomically persists application settings.
func (a *App) SaveSettings(s config.Settings) (*config.Settings, error) {
	s.Normalize()
	if err := config.SaveSettings(&s); err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.settings = s
	a.mu.Unlock()
	cp := s
	return &cp, nil
}

// GetHistory returns stored benchmark history runs, newest first.
func (a *App) GetHistory() ([]config.HistoryEntry, error) {
	return config.LoadHistory()
}

// ClearHistory deletes all saved benchmark history entries.
func (a *App) ClearHistory() error {
	return config.ClearHistory()
}

// CheckForUpdates checks GitHub Releases for a newer version matching the current OS.
func (a *App) CheckForUpdates() (*updater.ReleaseInfo, error) {
	ctx, cancel := context.WithTimeout(a.baseCtx(), 15*time.Second)
	defer cancel()
	info, err := updater.CheckForUpdates(ctx, AppVersion, AppRepo)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.latestRelease = info
	a.mu.Unlock()
	return info, nil
}

// DownloadAndApplyUpdate downloads, SHA-256 verifies, and installs the latest release, then restarts Resolv.
func (a *App) DownloadAndApplyUpdate() (*ActionResult, error) {
	a.mu.Lock()
	if a.updating {
		a.mu.Unlock()
		return &ActionResult{Success: false, Message: "An update installation is already in progress."}, nil
	}
	a.updating = true
	info := a.latestRelease
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.updating = false
		a.mu.Unlock()
	}()

	if info == nil || !info.IsAvailable {
		fresh, err := a.CheckForUpdates()
		if err != nil {
			return nil, err
		}
		info = fresh
	}

	if !info.IsAvailable {
		return &ActionResult{Success: true, Message: "Resolv is already running the latest version."}, nil
	}
	if !info.AssetReady {
		msg := info.UpdateNote
		if msg == "" {
			msg = "Release binary is not ready for automatic download yet."
		}
		return &ActionResult{Success: false, Message: msg}, nil
	}

	dlCtx, cancel := context.WithTimeout(a.baseCtx(), 5*time.Minute)
	defer cancel()

	if err := updater.DownloadAndApplyUpdate(dlCtx, info, func(p updater.UpdateProgress) {
		a.emit("update:progress", p)
	}); err != nil {
		return &ActionResult{
			Success: false,
			Message: err.Error(),
		}, nil
	}

	// Delay shutdown briefly so the RPC resolves cleanly in the UI before the swap helper replaces the binary.
	time.AfterFunc(350*time.Millisecond, func() {
		if a.ctx != nil {
			runtime.Quit(a.ctx)
		}
		time.Sleep(1500 * time.Millisecond)
		os.Exit(0)
	})

	return &ActionResult{
		Success: true,
		Message: fmt.Sprintf("Update v%s verified (SHA-256) — restarting Resolv...", info.Version),
	}, nil
}

// resolversLocked builds the ordered resolver list:
//  1. Current System DNS (auto-detected)
//  2. Local Router / Gateway DNS (auto-detected, if distinct from system DNS)
//  3. Curated built-in public resolvers
//  4. User-added custom resolvers
func (a *App) resolversLocked() []dns.Resolver {
	var out []dns.Resolver

	var sysAddrs []string
	var gwAddr string
	if a.netInfo != nil {
		sysAddrs = a.netInfo.SystemDNS
		gwAddr = a.netInfo.Gateway
	}

	if sysRes, ok := dns.FromAddresses("system-dns", "Current System DNS", dns.CategorySystem, dns.SourceSystem, sysAddrs); ok {
		sysRes.Description = "Active DNS server(s) currently configured on your operating system."
		enrichFromBuiltin(&sysRes)
		out = append(out, sysRes)
	}

	if gwAddr != "" {
		if gwRes, ok := dns.FromAddresses("gateway-dns", "Local Router / Gateway", dns.CategorySystem, dns.SourceGateway, []string{gwAddr}); ok {
			if len(out) == 0 || !samePrimary(out[0], gwRes) {
				gwRes.Description = "Default gateway DNS forwarder on your local network."
				out = append(out, gwRes)
			}
		}
	}

	out = append(out, dns.Builtin()...)
	out = append(out, a.custom...)
	return out
}

// enrichFromBuiltin attaches known DoH/DoT endpoints when the user's current
// system DNS already points to one of the curated public providers, enabling
// UDP-vs-DoH hijack comparison on the system resolver itself.
func enrichFromBuiltin(r *dns.Resolver) {
	primary := r.PrimaryAddr()
	if primary == "" {
		return
	}
	for _, b := range dns.Builtin() {
		for _, ip := range append(append([]string{}, b.IPv4...), b.IPv6...) {
			if ip == primary {
				r.DoH = b.DoH
				r.DoT = b.DoT
				r.Description = fmt.Sprintf("Currently using %s (%s).", b.Name, primary)
				return
			}
		}
	}
}

func samePrimary(a, b dns.Resolver) bool {
	return a.PrimaryAddr() != "" && a.PrimaryAddr() == b.PrimaryAddr()
}

func (a *App) baseCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) emit(event string, data any) {
	if a.ctx == nil {
		return
	}
	defer func() { _ = recover() }()
	runtime.EventsEmit(a.ctx, event, data)
}
