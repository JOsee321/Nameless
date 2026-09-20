// Package harvester implements passive subdomain and email harvesting (ex-theHarvester).
//
// Architecture:
//   - Each data source implements the Source interface.
//   - The Module orchestrates all sources in parallel via the shared core.Pool.
//   - Results are streamed into a chan core.Entity; source failures are logged to
//     stderr and do not abort the scan.
//
// Sources implemented in v1 (no API keys required):
//   - crt.sh          — certificate transparency log JSON API
//   - HackerTarget    — passive DNS text API
//   - AnubisDB        — subdomain aggregator JSON API
//   - URLScan.io      — scan result search JSON API
//   - DNS brute-force — Go net.LookupHost + embedded wordlist (not HTTP)
//
// Sources explicitly excluded from v1:
//   - Shodan, Hunter.io  — require paid API keys
//   - LinkedIn            — requires authenticated session
//   - Google/Bing dork   — aggressive anti-bot, high false-positive rate
//   - ThreatCrowd        — deprecated, unreliable uptime
package harvester

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"nameless/internal/core"
)

// ------------------------------------------------------------------ Source interface

// Source is the plugin interface that every harvester data source must implement.
// All HTTP-based sources must use core.Client.DoWithRetry and core.RateLimiter.
// Non-HTTP sources (e.g. DNS) are exempt from core.Client but must still respect
// ctx cancellation.
type Source interface {
	// Name returns a short identifier used in logging and Entity.SourceModule.
	Name() string
	// Query fetches intelligence for domain and writes Entities into out.
	// It must return when ctx is cancelled.  Partial results written before
	// cancellation are kept.
	Query(ctx context.Context, domain string, out chan<- core.Entity) error
}

// ------------------------------------------------------------------ Module

// Module runs all registered sources and aggregates their results.
type Module struct {
	sources []Source
	pool    *core.Pool
	errOut  io.Writer // where source failure messages are written
}

// New creates a Module with the provided sources wired to the shared pool.
// Use NewDefaultModule to get the full production source list.
func New(pool *core.Pool, sources []Source) *Module {
	return &Module{pool: pool, sources: sources, errOut: os.Stderr}
}

// Name returns the module identifier.
func (m *Module) Name() string { return "harvester" }

// Run queries all sources concurrently.  Source failures are logged to stderr;
// they do not stop the scan.  Run blocks until all sources finish or ctx is
// cancelled.
func (m *Module) Run(ctx context.Context, domain string, out chan<- core.Entity) error {
	var wg sync.WaitGroup

	for _, src := range m.sources {
		src := src
		wg.Add(1)
		if err := m.pool.Submit(ctx, func(ctx context.Context) {
			defer wg.Done()
			if err := src.Query(ctx, domain, out); err != nil {
				fmt.Fprintf(m.errOut,
					"[!] harvester source %q failed for %q: %v\n",
					src.Name(), domain, err)
			}
		}); err != nil {
			// Pool rejected submission (ctx cancelled or pool closed).
			wg.Done()
		}
	}

	wg.Wait()
	return nil
}

// ------------------------------------------------------------------ entity helpers

// SubdomainEntity creates an EntitySubdomain for a discovered subdomain.
func SubdomainEntity(subdomain, sourceModule, sourceName string) core.Entity {
	e := core.NewEntity(core.EntitySubdomain, subdomain, sourceModule)
	e.Metadata = map[string]string{"source": sourceName}
	return e
}

// EmailEntity creates an EntityEmail for a discovered email address.
func EmailEntity(email, sourceModule, sourceName string) core.Entity {
	e := core.NewEntity(core.EntityEmail, email, sourceModule)
	e.Metadata = map[string]string{"source": sourceName}
	return e
}
