package market

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const encryptedSecretPrefix = "enc:v1:"

// SecretCipher derives a separate encryption key from Traio's protected,
// stable runtime API token. Ciphertexts can be stored in the existing
// write-only secrets_json column without persisting the provider token.
type SecretCipher struct {
	aead cipher.AEAD
}

func NewSecretCipher(runtimeSecret string) (*SecretCipher, error) {
	if len(strings.TrimSpace(runtimeSecret)) < 32 {
		return nil, errors.New("runtime secret must contain at least 32 characters")
	}
	key := sha256.Sum256([]byte("traio/market-data-credentials/v1\x00" + runtimeSecret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize credential cipher: %w", err)
	}
	return &SecretCipher{aead: aead}, nil
}

func (c *SecretCipher) Encrypt(plaintext, purpose string) (string, error) {
	if c == nil || c.aead == nil {
		return "", errors.New("credential cipher is not configured")
	}
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("encrypt credential: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(purpose))
	return encryptedSecretPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (c *SecretCipher) Decrypt(ciphertext, purpose string) (string, error) {
	if c == nil || c.aead == nil {
		return "", errors.New("credential cipher is not configured")
	}
	if ciphertext == "" {
		return "", nil
	}
	if !strings.HasPrefix(ciphertext, encryptedSecretPrefix) {
		return "", errors.New("credential is not encrypted")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, encryptedSecretPrefix))
	if err != nil || len(raw) < c.aead.NonceSize() {
		return "", errors.New("credential ciphertext is invalid")
	}
	nonce, sealed := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, sealed, []byte(purpose))
	if err != nil {
		return "", errors.New("credential cannot be decrypted")
	}
	return string(plaintext), nil
}
