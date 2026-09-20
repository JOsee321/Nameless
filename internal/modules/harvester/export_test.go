// export_test.go exposes internal harvester types for black-box testing.
// Only compiled during `go test`.
package harvester

import (
	"io"
	"net"
)

// SetErrOutForTest redirects the Module's error output during tests.
func (m *Module) SetErrOutForTest(w io.Writer) {
	m.errOut = w
}

// NewDNSBruteSourceForTest creates a dnsBruteSource with an injected resolver
// and wordlist for unit testing without real DNS queries.
func NewDNSBruteSourceForTest(resolver *net.Resolver, wordlist []byte) Source {
	return newDNSBruteSourceForTest(resolver, wordlist)
}

// ParseWordlistForTest exposes parseWordlist for unit testing.
func ParseWordlistForTest(data []byte) []string {
	return parseWordlist(data)
}
