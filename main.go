// SPDX-License-Identifier: GPL-3.0-or-later
// Copyright (C) 2026 PulseDNS contributors

package main

import (
	"embed"
	"log"

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

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:            "PulseDNS",
		Width:            1180,
		Height:           780,
		MinWidth:         900,
		MinHeight:        620,
		BackgroundColour: &options.RGBA{R: 9, G: 9, B: 11, A: 255}, // #09090b (zinc-950)
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			DisableWindowIcon:    false,
			Theme:                windows.Dark,
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
				Title:   "PulseDNS " + AppVersion,
				Message: "Cross-platform DNS Benchmarker & Switcher\nLicensed under the GNU GPLv3.",
				Icon:    appIcon,
			},
		},
		Linux: &linux.Options{
			Icon:                appIcon,
			WindowIsTranslucent: false,
			WebviewGpuPolicy:    linux.WebviewGpuPolicyOnDemand,
			ProgramName:         "pulsedns",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
