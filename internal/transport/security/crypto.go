package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"sync"
)

var (
	ErrDecryptionFailed = errors.New("security: decryption or authentication failed")
	ErrReplayDetected   = errors.New("security: replayed or outdated packet nonce")
)

// Channel provides authenticated encryption (AES-256-GCM) with replay protection.
type Channel struct {
	mu        sync.Mutex
	aead      cipher.AEAD
	sendNonce uint64
	recvNonce uint64
}

// NewChannel creates an encrypted channel from a shared secret key (any string / passphrase).
func NewChannel(secretKey string) (*Channel, error) {
	// Derive 32-byte key using SHA-256
	hash := sha256.Sum256([]byte(secretKey))
	block, err := aes.NewCipher(hash[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	return &Channel{
		aead: aead,
	}, nil
}

// Encrypt encrypts plaintext using an incrementing nonce counter.
// Output layout: 12-byte Nonce + Ciphertext + 16-byte Auth Tag.
func (c *Channel) Encrypt(plaintext []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sendNonce++
	nonce := make([]byte, c.aead.NonceSize())
	binary.LittleEndian.PutUint64(nonce[0:8], c.sendNonce)

	ciphertext := c.aead.Seal(nonce, nonce, plaintext, nil)
	return ciphertext, nil
}

// Decrypt verifies auth tag and decrypts ciphertext.
// Rejects any nonce <= last seen nonce to protect against replay attacks.
func (c *Channel) Decrypt(data []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	nonceSize := c.aead.NonceSize()
	if len(data) < nonceSize {
		return nil, ErrDecryptionFailed
	}

	nonce := data[:nonceSize]
	ciphertext := data[nonceSize:]

	nonceVal := binary.LittleEndian.Uint64(nonce[0:8])
	if nonceVal <= c.recvNonce && c.recvNonce != 0 {
		return nil, ErrReplayDetected
	}

	plaintext, err := c.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	c.recvNonce = nonceVal
	return plaintext, nil
}

// GenerateRandomKey generates a cryptographic random token string.
func GenerateRandomKey(byteLen int) (string, error) {
	b := make([]byte, byteLen)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, byteLen)
	for i, v := range b {
		out[i] = chars[int(v)%len(chars)]
	}
	return string(out), nil
}
