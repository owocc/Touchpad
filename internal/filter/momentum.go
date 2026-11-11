package filter

import (
	"math"
	"sync"
	"time"

	"touchpad/pkg/protocol"
)

// MomentumCallback receives calculated delta frames and momentum phase.
type MomentumCallback func(dx, dy float32, phase protocol.MomentumPhase)

// MomentumGenerator manages simulated trackpad glide deceleration.
type MomentumGenerator struct {
	mu         sync.Mutex
	active     bool
	cancelChan chan struct{}

	minVelocity float64 // Minimum velocity required to trigger inertia (pt/s)
	cutVelocity float64 // Velocity below which inertia ends (pt/s)
	friction    float64 // Exponential decay rate per second
}

// NewMomentumGenerator creates an inertia engine.
func NewMomentumGenerator() *MomentumGenerator {
	return &MomentumGenerator{
		minVelocity: 40.0,  // pt/s
		cutVelocity: 2.0,   // pt/s
		friction:    3.8,   // decay factor: v(t) = v0 * exp(-friction * t)
	}
}

// Start begins emitting decaying inertial momentum frames.
// If a prior inertia glide was running, it is cancelled first.
func (m *MomentumGenerator) Start(vx, vy float64, cb MomentumCallback) {
	m.Cancel()

	speed := math.Hypot(vx, vy)
	if speed < m.minVelocity {
		return
	}

	m.mu.Lock()
	m.active = true
	m.cancelChan = make(chan struct{})
	cancel := m.cancelChan
	m.mu.Unlock()

	go m.run(vx, vy, cancel, cb)
}

func (m *MomentumGenerator) run(v0x, v0y float64, cancel chan struct{}, cb MomentumCallback) {
	const frameDuration = 8 * time.Millisecond // 120 Hz glide updates
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	// Initial begin frame
	dt := frameDuration.Seconds()
	dx := float32(v0x * dt)
	dy := float32(v0y * dt)

	cb(dx, dy, protocol.MomentumBegin)

	vx := v0x
	vy := v0y
	decay := math.Exp(-m.friction * dt)

	for {
		select {
		case <-cancel:
			cb(0, 0, protocol.MomentumEnd)
			return

		case <-ticker.C:
			vx *= decay
			vy *= decay
			currentSpeed := math.Hypot(vx, vy)

			if currentSpeed < m.cutVelocity {
				m.mu.Lock()
				m.active = false
				m.mu.Unlock()
				cb(0, 0, protocol.MomentumEnd)
				return
			}

			frameDx := float32(vx * dt)
			frameDy := float32(vy * dt)
			cb(frameDx, frameDy, protocol.MomentumContinue)
		}
	}
}

// Cancel immediately halts ongoing inertia.
// Returns true if an active glide was terminated.
func (m *MomentumGenerator) Cancel() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.active {
		return false
	}
	m.active = false
	if m.cancelChan != nil {
		close(m.cancelChan)
		m.cancelChan = nil
	}
	return true
}

// IsActive checks if an inertial glide is currently running.
func (m *MomentumGenerator) IsActive() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active
}
