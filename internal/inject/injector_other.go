//go:build !darwin && !windows

package inject

import (
	"log"

	"touchpad/pkg/protocol"
)

type dummyInjector struct{}

func newPlatformInjector() (Injector, error) {
	return &dummyInjector{}, nil
}

func (d *dummyInjector) MouseMove(dx, dy float32) error {
	log.Printf("[StubInjector] MouseMove dx=%.2f dy=%.2f", dx, dy)
	return nil
}

func (d *dummyInjector) MouseButton(button uint8, down bool) error {
	log.Printf("[StubInjector] MouseButton btn=%d down=%v", button, down)
	return nil
}

func (d *dummyInjector) MouseClick(button uint8, count int) error {
	log.Printf("[StubInjector] MouseClick btn=%d count=%d", button, count)
	return nil
}

func (d *dummyInjector) Scroll(dx, dy float32, phase protocol.Phase, momentumPhase protocol.MomentumPhase) error {
	log.Printf("[StubInjector] Scroll dx=%.2f dy=%.2f phase=%v mom=%v", dx, dy, phase, momentumPhase)
	return nil
}

func (d *dummyInjector) ResetState() error {
	log.Printf("[StubInjector] ResetState")
	return nil
}

func (d *dummyInjector) HasPermission() bool {
	return true
}

func (d *dummyInjector) RequestPermission() {}

func (d *dummyInjector) Close() error {
	return nil
}
