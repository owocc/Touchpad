package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"touchpad/internal/client"
	"touchpad/internal/config"
	"touchpad/internal/transport/lan"
)

func main() {
	hostIP := flag.String("host", "", "Host IP address (leave empty to scan)")
	port := flag.Int("port", config.DefaultUDPPort, "Host UDP port")
	adbMode := flag.Bool("adb", false, "Connect via ADB TCP forward (127.0.0.1:29181)")
	key := flag.String("key", "", "Pairing key / secret")
	flag.Parse()

	cfg := config.DefaultConfig()
	cfg.PairingKey = *key

	cli, err := client.NewClient(&cfg, 400.0, 800.0)
	if err != nil {
		log.Fatalf("Failed to initialize client: %v", err)
	}

	targetIP := *hostIP
	targetPort := *port

	if *adbMode {
		fmt.Printf("Connecting to Desktop Host via ADB tunnel at 127.0.0.1:%d...\n", targetPort)
		if err := cli.ConnectADB(targetPort); err != nil {
			log.Fatalf("ADB connection failed: %v", err)
		}
	} else {
		if targetIP == "" {
			fmt.Println("Scanning local network for Touchpad Hosts...")
			hosts, err := lan.DiscoverHosts(config.DefaultDiscoveryPort, 1200*time.Millisecond)
			if err != nil || len(hosts) == 0 {
				fmt.Println("No hosts discovered automatically.")
				fmt.Print("Enter Host IP: ")
				reader := bufio.NewReader(os.Stdin)
				targetIP, _ = reader.ReadString('\n')
				targetIP = strings.TrimSpace(targetIP)
			} else {
				fmt.Printf("Discovered %d Host(s):\n", len(hosts))
				for i, h := range hosts {
					fmt.Printf("  [%d] %s (%s) @ %s:%d\n", i+1, h.HostName, h.OS, h.IP, h.Port)
				}
				if len(hosts) == 1 {
					targetIP = hosts[0].IP
					targetPort = hosts[0].Port
				} else {
					fmt.Print("Select host number [1]: ")
					reader := bufio.NewReader(os.Stdin)
					choiceStr, _ := reader.ReadString('\n')
					idx, err := strconv.Atoi(strings.TrimSpace(choiceStr))
					if err == nil && idx >= 1 && idx <= len(hosts) {
						targetIP = hosts[idx-1].IP
						targetPort = hosts[idx-1].Port
					} else {
						targetIP = hosts[0].IP
						targetPort = hosts[0].Port
					}
				}
			}
		}

		if targetIP == "" {
			log.Fatal("No host specified.")
		}

		fmt.Printf("Connecting to %s:%d over LAN UDP...\n", targetIP, targetPort)
		if err := cli.ConnectLAN(targetIP, targetPort); err != nil {
			log.Fatalf("LAN connection failed: %v", err)
		}
	}

	fmt.Println("Connected! Starting motion test...")
	time.Sleep(200 * time.Millisecond)
	fmt.Printf("Initial Ping RTT: %.2f ms\n", cli.LastRTT())

	// Simulate a smooth circle mouse movement test to verify pipeline
	fmt.Println("Simulating cursor test circle...")
	now := time.Now()
	cli.Gesture().HandleTouchStart(0, 200.0, 400.0, now)

	const steps = 60
	for i := 0; i <= steps; i++ {
		now = now.Add(16 * time.Millisecond)
		angle := float64(i) * (2.0 * 3.14159 / float64(steps))
		x := 200.0 + 50.0*angle
		y := 400.0 + 30.0*angle
		cli.Gesture().HandleTouchMove(0, x, y, now)
		time.Sleep(16 * time.Millisecond)
	}
	cli.Gesture().HandleTouchEnd(0, 200.0, 400.0, now.Add(16*time.Millisecond))

	fmt.Println("Test movement completed.")
	fmt.Printf("Ping RTT: %.2f ms\n", cli.LastRTT())

	cli.Disconnect()
}
