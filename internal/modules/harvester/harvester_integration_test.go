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

// ------------------------------------------------------------------ integration: full orchestrator with all 5 mock sources

func TestOrchestratorFiveSourcesPartialFailure(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 8)
	defer pool.Close()

	errBuf := &bytes.Buffer{}

	sources := []harvester.Source{
		// 3 working sources with no overlap
		&mockSource{name: "crt.sh", entities: []core.Entity{
			newSubdomain("api.example.com", "crt.sh"),
			newSubdomain("mail.example.com", "crt.sh"),
		}},
		&mockSource{name: "hackertarget", entities: []core.Entity{
			newSubdomain("dev.example.com", "hackertarget"),
			newSubdomain("mail.example.com", "hackertarget"), // duplicate across sources
		}},
		&mockSource{name: "anubis", entities: []core.Entity{
			newSubdomain("staging.example.com", "anubis"),
		}},
		// 1 source that returns an email (ensures mixed entity types work)
		&mockSource{name: "urlscan", entities: []core.Entity{
			newEmail("admin@example.com", "urlscan"),
		}},
		// 1 source that fails completely
		&mockSource{name: "dns_brute", entities: nil, err: errors.New("resolver timeout")},
	}

	mod := harvester.New(pool, sources)
	mod.SetErrOutForTest(errBuf)

	out := make(chan core.Entity, 100)
	if err := mod.Run(ctx, "example.com", out); err != nil {
		t.Fatalf("Run returned error (should be nil even on partial failure): %v", err)
	}
	close(out)

	// Collect by type
	var subdomains, emails []string
	for e := range out {
		switch e.Type {
		case core.EntitySubdomain:
			subdomains = append(subdomains, e.Value)
		case core.EntityEmail:
			emails = append(emails, e.Value)
		}
	}

	// Working sources must contribute their results
	assertContains(t, subdomains, "api.example.com")
	assertContains(t, subdomains, "mail.example.com")
	assertContains(t, subdomains, "dev.example.com")
	assertContains(t, subdomains, "staging.example.com")
	assertContains(t, emails, "admin@example.com")

	// Failed source must be logged
	if !strings.Contains(errBuf.String(), "dns_brute") {
		t.Errorf("failure of dns_brute source not logged to stderr; got: %q", errBuf.String())
	}
	if !strings.Contains(errBuf.String(), "resolver timeout") {
		t.Errorf("error message not in stderr; got: %q", errBuf.String())
	}
}

func TestOrchestratorDeduplicatesAcrossSources(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 4)
	defer pool.Close()

	// Same subdomain from 3 different sources — orchestrator emits all 3,
	// deduplication happens at the Aggregator level (not inside Module.Run).
	// This test verifies that Module.Run does NOT silently drop cross-source dupes
	// (that's the Aggregator's job, not the harvester's).
	sources := []harvester.Source{
		&mockSource{name: "src1", entities: []core.Entity{newSubdomain("www.example.com", "src1")}},
		&mockSource{name: "src2", entities: []core.Entity{newSubdomain("www.example.com", "src2")}},
		&mockSource{name: "src3", entities: []core.Entity{newSubdomain("www.example.com", "src3")}},
	}

	mod := harvester.New(pool, sources)

	out := make(chan core.Entity, 20)
	_ = mod.Run(ctx, "example.com", out)
	close(out)

	count := 0
	for range out {
		count++
	}
	// Module passes all 3 through; Aggregator deduplicates on Add.
	if count != 3 {
		t.Errorf("expected 3 entities from 3 sources (before Aggregator dedup), got %d", count)
	}
}

func TestOrchestratorAllSourcesSucceed(t *testing.T) {
	ctx := context.Background()
	pool := core.NewPool(ctx, 8)
	defer pool.Close()

	errBuf := &bytes.Buffer{}

	sources := []harvester.Source{
		&mockSource{name: "a", entities: []core.Entity{newSubdomain("a.example.com", "a")}},
		&mockSource{name: "b", entities: []core.Entity{newSubdomain("b.example.com", "b")}},
		&mockSource{name: "c", entities: []core.Entity{newSubdomain("c.example.com", "c")}},
		&mockSource{name: "d", entities: []core.Entity{newSubdomain("d.example.com", "d")}},
		&mockSource{name: "e", entities: []core.Entity{newSubdomain("e.example.com", "e")}},
	}

	mod := harvester.New(pool, sources)
	mod.SetErrOutForTest(errBuf)

	out := make(chan core.Entity, 20)
	_ = mod.Run(ctx, "example.com", out)
	close(out)

	var vals []string
	for e := range out {
		vals = append(vals, e.Value)
	}

	for _, expected := range []string{
		"a.example.com", "b.example.com", "c.example.com",
		"d.example.com", "e.example.com",
	} {
		assertContains(t, vals, expected)
	}

	// No failures — stderr must be empty
	if errBuf.Len() > 0 {
		t.Errorf("unexpected stderr output: %q", errBuf.String())
	}
}

// ------------------------------------------------------------------ helpers used only in this file

func newEmail(value, src string) core.Entity {
	e := core.NewEntity(core.EntityEmail, value, "harvester")
	e.Metadata = map[string]string{"source": src}
	return e
}
