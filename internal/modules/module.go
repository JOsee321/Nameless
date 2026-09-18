// Package modules defines the common interface that all scan modules must implement.
//
// Putting the Module interface here rather than in core/ keeps the dependency
// graph clean: core/ is pure infrastructure (HTTP, rate limiting, worker pool),
// while modules/ owns the domain-level contract.  If Module lived in core/,
// every infrastructure type would implicitly become aware of scan semantics.
package modules

import (
	"context"

	"nameless/internal/core"
)

// Module is the contract every scan module (crawler, username, harvester,
// emailcheck) must satisfy.  The orchestrator and correlator call Run on each
// module and collect the resulting entities.
type Module interface {
	// Name returns a stable, lowercase identifier used in log output and the
	// SourceModule field of each Entity (e.g. "username", "crawler").
	Name() string

	// Run executes the module against the given target string (domain, username,
	// or email depending on the module) and streams discovered entities into out.
	// It MUST respect ctx cancellation and close out when it is done.
	Run(ctx context.Context, target string, out chan<- core.Entity) error
}
