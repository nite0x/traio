package runtime

import (
	"os"
	"testing"
)

func TestLoadOrCreateAPITokenCreatesPrivateStableToken(t *testing.T) {
	t.Setenv("TRAIO_API_TOKEN", "")
	dir := t.TempDir()

	first, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || second != first {
		t.Fatalf("expected stable 64-character token, got lengths %d and %d", len(first), len(second))
	}
	info, err := os.Stat(APITokenPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected token mode 0600, got %o", got)
	}
}

func TestLoadOrCreateAPITokenUsesEnvironment(t *testing.T) {
	const token = "environment-token-0123456789abcdef"
	t.Setenv("TRAIO_API_TOKEN", token)
	dir := t.TempDir()

	got, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != token {
		t.Fatalf("expected environment token, got %q", got)
	}
	fromDisk, err := ReadAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fromDisk != token {
		t.Fatalf("expected mirrored token, got %q", fromDisk)
	}
}
