package ibkr

import (
	"errors"
	"strconv"
	"strings"
)

// ValidateFlexQueryID distinguishes a report template ID from a Flex token.
// Empty values are permitted for optional query fields; callers enforce which
// query is required. Never echo the supplied value in errors.
func ValidateFlexQueryID(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return errors.New("invalid_flex_query_id")
		}
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return errors.New("invalid_flex_query_id")
	}
	return nil
}
