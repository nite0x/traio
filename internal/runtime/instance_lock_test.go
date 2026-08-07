package runtime

import "testing"

func TestInstanceLockIsExclusiveAndRecoverable(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if _, err := AcquireInstanceLock(dir); err == nil {
		t.Fatal("expected second instance lock to be rejected")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := AcquireInstanceLock(dir)
	if err != nil {
		t.Fatalf("expected lock to be reusable after release: %v", err)
	}
	defer second.Close()
}
