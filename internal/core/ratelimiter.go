package core

import (
	"context"
	"net/url"
	"sync"

	"golang.org/x/time/rate"
)

// RateLimiter manages a per-domain token-bucket limiter.
// Each unique hostname gets its own *rate.Limiter; all other hostnames
// share the same rate limit so a slow or restrictive domain never blocks
// progress on other targets.
type RateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	rps      rate.Limit // requests per second applied to each new domain
	burst    int        // burst size — allows short bursts without waiting
}

// NewRateLimiter creates a RateLimiter that allows rps requests per second
// per domain. burst is set to rps so a fresh domain can fire up to rps
// requests immediately before the steady-state limit kicks in.
func NewRateLimiter(rps int) *RateLimiter {
	if rps < 1 {
		rps = 1
	}
	return &RateLimiter{
		limiters: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    rps,
	}
}

// forHost returns the limiter for the given hostname, creating one if needed.
func (rl *RateLimiter) forHost(host string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	l, ok := rl.limiters[host]
	if !ok {
		l = rate.NewLimiter(rl.rps, rl.burst)
		rl.limiters[host] = l
	}
	return l
}

// Wait blocks until the rate limiter for the host extracted from rawURL
// grants permission, or until ctx is cancelled.
// If rawURL cannot be parsed, Wait returns immediately without blocking.
func (rl *RateLimiter) Wait(ctx context.Context, rawURL string) error {
	host := hostFromURL(rawURL)
	if host == "" {
		return nil
	}
	return rl.forHost(host).Wait(ctx)
}

// hostFromURL extracts the hostname from a raw URL string.
// Returns an empty string on parse error.
func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL // fall back to raw string as key if URL is unparseable
	}
	return u.Hostname()
}
