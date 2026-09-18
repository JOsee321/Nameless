// scan.go defines the 'scan' subcommand and its flags.
package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// scan flags — values populated by cobra before RunE runs.
var (
	scanTarget      string
	scanMode        string
	scanConcurrency int
	scanTimeout     string
	scanRateLimit   int
	scanProxy       string
	scanOutput      string
	scanFormat      string
	scanNoCorrelate bool
	scanConfig      string
	scanDepth       int
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Run an OSINT scan against a target",
	Long: `Run a full or targeted OSINT scan.

Examples:
  nameless scan --target example.com
  nameless scan --target johndoe --mode username
  nameless scan --target user@example.com --mode emailcheck --format csv`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if scanTarget == "" {
			return fmt.Errorf("--target is required")
		}
		// Execution logic will be wired in later phases.
		fmt.Printf("scan: target=%s mode=%s concurrency=%d timeout=%s\n",
			scanTarget, scanMode, scanConcurrency, scanTimeout)
		return nil
	},
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

	_ = scanCmd.MarkFlagRequired("target")

	rootCmd.AddCommand(scanCmd)
}
