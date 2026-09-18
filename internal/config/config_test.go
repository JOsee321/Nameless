package config_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"nameless/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load("")
	if err != nil {
		t.Fatalf("Load(\"\") error: %v", err)
	}
	if cfg.Concurrency != 50 {
		t.Errorf("concurrency default: got %d, want 50", cfg.Concurrency)
	}
	if cfg.Timeout.Duration != 10*time.Second {
		t.Errorf("timeout default: got %v, want 10s", cfg.Timeout)
	}
	if cfg.Format != "json" {
		t.Errorf("format default: got %q, want json", cfg.Format)
	}
}

func TestLoadFromYAML(t *testing.T) {
	content := `
concurrency: 10
timeout: 30s
rate_limit: 2
depth: 3
format: csv
data:
  sites_username: data/sites_username.json
  sites_emailcheck: data/sites_emailcheck.json
http:
  max_idle_conns_per_host: 20
  user_agent: "test-agent"
`
	tmp := filepath.Join(t.TempDir(), "test.yaml")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(tmp)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Concurrency != 10 {
		t.Errorf("concurrency: got %d, want 10", cfg.Concurrency)
	}
	if cfg.Timeout.Duration != 30*time.Second {
		t.Errorf("timeout: got %v, want 30s", cfg.Timeout)
	}
	if cfg.Format != "csv" {
		t.Errorf("format: got %q, want csv", cfg.Format)
	}
}

func TestValidationRejectsInvalidFormat(t *testing.T) {
	content := `
concurrency: 5
rate_limit: 1
depth: 1
format: xml
data:
  sites_username: ""
  sites_emailcheck: ""
http:
  max_idle_conns_per_host: 5
  user_agent: ""
`
	tmp := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := config.Load(tmp)
	if err == nil {
		t.Fatal("expected error for invalid format, got nil")
	}
}
