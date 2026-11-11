package ui

import (
	"fmt"
	"net"
	"time"

	"github.com/oneo-me/nsgo/base"
	"github.com/oneo-me/nsgo/core"
	"touchpad/internal/config"
	"touchpad/internal/host"
	"touchpad/internal/transport/adb"
)

// DesktopWindow renders the comprehensive Desktop Control Center GUI.
func DesktopWindow(cx *core.Scope, cfgMgr *config.Manager, srv *host.Server) core.Node {
	cfg := cfgMgr.Get()
	metrics := srv.Metrics()

	recvCount := core.NewSignal(cx, uint64(0))
	dropCount := core.NewSignal(cx, uint64(0))
	p50Text := core.NewSignal(cx, "-- ms")
	p95Text := core.NewSignal(cx, "-- ms")
	adbDeviceText := core.NewSignal(cx, "Scanning USB...")
	statusDotColor := core.NewSignal(cx, "size-2.5 rounded-full bg-green-500")
	statusLabel := core.NewSignal(cx, "Listening")
	hasPerm := core.NewSignal(cx, srv.Injector().HasPermission())

	sensDisplay := core.NewSignal(cx, fmt.Sprintf("%.1fx", cfg.Sensitivity))

	// ADB Device detection
	adbMgr := adb.NewManager()

	// Update stats periodically via background ticker
	go func() {
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			recv := metrics.PacketsRecv
			drop := metrics.PacketsDrop
			recvCount.Set(recv)
			dropCount.Set(drop)

			// Permission check
			hasPerm.Set(srv.Injector().HasPermission())

			p50, p95, _ := metrics.Percentiles()
			if p50 > 0 {
				p50Text.Set(fmt.Sprintf("%.2f ms", p50))
				p95Text.Set(fmt.Sprintf("%.2f ms", p95))
			}

			// Active vs idle
			if !metrics.LastActivity.IsZero() && time.Since(metrics.LastActivity) < 2*time.Second {
				statusDotColor.Set("size-2.5 rounded-full bg-emerald-400 animate-pulse")
				statusLabel.Set("Active (Receiving)")
			} else {
				statusDotColor.Set("size-2.5 rounded-full bg-green-500")
				statusLabel.Set("Ready (Listening)")
			}

			// Poll ADB devices
			devs, err := adbMgr.ListDevices()
			if err == nil && len(devs) > 0 {
				d := devs[0]
				model := d.Model
				if model == "" {
					model = d.Serial
				}
				adbDeviceText.Set(fmt.Sprintf("🟢 %s (%s)", model, d.State))
			} else {
				adbDeviceText.Set("⚪ No USB device (LAN Mode Active)")
			}
		}
	}()

	var ipList []string
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				ipList = append(ipList, fmt.Sprintf("%s : %s", iface.Name, ipnet.IP.String()))
			}
		}
	}

	ipNodes := make([]core.Node, len(ipList))
	for i, ip := range ipList {
		ipNodes[i] = core.View{
			Class: "flex-row items-center justify-between py-0.5",
			Children: []core.Node{
				core.Text{
					Content: ip,
					Class:   "text-xs font-mono text-blue-400 select-all",
				},
				core.Text{
					Content: fmt.Sprintf(":%d", cfg.UDPPort),
					Class:   "text-xs font-mono text-neutral-400",
				},
			},
		}
	}

	return core.View{
		Class: "size-full p-6 bg-neutral-950 text-white gap-4 select-none",
		Children: []core.Node{
			// Top App Header
			core.View{
				Class: "flex-row justify-between items-center pb-3 border-b border-neutral-800",
				Children: []core.Node{
					core.View{
						Class: "flex-row items-center gap-2.5",
						Children: []core.Node{
							core.Text{Content: "Touchpad Host", Class: "text-lg font-bold text-white tracking-wide"},
							core.View{
								Class: "px-2 py-0.5 rounded-full bg-neutral-800 border border-neutral-700",
								Children: []core.Node{
									core.Text{Content: "v1.0.0", Class: "text-[10px] text-neutral-400 font-mono"},
								},
							},
						},
					},
					// Status Badge
					core.View{
						Class: "flex-row items-center gap-2 px-3 py-1 rounded-full bg-neutral-900 border border-neutral-800",
						Children: []core.Node{
							core.View{ClassFunc: statusDotColor.Get},
							core.Text{ContentFunc: statusLabel.Get, Class: "text-xs font-medium text-neutral-200"},
						},
					},
				},
			},

			// Accessibility Permission Warning Banner (if not granted)
			core.View{
				ClassFunc: func() string {
					if hasPerm.Get() {
						return "hidden"
					}
					return "p-3 rounded-xl bg-amber-500/10 border border-amber-500/30 flex-row items-center justify-between gap-3"
				},
				Children: []core.Node{
					core.View{
						Class: "flex-1 gap-0.5",
						Children: []core.Node{
							core.Text{Content: "Accessibility Permission Required", Class: "text-xs font-semibold text-amber-300"},
							core.Text{Content: "macOS requires permission to inject cursor movement into other apps.", Class: "text-[11px] text-amber-200/70 leading-tight"},
						},
					},
					base.Button{
						Label: "Grant Permission",
						OnPress: func(core.PressEvent) {
							srv.Injector().RequestPermission()
						},
						Class: "px-3 py-1.5 rounded-lg bg-amber-500 hover:bg-amber-400 active:bg-amber-600 transition",
						Children: []core.Node{
							core.Text{Content: "Open Settings", Class: "text-xs font-semibold text-black"},
						},
					},
				},
			},

			// Connection Info Grid
			core.View{
				Class: "grid grid-cols-2 gap-3",
				Children: []core.Node{
					// LAN Card
					core.View{
						Class: "p-3.5 rounded-xl bg-neutral-900/80 border border-neutral-800/80 gap-2",
						Children: append([]core.Node{
							core.View{
								Class: "flex-row justify-between items-center",
								Children: []core.Node{
									core.Text{Content: "Wi-Fi / LAN IP", Class: "text-xs font-semibold text-neutral-300"},
									core.Text{Content: fmt.Sprintf("Port %d", cfg.UDPPort), Class: "text-[10px] font-mono text-neutral-400"},
								},
							},
						}, ipNodes...),
					},

					// USB / ADB Card
					core.View{
						Class: "p-3.5 rounded-xl bg-neutral-900/80 border border-neutral-800/80 gap-2 justify-between",
						Children: []core.Node{
							core.View{
								Class: "flex-row justify-between items-center",
								Children: []core.Node{
									core.Text{Content: "USB Cable (ADB)", Class: "text-xs font-semibold text-neutral-300"},
									core.Text{Content: fmt.Sprintf("Port %d", cfg.ADBPort), Class: "text-[10px] font-mono text-neutral-400"},
								},
							},
							core.Text{
								ContentFunc: adbDeviceText.Get,
								Class:       "text-xs font-medium text-neutral-300",
							},
							core.Text{
								Content: "Port forwarded: 127.0.0.1:29181",
								Class:   "text-[10px] text-neutral-400 font-mono",
							},
						},
					},
				},
			},

			// Performance & Real-time Telemetry Card
			core.View{
				Class: "p-4 rounded-xl bg-neutral-900 border border-neutral-800 gap-3",
				Children: []core.Node{
					core.Text{Content: "Live Telemetry & Diagnostics", Class: "text-xs font-semibold text-neutral-400 tracking-wider uppercase"},
					core.View{
						Class: "flex-row justify-between items-center gap-2",
						Children: []core.Node{
							// Packets Recv
							core.View{
								Class: "flex-1 p-2.5 rounded-lg bg-neutral-950/60 border border-neutral-800/50 gap-0.5",
								Children: []core.Node{
									core.Text{Content: "Packets In", Class: "text-[10px] text-neutral-400"},
									core.Text{
										ContentFunc: func() string { return fmt.Sprintf("%d", recvCount.Get()) },
										Class:       "text-lg font-bold font-mono text-white",
									},
								},
							},
							// p50 Latency
							core.View{
								Class: "flex-1 p-2.5 rounded-lg bg-neutral-950/60 border border-neutral-800/50 gap-0.5",
								Children: []core.Node{
									core.Text{Content: "p50 RTT", Class: "text-[10px] text-neutral-400"},
									core.Text{
										ContentFunc: p50Text.Get,
										Class:       "text-lg font-bold font-mono text-emerald-400",
									},
								},
							},
							// p95 Latency
							core.View{
								Class: "flex-1 p-2.5 rounded-lg bg-neutral-950/60 border border-neutral-800/50 gap-0.5",
								Children: []core.Node{
									core.Text{Content: "p95 RTT", Class: "text-[10px] text-neutral-400"},
									core.Text{
										ContentFunc: p95Text.Get,
										Class:       "text-lg font-bold font-mono text-cyan-400",
									},
								},
							},
							// Packets Dropped
							core.View{
								Class: "flex-1 p-2.5 rounded-lg bg-neutral-950/60 border border-neutral-800/50 gap-0.5",
								Children: []core.Node{
									core.Text{Content: "Dropped", Class: "text-[10px] text-neutral-400"},
									core.Text{
										ContentFunc: func() string { return fmt.Sprintf("%d", dropCount.Get()) },
										Class:       "text-lg font-bold font-mono text-neutral-300",
									},
								},
							},
						},
					},
				},
			},

			// Host Tuning Controls
			core.View{
				Class: "p-4 rounded-xl bg-neutral-900 border border-neutral-800 gap-3",
				Children: []core.Node{
					core.Text{Content: "Sensitivity & Controls", Class: "text-xs font-semibold text-neutral-400 tracking-wider uppercase"},
					core.View{
						Class: "flex-row justify-between items-center",
						Children: []core.Node{
							core.Text{Content: "Pointer Sensitivity", Class: "text-xs text-neutral-300"},
							core.View{
								Class: "flex-row gap-1 items-center",
								Children: []core.Node{
									base.Button{
										Label: "0.8x",
										OnPress: func(core.PressEvent) {
											_ = cfgMgr.Update(func(c *config.AppConfig) { c.Sensitivity = 0.8 })
											sensDisplay.Set("0.8x")
										},
										Class: "px-2.5 py-1 rounded bg-neutral-800 active:bg-neutral-700 text-xs text-neutral-300",
										Children: []core.Node{
											core.Text{Content: "0.8x", Class: "text-xs text-neutral-300"},
										},
									},
									base.Button{
										Label: "1.0x",
										OnPress: func(core.PressEvent) {
											_ = cfgMgr.Update(func(c *config.AppConfig) { c.Sensitivity = 1.0 })
											sensDisplay.Set("1.0x")
										},
										Class: "px-2.5 py-1 rounded bg-blue-600 active:bg-blue-700 text-xs text-white",
										Children: []core.Node{
											core.Text{Content: "1.0x", Class: "text-xs text-white font-medium"},
										},
									},
									base.Button{
										Label: "1.5x",
										OnPress: func(core.PressEvent) {
											_ = cfgMgr.Update(func(c *config.AppConfig) { c.Sensitivity = 1.5 })
											sensDisplay.Set("1.5x")
										},
										Class: "px-2.5 py-1 rounded bg-neutral-800 active:bg-neutral-700 text-xs text-neutral-300",
										Children: []core.Node{
											core.Text{Content: "1.5x", Class: "text-xs text-neutral-300"},
										},
									},
									base.Button{
										Label: "2.0x",
										OnPress: func(core.PressEvent) {
											_ = cfgMgr.Update(func(c *config.AppConfig) { c.Sensitivity = 2.0 })
											sensDisplay.Set("2.0x")
										},
										Class: "px-2.5 py-1 rounded bg-neutral-800 active:bg-neutral-700 text-xs text-neutral-300",
										Children: []core.Node{
											core.Text{Content: "2.0x", Class: "text-xs text-neutral-300"},
										},
									},
								},
							},
						},
					},
				},
			},

			// Bottom Actions
			core.View{
				Class: "flex-row justify-between items-center gap-3 mt-auto pt-2",
				Children: []core.Node{
					base.Button{
						Label: "Emergency Reset",
						OnPress: func(core.PressEvent) {
							_ = srv.Injector().ResetState()
						},
						Class: "flex-1 py-2 rounded-lg bg-neutral-800 hover:bg-neutral-700 active:bg-neutral-600 transition",
						Children: []core.Node{
							core.Text{Content: "Release Stuck Buttons", Class: "text-xs font-medium text-neutral-300 text-center"},
						},
					},
					base.Button{
						Label: "Pairing Info",
						OnPress: func(core.PressEvent) {
							_ = cfgMgr.Update(func(c *config.AppConfig) {})
						},
						Class: "px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-500 active:bg-blue-700 transition",
						Children: []core.Node{
							core.Text{Content: "Ready to Pair", Class: "text-xs font-medium text-white text-center"},
						},
					},
				},
			},
		},
	}
}

// DesktopPanel renders the compact panel shown when clicking the tray icon.
func DesktopPanel(cx *core.Scope, cfgMgr *config.Manager, srv *host.Server) core.Node {
	return DesktopWindow(cx, cfgMgr, srv)
}

// TrayIcon draws the touchpad icon in the menu bar tray.
func TrayIcon(cx *core.Scope) core.Node {
	return core.View{
		Class: "flex-row items-center justify-center size-full",
		Children: []core.Node{
			core.Text{
				Content: "⬚",
				Class:   "text-sm font-bold text-white",
			},
		},
	}
}
