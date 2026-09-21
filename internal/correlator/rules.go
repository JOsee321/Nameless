package correlator

import (
	"context"
	"regexp"
	"strings"

	"nameless/internal/core"
)

// genericLocalParts is the set of email local-parts that are too common to be
// useful as username candidates. Derived from RFC-common and SMTP-conventional
// mailbox names — matching any of these would produce high false-positive rates
// for the username module.
var genericLocalParts = map[string]struct{}{
	"admin": {}, "administrator": {}, "info": {}, "support": {},
	"noreply": {}, "no-reply": {}, "contact": {}, "hello": {},
	"team": {}, "mail": {}, "sales": {}, "billing": {},
	"help": {}, "service": {}, "news": {}, "newsletter": {},
	"webmaster": {}, "postmaster": {}, "hostmaster": {}, "abuse": {},
	"security": {}, "privacy": {}, "legal": {}, "careers": {},
	"jobs": {}, "press": {}, "marketing": {}, "ops": {},
	"dev": {}, "devops": {}, "sre": {}, "root": {},
	"system": {}, "daemon": {}, "nobody": {}, "noc": {},
	"feedback": {}, "unsubscribe": {}, "bounce": {}, "mailer-daemon": {},
}

// validLocalPartRe matches local-parts that are plausible usernames:
// 3–30 chars, alphanumeric with dots, underscores, or hyphens.
var validLocalPartRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,28}[a-zA-Z0-9]$`)

// RunRuleB applies Rule B: for each email in the aggregator (from any source,
// including new ones added by Rule A), extract the local-part and run username
// enumeration for it.
//
// The usernameFn parameter works identically to emailcheckFn in RunRuleA:
// it receives a username candidate and streams EntityPlatform/EntityUsername
// entities for found profiles.
//
// This must run AFTER RunRuleA so that emails discovered by emailcheck
// (e.g. an alternate address on a profile page) are also considered.
func (c *Correlator) RunRuleB(
	ctx context.Context,
	usernameFn func(ctx context.Context, username string, out chan<- core.Entity) error,
) {
	emails := c.agg.ByType(core.EntityEmail)
	checked := make(map[string]struct{}) // avoid running same local-part twice

	for _, e := range emails {
		if ctx.Err() != nil {
			return
		}

		// Skip the internal "emailchecked" sentinel entities added by Rule A.
		if strings.HasSuffix(e.Value, "::emailchecked") {
			continue
		}

		local, _, ok := strings.Cut(e.Value, "@")
		if !ok || local == "" {
			continue
		}
		local = strings.ToLower(local)

		// Skip generics.
		if _, generic := genericLocalParts[local]; generic {
			continue
		}
		// Skip local-parts that don't look like real usernames.
		if !validLocalPartRe.MatchString(local) {
			continue
		}
		// Skip if already checked in this correlation pass.
		if _, seen := checked[local]; seen {
			continue
		}
		checked[local] = struct{}{}

		// Create a synthetic entity representing the username candidate
		// so NewRelation has a proper source node with a stable ID.
		usernameCandidate := core.NewEntity(core.EntityUsername, local, "correlator")

		out := make(chan core.Entity, 50)
		done := make(chan struct{})
		go func(srcEmail, candidate core.Entity) {
			defer close(done)
			for found := range out {
				c.agg.Add(found)
				// Relation: email → username candidate (probabilistic)
				relEmailToUser := NewRelation(
					RelEmailToUsername,
					srcEmail,
					candidate,
					ConfidenceInferredMid,
					"rule_b_local_part",
				)
				c.store.Add(relEmailToUser)

				// Relation: username candidate → platform profile (observed)
				relUserToProfile := NewRelation(
					RelUsernameToProfile,
					candidate,
					found,
					ConfidenceObserved,
					"rule_b_username_check",
				)
				c.store.Add(relUserToProfile)
			}
		}(e, usernameCandidate)

		if err := usernameFn(ctx, local, out); err != nil {
			writeErr(c.errOut, "rule_b username(%s): %v", local, err)
		}
		close(out)
		<-done
	}
}

// IsGenericLocalPart reports whether a local-part is in the generic exclusion list.
// Exported for testing.
func IsGenericLocalPart(local string) bool {
	_, ok := genericLocalParts[strings.ToLower(local)]
	return ok
}

// IsValidLocalPart reports whether a local-part matches the username candidate regex.
// Exported for testing.
func IsValidLocalPart(local string) bool {
	return validLocalPartRe.MatchString(local)
}
