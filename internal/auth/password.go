package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/crypto/argon2"
)

var ErrInvalidCredentials = errors.New("invalid username or password")

const (
	passwordMemory      = 64 * 1024
	passwordIterations  = 3
	passwordParallelism = 2
	passwordSaltLength  = 16
	passwordKeyLength   = 32
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._@-]*$`)

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func validateBootstrapCredential(username, password string) error {
	if len(username) < 3 || len(username) > 64 || !usernamePattern.MatchString(username) {
		return fmt.Errorf("bootstrap username must be 3-64 lowercase letters, digits, or . _ @ -")
	}
	if len(password) < 12 {
		return fmt.Errorf("bootstrap password must contain at least 12 characters")
	}
	if len(password) > 1024 {
		return fmt.Errorf("bootstrap password is too long")
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return encodePassword(password, salt, passwordMemory, passwordIterations, passwordParallelism), nil
}

func encodePassword(password string, salt []byte, memory, iterations uint32, parallelism uint8) string {
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, passwordKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, iterations uint32
	var parallelism uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &parallelism); err != nil {
		return false
	}
	if memory < 8*1024 || memory > 256*1024 || iterations < 1 || iterations > 10 || parallelism < 1 || parallelism > 8 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) < 16 || len(want) > 64 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func dummyPasswordHash() string {
	return encodePassword("not-the-password", []byte("traio-login-dummy"), passwordMemory, passwordIterations, passwordParallelism)
}
