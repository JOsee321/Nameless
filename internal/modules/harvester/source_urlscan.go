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

// urlscanSource queries URLScan.io for recorded scans matching the target domain.
// Endpoint: https://urlscan.io/api/v1/search/?q=domain:{domain}&size=100
// Response: JSON object with results[].page.domain and results[].page.domain fields.
// No API key is required for unauthenticated search (rate limit: 100 req/h).
type urlscanSource struct {
	client       *core.Client
	limiter      *core.RateLimiter
	endpointTmpl string
}

func NewURLScanSource(client *core.Client, limiter *core.RateLimiter) Source {
	return &urlscanSource{
		client:       client,
		limiter:      limiter,
		endpointTmpl: "https://urlscan.io/api/v1/search/?q=domain:%s&size=100",
	}
}

func NewURLScanSourceWithEndpoint(client *core.Client, limiter *core.RateLimiter, tmpl string) Source {
	return &urlscanSource{client: client, limiter: limiter, endpointTmpl: tmpl}
}

func (s *urlscanSource) Name() string { return "urlscan" }

// urlscanResponse is the top-level response from the URLScan.io search API.
type urlscanResponse struct {
	Results []urlscanResult `json:"results"`
}

type urlscanResult struct {
	Page urlscanPage `json:"page"`
}

type urlscanPage struct {
	Domain string `json:"domain"`
}

// Query implements Source for URLScan.io.
// Each result's page.domain is extracted and emitted as a subdomain entity
// if it matches the target domain.
func (s *urlscanSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	endpoint := fmt.Sprintf(s.endpointTmpl, domain)

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
		return fmt.Errorf("urlscan request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("urlscan returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return fmt.Errorf("urlscan read body: %w", err)
	}

	var response urlscanResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("urlscan JSON decode: %w", err)
	}

	seen := make(map[string]struct{})
	for _, result := range response.Results {
		sub := strings.ToLower(strings.TrimSpace(result.Page.Domain))
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
