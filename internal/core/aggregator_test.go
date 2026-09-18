package core_test

import (
	"testing"

	"nameless/internal/core"
)

func TestEntityIDStable(t *testing.T) {
	e1 := core.NewEntity(core.EntityEmail, "foo@example.com", "crawler")
	e2 := core.NewEntity(core.EntityEmail, "foo@example.com", "harvester")

	// Same type+value must always produce the same ID regardless of source.
	if e1.ID != e2.ID {
		t.Errorf("ID mismatch: %q != %q", e1.ID, e2.ID)
	}
}

func TestEntityIDDistinct(t *testing.T) {
	e1 := core.NewEntity(core.EntityUsername, "johndoe", "username")
	e2 := core.NewEntity(core.EntityEmail, "johndoe", "username")

	// Same value but different type must yield different IDs.
	if e1.ID == e2.ID {
		t.Errorf("expected distinct IDs for different types, got %q for both", e1.ID)
	}
}

func TestAggregatorDedup(t *testing.T) {
	agg := core.NewAggregator()

	e := core.NewEntity(core.EntityDomain, "example.com", "crawler")
	agg.Add(e)
	agg.Add(e) // duplicate — same ID

	if agg.Len() != 1 {
		t.Errorf("Len: got %d, want 1 after duplicate add", agg.Len())
	}
}

func TestAggregatorFirstWriteWins(t *testing.T) {
	agg := core.NewAggregator()

	first := core.NewEntity(core.EntityEmail, "a@b.com", "crawler")
	second := core.NewEntity(core.EntityEmail, "a@b.com", "harvester")

	agg.Add(first)
	agg.Add(second)

	all := agg.All()
	if len(all) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(all))
	}
	if all[0].SourceModule != "crawler" {
		t.Errorf("first-write-wins failed: source=%q, want crawler", all[0].SourceModule)
	}
}

func TestAggregatorConcurrentAdd(t *testing.T) {
	agg := core.NewAggregator()
	done := make(chan struct{})

	for i := 0; i < 100; i++ {
		go func(n int) {
			agg.Add(core.NewEntity(core.EntityUsername, "user", "username"))
			if n == 99 {
				close(done)
			}
		}(i)
	}
	<-done

	// All 100 goroutines write the same entity — result must be exactly 1.
	if agg.Len() != 1 {
		t.Errorf("concurrent dedup: got %d, want 1", agg.Len())
	}
}
