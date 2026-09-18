// export_test.go exposes internal knobs that tests need to control.
// This file is only compiled during testing (package core_test cannot
// access unexported symbols, but a file in package core can).
package core

import "time"

// SetRetryDelayForTest overrides the backoff seed used by DoWithRetry.
// Call it in a test's t.Cleanup to restore the original value.
func SetRetryDelayForTest(d time.Duration) (restore func()) {
	old := retryDelay
	retryDelay = d
	return func() { retryDelay = old }
}
