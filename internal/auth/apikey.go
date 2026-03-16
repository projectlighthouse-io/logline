package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const keyPrefix = "ll_"

func GenerateKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}

	encoded := base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(bytes)

	return keyPrefix + encoded, nil
}

func HashKey(rawKey string) string {
	hash := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("%x", hash)
}

func ExtractPrefix(rawKey string) string {
	if len(rawKey) < 8 {
		return rawKey
	}
	return rawKey[:8]
}
