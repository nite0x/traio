package runtime

import (
	"os"
	"testing"
)

func TestAPIURLRoundTrip(t *testing.T) {
	runtimeDir := t.TempDir()
	const want = "http://127.0.0.1:45678"
	if err := WriteAPIURL(runtimeDir, want); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadAPIURL(runtimeDir); err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	info, err := os.Stat(APIURLPath(runtimeDir))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	RemoveAPIURL(runtimeDir)
	if _, err := os.Stat(APIURLPath(runtimeDir)); !os.IsNotExist(err) {
		t.Fatalf("endpoint file still exists: %v", err)
	}
}

func TestWriteAPIURLRejectsNonOrigin(t *testing.T) {
	if err := WriteAPIURL(t.TempDir(), "https://example.test/path"); err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestResolveAPIBaseUsesPublishedLocalAddress(t *testing.T) {
	runtimeDir := t.TempDir()
	if err := WriteAPIURL(runtimeDir, "http://127.0.0.1:45678"); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveAPIBase(runtimeDir); err != nil || got != "http://127.0.0.1:45678" {
		t.Fatalf("got %q, %v", got, err)
	}
	t.Setenv("TRAIO_API", "https://api.traio.example.com/")
	if got, err := ResolveAPIBase(runtimeDir); err != nil || got != "https://api.traio.example.com" {
		t.Fatalf("got %q, %v", got, err)
	}
}
