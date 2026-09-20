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

func TestURLScanSourceBasic(t *testing.T) {
	response := map[string]interface{}{
		"results": []map[string]interface{}{
			{"page": map[string]string{"domain": "api.example.com"}},
			{"page": map[string]string{"domain": "mail.example.com"}},
			{"page": map[string]string{"domain": "other.com"}},      // wrong domain — filter
			{"page": map[string]string{"domain": "api.example.com"}}, // duplicate
			{"page": map[string]string{"domain": ""}},               // empty — skip
		},
	}
	body, _ := json.Marshal(response)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewURLScanSourceWithEndpoint(client, limiter, srv.URL+"?q=domain:%s&size=100")

	entities := runSource(ctx, t, src, "example.com")
	vals := entityValues(entities)

	assertContains(t, vals, "api.example.com")
	assertContains(t, vals, "mail.example.com")
	assertNotContains(t, vals, "other.com")

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

func TestURLScanSourceHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized) // 401 without API key on some endpoints
	}))
	defer srv.Close()

	ctx := context.Background()
	client, limiter, _ := newTestInfra(ctx, t)
	src := harvester.NewURLScanSourceWithEndpoint(client, limiter, srv.URL+"?q=domain:%s&size=100")

	out := make(chan core.Entity, 10)
	if err := src.Query(ctx, "example.com", out); err == nil {
		t.Error("expected error on HTTP 401")
	}
	close(out)
}
