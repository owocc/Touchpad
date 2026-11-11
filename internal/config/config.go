package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const (
	DefaultUDPPort       = 29180
	DefaultADBPort       = 29181
	DefaultDiscoveryPort = 29182
)

// AppConfig represents user-customizable settings for Touchpad.
type AppConfig struct {
	// Host connection settings
	HostIP        string `json:"host_ip"`
	UDPPort       int    `json:"udp_port"`
	ADBPort       int    `json:"adb_port"`
	DiscoveryPort int    `json:"discovery_port"`
	PairingKey    string `json:"pairing_key"`
	DeviceName    string `json:"device_name"`

	// Touch & pointer behavior
	Sensitivity float64 `json:"sensitivity"` // 0.3 to 3.0, default 1.0
	ScrollSpeed float64 `json:"scroll_speed"` // 0.3 to 3.0, default 1.0
	NaturalScroll bool  `json:"natural_scroll"` // true = macOS trackpad style
	InertiaEnabled bool `json:"inertia_enabled"` // smooth momentum scroll
	TapToClick    bool  `json:"tap_to_click"`   // tap single finger to left click

	// Screen edge scroll
	EdgeScrollEnabled bool    `json:"edge_scroll_enabled"` // right/bottom edge scrolling
	EdgeGutterWidth   float64 `json:"edge_gutter_width"`   // gutter width in pt, default 28.0

	// 1-Euro Filter parameters
	FilterMinCutoff float64 `json:"filter_min_cutoff"` // fc_min, default 1.2 Hz
	FilterBeta      float64 `json:"filter_beta"`       // beta, default 0.04
	FilterDerivFreq float64 `json:"filter_deriv_freq"` // fd, default 1.0 Hz

	// UI & Power
	OledBlackMode bool `json:"oled_black_mode"` // true = strictly #000000
	HapticsEnabled bool `json:"haptics_enabled"` // tactile feedback on tap/tick
	HapticIntensity int  `json:"haptic_intensity"` // 1: light, 2: medium, 3: strong
	AutoDimSeconds  int  `json:"auto_dim_seconds"` // 0 = disabled, default 15
}

// DefaultConfig returns default values.
func DefaultConfig() AppConfig {
	return AppConfig{
		HostIP:            "",
		UDPPort:           DefaultUDPPort,
		ADBPort:           DefaultADBPort,
		DiscoveryPort:     DefaultDiscoveryPort,
		PairingKey:        "",
		DeviceName:        "Touchpad-Client",
		Sensitivity:       1.0,
		ScrollSpeed:       1.0,
		NaturalScroll:     true,
		InertiaEnabled:    true,
		TapToClick:        true,
		EdgeScrollEnabled: true,
		EdgeGutterWidth:   28.0,
		FilterMinCutoff:   1.2,
		FilterBeta:        0.04,
		FilterDerivFreq:   1.0,
		OledBlackMode:     true,
		HapticsEnabled:    true,
		HapticIntensity:   2,
		AutoDimSeconds:    15,
	}
}

// Manager handles thread-safe configuration management and persistence.
type Manager struct {
	mu       sync.RWMutex
	cfg      AppConfig
	filePath string
}

// NewManager loads configuration from a path or creates a default one.
func NewManager(customPath string) *Manager {
	if customPath == "" {
		home, _ := os.UserHomeDir()
		customPath = filepath.Join(home, ".touchpad", "config.json")
	}

	m := &Manager{
		cfg:      DefaultConfig(),
		filePath: customPath,
	}
	_ = m.Load()
	return m
}

// Get returns a copy of current config.
func (m *Manager) Get() AppConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

// Update modifies configuration in place and saves to disk.
func (m *Manager) Update(fn func(c *AppConfig)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.cfg)
	return m.saveLocked()
}

func (m *Manager) Load() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return json.Unmarshal(data, &m.cfg)
}

func (m *Manager) saveLocked() error {
	dir := filepath.Dir(m.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}
