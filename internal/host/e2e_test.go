package host

import (
	"sync"
	"testing"
	"time"

	"touchpad/internal/client"
	"touchpad/internal/config"
	"touchpad/pkg/protocol"
)

// mockInjector records all injected calls for verification.
type mockInjector struct {
	mu        sync.Mutex
	moves     [][2]float32
	buttons   []struct{ btn uint8; down bool }
	scrolls   []struct{ dx, dy float32; p protocol.Phase; m protocol.MomentumPhase }
	resets    int
}

func (m *mockInjector) MouseMove(dx, dy float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.moves = append(m.moves, [2]float32{dx, dy})
	return nil
}

func (m *mockInjector) MouseButton(button uint8, down bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buttons = append(m.buttons, struct{ btn uint8; down bool }{button, down})
	return nil
}

func (m *mockInjector) MouseClick(button uint8, count int) error {
	_ = m.MouseButton(button, true)
	return m.MouseButton(button, false)
}

func (m *mockInjector) Scroll(dx, dy float32, phase protocol.Phase, momentumPhase protocol.MomentumPhase) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scrolls = append(m.scrolls, struct{ dx, dy float32; p protocol.Phase; m protocol.MomentumPhase }{dx, dy, phase, momentumPhase})
	return nil
}

func (m *mockInjector) ResetState() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets++
	return nil
}

func (m *mockInjector) HasPermission() bool { return true }
func (m *mockInjector) RequestPermission()   {}

func (m *mockInjector) Close() error {
	return nil
}

func TestEndToEndLoopEncrypted(t *testing.T) {
	mock := &mockInjector{}
	cfg := config.DefaultConfig()
	cfg.UDPPort = 39280
	cfg.ADBPort = 39281
	cfg.DiscoveryPort = 39282
	cfg.PairingKey = "TOP_SECRET_PASSPHRASE"

	server, err := NewServer(&cfg, mock)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// Create client with matching pairing key
	clientCfg := config.DefaultConfig()
	clientCfg.PairingKey = "TOP_SECRET_PASSPHRASE"
	cli, err := client.NewClient(&clientCfg, 400.0, 800.0)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	if err := cli.ConnectLAN("127.0.0.1", 39280); err != nil {
		t.Fatalf("failed to connect LAN: %v", err)
	}
	defer cli.Disconnect()

	// Wait briefly for connection and initial ping
	time.Sleep(100 * time.Millisecond)

	// Simulate touch events
	now := time.Now()
	// Single finger move
	cli.Gesture().HandleTouchStart(0, 100.0, 100.0, now)
	cli.Gesture().HandleTouchMove(0, 120.0, 120.0, now.Add(20*time.Millisecond))
	cli.Gesture().HandleTouchMove(0, 140.0, 140.0, now.Add(40*time.Millisecond))
	cli.Gesture().HandleTouchEnd(0, 140.0, 140.0, now.Add(60*time.Millisecond))

	// Single finger tap -> Left Click
	now = now.Add(100 * time.Millisecond)
	cli.Gesture().HandleTouchStart(0, 200.0, 200.0, now)
	cli.Gesture().HandleTouchEnd(0, 201.0, 200.5, now.Add(40*time.Millisecond))

	// Allow UDP packets to reach server
	time.Sleep(150 * time.Millisecond)

	mock.mu.Lock()
	defer mock.mu.Unlock()

	if len(mock.moves) == 0 {
		t.Fatal("expected server to receive MouseMove events")
	}
	if len(mock.buttons) < 2 {
		t.Fatalf("expected server to receive MouseButton events, got %d", len(mock.buttons))
	}

	metrics := server.Metrics()
	if metrics.PacketsRecv == 0 {
		t.Fatal("expected server metrics to count received packets")
	}
	t.Logf("End-to-End Test Passed! Recv=%d, Drop=%d, Moves=%d, Buttons=%d",
		metrics.PacketsRecv, metrics.PacketsDrop, len(mock.moves), len(mock.buttons))
}
