//go:build windows

package inject

import (
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"touchpad/pkg/protocol"
)

const (
	inputMouse = 0

	mouseEventFMove       = 0x0001
	mouseEventFLeftDown   = 0x0002
	mouseEventFLeftUp     = 0x0004
	mouseEventFRightDown  = 0x0008
	mouseEventFRightUp    = 0x0010
	mouseEventFMiddleDown = 0x0020
	mouseEventFMiddleUp   = 0x0040
	mouseEventFWheel      = 0x0800
	mouseEventFHWHEEL     = 0x1000
)

type mouseInput struct {
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// inputStruct represents the Windows INPUT union struct.
// On 64-bit Windows (x64 and arm64), sizeof(INPUT) is exactly 40 bytes:
// 4 bytes Type + 4 bytes padding + 32 bytes mouseInput union.
type inputStruct struct {
	inputType uint32
	_         uint32 // 64-bit alignment padding
	mi        mouseInput
	_         [8]byte // padding to ensure exactly 40 bytes on 64-bit
}

type windowsInjector struct {
	mu        sync.Mutex
	sendInput *syscall.LazyProc

	leftDown   bool
	rightDown  bool
	middleDown bool

	// Fractional scroll accumulators
	remWheelX float32
	remWheelY float32
}

func newPlatformInjector() (Injector, error) {
	user32 := syscall.NewLazyDLL("user32.dll")
	sendInput := user32.NewProc("SendInput")
	if err := sendInput.Find(); err != nil {
		return nil, fmt.Errorf("find SendInput: %w", err)
	}

	return &windowsInjector{
		sendInput: sendInput,
	}, nil
}

func (w *windowsInjector) send(inputs []inputStruct) error {
	if len(inputs) == 0 {
		return nil
	}
	cbSize := unsafe.Sizeof(inputs[0])
	ret, _, err := w.sendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		cbSize,
	)
	if ret == 0 {
		return fmt.Errorf("SendInput failed: %v", err)
	}
	return nil
}

func (w *windowsInjector) MouseMove(dx, dy float32) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	inp := inputStruct{
		inputType: inputMouse,
		mi: mouseInput{
			dx:      int32(dx),
			dy:      int32(dy),
			dwFlags: mouseEventFMove,
		},
	}
	return w.send([]inputStruct{inp})
}

func (w *windowsInjector) MouseButton(button uint8, down bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var flags uint32
	switch button {
	case protocol.ButtonLeft:
		if down {
			flags = mouseEventFLeftDown
			w.leftDown = true
		} else {
			flags = mouseEventFLeftUp
			w.leftDown = false
		}
	case protocol.ButtonRight:
		if down {
			flags = mouseEventFRightDown
			w.rightDown = true
		} else {
			flags = mouseEventFRightUp
			w.rightDown = false
		}
	case protocol.ButtonMiddle:
		if down {
			flags = mouseEventFMiddleDown
			w.middleDown = true
		} else {
			flags = mouseEventFMiddleUp
			w.middleDown = false
		}
	default:
		return fmt.Errorf("unsupported button: %d", button)
	}

	inp := inputStruct{
		inputType: inputMouse,
		mi: mouseInput{
			dwFlags: flags,
		},
	}
	return w.send([]inputStruct{inp})
}

func (w *windowsInjector) MouseClick(button uint8, count int) error {
	if count <= 0 {
		count = 1
	}
	if err := w.MouseButton(button, true); err != nil {
		return err
	}
	return w.MouseButton(button, false)
}

func (w *windowsInjector) Scroll(dx, dy float32, phase protocol.Phase, momentumPhase protocol.MomentumPhase) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Scale points to high-res wheel units (sub-notch)
	rawY := dy*4.0 + w.remWheelY
	rawX := dx*4.0 + w.remWheelX

	unitsY := int32(rawY)
	unitsX := int32(rawX)

	w.remWheelY = rawY - float32(unitsY)
	w.remWheelX = rawX - float32(unitsX)

	var inputs []inputStruct

	if unitsY != 0 {
		inputs = append(inputs, inputStruct{
			inputType: inputMouse,
			mi: mouseInput{
				mouseData: uint32(unitsY),
				dwFlags:   mouseEventFWheel,
			},
		})
	}

	if unitsX != 0 {
		inputs = append(inputs, inputStruct{
			inputType: inputMouse,
			mi: mouseInput{
				mouseData: uint32(unitsX),
				dwFlags:   mouseEventFHWHEEL,
			},
		})
	}

	return w.send(inputs)
}

func (w *windowsInjector) ResetState() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	var inputs []inputStruct
	if w.leftDown {
		inputs = append(inputs, inputStruct{
			inputType: inputMouse,
			mi:        mouseInput{dwFlags: mouseEventFLeftUp},
		})
		w.leftDown = false
	}
	if w.rightDown {
		inputs = append(inputs, inputStruct{
			inputType: inputMouse,
			mi:        mouseInput{dwFlags: mouseEventFRightUp},
		})
		w.rightDown = false
	}
	if w.middleDown {
		inputs = append(inputs, inputStruct{
			inputType: inputMouse,
			mi:        mouseInput{dwFlags: mouseEventFMiddleUp},
		})
		w.middleDown = false
	}

	w.remWheelX = 0
	w.remWheelY = 0

	return w.send(inputs)
}

func (w *windowsInjector) HasPermission() bool {
	return true // Windows SendInput does not require accessibility permissions
}

func (w *windowsInjector) RequestPermission() {}

func (w *windowsInjector) Close() error {
	return w.ResetState()
}
