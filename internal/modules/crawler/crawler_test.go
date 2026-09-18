package crawler_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"nameless/internal/core"
	"nameless/internal/modules/crawler"
)

// ------------------------------------------------------------------ helpers

// captureErrOut redirects crawler's stderr during tests to a buffer.
func captureErrOut(t *testing.T) (restore func(), buf *bytes.Buffer) {
	t.Helper()
	buf = &bytes.Buffer{}
	restore = crawler.SetErrOutForTest(buf)
	return restore, buf
}

// newCrawlerForTest wires up shared infrastructure and returns a ready Module + entity channel.
func newCrawlerForTest(ctx context.Context, t *testing.T, opts crawler.CrawlerOptions) (*crawler.Module, chan core.Entity) {
	t.Helper()
	client := core.NewClient(core.DefaultClientOptions())
	limiter := core.NewRateLimiter(1000)
	pool := core.NewPool(ctx, 8)
	t.Cleanup(func() { pool.Close() })

	mod := crawler.New(client, limiter, pool, opts)
	out := make(chan core.Entity, 500)
	return mod, out
}

// collectKind drains the entity channel, returning values for entities of the given metadata kind.
func collectKind(out <-chan core.Entity, kind string) []string {
	var result []string
	for e := range out {
		if e.Metadata["kind"] == kind {
			result = append(result, e.Value)
		}
	}
	return result
}

// collectType drains the entity channel, returning values for entities of the given EntityType.
func collectType(out <-chan core.Entity, et core.EntityType) []string {
	var result []string
	for e := range out {
		if e.Type == et {
			result = append(result, e.Value)
		}
	}
	return result
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("slice should contain %q; got %v", want, slice)
}

func assertNotContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			t.Errorf("slice should NOT contain %q", want)
			return
		}
	}
}

// ------------------------------------------------------------------ extractLinks unit tests

func TestExtractLinksAbsolute(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	html := `<html><body>
		<a href="https://example.com/about">About</a>
		<a href="https://other.com/page">External</a>
		<a href="#section">Anchor only</a>
		<a href="javascript:void(0)">JS link</a>
	</body></html>`

	links := crawler.ExtractLinks([]byte(html), base)

	assertContains(t, links, "https://example.com/about")
	assertContains(t, links, "https://other.com/page")
	assertNotContains(t, links, "#section")
	assertNotContains(t, links, "javascript:void(0)")
}

func TestExtractLinksRelative(t *testing.T) {
	base, _ := url.Parse("https://example.com/blog/")
	html := `<html><body>
		<a href="/about">About</a>
		<a href="../contact">Contact</a>
		<a href="post1">Post 1</a>
	</body></html>`

	links := crawler.ExtractLinks([]byte(html), base)

	assertContains(t, links, "https://example.com/about")
	assertContains(t, links, "https://example.com/contact")
	assertContains(t, links, "https://example.com/blog/post1")
}

func TestExtractLinksDeduplicated(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	html := `<html><body>
		<a href="/page">Page</a>
		<a href="/page">Page again</a>
		<a href="/page">Page third time</a>
	</body></html>`

	links := crawler.ExtractLinks([]byte(html), base)
	if len(links) != 1 {
		t.Errorf("expected 1 unique link, got %d: %v", len(links), links)
	}
}

func TestExtractLinksStripsFragment(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	html := `<a href="/page#section">Link</a>`

	links := crawler.ExtractLinks([]byte(html), base)
	assertContains(t, links, "https://example.com/page")
	for _, l := range links {
		if strings.Contains(l, "#") {
			t.Errorf("link %q should not contain fragment", l)
		}
	}
}

// ------------------------------------------------------------------ extractJSLinks unit tests

func TestExtractJSLinks(t *testing.T) {
	base, _ := url.Parse("https://example.com/")
	html := `<html><head>
		<script src="/js/app.js"></script>
		<script src="https://cdn.example.com/lib.js"></script>
		<script>inline code here</script>
	</head></html>`

	jsLinks := crawler.ExtractJSLinks([]byte(html), base)

	assertContains(t, jsLinks, "https://example.com/js/app.js")
	assertContains(t, jsLinks, "https://cdn.example.com/lib.js")
	if len(jsLinks) != 2 {
		t.Errorf("expected 2 JS links, got %d: %v", len(jsLinks), jsLinks)
	}
}

// ------------------------------------------------------------------ extractEmails unit tests

func TestExtractEmails(t *testing.T) {
	body := []byte(`Contact us at info@example.com or support@company.co.uk.
	Also: ADMIN@EXAMPLE.COM (should be lowercased and deduplicated).`)

	emails := crawler.ExtractEmails(body)
	assertContains(t, emails, "info@example.com")
	assertContains(t, emails, "support@company.co.uk")
	assertContains(t, emails, "admin@example.com")
	assertNotContains(t, emails, "ADMIN@EXAMPLE.COM")
}

func TestExtractEmailsNoDuplicates(t *testing.T) {
	body := []byte("email@test.com email@test.com email@test.com")
	emails := crawler.ExtractEmails(body)
	if len(emails) != 1 {
		t.Errorf("expected 1 unique email, got %d", len(emails))
	}
}

// ------------------------------------------------------------------ extractJSEndpoints unit tests

func TestExtractJSEndpoints(t *testing.T) {
	js := []byte(`
		fetch("/api/v1/users", {method:"GET"});
		const base = "/auth/login";
		let url = '/admin/dashboard';
		var q = "/api/v2/search";
	`)

	eps := crawler.ExtractJSEndpoints(js)
	assertContains(t, eps, "/api/v1/users")
	assertContains(t, eps, "/auth/login")
	assertContains(t, eps, "/admin/dashboard")
}

func TestExtractJSEndpointsNoDuplicates(t *testing.T) {
	js := []byte(`"/api/users" "/api/users" "/api/users"`)
	eps := crawler.ExtractJSEndpoints(js)
	if len(eps) != 1 {
		t.Errorf("expected 1 unique endpoint, got %d", len(eps))
	}
}

// ------------------------------------------------------------------ extractSecrets unit tests

func TestExtractSecretsAWSKey(t *testing.T) {
	body := []byte(`var key = "AKIAIOSFODNN7EXAMPLE";`)
	secrets := crawler.ExtractSecrets(body)
	if len(secrets) == 0 {
		t.Fatal("expected AWS key to be detected")
	}
	found := false
	for _, s := range secrets {
		if s.Pattern == "aws_access_key" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected aws_access_key pattern, got %+v", secrets)
	}
}

func TestExtractSecretsGitHubPAT(t *testing.T) {
	body := []byte(`token: ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ123456789012`)
	secrets := crawler.ExtractSecrets(body)
	found := false
	for _, s := range secrets {
		if s.Pattern == "github_pat" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected github_pat pattern; secrets: %+v", secrets)
	}
}

func TestExtractSecretsJWT(t *testing.T) {
	body := []byte(`eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c`)
	secrets := crawler.ExtractSecrets(body)
	found := false
	for _, s := range secrets {
		if s.Pattern == "jwt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected jwt pattern; secrets: %+v", secrets)
	}
}

func TestExtractSecretsNoDuplicates(t *testing.T) {
	key := "AKIAIOSFODNN7EXAMPLE"
	body := []byte(key + " " + key + " " + key)
	secrets := crawler.ExtractSecrets(body)
	if len(secrets) != 1 {
		t.Errorf("expected 1 unique secret, got %d", len(secrets))
	}
}

// ------------------------------------------------------------------ robots.txt unit tests

func TestParseRobotsDisallowed(t *testing.T) {
	robotsTxt := `
User-agent: *
Disallow: /admin/
Disallow: /private/
Allow: /public/

User-agent: Googlebot
Disallow: /no-google/
`
	disallowed := crawler.ParseRobotsDisallowed([]byte(robotsTxt))
	assertContains(t, disallowed, "/admin/")
	assertContains(t, disallowed, "/private/")
	// Googlebot-specific rules must NOT appear in the * list
	assertNotContains(t, disallowed, "/no-google/")
}

// ------------------------------------------------------------------ integration tests via httptest

func TestCrawlFollowsLinksWithinDepth(t *testing.T) {
	restore, _ := captureErrOut(t)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body>
			<a href="/page1">Page 1</a>
			<a href="/page2">Page 2</a>
		</body></html>`)
	})
	mux.HandleFunc("/page1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body>Page 1</body></html>`)
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body>Page 2</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	mod, out := newCrawlerForTest(ctx, t, crawler.CrawlerOptions{
		MaxDepth: 2, MaxPages: 50, StayOnDomain: true, IgnoreRobots: false,
	})

	_ = mod.Run(ctx, srv.URL+"/", out)
	close(out)

	links := collectKind(out, "link")
	assertContains(t, links, srv.URL+"/page1")
	assertContains(t, links, srv.URL+"/page2")
}

func TestCrawlRespectsMaxDepth(t *testing.T) {
	restore, _ := captureErrOut(t)
	defer restore()

	deepFetched := false
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/level1">Level1</a>`)
	})
	mux.HandleFunc("/level1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/level2">Level2</a>`)
	})
	mux.HandleFunc("/level2", func(w http.ResponseWriter, r *http.Request) {
		deepFetched = true
		_, _ = io.WriteString(w, `<html><body>Deep</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	mod, out := newCrawlerForTest(ctx, t, crawler.CrawlerOptions{
		MaxDepth: 1, MaxPages: 50, StayOnDomain: true, IgnoreRobots: false,
	})

	_ = mod.Run(ctx, srv.URL+"/", out)
	close(out)

	if deepFetched {
		t.Error("crawler exceeded MaxDepth=1 and fetched /level2")
	}
}

func TestCrawlStaysOnDomain(t *testing.T) {
	restore, _ := captureErrOut(t)
	defer restore()

	externalFetched := false
	externalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalFetched = true
		w.WriteHeader(http.StatusOK)
	}))
	defer externalSrv.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/internal">Internal</a><a href="`+externalSrv.URL+`/page">External</a>`)
	})
	mux.HandleFunc("/internal", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	mod, out := newCrawlerForTest(ctx, t, crawler.CrawlerOptions{
		MaxDepth: 2, MaxPages: 50, StayOnDomain: true, IgnoreRobots: false,
	})

	_ = mod.Run(ctx, srv.URL+"/", out)
	close(out)

	if externalFetched {
		t.Error("crawler followed external link despite StayOnDomain=true")
	}
}

func TestCrawlRespectedRobotsTxt(t *testing.T) {
	restore, _ := captureErrOut(t)
	defer restore()

	adminFetched := false
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "User-agent: *\nDisallow: /admin/\n")
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<a href="/admin/secret">Admin</a><a href="/public">Public</a>`)
	})
	mux.HandleFunc("/admin/secret", func(w http.ResponseWriter, r *http.Request) {
		adminFetched = true
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/public", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	mod, out := newCrawlerForTest(ctx, t, crawler.CrawlerOptions{
		MaxDepth: 2, MaxPages: 50, StayOnDomain: true, IgnoreRobots: false,
	})

	_ = mod.Run(ctx, srv.URL+"/", out)
	close(out)

	if adminFetched {
		t.Error("crawler fetched /admin/secret despite robots.txt Disallow: /admin/")
	}
}

func TestCrawlExtractsEmailsFromHTML(t *testing.T) {
	restore, _ := captureErrOut(t)
	defer restore()

	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<html><body>Contact: admin@target.com</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx := context.Background()
	mod, out := newCrawlerForTest(ctx, t, crawler.CrawlerOptions{
		MaxDepth: 0, MaxPages: 10, StayOnDomain: true, IgnoreRobots: false,
	})

	_ = mod.Run(ctx, srv.URL+"/", out)
	close(out)

	emails := collectType(out, core.EntityEmail)
	assertContains(t, emails, "admin@target.com")
}
