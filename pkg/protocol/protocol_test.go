package protocol

import (
	"testing"
)

func TestPacketEncodeDecode(t *testing.T) {
	orig := Packet{
		Version:         ProtocolVersion,
		Type:            MsgScroll,
		Flags:           FlagReliable,
		SequenceNumber:  123456,
		TimestampMicros: 98765432,
		DeltaX:          -12.75,
		DeltaY:          45.125,
		Phase:           PhaseChanged,
		MomentumPhase:   MomentumContinue,
		FingerCount:     2,
		AuxValue:        -1,
	}

	buf := orig.Encode(nil)
	if len(buf) != FixedPacketSize {
		t.Fatalf("expected buffer len %d, got %d", FixedPacketSize, len(buf))
	}

	var decoded Packet
	if err := decoded.Decode(buf); err != nil {
		t.Fatalf("failed to decode packet: %v", err)
	}

	if decoded.Version != orig.Version ||
		decoded.Type != orig.Type ||
		decoded.Flags != orig.Flags ||
		decoded.SequenceNumber != orig.SequenceNumber ||
		decoded.TimestampMicros != orig.TimestampMicros ||
		decoded.DeltaX != orig.DeltaX ||
		decoded.DeltaY != orig.DeltaY ||
		decoded.Phase != orig.Phase ||
		decoded.MomentumPhase != orig.MomentumPhase ||
		decoded.FingerCount != orig.FingerCount ||
		decoded.AuxValue != orig.AuxValue {
		t.Fatalf("decoded packet mismatch: %+v vs %+v", decoded, orig)
	}
}

func TestCorruptedPackets(t *testing.T) {
	var p Packet
	// Too short
	if err := p.Decode([]byte{0x54, 0x01}); err != ErrPacketTooShort {
		t.Errorf("expected ErrPacketTooShort, got %v", err)
	}

	// Invalid magic
	buf := make([]byte, FixedPacketSize)
	buf[0] = 0x99
	buf[1] = ProtocolVersion
	if err := p.Decode(buf); err != ErrInvalidMagic {
		t.Errorf("expected ErrInvalidMagic, got %v", err)
	}

	// Unsupported version
	buf[0] = MagicByte
	buf[1] = 0x99
	if err := p.Decode(buf); err != ErrBadVersion {
		t.Errorf("expected ErrBadVersion, got %v", err)
	}
}

func TestSequenceTracker(t *testing.T) {
	st := &SequenceTracker{}

	// Move seq 10 -> pass
	p1 := &Packet{Type: MsgPointerMove, SequenceNumber: 10}
	if !st.ShouldProcess(p1) {
		t.Error("expected p1 (seq 10) to process")
	}

	// Move seq 8 (out-of-order) -> drop
	pOld := &Packet{Type: MsgPointerMove, SequenceNumber: 8}
	if st.ShouldProcess(pOld) {
		t.Error("expected pOld (seq 8) to be dropped")
	}

	// Move seq 11 -> pass
	p2 := &Packet{Type: MsgPointerMove, SequenceNumber: 11}
	if !st.ShouldProcess(p2) {
		t.Error("expected p2 (seq 11) to process")
	}

	// Reliable button up seq 5 (out-of-order) -> must still process!
	pBtn := &Packet{Type: MsgMouseButton, SequenceNumber: 5, Flags: FlagReliable}
	if !st.ShouldProcess(pBtn) {
		t.Error("expected reliable button packet to process even if out of order")
	}
}

func BenchmarkPacketEncode(b *testing.B) {
	p := &Packet{
		Version:         ProtocolVersion,
		Type:            MsgPointerMove,
		SequenceNumber:  100,
		TimestampMicros: 200,
		DeltaX:          1.5,
		DeltaY:          -2.5,
		FingerCount:     1,
	}
	buf := make([]byte, FixedPacketSize)
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		p.Encode(buf)
	}
}

func BenchmarkPacketDecode(b *testing.B) {
	p := &Packet{
		Version:         ProtocolVersion,
		Type:            MsgPointerMove,
		SequenceNumber:  100,
		TimestampMicros: 200,
		DeltaX:          1.5,
		DeltaY:          -2.5,
		FingerCount:     1,
	}
	buf := p.Encode(nil)
	var target Packet
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = target.Decode(buf)
	}
}
