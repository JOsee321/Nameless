package harvester_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/harvester"
)

// newTestInfra builds the shared infrastructure used by all source tests.
func newTestInfra(ctx context.Context, t *testing.T) (*core.Client, *core.RateLimiter, *core.Pool) {
	t.Helper()
	pool := core.NewPool(ctx, 4)
	t.Cleanup(func() { pool.Close() })
	return core.NewClient(core.DefaultClientOptions()),
		core.NewRateLimiter(1000),
		pool
}

func runSource(ctx context.Context, t *testing.T, src harvester.Source, domain string) []core.Entity {
	t.Helper()
	out := make(chan core.Entity, 100)
	if err := src.Query(ctx, domain, out); err != nil {
		t.Fatalf("Query returned error: %v", err)
	}
	close(out)
	var entities []core.Entity
	for e := range out {
		entities = append(entities, e)
	}
	return entities
}

func entityValues(entities []core.Entity) []string {
	var vals []string
	for _, e := range entities {
		vals = append(vals, e.Value)
	}
	return vals
}

// ------------------------------------------------------------------ crt.sh tests

func TestCrtshSourceBasic(t *testing.T) {
	// Mock crt.sh response with two entries.
	response := []map[string]string{
		{"name_value": "api.example.com"},
		{"name_value": "mail.example.com\nsmtp.example.com"},
		{"name_value": "*.example.com"},         // wildcard → stripped
		{"name_value": "other.com"},              // wrong domain → filtered
		{"name_value": "api.example.com"},        // duplicate → deduped
	}
	body, _ := json.Marshal(response)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)

	// Inject test server URL by using a custom source that overrides the endpoint.
	src := harvester.NewCrtshSourceWithEndpoint(client, limiter, srv.URL+"?q=%%.%s&output=json")
	entities := runSource(ctx, t, src, "example.com")
	vals := entityValues(entities)

	assertContains(t, vals, "api.example.com")
	assertContains(t, vals, "mail.example.com")
	assertContains(t, vals, "smtp.example.com")
	assertContains(t, vals, "example.com") // wildcard stripped to bare domain

	assertNotContains(t, vals, "other.com")
	assertNotContains(t, vals, "*.example.com")

	// Dedup: api.example.com appears only once
	count := 0
	for _, v := range vals {
		if v == "api.example.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("api.example.com should appear once, got %d; vals=%v", count, vals)
	}
}

func TestCrtshSourceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewCrtshSourceWithEndpoint(client, limiter, srv.URL+"?q=%%.%s&output=json")

	out := make(chan core.Entity, 10)
	err := src.Query(ctx, "example.com", out)
	close(out)
	if err == nil {
		t.Error("expected error on HTTP 429, got nil")
	}
}

func assertNotContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			t.Errorf("slice should NOT contain %q", want)
			return
		}
	}
}
