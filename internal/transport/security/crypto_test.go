package security

import (
	"bytes"
	"testing"
)

func TestChannelEncryptDecrypt(t *testing.T) {
	key := "SECRET_PIN_123456"
	sender, err := NewChannel(key)
	if err != nil {
		t.Fatalf("failed to create sender channel: %v", err)
	}
	receiver, err := NewChannel(key)
	if err != nil {
		t.Fatalf("failed to create receiver channel: %v", err)
	}

	payload := []byte("Hello, low-latency secure touchpad!")
	cipher, err := sender.Encrypt(payload)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	plain, err := receiver.Decrypt(cipher)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(plain, payload) {
		t.Fatalf("payload mismatch: %s vs %s", plain, payload)
	}

	// Replay attack test: second decrypt of same packet must fail
	_, err = receiver.Decrypt(cipher)
	if err != ErrReplayDetected {
		t.Fatalf("expected ErrReplayDetected on replayed packet, got %v", err)
	}

	// Tampered packet test on a fresh message
	cipher2, _ := sender.Encrypt([]byte("second message"))
	cipherTampered := make([]byte, len(cipher2))
	copy(cipherTampered, cipher2)
	cipherTampered[len(cipherTampered)-1] ^= 0x01
	_, err = receiver.Decrypt(cipherTampered)
	if err != ErrDecryptionFailed {
		t.Fatalf("expected ErrDecryptionFailed on tampered packet, got %v", err)
	}
}
