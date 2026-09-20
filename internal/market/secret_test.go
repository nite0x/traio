package market

import (
	"strings"
	"testing"
)

func TestSecretCipherEncryptsWithPurposeBinding(t *testing.T) {
	cipher, err := NewSecretCipher(strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	const secret = "tiingo-test-token-never-log"
	encrypted, err := cipher.Encrypt(secret, "tiingo/api_token")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == secret || strings.Contains(encrypted, secret) {
		t.Fatal("ciphertext contains plaintext token")
	}
	decrypted, err := cipher.Decrypt(encrypted, "tiingo/api_token")
	if err != nil || decrypted != secret {
		t.Fatalf("decrypt: value=%q err=%v", decrypted, err)
	}
	if _, err := cipher.Decrypt(encrypted, "other/provider"); err == nil {
		t.Fatal("purpose mismatch must fail closed")
	}
}
