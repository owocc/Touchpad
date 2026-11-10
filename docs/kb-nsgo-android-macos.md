# Knowledge Base 1: NSGo Framework Internals, Scheme B Android Extension & OLED/Haptics APIs

This document records the exact APIs, source mechanisms, and architectural blueprints for building the Touchpad app on [`oneo-me/nsgo`](https://github.com/oneo-me/nsgo) using **Scheme B (Single Unified `nsgo` Build)**.

---

## 1. NSGo Core & Desktop Lifecycle APIs

### 1.1 Unified Entry Point & Platform Branching
An NSGo application starts via `nsgo.Run`. To share a single binary/repository across macOS/Windows Desktop Hosts and Android Mobile Clients:

```go
package main

import (
	"github.com/oneo-me/nsgo"
	"github.com/oneo-me/nsgo/core"
	"github.com/oneo-me/nsgo/desktop"
)

func main() {
	// HideDock() makes macOS start as NSApplicationActivationPolicyAccessory (menu-bar tray app);
	// on Android/Windows it is a harmless no-op or handled per window.
	nsgo.Run(App, nsgo.HideDock())
}

func App(app *core.Scope) {
	switch app.Env().OS() {
	case core.OSAndroid, core.OSIOS:
		nsgo.SetMainView(app, MobileTouchpadRoot)
	default:
		// Desktop host mode (macOS / Windows / Linux)
		desktop.SetShutdownMode(app, desktop.ShutdownOnExplicit)
		panel := desktop.OpenPanel(app, desktop.PanelOptions{
			Width:      360,
			Height:     480,
			HideOnBlur: true,
		}, DesktopTrayPanel)
		desktop.NewTray(app, desktop.TrayOptions{
			Title:   "Touchpad",
			Tooltip: "Touchpad Host Daemon",
			Panel:   panel,
			Menu:    buildTrayMenu(app),
		})
	}
}
```

### 1.2 NSGo Desktop Capabilities Matrix (macOS vs Windows)
Per `docs/architecture/application.md` and `docs/roadmap.md`:
- **macOS (`internal/platform/macos`)**:
  - `desktop.NewTray`, `desktop.OpenPanel` (`NsgoPanel` at `NSStatusWindowLevel`), `desktop.OpenWindow`, and `desktop.RegisterHotKey` (Carbon `RegisterEventHotKey`) are **fully implemented**.
  - Pure Go reaches Objective-C and C frameworks via `github.com/ebitengine/purego` and `github.com/ebitengine/purego/objc` with `CGO_ENABLED=0`.
- **Windows (`internal/platform/windows`)**:
  - Currently runs the root view in the main window (`OpenWindow`/`NewTray` return `nil` on Windows until the roadmap item `Shell_NotifyIcon` lands).
  - **Fallback Strategy on Windows**: When `app.Env().OS() == core.OSWindows`, call `nsgo.SetMainView(app, DesktopMainWindow)` so the Windows host opens a compact control/QR-pairing window (`nsgo.WindowSize(400, 560)`), or register a minimal Win32 `Shell_NotifyIconW` tray icon directly via `syscall.NewLazyDLL("shell32.dll")`.

### 1.3 `nsgo.yaml` Configuration Specification
`nsgo` reads `nsgo.yaml` at the repository root. Note: window sizes belong in Go (`nsgo.WindowSize`), **never** in `nsgo.yaml`.

```yaml
name: Touchpad
id: me.oneo.touchpad
version: 0.1.0
build_number: 1
appearance: dark

android:
  permissions:
    - VIBRATE
    - HIGH_SAMPLING_RATE_SENSORS
    - BLUETOOTH
    - BLUETOOTH_ADMIN
    - BLUETOOTH_CONNECT
    - BLUETOOTH_ADVERTISE
    - BLUETOOTH_SCAN
    - ACCESS_NETWORK_STATE
    - ACCESS_WIFI_STATE
    - CHANGE_WIFI_MULTICAST_STATE
    - WAKE_LOCK

apple:
  info:
    NSLocalNetworkUsageDescription: "Touchpad uses the local network to send low-latency multi-touch events to your Mac or PC."
    NSBonjourServices: "_touchpad._udp"
    CADisableMinimumFrameDurationOnPhone: "true"
```

---

## 2. Scheme B Deep Dive: Extending NSGo's Android Runtime (`classes.dex`)

### 2.1 How NSGo Works on Android
In `internal/platform/android/android.go` and `internal/platform/android/java/dev/oneo/nsgo/`:
1. `go tool nsgo build -target android_arm64` compiles the Go app as an arm64 ELF executable (`CGO_ENABLED=0`) and packages it inside the APK as `lib/arm64-v8a/libnsgo.so`.
2. When the Android app launches `dev.oneo.nsgo.NsgoActivity`, it creates an `NsgoView`, which executes `libnsgo.so` as a **child process** and communicates over anonymous pipes (`Protocol.java` $\leftrightarrow$ `android.go`).
3. **Why Standard `View.OnTouch` Is Insufficient for a Pro Touchpad**:
   - In `NsgoView.java` (`touches(MotionEvent e)` at line 1678), touch events are formatted into a string `"id x y changed;id x y changed"` and sent as `Protocol.EVENT_TOUCH` over the pipe.
   - It **drops** `e.getHistorySize()` (all 240 Hz intermediate samples captured between UI frames), `e.getPressure(i)`, and `e.getTouchMajor(i)`.
   - In `internal/driver/events.go` (line 295), `EventTouch` is marked `Mergeable() = true`, meaning queued move events are overwritten if the UI engine is busy.

### 2.2 The Reflection-Based `core.Native` Mechanism in `Embedded.java`
Inspecting `internal/platform/android/java/dev/oneo/nsgo/Embedded.java` reveals NSGo's built-in bridge for native Android views without JNI:
- When Go renders `core.Native{Component: "touchpad.surface", Props: ...}`, the Go component's `View() any` method returns a **Java class name string**, e.g. `"dev.oneo.nsgo.TouchpadBridge"`.
- `Embedded.build(Context context, String name, Emitter emit)` instantiates `Class.forName(name).getConstructor(Context.class).newInstance(context)`.
- **Props (`SetProp`)**: Any prop `foo` sent from Go automatically invokes `public void setFoo(...)` on the Java `View` via reflection!
- **Commands (`Ref.Command(name, args)`)**: Automatically invokes `public void <name>(...)` on the Java `View`!
- **Events (`Emitter`)**: `Embedded.listen()` scans the `View` subclass for any `public void setOn<X>Listener(Interface)` method, attaches a `java.lang.reflect.Proxy` listener, and forwards every callback invocation to Go's `ViewEvent(methodName string, args []any)` as a JSON array!

### 2.3 `TouchpadBridge.java` Design (Added to `dev.oneo.nsgo`)
By placing `TouchpadBridge.java` in `internal/platform/android/java/dev/oneo/nsgo/TouchpadBridge.java` and running `internal/platform/android/runtime/build.sh` once, `classes.dex` embeds our high-frequency native bridge while preserving 100% compatibility with `go tool nsgo build` and `go tool nsgo dev`!

#### Key Responsibilities of `TouchpadBridge.java`:
1. **High-Rate Unbatched Touch Capture & Direct Local Socket/Callback**:
   - Overrides `onTouchEvent(MotionEvent e)`.
   - Iterates `for (int h = 0; h < e.getHistorySize(); h++)` to extract every 240 Hz historical sample (`getHistoricalEventTimeNanos(h)`, `getHistoricalX(p, h)`, `getHistoricalY(p, h)`, `getHistoricalTouchMajor(p, h)`), followed by the current sample.
   - **Fast-Path**: To avoid JSON serialization overhead over the UI pipe at 240 Hz, `TouchpadBridge` either:
     - Writes binary 24-byte frames directly to a local Unix Domain Socket (`LocalSocket` connected to `@touchpad_fastpath` listened by the Go child process in `net.ListenUnix("unix", &net.UnixAddr{Name: "@touchpad_fastpath", Net: "unix"})`), **achieving < 50 microseconds IPC latency between Java UI thread and Go network goroutine**, OR
     - Emits via `OnTouchpadListener` for control events.
2. **System Edge Gesture Exclusion (Android 10+ API 29+)**:
   - In `onLayout`, registers the full view rect (especially the left/right/bottom `32 dp` edges) via `setSystemGestureExclusionRects(List<Rect>)` and sets `WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE` while hiding `WindowInsets.Type.systemBars()`.
   - This prevents Android's system "Swipe from right edge to go Back" from stealing the user's **Screen Edge Single-Finger Scroll**!
3. **OLED Hardware Brightness & Keep-Screen-On Control**:
   - `public void setOledDim(boolean enabled)`:
     ```java
     Activity act = (Activity) getContext();
     Window window = act.getWindow();
     if (enabled) {
         window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
         WindowManager.LayoutParams lp = window.getAttributes();
         lp.screenBrightness = 0.01f; // Minimum hardware backlight / AMOLED drive
         window.setAttributes(lp);
     } else {
         WindowManager.LayoutParams lp = window.getAttributes();
         lp.screenBrightness = WindowManager.LayoutParams.BRIGHTNESS_OVERRIDE_NONE;
         window.setAttributes(lp);
     }
     ```
4. **Zero-Latency Haptic Primitives (Android 10/11+ API 29/30+)**:
   - `public void haptic(int kind)`:
     ```java
     Vibrator vib = getContext().getSystemService(Vibrator.class);
     switch (kind) {
         case 1: // Press Down (Crisp Mechanical Click)
             if (Build.VERSION.SDK_INT >= 30 && vib.areAllPrimitivesSupported(VibrationEffect.Composition.PRIMITIVE_CLICK)) {
                 vib.vibrate(VibrationEffect.startComposition()
                     .addPrimitive(VibrationEffect.Composition.PRIMITIVE_CLICK, 0.85f).compose());
             } else {
                 performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK,
                     HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
             }
             break;
         case 2: // Release Up (Soft Micro-switch Rebound)
             if (Build.VERSION.SDK_INT >= 30 && vib.areAllPrimitivesSupported(VibrationEffect.Composition.PRIMITIVE_TICK)) {
                 vib.vibrate(VibrationEffect.startComposition()
                     .addPrimitive(VibrationEffect.Composition.PRIMITIVE_TICK, 0.45f).compose());
             } else {
                 performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK,
                     HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
             }
             break;
         case 3: // Edge Scroll Detent Tick
             performHapticFeedback(HapticFeedbackConstants.Segment_Frequent_Tick /* or CLOCK_TICK */,
                 HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
             break;
         case 4: // Secondary / Two-Finger Click
             if (Build.VERSION.SDK_INT >= 29) {
                 vib.vibrate(VibrationEffect.createPredefined(VibrationEffect.EFFECT_HEAVY_CLICK));
             }
             break;
     }
     ```
5. **Bluetooth HID Peripheral Mode (`BluetoothHidDevice`)**:
   - Exposed via `public void startBtHid()`, `public void stopBtHid()`, and direct native touch forwarding when in standalone Bluetooth HID mode (detailed in `docs/kb-virtual-hid-and-input.md`).

### 2.4 Rebuilding `classes.dex`
Per `internal/platform/android/runtime/build.sh`:
```sh
export ANDROID_HOME=$HOME/Library/Android/sdk   # Standard macOS Android SDK path
./internal/platform/android/runtime/build.sh
```
Requirements verified from `build.sh`:
- JDK 17+ (`javac --release 17`)
- `$ANDROID_HOME/platforms/android-34/android.jar`
- `$ANDROID_HOME/build-tools/*/d8` (or `cmdline-tools/latest/bin/d8`)

---

## 3. OLED Pure Black Mode & Burn-In Prevention Specification

Modern Android flagships use LTPO AMOLED screens where pure `#000000` (`RGB(0, 0, 0)`) turns off the organic LED subpixels completely (`0 mA` anode current).

### 3.1 Visual & Power Rules
1. **Canvas Background**: Strictly `#000000` (`core.RGB(0, 0, 0)`), zero gradients or status bar backgrounds.
2. **System Bars**: Hidden via `WindowInsetsController.hide(WindowInsets.Type.statusBars() | WindowInsets.Type.navigationBars())`.
3. **Edge Scroll Gutter Indicator**:
   - Drawn as a `1 pt` ultra-dim hairline (`#141414`) along the right edge (`Width - 28pt`), which brightens slightly to `#2E2E2E` only while a finger is actively scrolling inside the edge gutter, then fades back to `#000000` after `2s` of inactivity.
4. **Lissajous Anti-Burn-In Pixel Shifter**:
   - Any persistent floating button (e.g., the top-left minimal gear/status pill) must never keep static pixels illuminated in the same physical coordinates for > 60 seconds.
   - A 60-second timer in Go updates the HUD pill's offset along a slow Lissajous trajectory:
     $$dx(k) = \text{round}(6 \cdot \sin(0.17 k)), \quad dy(k) = \text{round}(4 \cdot \cos(0.23 k))$$
   - While fingers are actively touching the pad (`touches > 0`), the HUD opacity transitions to `0.0` (100% black screen).
