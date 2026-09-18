// Package core provides shared infrastructure used by all scan modules.
package core

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ClientOptions configures the shared HTTP client.
type ClientOptions struct {
	// Timeout is applied to every request via the Request context.
	Timeout time.Duration
	// MaxIdleConnsPerHost controls keep-alive connection pool size per host.
	MaxIdleConnsPerHost int
	// UserAgent is sent in the User-Agent header on every request.
	UserAgent string
	// ProxyURL, if non-nil, routes all requests through the given proxy.
	ProxyURL *url.URL
}

// DefaultClientOptions returns options matching the PRD defaults.
func DefaultClientOptions() ClientOptions {
	return ClientOptions{
		Timeout:             10 * time.Second,
		MaxIdleConnsPerHost: 50,
		UserAgent:           "Mozilla/5.0 (compatible; Nameless/0.1; +https://github.com/nameless)",
	}
}

// Client wraps http.Client with a fixed User-Agent and convenience methods.
// A single Client instance is shared across all scan modules to maximise
// connection reuse and avoid per-request TLS handshake overhead.
type Client struct {
	http      *http.Client
	userAgent string
}

// NewClient constructs a Client from the given options.
// HTTP/2 is negotiated transparently via TLS ALPN when the server supports it.
func NewClient(opts ClientOptions) *Client {
	transport := &http.Transport{
		// Keep connections alive between requests to the same host.
		MaxIdleConnsPerHost:   opts.MaxIdleConnsPerHost,
		MaxIdleConns:          opts.MaxIdleConnsPerHost * 10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		// Allow HTTP/2 via ALPN.
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: true,
	}

	if opts.ProxyURL != nil {
		transport.Proxy = http.ProxyURL(opts.ProxyURL)
	}

	return &Client{
		http:      &http.Client{Timeout: opts.Timeout, Transport: transport},
		userAgent: opts.UserAgent,
	}
}

// Do executes req, injecting the shared User-Agent header if not already set.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return c.http.Do(req)
}

// Get is a convenience wrapper that builds a GET request and calls Do.
func (c *Client) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Underlying returns the wrapped *http.Client for callers that need direct
// access (e.g. cookie jars or redirect policies).
func (c *Client) Underlying() *http.Client {
	return c.http
}

// retryDelay is the initial backoff duration; it doubles on each retry.
// Exported as a var (not const) so tests can shrink it to milliseconds.
var retryDelay = time.Second

// retryableStatus reports whether an HTTP status code warrants a retry.
// Only transient server-side errors are retried — not client errors or 404s.
func retryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || // 429
		code == http.StatusBadGateway || // 502
		code == http.StatusServiceUnavailable // 503
}

// DoWithRetry executes the request produced by makeReq and retries up to 3
// times on 429/502/503 responses with exponential backoff (1s → 2s → 4s).
//
// makeReq is called once per attempt so callers can build a fresh
// *http.Request each time — this is required for POST requests whose bodies
// are consumed on first read and cannot be rewound.
//
// On non-retryable errors or status codes the response is returned immediately.
// If ctx is cancelled during a backoff sleep, the function returns ctx.Err().
func (c *Client) DoWithRetry(ctx context.Context, makeReq func() (*http.Request, error)) (*http.Response, error) {
	const maxRetries = 3
	delay := retryDelay

	for attempt := 0; ; attempt++ {
		req, err := makeReq()
		if err != nil {
			return nil, fmt.Errorf("build request (attempt %d): %w", attempt+1, err)
		}

		resp, err := c.Do(req)
		if err != nil {
			// Network-level errors are not retried — they usually indicate the
			// host is unreachable or the context was cancelled.
			return nil, err
		}

		if attempt < maxRetries && retryableStatus(resp.StatusCode) {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
				delay *= 2
				continue
			}
		}

		return resp, nil
	}
}
