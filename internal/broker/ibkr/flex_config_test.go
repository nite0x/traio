package ibkr

import (
	"fmt"
	"testing"

	"github.com/nite/traio/internal/broker"
)

func TestFlexQueryIDValidation(t *testing.T) {
	for _, value := range []string{"", "1234567", " 1234567 ", "9223372036854775807"} {
		if err := ValidateFlexQueryID(value); err != nil {
			t.Fatalf("valid ID rejected: %v", err)
		}
	}
	for _, value := range []string{"935800000000000000000000", "0", "-1", "+123", "report name", "https://example.invalid/?q=123"} {
		if err := ValidateFlexQueryID(value); err == nil {
			t.Fatal("invalid ID accepted")
		}
	}
	_, err := NewFactory().Open(t.Context(), broker.ConnectionConfig{Config: map[string]any{"connection_type": "flex", "flex_activity_query_id": "123"}, Secrets: map[string]string{"flex_token": "123"}})
	if err == nil {
		t.Fatal("token reused as query ID accepted")
	}
}

func TestWrappedFlexErrorPreservesOnlyCode(t *testing.T) {
	err := fmt.Errorf("report fetch: %w", newFlexError("1025"))
	if FlexErrorCode(err) != "1025" {
		t.Fatal("broker error code lost")
	}
	if !isRetryableFlexError(fmt.Errorf("download: %w", newFlexError("1019"))) {
		t.Fatal("retryable wrapped error lost")
	}
	if FlexErrorCode(newFlexError("token=secret")) != "" {
		t.Fatal("unsafe error code exposed")
	}
}
