package crypto

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	e, err := NewEncryptor(testKey())
	if err != nil {
		t.Fatalf("NewEncryptor: %v", err)
	}
	for _, in := range []string{"", "Сергей", "a longer value with unicode ✓ and spaces"} {
		blob, err := e.EncryptString(in)
		if err != nil {
			t.Fatalf("encrypt %q: %v", in, err)
		}
		got, err := e.DecryptString(blob)
		if err != nil {
			t.Fatalf("decrypt %q: %v", in, err)
		}
		if got != in {
			t.Fatalf("round trip mismatch: got %q want %q", got, in)
		}
	}
}

func TestEncryptNonceIsRandom(t *testing.T) {
	e, _ := NewEncryptor(testKey())
	a, _ := e.EncryptString("same")
	b, _ := e.EncryptString("same")
	if bytes.Equal(a, b) {
		t.Fatal("two encryptions of the same plaintext produced identical ciphertext (nonce reuse)")
	}
}

func TestEncryptNilIsNil(t *testing.T) {
	e, _ := NewEncryptor(testKey())
	blob, err := e.Encrypt(nil)
	if err != nil || blob != nil {
		t.Fatalf("Encrypt(nil) = %v, %v; want nil, nil", blob, err)
	}
	got, err := e.Decrypt(nil)
	if err != nil || got != nil {
		t.Fatalf("Decrypt(nil) = %v, %v; want nil, nil", got, err)
	}
}

func TestDecryptRejectsTampered(t *testing.T) {
	e, _ := NewEncryptor(testKey())
	blob, _ := e.EncryptString("secret")
	blob[len(blob)-1] ^= 0xff // flip a ciphertext bit
	if _, err := e.Decrypt(blob); err == nil {
		t.Fatal("expected auth failure on tampered ciphertext")
	}
}

func TestNewEncryptorRejectsShortKey(t *testing.T) {
	if _, err := NewEncryptor(make([]byte, 16)); err == nil {
		t.Fatal("expected error for 16-byte key")
	}
}

func TestBlindIndexDeterministic(t *testing.T) {
	bi, err := NewBlindIndexer(testKey())
	if err != nil {
		t.Fatalf("NewBlindIndexer: %v", err)
	}
	a := bi.Index("123456789")
	b := bi.Index("123456789")
	if !bytes.Equal(a, b) {
		t.Fatal("blind index not deterministic")
	}
	if bytes.Equal(a, bi.Index("987654321")) {
		t.Fatal("different inputs collided")
	}
	if len(a) != 32 {
		t.Fatalf("blind index len = %d, want 32", len(a))
	}
}

func TestRandomTokenUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := RandomToken(32)
		if err != nil {
			t.Fatalf("RandomToken: %v", err)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
}
