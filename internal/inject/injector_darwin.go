//go:build darwin

package inject

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"github.com/ebitengine/purego"
	"touchpad/pkg/protocol"
)

type CGPoint struct {
	X float64
	Y float64
}

const (
	kCGHIDEventTap     = 0
	kCGSessionEventTap = 1

	kCGEventNull             = 0
	kCGEventLeftMouseDown    = 1
	kCGEventLeftMouseUp      = 2
	kCGEventRightMouseDown   = 3
	kCGEventRightMouseUp     = 4
	kCGEventMouseMoved       = 5
	kCGEventLeftMouseDragged = 6
	kCGEventRightMouseDragged = 7
	kCGEventScrollWheel      = 22
	kCGEventOtherMouseDown   = 25
	kCGEventOtherMouseUp     = 26
	kCGEventOtherMouseDragged = 27

	kCGMouseButtonLeft   = 0
	kCGMouseButtonRight  = 1
	kCGMouseButtonCenter = 2

	kCGScrollEventUnitPixel = 0
	kCGScrollEventUnitLine  = 1

	kCGMouseEventClickState             = 1
	kCGScrollWheelEventDeltaAxis1       = 11
	kCGScrollWheelEventDeltaAxis2       = 12
	kCGScrollWheelEventIsContinuous     = 88
	kCGScrollWheelEventPointDeltaAxis1  = 96
	kCGScrollWheelEventPointDeltaAxis2  = 97
	kCGScrollWheelEventScrollPhase      = 99
	kCGScrollWheelEventMomentumPhase    = 123
)

type cgBindings struct {
	eventSourceCreate            func(stateID int32) uintptr
	eventCreate                  func(source uintptr) uintptr
	eventCreateMouseEvent        func(source uintptr, mouseType uint32, pos CGPoint, button uint32) uintptr
	eventCreateScrollWheelEvent2 func(source uintptr, units uint32, wheelCount uint32, wheel1, wheel2, wheel3 int32) uintptr
	eventGetLocation             func(event uintptr) CGPoint
	eventSetLocation             func(event uintptr, pos CGPoint)
	eventSetType                 func(event uintptr, evtType uint32)
	eventSetIntegerValueField    func(event uintptr, field uint32, value int64)
	eventSetDoubleValueField     func(event uintptr, field uint32, value float64)
	eventPost                    func(tap uint32, event uintptr)
	cfRelease                    func(cf uintptr)
	axIsProcessTrusted           func() bool
}

type darwinInjector struct {
	mu     sync.Mutex
	cg     cgBindings
	source uintptr

	// Track held buttons so ResetState can safely release them
	leftDown   bool
	rightDown  bool
	middleDown bool
}

func newPlatformInjector() (Injector, error) {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("open CoreGraphics: %w", err)
	}

	var b cgBindings
	purego.RegisterLibFunc(&b.eventSourceCreate, lib, "CGEventSourceCreate")
	purego.RegisterLibFunc(&b.eventCreate, lib, "CGEventCreate")
	purego.RegisterLibFunc(&b.eventCreateMouseEvent, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&b.eventCreateScrollWheelEvent2, lib, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&b.eventGetLocation, lib, "CGEventGetLocation")
	purego.RegisterLibFunc(&b.eventSetLocation, lib, "CGEventSetLocation")
	purego.RegisterLibFunc(&b.eventSetType, lib, "CGEventSetType")
	purego.RegisterLibFunc(&b.eventSetIntegerValueField, lib, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&b.eventSetDoubleValueField, lib, "CGEventSetDoubleValueField")
	purego.RegisterLibFunc(&b.eventPost, lib, "CGEventPost")
	purego.RegisterLibFunc(&b.cfRelease, lib, "CFRelease")

	// Try loading Accessibility check from ApplicationServices
	if appServices, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", purego.RTLD_GLOBAL); err == nil {
		purego.RegisterLibFunc(&b.axIsProcessTrusted, appServices, "AXIsProcessTrusted")
	}

	src := b.eventSourceCreate(1) // kCGEventSourceStateCombinedSessionState

	return &darwinInjector{
		cg:     b,
		source: src,
	}, nil
}

func (d *darwinInjector) currentPos() CGPoint {
	ev := d.cg.eventCreate(0)
	if ev == 0 {
		return CGPoint{}
	}
	defer d.cg.cfRelease(ev)
	return d.cg.eventGetLocation(ev)
}

func (d *darwinInjector) MouseMove(dx, dy float32) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pos := d.currentPos()
	newPos := CGPoint{
		X: pos.X + float64(dx),
		Y: pos.Y + float64(dy),
	}

	var evtType uint32 = kCGEventMouseMoved
	var btn uint32 = kCGMouseButtonLeft

	if d.leftDown {
		evtType = kCGEventLeftMouseDragged
		btn = kCGMouseButtonLeft
	} else if d.rightDown {
		evtType = kCGEventRightMouseDragged
		btn = kCGMouseButtonRight
	} else if d.middleDown {
		evtType = kCGEventOtherMouseDragged
		btn = kCGMouseButtonCenter
	}

	ev := d.cg.eventCreateMouseEvent(d.source, evtType, newPos, btn)
	if ev == 0 {
		return errors.New("failed to create mouse move event")
	}
	defer d.cg.cfRelease(ev)

	d.cg.eventPost(kCGHIDEventTap, ev)
	return nil
}

func (d *darwinInjector) MouseButton(button uint8, down bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pos := d.currentPos()
	var evtType uint32
	var btn uint32

	switch button {
	case protocol.ButtonLeft:
		btn = kCGMouseButtonLeft
		if down {
			evtType = kCGEventLeftMouseDown
			d.leftDown = true
		} else {
			evtType = kCGEventLeftMouseUp
			d.leftDown = false
		}
	case protocol.ButtonRight:
		btn = kCGMouseButtonRight
		if down {
			evtType = kCGEventRightMouseDown
			d.rightDown = true
		} else {
			evtType = kCGEventRightMouseUp
			d.rightDown = false
		}
	case protocol.ButtonMiddle:
		btn = kCGMouseButtonCenter
		if down {
			evtType = kCGEventOtherMouseDown
			d.middleDown = true
		} else {
			evtType = kCGEventOtherMouseUp
			d.middleDown = false
		}
	default:
		return fmt.Errorf("unsupported button: %d", button)
	}

	ev := d.cg.eventCreateMouseEvent(d.source, evtType, pos, btn)
	if ev == 0 {
		return errors.New("failed to create mouse button event")
	}
	defer d.cg.cfRelease(ev)

	d.cg.eventSetIntegerValueField(ev, kCGMouseEventClickState, 1)
	d.cg.eventPost(kCGHIDEventTap, ev)
	return nil
}

func (d *darwinInjector) MouseClick(button uint8, count int) error {
	if count <= 0 {
		count = 1
	}
	if err := d.MouseButton(button, true); err != nil {
		return err
	}
	return d.MouseButton(button, false)
}

func (d *darwinInjector) Scroll(dx, dy float32, phase protocol.Phase, momentumPhase protocol.MomentumPhase) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// In CoreGraphics:
	// wheel1 = vertical line delta
	// wheel2 = horizontal line delta
	// Units: kCGScrollEventUnitPixel for continuous trackpad scrolling
	ev := d.cg.eventCreateScrollWheelEvent2(d.source, kCGScrollEventUnitPixel, 2, 0, 0, 0)
	if ev == 0 {
		return errors.New("failed to create scroll wheel event")
	}
	defer d.cg.cfRelease(ev)

	// Set continuous trackpad flag
	d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventIsContinuous, 1)

	// Invert Y for macOS standard coordinate system (positive dy = scroll up)
	d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventPointDeltaAxis1, int64(dy))
	d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventPointDeltaAxis2, int64(dx))

	if phase != protocol.PhaseNone {
		d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventScrollPhase, int64(phase))
	}
	if momentumPhase != protocol.MomentumNone {
		d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventMomentumPhase, int64(momentumPhase))
	}

	d.cg.eventPost(kCGHIDEventTap, ev)
	return nil
}

func (d *darwinInjector) ResetState() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	pos := d.currentPos()

	if d.leftDown {
		ev := d.cg.eventCreateMouseEvent(d.source, kCGEventLeftMouseUp, pos, kCGMouseButtonLeft)
		if ev != 0 {
			d.cg.eventPost(kCGHIDEventTap, ev)
			d.cg.cfRelease(ev)
		}
		d.leftDown = false
	}
	if d.rightDown {
		ev := d.cg.eventCreateMouseEvent(d.source, kCGEventRightMouseUp, pos, kCGMouseButtonRight)
		if ev != 0 {
			d.cg.eventPost(kCGHIDEventTap, ev)
			d.cg.cfRelease(ev)
		}
		d.rightDown = false
	}
	if d.middleDown {
		ev := d.cg.eventCreateMouseEvent(d.source, kCGEventOtherMouseUp, pos, kCGMouseButtonCenter)
		if ev != 0 {
			d.cg.eventPost(kCGHIDEventTap, ev)
			d.cg.cfRelease(ev)
		}
		d.middleDown = false
	}

	// Send an empty scroll event with MomentumPhaseEnd to stop any ongoing inertia
	ev := d.cg.eventCreateScrollWheelEvent2(d.source, kCGScrollEventUnitPixel, 2, 0, 0, 0)
	if ev != 0 {
		d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventIsContinuous, 1)
		d.cg.eventSetIntegerValueField(ev, kCGScrollWheelEventMomentumPhase, 3 /* End */)
		d.cg.eventPost(kCGHIDEventTap, ev)
		d.cg.cfRelease(ev)
	}

	return nil
}

func (d *darwinInjector) HasPermission() bool {
	if d.cg.axIsProcessTrusted != nil {
		return d.cg.axIsProcessTrusted()
	}
	return true
}

func (d *darwinInjector) RequestPermission() {
	// Open macOS Accessibility settings directly
	exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility").Start()
}

func (d *darwinInjector) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.source != 0 {
		d.cg.cfRelease(d.source)
		d.source = 0
	}
	return nil
}
