// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

//go:build linux

package main

import "os"

// initPlatform mitigates a known blank-screen issue on NVIDIA + Wayland systems
// with WebKitGTK's DMA-BUF renderer before initializing the Wails webview.
func initPlatform() {
	if os.Getenv("WAYLAND_DISPLAY") == "" || os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") != "" {
		return
	}
	if _, err := os.Stat("/proc/driver/nvidia/version"); err == nil {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
}
