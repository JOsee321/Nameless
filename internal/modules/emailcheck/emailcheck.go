// Package emailcheck implements email registration checking across platforms (ex-Holehe).
//
// Detection works by probing password-reset or account-lookup endpoints with the
// target email and inspecting the response.  All three detection strategies from
// the username module apply here too (status_code / body_text / body_regex).
//
// The request schema is a superset of the username module's RequestCfg: it adds
// query-parameter support (for GET-based checks) and POST body templating (for
// password-reset flows), which are the two primary patterns Holehe uses.
package emailcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"nameless/internal/core"
)

// ------------------------------------------------------------------ site defs

// Detection mirrors the username module's Detection type verbatim — the three
// detection strategies are identical for both modules.
type Detection struct {
	Type         string `json:"type"`          // "status_code" | "body_text" | "body_regex"
	PresentCode  int    `json:"present_code"`  // status when email IS registered
	AbsentCode   int    `json:"absent_code"`   // status when email is NOT registered
	FoundPattern string `json:"found_pattern"` // matched in body → email registered
	ErrorPattern string `json:"error_pattern"` // matched in body → email NOT registered
}

// RequestCfg extends the username module's version with query-param and body
// support — the two additional patterns required for Holehe-style checks.
type RequestCfg struct {
	Method      string            `json:"method"`       // "GET" or "POST"
	Headers     map[string]string `json:"headers"`      // custom headers (e.g. Content-Type, Origin)
	Params      map[string]string `json:"params"`       // URL query params; "{}" → email
	Body        string            `json:"body"`         // POST body template; "{}" → email
	ContentType string            `json:"content_type"` // overrides Content-Type if Body is set
}

// SiteDefinition is a single row from sites_emailcheck.json.
type SiteDefinition struct {
	Name              string     `json:"name"`
	URL               string     `json:"url"`      // base URL; "{}" in URL or Params → email
	URLMain           string     `json:"url_main"` // informational only
	Detection         Detection  `json:"detection"`
	EmailRegex        string     `json:"email_regex"`         // pre-filter; empty = accept all
	RateLimitOverride int        `json:"rate_limit_override"` // 0 = use global RPS
	Request           RequestCfg `json:"request"`
	// RequiresSession marks sites that need authenticated cookies to produce a
	// reliable response.  These are skipped in v1 with a stderr warning.
	RequiresSession bool `json:"requires_session"`

	// compiled from EmailRegex at load time; not serialised
	compiledRegex *regexp.Regexp
}

// ------------------------------------------------------------------ result

// Result is the outcome of a single site check.
type Result struct {
	Site  SiteDefinition
	Found bool
	URL   string
	Err   error
}

// ------------------------------------------------------------------ module

// Module implements modules.Module for email registration checking.
type Module struct {
	client  *core.Client
	limiter *core.RateLimiter
	pool    *core.Pool
	sites   []SiteDefinition
}

// New loads site definitions from jsonPath and wires up shared infrastructure.
func New(
	jsonPath string,
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
) (*Module, error) {
	sites, err := loadSites(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("emailcheck: load sites: %w", err)
	}
	return &Module{
		client:  client,
		limiter: limiter,
		pool:    pool,
		sites:   sites,
	}, nil
}

// Name returns the module identifier used in Entity.SourceModule.
func (m *Module) Name() string { return "emailcheck" }

// Run checks the given email address against all loaded site definitions.
// Discovered entities are written to out; Run blocks until all checks complete
// or ctx is cancelled.
func (m *Module) Run(ctx context.Context, email string, out chan<- core.Entity) error {
	var wg sync.WaitGroup

	for i := range m.sites {
		site := m.sites[i]

		if site.RequiresSession {
			fmt.Fprintf(os.Stderr, "[!] skipping %s: requires_session=true (not supported in v1)\n", site.Name)
			continue
		}
		if site.compiledRegex != nil && !site.compiledRegex.MatchString(email) {
			continue
		}

		wg.Add(1)
		if err := m.pool.Submit(ctx, func(ctx context.Context) {
			defer wg.Done()
			result := m.checkSite(ctx, email, site)
			if result.Err != nil {
				e := core.NewEntity(core.EntityPlatform, site.Name, m.Name())
				e.Metadata = map[string]string{
					"status": "error",
					"error":  result.Err.Error(),
					"url":    result.URL,
				}
				out <- e
				return
			}
			if result.Found {
				e := core.NewEntity(core.EntityPlatform, site.Name, m.Name())
				e.Metadata = map[string]string{
					"status": "found",
					"url":    result.URL,
					"email":  email,
				}
				out <- e
			}
		}); err != nil {
			wg.Done()
		}
	}

	wg.Wait()
	return nil
}

// checkSite executes one probe against a site definition.
func (m *Module) checkSite(ctx context.Context, email string, site SiteDefinition) Result {
	targetURL := buildURL(site.URL, site.Request.Params, email)

	if err := m.limiter.Wait(ctx, targetURL); err != nil {
		return Result{Site: site, URL: targetURL, Err: err}
	}

	method := site.Request.Method
	if method == "" {
		method = http.MethodGet
	}

	resp, err := m.client.DoWithRetry(ctx, func() (*http.Request, error) {
		var body io.Reader
		if site.Request.Body != "" {
			expanded := strings.ReplaceAll(site.Request.Body, "{}", email)
			body = strings.NewReader(expanded)
		}

		req, err := http.NewRequestWithContext(ctx, method, targetURL, body)
		if err != nil {
			return nil, err
		}

		// Set Content-Type for requests with a body.
		if site.Request.Body != "" {
			ct := site.Request.ContentType
			if ct == "" {
				ct = "application/x-www-form-urlencoded"
			}
			req.Header.Set("Content-Type", ct)
		}
		for k, v := range site.Request.Headers {
			req.Header.Set(k, v)
		}
		return req, nil
	})
	if err != nil {
		return Result{Site: site, URL: targetURL, Err: err}
	}
	defer resp.Body.Close()

	return Result{
		Site:  site,
		URL:   targetURL,
		Found: isFound(resp, site, email),
	}
}

// buildURL constructs the final request URL by appending query parameters
// (with "{}" replaced by the email) to the base URL.
func buildURL(base string, params map[string]string, email string) string {
	if len(params) == 0 {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		q.Set(strings.ReplaceAll(k, "{}", email),
			strings.ReplaceAll(v, "{}", email))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// isFound applies the Detection rules to decide whether the email is registered.
func isFound(resp *http.Response, site SiteDefinition, email string) bool {
	switch site.Detection.Type {
	case "status_code":
		if site.Detection.PresentCode != 0 {
			return resp.StatusCode == site.Detection.PresentCode
		}
		if site.Detection.AbsentCode != 0 {
			return resp.StatusCode != site.Detection.AbsentCode
		}
		return resp.StatusCode == http.StatusOK

	case "body_text", "body_regex":
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return false
		}
		text := string(raw)
		found := strings.ReplaceAll(site.Detection.FoundPattern, "{}", email)
		errPat := strings.ReplaceAll(site.Detection.ErrorPattern, "{}", email)

		if site.Detection.Type == "body_regex" {
			if found != "" {
				m, _ := regexp.MatchString(found, text)
				return m
			}
			if errPat != "" {
				m, _ := regexp.MatchString(errPat, text)
				return !m
			}
		} else {
			if found != "" {
				return strings.Contains(text, found)
			}
			if errPat != "" {
				return !strings.Contains(text, errPat)
			}
		}
	}
	return false
}

// ------------------------------------------------------------------ loader

func loadSites(path string) ([]SiteDefinition, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sites []SiteDefinition
	if err := json.NewDecoder(f).Decode(&sites); err != nil {
		return nil, fmt.Errorf("decode %q: %w", path, err)
	}

	for i := range sites {
		if sites[i].EmailRegex == "" {
			continue
		}
		re, err := regexp.Compile(sites[i].EmailRegex)
		if err != nil {
			return nil, fmt.Errorf("site %q: invalid email_regex %q: %w",
				sites[i].Name, sites[i].EmailRegex, err)
		}
		sites[i].compiledRegex = re
	}
	return sites, nil
}
