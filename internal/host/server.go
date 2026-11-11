package host

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"
	"time"

	"touchpad/internal/config"
	"touchpad/internal/inject"
	"touchpad/internal/transport/adb"
	"touchpad/internal/transport/lan"
	"touchpad/internal/transport/security"
	"touchpad/pkg/protocol"
)

// Metrics records performance and latency statistics.
type Metrics struct {
	mu           sync.Mutex
	PacketsRecv  uint64
	PacketsDrop  uint64
	RTTs         []float64 // Last 200 ping RTTs in milliseconds
	LastActivity time.Time
}

// AddRTT registers a new round-trip measurement.
func (m *Metrics) AddRTT(rttMs float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.RTTs = append(m.RTTs, rttMs)
	if len(m.RTTs) > 200 {
		m.RTTs = m.RTTs[len(m.RTTs)-200:]
	}
}

// Percentiles calculates p50, p95, p99 latency in ms.
func (m *Metrics) Percentiles() (p50, p95, p99 float64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.RTTs) == 0 {
		return 0, 0, 0
	}
	sorted := make([]float64, len(m.RTTs))
	copy(sorted, m.RTTs)
	sort.Float64s(sorted)

	n := len(sorted)
	p50 = sorted[int(float64(n)*0.50)]
	p95 = sorted[int(float64(n)*0.95)]
	p99 = sorted[int(float64(n)*0.99)]
	return
}

// Server is the Desktop Host daemon that accepts UDP/TCP connections,
// validates frames, tracks safety watchdog, and injects inputs into the OS.
type Server struct {
	mu       sync.Mutex
	cfg      *config.AppConfig
	injector inject.Injector
	seqTrack protocol.SequenceTracker
	secChan  *security.Channel

	udpConn   *net.UDPConn
	tcpListen net.Listener
	discServ  *lan.DiscoveryService
	adbMgr    *adb.Manager

	metrics Metrics

	running  bool
	stopChan chan struct{}
	wg       sync.WaitGroup
}

// NewServer creates a new Host Server instance.
func NewServer(cfg *config.AppConfig, inj inject.Injector) (*Server, error) {
	var secChan *security.Channel
	var err error
	if cfg.PairingKey != "" {
		secChan, err = security.NewChannel(cfg.PairingKey)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize security channel: %w", err)
		}
	}

	return &Server{
		cfg:      cfg,
		injector: inj,
		secChan:  secChan,
		adbMgr:   adb.NewManager(),
		stopChan: make(chan struct{}),
	}, nil
}

// SetPairingKey updates or clears the secret key.
func (s *Server) SetPairingKey(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg.PairingKey = key
	if key == "" {
		s.secChan = nil
		return nil
	}
	ch, err := security.NewChannel(key)
	if err != nil {
		return err
	}
	s.secChan = ch
	return nil
}

// Start begins listening on LAN UDP and ADB TCP.
func (s *Server) Start() error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	// 1. LAN UDP listener
	udpAddr := fmt.Sprintf("0.0.0.0:%d", s.cfg.UDPPort)
	conn, err := lan.ListenUDP(udpAddr)
	if err != nil {
		return fmt.Errorf("listen UDP on %s: %w", udpAddr, err)
	}
	s.udpConn = conn

	// 2. TCP listener (for ADB port forward or direct TCP)
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", s.cfg.ADBPort)
	tcpL, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		// Log but continue if port busy
		log.Printf("[Host] Warning: unable to bind ADB TCP listener on %s: %v", tcpAddr, err)
	} else {
		s.tcpListen = tcpL
		s.wg.Add(1)
		go s.runTCP()
	}

	// 3. Discovery Service
	s.discServ = lan.NewDiscoveryService(s.cfg.DiscoveryPort, s.cfg.UDPPort, s.cfg.ADBPort, s.secChan != nil)
	_ = s.discServ.Start()

	// 4. UDP packet loop
	s.wg.Add(1)
	go s.runUDP()

	// 5. Watchdog loop (auto-release buttons if connection goes silent)
	s.wg.Add(1)
	go s.runWatchdog()

	return nil
}

func (s *Server) runUDP() {
	defer s.wg.Done()
	buf := make([]byte, 1024)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		_ = s.udpConn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, remoteAddr, err := s.udpConn.ReadFrom(buf)
		if err != nil {
			continue
		}

		s.handlePacketBytes(buf[:n], func(reply []byte) {
			_, err := s.udpConn.WriteTo(reply, remoteAddr)
			if err != nil {
				log.Printf("[Host] WriteTo error: %v", err)
			}
		})
	}
}

func (s *Server) runTCP() {
	defer s.wg.Done()

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		conn, err := s.tcpListen.Accept()
		if err != nil {
			continue
		}

		// When a new client connects via TCP, reset button state
		_ = s.injector.ResetState()

		go s.handleTCPConn(conn)
	}
}

func (s *Server) handleTCPConn(conn net.Conn) {
	defer conn.Close()
	defer func() {
		// Client disconnected -> release held buttons
		_ = s.injector.ResetState()
	}()

	// Read 2-byte length-prefixed frames over TCP
	lenBuf := make([]byte, 2)
	frameBuf := make([]byte, 1024)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return
		}

		frameLen := int(binary.LittleEndian.Uint16(lenBuf))
		if frameLen <= 0 || frameLen > len(frameBuf) {
			return
		}

		if _, err := io.ReadFull(conn, frameBuf[:frameLen]); err != nil {
			return
		}

		s.handlePacketBytes(frameBuf[:frameLen], func(reply []byte) {
			respHeader := make([]byte, 2)
			binary.LittleEndian.PutUint16(respHeader, uint16(len(reply)))
			_, _ = conn.Write(respHeader)
			_, _ = conn.Write(reply)
		})
	}
}

func (s *Server) handlePacketBytes(raw []byte, replyFn func([]byte)) {
	s.mu.Lock()
	s.metrics.LastActivity = time.Now()
	s.metrics.PacketsRecv++
	sec := s.secChan
	s.mu.Unlock()

	var payload []byte
	if sec != nil {
		decrypted, err := sec.Decrypt(raw)
		if err != nil {
			s.mu.Lock()
			s.metrics.PacketsDrop++
			s.mu.Unlock()
			return
		}
		payload = decrypted
	} else {
		payload = raw
	}

	var pkt protocol.Packet
	if err := pkt.Decode(payload); err != nil {
		s.mu.Lock()
		s.metrics.PacketsDrop++
		s.mu.Unlock()
		return
	}

	s.mu.Lock()
	shouldProcess := s.seqTrack.ShouldProcess(&pkt)
	if !shouldProcess {
		s.metrics.PacketsDrop++
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	s.processPacket(&pkt, replyFn)
}

func (s *Server) processPacket(pkt *protocol.Packet, replyFn func([]byte)) {
	switch pkt.Type {
	case protocol.MsgPointerMove:
		_ = s.injector.MouseMove(pkt.DeltaX, pkt.DeltaY)

	case protocol.MsgMouseButton:
		down := pkt.Phase == protocol.Phase(protocol.ButtonDown)
		_ = s.injector.MouseButton(uint8(pkt.AuxValue), down)

	case protocol.MsgScroll:
		_ = s.injector.Scroll(pkt.DeltaX, pkt.DeltaY, pkt.Phase, pkt.MomentumPhase)

	case protocol.MsgResetState:
		_ = s.injector.ResetState()

	case protocol.MsgPing:
		// Echo timestamp back as Pong
		pong := protocol.Packet{
			Type:            protocol.MsgPong,
			SequenceNumber:  pkt.SequenceNumber,
			TimestampMicros: pkt.TimestampMicros,
		}
		buf := pong.Encode(nil)
		if s.secChan != nil {
			if enc, err := s.secChan.Encrypt(buf); err == nil {
				buf = enc
			}
		}
		replyFn(buf)

	case protocol.MsgHeartbeat:
		// Keepalive packet, activity timestamp already updated
	}
}

func (s *Server) runWatchdog() {
	defer s.wg.Done()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			s.mu.Lock()
			last := s.metrics.LastActivity
			s.mu.Unlock()

			if !last.IsZero() && time.Since(last) > 800*time.Millisecond {
				// No packets for 800ms -> release stuck buttons & stop inertia
				_ = s.injector.ResetState()
			}
		}
	}
}

// Metrics returns the server's telemetry metrics.
func (s *Server) Metrics() *Metrics {
	return &s.metrics
}

// Injector returns the server's input injector.
func (s *Server) Injector() inject.Injector {
	return s.injector
}

// Stop shuts down the server.
func (s *Server) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stopChan)
	s.mu.Unlock()

	if s.discServ != nil {
		s.discServ.Stop()
	}
	if s.udpConn != nil {
		_ = s.udpConn.Close()
	}
	if s.tcpListen != nil {
		_ = s.tcpListen.Close()
	}

	_ = s.injector.ResetState()
	_ = s.injector.Close()

	s.wg.Wait()
}
