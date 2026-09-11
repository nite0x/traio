package auth

import "errors"

// initializationError separates a safe startup diagnosis from a dependency
// error that may contain credentials, URLs or database values.
type initializationError struct {
	message string
	cause   error
}

func (e *initializationError) Error() string {
	if e.cause != nil {
		return e.message + ": " + e.cause.Error()
	}
	return e.message
}

func (e *initializationError) Unwrap() error { return e.cause }

// InitializationErrorMessage returns only locally authored startup diagnostics.
func InitializationErrorMessage(err error) string {
	var failure *initializationError
	if errors.As(err, &failure) {
		return failure.message
	}
	return "initialization failed; check authentication configuration and initial administrator credentials"
}
