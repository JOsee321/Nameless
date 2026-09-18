// Package crawler implements recursive web crawling (ex-Photon).
//
// Design decisions (per operator input):
//   - Relative URLs are resolved to absolute using the page's base URL,
//     then filtered to stay on the seed domain by default.
//   - robots.txt is fetched and honoured; use CrawlerOptions.IgnoreRobots
//     to override.
//   - Hard limits: MaxDepth (default 2) and MaxPages (default 200) prevent
//     runaway crawls. Both should be exposed as CLI flags if operators need
//     to tune them — reported to operator for decision.
//
// All HTTP is done via the shared core.Client; rate limiting per-domain is
// enforced through core.RateLimiter; concurrency is capped by core.Pool.
package crawler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PuerkitoBio/goquery"

	"nameless/internal/core"
)

// ------------------------------------------------------------------ options

// CrawlerOptions configures a single crawl run.
type CrawlerOptions struct {
	// MaxDepth is the maximum link-follow depth from the seed URL.
	// Default: 2.  0 means fetch only the seed page.
	MaxDepth int
	// MaxPages is the hard ceiling on total pages fetched per crawl.
	// Default: 200.  Prevents runaway crawls on large sites.
	MaxPages int
	// StayOnDomain restricts following links to the seed's hostname.
	// Enabled by default; set false via --crawl-external to disable.
	StayOnDomain bool
	// IgnoreRobots skips fetching/parsing robots.txt when true.
	// Default: false (robots.txt is honoured).
	IgnoreRobots bool
}

// DefaultCrawlerOptions returns conservative defaults safe for OSINT use.
func DefaultCrawlerOptions() CrawlerOptions {
	return CrawlerOptions{
		MaxDepth:     2,
		MaxPages:     200,
		StayOnDomain: true,
		IgnoreRobots: false,
	}
}

// ------------------------------------------------------------------ module

// Module implements the crawler scan module.
type Module struct {
	client  *core.Client
	limiter *core.RateLimiter
	pool    *core.Pool
	opts    CrawlerOptions
}

// New creates a crawler Module wired to the shared infrastructure.
func New(client *core.Client, limiter *core.RateLimiter, pool *core.Pool, opts CrawlerOptions) *Module {
	return &Module{client: client, limiter: limiter, pool: pool, opts: opts}
}

// Name returns the module identifier.
func (m *Module) Name() string { return "crawler" }

// Run crawls the given seed URL and streams discovered entities into out.
func (m *Module) Run(ctx context.Context, seed string, out chan<- core.Entity) error {
	seedURL, err := url.Parse(seed)
	if err != nil {
		return fmt.Errorf("crawler: invalid seed URL %q: %w", seed, err)
	}
	// Normalise: ensure scheme is present.
	if seedURL.Scheme == "" {
		seedURL.Scheme = "https"
		seed = seedURL.String()
	}

	// Fetch and parse robots.txt before crawling.
	var disallowed []string
	if !m.opts.IgnoreRobots {
		disallowed = m.fetchRobotsDisallowed(ctx, seedURL)
		if len(disallowed) > 0 {
			fmt.Fprintf(errOut, "[robots.txt] %s disallows %d path(s) — respecting exclusions (use --ignore-robots to override)\n",
				seedURL.Host, len(disallowed))
		}
	}
	robotsChecker := buildRobotsChecker(disallowed)

	state := &crawlState{
		visited:  make(map[string]struct{}),
		maxDepth: m.opts.MaxDepth,
		maxPages: int64(m.opts.MaxPages),
		seedHost: seedURL.Hostname(),
		onDomain: m.opts.StayOnDomain,
		robots:   robotsChecker,
		out:      out,
		module:   m.Name(),
	}

	// Emit the seed domain as an entity.
	e := core.NewEntity(core.EntityDomain, seedURL.Hostname(), m.Name())
	out <- e

	m.crawl(ctx, state, seed, 0)
	return nil
}

// ------------------------------------------------------------------ crawl state

type crawlState struct {
	mu       sync.Mutex
	visited  map[string]struct{}
	pagesFetched atomic.Int64

	maxDepth int
	maxPages int64
	seedHost string
	onDomain bool
	robots   func(string) bool // returns true if URL is allowed

	out    chan<- core.Entity
	module string
}

func (s *crawlState) markVisited(u string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.visited[u]; seen {
		return false
	}
	s.visited[u] = struct{}{}
	return true
}

// ------------------------------------------------------------------ core crawl

// crawl fetches one page, extracts entities, and recurses (depth-limited).
func (m *Module) crawl(ctx context.Context, state *crawlState, rawURL string, depth int) {
	if depth > state.maxDepth {
		return
	}
	if !state.markVisited(rawURL) {
		return
	}
	if state.pagesFetched.Add(1) > state.maxPages {
		return
	}
	if !state.robots(rawURL) {
		fmt.Fprintf(errOut, "[robots.txt] skipping disallowed URL: %s\n", rawURL)
		return
	}

	if err := m.limiter.Wait(ctx, rawURL); err != nil {
		return
	}

	resp, err := m.client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	})
	if err != nil {
		return
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")

	// Read body once; reuse for all extractors.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20)) // 5 MiB cap
	if err != nil {
		return
	}

	baseURL, _ := url.Parse(rawURL)

	if isHTML(ct) {
		links := extractLinks(body, baseURL)
		emails := extractEmails(body)
		jsURLs := extractJSLinks(body, baseURL)

		for _, em := range emails {
			e := core.NewEntity(core.EntityEmail, em, state.module)
			e.Metadata = map[string]string{"source_url": rawURL}
			state.out <- e
		}
		for _, jsURL := range jsURLs {
			e := core.NewEntity(core.EntityEndpoint, jsURL, state.module)
			e.Metadata = map[string]string{"kind": "js_file", "source_url": rawURL}
			state.out <- e
			// Fetch JS file to extract endpoints and secrets.
			if depth < state.maxDepth {
				m.processJS(ctx, state, jsURL, rawURL)
			}
		}
		for _, link := range links {
			e := core.NewEntity(core.EntityEndpoint, link, state.module)
			e.Metadata = map[string]string{"kind": "link", "source_url": rawURL}
			state.out <- e

			if shouldFollow(link, state) {
				linkCopy := link
				depth := depth + 1
				_ = m.pool.Submit(ctx, func(ctx context.Context) {
					m.crawl(ctx, state, linkCopy, depth)
				})
			}
		}

		// Extract form action URLs.
		for _, form := range extractForms(body, baseURL) {
			e := core.NewEntity(core.EntityEndpoint, form, state.module)
			e.Metadata = map[string]string{"kind": "form_action", "source_url": rawURL}
			state.out <- e
		}
	}
}

// processJS fetches a JS file and extracts path-like endpoints and secrets.
func (m *Module) processJS(ctx context.Context, state *crawlState, jsURL, pageURL string) {
	if !state.markVisited("js:" + jsURL) {
		return
	}
	if err := m.limiter.Wait(ctx, jsURL); err != nil {
		return
	}
	resp, err := m.client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, jsURL, nil)
	})
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20)) // 2 MiB cap for JS
	if err != nil {
		return
	}

	for _, ep := range extractJSEndpoints(body) {
		e := core.NewEntity(core.EntityEndpoint, ep, state.module)
		e.Metadata = map[string]string{"kind": "js_endpoint", "source_url": jsURL, "page_url": pageURL}
		state.out <- e
	}
	for _, secret := range extractSecrets(body) {
		e := core.NewEntity(core.EntitySecret, secret.value, state.module)
		e.Metadata = map[string]string{
			"pattern":    secret.pattern,
			"source_url": jsURL,
			"page_url":   pageURL,
		}
		state.out <- e
	}
}

// ------------------------------------------------------------------ helpers

// shouldFollow returns true if the crawler should recursively crawl url.
func shouldFollow(u string, state *crawlState) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if state.onDomain && parsed.Hostname() != state.seedHost {
		return false
	}
	return true
}

// isHTML returns true when the Content-Type indicates an HTML document.
func isHTML(ct string) bool {
	return strings.Contains(ct, "text/html")
}

// errOut is where warning messages are written. Points to os.Stderr by default
// but can be overridden in tests.
var errOut io.Writer

func init() {
	// Initialised here to avoid an import cycle; os is imported indirectly.
	// Package-level var allows tests to capture output without os.Stderr.
	setDefaultErrOut()
}

// ------------------------------------------------------------------ robots.txt

// fetchRobotsDisallowed retrieves robots.txt and returns paths disallowed for *.
func (m *Module) fetchRobotsDisallowed(ctx context.Context, base *url.URL) []string {
	robotsURL := *base
	robotsURL.Path = "/robots.txt"
	robotsURL.RawQuery = ""
	robotsURL.Fragment = ""

	resp, err := m.client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, robotsURL.String(), nil)
	})
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10)) // 64 KiB max
	return parseRobotsDisallowed(body)
}

// parseRobotsDisallowed extracts Disallow paths for the * user-agent.
func parseRobotsDisallowed(body []byte) []string {
	var paths []string
	inStar := false
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "user-agent:") {
			agent := strings.TrimSpace(line[len("user-agent:"):])
			inStar = agent == "*"
		}
		if inStar && strings.HasPrefix(lower, "disallow:") {
			path := strings.TrimSpace(line[len("disallow:"):])
			if path != "" {
				paths = append(paths, path)
			}
		}
	}
	return paths
}

// buildRobotsChecker returns a function that returns true if a URL is allowed.
func buildRobotsChecker(disallowed []string) func(string) bool {
	if len(disallowed) == 0 {
		return func(string) bool { return true }
	}
	return func(rawURL string) bool {
		u, err := url.Parse(rawURL)
		if err != nil {
			return true
		}
		for _, d := range disallowed {
			if strings.HasPrefix(u.Path, d) {
				return false
			}
		}
		return true
	}
}

// ------------------------------------------------------------------ extractors

// extractLinks parses <a href="..."> from HTML and returns resolved absolute URLs.
func extractLinks(html []byte, base *url.URL) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var links []string
	doc.Find("a[href]").Each(func(_ int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		abs := resolveURL(href, base)
		if abs == "" {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		links = append(links, abs)
	})
	return links
}

// extractJSLinks finds <script src="..."> and returns resolved absolute URLs.
func extractJSLinks(html []byte, base *url.URL) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var jsLinks []string
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		src, _ := s.Attr("src")
		abs := resolveURL(src, base)
		if abs == "" {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		jsLinks = append(jsLinks, abs)
	})
	return jsLinks
}

// extractForms finds <form action="..."> and returns resolved absolute URLs.
func extractForms(html []byte, base *url.URL) []string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(html))
	if err != nil {
		return nil
	}
	seen := make(map[string]struct{})
	var forms []string
	doc.Find("form[action]").Each(func(_ int, s *goquery.Selection) {
		action, _ := s.Attr("action")
		abs := resolveURL(action, base)
		if abs == "" {
			return
		}
		if _, dup := seen[abs]; dup {
			return
		}
		seen[abs] = struct{}{}
		forms = append(forms, abs)
	})
	return forms
}

// resolveURL resolves href relative to base, returning "" for non-HTTP(S) or invalid URLs.
func resolveURL(href string, base *url.URL) string {
	href = strings.TrimSpace(href)
	if href == "" || strings.HasPrefix(href, "#") ||
		strings.HasPrefix(href, "javascript:") || strings.HasPrefix(href, "mailto:") {
		return ""
	}
	ref, err := url.Parse(href)
	if err != nil {
		return ""
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != "http" && abs.Scheme != "https" {
		return ""
	}
	abs.Fragment = "" // strip anchors
	return abs.String()
}

// ------------------------------------------------------------------ email extractor

// emailRegex matches common email address patterns in HTML/JS bodies.
// Pre-compiled once at package init for performance (PRD §9.3).
var emailRegex = regexp.MustCompile(
	`(?i)\b[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}\b`,
)

// extractEmails returns all unique email addresses found in body.
func extractEmails(body []byte) []string {
	matches := emailRegex.FindAll(body, -1)
	seen := make(map[string]struct{})
	var emails []string
	for _, m := range matches {
		em := strings.ToLower(string(m))
		if _, dup := seen[em]; dup {
			continue
		}
		seen[em] = struct{}{}
		emails = append(emails, em)
	}
	return emails
}

// ------------------------------------------------------------------ JS endpoint extractor

// jsEndpointRegex matches path-like strings inside JS files that look like API
// endpoints.  Patterns are deliberately conservative to avoid false positives
// from CSS selectors, regex literals, or comment text.
var jsEndpointRegex = regexp.MustCompile(
	`["'\x60](/(?:api|v\d+|graphql|rest|rpc|admin|auth|login|user|account|data|search|upload)[^\s"'\x60\\]{0,120})["'\x60]`,
)

// extractJSEndpoints returns path-like endpoint strings found in JS source.
func extractJSEndpoints(body []byte) []string {
	matches := jsEndpointRegex.FindAllSubmatch(body, -1)
	seen := make(map[string]struct{})
	var eps []string
	for _, m := range matches {
		ep := string(m[1])
		if _, dup := seen[ep]; dup {
			continue
		}
		seen[ep] = struct{}{}
		eps = append(eps, ep)
	}
	return eps
}

// ------------------------------------------------------------------ secret extractor

// secretPattern pairs a human-readable name with a compiled regex.
// Patterns adapted from:
//   - truffleHog v3 regex bank (github.com/trufflesecurity/trufflehog)
//   - gitleaks ruleset  (github.com/gitleaks/gitleaks)
//   - Burp Suite's built-in secret patterns
//
// Only high-confidence, low-FP patterns are included here.
type secretPattern struct {
	name string
	re   *regexp.Regexp
}

// detectedSecret is a single secret finding.
type detectedSecret struct {
	pattern string
	value   string
}

var secretPatterns = []secretPattern{
	// AWS Access Key ID — 20-char AKIA/ASIA/AROA/ANPA/ANVA/APKA prefix
	{
		name: "aws_access_key",
		re:   regexp.MustCompile(`(?:AKIA|ABIA|ACCA|ASIA|AROA|ANPA|ANVA|APKA)[A-Z0-9]{16}`),
	},
	// Generic API key / token — "apikey", "api_key", "token", "secret" followed by
	// a long alphanumeric string.  Anchored to assignment syntax to reduce FP.
	{
		name: "generic_api_key",
		re:   regexp.MustCompile(`(?i)(?:api[_\-]?key|apikey|auth[_\-]?token|access[_\-]?token|secret[_\-]?key)\s*[:=]\s*["']?([a-zA-Z0-9\-_]{20,60})["']?`),
	},
	// GitHub personal access token (classic and fine-grained)
	{
		name: "github_pat",
		re:   regexp.MustCompile(`(?:ghp|gho|ghu|ghs|ghr|github_pat)_[A-Za-z0-9_]{36,}`),
	},
	// Slack Bot/App/Webhook token
	{
		name: "slack_token",
		re:   regexp.MustCompile(`xox[baprs]-[0-9A-Za-z\-]{10,48}`),
	},
	// Stripe secret key
	{
		name: "stripe_key",
		re:   regexp.MustCompile(`(?:sk|pk)_(?:live|test)_[0-9a-zA-Z]{24,}`),
	},
	// Google API key
	{
		name: "google_api_key",
		re:   regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`),
	},
	// Mailchimp API key
	{
		name: "mailchimp_api_key",
		re:   regexp.MustCompile(`[0-9a-f]{32}-us[0-9]{1,2}`),
	},
	// Twilio API key
	{
		name: "twilio_api_key",
		re:   regexp.MustCompile(`SK[0-9a-fA-F]{32}`),
	},
	// SendGrid API key
	{
		name: "sendgrid_api_key",
		re:   regexp.MustCompile(`SG\.[a-zA-Z0-9\-_]{22}\.[a-zA-Z0-9\-_]{43}`),
	},
	// JSON Web Token (header.payload.signature structure)
	{
		name: "jwt",
		re:   regexp.MustCompile(`eyJ[a-zA-Z0-9\-_]+\.eyJ[a-zA-Z0-9\-_]+\.[a-zA-Z0-9\-_]+`),
	},
}

// extractSecrets scans body for known secret patterns and returns findings.
func extractSecrets(body []byte) []detectedSecret {
	seen := make(map[string]struct{})
	var found []detectedSecret
	for _, sp := range secretPatterns {
		matches := sp.re.FindAll(body, -1)
		for _, m := range matches {
			val := string(m)
			key := sp.name + ":" + val
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			found = append(found, detectedSecret{pattern: sp.name, value: val})
		}
	}
	return found
}
