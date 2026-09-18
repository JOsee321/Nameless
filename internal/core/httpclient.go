// Package core provides shared infrastructure used by all scan modules.
package core

import (
	"crypto/tls"
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
