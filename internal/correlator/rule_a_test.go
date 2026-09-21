package correlator_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/correlator"
)

// ------------------------------------------------------------------ helpers

func newAgg(entities ...core.Entity) *core.Aggregator {
	agg := core.NewAggregator()
	for _, e := range entities {
		agg.Add(e)
	}
	return agg
}

func crawlerEmail(addr string) core.Entity {
	return core.NewEntity(core.EntityEmail, addr, "crawler")
}

func harvesterEmail(addr string) core.Entity {
	return core.NewEntity(core.EntityEmail, addr, "harvester")
}

func platformEntity(name string) core.Entity {
	return core.NewEntity(core.EntityPlatform, name, "emailcheck")
}

// ------------------------------------------------------------------ Rule A tests

func TestRuleAEmailcheckRunsForCrawlerEmails(t *testing.T) {
	agg := newAgg(
		crawlerEmail("alice@target.com"),
		crawlerEmail("bob@target.com"),
	)
	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	called := map[string]int{}
	emailcheckFn := func(ctx context.Context, email string, out chan<- core.Entity) error {
		called[email]++
		out <- platformEntity("github.com")
		return nil
	}

	c.RunRuleA(context.Background(), emailcheckFn)

	if called["alice@target.com"] != 1 {
		t.Errorf("expected emailcheck called once for alice, got %d", called["alice@target.com"])
	}
	if called["bob@target.com"] != 1 {
		t.Errorf("expected emailcheck called once for bob, got %d", called["bob@target.com"])
	}
}

func TestRuleASkipsNonCrawlerEmails(t *testing.T) {
	agg := newAgg(
		harvesterEmail("info@target.com"),
	)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleA(context.Background(), func(ctx context.Context, email string, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 0 {
		t.Errorf("harvester emails must not trigger Rule A; called %d times", called)
	}
}

func TestRuleARelationsCreatedWithCorrectConfidence(t *testing.T) {
	email := crawlerEmail("admin@target.com")
	agg := newAgg(email)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	c.RunRuleA(context.Background(), func(ctx context.Context, e string, out chan<- core.Entity) error {
		out <- platformEntity("twitter.com")
		out <- platformEntity("github.com")
		return nil
	})

	rels := store.All()
	if len(rels) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(rels))
	}
	for _, r := range rels {
		if r.Confidence != correlator.ConfidenceInferredHigh {
			t.Errorf("Rule A confidence should be InferredHigh (0.8), got %v", r.Confidence)
		}
		if r.DiscoveredBy != "rule_a_email_emailcheck" {
			t.Errorf("unexpected DiscoveredBy: %q", r.DiscoveredBy)
		}
		if r.Type != correlator.RelEmailToRegistration {
			t.Errorf("unexpected relation type: %q", r.Type)
		}
	}
}

func TestRuleAPlatformEntitiesAddedToAggregator(t *testing.T) {
	agg := newAgg(crawlerEmail("user@example.com"))
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	c.RunRuleA(context.Background(), func(ctx context.Context, e string, out chan<- core.Entity) error {
		out <- platformEntity("linkedin.com")
		return nil
	})

	platforms := agg.ByType(core.EntityPlatform)
	if len(platforms) != 1 || platforms[0].Value != "linkedin.com" {
		t.Errorf("platform entity not added to aggregator; got %v", platforms)
	}
}

func TestRuleAEmailcheckErrorLogged(t *testing.T) {
	agg := newAgg(crawlerEmail("fail@example.com"))
	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	c.RunRuleA(context.Background(), func(ctx context.Context, e string, out chan<- core.Entity) error {
		return errors.New("connection refused")
	})

	// Error must be logged, scan must continue (no panic/return error from RunRuleA).
	if !strings.Contains(errBuf.String(), "connection refused") {
		t.Errorf("error not logged; stderr: %q", errBuf.String())
	}
	// No relations should be stored.
	if store.Len() != 0 {
		t.Errorf("no relations expected when emailcheck errors; got %d", store.Len())
	}
}

func TestRuleAContextCancellation(t *testing.T) {
	// Fill aggregator with many emails.
	agg := core.NewAggregator()
	for i := 0; i < 20; i++ {
		agg.Add(crawlerEmail("user" + string(rune('a'+i)) + "@example.com"))
	}
	store := correlator.NewRelationStore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	c := correlator.New(agg, store, &bytes.Buffer{})
	callCount := 0
	c.RunRuleA(ctx, func(ctx context.Context, e string, out chan<- core.Entity) error {
		callCount++
		return nil
	})

	// With immediate cancellation, zero or very few calls should happen.
	if callCount == 20 {
		t.Error("context cancellation should stop Rule A from processing all emails")
	}
}
