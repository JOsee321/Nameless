package harvester_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/harvester"
)

// ------------------------------------------------------------------ mock source

// mockSource is a test double for Source.
type mockSource struct {
	name     string
	entities []core.Entity
	err      error
}

func (m *mockSource) Name() string { return m.name }

func (m *mockSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	for _, e := range m.entities {
		select {
		case out <- e:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.err
}

// newEntity is a convenience wrapper for tests.
func newSubdomain(value, src string) core.Entity {
	e := core.NewEntity(core.EntitySubdomain, value, "harvester")
	e.Metadata = map[string]string{"source": src}
	return e
}

// ------------------------------------------------------------------ tests

func TestOrchestratorAggregatesFromMultipleSources(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 4)
	defer pool.Close()

	sources := []harvester.Source{
		&mockSource{name: "src1", entities: []core.Entity{
			newSubdomain("api.example.com", "src1"),
			newSubdomain("mail.example.com", "src1"),
		}},
		&mockSource{name: "src2", entities: []core.Entity{
			newSubdomain("www.example.com", "src2"),
		}},
	}

	mod := harvester.New(pool, sources)

	out := make(chan core.Entity, 20)
	if err := mod.Run(ctx, "example.com", out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	close(out)

	var got []string
	for e := range out {
		got = append(got, e.Value)
	}

	assertContains(t, got, "api.example.com")
	assertContains(t, got, "mail.example.com")
	assertContains(t, got, "www.example.com")
}

func TestOrchestratorContinuesOnSourceFailure(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 4)
	defer pool.Close()

	errBuf := &bytes.Buffer{}

	sources := []harvester.Source{
		&mockSource{name: "failing", entities: nil, err: errors.New("source timed out")},
		&mockSource{name: "working", entities: []core.Entity{
			newSubdomain("sub.example.com", "working"),
		}},
	}

	mod := harvester.New(pool, sources)
	mod.SetErrOutForTest(errBuf)

	out := make(chan core.Entity, 20)
	if err := mod.Run(ctx, "example.com", out); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	close(out)

	var got []string
	for e := range out {
		got = append(got, e.Value)
	}

	// Working source result must be present.
	assertContains(t, got, "sub.example.com")

	// Failure must be logged to stderr, not swallowed.
	if !strings.Contains(errBuf.String(), "failing") {
		t.Errorf("expected failure message in stderr, got: %q", errBuf.String())
	}
}

func TestOrchestratorBothSourcesFail(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 4)
	defer pool.Close()

	errBuf := &bytes.Buffer{}

	sources := []harvester.Source{
		&mockSource{name: "s1", err: errors.New("error one")},
		&mockSource{name: "s2", err: errors.New("error two")},
	}

	mod := harvester.New(pool, sources)
	mod.SetErrOutForTest(errBuf)

	out := make(chan core.Entity, 20)
	// Should return nil (not propagate source errors up).
	if err := mod.Run(ctx, "example.com", out); err != nil {
		t.Fatalf("Run should return nil even when all sources fail, got: %v", err)
	}
	close(out)

	var got []string
	for e := range out {
		got = append(got, e.Value)
	}

	if len(got) != 0 {
		t.Errorf("expected no entities when all sources fail, got %v", got)
	}
	if !strings.Contains(errBuf.String(), "s1") || !strings.Contains(errBuf.String(), "s2") {
		t.Errorf("both failures must be logged, got: %q", errBuf.String())
	}
}

func TestOrchestratorContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	pool := core.NewPool(context.Background(), 4)
	defer pool.Close()

	called := false
	sources := []harvester.Source{
		&mockSource{name: "slow", entities: []core.Entity{
			newSubdomain("sub.example.com", "slow"),
		}},
	}
	_ = called

	mod := harvester.New(pool, sources)

	out := make(chan core.Entity, 20)
	// Should not panic or deadlock with a cancelled context.
	_ = mod.Run(ctx, "example.com", out)
	close(out)
}

// ------------------------------------------------------------------ helpers

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("expected %q in %v", want, slice)
}
