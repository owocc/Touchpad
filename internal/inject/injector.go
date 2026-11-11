package inject

import (
	"touchpad/pkg/protocol"
)

// Injector abstracts OS-level cursor, mouse button, and scroll wheel event injection.
type Injector interface {
	// MouseMove moves the cursor by relative points (dx, dy).
	MouseMove(dx, dy float32) error

	// MouseButton presses or releases a mouse button.
	// button: protocol.ButtonLeft, protocol.ButtonRight, protocol.ButtonMiddle
	// down: true for press, false for release
	MouseButton(button uint8, down bool) error

	// MouseClick performs a complete click (down then up).
	MouseClick(button uint8, count int) error

	// Scroll injects high-resolution continuous scroll deltas.
	Scroll(dx, dy float32, phase protocol.Phase, momentumPhase protocol.MomentumPhase) error

	// ResetState releases all held buttons and halts scrolling.
	// Used on disconnect, timeout, or touch cancel.
	ResetState() error

	// HasPermission checks whether OS input injection permissions are granted.
	HasPermission() bool

	// RequestPermission prompts user to grant required system permissions.
	RequestPermission()

	// Close releases any allocated OS resources.
	Close() error
}

// New creates an OS-specific Injector.
func New() (Injector, error) {
	return newPlatformInjector()
}
