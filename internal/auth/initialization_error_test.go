package auth

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nite/traio/internal/store"
)

type failingInitializationRepository struct {
	store.AuthRepository
	err error
}

func (r failingInitializationRepository) HasPasswordIdentity(context.Context) (bool, error) {
	return false, r.err
}

func TestInitializationErrorMessageRedactsDependencyErrors(t *testing.T) {
	cause := errors.New("postgres://user:private-password@host/db")
	_, err := NewService(t.Context(), failingInitializationRepository{err: cause}, Config{Mode: ModePassword})
	if !errors.Is(err, cause) {
		t.Fatal("dependency cause was lost")
	}
	wrapped := fmt.Errorf("sensitive-wrapper: %w", err)
	if got := InitializationErrorMessage(wrapped); got != "cannot check built-in account; check database permissions and schema" {
		t.Fatalf("unexpected diagnostic: %s", got)
	}
	if got := InitializationErrorMessage(cause); got != "initialization failed; check authentication configuration and initial administrator credentials" {
		t.Fatalf("unknown error was not redacted: %s", got)
	}
}
