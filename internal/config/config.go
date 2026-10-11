package config

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   ServerConfig              `yaml:"server"`
	Upstream map[string]UpstreamConfig `yaml:"upstream"`
	Auth     AuthConfig                `yaml:"auth"`
	Services map[string]ServiceConfig  `yaml:"services"`
}

type ServerConfig struct {
	Listen        string        `yaml:"listen"`
	CacheDir      string        `yaml:"cache_dir"`
	MaxCacheSize  string        `yaml:"max_cache_size"`
	MaxFileSize   string        `yaml:"max_file_size"`
	MaxMatches    int           `yaml:"max_matches"`
	MaxDiffSize   string        `yaml:"max_diff_size"`
	MaxOutputSize string        `yaml:"max_output_size"`
	GitTimeout    time.Duration `yaml:"git_timeout"`
}

type UpstreamConfig struct {
	Token string `yaml:"token"`
	// BaseURL replaces "https://<host>" in clone URLs, e.g. "http://gitserver:8080".
	BaseURL string `yaml:"base_url"`
}

// UpstreamURL returns the clone URL and token for repo ("<host>/<path>").
func (c *Config) UpstreamURL(repo string) (string, string, error) {
	host, repoPath, _ := strings.Cut(repo, "/")
	upstream, ok := c.Upstream[host]
	if !ok {
		return "", "", fmt.Errorf("no upstream configured for host %q", host)
	}
	if upstream.BaseURL == "" {
		return "https://" + repo, upstream.Token, nil
	}
	return strings.TrimRight(upstream.BaseURL, "/") + "/" + repoPath, upstream.Token, nil
}

// InsecureUpstreams returns the hosts that send a token over plain http.
func (c *Config) InsecureUpstreams() []string {
	var hosts []string
	for host, upstream := range c.Upstream {
		if upstream.Token != "" && strings.HasPrefix(strings.ToLower(upstream.BaseURL), "http://") {
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

type AuthConfig struct {
	Audience string         `yaml:"audience"`
	Issuers  []IssuerConfig `yaml:"issuers"`
}

type IssuerConfig struct {
	Name    string `yaml:"name"`
	Iss     string `yaml:"iss"`
	JWKSURI string `yaml:"jwks_uri"`
}

type ServiceConfig struct {
	Issuer  string   `yaml:"issuer"`
	Subject string   `yaml:"subject"`
	Repos   []string `yaml:"repos"`
}

func (s ServiceConfig) AllowsRepo(repo string) bool {
	// path.Dir cleans its input, so "org/x/../repo" would match "org/*".
	if path.Clean(repo) != repo {
		return false
	}
	for _, pattern := range s.Repos {
		if pattern == repo {
			return true
		}
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if path.Dir(repo) == prefix {
				return true
			}
		}
	}
	return false
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if cfg.Server.MaxMatches == 0 {
		cfg.Server.MaxMatches = 50
	}
	if cfg.Server.GitTimeout == 0 {
		cfg.Server.GitTimeout = 30 * time.Second
	}

	return &cfg, nil
}
