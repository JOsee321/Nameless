// export_test.go exposes internal harvester types for black-box testing.
// Only compiled during `go test`.
package harvester

import "io"

// SetErrOutForTest redirects the Module's error output during tests.
func (m *Module) SetErrOutForTest(w io.Writer) {
	m.errOut = w
}
