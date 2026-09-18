// scan.go defines the 'scan' subcommand and its flags.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"nameless/internal/config"
	"nameless/internal/core"
	"nameless/internal/modules/crawler"
	"nameless/internal/modules/emailcheck"
	"nameless/internal/modules/username"
)

// scan flags — values populated by cobra before RunE runs.
var (
	scanTarget        string
	scanMode          string
	scanConcurrency   int
	scanTimeout       string
	scanRateLimit     int
	scanProxy         string
	scanOutput        string
	scanFormat        string
	scanNoCorrelate   bool
	scanConfig        string
	scanDepth         int
	scanCrawlExternal bool // --crawl-external: follow links outside seed domain
	scanIgnoreRobots  bool // --ignore-robots:  skip robots.txt enforcement
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run an OSINT scan against a target",
	Long: `Run a full or targeted OSINT scan.

Examples:
  nameless scan --target example.com
  nameless scan --target johndoe --mode username
  nameless scan --target user@example.com --mode emailcheck --format csv`,
	RunE: runScan,
}

func runScan(cmd *cobra.Command, args []string) error {
	// ── load config ───────────────────────────────────────────────────────────
	cfg, err := config.Load(scanConfig)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// CLI flags override config file values.
	if cmd.Flags().Changed("concurrency") {
		cfg.Concurrency = scanConcurrency
	}
	if cmd.Flags().Changed("rate-limit") {
		cfg.RateLimit = scanRateLimit
	}
	if cmd.Flags().Changed("timeout") {
		d, err := time.ParseDuration(scanTimeout)
		if err != nil {
			return fmt.Errorf("invalid --timeout %q: %w", scanTimeout, err)
		}
		cfg.Timeout = config.Duration{Duration: d}
	}
	if cmd.Flags().Changed("format") {
		cfg.Format = scanFormat
	}
	if cmd.Flags().Changed("output") {
		cfg.Output = scanOutput
	}

	// ── context with graceful shutdown on SIGINT/SIGTERM ──────────────────────
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// ── shared infrastructure ─────────────────────────────────────────────────
	clientOpts := core.DefaultClientOptions()
	clientOpts.Timeout = cfg.Timeout.Duration
	clientOpts.MaxIdleConnsPerHost = cfg.HTTP.MaxIdleConnsPerHost
	clientOpts.UserAgent = cfg.HTTP.UserAgent

	client := core.NewClient(clientOpts)
	limiter := core.NewRateLimiter(cfg.RateLimit)
	pool := core.NewPool(ctx, cfg.Concurrency)
	defer pool.Close()

	agg := core.NewAggregator()

	// ── output writer ─────────────────────────────────────────────────────────
	var out io.Writer = os.Stdout
	if cfg.Output != "" {
		f, err := os.Create(cfg.Output)
		if err != nil {
			return fmt.Errorf("open output file: %w", err)
		}
		defer f.Close()
		out = f
	}

	// ── dispatch by mode ─────────────────────────────────────────────────────
	switch scanMode {
	case "username":
		return runUsernameMode(ctx, scanTarget, cfg, client, limiter, pool, agg, out)
	case "emailcheck":
		return runEmailcheckMode(ctx, scanTarget, cfg, client, limiter, pool, agg, out)
	case "crawl":
		opts := crawler.DefaultCrawlerOptions()
		opts.MaxDepth = cfg.Depth
		if cmd.Flags().Changed("crawl-external") {
			opts.StayOnDomain = !scanCrawlExternal
		}
		if cmd.Flags().Changed("ignore-robots") {
			opts.IgnoreRobots = scanIgnoreRobots
		}
		return runCrawlMode(ctx, scanTarget, cfg, client, limiter, pool, agg, out, opts)
	default:
		fmt.Fprintf(os.Stderr,
			"mode %q not yet implemented — available modes: username, emailcheck, crawl\n", scanMode)
		return nil
	}
}

// runUsernameMode executes the username enumeration pipeline and writes results.
func runUsernameMode(
	ctx context.Context,
	target string,
	cfg *config.Config,
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
	agg *core.Aggregator,
	out io.Writer,
) error {
	mod, err := username.New(cfg.Data.SitesUsername, client, limiter, pool)
	if err != nil {
		return fmt.Errorf("username module init: %w", err)
	}

	entityCh := make(chan core.Entity, cfg.Concurrency*2)

	// Collect entities and stream progress to stderr.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entityCh {
			agg.Add(e)
			if e.Metadata["status"] == "found" {
				fmt.Fprintf(os.Stderr, "[+] %-25s %s\n", e.Value, e.Metadata["url"])
			}
		}
	}()

	start := time.Now()
	if err := mod.Run(ctx, target, entityCh); err != nil {
		return fmt.Errorf("username scan: %w", err)
	}
	close(entityCh)
	<-done

	elapsed := time.Since(start)
	entities := agg.All()

	// ── write output ──────────────────────────────────────────────────────────
	switch cfg.Format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entities); err != nil {
			return fmt.Errorf("json encode: %w", err)
		}
	case "csv":
		fmt.Fprintln(out, "id,type,value,source_module,url,timestamp")
		for _, e := range entities {
			if e.Metadata["status"] != "found" {
				continue
			}
			fmt.Fprintf(out, "%s,%s,%s,%s,%s,%s\n",
				e.ID, e.Type, e.Value, e.SourceModule,
				e.Metadata["url"], e.Timestamp.Format(time.RFC3339))
		}
	default:
		return fmt.Errorf("format %q not yet implemented", cfg.Format)
	}

	nFound := countFound(entities)
	fmt.Fprintf(os.Stderr,
		"\n[*] username scan complete: %d sites checked, %d found in %s\n",
		len(entities), nFound, elapsed.Round(time.Millisecond))

	return nil
}

func countFound(entities []core.Entity) int {
	n := 0
	for _, e := range entities {
		if e.Metadata["status"] == "found" {
			n++
		}
	}
	return n
}

func init() {
	scanCmd.Flags().StringVarP(&scanTarget, "target", "t", "", "domain, username, or email to scan (required)")
	scanCmd.Flags().StringVar(&scanMode, "mode", "full", "scan mode: full | crawl | username | harvester | emailcheck")
	scanCmd.Flags().IntVar(&scanConcurrency, "concurrency", 50, "number of parallel workers")
	scanCmd.Flags().StringVar(&scanTimeout, "timeout", "10s", "per-request timeout (e.g. 10s, 30s)")
	scanCmd.Flags().IntVar(&scanRateLimit, "rate-limit", 5, "requests per second per domain")
	scanCmd.Flags().StringVar(&scanProxy, "proxy", "", "path to proxy list file (optional)")
	scanCmd.Flags().StringVarP(&scanOutput, "output", "o", "", "output file path (default: stdout)")
	scanCmd.Flags().StringVar(&scanFormat, "format", "json", "output format: json | csv | html")
	scanCmd.Flags().BoolVar(&scanNoCorrelate, "no-correlate", false, "disable correlation layer, run modules independently")
	scanCmd.Flags().StringVar(&scanConfig, "config", "configs/default.yaml", "path to config file")
	scanCmd.Flags().IntVar(&scanDepth, "depth", 2, "crawl depth for the crawler module")
	scanCmd.Flags().BoolVar(&scanCrawlExternal, "crawl-external", false, "follow links to external domains during crawl")
	scanCmd.Flags().BoolVar(&scanIgnoreRobots, "ignore-robots", false, "ignore robots.txt when crawling")

	_ = scanCmd.MarkFlagRequired("target")

	rootCmd.AddCommand(scanCmd)
}

// runCrawlMode executes the recursive web crawler pipeline.
func runCrawlMode(
	ctx context.Context,
	target string,
	cfg *config.Config,
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
	agg *core.Aggregator,
	out io.Writer,
	opts crawler.CrawlerOptions,
) error {
	mod := crawler.New(client, limiter, pool, opts)

	entityCh := make(chan core.Entity, cfg.Concurrency*2)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entityCh {
			agg.Add(e)
			// Stream high-value finds to stderr.
			switch e.Type {
			case core.EntityEmail:
				fmt.Fprintf(os.Stderr, "[email]   %s\n", e.Value)
			case core.EntitySecret:
				fmt.Fprintf(os.Stderr, "[secret]  %-20s %s\n", e.Metadata["pattern"], e.Value)
			case core.EntityEndpoint:
				if e.Metadata["kind"] == "js_endpoint" {
					fmt.Fprintf(os.Stderr, "[endpoint] %s (from %s)\n", e.Value, e.Metadata["source_url"])
				}
			}
		}
	}()

	start := time.Now()
	if err := mod.Run(ctx, target, entityCh); err != nil {
		return fmt.Errorf("crawl: %w", err)
	}
	close(entityCh)
	<-done

	elapsed := time.Since(start)
	entities := agg.All()

	switch cfg.Format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entities); err != nil {
			return fmt.Errorf("json encode: %w", err)
		}
	case "csv":
		fmt.Fprintln(out, "id,type,value,source_module,kind,source_url,timestamp")
		for _, e := range entities {
			fmt.Fprintf(out, "%s,%s,%s,%s,%s,%s,%s\n",
				e.ID, e.Type, e.Value, e.SourceModule,
				e.Metadata["kind"], e.Metadata["source_url"],
				e.Timestamp.Format(time.RFC3339))
		}
	default:
		return fmt.Errorf("format %q not yet implemented", cfg.Format)
	}

	var (
		nEmails    int
		nSecrets   int
		nEndpoints int
		nLinks     int
	)
	for _, e := range entities {
		switch e.Type {
		case core.EntityEmail:
			nEmails++
		case core.EntitySecret:
			nSecrets++
		case core.EntityEndpoint:
			if e.Metadata["kind"] == "js_endpoint" {
				nEndpoints++
			} else {
				nLinks++
			}
		}
	}
	fmt.Fprintf(os.Stderr,
		"\n[*] crawl complete in %s — links:%d emails:%d endpoints:%d secrets:%d\n",
		elapsed.Round(time.Millisecond), nLinks, nEmails, nEndpoints, nSecrets)

	return nil
}

// runEmailcheckMode executes the email registration check pipeline.
func runEmailcheckMode(
	ctx context.Context,
	target string,
	cfg *config.Config,
	client *core.Client,
	limiter *core.RateLimiter,
	pool *core.Pool,
	agg *core.Aggregator,
	out io.Writer,
) error {
	mod, err := emailcheck.New(cfg.Data.SitesEmailcheck, client, limiter, pool)
	if err != nil {
		return fmt.Errorf("emailcheck module init: %w", err)
	}

	entityCh := make(chan core.Entity, cfg.Concurrency*2)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entityCh {
			agg.Add(e)
			if e.Metadata["status"] == "found" {
				fmt.Fprintf(os.Stderr, "[+] %-30s %s\n", e.Value, e.Metadata["url"])
			}
		}
	}()

	start := time.Now()
	if err := mod.Run(ctx, target, entityCh); err != nil {
		return fmt.Errorf("emailcheck scan: %w", err)
	}
	close(entityCh)
	<-done

	elapsed := time.Since(start)
	entities := agg.All()

	switch cfg.Format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(entities); err != nil {
			return fmt.Errorf("json encode: %w", err)
		}
	case "csv":
		fmt.Fprintln(out, "id,type,value,source_module,url,timestamp")
		for _, e := range entities {
			if e.Metadata["status"] != "found" {
				continue
			}
			fmt.Fprintf(out, "%s,%s,%s,%s,%s,%s\n",
				e.ID, e.Type, e.Value, e.SourceModule,
				e.Metadata["url"], e.Timestamp.Format(time.RFC3339))
		}
	default:
		return fmt.Errorf("format %q not yet implemented", cfg.Format)
	}

	fmt.Fprintf(os.Stderr,
		"\n[*] emailcheck scan complete: %d sites checked, %d found in %s\n",
		len(entities), countFound(entities), elapsed.Round(time.Millisecond))

	return nil
}
