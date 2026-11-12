# Resolv

**Cross-Platform DNS Protocol Benchmark, Privacy Probe & One-Click Switcher**  
Built with **Go**, **`github.com/miekg/dns`**, and **Wails v2** (WebView2 on Windows, WKWebView on macOS, WebKitGTK on Linux). Licensed under the **GNU GPLv3 (`GPL-3.0-or-later`)**.

---

## Key Capabilities

1. **Pure-Go Wire-Protocol Benchmarking (No ICMP Ping)**
   - Benchmarks resolvers over **Standard UDP (`:53`)**, **TCP (`:53`, RFC 7766 connection reuse)**, **DoH (DNS-over-HTTPS, RFC 8484 `application/dns-message`)**, and **DoT (DNS-over-TLS, `:853`)**.
   - **Connection Warm-Up + Bounded Worker Pool:** Pre-warms TCP/TLS/HTTP2 sessions outside timed windows, then dispatches interleaved, staggered queries (`12 ms` pacing across a bounded worker pool) so home routers never suffer socket exhaustion or bufferbloat.
   - **Cached vs. Uncached Resolution:** Measures both edge-cache hits (`cloudflare.com`, `google.com`, `apple.com`) and forced recursive lookups (`<random-uuid>.example.com`).
   - **Rich Statistical Telemetry:** Computes **Average**, **Min**, **Max**, **Median**, **P95 (95th Percentile)**, **Jitter ($\sigma$ standard deviation)**, **Packet Loss (%)**, and retains the chronological sample series for inline SVG sparklines.

2. **Cybersecurity, Privacy & Censorship Probes (`internal/dns/integrity.go`)**
   - **DNSSEC Validation Check:** Verifies whether each resolver sets the `AD` (Authenticated Data) bit on signed zones (`cloudflare.com`) and properly returns `SERVFAIL` on deliberately broken DNSSEC signatures (`dnssec-failed.org`).
   - **Ad & Tracker Blocking Probe:** Queries known ad/telemetry domains (`doubleclick.net`, `pagead2.googlesyndication.com`, `app-measurement.com`) and detects sinkhole IPs (`0.0.0.0`, `127.0.0.1`, `::`), `NXDOMAIN`, `REFUSED`, or `NODATA` filtering.
   - **ISP DNS Hijacking & Transparent Proxy Detection:**
     - Sends a canary query to an unroutable RFC 5737 `TEST-NET-1` address (`192.0.2.53:53`) to detect transparent port-53 interception.
     - Cross-references UDP `:53` responses against encrypted DoH responses for NXDOMAIN tampering, private/bogon IP redirection, and `whoami.akamai.net` resolver egress identity.

3. **Native OS Network Abstraction (`internal/sysnet/`)**
   - **Windows (`net_windows.go`):** Detects active physical/Wi-Fi adapters via PowerShell/CIM, applies DNS via `Set-DnsClientServerAddress`, resets to DHCP, flushes via `ipconfig /flushdns`, and elevates via UAC (`Start-Process -Verb RunAs`).
   - **macOS (`net_darwin.go`):** Detects active hardware ports via `networksetup` & `scutil --dns`, switches DNS via `networksetup -setdnsservers`, flushes via `dscacheutil -flushcache; killall -HUP mDNSResponder`, and elevates via `osascript`.
   - **Linux (`net_linux.go`):** Detects active routes via `/proc/net/route` & `ip route`, switches DNS via `nmcli connection modify` (with fallback to `resolvectl`), flushes via `resolvectl flush-caches`, and elevates via `pkexec`.
   - **Graceful Non-Admin Fallback:** If elevation is cancelled or unavailable, surfaces a one-click **"Copy DNS Addresses & Terminal Command"** modal with step-by-step OS instructions.

4. **Progressive Disclosure Dark-Mode UI & Pure SVG Micro-Charts**
   - **Simple View:** Hero network status card, plain-English winner recommendation banner, and streamlined resolver cards with inline SVG **Min–Avg–P95–Max range bars** and click-to-expand drawers.
   - **Pro / Nerd View:** Protocol selectors, query/timeout tuning, 15-column telemetry matrix with pure SVG **real-time latency sparklines**, DNSSEC/AdBlock/Hijack badges, and **JSON / CSV / Markdown** export.

---

## Project Architecture

```text
Resolv/
├── LICENSE                         # GNU General Public License v3.0
├── go.mod / go.sum                 # Go module configuration
├── wails.json                      # Wails v2 configuration
├── main.go                         # Native desktop window & asset embedding
├── app.go                          # Wails bound controller & custom resolver persistence
├── .github/workflows/release.yml   # Automated multi-OS GitHub Actions release pipeline
├── build/
│   ├── appicon.svg                 # Pure flat geometric vector icon (no gradients/filters)
│   └── appicon.png                 # 512×512 PNG application icon
├── frontend/
│   ├── index.html                  # Progressive disclosure Simple & Pro UI
│   ├── styles.css                  # Minimalist zinc/slate dark theme & latency badges
│   ├── app.js                      # State manager, pure SVG range-bars & sparklines
│   └── icon.svg                    # Header & modal vector icon
└── internal/
    ├── dns/
    │   ├── resolvers.go            # Built-in resolvers + system/gateway/custom parser
    │   ├── transport.go            # UDP :53, TCP :53, DoH (RFC 8484), DoT :853 queriers
    │   ├── stats.go                # Avg, Min, Max, Median, P95, Jitter (σ), Loss, Samples
    │   ├── engine.go               # Warm-up + staggered worker pool benchmark coordinator
    │   ├── integrity.go            # DNSSEC, AdBlock sinkhole, and ISP Hijack probes
    │   ├── export.go               # JSON, CSV, and Markdown report exporters
    │   └── engine_test.go          # Automated unit & integration tests
    └── sysnet/
        ├── sysnet.go               # Cross-platform types & strict IP validation boundary
        ├── exec_windows.go         # Hidden subprocess creation flags on Windows
        ├── exec_unix.go            # POSIX locale normalization & /etc/resolv.conf reader
        ├── net_windows.go          # Windows PowerShell/CIM + UAC implementation
        ├── net_darwin.go           # macOS networksetup/scutil + osascript implementation
        └── net_linux.go            # Linux nmcli/resolvectl + pkexec implementation
```

---

## Building from Source

### Prerequisites
- **Go 1.23+**
- **Wails v2 CLI:**
  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```
- **Platform WebView Dependencies:**
  - **Windows:** WebView2 Runtime (pre-installed on Windows 10/11)
  - **macOS:** Xcode Command Line Tools (`xcode-select --install`)
  - **Linux (Debian/Ubuntu):** `sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev`

### Run Unit Tests
```bash
go test -v ./internal/...
```

### Cross-Platform Production Builds
```bash
# Windows (standalone .exe)
wails build -platform windows/amd64 -clean -trimpath -ldflags "-s -w"

# macOS (Universal .app bundle for Intel + Apple Silicon)
wails build -platform darwin/universal -clean -trimpath -ldflags "-s -w"

# Linux (x86_64 standalone binary)
wails build -platform linux/amd64 -tags webkit2_41 -clean -trimpath -ldflags "-s -w"
```

---

## License

Copyright (C) 2026 Resolv Contributors.  
Distributed under the **GNU General Public License v3.0 or later (`GPL-3.0-or-later`)**. See [LICENSE](./LICENSE) for the full license text.
