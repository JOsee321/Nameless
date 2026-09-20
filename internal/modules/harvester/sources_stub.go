package harvester

import (
	"context"

	"nameless/internal/core"
)

// sources_stub.go is now empty — all sources have real implementations.
// The file is kept as a placeholder in case temporary stubs are needed
// during incremental development.

// Ensure the package compiles even if all sources are fully implemented.
var _ Source = (*dummySource)(nil)

type dummySource struct{}

func (d *dummySource) Name() string { return "" }
func (d *dummySource) Query(_ context.Context, _ string, _ chan<- core.Entity) error {
	return nil
}
