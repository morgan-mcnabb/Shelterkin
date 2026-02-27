package crypto

import (
	"testing"
)

func testKey() []byte {
	return []byte("test-key-that-is-exactly-32bytes")
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	enc, err := NewEncryptor(testKey())
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	tests := []struct {
		name      string
		plaintext string
	}{
		{"simple text", "hello world"},
		{"empty string", ""},
		{"unicode", "こんにちは世界 🌍"},
		{"long text", "Lorem ipsum dolor sit amet, consectetur adipiscing elit. Sed do eiusmod tempor incididunt ut labore et dolore magna aliqua."},
		{"special characters", `<script>alert("xss")</script> & "quotes" 'single'`},
		{"medical data", "Patient has type 2 diabetes, prescribed Metformin 500mg twice daily"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := enc.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("encrypt failed: %v", err)
			}

			if encrypted == tt.plaintext && tt.plaintext != "" {
				t.Error("encrypted text should differ from plaintext")
			}

			decrypted, err := enc.Decrypt(encrypted)
			if err != nil {
				t.Fatalf("decrypt failed: %v", err)
			}

			if decrypted != tt.plaintext {
				t.Errorf("round trip failed: got %q, want %q", decrypted, tt.plaintext)
			}
		})
	}
}

func TestEncryptProducesDifferentCiphertexts(t *testing.T) {
	enc, err := NewEncryptor(testKey())
	if err != nil {
		t.Fatalf("failed to create encryptor: %v", err)
	}

	plaintext := "same input"
	a, _ := enc.Encrypt(plaintext)
	b, _ := enc.Encrypt(plaintext)

	if a == b {
		t.Error("two encryptions of the same plaintext should produce different ciphertexts (random nonce)")
	}
}

func TestDecryptWithWrongKey(t *testing.T) {
	enc1, _ := NewEncryptor(testKey())
	enc2, _ := NewEncryptor([]byte("different-key-exactly-32-bytes!!"))

	encrypted, _ := enc1.Encrypt("secret data")

	_, err := enc2.Decrypt(encrypted)
	if err == nil {
		t.Error("decryption with wrong key should fail")
	}
}

func TestNewEncryptorInvalidKeyLength(t *testing.T) {
	_, err := NewEncryptor([]byte("too-short"))
	if err == nil {
		t.Error("expected error for invalid key length")
	}
}

func TestDeriveKeyProducesConsistentOutput(t *testing.T) {
	salt := []byte("test-salt-16byte")
	key1 := DeriveKey("my-secret", salt)
	key2 := DeriveKey("my-secret", salt)

	if len(key1) != 32 {
		t.Errorf("expected 32-byte key, got %d", len(key1))
	}

	for i := range key1 {
		if key1[i] != key2[i] {
			t.Error("same secret and salt should produce same key")
			break
		}
	}
}

func TestDeriveKeyDifferentSecrets(t *testing.T) {
	salt := []byte("test-salt-16byte")
	key1 := DeriveKey("secret-a", salt)
	key2 := DeriveKey("secret-b", salt)

	same := true
	for i := range key1 {
		if key1[i] != key2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("different secrets should produce different keys")
	}
}

func TestGenerateSalt(t *testing.T) {
	salt1, err := GenerateSalt()
	if err != nil {
		t.Fatalf("failed to generate salt: %v", err)
	}
	if len(salt1) != 16 {
		t.Errorf("expected 16-byte salt, got %d", len(salt1))
	}

	salt2, _ := GenerateSalt()
	same := true
	for i := range salt1 {
		if salt1[i] != salt2[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("two generated salts should differ")
	}
}

func TestHMACDeterministic(t *testing.T) {
	h := NewHMAC(testKey())

	hash1 := h.Hash("test@example.com")
	hash2 := h.Hash("test@example.com")

	if hash1 != hash2 {
		t.Error("same input should produce same HMAC hash")
	}

	if len(hash1) != 64 {
		t.Errorf("expected 64-char hex string, got %d chars", len(hash1))
	}
}

func TestHMACDifferentInputs(t *testing.T) {
	h := NewHMAC(testKey())

	hash1 := h.Hash("alice@example.com")
	hash2 := h.Hash("bob@example.com")

	if hash1 == hash2 {
		t.Error("different inputs should produce different HMAC hashes")
	}
}

func TestHMACDifferentKeys(t *testing.T) {
	h1 := NewHMAC([]byte("key-one"))
	h2 := NewHMAC([]byte("key-two"))

	hash1 := h1.Hash("same-input")
	hash2 := h2.Hash("same-input")

	if hash1 == hash2 {
		t.Error("different keys should produce different HMAC hashes")
	}
}
