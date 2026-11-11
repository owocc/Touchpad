package filter

import (
	"sync"
	"testing"
	"time"

	"touchpad/pkg/protocol"
)

func TestOneEuroFilter(t *testing.T) {
	filter := NewOneEuro2D(1.2, 0.04, 1.0)
	now := time.Now()

	// Initial sample
	x1, y1 := filter.Filter(100.0, 200.0, now)
	if x1 != 100.0 || y1 != 200.0 {
		t.Fatalf("first sample should be unfiltered, got (%v, %v)", x1, y1)
	}

	// Tiny jitter step (0.1 pt) 8ms later -> should be heavily smoothed
	now = now.Add(8 * time.Millisecond)
	x2, y2 := filter.Filter(100.1, 200.1, now)
	if x2 >= 100.1 || y2 >= 200.1 {
		t.Fatalf("subpixel jitter should be smoothed, got (%v, %v)", x2, y2)
	}

	// Fast flick step (50 pt) 8ms later -> filter should open up responsiveness
	now = now.Add(8 * time.Millisecond)
	x3, _ := filter.Filter(150.0, 200.0, now)
	if x3 < 115.0 {
		t.Fatalf("fast flick should respond quickly, got %v", x3)
	}
}

func TestSigmoidAccel(t *testing.T) {
	accel := NewSigmoidAccel(1.0)

	// Slow creep: sub-pixel remainder accumulation
	dx1, dy1 := accel.Accelerate(0.4, 0.4, 0.016)
	if dx1 != 0 || dy1 != 0 {
		t.Fatalf("expected 0 with remainder, got (%v, %v)", dx1, dy1)
	}

	// Second slow creep step -> remainder should push it over 1.0
	dx2, dy2 := accel.Accelerate(2.0, 2.0, 0.016)
	if dx2 < 1 || dy2 < 1 {
		t.Fatalf("expected remainder to kick in, got (%v, %v)", dx2, dy2)
	}

	// Fast motion
	dxFast, _ := accel.Accelerate(50.0, 0, 0.008)
	if dxFast < 100 {
		t.Fatalf("expected high acceleration gain on fast motion, got %v", dxFast)
	}
}

func TestMomentumGenerator(t *testing.T) {
	gen := NewMomentumGenerator()

	var wg sync.WaitGroup
	wg.Add(1)

	var phases []protocol.MomentumPhase
	var mu sync.Mutex

	gen.Start(200.0, 200.0, func(dx, dy float32, phase protocol.MomentumPhase) {
		mu.Lock()
		phases = append(phases, phase)
		mu.Unlock()

		if phase == protocol.MomentumEnd {
			wg.Done()
		}
	})

	// Wait for momentum completion or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("momentum generation timed out")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(phases) < 3 {
		t.Fatalf("expected multiple momentum frames, got %d", len(phases))
	}
	if phases[0] != protocol.MomentumBegin {
		t.Fatalf("first phase should be Begin, got %v", phases[0])
	}
	if phases[len(phases)-1] != protocol.MomentumEnd {
		t.Fatalf("last phase should be End, got %v", phases[len(phases)-1])
	}
}

func TestMomentumCancel(t *testing.T) {
	gen := NewMomentumGenerator()
	cancelled := make(chan bool, 1)

	gen.Start(300.0, 300.0, func(dx, dy float32, phase protocol.MomentumPhase) {
		if phase == protocol.MomentumEnd {
			select {
			case cancelled <- true:
			default:
			}
		}
	})

	time.Sleep(20 * time.Millisecond)
	if !gen.Cancel() {
		t.Fatal("expected Cancel() to return true for active momentum")
	}

	select {
	case <-cancelled:
		// Succeeded
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected MomentumEnd callback on Cancel")
	}
}
