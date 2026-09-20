package harvester

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"nameless/internal/core"
)

// hackertargetSource queries the HackerTarget passive DNS text API.
// Endpoint: https://api.hackertarget.com/hostsearch/?q={domain}
// Response: newline-separated "subdomain,ip" pairs (plain text).
// Free tier: 100 requests/day, no API key required.
type hackertargetSource struct {
	client       *core.Client
	limiter      *core.RateLimiter
	endpointTmpl string
}

func NewHackerTargetSource(client *core.Client, limiter *core.RateLimiter) Source {
	return &hackertargetSource{
		client:       client,
		limiter:      limiter,
		endpointTmpl: "https://api.hackertarget.com/hostsearch/?q=%s",
	}
}

func NewHackerTargetSourceWithEndpoint(client *core.Client, limiter *core.RateLimiter, tmpl string) Source {
	return &hackertargetSource{client: client, limiter: limiter, endpointTmpl: tmpl}
}

func (s *hackertargetSource) Name() string { return "hackertarget" }

// Query implements Source for HackerTarget.
// Response format: "subdomain.example.com,1.2.3.4\n..." (one entry per line).
// Lines starting with "error" indicate rate-limiting or quota exhaustion.
func (s *hackertargetSource) Query(ctx context.Context, domain string, out chan<- core.Entity) error {
	endpoint := fmt.Sprintf(s.endpointTmpl, domain)

	if err := s.limiter.Wait(ctx, endpoint); err != nil {
		return err
	}

	resp, err := s.client.DoWithRetry(ctx, func() (*http.Request, error) {
		return http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	})
	if err != nil {
		return fmt.Errorf("hackertarget request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("hackertarget returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return fmt.Errorf("hackertarget read body: %w", err)
	}

	// HackerTarget signals API quota exhaustion in the response body.
	if bytes.HasPrefix(body, []byte("error")) {
		return fmt.Errorf("hackertarget API error: %s", strings.TrimSpace(string(body)))
	}

	seen := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "subdomain,ip" — take the subdomain part only.
		sub := strings.SplitN(line, ",", 2)[0]
		sub = strings.ToLower(sub)
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
	return scanner.Err()
}
