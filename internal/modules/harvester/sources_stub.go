package harvester

import (
	"context"

	"nameless/internal/core"
)






// urlscanSource queries URLScan.io for recorded scans containing the domain.
// Endpoint: https://urlscan.io/api/v1/search/?q=domain:{domain}&size=100
// Response: JSON with results[].page.domain field.
type urlscanSource struct {
	client  *core.Client
	limiter *core.RateLimiter
}

func NewURLScanSource(client *core.Client, limiter *core.RateLimiter) Source {
	return &urlscanSource{client: client, limiter: limiter}
}

func (s *urlscanSource) Name() string { return "urlscan" }

func (s *urlscanSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	panic("urlscan source not yet implemented")
}

// dnsBruteSource resolves common subdomain names against the target domain
// using the Go standard library net.LookupHost.
//
// NOTE: This source does NOT use core.Client or core.RateLimiter because DNS
// resolution is not HTTP — it goes through the OS resolver / system DNS.
// Rate limiting is implicitly handled by goroutine concurrency limits in core.Pool.
// Context cancellation is honoured via a context-aware lookup helper.
type dnsBruteSource struct{}

func NewDNSBruteSource() Source { return &dnsBruteSource{} }

func (s *dnsBruteSource) Name() string { return "dns_brute" }

func (s *dnsBruteSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	panic("dns_brute source not yet implemented")
}
