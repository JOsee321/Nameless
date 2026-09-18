// export_test.go exposes internal crawler functions and types for testing.
// Only compiled during `go test`.
package crawler

import (
	"io"
	"net/url"

	"nameless/internal/core"
)

// Re-export extractor functions so crawler_test (package crawler_test) can call them.

// ExtractLinks is the exported test shim for extractLinks.
func ExtractLinks(html []byte, base *url.URL) []string { return extractLinks(html, base) }

// ExtractJSLinks is the exported test shim for extractJSLinks.
func ExtractJSLinks(html []byte, base *url.URL) []string { return extractJSLinks(html, base) }

// ExtractEmails is the exported test shim for extractEmails.
func ExtractEmails(body []byte) []string { return extractEmails(body) }

// ExtractJSEndpoints is the exported test shim for extractJSEndpoints.
func ExtractJSEndpoints(body []byte) []string { return extractJSEndpoints(body) }

// DetectedSecret is the exported view of detectedSecret for tests.
type DetectedSecret struct {
	Pattern string
	Value   string
}

// ExtractSecrets is the exported test shim for extractSecrets.
func ExtractSecrets(body []byte) []DetectedSecret {
	raw := extractSecrets(body)
	out := make([]DetectedSecret, len(raw))
	for i, s := range raw {
		out[i] = DetectedSecret{Pattern: s.pattern, Value: s.value}
	}
	return out
}

// ParseRobotsDisallowed is the exported test shim for parseRobotsDisallowed.
func ParseRobotsDisallowed(body []byte) []string { return parseRobotsDisallowed(body) }

// SetErrOutForTest redirects crawler's errOut to w for the duration of a test.
// Returns a restore function to be deferred.
func SetErrOutForTest(w io.Writer) (restore func()) {
	old := errOut
	errOut = w
	return func() { errOut = old }
}

// Re-export core types so crawler_test (external package) can use them without
// importing core directly.
type Entity = core.Entity
