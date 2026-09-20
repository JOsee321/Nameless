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

func TestAnubisSourceBasic(t *testing.T) {
	subdomains := []string{
		"api.example.com",
		"mail.example.com",
		"",                  // empty — skip
		"other.com",         // wrong domain — filter
		"api.example.com",   // duplicate — dedup
	}
	body, _ := json.Marshal(subdomains)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewAnubisSourceWithEndpoint(client, limiter, srv.URL+"/%s")

	entities := runSource(ctx, t, src, "example.com")
	vals := entityValues(entities)

	assertContains(t, vals, "api.example.com")
	assertContains(t, vals, "mail.example.com")
	assertNotContains(t, vals, "other.com")
	assertNotContains(t, vals, "")

	count := 0
	for _, v := range vals {
		if v == "api.example.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("api.example.com should appear once, got %d", count)
	}
}

func TestAnubisSourceInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewAnubisSourceWithEndpoint(client, limiter, srv.URL+"/%s")

	out := make(chan core.Entity, 10)
	if err := src.Query(ctx, "example.com", out); err == nil {
		t.Error("expected error on invalid JSON")
	}
	close(out)
}
