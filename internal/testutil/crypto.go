package testutil

import (
	"testing"

	"github.com/shelterkin/shelterkin/internal/crypto"
)

// fixed test key, exactly 32 bytes for AES-256
var testEncryptionKey = []byte("testkey-for-unit-tests-32bytes!!")

func NewTestEncryptor(t *testing.T) *crypto.Encryptor {
	t.Helper()
	enc, err := crypto.NewEncryptor(testEncryptionKey)
	if err != nil {
		t.Fatalf("creating test encryptor: %v", err)
	}
	return enc
}

func NewTestHMAC(t *testing.T) *crypto.HMACHasher {
	t.Helper()
	return crypto.NewHMAC(testEncryptionKey)
}
