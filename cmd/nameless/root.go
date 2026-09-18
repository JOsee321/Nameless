// Package main is the entrypoint for the nameless CLI.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "nameless",
	Short: "OSINT correlation engine — unified recon framework",
	Long: `Nameless is a high-performance OSINT framework that unifies web crawling,
username enumeration, passive harvesting, and email registration checking
into a single binary with an automatic correlation layer.`,
}

// Execute runs the root command and exits on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
