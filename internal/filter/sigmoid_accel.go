package filter

import (
	"math"
)

// SigmoidAccel implements adaptive sigmoid pointer acceleration
// with sub-pixel remainder preservation and speed jitter dampening.
type SigmoidAccel struct {
	userSensitivity float64
	gMin            float64
	gMax            float64
	vMid            float64
	k               float64

	smoothedSpeed float64
	hasSpeed      bool

	remX float64
	remY float64
}

// NewSigmoidAccel creates a new accelerator with default tuned constants.
func NewSigmoidAccel(userSensitivity float64) *SigmoidAccel {
	if userSensitivity <= 0 {
		userSensitivity = 1.0
	}
	return &SigmoidAccel{
		userSensitivity: userSensitivity,
		gMin:            0.45,
		gMax:            3.60,
		vMid:            280.0,
		k:               0.011,
	}
}

// SetSensitivity updates the user multiplier.
func (s *SigmoidAccel) SetSensitivity(sens float64) {
	if sens > 0 {
		s.userSensitivity = sens
	}
}

// Reset clears velocity memory and sub-pixel accumulators.
func (s *SigmoidAccel) Reset() {
	s.smoothedSpeed = 0
	s.hasSpeed = false
	s.remX = 0
	s.remY = 0
}

// Accelerate computes accelerated deltas from raw physical displacement (dx, dy)
// occurring over dt seconds, applying velocity smoothing and sub-pixel accumulation.
func (s *SigmoidAccel) Accelerate(dx, dy, dt float64) (float32, float32) {
	if dt <= 1e-5 {
		dt = 0.008 // ~120 Hz default assumption
	} else if dt > 0.1 {
		dt = 0.1 // clamp long pauses to prevent massive speed jumps
	}

	dist := math.Hypot(dx, dy)
	instantSpeed := dist / dt // pt/second

	// Dampen instantaneous frame spikes using EMA
	if !s.hasSpeed {
		s.smoothedSpeed = instantSpeed
		s.hasSpeed = true
	} else {
		// 65% history, 35% new sample provides smooth acceleration curve
		s.smoothedSpeed = 0.65*s.smoothedSpeed + 0.35*instantSpeed
	}

	speed := s.smoothedSpeed

	// Sigmoid gain G(v) = S_user * (G_min + (G_max - G_min) / (1 + exp(-k * (v - v_mid))))
	sigmoidPart := (s.gMax - s.gMin) / (1.0 + math.Exp(-s.k*(speed-s.vMid)))
	gain := s.userSensitivity * (s.gMin + sigmoidPart)

	rawX := dx*gain + s.remX
	rawY := dy*gain + s.remY

	// Preserve fractional remainder to avoid cursor stall during micro-movements
	outX := math.Trunc(rawX)
	outY := math.Trunc(rawY)

	s.remX = rawX - outX
	s.remY = rawY - outY

	return float32(outX), float32(outY)
}
