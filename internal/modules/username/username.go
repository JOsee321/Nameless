// Package username implements platform username enumeration (ex-Sherlock).
//
// It reads site definitions from an external JSON file so new platforms can be
// added without rebuilding the binary (PRD §5.6).  All HTTP requests are made
// via the shared core.Client and rate-limited per domain through core.RateLimiter.
// Concurrency is controlled by core.Pool — this module never creates its own
// goroutines or http.Client instances.
package username

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"

	"nameless/internal/core"
)

// ------------------------------------------------------------------ site defs

// Detection describes how to classify a response as "found" or "not found".
type Detection struct {
	// Type selects the detection strategy:
	//   "status_code" – compare HTTP status codes.
	//   "body_text"   – substring search in the response body.
	//   "body_regex"  – compiled regex match in the response body.
	Type string `json:"type"`

	// status_code fields
	PresentCode int `json:"present_code"` // status when user EXISTS
	AbsentCode  int `json:"absent_code"`  // status when user does NOT exist

	// body_text / body_regex fields — at least one must be set.
	// FoundPattern: if matched → user EXISTS.
	// ErrorPattern: if matched → user does NOT exist.
	FoundPattern string `json:"found_pattern"`
	ErrorPattern string `json:"error_pattern"`
}

// RequestCfg carries per-site HTTP request overrides.
type RequestCfg struct {
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers"`
}

// SiteDefinition is a single row from sites_username.json.
type SiteDefinition struct {
	Name              string     `json:"name"`
	URL               string     `json:"url"`      // "{}" is replaced by the username
	URLMain           string     `json:"url_main"` // informational only
	Detection         Detection  `json:"detection"`
	UsernameRegex     string     `json:"username_regex"`      // pre-filter; empty = accept all
	RateLimitOverride int        `json:"rate_limit_override"` // 0 = use global RPS
	Request           RequestCfg `json:"request"`

	// compiled from UsernameRegex at load time; not in JSON
	compiledRegex *regexp.Regexp
}

// ------------------------------------------------------------------ result

// Result is emitted for each site that was checked.
type Result struct {
	Site  SiteDefinition
	Found bool
	URL   string // final URL checked
	Err   error
}

// ------------------------------------------------------------------ module

// Module implements modules.Module for username enumeration.
type Module struct {
	client  *core.Client
	limiter *core.RateLimiter
	pool    *core.Pool
	sites   []SiteDefinition
}

// New loads site definitions from jsonPath and wires up the shared infrastructure.
func New(
	jsonPath string,
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
) (*Module, error) {
	sites, err := loadSites(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("username: load sites: %w", err)
	}
	return &Module{
		client:  client,
		limiter: limiter,
		pool:    pool,
		sites:   sites,
	}, nil
}

// Name returns the module identifier used in Entity.SourceModule.
func (m *Module) Name() string { return "username" }

// Run enumerates the given username across all loaded site definitions.
// Discovered entities (platforms where the user exists) are written to out.
// Run blocks until all checks complete or ctx is cancelled.
func (m *Module) Run(ctx context.Context, username string, out chan<- core.Entity) error {
	var wg sync.WaitGroup

	for i := range m.sites {
		site := m.sites[i] // copy to avoid closure capture of loop variable

		// Skip sites whose username format regex rejects this username.
		if site.compiledRegex != nil && !site.compiledRegex.MatchString(username) {
			continue
		}

		wg.Add(1)
		if err := m.pool.Submit(ctx, func(ctx context.Context) {
			defer wg.Done()
			result := m.checkSite(ctx, username, site)
			if result.Err != nil {
				// Non-fatal: log via entity metadata so callers can inspect errors
				// without breaking the scan pipeline.
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
					"status":   "found",
					"url":      result.URL,
					"username": username,
				}
				out <- e
			}
		}); err != nil {
			wg.Done() // pool rejected (ctx cancelled)
		}
	}

	// Wait for all submitted jobs, then signal caller that we are done.
	go func() {
		wg.Wait()
	}()
	wg.Wait()
	return nil
}

// checkSite performs a single HTTP probe against one site definition.
func (m *Module) checkSite(ctx context.Context, username string, site SiteDefinition) Result {
	targetURL := strings.ReplaceAll(site.URL, "{}", username)

	// Honour per-domain rate limit.
	if err := m.limiter.Wait(ctx, targetURL); err != nil {
		return Result{Site: site, URL: targetURL, Err: err}
	}

	method := site.Request.Method
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, nil)
	if err != nil {
		return Result{Site: site, URL: targetURL, Err: err}
	}
	for k, v := range site.Request.Headers {
		req.Header.Set(k, v)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return Result{Site: site, URL: targetURL, Err: err}
	}
	defer resp.Body.Close()

	return Result{
		Site:  site,
		URL:   targetURL,
		Found: isFound(resp, site, username),
	}
}

// isFound applies the Detection rules from the site definition to decide
// whether the username exists on that platform.
func isFound(resp *http.Response, site SiteDefinition, username string) bool {
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
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // cap at 1 MiB
		if err != nil {
			return false
		}
		text := string(body)
		// Substitute "{}" in patterns with the actual username.
		found := strings.ReplaceAll(site.Detection.FoundPattern, "{}", username)
		errPat := strings.ReplaceAll(site.Detection.ErrorPattern, "{}", username)

		if site.Detection.Type == "body_regex" {
			if found != "" {
				m, _ := regexp.MatchString(found, text)
				return m
			}
			if errPat != "" {
				m, _ := regexp.MatchString(errPat, text)
				return !m
			}
		} else { // body_text
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

// loadSites reads and parses the JSON site-definition file.
// Each site's UsernameRegex is compiled once here so checks are O(1) at runtime.
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
		if sites[i].UsernameRegex == "" {
			continue
		}
		re, err := regexp.Compile(sites[i].UsernameRegex)
		if err != nil {
			return nil, fmt.Errorf("site %q: invalid username_regex %q: %w",
				sites[i].Name, sites[i].UsernameRegex, err)
		}
		sites[i].compiledRegex = re
	}
	return sites, nil
}
