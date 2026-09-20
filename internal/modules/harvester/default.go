package harvester

import (
	"nameless/internal/core"
)

// NewDefaultModule constructs the Module with all production sources.
// Sources are added incrementally as each is implemented; see harvester.go
// for the complete source list.
func NewDefaultModule(
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
) *Module {
	sources := []Source{
		NewCrtshSource(client, limiter),
		NewHackerTargetSource(client, limiter),
		NewAnubisSource(client, limiter),
		NewURLScanSource(client, limiter),
		NewDNSBruteSource(),
	}
	return New(pool, sources)
}
