package filter

import (
	"math"
	"time"
)

// LowPassFilter implements a first-order exponential smoothing filter.
type LowPassFilter struct {
	hasLast bool
	lastVal float64
}

func (l *LowPassFilter) Filter(val, alpha float64) float64 {
	if !l.hasLast {
		l.hasLast = true
		l.lastVal = val
		return val
	}
	res := alpha*val + (1.0-alpha)*l.lastVal
	l.lastVal = res
	return res
}

func (l *LowPassFilter) Last() float64 {
	return l.lastVal
}

func (l *LowPassFilter) Reset() {
	l.hasLast = false
	l.lastVal = 0
}

func alpha(rate, cutoff float64) float64 {
	tau := 1.0 / (2.0 * math.Pi * cutoff)
	te := 1.0 / rate
	return 1.0 / (1.0 + tau/te)
}

// OneEuro1D filters a single 1D scalar using the 1-Euro algorithm.
type OneEuro1D struct {
	minCutoff float64 // fc_min
	beta      float64 // beta
	dCutoff   float64 // f_d
	xFilter   LowPassFilter
	dxFilter  LowPassFilter
	lastTime  time.Time
}

// NewOneEuro1D creates a 1D filter instance.
func NewOneEuro1D(minCutoff, beta, dCutoff float64) *OneEuro1D {
	return &OneEuro1D{
		minCutoff: minCutoff,
		beta:      beta,
		dCutoff:   dCutoff,
	}
}

// Filter filters sample x at timestamp t.
func (o *OneEuro1D) Filter(x float64, t time.Time) float64 {
	if o.lastTime.IsZero() {
		o.lastTime = t
		return o.xFilter.Filter(x, 1.0)
	}

	dt := t.Sub(o.lastTime).Seconds()
	o.lastTime = t

	// Guard against clock jumps or duplicate timestamps
	if dt <= 1e-5 {
		dt = 1e-3
	} else if dt > 0.5 {
		// Long gap, treat as new start
		o.Reset()
		o.lastTime = t
		return o.xFilter.Filter(x, 1.0)
	}

	rate := 1.0 / dt

	// Estimate derivative (velocity)
	dx := (x - o.xFilter.Last()) * rate
	edx := o.dxFilter.Filter(dx, alpha(rate, o.dCutoff))

	// Dynamic cutoff frequency
	cutoff := o.minCutoff + o.beta*math.Abs(edx)

	// Filtered value
	filtered := o.xFilter.Filter(x, alpha(rate, cutoff))

	// Deadzone jitter suppression for sub-pixel noise when stationary
	if math.Abs(filtered-o.xFilter.Last()) < 0.25 && math.Abs(edx) < 5.0 {
		return o.xFilter.Last()
	}

	return filtered
}

// Reset clears filter memory.
func (o *OneEuro1D) Reset() {
	o.xFilter.Reset()
	o.dxFilter.Reset()
	o.lastTime = time.Time{}
}

// OneEuro2D filters 2D coordinates (x, y).
type OneEuro2D struct {
	fx *OneEuro1D
	fy *OneEuro1D
}

// NewOneEuro2D creates a 2D filter with given parameters.
func NewOneEuro2D(minCutoff, beta, dCutoff float64) *OneEuro2D {
	return &OneEuro2D{
		fx: NewOneEuro1D(minCutoff, beta, dCutoff),
		fy: NewOneEuro1D(minCutoff, beta, dCutoff),
	}
}

// Filter processes (x, y) at timestamp t and returns smoothed coordinates.
func (f *OneEuro2D) Filter(x, y float64, t time.Time) (float64, float64) {
	return f.fx.Filter(x, t), f.fy.Filter(y, t)
}

// Reset clears both axes.
func (f *OneEuro2D) Reset() {
	f.fx.Reset()
	f.fy.Reset()
}
