// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 Resolv contributors

package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend
var assets embed.FS

//go:embed build/appicon.png
var appIcon []byte

// webviewUserDataDir places portable WebView2 browser state inside the user cache
// directory so running the portable binary from Downloads/Desktop leaves no clutter.
func webviewUserDataDir() string {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		base, err = os.UserConfigDir()
	}
	if err != nil || base == "" {
		return ""
	}
	dir := filepath.Join(base, "Resolv", "WebView2")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

func main() {
	initPlatform()
	// Keep Go backend memory compact on low-RAM machines without throttling active DNS bursts.
	debug.SetMemoryLimit(64 << 20)

	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "Resolv",
		Width:            1180,
		Height:           780,
		MinWidth:         900,
		MinHeight:        620,
		BackgroundColour: &options.RGBA{R: 9, G: 10, B: 14, A: 255}, // #090a0e
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent:                false,
			WindowIsTranslucent:                 false,
			DisableWindowIcon:                   false,
			IsZoomControlEnabled:                false,
			DisablePinchZoom:                    true,
			WebviewUserDataPath:                 webviewUserDataDir(),
			WebviewDisableRendererCodeIntegrity: true,
			Theme:                               windows.Dark,
			CustomTheme: &windows.ThemeSettings{
				DarkModeTitleBar:          windows.RGB(9, 10, 14),
				DarkModeTitleBarInactive:  windows.RGB(13, 15, 20),
				DarkModeTitleText:         windows.RGB(241, 243, 248),
				DarkModeTitleTextInactive: windows.RGB(148, 155, 171),
				DarkModeBorder:            windows.RGB(30, 34, 45),
				DarkModeBorderInactive:    windows.RGB(22, 25, 33),
			},
		},
		Mac: &mac.Options{
			TitleBar: &mac.TitleBar{
				TitlebarAppearsTransparent: true,
				HideTitle:                  false,
				HideTitleBar:               false,
				FullSizeContent:            false,
				UseToolbar:                 false,
			},
			Appearance:           mac.NSAppearanceNameDarkAqua,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			About: &mac.AboutInfo{
				Title:   "Resolv " + AppVersion,
				Message: "Cross-platform DNS Benchmarker & Switcher\nLicensed under the GNU GPLv3.",
				Icon:    appIcon,
			},
		},
		Linux: &linux.Options{
			Icon:                appIcon,
			WindowIsTranslucent: false,
			WebviewGpuPolicy:    linux.WebviewGpuPolicyOnDemand,
			ProgramName:         "resolv",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
