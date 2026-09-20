package harvester_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/harvester"
)

func TestHackerTargetSourceBasic(t *testing.T) {
	response := "api.example.com,1.2.3.4\nmail.example.com,5.6.7.8\nother.com,9.10.11.12\napi.example.com,1.2.3.4\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewHackerTargetSourceWithEndpoint(client, limiter, srv.URL+"?q=%s")

	entities := runSource(ctx, t, src, "example.com")
	vals := entityValues(entities)

	assertContains(t, vals, "api.example.com")
	assertContains(t, vals, "mail.example.com")
	assertNotContains(t, vals, "other.com") // wrong domain — filtered

	count := 0
	for _, v := range vals {
		if v == "api.example.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("api.example.com should appear once (deduped), got %d", count)
	}
}

func TestHackerTargetSourceAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("error check your api key or plan limits"))
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewHackerTargetSourceWithEndpoint(client, limiter, srv.URL+"?q=%s")

	out := make(chan core.Entity, 10)
	if err := src.Query(ctx, "example.com", out); err == nil {
		t.Error("expected error when API returns error message, got nil")
	}
	close(out)
}

func TestHackerTargetSourceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewHackerTargetSourceWithEndpoint(client, limiter, srv.URL+"?q=%s")

	out := make(chan core.Entity, 10)
	if err := src.Query(ctx, "example.com", out); err == nil {
		t.Error("expected error on HTTP 503")
	}
	close(out)
}
