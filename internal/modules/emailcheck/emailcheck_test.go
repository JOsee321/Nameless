package emailcheck_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/emailcheck"
)

func writeSites(t *testing.T, sites []emailcheck.SiteDefinition) string {
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

func newInfra(ctx context.Context) (*core.Client, *core.RateLimiter, *core.Pool) {
	return core.NewClient(core.DefaultClientOptions()),
		core.NewRateLimiter(1000),
		core.NewPool(ctx, 4)
}

func collect(out <-chan core.Entity) []core.Entity {
	var res []core.Entity
	for e := range out {
		res = append(res, e)
	}
	return res
}

// ------------------------------------------------------------------ tests

func TestStatusCodeFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name:      "StatusSite",
		URL:       srv.URL + "/reset",
		Detection: emailcheck.Detection{Type: "status_code", PresentCode: 200, AbsentCode: 404},
		Request:   emailcheck.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, err := emailcheck.New(writeSites(t, sites), client, limiter, pool)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "test@example.com", out)
	close(out)

	results := collect(out)
	if len(results) != 1 || results[0].Metadata["status"] != "found" {
		t.Errorf("expected 1 found; got %+v", results)
	}
}

func TestBodyTextFoundPatternWithEmailSubstitution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"registered":true,"email":"test@example.com"}`))
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name:      "BodySite",
		URL:       srv.URL + "/check",
		Detection: emailcheck.Detection{Type: "body_text", FoundPattern: `"email":"{}"`},
		Request:   emailcheck.RequestCfg{Method: "GET"},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := emailcheck.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "test@example.com", out)
	close(out)

	if results := collect(out); len(results) != 1 || results[0].Metadata["status"] != "found" {
		t.Errorf("expected 1 found; got %+v", results)
	}
}

func TestQueryParamsSubstitution(t *testing.T) {
	var gotEmail string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotEmail = r.URL.Query().Get("email")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name:      "QuerySite",
		URL:       srv.URL + "/lookup",
		Detection: emailcheck.Detection{Type: "status_code", PresentCode: 200},
		Request: emailcheck.RequestCfg{
			Method: "GET",
			Params: map[string]string{"email": "{}"},
		},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := emailcheck.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "probe@test.com", out)
	close(out)

	if gotEmail != "probe@test.com" {
		t.Errorf("query param email: got %q, want probe@test.com", gotEmail)
	}
}

func TestPostBodySubstitution(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"sent":true}`))
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name: "PostSite",
		URL:  srv.URL + "/password/reset",
		Detection: emailcheck.Detection{
			Type:         "body_text",
			FoundPattern: `"sent":true`,
		},
		Request: emailcheck.RequestCfg{
			Method:      "POST",
			Body:        `email={}`,
			ContentType: "application/x-www-form-urlencoded",
		},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := emailcheck.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "user@domain.com", out)
	close(out)

	if gotBody != "email=user@domain.com" {
		t.Errorf("POST body: got %q, want email=user@domain.com", gotBody)
	}
	results := collect(out)
	if len(results) != 1 || results[0].Metadata["status"] != "found" {
		t.Errorf("expected 1 found; got %+v", results)
	}
}

func TestPostBodyJSONSubstitution(t *testing.T) {
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"not_found":true}`))
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name: "JSONPostSite",
		URL:  srv.URL + "/api/reset",
		Detection: emailcheck.Detection{
			Type:         "body_text",
			ErrorPattern: `"not_found":true`,
		},
		Request: emailcheck.RequestCfg{
			Method:      "POST",
			Body:        `{"email":"{}"}`,
			ContentType: "application/json",
		},
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := emailcheck.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "nobody@test.com", out)
	close(out)

	if gotCT != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", gotCT)
	}
	// error_pattern matched → not found → no entity
	if len(collect(out)) != 0 {
		t.Error("expected no entities when error_pattern is matched")
	}
}

func TestRequiresSessionSkipped(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sites := []emailcheck.SiteDefinition{{
		Name:            "SessionSite",
		URL:             srv.URL + "/reset",
		Detection:       emailcheck.Detection{Type: "status_code", PresentCode: 200},
		Request:         emailcheck.RequestCfg{Method: "GET"},
		RequiresSession: true,
	}}

	ctx := context.Background()
	client, limiter, pool := newInfra(ctx)
	defer pool.Close()

	mod, _ := emailcheck.New(writeSites(t, sites), client, limiter, pool)

	out := make(chan core.Entity, 10)
	_ = mod.Run(ctx, "x@y.com", out)
	close(out)

	if called {
		t.Error("server was called despite requires_session=true")
	}
}
