package collector

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ScrapeConfig is the parsed shape of /etc/ongrid-edge/scrape.yaml.
type ScrapeConfig struct {
	Targets []ScrapeTarget `yaml:"targets"`
}

const (
	ScrapeRoleHost      = "host"
	ScrapeRoleComponent = "component"
)

// ScrapeTarget describes one /metrics URL to poll.
type ScrapeTarget struct {
	// Name is the operator-chosen identifier; carried on the wire as
	// Source = "scrape:<Name>".
	Name string `yaml:"name"`
	// URL is the absolute /metrics URL (http or https).
	URL string `yaml:"url"`
	// Role controls how the target participates in ongrid:
	//   - host: eligible as the host source for fast-path dashboard and
	//     built-in host alerts; replaces embedded baseline in auto mode.
	//   - component: rich-path Prom samples only.
	//
	// Default: component.
	Role string `yaml:"role,omitempty"`
	// Interval is the per-target scrape period. Default 30s if zero.
	Interval time.Duration `yaml:"interval"`
	// Timeout caps each scrape's HTTP round-trip. Default 10s if zero.
	Timeout time.Duration `yaml:"timeout"`
	// BearerTokenFile, if set, is read once on first scrape and the
	// contents are sent as "Authorization: Bearer <...>".
	BearerTokenFile string `yaml:"bearer_token_file,omitempty"`
	// TLSInsecure disables certificate verification for https targets.
	// Use sparingly — most useful for self-signed kubelet endpoints.
	TLSInsecure bool `yaml:"tls_insecure,omitempty"`
	// StaticLabels are merged into every PromSample emitted for this
	// target. The producer's metric labels win on collisions.
	StaticLabels map[string]string `yaml:"static_labels,omitempty"`
}

// LoadScrapeConfig reads the yaml at path and returns the parsed config
// with default values applied (Interval=30s, Timeout=10s).
func LoadScrapeConfig(path string) (*ScrapeConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read scrape config %q: %w", path, err)
	}
	var cfg ScrapeConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse scrape config %q: %w", path, err)
	}
	if len(cfg.Targets) == 0 {
		return nil, fmt.Errorf("scrape config %q has no targets", path)
	}
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.Name == "" {
			return nil, fmt.Errorf("scrape config %q: target #%d missing name", path, i)
		}
		if t.URL == "" {
			return nil, fmt.Errorf("scrape config %q: target %q missing url", path, t.Name)
		}
		t.Role = strings.TrimSpace(t.Role)
		if t.Role == "" {
			t.Role = ScrapeRoleComponent
		}
		switch t.Role {
		case ScrapeRoleHost, ScrapeRoleComponent:
		default:
			return nil, fmt.Errorf("scrape config %q: target %q invalid role %q", path, t.Name, t.Role)
		}
		if t.Interval <= 0 {
			t.Interval = 30 * time.Second
		}
		if t.Timeout <= 0 {
			t.Timeout = 10 * time.Second
		}
	}
	return &cfg, nil
}
