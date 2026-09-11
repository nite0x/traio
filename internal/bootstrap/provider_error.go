package bootstrap

import "errors"

// providerDiagnostic contains only locally generated, safe diagnostic text.
// Never convert a raw SDK error, response body or URL into this type.
type providerDiagnostic string

func (e providerDiagnostic) Error() string { return string(e) }

func providerFailureSummary(err error) string {
	var diagnostic providerDiagnostic
	if errors.As(err, &diagnostic) {
		return diagnostic.Error()
	}
	return "configuration provider unavailable"
}
