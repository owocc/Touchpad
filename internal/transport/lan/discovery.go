package lan

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	DiscoverProbe = "TOUCHPAD_DISCOVER"
)

// HostInfo contains discovery metadata.
type HostInfo struct {
	HostName string `json:"host_name"`
	OS       string `json:"os"`
	IP       string `json:"ip"`
	Port     int    `json:"port"`
	ADBPort  int    `json:"adb_port"`
	Paired   bool   `json:"paired"`
}

// DiscoveryService manages LAN discovery response on the Host side.
type DiscoveryService struct {
	info       HostInfo
	listenPort int
	conn       *net.UDPConn
	stopChan   chan struct{}
	wg         sync.WaitGroup
}

// NewDiscoveryService creates a new discovery listener on the host.
func NewDiscoveryService(port, hostPort, adbPort int, paired bool) *DiscoveryService {
	hostName, _ := os.Hostname()
	if hostName == "" {
		hostName = "Desktop-Host"
	}

	return &DiscoveryService{
		info: HostInfo{
			HostName: hostName,
			OS:       runtime.GOOS,
			Port:     hostPort,
			ADBPort:  adbPort,
			Paired:   paired,
		},
		listenPort: port,
		stopChan:   make(chan struct{}),
	}
}

// Start begins listening for client discovery probes.
func (d *DiscoveryService) Start() error {
	addr := fmt.Sprintf("0.0.0.0:%d", d.listenPort)
	conn, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return err
	}
	d.conn = conn.(*net.UDPConn)

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		buf := make([]byte, 512)
		for {
			select {
			case <-d.stopChan:
				return
			default:
			}

			_ = d.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, remoteAddr, err := d.conn.ReadFrom(buf)
			if err != nil {
				continue
			}

			msg := strings.TrimSpace(string(buf[:n]))
			if msg == DiscoverProbe {
				// Reply with HostInfo
				resp, _ := json.Marshal(d.info)
				_, _ = d.conn.WriteTo(resp, remoteAddr)
			}
		}
	}()

	return nil
}

// Stop stops the discovery responder.
func (d *DiscoveryService) Stop() {
	close(d.stopChan)
	if d.conn != nil {
		_ = d.conn.Close()
	}
	d.wg.Wait()
}

// DiscoverHosts scans local network interfaces and broadcasts discovery probes.
func DiscoverHosts(discoveryPort int, timeout time.Duration) ([]HostInfo, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	destAddr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", discoveryPort))
	if err != nil {
		return nil, err
	}

	// Send probe
	_, err = conn.WriteTo([]byte(DiscoverProbe), destAddr)
	if err != nil {
		return nil, err
	}

	// Also send to directed broadcast addresses for all active IPv4 interfaces
	ifaces, _ := net.Interfaces()
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipNet, ok := a.(*net.IPNet); ok && ipNet.IP.To4() != nil {
				// Broadcast address = IP | ^Netmask
				ip := ipNet.IP.To4()
				mask := ipNet.Mask
				if len(mask) == 4 {
					bcast := net.IPv4(
						ip[0]|^mask[0],
						ip[1]|^mask[1],
						ip[2]|^mask[2],
						ip[3]|^mask[3],
					)
					bcastAddr := &net.UDPAddr{IP: bcast, Port: discoveryPort}
					_, _ = conn.WriteTo([]byte(DiscoverProbe), bcastAddr)
				}
			}
		}
	}

	var results []HostInfo
	seen := make(map[string]bool)
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1024)

	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		n, remoteAddr, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}

		var info HostInfo
		if err := json.Unmarshal(buf[:n], &info); err == nil {
			// Extract IP from remoteAddr
			if udpAddr, ok := remoteAddr.(*net.UDPAddr); ok {
				info.IP = udpAddr.IP.String()
			}
			key := fmt.Sprintf("%s:%d", info.IP, info.Port)
			if !seen[key] {
				seen[key] = true
				results = append(results, info)
			}
		}
	}

	return results, nil
}
