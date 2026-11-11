package lan

import (
	"net"
)

// ConfigureLowLatencyUDP tunes a UDP connection for minimum latency.
// Calls platform-specific socket options.
func ConfigureLowLatencyUDP(conn *net.UDPConn) error {
	return setLowLatencySocketOpts(conn)
}

// ListenUDP creates a configured low-latency UDP listener.
func ListenUDP(addr string) (*net.UDPConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, err
	}
	_ = ConfigureLowLatencyUDP(conn)
	return conn, nil
}

// DialUDP creates a configured low-latency UDP client connection.
func DialUDP(remoteAddr string) (*net.UDPConn, error) {
	rAddr, err := net.ResolveUDPAddr("udp4", remoteAddr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp4", nil, rAddr)
	if err != nil {
		return nil, err
	}
	_ = ConfigureLowLatencyUDP(conn)
	return conn, nil
}
