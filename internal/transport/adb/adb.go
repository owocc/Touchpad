package adb

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// DeviceState represents ADB device status.
type DeviceState string

const (
	StateDevice       DeviceState = "device"
	StateUnauthorized DeviceState = "unauthorized"
	StateOffline      DeviceState = "offline"
)

// Device represents an attached Android hardware device.
type Device struct {
	Serial string
	State  DeviceState
	Model  string
}

// Manager handles adb binary execution, device enumeration, and port forwarding.
type Manager struct {
	mu      sync.Mutex
	adbPath string
}

// NewManager detects or configures ADB path.
func NewManager() *Manager {
	path, err := exec.LookPath("adb")
	if err != nil {
		path = "adb"
	}
	return &Manager{
		adbPath: path,
	}
}

// ListDevices returns all currently connected Android devices.
func (m *Manager) ListDevices() ([]Device, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := exec.Command(m.adbPath, "devices", "-l")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("adb devices failed: %w", err)
	}

	lines := strings.Split(string(out), "\n")
	var devices []Device

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 2 {
			dev := Device{
				Serial: parts[0],
				State:  DeviceState(parts[1]),
			}
			for _, p := range parts[2:] {
				if strings.HasPrefix(p, "model:") {
					dev.Model = strings.TrimPrefix(p, "model:")
				}
			}
			devices = append(devices, dev)
		}
	}

	return devices, nil
}

// Forward executes port forwarding:
// `adb -s <serial> forward tcp:<localPort> <remoteTarget>`
// remoteTarget can be "localabstract:touchpad_usb" or "tcp:29181"
func (m *Manager) Forward(serial string, localPort int, remoteTarget string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	args := []string{}
	if serial != "" {
		args = append(args, "-s", serial)
	}
	args = append(args, "forward", fmt.Sprintf("tcp:%d", localPort), remoteTarget)

	cmd := exec.Command(m.adbPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("adb forward error: %w (stderr: %s)", err, stderr.String())
	}
	return nil
}

// RemoveForward removes a forwarded port.
func (m *Manager) RemoveForward(serial string, localPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	args := []string{}
	if serial != "" {
		args = append(args, "-s", serial)
	}
	args = append(args, "forward", "--remove", fmt.Sprintf("tcp:%d", localPort))

	cmd := exec.Command(m.adbPath, args...)
	return cmd.Run()
}

// DeviceWatcher continuously notifies when devices connect or disconnect.
type DeviceWatcher struct {
	mgr       *Manager
	stopChan  chan struct{}
	OnDevices func(devices []Device)
}

// NewDeviceWatcher creates a watcher polling every 1.5 seconds.
func NewDeviceWatcher(mgr *Manager, onDevices func(devices []Device)) *DeviceWatcher {
	return &DeviceWatcher{
		mgr:       mgr,
		stopChan:  make(chan struct{}),
		OnDevices: onDevices,
	}
}

// Start begins the watcher loop.
func (w *DeviceWatcher) Start() {
	go func() {
		ticker := time.NewTicker(1500 * time.Millisecond)
		defer ticker.Stop()

		var lastSerials string
		for {
			select {
			case <-w.stopChan:
				return
			case <-ticker.C:
				devs, err := w.mgr.ListDevices()
				if err != nil {
					continue
				}

				var current []string
				for _, d := range devs {
					current = append(current, fmt.Sprintf("%s:%s", d.Serial, d.State))
				}
				joined := strings.Join(current, ",")
				if joined != lastSerials {
					lastSerials = joined
					if w.OnDevices != nil {
						w.OnDevices(devs)
					}
				}
			}
		}
	}()
}

// Stop stops the watcher.
func (w *DeviceWatcher) Stop() {
	select {
	case <-w.stopChan:
	default:
		close(w.stopChan)
	}
}

var ErrNoDevice = errors.New("adb: no connected android device found")
