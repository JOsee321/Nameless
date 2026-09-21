package correlator_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/correlator"
	"nameless/internal/modules/crawler"
)

func harvesterSubdomain(sub string) core.Entity {
	return core.NewEntity(core.EntitySubdomain, sub, "harvester")
}

func userSubdomain(sub string) core.Entity {
	return core.NewEntity(core.EntitySubdomain, sub, "user")
}

func makeCrawlFn(results map[string][]core.Entity, errMap map[string]error) func(context.Context, string, crawler.CrawlerOptions, chan<- core.Entity) error {
	return func(ctx context.Context, targetURL string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		if err, ok := errMap[targetURL]; ok {
			return err
		}
		for _, e := range results[targetURL] {
			out <- e
		}
		return nil
	}
}

func TestRuleCResCrawlsHarvesterSubdomains(t *testing.T) {
	sd := harvesterSubdomain("api.example.com")
	agg := newAgg(sd)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := map[string]int{}
	crawlFn := makeCrawlFn(nil, nil)
	wrappedCrawl := func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		called[url]++
		return crawlFn(ctx, url, opts, out)
	}

	c.RunRuleC(context.Background(), wrappedCrawl)

	if called["https://api.example.com"] != 1 {
		t.Errorf("expected 1 crawl call for api.example.com, got %v", called)
	}
}

func TestRuleCSkipsNonHarvesterSubdomains(t *testing.T) {
	agg := newAgg(userSubdomain("www.example.com"))
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	called := 0
	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called != 0 {
		t.Errorf("non-harvester subdomains should not be re-crawled, got %d calls", called)
	}
}

func TestRuleCHardCap(t *testing.T) {
	agg := core.NewAggregator()
	for i := 0; i < 15; i++ {
		agg.Add(harvesterSubdomain("sub" + string(rune('a'+i)) + ".example.com"))
	}
	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	called := 0
	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		called++
		return nil
	})

	if called > 10 {
		t.Errorf("hard cap of 10 should be enforced, got %d crawl calls", called)
	}
	// Cap warning must be logged.
	if !strings.Contains(errBuf.String(), "capped") {
		t.Errorf("cap warning not logged; stderr: %q", errBuf.String())
	}
}

func TestRuleCHardCrawlOptionsEnforced(t *testing.T) {
	agg := newAgg(harvesterSubdomain("api.example.com"))
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	var capturedOpts crawler.CrawlerOptions
	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		capturedOpts = opts
		return nil
	})

	if capturedOpts.MaxDepth != 1 {
		t.Errorf("MaxDepth should be 1, got %d", capturedOpts.MaxDepth)
	}
	if capturedOpts.MaxPages != 20 {
		t.Errorf("MaxPages should be 20, got %d", capturedOpts.MaxPages)
	}
	if !capturedOpts.StayOnDomain {
		t.Error("StayOnDomain should be true")
	}
}

func TestRuleCRelationsHaveCorrectConfidence(t *testing.T) {
	sd := harvesterSubdomain("api.example.com")
	agg := newAgg(sd)
	store := correlator.NewRelationStore()
	c := correlator.New(agg, store, &bytes.Buffer{})

	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		out <- core.NewEntity(core.EntityEmail, "found@api.example.com", "crawler")
		return nil
	})

	rels := store.All()
	if len(rels) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(rels))
	}
	if rels[0].Confidence != correlator.ConfidenceInferredLow {
		t.Errorf("Rule C confidence should be InferredLow (0.5), got %v", rels[0].Confidence)
	}
	if rels[0].Type != correlator.RelSubdomainToCrawl {
		t.Errorf("unexpected relation type: %q", rels[0].Type)
	}
}

func TestRuleCCrawlErrorLogged(t *testing.T) {
	agg := newAgg(harvesterSubdomain("api.example.com"))
	store := correlator.NewRelationStore()
	errBuf := &bytes.Buffer{}
	c := correlator.New(agg, store, errBuf)

	c.RunRuleC(context.Background(), func(ctx context.Context, url string, opts crawler.CrawlerOptions, out chan<- core.Entity) error {
		return context.DeadlineExceeded
	})

	if !strings.Contains(errBuf.String(), "api.example.com") {
		t.Errorf("crawl error not logged; stderr: %q", errBuf.String())
	}
}
