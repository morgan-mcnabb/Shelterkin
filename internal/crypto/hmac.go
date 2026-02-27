package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

type HMACHasher struct {
	key []byte
}

func NewHMAC(key []byte) *HMACHasher {
	return &HMACHasher{key: key}
}

func (h *HMACHasher) Hash(plaintext string) string {
	mac := hmac.New(sha256.New, h.key)
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}
