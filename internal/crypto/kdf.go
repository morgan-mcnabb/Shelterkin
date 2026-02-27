package crypto

import (
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/argon2"
)

// DeriveKey derives a 32-byte AES-256 key from a master secret and salt
// using Argon2id (1 iteration, 64MB memory, 4 threads)
func DeriveKey(masterSecret string, salt []byte) []byte {
	return argon2.IDKey([]byte(masterSecret), salt, 1, 64*1024, 4, 32)
}

func GenerateSalt() ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generating salt: %w", err)
	}
	return salt, nil
}
