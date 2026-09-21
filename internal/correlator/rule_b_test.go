package correlator_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/correlator"
)

// ------------------------------------------------------------------ helper for Rule B tests

func anyEmail(addr string) core.Entity {
	return core.NewEntity(core.EntityEmail, addr, "harvester")
}

// ------------------------------------------------------------------ Rule B unit tests

func TestRuleBRunsForValidLocalParts(t *testing.T) {
	agg := newAgg(anyEmail("johndoe@example.com"))
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := map[string]int{}
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		called[username]++
		return nil
	})

	if called["johndoe"] != 1 {
		t.Errorf("expected username check for 'johndoe', got %v", called)
	}
}

func TestRuleBSkipsGenericLocalParts(t *testing.T) {
	generics := []string{
		"admin@example.com",
		"info@example.com",
		"support@example.com",
		"noreply@example.com",
		"no-reply@example.com",
		"contact@example.com",
		"sales@example.com",
	}
	var entities []core.Entity
	for _, addr := range generics {
		entities = append(entities, anyEmail(addr))
	}
	agg := newAgg(entities...)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 0 {
		t.Errorf("generic local-parts should be skipped, got %d calls", called)
	}
}

func TestRuleBSkipsInvalidLocalParts(t *testing.T) {
	// Too short (< 3 chars), too long, or weird characters
	invalids := []string{
		"ab@example.com",           // too short (2 chars)
		"x@example.com",            // too short (1 char)
		"<script>@example.com",     // special chars
	}
	var entities []core.Entity
	for _, addr := range invalids {
		entities = append(entities, anyEmail(addr))
	}
	agg := newAgg(entities...)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 0 {
		t.Errorf("invalid local-parts should be skipped, got %d calls", called)
	}
}

func TestRuleBDeduplicatesLocalParts(t *testing.T) {
	// Same local-part from different domains — should only check once.
	agg := newAgg(
		anyEmail("alice@example.com"),
		anyEmail("alice@other.com"),
		anyEmail("alice@third.com"),
	)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 1 {
		t.Errorf("same local-part from different domains should only run once, got %d", called)
	}
}

func TestRuleBRelationConfidences(t *testing.T) {
	agg := newAgg(anyEmail("alice@example.com"))
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		out <- core.NewEntity(core.EntityPlatform, "github.com/alice", "username")
		return nil
	})

	rels := store.All()
	if len(rels) != 2 {
		t.Fatalf("expected 2 relations (email→username, username→profile), got %d", len(rels))
	}

	relsByType := map[correlator.RelationType]correlator.Confidence{}
	for _, r := range rels {
		relsByType[r.Type] = r.Confidence
	}

	if relsByType[correlator.RelEmailToUsername] != correlator.ConfidenceInferredMid {
		t.Errorf("email→username should be InferredMid (0.6), got %v",
			relsByType[correlator.RelEmailToUsername])
	}
	if relsByType[correlator.RelUsernameToProfile] != correlator.ConfidenceObserved {
		t.Errorf("username→profile should be Observed (1.0), got %v",
			relsByType[correlator.RelUsernameToProfile])
	}
}

func TestRuleBSkipsSentinelEntities(t *testing.T) {
	// Ensure "::emailchecked" sentinel entities from Rule A are not processed.
	agg := newAgg(
		core.NewEntity(core.EntityEmail, "alice@example.com::emailchecked", "correlator"),
	)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 0 {
		t.Errorf("sentinel entities should be skipped, got %d calls", called)
	}
}

// ------------------------------------------------------------------ IsGenericLocalPart / IsValidLocalPart unit tests

func TestIsGenericLocalPart(t *testing.T) {
	generics := []string{"admin", "info", "support", "noreply", "sales", "webmaster"}
	for _, g := range generics {
		if !correlator.IsGenericLocalPart(g) {
			t.Errorf("expected %q to be generic", g)
		}
	}
	nonGenerics := []string{"johndoe", "alice", "bob123", "j.smith"}
	for _, ng := range nonGenerics {
		if correlator.IsGenericLocalPart(ng) {
			t.Errorf("expected %q to NOT be generic", ng)
		}
	}
}

func TestIsValidLocalPart(t *testing.T) {
	valids := []string{"johndoe", "alice123", "j.smith", "bob-dev", "user_name"}
	for _, v := range valids {
		if !correlator.IsValidLocalPart(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	invalids := []string{"ab", "x", "<script>", "user name", "a" + strings.Repeat("x", 30)}
	for _, inv := range invalids {
		if correlator.IsValidLocalPart(inv) {
			t.Errorf("expected %q to be invalid", inv)
		}
	}
}
