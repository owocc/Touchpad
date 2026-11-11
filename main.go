package main

import (
	"log"

	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/desktop"
	"touchpad/internal/client"
	"touchpad/internal/config"
	"touchpad/internal/host"
	"touchpad/internal/inject"
	"touchpad/ui"
)

func main() {
	nsgo.Run(App)
}

// App is the cross-platform application entrypoint.
func App(app *core.Scope) {
	cfgMgr := config.NewManager("")
	cfg := cfgMgr.Get()

	switch app.Env().OS() {
	case core.OSAndroid, core.OSIOS:
		// Mobile Client Mode
		cli, err := client.NewClient(&cfg, 400.0, 800.0)
		if err != nil {
			log.Fatalf("Failed to initialize mobile client: %v", err)
		}

		nsgo.SetMainView(app, func(cx *core.Scope) core.Node {
			return ui.MobileRoot(cx, cfgMgr, cli)
		})

	default:
		// Desktop Host Mode (macOS / Windows / Linux)
		desktop.SetShutdownMode(app, desktop.ShutdownOnExplicit)

		inj, err := inject.New()
		if err != nil {
			log.Fatalf("Failed to initialize input injector: %v", err)
		}

		server, err := host.NewServer(&cfg, inj)
		if err != nil {
			log.Fatalf("Failed to initialize host server: %v", err)
		}

		if err := server.Start(); err != nil {
			log.Fatalf("Failed to start host server: %v", err)
		}

		// Open main Desktop Control Center Window
		win := desktop.OpenWindow(app, desktop.WindowOptions{
			Title:  "Touchpad Host",
			Width:  500,
			Height: 620,
			Key:    "touchpad-main",
		}, func(cx *core.Scope) core.Node {
			return ui.DesktopWindow(cx, cfgMgr, server)
		})

		// Menu bar / Tray icon
		desktop.NewTray(app, desktop.TrayOptions{
			Title:   "Touchpad",
			Tooltip: "Touchpad Host Daemon",
			View: func(cx *core.Scope) core.Node {
				return ui.TrayIcon(cx)
			},
			Menu: func() []desktop.MenuItem {
				return []desktop.MenuItem{
					{
						Label: "Show Control Window",
						OnSelect: func() {
							win.Show()
						},
					},
					{
						Label: "Reset Stuck Buttons",
						OnSelect: func() {
							_ = inj.ResetState()
						},
					},
					{Separator: true},
					{
						Label: "Quit Touchpad",
						OnSelect: func() {
							server.Stop()
							app.Quit()
						},
					},
				}
			},
		})
	}
}
