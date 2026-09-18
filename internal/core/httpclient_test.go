package core_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
