package config

import (
	"fmt"
	"os"
	"path"
	"sort"
	"strconv"
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

	// Parsed sizes in bytes, set by Load. MaxCacheBytes 0 means no limit.
	MaxCacheBytes  int64 `yaml:"-"`
	MaxFileBytes   int   `yaml:"-"`
	MaxDiffBytes   int   `yaml:"-"`
	MaxOutputBytes int   `yaml:"-"`
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
	if err := cfg.Server.parseSizes(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (s *ServerConfig) parseSizes() error {
	sizes := []struct {
		name  string
		value string
		def   int64
		dst   func(int64)
	}{
		{"max_cache_size", s.MaxCacheSize, 0, func(n int64) { s.MaxCacheBytes = n }},
		{"max_file_size", s.MaxFileSize, 1024 * 1024, func(n int64) { s.MaxFileBytes = int(n) }},
		{"max_diff_size", s.MaxDiffSize, 500 * 1024, func(n int64) { s.MaxDiffBytes = int(n) }},
		{"max_output_size", s.MaxOutputSize, 1024 * 1024, func(n int64) { s.MaxOutputBytes = int(n) }},
	}
	for _, size := range sizes {
		n, err := ParseSize(size.value)
		if err != nil {
			return fmt.Errorf("server.%s: %w", size.name, err)
		}
		if size.value == "" {
			n = size.def
		}
		size.dst(n)
	}
	return nil
}

var sizeUnits = map[string]int64{"": 1, "B": 1, "KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30}

// ParseSize parses a size such as "512", "500KB", "1MB" or "50GB" (units are
// case-insensitive, base 1024). An empty string is 0.
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" {
		return 0, nil
	}
	i := strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' })
	if i == -1 {
		i = len(s)
	}
	unit, ok := sizeUnits[strings.TrimSpace(s[i:])]
	if i == 0 || !ok {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", s, err)
	}
	return n * unit, nil
}
