package harvester

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net"
	"strings"
	"sync"

	"nameless/internal/core"
)

// wordlistDNS is the embedded subdomain brute-force wordlist.
//
// Location note: this file lives at internal/modules/harvester/data/wordlist_dns.txt
// instead of the project-level data/ directory (where sites_username.json and
// sites_emailcheck.json live) because Go's go:embed directive does not allow
// paths that traverse upward with "../". The embedded file must reside within
// the package directory tree. This is a Go toolchain constraint, not a style
// choice — do not "fix" this by duplicating the file to data/ as that creates
// two sources of truth that will diverge silently.
//go:embed data/wordlist_dns.txt
var wordlistDNS []byte

// dnsBruteSource resolves common subdomain names against the target domain
// using the Go standard library net.Resolver.
//
// Design note: This source does NOT use core.Client or core.RateLimiter because
// DNS resolution is not HTTP — it goes through the OS stub resolver and ultimately
// to the system's configured DNS server(s). There is no per-domain rate limit
// concept at this layer; concurrency is already bounded by core.Pool. Each
// candidate is resolved in a short goroutine using a shared WaitGroup to avoid
// spawning uncapped goroutines — the pool goroutine returns only when all
// candidates are resolved or ctx is cancelled.
type dnsBruteSource struct {
	// resolver can be replaced in tests with a custom net.Resolver.
	resolver *net.Resolver
	// wordlist overrides the embedded wordlist in tests.
	wordlist []byte
}

func NewDNSBruteSource() Source {
	return &dnsBruteSource{
		resolver: net.DefaultResolver,
		wordlist: wordlistDNS,
	}
}

// newDNSBruteSourceForTest creates a dnsBruteSource with injected resolver and wordlist.
// Only called from export_test.go.
func newDNSBruteSourceForTest(resolver *net.Resolver, wordlist []byte) Source {
	return &dnsBruteSource{resolver: resolver, wordlist: wordlist}
}

func (s *dnsBruteSource) Name() string { return "dns_brute" }

// Query implements Source for DNS brute-force.
// For each word in the embedded wordlist it resolves "{word}.{domain}".
// Successful lookups (any IP returned) are emitted as EntitySubdomain entities.
// Failed lookups (NXDOMAIN, timeout, etc.) are silently ignored.
func (s *dnsBruteSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	candidates := parseWordlist(s.wordlist)
	if len(candidates) == 0 {
		return fmt.Errorf("dns_brute: empty wordlist")
	}

	var (
		mu   sync.Mutex
		seen = make(map[string]struct{})
		wg   sync.WaitGroup
		// Limit inner goroutine fan-out to avoid overwhelming the resolver.
		sem = make(chan struct{}, 50)
	)

	for _, word := range candidates {
		if ctx.Err() != nil {
			break
		}
		candidate := word + "." + domain

		wg.Add(1)
		sem <- struct{}{}
		go func(candidate string) {
			defer wg.Done()
			defer func() { <-sem }()

			addrs, err := s.resolver.LookupHost(ctx, candidate)
			if err != nil || len(addrs) == 0 {
				return
			}

			mu.Lock()
			_, dup := seen[candidate]
			if !dup {
				seen[candidate] = struct{}{}
			}
			mu.Unlock()

			if dup {
				return
			}

			select {
			case out <- SubdomainEntity(candidate, "harvester", "dns_brute"):
			case <-ctx.Done():
			}
		}(candidate)
	}

	wg.Wait()
	return nil
}

// parseWordlist splits the wordlist bytes into non-empty, non-comment lines.
func parseWordlist(data []byte) []string {
	var words []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		words = append(words, line)
	}
	return words
}
