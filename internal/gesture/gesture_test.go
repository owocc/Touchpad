package gesture

import (
	"sync"
	"testing"
	"time"

	"touchpad/internal/config"
	"touchpad/pkg/protocol"
)

func newTestEngine() (*Engine, *[]protocol.Packet, *sync.Mutex) {
	cfg := config.DefaultConfig()
	e := NewEngine(&cfg, 400.0, 800.0)
	var packets []protocol.Packet
	var mu sync.Mutex

	e.OnPacket = func(pkt *protocol.Packet) {
		mu.Lock()
		packets = append(packets, *pkt)
		mu.Unlock()
	}

	return e, &packets, &mu
}

func TestSingleFingerTap(t *testing.T) {
	e, pkts, mu := newTestEngine()
	now := time.Now()

	e.HandleTouchStart(0, 150.0, 300.0, now)
	e.HandleTouchEnd(0, 151.0, 300.5, now.Add(50*time.Millisecond))

	mu.Lock()
	defer mu.Unlock()

	if len(*pkts) != 2 {
		t.Fatalf("expected 2 packets (Down and Up), got %d", len(*pkts))
	}
	pDown := (*pkts)[0]
	pUp := (*pkts)[1]

	if pDown.Type != protocol.MsgMouseButton || pDown.AuxValue != int8(protocol.ButtonLeft) || pDown.Phase != protocol.Phase(protocol.ButtonDown) {
		t.Fatalf("unexpected down packet: %+v", pDown)
	}
	if pUp.Type != protocol.MsgMouseButton || pUp.AuxValue != int8(protocol.ButtonLeft) || pUp.Phase != protocol.Phase(protocol.ButtonUp) {
		t.Fatalf("unexpected up packet: %+v", pUp)
	}
}

func TestTwoFingerTap(t *testing.T) {
	e, pkts, mu := newTestEngine()
	now := time.Now()

	e.HandleTouchStart(0, 150.0, 300.0, now)
	e.HandleTouchStart(1, 190.0, 305.0, now.Add(5*time.Millisecond))

	e.HandleTouchEnd(0, 151.0, 300.5, now.Add(60*time.Millisecond))
	e.HandleTouchEnd(1, 191.0, 305.5, now.Add(65*time.Millisecond))

	mu.Lock()
	defer mu.Unlock()

	var rightClicks int
	for _, p := range *pkts {
		if p.Type == protocol.MsgMouseButton && p.AuxValue == int8(protocol.ButtonRight) {
			rightClicks++
		}
	}
	if rightClicks < 2 {
		t.Fatalf("expected right click down and up, got %d", rightClicks)
	}
}

func TestSingleFingerMove(t *testing.T) {
	e, pkts, mu := newTestEngine()
	now := time.Now()

	e.HandleTouchStart(0, 150.0, 300.0, now)
	// Move significantly
	e.HandleTouchMove(0, 170.0, 320.0, now.Add(30*time.Millisecond))
	e.HandleTouchMove(0, 190.0, 340.0, now.Add(40*time.Millisecond))
	e.HandleTouchEnd(0, 190.0, 340.0, now.Add(50*time.Millisecond))

	mu.Lock()
	defer mu.Unlock()

	var moveCount int
	for _, p := range *pkts {
		if p.Type == protocol.MsgPointerMove {
			moveCount++
		}
	}
	if moveCount == 0 {
		t.Fatal("expected pointer move packets")
	}
}

func TestEdgeScrollRight(t *testing.T) {
	e, pkts, mu := newTestEngine()
	now := time.Now()

	// Touch down in right gutter: padWidth = 400, gutter = 28 -> X >= 372
	e.HandleTouchStart(0, 385.0, 200.0, now)
	e.HandleTouchMove(0, 385.0, 240.0, now.Add(20*time.Millisecond))
	e.HandleTouchMove(0, 385.0, 280.0, now.Add(40*time.Millisecond))
	e.HandleTouchEnd(0, 385.0, 280.0, now.Add(60*time.Millisecond))

	mu.Lock()
	defer mu.Unlock()

	var scrollCount int
	for _, p := range *pkts {
		if p.Type == protocol.MsgScroll && p.Phase == protocol.PhaseChanged {
			scrollCount++
			if p.DeltaY == 0 {
				t.Fatalf("expected vertical scroll delta, got DeltaY == 0")
			}
		}
	}
	if scrollCount == 0 {
		t.Fatal("expected edge scroll packets")
	}
}

func TestTouchCancel(t *testing.T) {
	e, pkts, mu := newTestEngine()
	now := time.Now()

	e.HandleTouchStart(0, 100.0, 100.0, now)
	e.HandleTouchCancel()

	mu.Lock()
	defer mu.Unlock()

	if len(*pkts) != 1 {
		t.Fatalf("expected 1 packet on cancel, got %d", len(*pkts))
	}
	if (*pkts)[0].Type != protocol.MsgResetState {
		t.Fatalf("expected MsgResetState on cancel, got %v", (*pkts)[0].Type)
	}
}
