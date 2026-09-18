package core_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"nameless/internal/core"
)

func TestClientInjectsUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	opts := core.DefaultClientOptions()
	client := core.NewClient(opts)

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("Get error: %v", err)
	}
	resp.Body.Close()

	if gotUA != opts.UserAgent {
		t.Errorf("User-Agent: got %q, want %q", gotUA, opts.UserAgent)
	}
}

func TestClientRespectsCustomUserAgent(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := core.NewClient(core.DefaultClientOptions())

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("User-Agent", "custom-agent")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do error: %v", err)
	}
	resp.Body.Close()

	if gotUA != "custom-agent" {
		t.Errorf("User-Agent: got %q, want custom-agent", gotUA)
	}
}

func TestDoWithRetryRetriesOn429(t *testing.T) {
	t.Cleanup(core.SetRetryDelayForTest(time.Millisecond))

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests) // first two → 429
			return
		}
		w.WriteHeader(http.StatusOK) // third → success
	}))
	defer srv.Close()

	// Use a very short backoff so the test runs fast.
	opts := core.DefaultClientOptions()
	client := core.NewClient(opts)

	ctx := context.Background()
	resp, err := client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("DoWithRetry error: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("final status: got %d, want 200", resp.StatusCode)
	}
	if calls.Load() != 3 {
		t.Errorf("call count: got %d, want 3", calls.Load())
	}
}

func TestDoWithRetryStopsAfterMaxRetries(t *testing.T) {
	t.Cleanup(core.SetRetryDelayForTest(time.Millisecond))

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable) // always 503
	}))
	defer srv.Close()

	client := core.NewClient(core.DefaultClientOptions())
	ctx := context.Background()

	resp, err := client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// initial attempt + 3 retries = 4 total calls
	if calls.Load() != 4 {
		t.Errorf("call count: got %d, want 4", calls.Load())
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("final status: got %d, want 503", resp.StatusCode)
	}
}

func TestDoWithRetrySkipsNonRetryableStatus(t *testing.T) {
	t.Cleanup(core.SetRetryDelayForTest(time.Millisecond))

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound) // 404 — not retried
	}))
	defer srv.Close()

	client := core.NewClient(core.DefaultClientOptions())
	ctx := context.Background()

	resp, err := client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	if calls.Load() != 1 {
		t.Errorf("call count: got %d, want 1 (404 must not be retried)", calls.Load())
	}
}
