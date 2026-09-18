package username_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/username"
)

// writeSites serialises sites to a temp JSON file and returns its path.
func writeSites(t *testing.T, sites []username.SiteDefinition) string {
	t.Helper()
	data, err := json.Marshal(sites)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "sites.json")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// newInfra builds the shared infrastructure components used by each test.
func newInfra(ctx context.Context) (*core.Client, *core.RateLimiter, *core.Pool) {
	client := core.NewClient(core.DefaultClientOptions())
	limiter := core.NewRateLimiter(1000) // effectively unlimited in tests
	pool := core.NewPool(ctx, 4)
	return client, limiter, pool
}

// collect drains the entity channel until it is closed, returning all entities.
func collect(out <-chan core.Entity) []core.Entity {
	var results []core.Entity
	for e := range out {
		results = append(results, e)
	}
	return results
}

// ------------------------------------------------------------------ tests

func TestStatusCodeDetectionFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:    "TestSite",
		URL:     srv.URL + "/{}",
		URLMain: srv.URL,
		Detection: username.Detection{
			Type:        "status_code",
			PresentCode: 200,
			AbsentCode:  404,
		},
		Request: username.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, err := username.New(writeSites(t, sites), client, limiter, pool)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan core.Entity, 10)
	if err := mod.Run(ctx, "testuser", out); err != nil {
		t.Fatal(err)
	}
	close(out)

	results := collect(out)
	if len(results) != 1 {
		t.Fatalf("expected 1 entity, got %d", len(results))
	}
	if results[0].Metadata["status"] != "found" {
		t.Errorf("expected status=found, got %q", results[0].Metadata["status"])
	}
}

func TestStatusCodeDetectionNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:    "TestSite",
		URL:     srv.URL + "/{}",
		URLMain: srv.URL,
		Detection: username.Detection{
			Type:        "status_code",
			PresentCode: 200,
			AbsentCode:  404,
		},
		Request: username.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "nobody", out)
	close(out)

	if len(collect(out)) != 0 {
		t.Error("expected no entities for 404 response")
	}
}

func TestBodyTextFoundPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"testuser","active":true}`))
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:    "BodyTextSite",
		URL:     srv.URL + "/{}",
		URLMain: srv.URL,
		Detection: username.Detection{
			Type:         "body_text",
			FoundPattern: `"id":"{}"`,
		},
		Request: username.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "testuser", out)
	close(out)

	results := collect(out)
	if len(results) != 1 || results[0].Metadata["status"] != "found" {
		t.Errorf("expected 1 found entity; got %+v", results)
	}
}

func TestBodyTextErrorPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("This account doesn't exist"))
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:    "ErrorPatternSite",
		URL:     srv.URL + "/{}",
		URLMain: srv.URL,
		Detection: username.Detection{
			Type:         "body_text",
			ErrorPattern: "account doesn't exist",
		},
		Request: username.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "nobody", out)
	close(out)

	// Error pattern matched → user NOT found → no entity emitted.
	if len(collect(out)) != 0 {
		t.Error("expected no entities when error_pattern is matched")
	}
}

func TestUsernameRegexSkip(t *testing.T) {
	// Server should never be called because "ab cd" fails the regex.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:          "RegexSite",
		URL:           srv.URL + "/{}",
		URLMain:       srv.URL,
		Detection:     username.Detection{Type: "status_code", PresentCode: 200},
		UsernameRegex: `^[a-zA-Z0-9_]+$`, // no spaces allowed
		Request:       username.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "ab cd", out) // space → should be filtered
	close(out)

	if called {
		t.Error("HTTP server was called despite username failing regex filter")
	}
}

func TestMultipleSitesConcurrent(t *testing.T) {
	// Two servers: first returns 200, second returns 404.
	srv200 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv200.Close()

	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv404.Close()

	sites := []username.SiteDefinition{
		{
			Name: "Present", URL: srv200.URL + "/{}",
			Detection: username.Detection{Type: "status_code", PresentCode: 200, AbsentCode: 404},
			Request:   username.RequestCfg{Method: "GET"},
		},
		{
			Name: "Absent", URL: srv404.URL + "/{}",
			Detection: username.Detection{Type: "status_code", PresentCode: 200, AbsentCode: 404},
			Request:   username.RequestCfg{Method: "GET"},
		},
	}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "alice", out)
	close(out)

	results := collect(out)
	if len(results) != 1 {
		t.Errorf("expected 1 found entity, got %d", len(results))
	}
	if results[0].Value != "Present" {
		t.Errorf("wrong site found: %q", results[0].Value)
	}
}

func TestRequiresSessionSkip(t *testing.T) {
	// Server must never be reached for a requires_session site.
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []username.SiteDefinition{{
		Name:            "SessionSite",
		URL:             srv.URL + "/{}",
		URLMain:         srv.URL,
		Detection:       username.Detection{Type: "status_code", PresentCode: 200},
		Request:         username.RequestCfg{Method: "GET"},
		RequiresSession: true,
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := username.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "alice", out)
	close(out)

	if called {
		t.Error("HTTP server was called despite requires_session=true")
	}
	if len(collect(out)) != 0 {
		t.Error("expected no entities for requires_session site")
	}
}
