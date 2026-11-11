package client

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"touchpad/internal/config"
	"touchpad/internal/gesture"
	"touchpad/internal/transport/lan"
	"touchpad/internal/transport/security"
	"touchpad/pkg/protocol"
)

type ConnectionType string

const (
	ConnLAN ConnectionType = "LAN"
	ConnADB ConnectionType = "ADB"
)

// Client coordinates gestures, connection management, encryption, and telemetry.
type Client struct {
	mu       sync.Mutex
	cfg      *config.AppConfig
	gesture  *gesture.Engine
	secChan  *security.Channel

	connType ConnectionType
	udpConn  *net.UDPConn
	tcpConn  net.Conn

	connected bool
	lastRTT   float64 // ms

	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewClient initializes a new Client instance.
func NewClient(cfg *config.AppConfig, width, height float64) (*Client, error) {
	c := &Client{
		cfg:      cfg,
		connType: ConnLAN,
		stopChan: make(chan struct{}),
	}

	if cfg.PairingKey != "" {
		sec, err := security.NewChannel(cfg.PairingKey)
		if err != nil {
			return nil, fmt.Errorf("init security channel: %w", err)
		}
		c.secChan = sec
	}

	c.gesture = gesture.NewEngine(cfg, width, height)
	c.gesture.OnPacket = c.sendPacket

	return c, nil
}

// Gesture returns the gesture engine.
func (c *Client) Gesture() *gesture.Engine {
	return c.gesture
}

// ConnectLAN establishes connection via LAN UDP.
func (c *Client) ConnectLAN(hostIP string, port int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if port <= 0 {
		port = c.cfg.UDPPort
	}
	remoteAddr := fmt.Sprintf("%s:%d", hostIP, port)
	conn, err := lan.DialUDP(remoteAddr)
	if err != nil {
		return fmt.Errorf("dial UDP to %s: %w", remoteAddr, err)
	}

	c.closeConnsLocked()
	c.udpConn = conn
	c.connType = ConnLAN
	c.connected = true

	c.startWorkersLocked()
	return nil
}

// ConnectADB establishes connection via ADB TCP tunnel.
func (c *Client) ConnectADB(port int) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if port <= 0 {
		port = c.cfg.ADBPort
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 1500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("dial ADB TCP to %s: %w", addr, err)
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetNoDelay(true)
	}

	c.closeConnsLocked()
	c.tcpConn = conn
	c.connType = ConnADB
	c.connected = true

	c.startWorkersLocked()
	return nil
}

func (c *Client) markDisconnected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connected {
		c.connected = false
		if c.stopChan != nil {
			select {
			case <-c.stopChan:
			default:
				close(c.stopChan)
			}
		}
		if c.udpConn != nil {
			_ = c.udpConn.Close()
			c.udpConn = nil
		}
		if c.tcpConn != nil {
			_ = c.tcpConn.Close()
			c.tcpConn = nil
		}
	}
}

func (c *Client) closeConnsLocked() {
	if c.stopChan != nil {
		select {
		case <-c.stopChan:
		default:
			close(c.stopChan)
		}
	}
	if c.udpConn != nil {
		_ = c.udpConn.Close()
		c.udpConn = nil
	}
	if c.tcpConn != nil {
		_ = c.tcpConn.Close()
		c.tcpConn = nil
	}
	c.connected = false
}

func (c *Client) startWorkersLocked() {
	c.stopChan = make(chan struct{})
	c.wg.Add(2)
	go c.runHeartbeat()
	go c.runReceiver()
}

func (c *Client) sendPacket(pkt *protocol.Packet) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return
	}

	buf := pkt.Encode(nil)

	if c.secChan != nil {
		enc, err := c.secChan.Encrypt(buf)
		if err != nil {
			return
		}
		buf = enc
	}

	if c.connType == ConnLAN && c.udpConn != nil {
		_, _ = c.udpConn.Write(buf)
	} else if c.connType == ConnADB && c.tcpConn != nil {
		hdr := make([]byte, 2)
		binary.LittleEndian.PutUint16(hdr, uint16(len(buf)))
		_ = c.tcpConn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := c.tcpConn.Write(hdr); err != nil {
			go c.markDisconnected()
			return
		}
		if _, err := c.tcpConn.Write(buf); err != nil {
			go c.markDisconnected()
			return
		}
	}
}

func (c *Client) runHeartbeat() {
	defer c.wg.Done()
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopChan:
			return
		case <-ticker.C:
			nowMicros := uint32(time.Now().UnixMicro())
			c.sendPacket(&protocol.Packet{
				Type:            protocol.MsgPing,
				TimestampMicros: nowMicros,
			})
		}
	}
}

func (c *Client) runReceiver() {
	defer c.wg.Done()

	for {
		select {
		case <-c.stopChan:
			return
		default:
		}

		c.mu.Lock()
		connType := c.connType
		udp := c.udpConn
		tcp := c.tcpConn
		c.mu.Unlock()

		if connType == ConnLAN {
			if udp == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			buf := make([]byte, 1024)
			_ = udp.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			n, err := udp.Read(buf)
			if err != nil {
				continue
			}
			c.handleIncoming(buf[:n])
		} else if connType == ConnADB {
			if tcp == nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			hdr := make([]byte, 2)
			_ = tcp.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := io.ReadFull(tcp, hdr); err != nil {
				c.markDisconnected()
				return
			}
			frameLen := int(binary.LittleEndian.Uint16(hdr))
			if frameLen <= 0 || frameLen > 1024 {
				c.markDisconnected()
				return
			}
			frame := make([]byte, frameLen)
			if _, err := io.ReadFull(tcp, frame); err != nil {
				c.markDisconnected()
				return
			}
			c.handleIncoming(frame)
		}
	}
}

func (c *Client) handleIncoming(raw []byte) {
	if c.secChan != nil {
		dec, err := c.secChan.Decrypt(raw)
		if err != nil {
			return
		}
		raw = dec
	}

	var resp protocol.Packet
	if err := resp.Decode(raw); err == nil {
		if resp.Type == protocol.MsgPong {
			sentMicros := resp.TimestampMicros
			nowMicros := uint32(time.Now().UnixMicro())
			rttMs := float64(nowMicros-sentMicros) / 1000.0
			if rttMs > 0 && rttMs < 5000 {
				c.mu.Lock()
				c.lastRTT = rttMs
				c.mu.Unlock()
			}
		}
	}
}

// LastRTT returns the most recent ping round-trip time in ms.
func (c *Client) LastRTT() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastRTT
}

// IsConnected returns current connection status.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// Disconnect gracefully disconnects from host.
func (c *Client) Disconnect() {
	c.sendPacket(&protocol.Packet{
		Type:  protocol.MsgResetState,
		Flags: protocol.FlagReliable,
	})

	c.mu.Lock()
	if c.connected {
		c.closeConnsLocked()
	}
	c.mu.Unlock()
	c.wg.Wait()
}
