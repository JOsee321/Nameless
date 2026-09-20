package harvester

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nameless/internal/core"
)

// anubisSource queries the AnubisDB subdomain aggregator.
// Endpoint: https://jonlu.ca/anubis/subdomains/{domain}
// Response: JSON array of subdomain strings (may include empty strings).
type anubisSource struct {
	client       *core.Client
	limiter      *core.RateLimiter
	endpointTmpl string
}

func NewAnubisSource(client *core.Client, limiter *core.RateLimiter) Source {
	return &anubisSource{
		client:       client,
		limiter:      limiter,
		endpointTmpl: "https://jonlu.ca/anubis/subdomains/%s",
	}
}

func NewAnubisSourceWithEndpoint(client *core.Client, limiter *core.RateLimiter, tmpl string) Source {
	return &anubisSource{client: client, limiter: limiter, endpointTmpl: tmpl}
}

func (s *anubisSource) Name() string { return "anubis" }

// Query implements Source for AnubisDB.
// Response is a JSON array of strings: ["sub1.example.com", "sub2.example.com", ...].
// Empty strings and entries not matching the target domain are skipped.
func (s *anubisSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	endpoint := fmt.Sprintf(s.endpointTmpl, domain)

	if err := s.limiter.Wait(ctx, endpoint); err != nil {
		return err
	}

	resp, err := s.client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	})
	if err != nil {
		return fmt.Errorf("anubis request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("anubis returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return fmt.Errorf("anubis read body: %w", err)
	}

	var subdomains []string
	if err := json.Unmarshal(body, &subdomains); err != nil {
		return fmt.Errorf("anubis JSON decode: %w", err)
	}

	seen := make(map[string]struct{})
	for _, sub := range subdomains {
		sub = strings.ToLower(strings.TrimSpace(sub))
		if sub == "" {
			continue
		}
		if !strings.HasSuffix(sub, "."+domain) && sub != domain {
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
	return nil
}
