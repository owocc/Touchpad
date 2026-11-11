package adb

import (
	"testing"
)

func TestADBListDevices(t *testing.T) {
	mgr := NewManager()
	devs, err := mgr.ListDevices()
	if err != nil {
		t.Logf("adb not available or failed: %v (skipping hardware test)", err)
		return
	}
	t.Logf("Found %d devices: %+v", len(devs), devs)
}
