package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"touchpad/internal/config"
	"touchpad/internal/host"
	"touchpad/internal/inject"
)

func main() {
	port := flag.Int("port", config.DefaultUDPPort, "UDP listen port")
	adbPort := flag.Int("adb-port", config.DefaultADBPort, "ADB TCP listen port")
	discPort := flag.Int("disc-port", config.DefaultDiscoveryPort, "Discovery listen port")
	key := flag.String("key", "", "Pairing key / secret (leave empty for plaintext debug)")
	flag.Parse()

	log.Printf("Starting Touchpad Host Daemon on %s (%s)...", runtime.GOOS, runtime.GOARCH)

	cfg := config.DefaultConfig()
	cfg.UDPPort = *port
	cfg.ADBPort = *adbPort
	cfg.DiscoveryPort = *discPort
	cfg.PairingKey = *key

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

	// Print local IP addresses
	printHostAddresses(*port, *adbPort)
	if *key != "" {
		log.Printf("Pairing Key: %s (Encryption enabled: AES-256-GCM)", *key)
	} else {
		log.Printf("Pairing Key: [None] (Plaintext mode)")
	}

	// Background logger for telemetry
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			m := server.Metrics()
			p50, p95, _ := m.Percentiles()
			log.Printf("[Host Status] Recv: %d packets, Drop: %d, p50: %.2f ms, p95: %.2f ms, Active: %v",
				m.PacketsRecv, m.PacketsDrop, p50, p95, time.Since(m.LastActivity) < 2*time.Second)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	sig := <-sigChan
	log.Printf("Received signal: %v", sig)

	log.Println("Shutting down Touchpad Host...")
	server.Stop()
	log.Println("Touchpad Host stopped.")
}

func printHostAddresses(udpPort, adbPort int) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	fmt.Println("Available Host Addresses:")
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				fmt.Printf("  • %-12s : %s:%d (ADB forward port: %d)\n",
					iface.Name, ipnet.IP.String(), udpPort, adbPort)
			}
		}
	}
}
