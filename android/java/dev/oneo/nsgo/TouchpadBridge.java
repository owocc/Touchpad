package dev.oneo.nsgo;

import android.app.Activity;
import android.bluetooth.BluetoothAdapter;
import android.bluetooth.BluetoothDevice;
import android.bluetooth.BluetoothHidDevice;
import android.bluetooth.BluetoothHidDeviceAppSdpSettings;
import android.bluetooth.BluetoothProfile;
import android.content.Context;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Rect;
import android.os.Build;
import android.os.VibrationEffect;
import android.os.Vibrator;
import android.util.AttributeSet;
import android.view.HapticFeedbackConstants;
import android.view.MotionEvent;
import android.view.View;
import android.view.Window;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.view.WindowManager;

import java.io.OutputStream;
import java.net.Socket;
import java.nio.ByteBuffer;
import java.nio.ByteOrder;
import java.util.Collections;
import java.util.List;

/**
 * TouchpadBridge provides high-frequency unbatched touch sampling (up to 240Hz+),
 * Android system gesture exclusion, OLED AMOLED black display mode,
 * microsecond haptic tactile triggers, and an optional Bluetooth HID peripheral prototype.
 */
public class TouchpadBridge extends View {

    public interface OnTouchpadListener {
        void onSample(int action, int pointerCount, float x, float y, long eventTimeNanos);
        void onHapticTriggered(int kind);
    }

    private OnTouchpadListener listener;
    private Vibrator vibrator;
    private final Paint paint = new Paint();
    private boolean oledBlack = true;
    private boolean fastSocketEnabled = false;
    private Socket fastSocket;
    private OutputStream fastOut;
    private final ByteBuffer frameBuffer = ByteBuffer.allocate(24).order(ByteOrder.LITTLE_ENDIAN);

    // Bluetooth HID
    private BluetoothHidDevice btHidDevice;
    private BluetoothDevice connectedBtHost;
    private boolean btHidRegistered = false;

    // Composite HID Report Descriptor (Mouse + High-res Wheel + AC Pan)
    public static final byte[] HID_REPORT_DESCRIPTOR = new byte[] {
        (byte) 0x05, (byte) 0x01,       // Usage Page (Generic Desktop)
        (byte) 0x09, (byte) 0x02,       // Usage (Mouse)
        (byte) 0xA1, (byte) 0x01,       // Collection (Application)
        (byte) 0x85, (byte) 0x01,       //   Report ID (1)
        (byte) 0x09, (byte) 0x01,       //   Usage (Pointer)
        (byte) 0xA1, (byte) 0x00,       //   Collection (Physical)
        (byte) 0x05, (byte) 0x09,       //     Usage Page (Buttons)
        (byte) 0x19, (byte) 0x01,       //     Usage Minimum (1)
        (byte) 0x29, (byte) 0x05,       //     Usage Maximum (5)
        (byte) 0x15, (byte) 0x00,       //     Logical Minimum (0)
        (byte) 0x25, (byte) 0x01,       //     Logical Maximum (1)
        (byte) 0x95, (byte) 0x05,       //     Report Count (5)
        (byte) 0x75, (byte) 0x01,       //     Report Size (1)
        (byte) 0x81, (byte) 0x02,       //     Input (Data, Variable, Absolute)
        (byte) 0x95, (byte) 0x01,       //     Report Count (1)
        (byte) 0x75, (byte) 0x03,       //     Report Size (3) - Padding
        (byte) 0x81, (byte) 0x01,       //     Input (Constant)
        (byte) 0x05, (byte) 0x01,       //     Usage Page (Generic Desktop)
        (byte) 0x09, (byte) 0x30,       //     Usage (X)
        (byte) 0x09, (byte) 0x31,       //     Usage (Y)
        (byte) 0x16, (byte) 0x01, (byte) 0x80, // Logical Minimum (-32767)
        (byte) 0x26, (byte) 0xFF, (byte) 0x7F, // Logical Maximum (32767)
        (byte) 0x75, (byte) 0x10,       //     Report Size (16 bits)
        (byte) 0x95, (byte) 0x02,       //     Report Count (2)
        (byte) 0x81, (byte) 0x06,       //     Input (Data, Variable, Relative)
        (byte) 0x09, (byte) 0x38,       //     Usage (Wheel)
        (byte) 0x15, (byte) 0x81,       //     Logical Minimum (-127)
        (byte) 0x25, (byte) 0x7F,       //     Logical Maximum (127)
        (byte) 0x75, (byte) 0x08,       //     Report Size (8 bits)
        (byte) 0x95, (byte) 0x01,       //     Report Count (1)
        (byte) 0x81, (byte) 0x06,       //     Input (Data, Variable, Relative)
        (byte) 0x05, (byte) 0x0C,       //     Usage Page (Consumer Devices)
        (byte) 0x0A, (byte) 0x38, (byte) 0x02, // Usage (AC Pan)
        (byte) 0x15, (byte) 0x81,       //     Logical Minimum (-127)
        (byte) 0x25, (byte) 0x7F,       //     Logical Maximum (127)
        (byte) 0x75, (byte) 0x08,       //     Report Size (8 bits)
        (byte) 0x95, (byte) 0x01,       //     Report Count (1)
        (byte) 0x81, (byte) 0x06,       //     Input (Data, Variable, Relative)
        (byte) 0xC0,                    //   End Collection
        (byte) 0xC0                     // End Collection
    };

    public TouchpadBridge(Context context) {
        super(context);
        init();
    }

    public TouchpadBridge(Context context, AttributeSet attrs) {
        super(context, attrs);
        init();
    }

    private void init() {
        setFocusable(true);
        setFocusableInTouchMode(true);
        vibrator = (Vibrator) getContext().getSystemService(Context.VIBRATOR_SERVICE);
    }

    public void setOnTouchpadListener(OnTouchpadListener l) {
        this.listener = l;
    }

    @Override
    protected void onLayout(boolean changed, int left, int top, int right, int bottom) {
        super.onLayout(changed, left, top, right, bottom);
        excludeSystemGestures(right - left, bottom - top);
        hideSystemBars();
    }

    private void excludeSystemGestures(int w, int h) {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            // Exclude whole touch view (and especially right/bottom gutters)
            // so system back gestures won't interrupt edge scrolling.
            Rect rect = new Rect(0, 0, w, h);
            setSystemGestureExclusionRects(Collections.singletonList(rect));
        }
    }

    private void hideSystemBars() {
        if (getContext() instanceof Activity) {
            Activity act = (Activity) getContext();
            Window window = act.getWindow();
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                WindowInsetsController controller = window.getInsetsController();
                if (controller != null) {
                    controller.hide(WindowInsets.Type.statusBars() | WindowInsets.Type.navigationBars());
                    controller.setSystemBarsBehavior(WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE);
                }
            } else {
                window.getDecorView().setSystemUiVisibility(
                    View.SYSTEM_UI_FLAG_FULLSCREEN |
                    View.SYSTEM_UI_FLAG_HIDE_NAVIGATION |
                    View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                );
            }
        }
    }

    public void setOledDim(boolean dim) {
        if (getContext() instanceof Activity) {
            Activity act = (Activity) getContext();
            act.runOnUiThread(() -> {
                Window window = act.getWindow();
                if (dim) {
                    window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                    WindowManager.LayoutParams lp = window.getAttributes();
                    lp.screenBrightness = 0.01f;
                    window.setAttributes(lp);
                } else {
                    window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON);
                    WindowManager.LayoutParams lp = window.getAttributes();
                    lp.screenBrightness = WindowManager.LayoutParams.BRIGHTNESS_OVERRIDE_NONE;
                    window.setAttributes(lp);
                }
            });
        }
    }

    public void haptic(int kind) {
        if (vibrator == null) return;
        switch (kind) {
            case 1: // Press Down (Crisp Mechanical Click)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
                    vibrator.areAllPrimitivesSupported(VibrationEffect.Composition.PRIMITIVE_CLICK)) {
                    vibrator.vibrate(VibrationEffect.startComposition()
                        .addPrimitive(VibrationEffect.Composition.PRIMITIVE_CLICK, 0.85f).compose());
                } else {
                    performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK,
                        HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
                }
                break;

            case 2: // Release Up (Soft Micro-switch Rebound)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R &&
                    vibrator.areAllPrimitivesSupported(VibrationEffect.Composition.PRIMITIVE_TICK)) {
                    vibrator.vibrate(VibrationEffect.startComposition()
                        .addPrimitive(VibrationEffect.Composition.PRIMITIVE_TICK, 0.45f).compose());
                } else {
                    performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK,
                        HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
                }
                break;

            case 3: // Edge Scroll Detent Tick
                performHapticFeedback(HapticFeedbackConstants.CLOCK_TICK,
                    HapticFeedbackConstants.FLAG_IGNORE_GLOBAL_SETTING);
                break;

            case 4: // Two-Finger Secondary Click
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                    vibrator.vibrate(VibrationEffect.createPredefined(VibrationEffect.EFFECT_HEAVY_CLICK));
                }
                break;
        }
    }

    @Override
    public boolean onTouchEvent(MotionEvent e) {
        int action = e.getActionMasked();
        int pointerCount = e.getPointerCount();

        // High-rate unbatched sampling: first extract all intermediate historical samples
        int historySize = e.getHistorySize();
        for (int h = 0; h < historySize; h++) {
            long histTimeNanos = e.getHistoricalEventTimeNanos(h);
            for (int p = 0; p < pointerCount; p++) {
                float hx = e.getHistoricalX(p, h);
                float hy = e.getHistoricalY(p, h);
                if (listener != null) {
                    listener.onSample(MotionEvent.ACTION_MOVE, pointerCount, hx, hy, histTimeNanos);
                }
            }
        }

        // Process current sample
        long eventTimeNanos = e.getEventTime() * 1_000_000L;
        for (int p = 0; p < pointerCount; p++) {
            float x = e.getX(p);
            float y = e.getY(p);
            if (listener != null) {
                listener.onSample(action, pointerCount, x, y, eventTimeNanos);
            }
        }

        return true;
    }

    @Override
    protected void onDraw(Canvas canvas) {
        // OLED Pure Black Canvas
        if (oledBlack) {
            canvas.drawColor(Color.BLACK);
        }
        super.onDraw(canvas);
    }
}
