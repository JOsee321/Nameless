package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"nameless/internal/core"
)

// crtshSource queries the crt.sh certificate transparency JSON API.
// Endpoint: https://crt.sh/?q=%.{domain}&output=json
// Response: JSON array of certificate records containing "name_value" fields.
type crtshSource struct {
	client       *core.Client
	limiter      *core.RateLimiter
	endpointTmpl string // printf template: first %s = domain; default is production URL
}

// NewCrtshSource returns a crt.sh source wired to shared infrastructure.
func NewCrtshSource(client *core.Client, limiter *core.RateLimiter) Source {
	return &crtshSource{
		client:       client,
		limiter:      limiter,
		endpointTmpl: "https://crt.sh/?q=%%.%s&output=json",
	}
}

// NewCrtshSourceWithEndpoint is like NewCrtshSource but uses a custom endpoint
// template for testing (e.g. pointing at an httptest.Server).
// The template is a fmt.Sprintf format string with one %s for the domain.
func NewCrtshSourceWithEndpoint(client *core.Client, limiter *core.RateLimiter, tmpl string) Source {
	return &crtshSource{client: client, limiter: limiter, endpointTmpl: tmpl}
}

func (s *crtshSource) Name() string { return "crt.sh" }

// crtshEntry is one record from the crt.sh JSON response.
// name_value may contain multiple subdomains separated by newlines.
type crtshEntry struct {
	NameValue string `json:"name_value"`
}

// Query implements Source for crt.sh.
func (s *crtshSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	endpoint := fmt.Sprintf(s.endpointTmpl, url.QueryEscape(domain))

	if err := s.limiter.Wait(ctx, endpoint); err != nil {
		return err
	}

	resp, err := s.client.DoWithRetry(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return fmt.Errorf("crt.sh request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("crt.sh returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20)) // 10 MiB cap
	if err != nil {
		return fmt.Errorf("crt.sh read body: %w", err)
	}

	var entries []crtshEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return fmt.Errorf("crt.sh JSON decode: %w", err)
	}

	seen := make(map[string]struct{})
	for _, entry := range entries {
		// name_value can contain multiple subdomains separated by newlines.
		for _, name := range strings.Split(entry.NameValue, "\n") {
			sub := strings.TrimSpace(name)
			// Strip wildcard prefix: *.example.com → example.com
			sub = strings.TrimPrefix(sub, "*.")
			sub = strings.ToLower(sub)

			if sub == "" || !strings.HasSuffix(sub, "."+domain) && sub != domain {
				continue
			}
			if _, dup := seen[sub]; dup {
				continue
			}
			seen[sub] = struct{}{}

			select {
			case out <- SubdomainEntity(sub, "harvester", s.Name()):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}
