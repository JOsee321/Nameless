package correlator_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/correlator"
	"nameless/internal/modules/crawler"
)

// fullPipelineState simulates the aggregator state after crawler + harvester
// have both run, then applies all three rules in order and verifies the outcome.
func TestFullPipelineRuleOrdering(t *testing.T) {
	// Simulate: crawler found an email, harvester found a subdomain.
	crawlerEmail := core.NewEntity(core.EntityEmail, "alice@target.com", "crawler")
	harvesterSub := core.NewEntity(core.EntitySubdomain, "api.target.com", "harvester")

	agg := core.NewAggregator()
	agg.Add(crawlerEmail)
	agg.Add(harvesterSub)

	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	// --- Rule A: crawler email → emailcheck ---
	emailcheckCallCount := 0
	c.RunRuleA(context.Background(), func(ctx context.Context, email string, out chan<- core.Entity) error {
		emailcheckCallCount++
		// Emailcheck finds a registration on github.com
		out <- core.NewEntity(core.EntityPlatform, "github.com", "emailcheck")
		return nil
	})

	if emailcheckCallCount != 1 {
		t.Errorf("Rule A: expected 1 emailcheck call, got %d", emailcheckCallCount)
	}

	// After Rule A, aggregator should have the platform entity.
	platforms := agg.ByType(core.EntityPlatform)
	if len(platforms) != 1 || platforms[0].Value != "github.com" {
		t.Errorf("Rule A: platform entity not in aggregator; got %v", platforms)
	}

	// --- Rule B: email local-parts → username (runs AFTER A) ---
	usernameCallCount := 0
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		usernameCallCount++
		if username != "alice" {
			t.Errorf("Rule B: expected username 'alice', got %q", username)
		}
		out <- core.NewEntity(core.EntityPlatform, "github.com/alice", "username")
		return nil
	})

	if usernameCallCount != 1 {
		t.Errorf("Rule B: expected 1 username call, got %d", usernameCallCount)
	}

	// --- Rule C: re-crawl harvester subdomain ---
	crawlCallCount := 0
	c.RunRuleC(context.Background(), func(ctx context.Context, targetURL string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		crawlCallCount++
		if targetURL != "https://api.target.com" {
			t.Errorf("Rule C: unexpected URL %q", targetURL)
		}
		return nil
	})

	if crawlCallCount != 1 {
		t.Errorf("Rule C: expected 1 crawl call, got %d", crawlCallCount)
	}

	// --- Verify relations ---
	rels := store.All()
	relsByType := map[correlator.RelationType]int{}
	for _, r := range rels {
		relsByType[r.Type]++
	}

	// Rule A → email_to_registration
	if relsByType[correlator.RelEmailToRegistration] < 1 {
		t.Errorf("expected at least 1 email_to_registration relation; got %v", relsByType)
	}
	// Rule B → email_to_username + username_to_profile
	if relsByType[correlator.RelEmailToUsername] < 1 {
		t.Errorf("expected at least 1 email_to_username relation; got %v", relsByType)
	}
	if relsByType[correlator.RelUsernameToProfile] < 1 {
		t.Errorf("expected at least 1 username_to_profile relation; got %v", relsByType)
	}

	// No errors logged
	if errBuf.Len() > 0 {
		t.Errorf("unexpected errors: %q", errBuf.String())
	}
}

// TestFullPipelineRuleBSeesRuleAEmails verifies that Rule B picks up emails
// added to the aggregator BY Rule A, not just the ones present before Rule A ran.
func TestFullPipelineRuleBSeesRuleAEmails(t *testing.T) {
	// Start with only a crawler email.
	crawlerEmailEnt := core.NewEntity(core.EntityEmail, "boss@company.com", "crawler")
	agg := core.NewAggregator()
	agg.Add(crawlerEmailEnt)

	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	// Rule A: emailcheck finds another email on a profile page and adds it.
	// In a real run this would happen via the emailcheck platform page scraper;
	// we simulate by directly adding to agg inside the mock.
	c.RunRuleA(context.Background(), func(ctx context.Context, email string, out chan<- core.Entity) error {
		// Simulate emailcheck discovering an alternate email
		agg.Add(core.NewEntity(core.EntityEmail, "cto@company.com", "emailcheck"))
		out <- core.NewEntity(core.EntityPlatform, "linkedin.com", "emailcheck")
		return nil
	})

	// Rule B should now see BOTH boss@company.com and cto@company.com.
	usernamesCalled := map[string]int{}
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		usernamesCalled[username]++
		return nil
	})

	if usernamesCalled["boss"] != 1 {
		t.Errorf("expected username check for 'boss', got %v", usernamesCalled)
	}
	if usernamesCalled["cto"] != 1 {
		t.Errorf("expected username check for 'cto' (added by Rule A), got %v", usernamesCalled)
	}
}

// TestFullPipelinePartialFailureIsolation verifies that a Rule A failure
// does not prevent Rule B from running on already-present emails.
func TestFullPipelinePartialFailureIsolation(t *testing.T) {
	agg := core.NewAggregator()
	agg.Add(core.NewEntity(core.EntityEmail, "alice@example.com", "crawler"))
	agg.Add(core.NewEntity(core.EntityEmail, "bob@example.com", "crawler"))

	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	// Rule A fails for both emails.
	c.RunRuleA(context.Background(), func(ctx context.Context, email string, out chan<- core.Entity) error {
		return errors.New("timeout")
	})

	// Rule B should still run for both local-parts.
	usernamesCalled := map[string]int{}
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		usernamesCalled[username]++
		return nil
	})

	if usernamesCalled["alice"] != 1 || usernamesCalled["bob"] != 1 {
		t.Errorf("Rule B should still run after Rule A failures; got %v", usernamesCalled)
	}
	if !strings.Contains(errBuf.String(), "timeout") {
		t.Errorf("Rule A errors must be logged; got: %q", errBuf.String())
	}
}

// TestFullPipelineConfidenceScores verifies the confidence tiers across all
// three rules are consistent with the documented schema.
func TestFullPipelineConfidenceScores(t *testing.T) {
	agg := core.NewAggregator()
	agg.Add(core.NewEntity(core.EntityEmail, "alice@target.com", "crawler"))
	agg.Add(core.NewEntity(core.EntitySubdomain, "api.target.com", "harvester"))

	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	c.RunRuleA(context.Background(), func(ctx context.Context, email string, out chan<- core.Entity) error {
		out <- core.NewEntity(core.EntityPlatform, "github.com", "emailcheck")
		return nil
	})
	c.RunRuleB(context.Background(), func(ctx context.Context, username string, out chan<- core.Entity) error {
		out <- core.NewEntity(core.EntityPlatform, "github.com/alice", "username")
		return nil
	})
	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		out <- core.NewEntity(core.EntityEndpoint, url+"/api/v1", "crawler")
		return nil
	})

	expected := map[correlator.RelationType]correlator.Confidence{
		correlator.RelEmailToRegistration: correlator.ConfidenceInferredHigh,
		correlator.RelEmailToUsername:     correlator.ConfidenceInferredMid,
		correlator.RelUsernameToProfile:   correlator.ConfidenceObserved,
		correlator.RelSubdomainToCrawl:    correlator.ConfidenceInferredLow,
	}

	for _, r := range store.All() {
		want, ok := expected[r.Type]
		if !ok {
			t.Errorf("unexpected relation type %q", r.Type)
			continue
		}
		if r.Confidence != want {
			t.Errorf("relation %q: confidence got %v, want %v", r.Type, r.Confidence, want)
		}
	}
}
