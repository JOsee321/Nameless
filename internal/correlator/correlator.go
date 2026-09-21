// Package correlator cross-references entities discovered by all modules.
//
// Design: the correlator runs as a single-pass post-processing step after all
// base modules (crawler, harvester) have completed. It reads from the shared
// Aggregator, applies three correlation rules in sequence (A → B → C), and
// writes results back into the Aggregator and a RelationStore.
//
// Correlator is only active in --mode full. Standalone modes (--mode crawl,
// --mode harvester, --mode username, --mode emailcheck) do NOT trigger
// correlation — operators using those modes have explicit, scoped intent and
// should not get auto-chained results they didn't request.
//
// Rule execution order and dependencies:
//
//	Rule A: Email (from crawler) → emailcheck
//	  ↓ may produce new emails from platform registration pages
//	Rule B: Email (all sources) → local-part → username check
//	  ↓ username module runs for each candidate
//	Rule C: Subdomain (from harvester) → re-crawl (only if --correlate-crawl)
//	  No recursive triggering — correlator runs once, not in a loop.
//	  Subdomains found during re-crawl do NOT re-trigger Rule C.
package correlator

import (
	"context"
	"io"

	"nameless/internal/core"
)

// Correlator orchestrates the three correlation rules against the shared Aggregator.
type Correlator struct {
	agg    *core.Aggregator
	store  *RelationStore
	errOut io.Writer
}

// New returns a Correlator ready to apply rules.
func New(agg *core.Aggregator, store *RelationStore, errOut io.Writer) *Correlator {
	return &Correlator{agg: agg, store: store, errOut: errOut}
}

// RunRuleA applies Rule A: for each email discovered by the crawler module,
// run emailcheck and record the relation. New emailcheck results are added
// back into the Aggregator so Rule B can pick them up.
//
// The emailcheckFn parameter is the actual runner — it receives an email address
// and writes EntityPlatform entities for platforms where the email is registered.
// This indirection keeps the correlator free of direct module imports and
// makes the logic trivially testable.
func (c *Correlator) RunRuleA(
	ctx context.Context,
	emailcheckFn func(ctx context.Context, email string, out chan<- core.Entity) error,
) {
	emails := c.agg.ByType(core.EntityEmail)
	for _, e := range emails {
		if ctx.Err() != nil {
			return
		}
		// Only chase emails that were found by the crawler — emails from
		// harvester (crt.sh, etc.) are already addresses we know about; the
		// operator likely wants emailcheck results for target-site emails.
		if e.SourceModule != "crawler" {
			continue
		}
		// Skip if we've already run emailcheck for this address (i.e. the email
		// entity was already added pre-correlation by a standalone emailcheck run).
		// This guards against double-checking when user runs --mode full after
		// having prior emailcheck results in the aggregator.
		if c.agg.Has(core.EntityEmail, e.Value+"::emailchecked") {
			continue
		}

		out := make(chan core.Entity, 50)
		done := make(chan struct{})
		go func(email core.Entity) {
			defer close(done)
			for platform := range out {
				c.agg.Add(platform)
				rel := NewRelation(
					RelEmailToRegistration,
					email,
					platform,
					ConfidenceInferredHigh,
					"rule_a_email_emailcheck",
				)
				c.store.Add(rel)
			}
		}(e)

		if err := emailcheckFn(ctx, e.Value, out); err != nil {
			writeErr(c.errOut, "rule_a emailcheck(%s): %v", e.Value, err)
		}
		close(out)
		<-done

		// Mark this email as having been emailchecked so we don't re-run it
		// if it's encountered again (e.g. from harvester results added later).
		c.agg.Add(core.NewEntity(core.EntityEmail, e.Value+"::emailchecked", "correlator"))
	}
}
