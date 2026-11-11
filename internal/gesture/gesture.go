package gesture

import (
	"math"
	"sync"
	"time"

	"touchpad/internal/config"
	"touchpad/internal/filter"
	"touchpad/pkg/protocol"
)

// State tracks the gesture engine's current operational mode.
type State uint8

const (
	StateIdle State = iota
	State1FingerPending
	State1FingerMove
	State2FingerPending
	State2FingerScroll
	StateEdgeScrollVertical
	StateEdgeScrollHorizontal
)

// TouchPoint tracks an individual active finger contact.
type TouchPoint struct {
	ID        int
	StartX    float64
	StartY    float64
	LastX     float64
	LastY     float64
	CurrentX  float64
	CurrentY  float64
	StartTime time.Time
	LastTime  time.Time
}

// Engine processes multi-touch inputs, manages gesture states,
// applies 1-Euro filtering and Sigmoid acceleration, and produces protocol packets.
type Engine struct {
	mu sync.Mutex

	cfg      *config.AppConfig
	state    State
	contacts map[int]*TouchPoint

	padWidth    float64
	padHeight   float64
	gutterWidth float64

	// Filtering & acceleration
	oneEuro *filter.OneEuro2D
	accel   *filter.SigmoidAccel
	momGen  *filter.MomentumGenerator

	// Sequence counter
	seq uint32

	// Haptic callback for detent ticks and click triggers
	OnHaptic func(kind int) // 1=click, 2=release, 3=tick, 4=right-click

	// Packet output sink
	OnPacket func(pkt *protocol.Packet)

	// Velocity tracking for inertia hand-off
	velX float64
	velY float64

	// Detent accumulator for edge scroll ticks
	edgeDetentAcc float64
}

// NewEngine creates an initialized gesture engine.
func NewEngine(cfg *config.AppConfig, width, height float64) *Engine {
	gutter := cfg.EdgeGutterWidth
	if gutter <= 0 {
		gutter = 28.0
	}

	e := &Engine{
		cfg:         cfg,
		state:       StateIdle,
		contacts:    make(map[int]*TouchPoint),
		padWidth:    width,
		padHeight:   height,
		gutterWidth: gutter,
		oneEuro:     filter.NewOneEuro2D(cfg.FilterMinCutoff, cfg.FilterBeta, cfg.FilterDerivFreq),
		accel:       filter.NewSigmoidAccel(cfg.Sensitivity),
		momGen:      filter.NewMomentumGenerator(),
	}

	return e
}

// SetDimensions updates pad bounds dynamically (e.g. on screen rotation or layout changes).
func (e *Engine) SetDimensions(w, h float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if w > 0 && h > 0 {
		e.padWidth = w
		e.padHeight = h
	}
}

// nextSeq generates an incrementing sequence number.
func (e *Engine) nextSeq() uint32 {
	e.seq++
	return e.seq
}

// emit sends a packet to the output handler.
func (e *Engine) emit(pkt *protocol.Packet) {
	if e.OnPacket != nil {
		e.OnPacket(pkt)
	}
}

// HandleTouchStart is called when fingers touch down.
func (e *Engine) HandleTouchStart(id int, x, y float64, t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 1. Immediately cancel active momentum scroll if new touch touches down
	if e.momGen.Cancel() {
		// Emit momentum end frame to desktop
		e.emit(&protocol.Packet{
			Type:            protocol.MsgScroll,
			SequenceNumber:  e.nextSeq(),
			TimestampMicros: uint32(t.UnixMicro()),
			MomentumPhase:   protocol.MomentumEnd,
			FingerCount:     uint8(len(e.contacts)),
		})
	}

	pt := &TouchPoint{
		ID:        id,
		StartX:    x,
		StartY:    y,
		LastX:     x,
		LastY:     y,
		CurrentX:  x,
		CurrentY:  y,
		StartTime: t,
		LastTime:  t,
	}
	e.contacts[id] = pt

	fingerCount := len(e.contacts)

	if fingerCount == 1 {
		// Self-adaptation for screen size / rotation: if touch arrives outside existing bounds, expand immediately
		if x > e.padWidth {
			e.padWidth = x + e.gutterWidth
		}
		if y > e.padHeight {
			e.padHeight = y + e.gutterWidth
		}

		e.oneEuro.Reset()
		e.accel.Reset()
		e.velX = 0
		e.velY = 0
		e.edgeDetentAcc = 0

		// Check if touched down in edge gutter
		inRightGutter := e.cfg.EdgeScrollEnabled && (x >= e.padWidth-e.gutterWidth)
		inBottomGutter := e.cfg.EdgeScrollEnabled && (y >= e.padHeight-e.gutterWidth)

		if inRightGutter && inBottomGutter {
			// In bottom-right corner overlap -> will disambiguate on first Move
			e.state = StateEdgeScrollVertical
		} else if inRightGutter {
			e.state = StateEdgeScrollVertical
		} else if inBottomGutter {
			e.state = StateEdgeScrollHorizontal
		} else {
			e.state = State1FingerPending
		}
	} else if fingerCount == 2 {
		// Hand transition to 2 fingers
		e.state = State2FingerPending
	}
}

// HandleTouchMove is called when active fingers move.
func (e *Engine) HandleTouchMove(id int, x, y float64, t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	pt, exists := e.contacts[id]
	if !exists {
		return
	}

	dt := t.Sub(pt.LastTime).Seconds()
	if dt <= 1e-5 {
		dt = 0.008
	}

	dx := x - pt.LastX
	dy := y - pt.LastY
	pt.LastX = pt.CurrentX
	pt.LastY = pt.CurrentY
	pt.CurrentX = x
	pt.CurrentY = y
	pt.LastTime = t

	// Update velocity estimate (exponential moving average)
	instantVx := dx / dt
	instantVy := dy / dt
	e.velX = 0.7*e.velX + 0.3*instantVx
	e.velY = 0.7*e.velY + 0.3*instantVy

	totalDist := math.Hypot(x-pt.StartX, y-pt.StartY)

	switch e.state {
	case State1FingerPending:
		// Check break-out threshold from tap to move
		if totalDist > 6.0 || t.Sub(pt.StartTime) > 180*time.Millisecond {
			e.state = State1FingerMove
			e.handleMove(x, y, dx, dy, dt, t)
		}

	case State1FingerMove:
		e.handleMove(x, y, dx, dy, dt, t)

	case State2FingerPending:
		if totalDist > 5.0 {
			e.state = State2FingerScroll
			// Emit Began frame
			e.emit(&protocol.Packet{
				Type:            protocol.MsgScroll,
				Flags:           protocol.FlagReliable,
				SequenceNumber:  e.nextSeq(),
				TimestampMicros: uint32(t.UnixMicro()),
				Phase:           protocol.PhaseBegan,
				FingerCount:     2,
			})
			e.handle2FingerScroll(dx, dy, t)
		}

	case State2FingerScroll:
		e.handle2FingerScroll(dx, dy, t)

	case StateEdgeScrollVertical:
		// Right edge vertical scroll
		// Hysteresis band: allow finger to drift up to 2 * gutterWidth from right edge
		if x < e.padWidth-2*e.gutterWidth {
			// Drifted too far inward -> abort edge scroll and fall back to normal move
			e.state = State1FingerMove
			return
		}
		e.handleEdgeVerticalScroll(dy, t)

	case StateEdgeScrollHorizontal:
		// Bottom edge horizontal scroll
		if y < e.padHeight-2*e.gutterWidth {
			e.state = State1FingerMove
			return
		}
		e.handleEdgeHorizontalScroll(dx, t)
	}
}

func (e *Engine) handleMove(x, y, dx, dy, dt float64, t time.Time) {
	// Filter positions with 1-Euro
	fx, fy := e.oneEuro.Filter(x, y, t)
	filteredDx := fx - (x - dx)
	filteredDy := fy - (y - dy)

	// Apply Sigmoid pointer acceleration
	outX, outY := e.accel.Accelerate(filteredDx, filteredDy, dt)
	if outX == 0 && outY == 0 {
		return
	}

	e.emit(&protocol.Packet{
		Type:            protocol.MsgPointerMove,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro()),
		DeltaX:          outX,
		DeltaY:          outY,
		FingerCount:     1,
	})
}

func (e *Engine) handle2FingerScroll(dx, dy float64, t time.Time) {
	// Natural scrolling modifier
	multiplier := float32(e.cfg.ScrollSpeed)
	if !e.cfg.NaturalScroll {
		multiplier = -multiplier
	}

	// 2-finger scroll deltas (points)
	scrollDx := float32(dx) * multiplier * 2.5
	scrollDy := float32(dy) * multiplier * 2.5

	e.emit(&protocol.Packet{
		Type:            protocol.MsgScroll,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro()),
		DeltaX:          scrollDx,
		DeltaY:          scrollDy,
		Phase:           protocol.PhaseChanged,
		FingerCount:     2,
	})
}

func (e *Engine) handleEdgeVerticalScroll(dy float64, t time.Time) {
	multiplier := float32(e.cfg.ScrollSpeed)
	if !e.cfg.NaturalScroll {
		multiplier = -multiplier
	}
	scrollDy := float32(dy) * multiplier * 2.5

	e.emit(&protocol.Packet{
		Type:            protocol.MsgScroll,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro()),
		DeltaX:          0,
		DeltaY:          scrollDy,
		Phase:           protocol.PhaseChanged,
		FingerCount:     1,
	})

	// Detent tick calculation
	e.edgeDetentAcc += math.Abs(dy)
	if e.edgeDetentAcc >= 18.0 {
		e.edgeDetentAcc -= 18.0
		if e.OnHaptic != nil {
			e.OnHaptic(3) // Detent tick
		}
	}
}

func (e *Engine) handleEdgeHorizontalScroll(dx float64, t time.Time) {
	multiplier := float32(e.cfg.ScrollSpeed)
	if !e.cfg.NaturalScroll {
		multiplier = -multiplier
	}
	scrollDx := float32(dx) * multiplier * 2.5

	e.emit(&protocol.Packet{
		Type:            protocol.MsgScroll,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro()),
		DeltaX:          scrollDx,
		DeltaY:          0,
		Phase:           protocol.PhaseChanged,
		FingerCount:     1,
	})

	e.edgeDetentAcc += math.Abs(dx)
	if e.edgeDetentAcc >= 18.0 {
		e.edgeDetentAcc -= 18.0
		if e.OnHaptic != nil {
			e.OnHaptic(3)
		}
	}
}

// HandleTouchEnd is called when a finger lifts from the surface.
func (e *Engine) HandleTouchEnd(id int, x, y float64, t time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()

	pt, exists := e.contacts[id]
	if !exists {
		return
	}
	delete(e.contacts, id)

	totalDist := math.Hypot(x-pt.StartX, y-pt.StartY)
	duration := t.Sub(pt.StartTime)

	switch e.state {
	case State1FingerPending:
		// Single finger tap -> Left Click
		if e.cfg.TapToClick && totalDist < 8.0 && duration < 250*time.Millisecond {
			e.emitClick(protocol.ButtonLeft, 1, t)
		}
		e.state = StateIdle

	case State1FingerMove:
		if len(e.contacts) == 0 {
			e.state = StateIdle
		}

	case State2FingerPending:
		// Two finger tap -> Right Click
		if totalDist < 10.0 && duration < 250*time.Millisecond {
			e.emitClick(protocol.ButtonRight, 1, t)
		}
		if len(e.contacts) == 0 {
			e.state = StateIdle
		}

	case State2FingerScroll:
		if len(e.contacts) == 0 {
			e.state = StateIdle
			// Emit Scroll Ended
			e.emit(&protocol.Packet{
				Type:            protocol.MsgScroll,
				Flags:           protocol.FlagReliable,
				SequenceNumber:  e.nextSeq(),
				TimestampMicros: uint32(t.UnixMicro()),
				Phase:           protocol.PhaseEnded,
				FingerCount:     2,
			})

			// Start inertia glide if enabled and flick velocity is sufficient
			if e.cfg.InertiaEnabled {
				vx := e.velX
				vy := e.velY
				if !e.cfg.NaturalScroll {
					vx = -vx
					vy = -vy
				}
				e.startInertia(vx*2.5, vy*2.5)
			}
		}

	case StateEdgeScrollVertical, StateEdgeScrollHorizontal:
		if len(e.contacts) == 0 {
			e.state = StateIdle
			// If barely moved and lifted quickly, treat as a normal tap!
			if totalDist < 5.0 && duration < 200*time.Millisecond {
				if e.cfg.TapToClick {
					e.emitClick(protocol.ButtonLeft, 1, t)
				}
			} else {
				e.emit(&protocol.Packet{
					Type:            protocol.MsgScroll,
					Flags:           protocol.FlagReliable,
					SequenceNumber:  e.nextSeq(),
					TimestampMicros: uint32(t.UnixMicro()),
					Phase:           protocol.PhaseEnded,
					FingerCount:     1,
				})
				if e.cfg.InertiaEnabled {
					vx := e.velX
					vy := e.velY
					if !e.cfg.NaturalScroll {
						vx = -vx
						vy = -vy
					}
					e.startInertia(vx*2.5, vy*2.5)
				}
			}
		}
	}
}

// startInertia kicks off the momentum generator.
func (e *Engine) startInertia(vx, vy float64) {
	e.momGen.Start(vx, vy, func(dx, dy float32, phase protocol.MomentumPhase) {
		e.mu.Lock()
		seq := e.nextSeq()
		e.mu.Unlock()

		flags := protocol.FlagSynthetic
		if phase == protocol.MomentumEnd || phase == protocol.MomentumBegin {
			flags |= protocol.FlagReliable
		}

		e.emit(&protocol.Packet{
			Type:            protocol.MsgScroll,
			Flags:           flags,
			SequenceNumber:  seq,
			TimestampMicros: uint32(time.Now().UnixMicro()),
			DeltaX:          dx,
			DeltaY:          dy,
			MomentumPhase:   phase,
			FingerCount:     0,
		})
	})
}

// emitClick generates reliable button down and button up packets.
func (e *Engine) emitClick(button uint8, clickCount int, t time.Time) {
	if e.OnHaptic != nil {
		if button == protocol.ButtonRight {
			e.OnHaptic(4) // Right click haptic
		} else {
			e.OnHaptic(1) // Left click haptic
		}
	}

	// Down
	e.emit(&protocol.Packet{
		Type:            protocol.MsgMouseButton,
		Flags:           protocol.FlagReliable,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro()),
		Phase:           protocol.Phase(protocol.ButtonDown),
		FingerCount:     uint8(clickCount),
		AuxValue:        int8(button),
	})

	// Up
	e.emit(&protocol.Packet{
		Type:            protocol.MsgMouseButton,
		Flags:           protocol.FlagReliable,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(t.UnixMicro() + 15000), // +15ms click duration
		Phase:           protocol.Phase(protocol.ButtonUp),
		FingerCount:     uint8(clickCount),
		AuxValue:        int8(button),
	})
}

// HandleTouchCancel resets all fingers and emits a ResetState event.
func (e *Engine) HandleTouchCancel() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.contacts = make(map[int]*TouchPoint)
	e.state = StateIdle
	e.momGen.Cancel()

	e.emit(&protocol.Packet{
		Type:            protocol.MsgResetState,
		Flags:           protocol.FlagReliable,
		SequenceNumber:  e.nextSeq(),
		TimestampMicros: uint32(time.Now().UnixMicro()),
	})
}
