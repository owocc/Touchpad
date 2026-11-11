package lan

import (
	"testing"
	"time"
)

func TestDiscoveryService(t *testing.T) {
	discPort := 39182
	hostPort := 39180
	adbPort := 39181

	service := NewDiscoveryService(discPort, hostPort, adbPort, false)
	if err := service.Start(); err != nil {
		t.Fatalf("failed to start discovery service: %v", err)
	}
	defer service.Stop()

	// Wait briefly for socket to listen
	time.Sleep(50 * time.Millisecond)

	hosts, err := DiscoverHosts(discPort, 300*time.Millisecond)
	if err != nil {
		t.Fatalf("DiscoverHosts failed: %v", err)
	}

	if len(hosts) == 0 {
		t.Fatal("expected to discover at least 1 local host")
	}

	found := false
	for _, h := range hosts {
		if h.Port == hostPort && h.ADBPort == adbPort {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("did not find expected host in results: %+v", hosts)
	}
}
