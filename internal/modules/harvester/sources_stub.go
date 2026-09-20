package harvester

import (
	"context"

	"nameless/internal/core"
)

// dnsBruteSource resolves common subdomain names against the target domain
// using the Go standard library net.LookupHost.
//
// NOTE: This source does NOT use core.Client or core.RateLimiter because DNS
// resolution is not HTTP — it goes through the OS resolver / system DNS.
// Rate limiting is implicitly handled by goroutine concurrency limits in core.Pool.
// Context cancellation is honoured via net.DefaultResolver.LookupHost with ctx.
type dnsBruteSource struct{}

func NewDNSBruteSource() Source { return &dnsBruteSource{} }

func (s *dnsBruteSource) Name() string { return "dns_brute" }

func (s *dnsBruteSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	panic("dns_brute source not yet implemented — see source_dnsbrute.go")
}
