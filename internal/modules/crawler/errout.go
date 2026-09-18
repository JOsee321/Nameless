package crawler

import "os"

// setDefaultErrOut points errOut to os.Stderr.
// Called from init() in crawler.go; separated here so export_test.go can
// override errOut without touching crawler.go.
func setDefaultErrOut() {
	errOut = os.Stderr
}
