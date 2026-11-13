// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

(() => {
  'use strict';

  // Application UI State
  const state = {
    version: '1.1.1',
    mode: 'simple',            // 'simple' | 'pro'
    categoryFilter: 'all',     // 'all' | 'Privacy' | 'Ad-blocking' | 'Standard' | 'Family' | 'Custom'
    searchQuery: '',           // free-text filter
    sortBy: 'rank',            // 'rank' | 'avg' | 'p95' | 'cached' | 'uncached' | 'jitter'
    running: false,
    expandedIds: new Set(),    // Resolver IDs expanded in Simple Mode
    network: null,
    resolvers: [],
    report: null,
    liveResults: null,         // Streaming partial results during benchmark
    verifyResult: null,        // Latest QuickVerifyCurrentDNS measurement
    updateInfo: null,          // Cached GitHub release check metadata
    updating: false,
    configDir: '',
    settings: {
      viewMode: 'simple',
      categoryFilter: 'all',
      sortBy: 'rank',
      autoCheckUpdates: true,
      options: {
        queries: 10,
        timeoutMs: 2000,
        workers: 12,
        staggerMs: 12,
        protocols: ['udp', 'doh'],
        cachedDomains: ['cloudflare.com', 'google.com', 'apple.com'],
        uncachedBase: 'resbench.invalid',
        checkIntegrity: true
      }
    },
    defaultOptions: {
      queries: 10,
      timeoutMs: 2000,
      workers: 12,
      staggerMs: 12,
      protocols: ['udp', 'doh'],
      cachedDomains: ['cloudflare.com', 'google.com', 'apple.com'],
      uncachedBase: 'resbench.invalid',
      checkIntegrity: true
    },
    currentExportFormat: 'md'
  };

  // Wails Backend Bridge (with graceful browser-preview fallback if opened outside Wails)
  const hasWails = () => Boolean(window.go && window.go.main && window.go.main.App);
  const backend = {
    async GetBootstrap() {
      if (hasWails()) return window.go.main.App.GetBootstrap();
      return demoBridge.getBootstrap();
    },
    async RefreshNetwork() {
      if (hasWails()) return window.go.main.App.RefreshNetwork();
      return demoBridge.getBootstrap();
    },
    async SelectInterface(id) {
      if (hasWails()) return window.go.main.App.SelectInterface(id);
      return demoBridge.selectInterface(id);
    },
    async AddCustomResolver(name, input) {
      if (hasWails()) return window.go.main.App.AddCustomResolver(name, input);
      return demoBridge.addCustomResolver(name, input);
    },
    async RemoveCustomResolver(id) {
      if (hasWails()) return window.go.main.App.RemoveCustomResolver(id);
      return demoBridge.removeCustomResolver(id);
    },
    async RunBenchmark(opts) {
      if (hasWails()) return window.go.main.App.RunBenchmark(opts);
      return demoBridge.runBenchmark(opts, handleProgress);
    },
    async CancelBenchmark() {
      if (hasWails()) return window.go.main.App.CancelBenchmark();
      demoBridge.cancel();
    },
    async ApplyResolver(resolverID) {
      if (hasWails()) return window.go.main.App.ApplyResolver(resolverID);
      return demoBridge.applyResolver(resolverID);
    },
    async ResetDNS() {
      if (hasWails()) return window.go.main.App.ResetDNS();
      return demoBridge.resetDNS();
    },
    async FlushCache() {
      if (hasWails()) return window.go.main.App.FlushCache();
      return { success: true, message: 'OS DNS resolver cache flushed.' };
    },
    async QuickVerifyCurrentDNS() {
      if (hasWails()) return window.go.main.App.QuickVerifyCurrentDNS();
      return demoBridge.quickVerify();
    },
    async GetSettings() {
      if (hasWails()) return window.go.main.App.GetSettings();
      return demoBridge.getSettings();
    },
    async SaveSettings(s) {
      if (hasWails()) return window.go.main.App.SaveSettings(s);
      return demoBridge.saveSettings(s);
    },
    async GetHistory() {
      if (hasWails()) return window.go.main.App.GetHistory();
      return demoBridge.getHistory();
    },
    async ClearHistory() {
      if (hasWails()) return window.go.main.App.ClearHistory();
      return demoBridge.clearHistory();
    },
    async CheckForUpdates() {
      if (hasWails()) return window.go.main.App.CheckForUpdates();
      return demoBridge.checkForUpdates();
    },
    async DownloadAndApplyUpdate() {
      if (hasWails()) return window.go.main.App.DownloadAndApplyUpdate();
      return demoBridge.downloadAndApplyUpdate();
    },
    async ExportReport(format) {
      if (hasWails()) return window.go.main.App.ExportReport(format);
      return demoBridge.exportReport(state.report, format);
    },
    async SaveReport(format) {
      if (hasWails()) return window.go.main.App.SaveReport(format);
      const content = await this.ExportReport(format);
      const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `resolv-report.${format}`;
      a.click();
      URL.revokeObjectURL(url);
      return a.download;
    }
  };

  // DOM helpers
  const $ = (id) => document.getElementById(id);

  function escapeHTML(str) {
    return String(str ?? '')
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  // Latency Tier Styling (<20ms Emerald, 20-60ms Amber, >60ms Rose)
  function latencyBadgeClass(ms, failed = false) {
    if (failed || ms === undefined || ms === null || ms <= 0) return 'badge-neutral';
    if (ms < 20) return 'badge-emerald';
    if (ms <= 60) return 'badge-amber';
    return 'badge-rose';
  }

  function latencyHexColor(ms, failed = false) {
    if (failed || !ms || ms <= 0) return '#52525b';
    if (ms < 20) return '#10b981';
    if (ms <= 60) return '#f59e0b';
    return '#f43f5e';
  }

  // Pure SVG Range Bar (Min — Avg — P95 — Max)
  function renderRangeBarSVG(overall, maxScaleMs, width = 240, height = 22) {
    if (!overall || !overall.received || overall.received <= 0) {
      return `<svg viewBox="0 0 ${width} ${height}" width="100%" height="${height}" class="block">
        <line x1="6" y1="${height / 2}" x2="${width - 6}" y2="${height / 2}" stroke="#27272a" stroke-width="2" stroke-linecap="round"/>
      </svg>`;
    }
    const pad = 8;
    const usable = width - pad * 2;
    const cap = Math.max(maxScaleMs || 100, overall.maxMs || 1, 25);
    const toX = (ms) => pad + Math.min(1, Math.max(0, ms / cap)) * usable;

    const xMin = toX(overall.minMs);
    const xAvg = toX(overall.avgMs);
    const xP95 = toX(overall.p95Ms || overall.maxMs);
    const xMax = Math.max(xMin + 2, toX(overall.maxMs));
    const cy = height / 2;
    const color = latencyHexColor(overall.avgMs);

    return `<svg viewBox="0 0 ${width} ${height}" width="100%" height="${height}" class="block" role="img"
      aria-label="Min ${overall.minMs.toFixed(1)}ms, Avg ${overall.avgMs.toFixed(1)}ms, P95 ${(overall.p95Ms || overall.maxMs).toFixed(1)}ms, Max ${overall.maxMs.toFixed(1)}ms">
      <title>Min ${overall.minMs.toFixed(1)} ms · Avg ${overall.avgMs.toFixed(1)} ms · P95 ${(overall.p95Ms || overall.maxMs).toFixed(1)} ms · Max ${overall.maxMs.toFixed(1)} ms</title>
      <!-- Baseline Track -->
      <line x1="${pad}" y1="${cy}" x2="${width - pad}" y2="${cy}" stroke="#1f1f23" stroke-width="2"/>
      <!-- Full Min..Max Whisker Span -->
      <line x1="${xMin.toFixed(1)}" y1="${cy}" x2="${xMax.toFixed(1)}" y2="${cy}" stroke="#3f3f46" stroke-width="2"/>
      <line x1="${xMin.toFixed(1)}" y1="${cy - 4}" x2="${xMin.toFixed(1)}" y2="${cy + 4}" stroke="#52525b" stroke-width="1.5"/>
      <line x1="${xMax.toFixed(1)}" y1="${cy - 4}" x2="${xMax.toFixed(1)}" y2="${cy + 4}" stroke="#52525b" stroke-width="1.5"/>
      <!-- Min..P95 Core Band -->
      <rect x="${xMin.toFixed(1)}" y="${cy - 2.5}" width="${Math.max(2, xP95 - xMin).toFixed(1)}" height="5" rx="2" fill="${color}" fill-opacity="0.35"/>
      <!-- P95 Tick -->
      <line x1="${xP95.toFixed(1)}" y1="${cy - 5}" x2="${xP95.toFixed(1)}" y2="${cy + 5}" stroke="${color}" stroke-width="1.5"/>
      <!-- Avg Marker Dot -->
      <circle cx="${xAvg.toFixed(1)}" cy="${cy}" r="4" fill="#09090b" stroke="${color}" stroke-width="2.2"/>
    </svg>`;
  }

  // Pure SVG Chronological Latency Sparkline
  function renderSparklineSVG(samples, avgMs, width = 128, height = 24) {
    if (!Array.isArray(samples) || samples.length < 2) {
      return `<svg viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" class="block">
        <line x1="2" y1="${height / 2}" x2="${width - 2}" y2="${height / 2}" stroke="#27272a" stroke-width="1.5" stroke-dasharray="2 3"/>
      </svg>`;
    }
    const min = Math.min(...samples);
    const max = Math.max(...samples);
    const span = Math.max(max - min, 1);
    const padX = 3;
    const padY = 4;
    const w = width - padX * 2;
    const h = height - padY * 2;

    const pts = samples.map((v, idx) => {
      const x = padX + (idx / (samples.length - 1)) * w;
      const y = padY + h - ((v - min) / span) * h;
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    }).join(' ');

    const color = latencyHexColor(avgMs);
    const last = samples[samples.length - 1];
    const lastY = padY + h - ((last - min) / span) * h;

    return `<svg viewBox="0 0 ${width} ${height}" width="${width}" height="${height}" class="block" role="img"
      aria-label="Latency sparkline (${samples.length} samples)">
      <title>Query RTT sequence (${samples.length} samples): ${samples.map((s) => s.toFixed(1) + 'ms').join(' → ')}</title>
      <polyline points="${pts}" fill="none" stroke="${color}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/>
      <circle cx="${(width - padX).toFixed(1)}" cy="${lastY.toFixed(1)}" r="2.2" fill="${color}"/>
    </svg>`;
  }

  function categoryBadgeHTML(cat, deprecated = false) {
    const map = {
      'Privacy': 'bg-cyan-950/60 border-cyan-800/60 text-cyan-300',
      'Ad-blocking': 'bg-purple-950/60 border-purple-800/60 text-purple-300',
      'Standard': 'bg-zinc-800/80 border-zinc-700 text-zinc-300',
      'Family': 'bg-amber-950/60 border-amber-800/60 text-amber-300',
      'System': 'bg-emerald-950/60 border-emerald-800/60 text-emerald-300',
      'Custom': 'bg-indigo-950/60 border-indigo-800/60 text-indigo-300'
    };
    const cls = map[cat] || map['Standard'];
    let html = `<span class="text-[10px] font-mono px-1.5 py-0.5 rounded border ${cls}">${escapeHTML(cat)}</span>`;
    if (deprecated) {
      html += ` <span class="text-[10px] font-mono px-1.5 py-0.5 rounded border bg-amber-950/70 border-amber-800/70 text-amber-300" title="Discontinued endpoint">Deprecated</span>`;
    }
    return html;
  }

  function dnssecBadgeHTML(dnssec) {
    switch (dnssec) {
      case 'validating':
        return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-emerald-950/60 border border-emerald-800/60 text-emerald-300" title="Resolver validates DNSSEC signatures and rejects bogus responses">✓ DNSSEC</span>`;
      case 'not-validating':
        return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-amber-950/60 border border-amber-800/60 text-amber-300" title="Resolver did not reject broken DNSSEC signatures">No DNSSEC</span>`;
      case 'partial':
        return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-zinc-800 border border-zinc-700 text-zinc-300">Partial</span>`;
      default:
        return `<span class="text-[10px] font-mono text-zinc-500">—</span>`;
    }
  }

  function adBlockBadgeHTML(filtering) {
    if (!filtering || !filtering.checked) {
      return `<span class="text-[10px] font-mono text-zinc-500">—</span>`;
    }
    if (filtering.blocksAds) {
      return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-purple-950/60 border border-purple-800/60 text-purple-300" title="Sinkholed ${filtering.blockedCount}/${filtering.testedCount} ad/tracker canary domains (${escapeHTML(filtering.method || 'sinkhole')})">🛡 ${filtering.blockedCount}/${filtering.testedCount} Ads</span>`;
    }
    return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-zinc-900 border border-zinc-800 text-zinc-400" title="Does not filter ad/tracker domains">Unfiltered</span>`;
  }

  function hijackBadgeHTML(hijack) {
    if (!hijack || !hijack.status || hijack.status === 'unknown') {
      return `<span class="text-[10px] font-mono text-zinc-500">—</span>`;
    }
    if (hijack.status === 'clean') {
      return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-emerald-950/60 border border-emerald-800/60 text-emerald-300" title="Plain DNS matches encrypted DoH responses">✓ Verified</span>`;
    }
    if (hijack.status === 'suspicious') {
      return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-amber-950/70 border border-amber-700/70 text-amber-300">⚠ Suspicious</span>`;
    }
    return `<span class="inline-flex items-center gap-1 text-[10px] font-mono px-1.5 py-0.5 rounded bg-rose-950/80 border border-rose-700 text-rose-200 font-semibold">⚠ Hijacked</span>`;
  }

  function showToast(message, kind = 'info') {
    const el = $('statusToast');
    const txt = $('statusToastText');
    const styles = {
      success: 'bg-emerald-950/60 border-emerald-800/80 text-emerald-200',
      error: 'bg-rose-950/60 border-rose-800/80 text-rose-200',
      info: 'bg-zinc-900 border-zinc-800 text-zinc-200'
    };
    el.className = `rounded-xl border px-4 py-3 text-xs flex items-center justify-between gap-3 ${styles[kind] || styles.info}`;
    txt.textContent = message;
    el.classList.remove('hidden');
  }

  // Render Network Header Card & Offline State
  function renderNetwork() {
    const net = state.network;
    if (!net) return;

    $('osNameValue').textContent = net.osName || net.os || 'Unknown OS';
    $('hostnameValue').textContent = `${net.hostname || 'localhost'} · ${net.arch || ''}`;
    $('netBackendBadge').textContent = net.backend || net.os;

    $('elevatedBadge').classList.toggle('hidden', !net.elevated);

    const isOffline = net.online === false || navigator.onLine === false;
    $('offlineBadge').classList.toggle('hidden', !isOffline);
    $('offlineBanner').classList.toggle('hidden', !isOffline && (!net.warnings || net.warnings.length === 0));
    if (isOffline) {
      $('offlineBannerText').textContent = 'No active network route detected. Connect to Wi-Fi or Ethernet and click Re-check Network.';
    } else if (net.warnings && net.warnings.length > 0) {
      $('offlineBannerText').textContent = net.warnings.join(' ');
    }

    const sel = $('interfaceSelect');
    sel.innerHTML = '';
    const interfaces = Array.isArray(net.interfaces) ? net.interfaces : [];
    if (interfaces.length === 0) {
      const opt = document.createElement('option');
      opt.value = '';
      opt.textContent = 'No active interface found';
      sel.appendChild(opt);
    } else {
      interfaces.forEach((iface) => {
        const opt = document.createElement('option');
        opt.value = iface.id;
        opt.textContent = `${iface.name} (${iface.device || iface.kind || iface.id})`;
        if (net.active && net.active.id === iface.id) opt.selected = true;
        sel.appendChild(opt);
      });
    }

    const active = net.active;
    if (active) {
      const ip4 = (active.ipv4 && active.ipv4[0]) || 'no IPv4';
      const gw = active.gateway || net.gateway || 'no gateway';
      $('interfaceMetaValue').textContent = `${ip4} · GW ${gw}`;
      $('dnsModeBadge').textContent = active.dnsManual ? 'Static' : 'DHCP / Auto';
    } else {
      $('interfaceMetaValue').textContent = '—';
      $('dnsModeBadge').textContent = '—';
    }

    const sysDns = (net.systemDns && net.systemDns.length > 0)
      ? net.systemDns.join(', ')
      : (active && active.dns && active.dns.length > 0 ? active.dns.join(', ') : 'None detected');
    $('currentDnsValue').textContent = sysDns;
    renderVerifyBadge();
  }

  function renderVerifyBadge() {
    const el = $('verifyResultBadge');
    if (!el) return;
    const v = state.verifyResult;
    if (!v || !v.success) {
      el.classList.add('hidden');
      return;
    }
    const cls = latencyBadgeClass(v.avgMs);
    let deltaText = '';
    if (v.improvementPct && v.improvementPct >= 2) {
      deltaText = ` (-${Math.round(v.improvementPct)}%)`;
    } else if (v.improvementPct && v.improvementPct <= -5) {
      deltaText = ` (+${Math.round(-v.improvementPct)}%)`;
    }
    el.className = `${cls} font-mono text-[10px] px-1.5 py-0.5 rounded`;
    el.textContent = `${v.avgMs.toFixed(1)} ms${deltaText}`;
    el.title = v.message || 'Verified live DNS speed';
    el.classList.remove('hidden');
  }

  // Render Winner, KPI Telemetry Strip & Integrity Banners
  function renderBanners() {
    const report = state.report;
    const winnerEl = $('winnerBanner');
    const hijackEl = $('hijackBanner');
    const kpiEl = $('kpiSummaryStrip');

    if (!report) {
      winnerEl.classList.add('hidden');
      hijackEl.classList.add('hidden');
      if (kpiEl) kpiEl.classList.add('hidden');
      $('exportMenuBtn').disabled = true;
      return;
    }

    $('exportMenuBtn').disabled = false;

    // Populate KPI Telemetry Strip
    const validResults = (report.results || []).filter(
      (r) => r.status !== 'failed' && r.status !== 'skipped' && r.status !== 'pending' && r.overall && r.overall.received > 0
    );
    if (kpiEl && validResults.length > 0) {
      kpiEl.classList.remove('hidden');
      const byAvg = [...validResults].sort((a, b) => a.overall.avgMs - b.overall.avgMs)[0];
      const byCached = [...validResults].filter((r) => r.cached && r.cached.received > 0).sort((a, b) => a.cached.avgMs - b.cached.avgMs)[0];
      const byUncached = [...validResults].filter((r) => r.uncached && r.uncached.received > 0).sort((a, b) => a.uncached.avgMs - b.uncached.avgMs)[0];
      const dnssecCount = validResults.filter((r) => r.dnssec === 'validating').length;
      const adBlockCount = validResults.filter((r) => r.filtering && r.filtering.blocksAds).length;

      $('kpiFastestVal').textContent = `${byAvg.resolver.name} · ${byAvg.overall.avgMs.toFixed(1)} ms`;
      $('kpiFastestSub').textContent = `P95 ${(byAvg.overall.p95Ms || byAvg.overall.maxMs).toFixed(1)} ms · Jitter ${byAvg.overall.jitterMs.toFixed(1)} ms`;

      if (byCached) {
        $('kpiCachedVal').textContent = `${byCached.resolver.name} · ${byCached.cached.avgMs.toFixed(1)} ms`;
        $('kpiCachedSub').textContent = `Min ${byCached.cached.minMs.toFixed(1)} ms edge hit`;
      }
      if (byUncached) {
        $('kpiUncachedVal').textContent = `${byUncached.resolver.name} · ${byUncached.uncached.avgMs.toFixed(1)} ms`;
        $('kpiUncachedSub').textContent = `True recursive authority lookup`;
      }
      $('kpiSecurityVal').textContent = `${dnssecCount}/${validResults.length} DNSSEC · ${adBlockCount} AdBlock`;
      $('kpiSecuritySub').textContent = report.network && report.network.intercepted
        ? '⚠ Port 53 Intercepted'
        : '✓ Port 53 Unmodified';
    }

    if (report.network && (report.network.intercepted || report.network.hijackedCount > 0)) {
      hijackEl.classList.remove('hidden');
      $('hijackCountBadge').textContent = report.network.intercepted
        ? 'Port 53 Intercepted'
        : `${report.network.hijackedCount} Flagged`;
      $('hijackSummaryText').textContent = report.network.summary || '';
    } else {
      hijackEl.classList.add('hidden');
    }

    const rec = report.recommendation;
    if (rec) {
      winnerEl.classList.remove('hidden');
      $('winnerAvgBadge').textContent = `${rec.avgMs.toFixed(1)} ms avg`;
      $('winnerHeadline').textContent = rec.headline;
      $('winnerDetail').textContent = rec.detail || '';
      $('winnerApplyBtn').dataset.resolverId = rec.resolverId;
      $('winnerCopyBtn').dataset.resolverId = rec.resolverId;
      $('winnerApplyBtn').classList.toggle('hidden', Boolean(rec.currentIsFastest));
    } else {
      winnerEl.classList.add('hidden');
    }
  }

  // Build unified items list (streaming partial results, finished report, or pre-benchmark resolver list)
  function getDisplayItems() {
    const cat = state.categoryFilter;
    const q = (state.searchQuery || '').toLowerCase().trim();
    const matchesSearch = (r) => {
      if (!q) return true;
      const hay = [r.name, r.category, ...(r.ipv4 || []), ...(r.ipv6 || []), r.doh || '', r.dot || ''].join(' ').toLowerCase();
      return hay.includes(q);
    };

    const sourceResults = (state.running && Array.isArray(state.liveResults) && state.liveResults.length > 0)
      ? state.liveResults
      : (state.report && Array.isArray(state.report.results) && state.report.results.length > 0 ? state.report.results : null);

    if (sourceResults) {
      const filtered = sourceResults.filter((r) => (cat === 'all' || r.resolver.category === cat) && matchesSearch(r.resolver));
      const metric = (item) => {
        if (item.status === 'pending') return 999997;
        if (item.status === 'failed') return 999998;
        if (item.status === 'skipped') return 999999;
        if (!item.overall || item.overall.received === 0) return 999997;
        switch (state.sortBy) {
          case 'avg': return item.overall.avgMs;
          case 'p95': return item.overall.p95Ms || item.overall.maxMs;
          case 'cached': return item.cached && item.cached.received > 0 ? item.cached.avgMs : 999996;
          case 'uncached': return item.uncached && item.uncached.received > 0 ? item.uncached.avgMs : 999996;
          case 'jitter': return item.overall.jitterMs;
          default: return item.rank || 999995;
        }
      };
      return [...filtered].sort((a, b) => metric(a) - metric(b));
    }
    return state.resolvers
      .filter((r) => (cat === 'all' || r.category === cat) && matchesSearch(r))
      .map((r, idx) => ({
        resolver: r,
        rank: idx + 1,
        status: 'pending',
        primary: '',
        overall: null,
        cached: null,
        uncached: null,
        protocols: {},
        dnssec: 'unknown',
        filtering: { checked: false, blocksAds: false },
        hijack: { status: 'unknown' }
      }));
  }

  function renderTablesOnly() {
    const items = getDisplayItems();
    let maxCeiling = 80;
    items.forEach((item) => {
      if (item.overall && item.overall.maxMs > maxCeiling) {
        maxCeiling = item.overall.maxMs;
      }
    });
    renderSimpleList(items, maxCeiling);
    renderProTable(items, maxCeiling);
  }

  function renderResults() {
    renderBanners();
    renderTablesOnly();
  }

  function renderSimpleList(items, maxCeiling) {
    const container = $('simpleListContainer');
    if (items.length === 0) {
      container.innerHTML = `<div class="rounded-xl bg-[#121215] border border-zinc-800/80 p-8 text-center text-xs text-zinc-400">No resolvers match the selected category filter.</div>`;
      return;
    }

    container.innerHTML = items.map((item) => {
      const r = item.resolver;
      const isSkipped = item.status === 'skipped';
      const isFailed = item.status === 'failed';
      const isPending = item.status === 'pending' || (!isSkipped && !isFailed && (!item.overall || item.overall.received === 0));
      const avg = item.overall ? item.overall.avgMs : 0;
      const expanded = state.expandedIds.has(r.id);
      const endpoint = (r.ipv4 && r.ipv4.length > 0)
        ? r.ipv4.join(', ')
        : (r.doh || r.dot || '—');
      const canApply = Array.isArray(r.ipv4) && r.ipv4.length > 0 && !r.deprecated;

      return `
      <div class="resolver-card rounded-xl bg-[#121215] border border-zinc-800/90 overflow-hidden ${isSkipped ? 'opacity-65' : ''}">
        <!-- Main Row -->
        <div class="px-4 py-3 flex flex-col md:flex-row md:items-center justify-between gap-3 cursor-pointer" data-toggle-row="${escapeHTML(r.id)}">
          <!-- Left: Rank + Name + Badges -->
          <div class="flex items-center gap-3 min-w-0 md:w-[36%]">
            <div class="w-7 h-7 rounded-lg bg-zinc-900 border border-zinc-800 flex items-center justify-center font-mono text-xs text-zinc-400 shrink-0">
              ${isSkipped ? '—' : isPending ? '·' : `#${item.rank}`}
            </div>
            <div class="min-w-0 flex-1">
              <div class="flex flex-wrap items-center gap-1.5">
                <span class="font-semibold text-sm text-zinc-100 truncate">${escapeHTML(r.name)}</span>
                ${categoryBadgeHTML(r.category, r.deprecated)}
                ${item.filtering && item.filtering.blocksAds ? adBlockBadgeHTML(item.filtering) : ''}
                ${item.hijack && item.hijack.status === 'hijacked' ? hijackBadgeHTML(item.hijack) : ''}
              </div>
              <div class="mt-0.5 text-[11px] font-mono text-zinc-400 truncate">${escapeHTML(endpoint)}</div>
            </div>
          </div>

          <!-- Center: Pure SVG Min–Avg–P95–Max Range Bar + Sparkline -->
          <div class="flex-1 flex items-center gap-3">
            <div class="flex-1">
              ${renderRangeBarSVG(isSkipped || isPending || isFailed ? null : item.overall, maxCeiling, 280, 22)}
            </div>
            ${!isPending && !isFailed && !isSkipped && item.overall && item.overall.samples
              ? `<div class="hidden sm:block shrink-0">${renderSparklineSVG(item.overall.samples, avg, 88, 22)}</div>`
              : ''
            }
          </div>

          <!-- Right: Avg ms Badge + One-Click Actions -->
          <div class="flex items-center justify-end gap-2 shrink-0" onclick="event.stopPropagation()">
            ${isSkipped
              ? `<span class="badge-neutral text-xs font-mono px-2.5 py-1 rounded-md" title="No endpoint matches selected transport protocols">Skipped</span>`
              : isPending
                ? `<span class="badge-neutral text-xs font-mono px-2.5 py-1 rounded-md">${state.running ? 'Testing…' : 'Ready'}</span>`
                : isFailed
                  ? `<span class="badge-rose text-xs font-mono px-2.5 py-1 rounded-md">Unreachable</span>`
                  : `<span class="${latencyBadgeClass(avg)} text-xs font-mono font-semibold px-2.5 py-1 rounded-md" title="P95: ${(item.overall.p95Ms || avg).toFixed(1)} ms">${avg.toFixed(1)} ms</span>`
            }

            ${canApply
              ? `<button type="button" data-apply-resolver="${escapeHTML(r.id)}"
                  class="px-3 py-1.5 rounded-lg bg-zinc-900 hover:bg-emerald-500 hover:text-zinc-950 border border-zinc-700 hover:border-emerald-400 text-xs font-medium text-zinc-200 transition">
                  Apply
                </button>`
              : `<button type="button" data-copy-resolver="${escapeHTML(r.id)}"
                  class="px-2.5 py-1.5 rounded-lg bg-zinc-900 hover:bg-zinc-800 border border-zinc-800 text-xs font-mono text-zinc-400">
                  DoH Only
                </button>`
            }

            ${r.source === 'custom'
              ? `<button type="button" data-remove-custom="${escapeHTML(r.id)}" title="Remove custom resolver"
                  class="p-1.5 rounded-lg bg-zinc-900 hover:bg-rose-950 border border-zinc-800 text-zinc-400 hover:text-rose-300 text-xs">✕</button>`
              : ''
            }

            <button type="button" data-toggle-row="${escapeHTML(r.id)}" title="Inspect protocol telemetry"
              class="p-1.5 rounded-lg bg-zinc-900/70 hover:bg-zinc-800 text-zinc-400 hover:text-zinc-200 text-xs font-mono">
              ${expanded ? '▲' : '▼'}
            </button>
          </div>
        </div>

        <!-- Progressive Disclosure Expandable Telemetry Drawer -->
        <div class="${expanded ? '' : 'hidden'} border-t border-zinc-800/80 bg-zinc-950/60 px-4 py-3.5 text-xs">
          ${renderExpandedTelemetry(item)}
        </div>
      </div>`;
    }).join('');
  }

  function renderExpandedTelemetry(item) {
    const r = item.resolver;
    const o = item.overall;
    const c = item.cached;
    const u = item.uncached;
    const protos = item.protocols || {};

    const fmtStat = (st) => (st && st.received > 0 ? `${st.avgMs.toFixed(2)} ms` : '—');
    const protoCards = ['udp', 'doh', 'dot', 'tcp'].map((p) => {
      const pr = protos[p];
      const label = p.toUpperCase();
      if (!pr) {
        return `<div class="rounded-lg bg-zinc-900/60 border border-zinc-800/70 p-2.5">
          <div class="font-mono text-[10px] uppercase text-zinc-500">${label}</div>
          <div class="mt-1 font-mono text-xs text-zinc-500">Not tested</div>
        </div>`;
      }
      const ok = pr.combined && pr.combined.received > 0;
      return `<div class="rounded-lg bg-zinc-900/80 border border-zinc-800 p-2.5">
        <div class="flex items-center justify-between">
          <span class="font-mono text-[10px] uppercase text-zinc-400">${label}</span>
          <span class="font-mono text-[10px] text-zinc-500 truncate max-w-[150px]" title="${escapeHTML(pr.endpoint)}">${escapeHTML(pr.endpoint || '')}</span>
        </div>
        <div class="mt-1 flex items-center justify-between gap-2">
          <span class="font-mono text-sm font-semibold ${ok ? 'text-zinc-100' : 'text-rose-300'}">
            ${ok ? `${pr.combined.avgMs.toFixed(2)} ms` : escapeHTML(pr.error || 'Failed')}
          </span>
          ${ok && pr.combined.samples ? renderSparklineSVG(pr.combined.samples, pr.combined.avgMs, 76, 18) : ''}
        </div>
        <div class="mt-0.5 font-mono text-[11px] text-zinc-400">
          Cached ${fmtStat(pr.cached)} · Uncached ${fmtStat(pr.uncached)} · P95 ${ok ? (pr.combined.p95Ms || pr.combined.maxMs).toFixed(1) + 'ms' : '—'}
        </div>
      </div>`;
    }).join('');

    const findings = (item.hijack && Array.isArray(item.hijack.findings) && item.hijack.findings.length > 0)
      ? `<ul class="mt-2 list-disc list-inside space-y-1 text-[11px] text-zinc-300">${item.hijack.findings.map((f) => `<li>${escapeHTML(f)}</li>`).join('')}</ul>`
      : '';

    return `
      <div class="flex flex-wrap items-center justify-between gap-2 pb-3 border-b border-zinc-800/70">
        <div class="text-zinc-300">${escapeHTML(r.description || 'Custom or system DNS resolver.')}</div>
        <div class="flex flex-wrap items-center gap-2">
          ${dnssecBadgeHTML(item.dnssec)}
          ${adBlockBadgeHTML(item.filtering)}
          ${hijackBadgeHTML(item.hijack)}
          <button type="button" data-copy-resolver="${escapeHTML(r.id)}"
            class="px-2.5 py-1 rounded bg-zinc-900 hover:bg-zinc-800 border border-zinc-800 font-mono text-[11px] text-emerald-300">
            Copy DNS / Manual Command
          </button>
        </div>
      </div>

      ${item.status === 'skipped'
        ? `<div class="mt-3 rounded-lg bg-zinc-900/60 border border-zinc-800 px-3 py-2 text-zinc-400 font-mono">Skipped in this test run — no endpoints match your currently enabled transport protocols (e.g., encrypted-only DoH/DoT resolver while testing UDP).</div>`
        : (o && (o.sent > 0 || o.received > 0) ? `
      <div class="grid grid-cols-2 sm:grid-cols-6 gap-2.5 mt-3">
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">Cached Avg</div>
          <div class="mt-0.5 font-mono text-sm font-semibold text-emerald-300">${fmtStat(c)}</div>
          <div class="text-[10px] text-zinc-500">Edge cache hit</div>
        </div>
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">Uncached Avg</div>
          <div class="mt-0.5 font-mono text-sm font-semibold text-cyan-300">${fmtStat(u)}</div>
          <div class="text-[10px] text-zinc-500">UUID recursive</div>
        </div>
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">P95 / Median</div>
          <div class="mt-0.5 font-mono text-sm font-semibold text-zinc-100">${(o.p95Ms || o.maxMs).toFixed(1)} / ${o.medianMs.toFixed(1)} ms</div>
          <div class="text-[10px] text-zinc-500">Min ${o.minMs.toFixed(1)} · Max ${o.maxMs.toFixed(1)} ms</div>
        </div>
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">Jitter (σ)</div>
          <div class="mt-0.5 font-mono text-sm font-semibold text-zinc-100">${o.jitterMs.toFixed(2)} ms</div>
          <div class="text-[10px] text-zinc-500">Std deviation</div>
        </div>
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">Reliability</div>
          <div class="mt-0.5 font-mono text-sm font-semibold text-zinc-100">${(100 - o.lossPct).toFixed(0)}%</div>
          <div class="text-[10px] text-zinc-500">${o.received}/${o.sent} (${o.lossPct.toFixed(0)}% loss)</div>
        </div>
        <div class="rounded-lg bg-zinc-900/70 border border-zinc-800/80 p-2.5">
          <div class="text-[10px] font-mono uppercase text-zinc-400">Egress Identity</div>
          <div class="mt-0.5 font-mono text-xs font-semibold text-zinc-200 truncate">${escapeHTML((item.hijack && item.hijack.udpEgress) || '—')}</div>
          <div class="text-[10px] text-zinc-500 truncate">DoH: ${escapeHTML((item.hijack && item.hijack.dohEgress) || '—')}</div>
        </div>
      </div>

      <div class="grid grid-cols-1 sm:grid-cols-4 gap-2.5 mt-2.5">
        ${protoCards}
      </div>
      ${findings}
      ` : `<div class="mt-3 text-zinc-400 font-mono">Click “Run Speed Test” above to measure cached vs. uncached latency, P95, jitter, DNSSEC, ad-blocking, and ISP hijacking.</div>`)}
    `;
  }

  function detectProTableProtocols(items) {
    const selected = Array.from(document.querySelectorAll('.proto-checkbox:checked')).map((el) => el.value);
    const present = new Set(selected.length > 0 ? selected : ['udp', 'doh']);
    present.add('udp');
    present.add('doh');
    items.forEach((item) => {
      if (item && item.protocols) {
        Object.keys(item.protocols).forEach((p) => present.add(p));
      }
    });
    return ['udp', 'doh', 'dot', 'tcp'].filter((p) => present.has(p));
  }

  function renderProTable(items, maxCeiling) {
    const tbody = $('proTableBody');
    const headRow = $('proTableHeadRow');
    const activeProtos = detectProTableProtocols(items);
    const protoTitles = { udp: 'UDP :53', doh: 'DoH', dot: 'DoT :853', tcp: 'TCP :53' };
    if (headRow) {
      headRow.innerHTML = `
        <th class="py-3 px-3">#</th>
        <th class="py-3 px-3">Resolver</th>
        <th class="py-3 px-2.5 text-right">Avg (ms)</th>
        <th class="py-3 px-2.5 text-right">P95</th>
        <th class="py-3 px-3">Latency Sparkline &amp; Range (Min–Avg–Max)</th>
        <th class="py-3 px-2.5 text-right">Cached</th>
        <th class="py-3 px-2.5 text-right">Uncached</th>
        <th class="py-3 px-2.5 text-right">Jitter (σ)</th>
        <th class="py-3 px-2.5 text-right">Loss</th>
        ${activeProtos.map((p) => `<th class="py-3 px-2.5 text-right">${protoTitles[p] || p.toUpperCase()}</th>`).join('')}
        <th class="py-3 px-2.5">DNSSEC</th>
        <th class="py-3 px-2.5">AdBlock</th>
        <th class="py-3 px-2.5">Integrity</th>
        <th class="py-3 px-3 text-right">Action</th>
      `;
    }

    const colSpan = 13 + activeProtos.length;
    if (items.length === 0) {
      tbody.innerHTML = `<tr><td colspan="${colSpan}" class="py-8 text-center text-zinc-400">No resolvers match the selected filter.</td></tr>`;
      return;
    }

    const protoCell = (item, p) => {
      const pr = item.protocols && item.protocols[p];
      if (!pr || !pr.combined || pr.combined.sent === 0) return '<span class="text-zinc-600">—</span>';
      if (pr.combined.received === 0) return `<span class="text-rose-400">${escapeHTML(pr.error || 'err')}</span>`;
      return `${pr.combined.avgMs.toFixed(1)} ms`;
    };

    tbody.innerHTML = items.map((item) => {
      const r = item.resolver;
      const o = item.overall;
      const isSkipped = item.status === 'skipped';
      const isFailed = item.status === 'failed';
      const isPending = item.status === 'pending' || (!isSkipped && !isFailed && (!o || o.received === 0));
      const canApply = Array.isArray(r.ipv4) && r.ipv4.length > 0 && !r.deprecated;
      const primaryAddr = (r.ipv4 && r.ipv4[0]) || r.doh || r.dot || '—';

      return `
      <tr class="hover:bg-zinc-900/50 transition ${isSkipped ? 'opacity-65' : ''}">
        <td class="py-3 px-3 font-mono text-zinc-400">${isSkipped ? '—' : isPending ? '·' : item.rank}</td>
        <td class="py-3 px-3">
          <div class="flex items-center gap-1.5">
            <span class="font-semibold text-zinc-100">${escapeHTML(r.name)}</span>
            ${categoryBadgeHTML(r.category, r.deprecated)}
          </div>
          <div class="font-mono text-[11px] text-zinc-400 truncate max-w-[190px]">${escapeHTML(primaryAddr)}</div>
        </td>
        <td class="py-3 px-2.5 text-right font-mono">
          ${isSkipped ? '<span class="badge-neutral px-2 py-0.5 rounded">Skipped</span>' : isPending ? `<span class="text-zinc-500">${state.running ? '…' : '—'}</span>` : isFailed ? '<span class="badge-rose px-2 py-0.5 rounded">Failed</span>' : `<span class="${latencyBadgeClass(o.avgMs)} px-2 py-0.5 rounded font-semibold">${o.avgMs.toFixed(2)}</span>`}
        </td>
        <td class="py-3 px-2.5 text-right font-mono text-zinc-300">
          ${!isPending && !isFailed && !isSkipped ? `${(o.p95Ms || o.maxMs).toFixed(1)}` : '—'}
        </td>
        <td class="py-3 px-3">
          <div class="flex items-center gap-2 min-w-[210px]">
            <div class="flex-1">${renderRangeBarSVG(isSkipped || isPending || isFailed ? null : o, maxCeiling, 130, 20)}</div>
            ${!isPending && !isFailed && !isSkipped && o && o.samples ? renderSparklineSVG(o.samples, o.avgMs, 76, 20) : ''}
          </div>
        </td>
        <td class="py-3 px-2.5 text-right font-mono text-emerald-300">${!isPending && !isSkipped && item.cached && item.cached.received > 0 ? item.cached.avgMs.toFixed(2) : '—'}</td>
        <td class="py-3 px-2.5 text-right font-mono text-cyan-300">${!isPending && !isSkipped && item.uncached && item.uncached.received > 0 ? item.uncached.avgMs.toFixed(2) : '—'}</td>
        <td class="py-3 px-2.5 text-right font-mono text-zinc-300">${!isPending && !isFailed && !isSkipped ? `${o.jitterMs.toFixed(2)} ms` : '—'}</td>
        <td class="py-3 px-2.5 text-right font-mono ${o && o.lossPct > 0 ? 'text-amber-300' : 'text-zinc-400'}">${!isPending && !isSkipped ? `${o.lossPct.toFixed(0)}%` : '—'}</td>
        ${activeProtos.map((p) => `<td class="py-3 px-2.5 text-right font-mono text-zinc-200">${protoCell(item, p)}</td>`).join('')}
        <td class="py-3 px-2.5">${dnssecBadgeHTML(item.dnssec)}</td>
        <td class="py-3 px-2.5">${adBlockBadgeHTML(item.filtering)}</td>
        <td class="py-3 px-2.5">${hijackBadgeHTML(item.hijack)}</td>
        <td class="py-3 px-3 text-right">
          <div class="inline-flex items-center gap-1.5">
            ${canApply ? `<button type="button" data-apply-resolver="${escapeHTML(r.id)}" class="px-2.5 py-1 rounded bg-zinc-800 hover:bg-emerald-500 hover:text-zinc-950 text-zinc-100 font-medium transition">Apply</button>` : ''}
            <button type="button" data-copy-resolver="${escapeHTML(r.id)}" class="px-2 py-1 rounded bg-zinc-900 hover:bg-zinc-800 border border-zinc-800 font-mono text-[11px] text-zinc-300" title="Copy DNS addresses">Copy</button>
          </div>
        </td>
      </tr>`;
    }).join('');
  }

  // Handle Live Progress Event
  function handleProgress(p) {
    if (!p) return;
    $('progressContainer').classList.remove('hidden');
    $('progressPhaseBadge').textContent = (p.phase || 'benchmark').toUpperCase();
    $('progressMessage').textContent = p.message || 'Benchmarking…';
    const done = Number(p.done || 0);
    const total = Number(p.total || 1);
    $('progressCount').textContent = `${done} / ${total}`;
    const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
    $('progressBar').style.width = `${pct}%`;

    if (state.running && Array.isArray(p.partial) && p.partial.length > 0) {
      state.liveResults = p.partial;
      renderTablesOnly();
    }
  }

  // Gather Benchmark Options from UI + Persisted Settings
  function collectOptions() {
    const protos = Array.from(document.querySelectorAll('.proto-checkbox:checked')).map((el) => el.value);
    const uncachedBase = (state.settings && state.settings.options && state.settings.options.uncachedBase) || state.defaultOptions.uncachedBase || 'resbench.invalid';
    return {
      queries: Number($('optQueries').value) || 10,
      timeoutMs: Number($('optTimeout').value) || 2000,
      workers: (state.defaultOptions && state.defaultOptions.workers) || 12,
      staggerMs: (state.defaultOptions && state.defaultOptions.staggerMs) || 12,
      protocols: protos.length > 0 ? protos : ['udp', 'doh'],
      cachedDomains: (state.defaultOptions && state.defaultOptions.cachedDomains) || ['cloudflare.com', 'google.com', 'apple.com'],
      uncachedBase,
      checkIntegrity: Boolean($('optIntegrity').checked)
    };
  }

  // Execute Benchmark
  async function startBenchmark() {
    if (state.running) return;
    if (navigator.onLine === false) {
      showToast('System appears to be offline. Connect to a network before running the benchmark.', 'error');
      return;
    }
    state.running = true;
    state.liveResults = null;
    $('runBenchmarkBtn').disabled = true;
    $('runBenchmarkBtn').classList.add('opacity-60');
    $('runBtnLabel').textContent = 'Testing DNS…';
    $('cancelBenchmarkBtn').classList.remove('hidden');
    $('progressContainer').classList.remove('hidden');
    $('progressBar').style.width = '2%';

    try {
      const opts = collectOptions();
      const report = await backend.RunBenchmark(opts);
      state.liveResults = null;
      state.report = report;
      renderResults();
      if (report && report.cancelled) {
        showToast('Benchmark stopped — showing partial measurements.', 'info');
      } else if (report) {
        showToast(`Benchmark completed in ${(report.durationMs / 1000).toFixed(1)}s across ${report.results.length} resolvers.`, 'success');
      }
    } catch (err) {
      state.liveResults = null;
      showToast(`Benchmark failed: ${err.message || err}`, 'error');
    } finally {
      state.running = false;
      $('runBenchmarkBtn').disabled = false;
      $('runBenchmarkBtn').classList.remove('opacity-60');
      $('runBtnLabel').textContent = 'Run Speed Test';
      $('cancelBenchmarkBtn').classList.add('hidden');
      $('progressContainer').classList.add('hidden');
    }
  }

  async function verifyCurrentDNS(options = { silentToast: false, prefixMessage: '' }) {
    const btn = $('verifyDnsBtn');
    const origText = btn ? btn.textContent : 'Verify Speed';
    if (btn) {
      btn.disabled = true;
      btn.textContent = 'Verifying…';
    }
    try {
      const vr = await backend.QuickVerifyCurrentDNS();
      state.verifyResult = vr;
      renderVerifyBadge();
      if (vr && vr.success) {
        const deltaStr = vr.improvementPct && vr.improvementPct >= 2
          ? ` (${Math.round(vr.improvementPct)}% faster)`
          : '';
        if (options.prefixMessage) {
          showToast(`${options.prefixMessage} Verified ${vr.avgMs.toFixed(1)} ms${deltaStr}.`, 'success');
        } else if (!options.silentToast) {
          showToast(vr.message, 'success');
        }
      } else if (!options.silentToast && vr && vr.message) {
        showToast(vr.message, 'error');
      }
      return vr;
    } catch (err) {
      if (!options.silentToast) {
        showToast(`Verification failed: ${err.message || err}`, 'error');
      }
      return null;
    } finally {
      if (btn) {
        btn.disabled = false;
        btn.textContent = origText;
      }
    }
  }

  // Apply Resolver or Show Copy Fallback Modal
  async function applyResolverById(resolverId) {
    try {
      const res = await backend.ApplyResolver(resolverId);
      if (res.network) {
        state.network = res.network;
        renderNetwork();
      }
      if (res.success) {
        showToast(res.message, 'success');
        if (state.report && state.report.recommendation && state.report.recommendation.resolverId === resolverId) {
          state.report.recommendation.currentIsFastest = true;
          state.report.recommendation.headline = `You're now using ${state.report.recommendation.name} — the fastest resolver on your network`;
          renderBanners();
        }
        verifyCurrentDNS({ prefixMessage: res.message });
      } else if (res.needsManual) {
        openManualFallbackModal(res);
      } else {
        showToast(res.message || 'Could not apply DNS settings.', 'error');
      }
    } catch (err) {
      showToast(`Error applying DNS: ${err.message || err}`, 'error');
    }
  }

  function openCopyModalForResolver(resolverId) {
    const r = state.resolvers.find((x) => x.id === resolverId) ||
      (state.report && state.report.results.map((x) => x.resolver).find((x) => x.id === resolverId));
    if (!r) return;
    const addrs = [...(r.ipv4 || []), ...(r.ipv6 || []).slice(0, 2)];
    const displayAddrs = addrs.length > 0 ? addrs : [r.doh || r.dot || ''];
    const ifaceName = (state.network && state.network.active && state.network.active.name) || 'Wi-Fi / Ethernet';
    openManualFallbackModal({
      message: `Copy ${r.name}'s addresses below or run the one-liner command in an elevated terminal.`,
      addresses: displayAddrs,
      manual: {
        steps: [
          `Open your system Network Settings for “${ifaceName}”.`,
          `Paste the primary and secondary addresses below into the DNS configuration field.`,
          r.doh ? `Optional DoH endpoint: ${r.doh}` : 'Save changes and flush your DNS cache.'
        ],
        command: buildQuickCommand(addrs, ifaceName)
      }
    });
  }

  function buildQuickCommand(addrs, ifaceName) {
    if (!addrs || addrs.length === 0) return '# Encrypted DoH/DoT endpoint only';
    const os = (state.network && state.network.os) || 'windows';
    if (os === 'darwin') {
      return `sudo networksetup -setdnsservers "${ifaceName}" ${addrs.join(' ')} && sudo dscacheutil -flushcache && sudo killall -HUP mDNSResponder`;
    }
    if (os === 'linux') {
      return `nmcli connection modify "${ifaceName}" ipv4.dns "${addrs.filter((a) => !a.includes(':')).join(' ')}" ipv4.ignore-auto-dns yes && resolvectl flush-caches`;
    }
    const idx = (state.network && state.network.active && state.network.active.id) || '1';
    return `Set-DnsClientServerAddress -InterfaceIndex ${idx} -ServerAddresses @(${addrs.map((a) => `'${a}'`).join(',')}); ipconfig /flushdns`;
  }

  function openManualFallbackModal(actionRes) {
    $('manualModalMessage').textContent = actionRes.message || '';
    const addrs = (actionRes.addresses || []).join(', ');
    $('manualAddressesBox').textContent = addrs || '—';
    const manual = actionRes.manual || { steps: [], command: '' };
    $('manualCommandBox').textContent = manual.command || '—';
    $('manualStepsList').innerHTML = (manual.steps || []).map((s) => `<li>${escapeHTML(s)}</li>`).join('');
    $('manualFallbackModal').classList.remove('hidden');
  }

  function setMode(mode, persist = true) {
    state.mode = mode === 'pro' ? 'pro' : 'simple';
    $('modeSimpleBtn').classList.toggle('active', state.mode === 'simple');
    $('modeProBtn').classList.toggle('active', state.mode === 'pro');
    $('simpleViewSection').classList.toggle('hidden', state.mode !== 'simple');
    $('proViewSection').classList.toggle('hidden', state.mode !== 'pro');
    $('proConfigBar').classList.toggle('hidden', state.mode !== 'pro');
    if (persist) persistUiPreferences();
  }

  function applyCategoryFilterUI(cat) {
    state.categoryFilter = cat || 'all';
    document.querySelectorAll('#categoryFilterGroup .cat-chip').forEach((el) => {
      const active = el.dataset.cat === state.categoryFilter;
      el.className = active
        ? 'cat-chip active px-2.5 py-1 rounded-md text-xs font-medium bg-zinc-800 text-zinc-100 border border-zinc-700'
        : 'cat-chip px-2.5 py-1 rounded-md text-xs font-medium bg-zinc-900/80 text-zinc-400 hover:text-zinc-200 border border-zinc-800';
    });
  }

  function syncOptionsToProBar(opts) {
    if (!opts) return;
    const protos = new Set(opts.protocols || ['udp', 'doh']);
    document.querySelectorAll('.proto-checkbox').forEach((cb) => {
      cb.checked = protos.has(cb.value);
    });
    if ($('optQueries') && opts.queries) $('optQueries').value = String(opts.queries);
    if ($('optTimeout') && opts.timeoutMs) $('optTimeout').value = String(opts.timeoutMs);
    if ($('optIntegrity')) $('optIntegrity').checked = opts.checkIntegrity !== false;
  }

  async function persistUiPreferences() {
    try {
      const updated = {
        ...state.settings,
        viewMode: state.mode,
        categoryFilter: state.categoryFilter,
        sortBy: state.sortBy,
        options: state.settings.options || state.defaultOptions
      };
      const saved = await backend.SaveSettings(updated);
      if (saved) state.settings = saved;
    } catch (_) {
      // Non-fatal in preview or transient IO issues
    }
  }

  function formatBytes(bytes) {
    if (!bytes || bytes <= 0) return '—';
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(0)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  }

  function updateNavPill() {
    const btn = $('openUpdateModalBtn');
    const dot = $('updateNavDot');
    const icon = $('updateNavIcon');
    const lbl = $('updateNavLabel');
    if (!btn) return;

    const info = state.updateInfo;
    if (info && info.isAvailable) {
      btn.classList.add('update-pill-available');
      dot.classList.remove('hidden');
      icon.classList.add('hidden');
      lbl.textContent = `v${info.version} Ready`;
      btn.title = `Resolv v${info.version} is available`;
    } else {
      btn.classList.remove('update-pill-available');
      dot.classList.add('hidden');
      icon.classList.remove('hidden');
      lbl.textContent = 'Updates';
      btn.title = 'Check for Resolv Software Updates';
    }
  }

  function renderUpdateModal(errorMsg = '') {
    const info = state.updateInfo;
    $('updateCurrentVer').textContent = `v${state.version || '1.1.1'}`;
    $('updateLatestVer').textContent = info && info.version ? `v${info.version}` : '—';

    const badge = $('updateModalBadge');
    const title = $('updateStatusTitle');
    const meta = $('updateAssetMeta');
    const noteBox = $('updateNoteBox');
    const installBtn = $('installUpdateBtn');
    const notesEl = $('updateReleaseNotes');
    const linkEl = $('openReleasePageLink');

    noteBox.classList.add('hidden');
    if (errorMsg) {
      badge.className = 'font-mono text-[10px] px-1.5 py-0.5 rounded bg-rose-950/70 border border-rose-800 text-rose-200';
      badge.textContent = 'Error';
      title.textContent = errorMsg;
      installBtn.classList.add('hidden');
      meta.classList.add('hidden');
      return;
    }

    if (!info) {
      badge.className = 'font-mono text-[10px] px-1.5 py-0.5 rounded bg-zinc-800 text-zinc-300';
      badge.textContent = 'Idle';
      title.textContent = 'Click Check Now to query GitHub Releases.';
      notesEl.textContent = 'No changelog loaded yet.';
      meta.classList.add('hidden');
      installBtn.classList.add('hidden');
      return;
    }

    if (info.htmlUrl) linkEl.href = info.htmlUrl;
    notesEl.textContent = (info.body && info.body.trim()) || 'No changelog summary provided for this release.';

    if (!info.isAvailable) {
      badge.className = 'font-mono text-[10px] px-1.5 py-0.5 rounded bg-emerald-950/60 border border-emerald-800/60 text-emerald-300';
      badge.textContent = 'Up to date';
      title.textContent = `You are running the latest version of Resolv (v${state.version}).`;
      meta.classList.add('hidden');
      installBtn.classList.add('hidden');
      return;
    }

    badge.className = 'font-mono text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/20 border border-emerald-500/50 text-emerald-300';
    badge.textContent = info.assetReady ? 'Update Ready' : 'Release Found';
    title.textContent = `${info.title || 'Resolv v' + info.version} is available!`;

    if (info.assetName) {
      meta.classList.remove('hidden');
      $('updateAssetName').textContent = info.assetName;
      $('updateAssetSize').textContent = formatBytes(info.assetSize);
      $('updateAssetSha').textContent = info.sha256 ? `SHA-256 ${info.sha256.slice(0, 12)}…` : 'Checksum pending';
    } else {
      meta.classList.add('hidden');
    }

    if (info.updateNote) {
      noteBox.textContent = info.updateNote;
      noteBox.classList.remove('hidden');
    }

    const canAutoInstall = Boolean(info.assetReady && info.canSelfUpdate);
    installBtn.classList.toggle('hidden', !canAutoInstall);
  }

  async function checkForUpdates({ silent = false } = {}) {
    const checkBtn = $('checkUpdatesNowBtn');
    if (checkBtn && !silent) {
      checkBtn.disabled = true;
      checkBtn.textContent = 'Checking…';
      $('updateModalBadge').textContent = 'Checking';
    }
    try {
      const info = await backend.CheckForUpdates();
      state.updateInfo = info;
      updateNavPill();
      renderUpdateModal();
      if (!silent && !info.isAvailable) {
        showToast(`Resolv v${state.version} is already up to date.`, 'success');
      }
      return info;
    } catch (err) {
      if (!silent) {
        renderUpdateModal(err.message || String(err));
      }
      return null;
    } finally {
      if (checkBtn && !silent) {
        checkBtn.disabled = false;
        checkBtn.textContent = 'Check Now';
      }
    }
  }

  function handleUpdateProgress(p) {
    if (!p) return;
    $('updateProgressBox').classList.remove('hidden');
    $('updateProgressLabel').textContent = p.status || p.phase || 'Updating…';
    const pct = Math.max(0, Math.min(100, Number(p.percent || 0)));
    $('updateProgressPct').textContent = `${pct}%`;
    $('updateProgressBar').style.width = `${pct}%`;
  }

  async function installUpdateNow() {
    if (state.updating) return;
    state.updating = true;
    const installBtn = $('installUpdateBtn');
    const checkBtn = $('checkUpdatesNowBtn');
    installBtn.disabled = true;
    installBtn.textContent = 'Updating…';
    checkBtn.disabled = true;
    $('updateProgressBox').classList.remove('hidden');
    handleUpdateProgress({ phase: 'downloading', percent: 5, status: 'Starting verified download…' });

    try {
      const res = await backend.DownloadAndApplyUpdate();
      if (res && res.success) {
        showToast(res.message || 'Update verified — restarting Resolv…', 'success');
        handleUpdateProgress({ phase: 'installing', percent: 100, status: res.message || 'Restarting Resolv…' });
      } else {
        const msg = (res && res.message) || 'Update installation could not complete.';
        renderUpdateModal(msg);
        showToast(msg, 'error');
        $('updateProgressBox').classList.add('hidden');
      }
    } catch (err) {
      const msg = err.message || String(err);
      renderUpdateModal(msg);
      showToast(`Update failed: ${msg}`, 'error');
      $('updateProgressBox').classList.add('hidden');
    } finally {
      state.updating = false;
      installBtn.disabled = false;
      installBtn.textContent = 'Download & Restart';
      checkBtn.disabled = false;
    }
  }

  async function openHistoryModal() {
    $('historyModal').classList.remove('hidden');
    const container = $('historyListContainer');
    container.innerHTML = '<div class="py-8 text-center text-zinc-400">Loading history…</div>';
    try {
      const entries = await backend.GetHistory();
      renderHistoryList(entries || []);
    } catch (err) {
      container.innerHTML = `<div class="py-8 text-center text-rose-300">Failed to load history: ${escapeHTML(err.message || err)}</div>`;
    }
  }

  function renderHistoryList(entries) {
    const container = $('historyListContainer');
    $('historyCountBadge').textContent = `${entries.length} run${entries.length === 1 ? '' : 's'}`;
    if (!entries || entries.length === 0) {
      container.innerHTML = `<div class="rounded-xl bg-zinc-900/50 border border-zinc-800 p-8 text-center text-zinc-400">No benchmark runs recorded yet. Run a Speed Test to save snapshots here.</div>`;
      return;
    }
    container.innerHTML = entries.map((e) => {
      const ts = e.timestamp ? new Date(e.timestamp).toLocaleString() : e.id;
      const sysStr = Array.isArray(e.systemDns) && e.systemDns.length > 0 ? e.systemDns.join(', ') : 'Auto';
      const top = Array.isArray(e.topResults) ? e.topResults : [];
      const topHtml = top.map((row, idx) => `
        <div class="flex items-center justify-between gap-2 text-[11px] font-mono">
          <span class="text-zinc-300 truncate">${idx + 1}. ${escapeHTML(row.name)}</span>
          <span class="${latencyBadgeClass(row.avgMs)} px-1.5 py-0.2 rounded">${Number(row.avgMs).toFixed(1)} ms</span>
        </div>
      `).join('');
      const gainStr = e.improvementPct >= 2 ? `<span class="text-emerald-400 font-mono">+${Math.round(e.improvementPct)}% gain</span>` : '';
      return `
        <div class="rounded-xl bg-zinc-900/70 border border-zinc-800 p-3.5 space-y-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <div>
              <div class="font-medium text-zinc-100">${escapeHTML(e.winnerName || 'No winner')} ${e.winnerAvgMs ? `<span class="font-mono text-emerald-400">(${Number(e.winnerAvgMs).toFixed(1)} ms)</span>` : ''}</div>
              <div class="text-[11px] font-mono text-zinc-400">${escapeHTML(ts)} · ${escapeHTML(e.adapterName || 'Network')} · Sys: ${escapeHTML(sysStr)}</div>
            </div>
            <div class="flex items-center gap-2">
              ${e.intercepted ? '<span class="badge-rose font-mono text-[10px] px-1.5 py-0.5 rounded">Hijacked Port 53</span>' : ''}
              ${gainStr}
              <span class="font-mono text-[10px] text-zinc-500">${((e.durationMs || 0) / 1000).toFixed(1)}s</span>
            </div>
          </div>
          ${topHtml ? `<div class="pt-2 border-t border-zinc-800/80 grid grid-cols-1 sm:grid-cols-2 gap-1.5">${topHtml}</div>` : ''}
        </div>
      `;
    }).join('');
  }

  function openSettingsModal() {
    const s = state.settings || {};
    const o = s.options || state.defaultOptions;
    $('setAutoCheckUpdates').checked = s.autoCheckUpdates !== false;
    const protos = new Set(o.protocols || ['udp', 'doh']);
    document.querySelectorAll('[data-set-proto]').forEach((cb) => {
      cb.checked = protos.has(cb.dataset.setProto);
    });
    $('setDefaultQueries').value = String(o.queries || 10);
    $('setDefaultTimeout').value = String(o.timeoutMs || 2000);
    $('setUncachedBase').value = o.uncachedBase || 'resbench.invalid';
    $('setCheckIntegrity').checked = o.checkIntegrity !== false;
    $('settingsConfigDir').textContent = state.configDir || 'OS User Config Directory / Resolv';
    $('settingsModal').classList.remove('hidden');
  }

  async function saveSettingsForm() {
    const protos = Array.from(document.querySelectorAll('[data-set-proto]:checked')).map((cb) => cb.dataset.setProto);
    if (protos.length === 0) {
      showToast('Select at least one default DNS protocol.', 'error');
      return;
    }
    const payload = {
      ...state.settings,
      viewMode: state.mode,
      categoryFilter: state.categoryFilter,
      sortBy: state.sortBy,
      autoCheckUpdates: Boolean($('setAutoCheckUpdates').checked),
      options: {
        ...(state.defaultOptions || {}),
        queries: Number($('setDefaultQueries').value) || 10,
        timeoutMs: Number($('setDefaultTimeout').value) || 2000,
        protocols: protos,
        uncachedBase: ($('setUncachedBase').value || 'resbench.invalid').trim(),
        checkIntegrity: Boolean($('setCheckIntegrity').checked)
      }
    };
    try {
      const saved = await backend.SaveSettings(payload);
      if (saved) {
        state.settings = saved;
        if (saved.options) {
          state.defaultOptions = saved.options;
          syncOptionsToProBar(saved.options);
        }
      }
      $('settingsModal').classList.add('hidden');
      showToast('Settings saved to persistent config.', 'success');
    } catch (err) {
      showToast(`Failed to save settings: ${err.message || err}`, 'error');
    }
  }

  async function openExportModal(format) {
    state.currentExportFormat = format;
    $('exportDropdown').classList.add('hidden');
    try {
      const content = await backend.ExportReport(format);
      $('exportModalTitle').textContent = `Export Benchmark Report (${format.toUpperCase()})`;
      $('exportTextarea').value = content;
      $('exportStatusHint').textContent = `${content.length.toLocaleString()} bytes ready`;
      $('exportModal').classList.remove('hidden');
    } catch (err) {
      showToast(`Export error: ${err.message || err}`, 'error');
    }
  }

  function bindEvents() {
    if (window.runtime && typeof window.runtime.EventsOn === 'function') {
      window.runtime.EventsOn('benchmark:progress', handleProgress);
      window.runtime.EventsOn('update:progress', handleUpdateProgress);
    }

    window.addEventListener('online', () => renderNetwork());
    window.addEventListener('offline', () => renderNetwork());

    $('modeSimpleBtn').addEventListener('click', () => setMode('simple'));
    $('modeProBtn').addEventListener('click', () => setMode('pro'));

    if ($('searchResolverInput')) {
      $('searchResolverInput').addEventListener('input', (e) => {
        state.searchQuery = e.target.value;
        renderResults();
      });
    }
    if ($('sortSelect')) {
      $('sortSelect').addEventListener('change', (e) => {
        state.sortBy = e.target.value;
        persistUiPreferences();
        renderResults();
      });
    }

    // Keyboard shortcuts (Linear / Raycast style)
    window.addEventListener('keydown', (e) => {
      const tag = (e.target && e.target.tagName) || '';
      const inInput = tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
      if (e.key === 'Escape') {
        document.querySelectorAll('[id$="Modal"]').forEach((m) => {
          if (m.id === 'updateModal' && state.updating) return;
          m.classList.add('hidden');
        });
        $('exportDropdown').classList.add('hidden');
        if (inInput && e.target.id === 'searchResolverInput') {
          e.target.value = '';
          state.searchQuery = '';
          e.target.blur();
          renderResults();
        }
        return;
      }
      if (!inInput && (e.key === '/' || ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k'))) {
        e.preventDefault();
        if ($('searchResolverInput')) $('searchResolverInput').focus();
      } else if (!inInput && e.altKey && e.key === '1') {
        setMode('simple');
      } else if (!inInput && e.altKey && e.key === '2') {
        setMode('pro');
      }
    });

    // Click backdrop to close modals
    document.querySelectorAll('[id$="Modal"]').forEach((modal) => {
      modal.addEventListener('click', (e) => {
        if (e.target !== modal) return;
        if (modal.id === 'updateModal' && state.updating) return;
        modal.classList.add('hidden');
      });
    });

    $('runBenchmarkBtn').addEventListener('click', startBenchmark);
    $('cancelBenchmarkBtn').addEventListener('click', () => backend.CancelBenchmark());

    const doRefreshNet = async () => {
      const b = await backend.RefreshNetwork();
      applyBootstrap(b);
      showToast('Network interface state refreshed.', 'info');
    };
    $('refreshNetBtn').addEventListener('click', doRefreshNet);
    $('offlineRetryBtn').addEventListener('click', doRefreshNet);

    $('interfaceSelect').addEventListener('change', async (e) => {
      if (!e.target.value) return;
      state.report = null;
      state.liveResults = null;
      state.verifyResult = null;
      const b = await backend.SelectInterface(e.target.value);
      applyBootstrap(b);
    });

    $('verifyDnsBtn').addEventListener('click', () => verifyCurrentDNS());

    $('resetDnsBtn').addEventListener('click', async () => {
      const res = await backend.ResetDNS();
      if (res.network) {
        state.network = res.network;
        renderNetwork();
      }
      if (res.success) {
        showToast(res.message, 'success');
        if (state.report && state.report.recommendation && state.report.recommendation.currentIsFastest) {
          state.report.recommendation.currentIsFastest = false;
          renderBanners();
        }
        verifyCurrentDNS({ prefixMessage: res.message });
      } else {
        showToast(res.message, 'error');
      }
    });

    $('flushCacheBtn').addEventListener('click', async () => {
      const res = await backend.FlushCache();
      showToast(res.message, res.success ? 'success' : 'error');
    });

    $('winnerApplyBtn').addEventListener('click', (e) => {
      const id = e.currentTarget.dataset.resolverId;
      if (id) applyResolverById(id);
    });

    $('winnerCopyBtn').addEventListener('click', (e) => {
      const id = e.currentTarget.dataset.resolverId;
      if (id) openCopyModalForResolver(id);
    });

    $('statusToastClose').addEventListener('click', () => $('statusToast').classList.add('hidden'));

    $('categoryFilterGroup').addEventListener('click', (e) => {
      const btn = e.target.closest('[data-cat]');
      if (!btn) return;
      applyCategoryFilterUI(btn.dataset.cat);
      persistUiPreferences();
      renderResults();
    });

    document.body.addEventListener('click', async (e) => {
      if (!e.target.closest('#exportMenuBtn') && !e.target.closest('#exportDropdown')) {
        $('exportDropdown').classList.add('hidden');
      }
      const applyBtn = e.target.closest('[data-apply-resolver]');
      if (applyBtn) {
        applyResolverById(applyBtn.dataset.applyResolver);
        return;
      }
      const copyBtn = e.target.closest('[data-copy-resolver]');
      if (copyBtn) {
        openCopyModalForResolver(copyBtn.dataset.copyResolver);
        return;
      }
      const rmBtn = e.target.closest('[data-remove-custom]');
      if (rmBtn) {
        const rmId = rmBtn.dataset.removeCustom;
        const list = await backend.RemoveCustomResolver(rmId);
        state.resolvers = list;
        if (state.report && Array.isArray(state.report.results)) {
          state.report.results = state.report.results.filter((item) => item.resolver.id !== rmId);
        }
        renderResults();
        showToast('Custom resolver removed.', 'info');
        return;
      }
      const toggleRow = e.target.closest('[data-toggle-row]');
      if (toggleRow) {
        const id = toggleRow.dataset.toggleRow;
        if (state.expandedIds.has(id)) state.expandedIds.delete(id);
        else state.expandedIds.add(id);
        renderResults();
      }
    });

    $('openCustomModalBtn').addEventListener('click', () => {
      $('customDnsError').classList.add('hidden');
      $('customDnsModal').classList.remove('hidden');
    });
    $('openAboutModalBtn').addEventListener('click', () => $('aboutModal').classList.remove('hidden'));
    $('openHistoryModalBtn').addEventListener('click', openHistoryModal);
    $('openSettingsModalBtn').addEventListener('click', openSettingsModal);
    $('openUpdateModalBtn').addEventListener('click', () => {
      $('updateProgressBox').classList.add('hidden');
      $('updateModal').classList.remove('hidden');
      renderUpdateModal();
      if (!state.updateInfo) {
        checkForUpdates({ silent: false });
      }
    });

    $('checkUpdatesNowBtn').addEventListener('click', () => checkForUpdates({ silent: false }));
    $('installUpdateBtn').addEventListener('click', installUpdateNow);
    $('saveSettingsBtn').addEventListener('click', saveSettingsForm);
    $('clearHistoryBtn').addEventListener('click', async () => {
      await backend.ClearHistory();
      renderHistoryList([]);
      showToast('Benchmark history cleared.', 'info');
    });

    document.querySelectorAll('[data-close-modal]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const id = btn.dataset.closeModal;
        if (id === 'updateModal' && state.updating) return;
        $(id).classList.add('hidden');
      });
    });

    $('customDnsForm').addEventListener('submit', async (e) => {
      e.preventDefault();
      const name = $('customNameInput').value.trim();
      const input = $('customEndpointInput').value.trim();
      try {
        const resolvers = await backend.AddCustomResolver(name, input);
        state.resolvers = resolvers;
        if (state.report && Array.isArray(state.report.results)) {
          const existingIds = new Set(state.report.results.map((item) => item.resolver.id));
          resolvers.forEach((r) => {
            if (!existingIds.has(r.id)) {
              state.report.results.push({
                resolver: r,
                rank: state.report.results.length + 1,
                status: 'pending',
                primary: '',
                overall: null,
                cached: null,
                uncached: null,
                protocols: {},
                dnssec: 'unknown',
                filtering: { checked: false, blocksAds: false },
                hijack: { status: 'unknown' }
              });
            }
          });
        }
        $('customNameInput').value = '';
        $('customEndpointInput').value = '';
        $('customDnsModal').classList.add('hidden');
        renderResults();
        showToast('Added custom DNS resolver.', 'success');
      } catch (err) {
        const errBox = $('customDnsError');
        errBox.textContent = err.message || String(err);
        errBox.classList.remove('hidden');
      }
    });

    $('exportMenuBtn').addEventListener('click', (e) => {
      e.stopPropagation();
      $('exportDropdown').classList.toggle('hidden');
    });
    document.querySelectorAll('[data-export]').forEach((btn) => {
      btn.addEventListener('click', () => openExportModal(btn.dataset.export));
    });
    $('copyExportBtn').addEventListener('click', async () => {
      await navigator.clipboard.writeText($('exportTextarea').value);
      $('exportStatusHint').textContent = 'Copied to clipboard!';
    });
    $('saveExportFileBtn').addEventListener('click', async () => {
      const saved = await backend.SaveReport(state.currentExportFormat);
      if (saved) $('exportStatusHint').textContent = `Saved to ${saved}`;
    });

    $('copyAddressesBtn').addEventListener('click', async () => {
      await navigator.clipboard.writeText($('manualAddressesBox').textContent);
      showToast('Copied DNS addresses to clipboard.', 'success');
    });
    $('copyCommandBtn').addEventListener('click', async () => {
      await navigator.clipboard.writeText($('manualCommandBox').textContent);
      showToast('Copied terminal command to clipboard.', 'success');
    });
  }

  function applyBootstrap(b) {
    if (!b) return;
    state.version = b.version || '1.1.1';
    state.configDir = b.configDir || '';
    $('appVersionBadge').textContent = `v${state.version}`;
    if (b.network) {
      state.network = b.network;
      $('aboutVersionArch').textContent = `v${state.version} · ${b.network.osName || b.network.os} (${b.network.arch || 'amd64'})`;
    }
    if (Array.isArray(b.resolvers)) state.resolvers = b.resolvers;
    if (b.defaultOptions) {
      state.defaultOptions = b.defaultOptions;
      syncOptionsToProBar(b.defaultOptions);
    }
    if (b.settings) {
      state.settings = b.settings;
      if (b.settings.viewMode) setMode(b.settings.viewMode, false);
      if (b.settings.categoryFilter) applyCategoryFilterUI(b.settings.categoryFilter);
      if (b.settings.sortBy && $('sortSelect')) {
        state.sortBy = b.settings.sortBy;
        $('sortSelect').value = b.settings.sortBy;
      }
      if (b.settings.options) {
        state.defaultOptions = b.settings.options;
        syncOptionsToProBar(b.settings.options);
      }
    }
    state.report = b.lastReport || null;

    renderNetwork();
    renderResults();
  }

  // Standalone Browser Demo Bridge (only used when previewed outside Wails webview)
  const demoBridge = (() => {
    let cancelled = false;
    let demoSettings = {
      viewMode: 'simple',
      categoryFilter: 'all',
      sortBy: 'rank',
      autoCheckUpdates: true,
      options: {
        queries: 10,
        timeoutMs: 2000,
        workers: 12,
        staggerMs: 12,
        protocols: ['udp', 'doh'],
        cachedDomains: ['cloudflare.com', 'google.com', 'apple.com'],
        uncachedBase: 'resbench.invalid',
        checkIntegrity: true
      }
    };
    let demoHistory = [];
    const demoNet = {
      os: 'windows',
      osName: 'Windows 11 Pro (Browser Preview)',
      arch: 'amd64',
      hostname: 'workstation',
      elevated: false,
      online: true,
      backend: 'powershell',
      gateway: '192.168.1.1',
      systemDns: ['192.168.1.1'],
      active: {
        id: '12',
        name: 'Wi-Fi',
        device: 'Intel Wi-Fi 6E AX211',
        kind: 'wifi',
        ipv4: ['192.168.1.42'],
        ipv6: [],
        gateway: '192.168.1.1',
        dns: ['192.168.1.1'],
        dnsManual: false
      },
      interfaces: [
        { id: '12', name: 'Wi-Fi', device: 'Intel Wi-Fi 6E AX211', kind: 'wifi', ipv4: ['192.168.1.42'], gateway: '192.168.1.1', dns: ['192.168.1.1'], dnsManual: false }
      ]
    };
    let resolvers = [
      { id: 'system-dns', name: 'Current System DNS', category: 'System', source: 'system', description: 'Active DNS server configured on your OS.', ipv4: ['192.168.1.1'] },
      { id: 'cloudflare', name: 'Cloudflare', category: 'Privacy', source: 'builtin', description: 'Fast anycast resolver with a strict no-logging policy.', ipv4: ['1.1.1.1', '1.0.0.1'], doh: 'https://cloudflare-dns.com/dns-query', dot: 'one.one.one.one' },
      { id: 'cloudflare-security', name: 'Cloudflare Malware', category: 'Privacy', source: 'builtin', description: 'Cloudflare 1.1.1.2 malware-blocking endpoint.', ipv4: ['1.1.1.2', '1.0.0.2'], doh: 'https://security.cloudflare-dns.com/dns-query', dot: 'security.cloudflare-dns.com' },
      { id: 'cloudflare-family', name: 'Cloudflare Family', category: 'Family', source: 'builtin', description: 'Cloudflare 1.1.1.3 family-safe endpoint.', ipv4: ['1.1.1.3', '1.0.0.3'], doh: 'https://family.cloudflare-dns.com/dns-query', dot: 'family.cloudflare-dns.com' },
      { id: 'google', name: 'Google Public DNS', category: 'Standard', source: 'builtin', description: 'Large global anycast network operated by Google.', ipv4: ['8.8.8.8', '8.8.4.4'], doh: 'https://dns.google/dns-query', dot: 'dns.google' },
      { id: 'quad9', name: 'Quad9', category: 'Privacy', source: 'builtin', description: 'Non-profit resolver with malware-domain blocking and DNSSEC.', ipv4: ['9.9.9.9', '149.112.112.112'], doh: 'https://dns.quad9.net/dns-query', dot: 'dns.quad9.net' },
      { id: 'quad9-ecs', name: 'Quad9 ECS', category: 'Privacy', source: 'builtin', description: 'Quad9 malware blocking and DNSSEC with EDNS Client Subnet (ECS) enabled.', ipv4: ['9.9.9.11', '149.112.112.11'], doh: 'https://dns11.quad9.net/dns-query', dot: 'dns11.quad9.net' },
      { id: 'adguard', name: 'AdGuard DNS', category: 'Ad-blocking', source: 'builtin', description: 'Blocks ads, trackers and phishing at the DNS level.', ipv4: ['94.140.14.14', '94.140.15.15'], doh: 'https://dns.adguard-dns.com/dns-query', dot: 'dns.adguard-dns.com' },
      { id: 'adguard-family', name: 'AdGuard Family', category: 'Family', source: 'builtin', description: 'Blocks ads and adult content.', ipv4: ['94.140.14.15', '94.140.15.16'], doh: 'https://family.adguard-dns.com/dns-query', dot: 'family.adguard-dns.com' },
      { id: 'nextdns', name: 'NextDNS', category: 'Privacy', source: 'builtin', description: 'Configurable privacy resolver.', ipv4: ['45.90.28.0', '45.90.30.0'], doh: 'https://dns.nextdns.io', dot: 'dns.nextdns.io' },
      { id: 'opendns', name: 'OpenDNS', category: 'Standard', source: 'builtin', description: 'Cisco Umbrella public resolver with phishing protection.', ipv4: ['208.67.222.222', '208.67.220.220'], doh: 'https://doh.opendns.com/dns-query', dot: 'dns.opendns.com' },
      { id: 'opendns-family', name: 'OpenDNS FamilyShield', category: 'Family', source: 'builtin', description: 'Cisco OpenDNS family filter pre-configured to block adult content and phishing.', ipv4: ['208.67.222.123', '208.67.220.123'], doh: 'https://doh.familyshield.opendns.com/dns-query', dot: 'familyshield.opendns.com' },
      { id: 'controld', name: 'Control D', category: 'Standard', source: 'builtin', description: 'Free unfiltered resolver by Control D.', ipv4: ['76.76.2.0', '76.76.10.0'], doh: 'https://freedns.controld.com/p0', dot: 'p0.freedns.controld.com' },
      { id: 'controld-adblock', name: 'Control D AdBlock', category: 'Ad-blocking', source: 'builtin', description: 'Control D Free DNS filtering ads and trackers.', ipv4: ['76.76.2.2', '76.76.10.2'], doh: 'https://freedns.controld.com/p2', dot: 'p2.freedns.controld.com' },
      { id: 'dns4eu-protective', name: 'DNS4EU Protective', category: 'Privacy', source: 'builtin', description: 'Official EU GDPR-compliant resolver blocking malware and phishing.', ipv4: ['86.54.11.1', '86.54.11.201'], doh: 'https://protective.joindns4.eu/dns-query', dot: 'protective.joindns4.eu' },
      { id: 'dns4eu-unfiltered', name: 'DNS4EU Unfiltered', category: 'Privacy', source: 'builtin', description: 'Official EU GDPR-compliant resolver with zero filtering.', ipv4: ['86.54.11.100', '86.54.11.200'], doh: 'https://unfiltered.joindns4.eu/dns-query', dot: 'unfiltered.joindns4.eu' },
      { id: 'dns4eu-adblock', name: 'DNS4EU AdBlock', category: 'Ad-blocking', source: 'builtin', description: 'Official EU GDPR-compliant resolver filtering ads and trackers.', ipv4: ['86.54.11.13', '86.54.11.213'], doh: 'https://noads.joindns4.eu/dns-query', dot: 'noads.joindns4.eu' },
      { id: 'dns-sb', name: 'DNS.SB', category: 'Privacy', source: 'builtin', description: 'Privacy-first global anycast resolver with DNSSEC and zero logging.', ipv4: ['185.222.222.222', '45.11.45.11'], doh: 'https://doh.sb/dns-query', dot: 'dot.sb' },
      { id: 'cleanbrowsing', name: 'CleanBrowsing Family', category: 'Family', source: 'builtin', description: 'Family filter: blocks adult content and enforces SafeSearch.', ipv4: ['185.228.168.168', '185.228.169.168'], doh: 'https://doh.cleanbrowsing.org/doh/family-filter/', dot: 'family-filter-dns.cleanbrowsing.org' }
    ];

    const buildDemoResult = (r) => {
      const bases = {
        'cloudflare': 11.4, 'cloudflare-security': 12.1, 'cloudflare-family': 13.0,
        'quad9': 16.8, 'quad9-ecs': 15.9, 'google': 18.9, 'nextdns': 22.4,
        'dns4eu-protective': 23.1, 'dns4eu-unfiltered': 22.6, 'dns4eu-adblock': 24.0,
        'controld': 24.7, 'controld-adblock': 26.2, 'dns-sb': 27.4,
        'adguard': 29.5, 'adguard-family': 31.1, 'opendns': 34.2, 'opendns-family': 35.6,
        'system-dns': 41.6, 'cleanbrowsing': 48.3
      };
      const base = bases[r.id] || 31.0;
      const cachedAvg = +(base * 0.68).toFixed(2);
      const uncachedAvg = +(base * 1.32).toFixed(2);
      const avg = +((cachedAvg + uncachedAvg) / 2).toFixed(2);
      const jitter = +(base * 0.14).toFixed(2);
      const minMs = +(cachedAvg * 0.82).toFixed(2);
      const maxMs = +(uncachedAvg * 1.25).toFixed(2);
      const p95Ms = +(uncachedAvg * 1.16).toFixed(2);
      const samples = [
        +(cachedAvg * 0.85).toFixed(2), +(uncachedAvg * 1.12).toFixed(2),
        +(cachedAvg * 0.94).toFixed(2), +(uncachedAvg * 0.96).toFixed(2),
        +(cachedAvg * 1.02).toFixed(2), +(uncachedAvg * 1.21).toFixed(2),
        +(cachedAvg * 0.88).toFixed(2), +(uncachedAvg * 1.04).toFixed(2),
        +(cachedAvg * 0.91).toFixed(2), +(uncachedAvg * 1.16).toFixed(2)
      ];
      const blocksAds = r.id === 'adguard' || r.id === 'controld-adblock' || r.id === 'dns4eu-adblock';
      return {
        resolver: r,
        rank: 0,
        status: 'ok',
        score: avg + jitter * 0.5,
        primary: 'udp',
        overall: { sent: 20, received: 20, avgMs: avg, minMs, maxMs, medianMs: avg, p95Ms, jitterMs: jitter, lossPct: 0, samples },
        cached: { sent: 10, received: 10, avgMs: cachedAvg, minMs, maxMs: +(cachedAvg * 1.18).toFixed(2), medianMs: cachedAvg, p95Ms: +(cachedAvg * 1.14).toFixed(2), jitterMs: +(jitter * 0.7).toFixed(2), lossPct: 0 },
        uncached: { sent: 10, received: 10, avgMs: uncachedAvg, minMs: +(uncachedAvg * 0.88).toFixed(2), maxMs, medianMs: uncachedAvg, p95Ms, jitterMs: jitter, lossPct: 0 },
        protocols: {
          udp: r.ipv4 ? { protocol: 'udp', endpoint: `${r.ipv4[0]}:53`, cached: { sent: 10, received: 10, avgMs: cachedAvg }, uncached: { sent: 10, received: 10, avgMs: uncachedAvg }, combined: { sent: 20, received: 20, avgMs: avg, p95Ms, maxMs, lossPct: 0, samples } } : undefined,
          doh: r.doh ? { protocol: 'doh', endpoint: r.doh, cached: { sent: 10, received: 10, avgMs: +(cachedAvg * 1.09).toFixed(2) }, uncached: { sent: 10, received: 10, avgMs: +(uncachedAvg * 1.07).toFixed(2) }, combined: { sent: 20, received: 20, avgMs: +(avg * 1.08).toFixed(2), p95Ms: +(p95Ms * 1.08).toFixed(2), maxMs: +(maxMs * 1.08).toFixed(2), lossPct: 0, samples } } : undefined
        },
        dnssec: 'validating',
        filtering: { checked: true, blocksAds, blockedCount: blocksAds ? 3 : 0, testedCount: 3, method: blocksAds ? 'sinkhole-ip' : 'none' },
        hijack: { status: 'clean', udpEgress: '172.68.10.12', dohEgress: '172.68.10.19' }
      };
    };

    return {
      getBootstrap: () => ({ version: '1.1.1', configDir: '~/.config/Resolv (Preview)', network: demoNet, resolvers, settings: demoSettings, defaultOptions: demoSettings.options }),
      selectInterface: () => ({ version: '1.1.1', configDir: '~/.config/Resolv (Preview)', network: demoNet, resolvers, settings: demoSettings, defaultOptions: demoSettings.options }),
      addCustomResolver(name, input) {
        if (!input) throw new Error('Enter an IPv4/IPv6 address or DoH URL');
        resolvers = [...resolvers, { id: 'custom-' + Date.now(), name: name || input, category: 'Custom', source: 'custom', ipv4: [input] }];
        return resolvers;
      },
      removeCustomResolver(id) {
        resolvers = resolvers.filter((r) => r.id !== id);
        return resolvers;
      },
      getSettings: () => demoSettings,
      saveSettings: (s) => { demoSettings = s; return demoSettings; },
      getHistory: () => demoHistory,
      clearHistory: () => { demoHistory = []; },
      quickVerify: () => ({
        success: true,
        resolverName: demoNet.systemDns.join(', '),
        addresses: demoNet.systemDns,
        avgMs: 11.4,
        cachedAvgMs: 7.8,
        uncachedAvgMs: 15.0,
        p95Ms: 16.2,
        lossPct: 0,
        previousAvgMs: 41.6,
        improvementPct: 72.6,
        message: `Verified ${demoNet.systemDns.join(', ')}: 11.4 ms avg (cached 7.8 ms · uncached 15.0 ms · 0% loss)`
      }),
      checkForUpdates: () => ({
        currentVersion: '1.1.1',
        version: '1.1.1',
        tagName: 'v1.1.1',
        title: 'Resolv v1.1.1',
        body: '- Auto-updater with SHA-256 verification\n- Live streaming benchmark results\n- Persistent settings and benchmark history',
        htmlUrl: 'https://github.com/efeartn/Resolv/releases',
        isAvailable: false,
        assetReady: true,
        canSelfUpdate: true
      }),
      downloadAndApplyUpdate: () => ({ success: false, message: 'Self-update requires the compiled desktop binary.' }),
      cancel() { cancelled = true; },
      async runBenchmark(opts, onProgress) {
        cancelled = false;
        const total = resolvers.length;
        const built = [];
        for (let i = 0; i < total; i++) {
          if (cancelled) break;
          built.push(buildDemoResult(resolvers[i]));
          const partial = [
            ...built.sort((a, b) => a.score - b.score).map((item, idx) => ({ ...item, rank: idx + 1 })),
            ...resolvers.slice(i + 1).map((r, idx) => ({ resolver: r, rank: built.length + idx + 1, status: 'pending', overall: null, protocols: {} }))
          ];
          onProgress({ phase: 'benchmark', done: i + 1, total, message: `Querying ${resolvers[i].name}`, partial });
          await new Promise((r) => setTimeout(r, 70));
        }
        const results = resolvers.map(buildDemoResult).sort((a, b) => a.score - b.score).map((item, idx) => ({ ...item, rank: idx + 1 }));
        demoHistory.unshift({
          id: String(Date.now()),
          timestamp: new Date().toISOString(),
          adapterName: 'Wi-Fi',
          systemDns: demoNet.systemDns,
          durationMs: 1420,
          winnerName: 'Cloudflare',
          winnerAvgMs: 11.4,
          improvementPct: 72.6,
          intercepted: false,
          topResults: results.slice(0, 5).map((r) => ({ name: r.resolver.name, category: r.resolver.category, avgMs: r.overall.avgMs, p95Ms: r.overall.p95Ms, jitterMs: r.overall.jitterMs, lossPct: r.overall.lossPct }))
        });
        return {
          version: '1.1.1',
          durationMs: 1420,
          options: opts,
          results,
          recommendation: {
            resolverId: 'cloudflare',
            name: 'Cloudflare',
            avgMs: 11.4,
            currentAvgMs: 41.6,
            improvementPct: 72.6,
            headline: 'Cloudflare is 73% faster than your current DNS',
            detail: 'Average 11.4 ms · P95 17.4 ms · jitter 1.6 ms · 0% packet loss · Privacy'
          },
          network: { checked: true, intercepted: false, hijackedCount: 0, summary: 'No DNS interception or tampering detected.' },
          cancelled
        };
      },
      applyResolver(id) {
        const r = resolvers.find((x) => x.id === id);
        if (!r || !r.ipv4) return { success: false, message: 'No IPv4 addresses available.' };
        demoNet.systemDns = r.ipv4;
        demoNet.active.dns = r.ipv4;
        demoNet.active.dnsManual = true;
        return { success: true, message: `Applied ${r.name} (${r.ipv4.join(', ')}) to Wi-Fi and flushed DNS cache.`, addresses: r.ipv4, network: demoNet };
      },
      resetDNS() {
        demoNet.systemDns = ['192.168.1.1'];
        demoNet.active.dns = ['192.168.1.1'];
        demoNet.active.dnsManual = false;
        return { success: true, message: 'Reset Wi-Fi to automatic (DHCP) DNS and flushed resolver cache.', network: demoNet };
      },
      exportReport(r, fmt) {
        if (fmt === 'json') return JSON.stringify(r, null, 2);
        if (fmt === 'csv') return 'rank,name,category,avg_ms,p95_ms,cached_ms,uncached_ms,jitter_ms,loss_pct,blocks_ads\n' +
          r.results.map((s) => `${s.rank},${s.resolver.name},${s.resolver.category},${s.overall.avgMs},${s.overall.p95Ms},${s.cached.avgMs},${s.uncached.avgMs},${s.overall.jitterMs},${s.overall.lossPct},${s.filtering.blocksAds}`).join('\n');
        return `# Resolv Benchmark Report\n\n| # | Resolver | Category | Avg (ms) | P95 | Cached | Uncached | Jitter | Loss | AdBlock |\n|---:|---|---|---:|---:|---:|---:|---:|---:|---|\n` +
          r.results.map((s) => `| ${s.rank} | ${s.resolver.name} | ${s.resolver.category} | **${s.overall.avgMs}** | ${s.overall.p95Ms} | ${s.cached.avgMs} | ${s.uncached.avgMs} | ${s.overall.jitterMs} | ${s.overall.lossPct}% | ${s.filtering.blocksAds ? 'Yes' : 'No'} |`).join('\n');
      }
    };
  })();

  window.addEventListener('DOMContentLoaded', async () => {
    bindEvents();
    try {
      const b = await backend.GetBootstrap();
      applyBootstrap(b);
      if (state.settings && state.settings.autoCheckUpdates !== false) {
        setTimeout(() => checkForUpdates({ silent: true }), 1200);
      }
    } catch (err) {
      showToast(`Initialization warning: ${err.message || err}`, 'error');
    }
  });
})();
