package host

import (
	"testing"
	"time"

	"touchpad/internal/client"
	"touchpad/internal/config"
)

func TestSecurityPairingEnforcement(t *testing.T) {
	mock := &mockInjector{}
	cfg := config.DefaultConfig()
	cfg.UDPPort = 39380
	cfg.PairingKey = "CORRECT_SECRET_KEY"

	server, err := NewServer(&cfg, mock)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	defer server.Stop()

	// 1. Unauthenticated client (no key or wrong key)
	badCfg := config.DefaultConfig()
	badCfg.PairingKey = "WRONG_SECRET_KEY"
	badCli, err := client.NewClient(&badCfg, 400.0, 800.0)
	if err != nil {
		t.Fatalf("failed to create bad client: %v", err)
	}
	if err := badCli.ConnectLAN("127.0.0.1", 39380); err != nil {
		t.Fatalf("bad client connect error: %v", err)
	}
	defer badCli.Disconnect()

	// Send single finger move
	now := time.Now()
	badCli.Gesture().HandleTouchStart(0, 100.0, 100.0, now)
	badCli.Gesture().HandleTouchMove(0, 150.0, 150.0, now.Add(20*time.Millisecond))
	badCli.Gesture().HandleTouchEnd(0, 150.0, 150.0, now.Add(40*time.Millisecond))

	time.Sleep(100 * time.Millisecond)

	mock.mu.Lock()
	badMoves := len(mock.moves)
	mock.mu.Unlock()

	if badMoves != 0 {
		t.Fatalf("unauthorized client injected %d events! Expected 0", badMoves)
	}

	// 2. Authenticated client with correct key
	goodCfg := config.DefaultConfig()
	goodCfg.PairingKey = "CORRECT_SECRET_KEY"
	goodCli, err := client.NewClient(&goodCfg, 400.0, 800.0)
	if err != nil {
		t.Fatalf("failed to create good client: %v", err)
	}
	if err := goodCli.ConnectLAN("127.0.0.1", 39380); err != nil {
		t.Fatalf("good client connect error: %v", err)
	}
	defer goodCli.Disconnect()

	goodCli.Gesture().HandleTouchStart(0, 200.0, 200.0, now)
	goodCli.Gesture().HandleTouchMove(0, 250.0, 250.0, now.Add(20*time.Millisecond))
	goodCli.Gesture().HandleTouchEnd(0, 250.0, 250.0, now.Add(40*time.Millisecond))

	time.Sleep(100 * time.Millisecond)

	mock.mu.Lock()
	goodMoves := len(mock.moves)
	mock.mu.Unlock()

	if goodMoves == 0 {
		t.Fatal("authorized client failed to inject move events!")
	}
}
