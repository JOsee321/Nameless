package correlator_test

import (
	"testing"
	"time"

	"nameless/internal/core"
	"nameless/internal/correlator"
)

func makeEntity(t core.EntityType, value string) core.Entity {
	return core.NewEntity(t, value, "test")
}

func TestNewRelationStableID(t *testing.T) {
	from := makeEntity(core.EntityEmail, "user@example.com")
	to := makeEntity(core.EntityPlatform, "github.com")

	r1 := correlator.NewRelation(correlator.RelEmailToRegistration, from, to, correlator.ConfidenceInferredHigh, "rule_a")
	r2 := correlator.NewRelation(correlator.RelEmailToRegistration, from, to, correlator.ConfidenceInferredHigh, "rule_a")

	if r1.ID != r2.ID {
		t.Errorf("same inputs must produce same relation ID; got %q and %q", r1.ID, r2.ID)
	}
}

func TestNewRelationDistinctForDifferentTypes(t *testing.T) {
	from := makeEntity(core.EntityEmail, "user@example.com")
	to := makeEntity(core.EntityUsername, "user")

	rA := correlator.NewRelation(correlator.RelEmailToRegistration, from, to, correlator.ConfidenceInferredHigh, "rule_a")
	rB := correlator.NewRelation(correlator.RelEmailToUsername, from, to, correlator.ConfidenceInferredMid, "rule_b")

	if rA.ID == rB.ID {
		t.Error("different relation types must produce different IDs")
	}
}

func TestNewRelationFields(t *testing.T) {
	from := makeEntity(core.EntityEmail, "admin@target.com")
	to := makeEntity(core.EntityUsername, "admin")

	before := time.Now()
	r := correlator.NewRelation(correlator.RelEmailToUsername, from, to, correlator.ConfidenceInferredMid, "rule_b")
	after := time.Now()

	if r.FromID != from.ID {
		t.Errorf("FromID: got %q, want %q", r.FromID, from.ID)
	}
	if r.ToID != to.ID {
		t.Errorf("ToID: got %q, want %q", r.ToID, to.ID)
	}
	if r.FromValue != "admin@target.com" {
		t.Errorf("FromValue: got %q", r.FromValue)
	}
	if r.ToValue != "admin" {
		t.Errorf("ToValue: got %q", r.ToValue)
	}
	if r.Confidence != correlator.ConfidenceInferredMid {
		t.Errorf("Confidence: got %v, want %v", r.Confidence, correlator.ConfidenceInferredMid)
	}
	if r.DiscoveredBy != "rule_b" {
		t.Errorf("DiscoveredBy: got %q", r.DiscoveredBy)
	}
	if r.Timestamp.Before(before) || r.Timestamp.After(after) {
		t.Error("Timestamp should be set at construction time")
	}
}

func TestRelationStoreDedup(t *testing.T) {
	from := makeEntity(core.EntityEmail, "x@example.com")
	to := makeEntity(core.EntityPlatform, "github.com")

	store := correlator.NewRelationStore()
	r := correlator.NewRelation(correlator.RelEmailToRegistration, from, to, correlator.ConfidenceObserved, "rule_a")

	store.Add(r)
	store.Add(r) // duplicate
	store.Add(r) // duplicate again

	if store.Len() != 1 {
		t.Errorf("duplicate relations should be deduplicated; got %d", store.Len())
	}
}

func TestRelationStoreAll(t *testing.T) {
	e1 := makeEntity(core.EntityEmail, "a@example.com")
	e2 := makeEntity(core.EntityEmail, "b@example.com")
	platform := makeEntity(core.EntityPlatform, "github.com")

	store := correlator.NewRelationStore()
	store.Add(correlator.NewRelation(correlator.RelEmailToRegistration, e1, platform, correlator.ConfidenceInferredHigh, "rule_a"))
	store.Add(correlator.NewRelation(correlator.RelEmailToRegistration, e2, platform, correlator.ConfidenceInferredHigh, "rule_a"))

	all := store.All()
	if len(all) != 2 {
		t.Errorf("expected 2 relations, got %d", len(all))
	}
}

func TestConfidenceTierValues(t *testing.T) {
	// Ensure the tier ordering is correct and values haven't been swapped.
	tiers := []struct {
		name string
		val  correlator.Confidence
	}{
		{"Observed", correlator.ConfidenceObserved},
		{"InferredHigh", correlator.ConfidenceInferredHigh},
		{"InferredMid", correlator.ConfidenceInferredMid},
		{"InferredLow", correlator.ConfidenceInferredLow},
	}
	for i := 1; i < len(tiers); i++ {
		if tiers[i].val >= tiers[i-1].val {
			t.Errorf("confidence tier ordering broken: %s (%v) >= %s (%v)",
				tiers[i].name, tiers[i].val,
				tiers[i-1].name, tiers[i-1].val)
		}
	}
}
