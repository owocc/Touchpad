package main

import (
	"net"
	"testing"
	"time"

	"touchpad/internal/config"
	"touchpad/internal/host"
	"touchpad/internal/inject"
	"touchpad/pkg/protocol"
)

func TestMeasurementPingPong(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UDPPort = 39880
	cfg.ADBPort = 39881
	cfg.DiscoveryPort = 39882

	inj, err := inject.New()
	if err != nil {
		t.Fatalf("inj error: %v", err)
	}

	server, err := host.NewServer(&cfg, inj)
	if err != nil {
		t.Fatalf("server error: %v", err)
	}
	if err := server.Start(); err != nil {
		t.Fatalf("start error: %v", err)
	}
	defer server.Stop()

	conn, err := net.Dial("udp", "127.0.0.1:39880")
	if err != nil {
		t.Fatalf("dial error: %v", err)
	}
	defer conn.Close()

	pkt := protocol.Packet{
		Version:         protocol.ProtocolVersion,
		Type:            protocol.MsgPing,
		SequenceNumber:  1,
		TimestampMicros: uint32(time.Now().UnixMicro()),
	}

	data := pkt.Encode(nil)
	if _, err := conn.Write(data); err != nil {
		t.Fatalf("write error: %v", err)
	}

	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}

	var resp protocol.Packet
	if err := resp.Decode(buf[:n]); err != nil {
		t.Fatalf("decode resp error: %v", err)
	}

	if resp.Type != protocol.MsgPong {
		t.Fatalf("expected MsgPong, got %v", resp.Type)
	}
}
