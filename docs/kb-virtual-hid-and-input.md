# Knowledge Base 2: Desktop Input Injection & Virtual Peripheral Emulation (macOS, Windows & Bluetooth HID)

This document catalogs the low-level OS APIs, C/Win32 struct layouts, symbol signatures, and Bluetooth HID Report Descriptors required to make **macOS** and **Windows** treat the phone as a real trackpad peripheral.

---

## 1. macOS Native Trackpad & Gesture Injection (`purego` + CoreGraphics)

On macOS (`GOOS=darwin`, `CGO_ENABLED=0`), we load `/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics` via `github.com/ebitengine/purego`.

### 1.1 Dynamic Symbol Bindings (`CoreGraphics`)
```go
package input

import "github.com/ebitengine/purego"

type (
	CGEventRef       uintptr
	CGEventSourceRef uintptr
	CGEventType      uint32
	CGEventField     uint32
	CGMouseButton    uint32
	CGScrollEventUnit uint32
)

type CGPoint struct {
	X float64
	Y float64
}

var cg struct {
	EventSourceCreate               func(stateID int32) CGEventSourceRef
	EventCreate                     func(source CGEventSourceRef) CGEventRef
	EventCreateMouseEvent           func(source CGEventSourceRef, mouseType CGEventType, pos CGPoint, button CGMouseButton) CGEventRef
	EventCreateScrollWheelEvent2    func(source CGEventSourceRef, units CGScrollEventUnit, wheelCount uint32, wheel1, wheel2, wheel3 int32) CGEventRef
	EventGetLocation                func(event CGEventRef) CGPoint
	EventSetLocation                func(event CGEventRef, pos CGPoint)
	EventSetType                    func(event CGEventRef, evtType CGEventType)
	EventSetIntegerValueField       func(event CGEventRef, field CGEventField, value int64)
	EventSetDoubleValueField        func(event CGEventRef, field CGEventField, value float64)
	EventSetFlags                   func(event CGEventRef, flags uint64)
	EventPost                       func(tap uint32, event CGEventRef)
	CFRelease                       func(cf uintptr)
	// Private CoreGraphics Symbolic HotKey APIs for native Spaces / Mission Control / App Exposé
	CGSGetSymbolicHotKeyValue       func(hotKey int32, outKeyEquivalent *uint16, outVirtualKeyCode *uint16, outModifiers *uint32) int32
	CGSIsSymbolicHotKeyEnabled      func(hotKey int32) bool
}
```

### 1.2 Exact `CGEventField` Constants for Magic Trackpad Emulation
Why does KDE Connect scrolling feel "steppy" on macOS while an Apple Magic Trackpad feels liquid and triggers rubber-band bounce in Safari/Xcode? Because AppKit checks continuous phase fields on every `kCGEventScrollWheel` (`22`) event:

| Constant Name | Value (`CGEventField`) | Required Values & Semantics |
| :--- | :--- | :--- |
| `kCGMouseEventClickState` | `1` | `1` (single), `2` (double click), `3` (triple click). Must be set on both `MouseDown` and `MouseUp`! |
| `kCGScrollWheelEventDeltaAxis1` | `11` | Vertical line delta (`int64`). |
| `kCGScrollWheelEventDeltaAxis2` | `12` | Horizontal line delta (`int64`). |
| `kCGScrollWheelEventPointDeltaAxis1` | `96` | Vertical **pixel** delta (`int64` / `double`, positive = scroll up / content down). |
| `kCGScrollWheelEventPointDeltaAxis2` | `97` | Horizontal **pixel** delta (`int64` / `double`). |
| `kCGScrollWheelEventIsContinuous` | `88` | **MUST be set to `1`** so macOS treats it as a Multi-Touch Trackpad instead of a notched mouse wheel! |
| `kCGScrollWheelEventScrollPhase` | `99` | Trackpad finger phase (`NSEventPhase`):<br>- `1` (`kCGScrollPhaseBegan`): Finger(s) started scrolling<br>- `2` (`kCGScrollPhaseChanged`): Finger(s) moving<br>- `4` (`kCGScrollPhaseEnded`): Finger(s) lifted (if no momentum, or right before momentum starts)<br>- `8` (`kCGScrollPhaseCancelled`)<br>- `128` (`kCGScrollPhaseMayBegin`): Two fingers touched down before moving |
| `kCGScrollWheelEventMomentumPhase` | `123` | Inertial glide phase after finger lift:<br>- `0` (`kCGMomentumScrollPhaseNone`): Fingers still on pad<br>- `1` (`kCGMomentumScrollPhaseBegin`): First inertial frame after lift<br>- `2` (`kCGMomentumScrollPhaseContinue`): Decaying inertial frames<br>- `3` (`kCGMomentumScrollPhaseEnd`): Inertia reached zero OR interrupted by touch-down |

#### Scroll Event State Machine Sequence (Mandatory for macOS Rubber-Band Bounce):
1. **Two fingers touch down (or 1 finger enters Edge Scroll zone)**:
   - Emit 1 event: `ScrollPhase = 1 (Began)`, `MomentumPhase = 0`, `PointDelta = (0, 0)`.
2. **Fingers move $(dx, dy)$**:
   - Emit events at 120 Hz: `ScrollPhase = 2 (Changed)`, `MomentumPhase = 0`, `PointDeltaAxis1 = dy`, `PointDeltaAxis2 = dx`.
3. **Fingers lift with velocity $|v_0| > v_{\text{threshold}}$**:
   - First emit 1 finger-up transition event: `ScrollPhase = 4 (Ended)`, `MomentumPhase = 0`, `PointDelta = (0, 0)`.
   - Next frame: `ScrollPhase = 0`, `MomentumPhase = 1 (Begin)`, `PointDelta = (vx, vy)`.
   - Subsequent decay frames: `ScrollPhase = 0`, `MomentumPhase = 2 (Continue)`, `PointDelta = (vx_t, vy_t)`.
   - Final frame (or immediately when user touches screen again): `ScrollPhase = 0`, `MomentumPhase = 3 (End)`, `PointDelta = (0, 0)`.

### 1.3 Native macOS Pinch-to-Zoom, Rotate & 3/4-Finger System Gestures
1. **Pinch-to-Zoom (`NSEventTypeMagnify`) & Rotate (`NSEventTypeRotate`)**:
   - `CGEvent` type `29` (`kCGEventGesture`).
   - Relevant undocumented `CGEventField` values used by WindowServer/AppKit:
     - Field `55` (`kCGEventGestureHIDType`):
       - `6` = `kIOHIDEventTypeRotation` (Field `114` float64 = rotation degrees)
       - `8` = `kIOHIDEventTypeZoom` (Field `114` float64 = scale delta, e.g., `+0.02` for 2% zoom in)
     - Field `132` (`kCGEventGesturePhase`): `1` (Began), `2` (Changed), `4` (Ended).
   - Every gesture packet must be paired with a backing `kCGEventGesture` phase frame so Preview, Safari, Figma, and Maps zoom smoothly around the cursor position!
2. **3-Finger & 4-Finger System Gestures (Mission Control, App Exposé, Desktop Spaces)**:
   - macOS maps system gestures to `CGSSymbolicHotKey` IDs in `CoreGraphics`:
     - `32` = Mission Control (`3-Finger Swipe Up`)
     - `33` = Application Windows / App Exposé (`3-Finger Swipe Down`)
     - `36` = Show Desktop (`4-Finger Thumb+3F Spread`)
     - `79` = Move Left a Space (`3/4-Finger Swipe Right`)
     - `81` = Move Right a Space (`3/4-Finger Swipe Left`)
   - Calling `CGSGetSymbolicHotKeyValue(id, &keyEquiv, &vkCode, &mods)` retrieves the user's actual configured keycode/modifiers from WindowServer, or we can trigger Dock's fluid gesture via `CGEventCreateKeyboardEvent` with the resolved `vkCode` and `CGEventFlags(mods)`.

---

## 2. Windows Native Input, High-Res Scroll & Touch/Precision Touchpad Injection

On Windows (`GOOS=windows`), we reach `user32.dll` directly via `syscall.NewLazyDLL("user32.dll")`.

### 2.1 High-Resolution Smooth Mouse & Wheel (`SendInput`)
```go
const (
	INPUT_MOUSE      = 0
	 INPUT_KEYBOARD   = 1

	MOUSEEVENTF_MOVE       = 0x0001
	MOUSEEVENTF_LEFTDOWN   = 0x0002
	MOUSEEVENTF_LEFTUP     = 0x0004
	MOUSEEVENTF_RIGHTDOWN  = 0x0008
	MOUSEEVENTF_RIGHTUP    = 0x0010
	MOUSEEVENTF_MIDDLEDOWN = 0x0020
	MOUSEEVENTF_MIDDLEUP   = 0x0040
	MOUSEEVENTF_WHEEL      = 0x0800
	MOUSEEVENTF_HWHEEL     = 0x1000
)

type MOUSEINPUT struct {
	Dx          int32
	Dy          int32
	MouseData   uint32 // int32 wheel delta cast to uint32 (120 = 1 standard notch, send 1..30 for smooth pixel scroll!)
	DwFlags     uint32
	Time        uint32
	DwExtraInfo uintptr
}

type MOUSE_INPUT_UNION struct {
	Type uint32
	_    uint32 // 64-bit alignment padding on amd64/arm64!
	Mi   MOUSEINPUT
	_    [8]byte // union padding to sizeof(INPUT) == 40 bytes on 64-bit Windows
}
```
> **Critical Windows Struct Alignment Invariant**: On 64-bit Windows (`amd64`/`arm64`), `sizeof(INPUT)` **must be exactly 40 bytes** (`4B Type + 4B Pad + 32B KEYBDINPUT/MOUSEINPUT/HARDWAREINPUT union`). If `cbSize != 40`, `SendInput` silently returns `0` (`ERROR_INVALID_PARAMETER`).

### 2.2 Sub-Notch Smooth Wheel Scrolling on Windows 10/11
- Standard mouse wheels send `MouseData = ±120` (`WHEEL_DELTA`).
- Modern Windows apps (Chrome, Edge, VS Code, Explorer, UWP/WinUI) support **high-resolution fractional wheel deltas** (`MouseData = ±1` to `±40` at 120 Hz).
- During 2-finger scroll, Edge Scroll, and Momentum decay, we scale 1 point of phone movement to `4` wheel units and emit immediately every frame without waiting to accumulate `120`, delivering silky 120 Hz scrolling on Windows.

### 2.3 Windows 10/11 Multi-Touch & Gesture Injection
1. **Native Windows 11 3-Finger & 4-Finger Gestures**:
   - **3-Finger Swipe Up (Task View)**: `Win + Tab` (`VK_LWIN (0x5B)` + `VK_TAB (0x09)`)
   - **3-Finger Swipe Down (Show Desktop)**: `Win + D` (`VK_LWIN` + `'D' (0x44)`)
   - **3-Finger Swipe Left/Right (App Switcher)**: Hold `Alt` (`VK_MENU 0x12`), tap `Tab` / `Shift+Tab` while fingers move horizontally, release `Alt` on `TouchEnd`!
   - **4-Finger Swipe Left/Right (Switch Virtual Desktops)**: `Ctrl + Win + Left/Right` (`VK_LCONTROL (0xA2)` + `VK_LWIN (0x5B)` + `VK_LEFT (0x25)` / `VK_RIGHT (0x27)`).
   - **2-Finger Pinch-to-Zoom**: Hold `Ctrl` (`VK_LCONTROL`) + high-res `MOUSEEVENTF_WHEEL` deltas (supported universally across browsers, editors, Photo Viewer, Office).
2. **`CreateSyntheticPointerDevice` / `InitializeTouchInjection` (`user32.dll`)**:
   - Windows 10 (1809+) and Windows 11 expose `CreateSyntheticPointerDevice(PT_TOUCH, 5, POINTER_FEEDBACK_NONE)` and `InjectSyntheticPointerInput(hDevice, &pointerInfo[0], count)`.
   - Struct `POINTER_TYPE_INFO` + `POINTER_TOUCH_INFO` allows injecting raw 5-finger contacts directly into the Windows Pointer Input stack (`WM_POINTERDOWN`, `WM_POINTERUPDATE`, `WM_POINTERUP`) for DirectManipulation-aware applications.

---

## 3. Bluetooth HID Peripheral Mode (Zero Host Software Required)

Android 9+ (`API 28+`) includes `android.bluetooth.BluetoothHidDevice`, which registers the phone as a real hardware Bluetooth HID peripheral (`UUID 0x1812`). Both **macOS** and **Windows** connect to it natively from System Settings $\rightarrow$ Bluetooth.

### 3.1 Composite HID Report Descriptor (High-Res 16-bit Mouse + Smooth Wheel + AC Pan + Gesture Hotkeys)
To ensure 100% plug-and-play compatibility across macOS and Windows without needing custom host drivers in Bluetooth mode, we use a **Composite HID Descriptor** with two Report IDs:
- **Report ID `0x01` (7 Bytes)**:
  - Byte 0: Buttons (`Bit 0: Left`, `Bit 1: Right`, `Bit 2: Middle`, `Bit 3: Back`, `Bit 4: Forward`)
  - Bytes 1–2: `X` Relative displacement (`int16` little-endian, `-32767` to `+32767` — avoids 8-bit `±127` clipping on fast flicks!)
  - Bytes 3–4: `Y` Relative displacement (`int16` little-endian)
  - Byte 5: Vertical Wheel (`int8`, `-127` to `+127`, with Resolution Multiplier feature report support)
  - Byte 6: Horizontal Pan (`AC Pan`, `Usage 0x0C, 0x0238`, `int8`, `-127` to `+127` — supported natively by both macOS and Windows for horizontal trackpad scrolling!)

```java
// Exact HID Report Descriptor byte array for TouchpadBridge.java
public static final byte[] HID_REPORT_DESCRIPTOR = new byte[] {
    (byte) 0x05, (byte) 0x01,       // Usage Page (Generic Desktop)
    (byte) 0x09, (byte) 0x02,       // Usage (Mouse)
    (byte) 0xA1, (byte) 0x01,       // Collection (Application)
    (byte) 0x85, (byte) 0x01,       //   Report ID (1)
    (byte) 0x09, (byte) 0x01,       //   Usage (Pointer)
    (byte) 0xA1, (byte) 0x00,       //   Collection (Physical)
    // 5 Buttons
    (byte) 0x05, (byte) 0x09,       //     Usage Page (Button)
    (byte) 0x19, (byte) 0x01,       //     Usage Minimum (Button 1)
    (byte) 0x29, (byte) 0x05,       //     Usage Maximum (Button 5)
    (byte) 0x15, (byte) 0x00,       //     Logical Minimum (0)
    (byte) 0x25, (byte) 0x01,       //     Logical Maximum (1)
    (byte) 0x95, (byte) 0x05,       //     Report Count (5)
    (byte) 0x75, (byte) 0x01,       //     Report Size (1)
    (byte) 0x81, (byte) 0x02,       //     Input (Data, Var, Abs)
    // 3-bit padding
    (byte) 0x95, (byte) 0x01,       //     Report Count (1)
    (byte) 0x75, (byte) 0x03,       //     Report Size (3)
    (byte) 0x81, (byte) 0x01,       //     Input (Const, Array, Abs)
    // 16-bit X, Y relative movement
    (byte) 0x05, (byte) 0x01,       //     Usage Page (Generic Desktop)
    (byte) 0x09, (byte) 0x30,       //     Usage (X)
    (byte) 0x09, (byte) 0x31,       //     Usage (Y)
    (byte) 0x16, (byte) 0x01, (byte) 0x80, // Logical Minimum (-32767)
    (byte) 0x26, (byte) 0xFF, (byte) 0x7F, // Logical Maximum (32767)
    (byte) 0x75, (byte) 0x10,       //     Report Size (16)
    (byte) 0x95, (byte) 0x02,       //     Report Count (2)
    (byte) 0x81, (byte) 0x06,       //     Input (Data, Var, Rel)
    // 8-bit Vertical Wheel
    (byte) 0x09, (byte) 0x38,       //     Usage (Wheel)
    (byte) 0x15, (byte) 0x81,       //     Logical Minimum (-127)
    (byte) 0x25, (byte) 0x7F,       //     Logical Maximum (127)
    (byte) 0x75, (byte) 0x08,       //     Report Size (8)
    (byte) 0x95, (byte) 0x01,       //     Report Count (1)
    (byte) 0x81, (byte) 0x06,       //     Input (Data, Var, Rel)
    // 8-bit Horizontal Wheel (AC Pan in Consumer Page)
    (byte) 0x05, (byte) 0x0C,       //     Usage Page (Consumer Devices)
    (byte) 0x0A, (byte) 0x38, (byte) 0x02, // Usage (AC Pan)
    (byte) 0x15, (byte) 0x81,       //     Logical Minimum (-127)
    (byte) 0x25, (byte) 0x7F,       //     Logical Maximum (127)
    (byte) 0x75, (byte) 0x08,       //     Report Size (8)
    (byte) 0x95, (byte) 0x01,       //     Report Count (1)
    (byte) 0x81, (byte) 0x06,       //     Input (Data, Var, Rel)
    (byte) 0xC0,                    //   End Collection (Physical)
    (byte) 0xC0                     // End Collection (Application)
};
```

### 3.2 `BluetoothHidDeviceAppQosSettings` for Low Latency (< 11.25 ms)
When registering the HID app in `BluetoothHidDevice.registerApp(sdpSettings, inQos, outQos, executor, callback)`, we explicitly supply low-latency QoS parameters so the Bluetooth baseband controller negotiates the fastest sniff/poll interval (`7.5 ms` – `11.25 ms`):
```java
BluetoothHidDeviceAppQosSettings outQos = new BluetoothHidDeviceAppQosSettings(
    BluetoothHidDeviceAppQosSettings.SERVICE_GUARANTEED,
    800,   // tokenRate (bytes/s)
    9,     // tokenBucketSize
    0,     // peakBandwidth
    11250, // latency in microseconds (11.25 ms)
    BluetoothHidDeviceAppQosSettings.MAX
);
```
