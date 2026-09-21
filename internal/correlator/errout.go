package correlator

import (
	"fmt"
	"io"
)

// writeErr formats a warning message and writes it to w (typically os.Stderr).
// It is used by all rules to log non-fatal errors without stopping the correlation pass.
func writeErr(w io.Writer, format string, args ...any) {
	fmt.Fprintf(w, "[!] correlator: "+format+"\n", args...)
}
