package ui

import (
	"fmt"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"touchpad/internal/client"
	"touchpad/internal/config"
	"touchpad/internal/transport/lan"
)

// MobileRoot renders the OLED-optimized mobile touch surface with full
// landscape / portrait auto-orientation adaptation.
func MobileRoot(cx *core.Scope, cfgMgr *config.Manager, cli *client.Client) core.Node {
	cfg := cfgMgr.Get()

	// Reactive state signals
	connected := core.NewSignal(cx, cli.IsConnected())
	hostDisplay := core.NewSignal(cx, "Touchpad")
	rttDisplay := core.NewSignal(cx, "-- ms")
	showSettings := core.NewSignal(cx, false)
	isLandscape := core.NewSignal(cx, false)
	hudAlpha := core.NewSignal(cx, float32(1.0))

	lastTouch := time.Now()

	// Watchdog timer to refresh RTT and auto-dim HUD
	go func() {
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if cli.IsConnected() {
				connected.Set(true)
				rtt := cli.LastRTT()
				if rtt > 0 {
					rttDisplay.Set(fmt.Sprintf("%.1f ms", rtt))
				}
			} else {
				connected.Set(false)
				rttDisplay.Set("Offline")
			}

			// Auto-dim floating HUD pill after 3s of inactivity to save OLED battery & avoid burn-in
			if time.Since(lastTouch) > 3*time.Second {
				if hudAlpha.Get() > 0.15 {
					hudAlpha.Set(0.15)
				}
			} else {
				if hudAlpha.Get() < 1.0 {
					hudAlpha.Set(1.0)
				}
			}
		}
	}()

	// Auto-connect background routine: tries ADB tunnel first, then LAN discovery, then configured IP
	go func() {
		for {
			if !cli.IsConnected() {
				// 1. Try ADB tunnel
				if err := cli.ConnectADB(config.DefaultADBPort); err == nil {
					hostDisplay.Set("ADB USB")
					connected.Set(true)
				} else {
					// 2. Try configured IP
					currCfg := cfgMgr.Get()
					if currCfg.HostIP != "" {
						if err := cli.ConnectLAN(currCfg.HostIP, currCfg.UDPPort); err == nil {
							hostDisplay.Set(currCfg.HostIP)
							connected.Set(true)
						}
					}
					// 3. Try LAN auto-discovery
					if !cli.IsConnected() {
						hosts, err := lan.DiscoverHosts(config.DefaultDiscoveryPort, 800*time.Millisecond)
						if err == nil && len(hosts) > 0 {
							h := hosts[0]
							if err := cli.ConnectLAN(h.IP, h.Port); err == nil {
								hostDisplay.Set(h.HostName)
								connected.Set(true)
								_ = cfgMgr.Update(func(c *config.AppConfig) { c.HostIP = h.IP })
							}
						}
					}
				}
			}
			time.Sleep(2 * time.Second)
		}
	}()

	// Layout change callback: handles screen rotation (Portrait <-> Landscape)
	onLayout := func(b core.Bounds) {
		w := float64(b.Size.Width)
		h := float64(b.Size.Height)
		if w > 0 && h > 0 {
			cli.Gesture().SetDimensions(w, h)
			isLandscape.Set(w > h)
		}
	}

	handleTouch := func(e core.TouchEvent) {
		lastTouch = time.Now()
		hudAlpha.Set(1.0)

		now := time.Now()
		for _, t := range e.Touches {
			switch e.Phase {
			case core.TouchStart:
				cli.Gesture().HandleTouchStart(t.ID, float64(t.Position.X), float64(t.Position.Y), now)
			case core.TouchMove:
				cli.Gesture().HandleTouchMove(t.ID, float64(t.Position.X), float64(t.Position.Y), now)
			case core.TouchEnd:
				cli.Gesture().HandleTouchEnd(t.ID, float64(t.Position.X), float64(t.Position.Y), now)
			case core.TouchCancel:
				cli.Gesture().HandleTouchCancel()
			}
		}
	}

	return core.View{
		Class:    "size-full bg-black relative select-none",
		OnLayout: onLayout,
		OnTouch:  handleTouch,
		Children: []core.Node{
			// Right Edge Scroll Gutter Hairline (28pt)
			core.View{
				Class: "absolute right-7 top-0 bottom-0 w-px bg-neutral-900 pointer-events-none",
			},

			// Bottom Edge Scroll Gutter Hairline (28pt)
			core.View{
				Class: "absolute left-0 right-0 bottom-7 h-px bg-neutral-900 pointer-events-none",
			},

			// Floating Top HUD / Status Pill
			// In landscape, uses top-3 and compact padding; in portrait, uses top-8.
			core.View{
				ClassFunc: func() string {
					baseClass := "absolute left-5 right-5 h-10 bg-neutral-900/90 rounded-full px-4 flex-row items-center justify-between shadow-lg border border-neutral-800/60"
					if isLandscape.Get() {
						return baseClass + " top-3 max-w-md mx-auto"
					}
					return baseClass + " top-8"
				},
				StyleFunc: func() core.Style {
					return core.Style{
						Opacity: core.Alpha(hudAlpha.Get()),
					}
				},
				Children: []core.Node{
					// Status indicator dot + Host title
					core.View{
						Class: "flex-row items-center gap-2",
						Children: []core.Node{
							core.View{
								ClassFunc: func() string {
									if connected.Get() {
										return "size-2 rounded-full bg-green-500"
									}
									return "size-2 rounded-full bg-red-500"
								},
							},
							core.Text{
								ContentFunc: hostDisplay.Get,
								Class:       "text-xs font-semibold text-neutral-200 tracking-wide",
							},
						},
					},

					// Latency readout + Settings button
					core.View{
						Class: "flex-row items-center gap-3",
						Children: []core.Node{
							core.Text{
								ContentFunc: rttDisplay.Get,
								Class:       "text-[11px] font-mono text-neutral-400",
							},
							base.Button{
								Label: "⚙",
								OnPress: func(core.PressEvent) {
									showSettings.Set(!showSettings.Get())
								},
								Class: "px-2 py-0.5 rounded-full bg-neutral-800 active:bg-neutral-700 border border-neutral-700/60",
								Children: []core.Node{
									core.Text{Content: "⚙", Class: "text-xs text-neutral-200"},
								},
							},
						},
					},
				},
			},

			// Settings Drawer Modal (Responsive to Portrait & Landscape)
			core.View{
				ClassFunc: func() string {
					if !showSettings.Get() {
						return "hidden"
					}
					if isLandscape.Get() {
						return "absolute top-16 bottom-6 left-1/4 right-1/4 bg-neutral-900/95 p-4 rounded-2xl gap-2 shadow-2xl border border-neutral-800 overflow-y-auto"
					}
					return "absolute bottom-10 left-4 right-4 bg-neutral-900/95 p-5 rounded-2xl gap-3 shadow-2xl border border-neutral-800"
				},
				Children: []core.Node{
					core.Text{
						Content: "Touchpad Settings",
						Class:   "text-sm font-bold text-white",
					},
					core.View{
						Class: "flex-row gap-2 mt-1",
						Children: []core.Node{
							base.Button{
								Label: "Auto-Discover Host",
								OnPress: func(core.PressEvent) {
									go func() {
										hosts, err := lan.DiscoverHosts(config.DefaultDiscoveryPort, 1*time.Second)
										if err == nil && len(hosts) > 0 {
											h := hosts[0]
											_ = cli.ConnectLAN(h.IP, h.Port)
											hostDisplay.Set(h.HostName)
											showSettings.Set(false)
										}
									}()
								},
								Class: "flex-1 py-1.5 rounded-lg bg-blue-600 active:bg-blue-700",
								Children: []core.Node{
									core.Text{Content: "Scan LAN Hosts", Class: "text-xs text-white text-center font-medium"},
								},
							},
							base.Button{
								Label: "ADB USB Mode",
								OnPress: func(core.PressEvent) {
									_ = cli.ConnectADB(config.DefaultADBPort)
									hostDisplay.Set("ADB USB")
									showSettings.Set(false)
								},
								Class: "flex-1 py-1.5 rounded-lg bg-neutral-700 active:bg-neutral-600",
								Children: []core.Node{
									core.Text{Content: "USB Tunnel", Class: "text-xs text-white text-center font-medium"},
								},
							},
						},
					},
					core.View{
						Class: "flex-row justify-between items-center py-1",
						Children: []core.Node{
							core.Text{Content: "Natural Scrolling", Class: "text-xs text-neutral-300"},
							base.Switch{
								Checked: func() bool { return cfg.NaturalScroll },
								OnChange: func(val bool) {
									_ = cfgMgr.Update(func(c *config.AppConfig) { c.NaturalScroll = val })
								},
								Class:      "w-9 h-5 rounded-full bg-neutral-700 checked:bg-blue-600",
								ThumbClass: "size-4 m-0.5 rounded-full bg-white",
							},
						},
					},
					core.View{
						Class: "flex-row justify-between items-center py-1",
						Children: []core.Node{
							core.Text{Content: "Edge Gutter Scroll", Class: "text-xs text-neutral-300"},
							base.Switch{
								Checked: func() bool { return cfg.EdgeScrollEnabled },
								OnChange: func(val bool) {
									_ = cfgMgr.Update(func(c *config.AppConfig) { c.EdgeScrollEnabled = val })
								},
								Class:      "w-9 h-5 rounded-full bg-neutral-700 checked:bg-blue-600",
								ThumbClass: "size-4 m-0.5 rounded-full bg-white",
							},
						},
					},
					base.Button{
						Label: "Close",
						OnPress: func(core.PressEvent) {
							showSettings.Set(false)
						},
						Class: "py-1.5 rounded-lg bg-neutral-800 active:bg-neutral-700 mt-1",
						Children: []core.Node{
							core.Text{Content: "Done", Class: "text-xs text-neutral-300 text-center font-medium"},
						},
					},
				},
			},
		},
	}
}
