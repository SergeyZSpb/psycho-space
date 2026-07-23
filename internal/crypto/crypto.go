// Package crypto provides the symmetric primitives used to keep personal data
// out of the database in plaintext:
//
//   - Encryptor    — AES-256-GCM authenticated encryption for at-rest fields.
//   - BlindIndexer — deterministic HMAC-SHA256 index for equality lookups
//     (allowlist / dedup) without storing the plaintext identifier.
//   - HMACSHA256 / RandomToken — session-token hashing and generation.
//
// All randomness comes from crypto/rand. Keys must be exactly 32 bytes.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// ErrCiphertextTooShort is returned when a blob is smaller than the GCM nonce.
var ErrCiphertextTooShort = errors.New("crypto: ciphertext too short")

// Encryptor performs AES-256-GCM encryption. The AEAD is built once and reused.
type Encryptor struct {
	aead cipher.AEAD
}

// NewEncryptor builds an Encryptor from a 32-byte key (AES-256).
func NewEncryptor(key []byte) (*Encryptor, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("crypto: enc key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Encryptor{aead: aead}, nil
}

// Encrypt returns nonce||ciphertext with a fresh random nonce per call.
// A nil plaintext encrypts to nil so optional fields stay NULL in the DB.
func (e *Encryptor) Encrypt(plaintext []byte) ([]byte, error) {
	if plaintext == nil {
		return nil, nil
	}
	nonce := make([]byte, e.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return e.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// EncryptString is Encrypt for a string. An empty string still encrypts (to a
// non-nil blob); pass a nil slice via Encrypt when you want NULL.
func (e *Encryptor) EncryptString(s string) ([]byte, error) {
	return e.Encrypt([]byte(s))
}

// Decrypt reverses Encrypt. A nil blob decrypts to nil.
func (e *Encryptor) Decrypt(blob []byte) ([]byte, error) {
	if blob == nil {
		return nil, nil
	}
	ns := e.aead.NonceSize()
	if len(blob) < ns {
		return nil, ErrCiphertextTooShort
	}
	nonce, ct := blob[:ns], blob[ns:]
	return e.aead.Open(nil, nonce, ct, nil)
}

// DecryptString is Decrypt returning a string.
func (e *Encryptor) DecryptString(blob []byte) (string, error) {
	b, err := e.Decrypt(blob)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// BlindIndexer produces a deterministic keyed hash for equality lookups.
type BlindIndexer struct {
	key []byte
}

// NewBlindIndexer builds a BlindIndexer. The key should be >= 32 bytes.
func NewBlindIndexer(key []byte) (*BlindIndexer, error) {
	if len(key) < 32 {
		return nil, fmt.Errorf("crypto: hmac key must be >= 32 bytes, got %d", len(key))
	}
	return &BlindIndexer{key: key}, nil
}

// Index returns HMAC-SHA256(key, value) — stable across calls for the same value.
func (b *BlindIndexer) Index(value string) []byte {
	return HMACSHA256(b.key, []byte(value))
}

// HMACSHA256 returns HMAC-SHA256(key, msg).
func HMACSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// RandomToken returns a URL-safe base64 string carrying nBytes of entropy.
func RandomToken(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
