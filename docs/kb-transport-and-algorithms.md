# Knowledge Base 3: Low-Latency Transport (LAN UDP DSCP, USB/ADB) & Touch Physics Algorithms

This document specifies the wire protocol, socket optimizations, USB auto-forwarding mechanics, and mathematical formulas for the touch filtering, pointer acceleration, inertial scroll, and screen-edge single-finger scroll engines.

---

## 1. Zero-Allocation Binary Wire Protocol (`internal/protocol`)

All high-frequency frames use a fixed **12-byte Header + 12-byte Payload (24 bytes total)** in Little-Endian byte order, avoiding heap allocations (`sync.Pool` or stack `[64]byte` arrays).

### 1.1 Packet Layout (24 Bytes Fixed Frame)
```text
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|  Magic (0x54) |    MsgType    |     Flags     |  FingerCount  |  Bytes 0..3
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        SequenceNumber                         |  Bytes 4..7  (uint32)
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        TimestampMicros                        |  Bytes 8..11 (uint32)
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|            DeltaX             |            DeltaY             |  Bytes 12..19 (float32, float32)
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|     Phase     | MomentumPhase |           AuxValue            |  Bytes 20..23 (uint8, uint8, int16)
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
```

### 1.2 `MsgType` Enumeration
| Code | Name | Description & Field Mapping |
| :--- | :--- | :--- |
| `0x01` | `MsgPointerMove` | Relative cursor move: `DeltaX`, `DeltaY` (post-acceleration or raw), `Flags` bitmask for held buttons. |
| `0x02` | `MsgMouseButton` | Button press/release: `Phase` (`1=Down, 2=Up, 3=Click`), `AuxValue` (`1=Left, 2=Right, 4=Middle`), `FingerCount` = click count (`1, 2, 3`). |
| `0x03` | `MsgScroll` | 2-Finger or 1-Finger Edge Scroll: `DeltaX`, `DeltaY` (pixel deltas), `Phase` (`1=Began, 2=Changed, 4=Ended`), `MomentumPhase` (`0=None, 1=Begin, 2=Continue, 3=End`). |
| `0x04` | `MsgPinchRotate` | 2-Finger Pinch/Rotate: `DeltaX` = scale delta ($\Delta s$), `DeltaY` = rotation angle in degrees ($\Delta \theta$), `Phase` (`1=Began, 2=Changed, 4=Ended`). |
| `0x05` | `MsgSystemSwipe` | 3-Finger or 4-Finger OS Gesture: `FingerCount` (`3` or `4`), `AuxValue` (`1=Up/MissionControl, 2=Down/Exposé, 3=Left/NextSpace, 4=Right/PrevSpace`), `Phase` (`1=Began, 2=Changed, 4=Ended`). |
| `0x06` | `MsgRawContacts` | Extended 64-byte frame containing up to 5 normalized multi-touch contacts `(id, phase, x_u16, y_u16, pressure_u8)` for direct Digitizer injection. |
| `0xFE` | `MsgPing` | Round-trip latency probe echoing `TimestampMicros`. |

### 1.3 Sequence Number & Anti-Jitter Window
- **Drop-Oldest Rule**: For `MsgPointerMove` and `MsgScroll` (`Phase == Changed` and `MomentumPhase == Continue`), if `pkt.SequenceNumber <= lastProcessedSeq`, the packet arrived out-of-order and is **silently dropped**.
- **Critical State Guarantee**: Phase boundary packets (`Began`, `Ended`, `MouseButton Down/Up`, `SystemSwipe`) carry `Flags |= FlagReliable (0x80)`: the sender transmits them **twice** (immediately and piggybacked on the next frame $+4\text{ ms}$) with an idempotent transition ID so the desktop never misses a `MouseUp` or `ScrollPhaseEnded` even under 5% Wi-Fi packet loss!

---

## 2. LAN Fast-Path & Wired USB Auto-Connect (`internal/transport`)

### 2.1 Wi-Fi WMM Voice Queue Prioritization (`DSCP EF`)
Standard UDP packets sit in the Wi-Fi router/phone radio's `AC_BE` (Best Effort) queue behind background traffic, suffering `10–40 ms` jitter spikes. Setting `IP_TOS` to **Expedited Forwarding (`DSCP 46` $\rightarrow$ TOS byte `0xB8`)** routes frames into the hardware `AC_VO` (Voice) TX queue:

```go
func configureLowLatencyUDP(conn *net.UDPConn) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return err
	}
	return raw.Control(func(fd uintptr) {
		// 0xB8 = DSCP 46 (EF) << 2, mapped to 802.11e WMM AC_VO (Voice priority)
		_ = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TOS, 0xB8)
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 256*1024)
		_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, 256*1024)
	})
}
```

### 2.2 Wired USB Mode (Android $\leftrightarrow$ macOS / Windows)
We support **two parallel zero-configuration wired paths**:

1. **Path A: Pure-Go ADB Server Socket Watcher (Zero User Steps when USB Debugging is On)**
   - The desktop host connects to `127.0.0.1:5037` (the standard local ADB daemon port used by Android Studio / `adb`).
   - Sends `0012host:track-devices`. Whenever a device enters `device` state:
   - Sends `host-serial:<serial>:forward:tcp:29181;localabstract:touchpad_usb`.
   - Connects a TCP socket with `SetNoDelay(true)` to `127.0.0.1:29181`.
   - All 24-byte frames traverse the USB 2.0/3.0 Bulk endpoint directly with **`0.25 ms` – `0.45 ms` round-trip latency** and **zero packet loss**.

2. **Path B: USB Tethering Virtual NIC Auto-Bind (Zero Developer Mode Required)**
   - When the user enables "USB Tethering" in Android settings, macOS (`bridge`/`enX`) and Windows (`Remote NDIS Compatible Device`) assign an IP in `192.168.42.0/24` or `192.168.225.0/24`.
   - Our network watcher monitors `net.Interfaces()` every `2s`. As soon as a point-to-point or USB subnet appears, the mobile client switches its active UDP destination IP to the USB interface IP automatically without dropping the gesture state!

---

## 3. Touch Filtering, Acceleration & Edge-Scroll Algorithms (`internal/gesture`)

### 3.1 Adaptive 1-Euro Filter (`filter_one_euro.go`)
Eliminates sub-pixel capacitive screen noise when holding a finger nearly still (for pixel-accurate button clicking) while adding zero lag during fast flicks.

Given sample $(x_t, t)$ with time step $\Delta t = t - t_{t-1}$:
1. **Filtered Derivative (Velocity)**:
   $$\hat{\dot{x}}_t = \alpha(\Delta t, f_{d}) \cdot \frac{x_t - \hat{x}_{t-1}}{\Delta t} + (1 - \alpha(\Delta t, f_{d})) \cdot \hat{\dot{x}}_{t-1}$$
2. **Dynamic Cutoff Frequency**:
   $$f_c(t) = f_{c_{\min}} + \beta \cdot |\hat{\dot{x}}_t|$$
3. **Filtered Position**:
   $$\hat{x}_t = \alpha(\Delta t, f_c(t)) \cdot x_t + (1 - \alpha(\Delta t, f_c(t))) \cdot \hat{x}_{t-1}$$
   where the smoothing factor is:
   $$\alpha(\Delta t, f) = \frac{1}{1 + \frac{1}{2\pi f \Delta t}}$$
- **Tuned Touchpad Constants**:
  - $f_{c_{\min}} = 1.2\text{ Hz}$ (strong jitter suppression at rest)
  - $\beta = 0.04$ (rapidly opens filter bandwidth on movement)
  - $f_d = 1.0\text{ Hz}$

### 3.2 Sigmoid Pointer Acceleration + Sub-Pixel Accumulator (`accel_sigmoid.go`)
Given filtered displacement $(\Delta x, \Delta y)$ in points over $\Delta t$ seconds, instantaneous speed is $v = \frac{\sqrt{\Delta x^2 + \Delta y^2}}{\Delta t}$ (in $\text{pt/s}$):
1. **Gain Function $G(v)$**:
   $$G(v) = S_{\text{user}} \cdot \left( G_{\min} + \frac{G_{\max} - G_{\min}}{1 + e^{-k(v - v_{\text{mid}})}} \right)$$
   - Default parameters: $G_{\min} = 0.45$ (sub-pixel sniper precision at slow speeds), $G_{\max} = 3.6$, $v_{\text{mid}} = 280\text{ pt/s}$, $k = 0.011$, $S_{\text{user}} \in [0.3, 3.0]$.
2. **Sub-Pixel Remainder Preservation**:
   ```go
   rawX := dx * gain + s.remX
   rawY := dy * gain + s.remY
   outX := float32(math.Trunc(float64(rawX)))
   outY := float32(math.Trunc(float64(rawY)))
   s.remX = rawX - outX
   s.remY = rawY - outY
   ```
   Without `remX`/`remY`, moving a finger slowly at $0.4\text{ pt/frame}$ would truncate to `0` every frame and freeze the cursor!

### 3.3 Screen Edge Single-Finger Scroll (`edge_scroll.go`)
Allows effortless infinite single-handed scrolling with the thumb along the screen edge:

```text
+----------------------------------------+-------+
|                                        |   |   |
|                                        | V |   |
|                                        | E | R |
|         Main Multi-Touch Pad           | R | I |
|       (1F Move, 2F Scroll/Pinch,       | T | G |
|        3F/4F System Gestures)          |   | H |
|                                        | S | T |
|                                        | C |   |
|                                        | R | 28|
|                                        | L | pt|
+----------------------------------------+-------+
|       Bottom Horizontal Scroll (28 pt)         |
+------------------------------------------------+
```

#### State Machine Rules:
1. **Zone Entry (`TouchStart`, `FingerCount == 1`)**:
   - **Right Gutter (`X >= PadWidth - EdgeWidth`, default `EdgeWidth = 28 pt`)**: Enters `StateEdgeScrollVertical`.
   - **Bottom Gutter (`Y >= PadHeight - EdgeWidth`)**: Enters `StateEdgeScrollHorizontal`.
   - Once locked in `StateEdgeScroll*`, the finger can drift slightly inward (up to `56 pt` hysteresis band) without breaking the scroll lock until `TouchEnd`!
2. **Activation & Tap Disambiguation**:
   - Wait until cumulative $|\Delta y| > 3\text{ pt}$ (or $|\Delta x| > 3\text{ pt}$) before emitting `MsgScroll(Phase = Began)`. If the user lifts within `< 180 ms` and `< 3 pt` movement, treat it as a normal single tap!
3. **Haptic Detent Ticks & Inertial Hand-off**:
   - Accumulate scrolled distance `detentAcc += abs(dy)`. Whenever `detentAcc >= 18.0 pt`, fire `HapticScrollTick` (`detentAcc -= 18.0`) to give the physical feel of a precision rotary wheel.
   - On `TouchEnd`, hand the smoothed release velocity $v_0$ directly to `MomentumGenerator` (`momentum.go`), so flicking a thumb down the right edge glides with full macOS/Windows inertia and rubber-banding!
