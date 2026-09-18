// Package config loads and validates the Nameless YAML configuration file.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that can be unmarshalled from a YAML string
// such as "10s" or "30s" via time.ParseDuration.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	dur, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("config: invalid duration %q: %w", value.Value, err)
	}
	d.Duration = dur
	return nil
}

// Config holds all runtime configuration for a Nameless scan.
// Fields map directly to the keys in configs/default.yaml and can be
// overridden by CLI flags after loading.
type Config struct {
	Concurrency int      `yaml:"concurrency"`
	Timeout     Duration `yaml:"timeout"`
	RateLimit   int      `yaml:"rate_limit"`
	Depth       int      `yaml:"depth"`
	Output      string   `yaml:"output"`
	Format      string   `yaml:"format"`

	Data DataPaths  `yaml:"data"`
	HTTP HTTPConfig `yaml:"http"`
}

// DataPaths points to the external JSON site-definition files.
type DataPaths struct {
	SitesUsername   string `yaml:"sites_username"`
	SitesEmailcheck string `yaml:"sites_emailcheck"`
}

// HTTPConfig controls the shared HTTP client's transport settings.
type HTTPConfig struct {
	MaxIdleConnsPerHost int    `yaml:"max_idle_conns_per_host"`
	UserAgent           string `yaml:"user_agent"`
}

// defaults returns a Config pre-populated with the values from PRD §6.1.
func defaults() Config {
	return Config{
		Concurrency: 50,
		Timeout:     Duration{10 * time.Second},
		RateLimit:   5,
		Depth:       2,
		Format:      "json",
		Data: DataPaths{
			SitesUsername:   "data/sites_username.json",
			SitesEmailcheck: "data/sites_emailcheck.json",
		},
		HTTP: HTTPConfig{
			MaxIdleConnsPerHost: 50,
			UserAgent:           "Mozilla/5.0 (compatible; Nameless/0.1; +https://github.com/nameless)",
		},
	}
}

// Load reads a YAML config file and merges it over the built-in defaults.
// If path is empty, defaults are returned without reading any file.
func Load(path string) (*Config, error) {
	cfg := defaults()

	if path == "" {
		return &cfg, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %q: %w", path, err)
	}
	defer f.Close()

	dec := yaml.NewDecoder(f)
	dec.KnownFields(true) // reject unknown keys to catch typos early
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode %q: %w", path, err)
	}

	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// validate checks that required fields have sensible values.
func validate(cfg *Config) error {
	if cfg.Concurrency < 1 {
		return fmt.Errorf("config: concurrency must be >= 1, got %d", cfg.Concurrency)
	}
	if cfg.RateLimit < 1 {
		return fmt.Errorf("config: rate_limit must be >= 1, got %d", cfg.RateLimit)
	}
	if cfg.Depth < 0 {
		return fmt.Errorf("config: depth must be >= 0, got %d", cfg.Depth)
	}
	allowed := map[string]bool{"json": true, "csv": true, "html": true}
	if !allowed[cfg.Format] {
		return fmt.Errorf("config: format must be json|csv|html, got %q", cfg.Format)
	}
	return nil
}
