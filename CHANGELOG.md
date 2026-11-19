# Changelog

All notable changes to **Resolv** are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [v1.3.0] - 2026-11-19

### Added
- **DoH Speed Spotlight:** Prominent `DoH` latency badges in Simple View rows, dedicated highlighted `DoH` column in the Pro Telemetry table, accented DoH tile in the Expanded Details Drawer, and a persistent `"Sort: Fastest DoH"` (`sortBy = "doh"`) mode.
- **Inline Speedup Delta Badges:** Real-time percentage delta pills (`-X%` / `+X%`) on every resolver row comparing average latency against the active system DNS baseline.
- **Clickable Pro Telemetry Sorting:** Clickable column headers (`#`, `Avg`, `P95`, `Cached`, `Uncached`, `Jitter`, `DoH`) with active sort indicators.
- **Search Counter & Clear Control:** Live `Showing X / Y` count badge in the filter toolbar, debounced filtering, and an inline clear (`✕`) button inside the search field.
- **Dynamic Markdown Protocol Columns:** Markdown exports now automatically include `DoT` and `TCP` latency columns when any tested resolver reports them.
- **macOS & Linux Unit Coverage:** Added build-tagged unit tests for localized macOS `networksetup` parsing (`net_darwin_test.go`), Gatekeeper translocation detection (`swap_unix_test.go`), and Linux `ip route` / `ip addr` / `nmcli` / `resolvectl` parsing (`net_linux_test.go`).

### Changed
- **Platform-Aware Shortcuts & Mutation Guards:** Footer hints and keyboard shortcuts adapt to `⌥1` / `⌥2` (`Option+1` / `Option+2`) on macOS, while Apply/Reset/Flush controls lock safely during active DNS write operations.
- **Faster Cold Startup Rendering:** Coalesced initial bootstrap state so the DOM renders once on launch instead of twice.
- **Documentation & Version Alignment:** Updated version identifiers to `v1.3.0` across runtime bindings, WebView title/UI, tests, and documentation.

### Fixed
- **macOS Localized OS Parsing & Self-Updater:** Replaced English-only `networksetup` string assumptions with IP/port heuristics for `-getdnsservers` and `-listnetworkserviceorder`, added IPv6-fallback default-route detection, and hardened `.app` bundle updates against Gatekeeper App Translocation with same-filesystem staging, quarantine stripping, and detached relaunch.
- **Linux Networking & Wayland NVIDIA Stability:** Added active `systemd-resolved` detection before querying/flushing, IPv6 default-route fallback, `ip -4 -o addr` parsing, stale IPv6 DNS clearing on IPv4-only `nmcli` changes, `resolvectl` manual CLI fallbacks, detached PID-wait binary relaunches, and automatic `WEBKIT_DISABLE_DMABUF_RENDERER=1` initialization on Wayland + NVIDIA systems (`platform_linux.go`).
- **Adapter Selection Snapback:** Preserved the user's selected network adapter across post-DNS-change refreshes (`refreshAfterChange`).

## [v1.2.0] - 2026-11-16

### Changed & Redesigned
- **Precision Dark Instrument UI (100% Offline, Zero-CDN Static CSS):**
  - Replaced the external `cdn.tailwindcss.com` runtime JavaScript compiler (~400 KB + DOM `MutationObserver`) with a self-contained 27 KB static design system in `frontend/styles.css`. Resolv now renders instantaneously on air-gapped networks, captive portals, or broken-DNS computers with significantly lower WebView RAM and CPU usage.
  - Redesigned the interface around matte obsidian hairline surfaces, tabular monospaced numeric columns (`tabular-nums`), crisp inline SVG status icons (replacing OS-inconsistent Unicode emojis), non-pulsing live progress tracks, and matte modal backdrops (removing expensive GPU `backdrop-blur` passes).
  - Kept every feature intact across Simple View, Pro Telemetry, History, Settings, Updater, Custom DNS, Export, and About modals.

### Added
- **Hero Depth Presets (`Quick` / `Standard` / `Deep`):** Select `5`, `10`, or `20` queries per domain directly from the main command console, synced automatically with Pro Telemetry options and persistent Settings.
- **One-Click IPv4, IPv6, DoH & DoT Quick-Copy Chips:** Expanded resolver drawers and the Endpoint Setup modal now include dedicated one-click copy controls for IPv4, IPv6, DoH URL, DoT hostname, and elevated CLI commands.

### Optimized & Fixed
- **Zero-Clutter Portable Windows Profile & Broad PC Compatibility:**
  - Redirected Windows WebView2 user-data storage (`WebviewUserDataPath`) to `%LocalAppData%\Resolv\WebView2`, preventing portable `.exe` runs from leaving `.WebView2` folders next to Downloads/Desktop binaries.
  - Enabled `WebviewDisableRendererCodeIntegrity` and disabled accidental zoom (`DisablePinchZoom`, `IsZoomControlEnabled: false`) with dark native window frame matching for maximum compatibility across Windows PCs and third-party overlays.
  - Added batched PowerShell adapter detection with resilient `winStringSlice` JSON decoding (fixing single-IP / zero-IPv6 adapter parsing quirks on PowerShell 5.1) plus automatic `netsh` fallback when `Set-DnsClientServerAddress` is unavailable, and `ip -4` IPv4 detection fallback on non-NetworkManager Linux systems.
- **Lower Go & Frontend Memory/CPU Overhead:**
  - Added `64 MiB` Go soft heap cap (`debug.SetMemoryLimit`) and post-run `debug.FreeOSMemory()`, pre-sized job queues, reused persistent `mdns.Client` instances across UDP queries, eliminated shallow-slice deep copies on DoH requests, and reused Phase 2 connections for Phase 3 DNSSEC/AdBlock/Hijack probes.
  - Stopped rebuilding the hidden tab (`Simple` or `Pro`) during live benchmark streaming ticks (`renderTablesOnly()` now renders only the active view).

---

## [v1.1.1] - 2026-11-13

### Removed
- **Removed Discontinued Mullvad Public DNS:** Removed `dns.mullvad.net` following Mullvad's permanent shutdown of its public DNS service in November 2026 (live checks showed DoH connection resets).

### Added
- **6 Live-Verified Built-In Public Resolvers (all supporting IPv4, IPv6, UDP, TCP, DoH & DoT):**
  - **DNS4EU Protective** (`86.54.11.1` / `86.54.11.201`) — Official EU GDPR-compliant malware and phishing protection.
  - **DNS4EU Unfiltered** (`86.54.11.100` / `86.54.11.200`) — EU GDPR-compliant no-log resolver with zero filtering.
  - **DNS4EU AdBlock** (`86.54.11.13` / `86.54.11.213`) — EU GDPR-compliant ad, tracker, and threat blocking.
  - **Quad9 ECS** (`9.9.9.11` / `149.112.112.11`) — Quad9 malware blocking + DNSSEC with EDNS Client Subnet enabled for faster CDN routing.
  - **OpenDNS FamilyShield** (`208.67.222.123` / `208.67.220.123`) — Pre-configured Cisco adult-content and phishing filter.
  - **DNS.SB** (`185.222.222.222` / `45.11.45.11`) — Privacy-first global anycast resolver with DNSSEC and zero logging.

### Fixed
- **Pro Mode DoT Fallback for Split-IP Providers (Control D):** `streamQuerier` now falls back from `PrimaryAddr():853` to `<DoT Host>:853` when the plain-DNS anycast IP refuses port `853`, fixing Pro-mode DoT benchmarks for **Control D** (`76.76.2.0` -> `p0.freedns.controld.com`) and **Control D AdBlock** (`76.76.2.2` -> `p2.freedns.controld.com`).
- **All Existing Providers Live-Audited:** Verified Cloudflare, Google Public DNS, Quad9, AdGuard DNS, NextDNS, OpenDNS, Control D, and CleanBrowsing remain active across UDP `:53`, TCP `:53`, DoH (`RFC 8484` over HTTP/2), and DoT (`:853`).

---

## [v1.1.0] - 2026-11-13

### Added
- **In-App Auto-Updater & Manual Update Check:**
  - Asynchronous non-blocking update check on app launch (toggleable in **Settings**) with a quiet glowing **Updates** header pill when a newer GitHub release is available.
  - Dedicated **Software Update** modal showing installed vs. latest version, release notes, artifact size, `SHA-256` verification badge, and a real-time download/verify/install progress bar.
  - Strict `SHA256SUMS.txt` verification prior to atomic cross-platform self-replacement and restart on Windows (`.exe`), macOS (`Resolv.app` inside `.zip`), and Linux (`resolv-linux-amd64`), with clear fallback messaging for read-only install folders or in-progress CI builds.
- **Persistent Settings & Benchmark History (`os.UserConfigDir()/Resolv`):**
  - Stored independently from the app binary in `settings.json`, `custom_resolvers.json`, and `history.json` (up to 25 recent runs) so upgrades never overwrite user configuration.
  - Added **Settings** modal for startup auto-check preference, default protocols, query count, timeout, custom uncached test zone (`UncachedBase`), and integrity probes. View mode, category filter, and sort order persist automatically across restarts.
  - Added **History** modal to review and compare past benchmark snapshots across adapters, Wi-Fi networks, and ISPs (with **Clear History**).
- **Live Streaming Benchmark Rows:** Simple View and Pro Telemetry now update row-by-row in real time during benchmarks (`Progress.Partial`) while preserving open resolver drawers.
- **Post-Switch Speed Verification (`Verify Speed`):** Applying a resolver or resetting to DHCP automatically runs a fast live probe (`QuickVerifyCurrentDNS`) to display verified average latency and improvement `%` on the Current System DNS card.
- **Expanded Built-In Resolvers & Custom Filter:** Added `Cloudflare Malware` (`1.1.1.2`), `Cloudflare Family` (`1.1.1.3`), `AdGuard Family` (`94.140.14.15`), and `Control D AdBlock` (`76.76.2.2`), plus a dedicated **Custom** category filter chip.

### Fixed & Improved
- **Skipped Status Clarity:** Resolvers with no endpoints matching enabled protocols (such as encrypted-only resolvers under UDP-only runs) now report `skipped` cleanly instead of `failed` / `Unreachable` and are omitted from recommendations.
- **Dynamic Pro Table Protocol Columns:** `DoT :853` and `TCP :53` telemetry columns automatically appear alongside `UDP :53` and `DoH` when tested.
- **Winner Banner State Sync:** Hides `Apply Best DNS` when the current DNS is already the fastest option (`currentIsFastest`) and updates immediately after applying/resetting DNS.
- **Adapter & Custom List State:** Switching network adapters clears stale prior-adapter metrics; adding or removing custom resolvers updates active report rows immediately; modal backdrops close dialogs on click (guarded during update installation).

### Download Checksums (`SHA-256`)
- `resolv-windows-amd64.exe`: `e084e3c76b1c30bed5ab4cc2df9e33ce061b3faa0e7a0e5d82772d90bbf00c6f`
- `resolv-macos-universal.zip`: `0467793de330e8d8e5ae3b2028f773cc75c26625ab7e5008ee172e896998a100`
- `resolv-linux-amd64`: `44869e69cb5ee7c196db930369027929374b230461e1875ceedb0b0defc4bc39`

---

## [v1.0.1] - 2026-11-12

### Changed
- Added Simple View and Pro Telemetry UI screenshots, badges, and multi-OS download matrix to `README.md`.
- Updated Windows/Wails application metadata version to `1.0.1`.

---

## [v1.0.0] - 2026-11-12

### Added
- Initial public release of **Resolv**, built with Go, `github.com/miekg/dns`, and Wails v2.
- Anti-bufferbloat multi-protocol DNS engine (`UDP :53`, `TCP :53`, `DoH`, `DoT :853`) measuring Cached vs. Uncached latency, P95, Jitter, and Packet Loss.
- Security & censorship probes (DNSSEC validation, ad-blocking sinkhole detection, and ISP port-53 hijacking/proxying checks).
- Native cross-platform adapter detection, one-click DNS switching/reset, cache flush, and elevation fallback on Windows, macOS, and Linux.
- JSON, CSV, and Markdown benchmark report exports and custom DNS resolver support.
