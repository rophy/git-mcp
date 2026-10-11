package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
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

// UpstreamConfig holds no secrets: the password (or token) is read from the
// env var named by PasswordEnv.
type UpstreamConfig struct {
	// BaseURL replaces "https://<host>" in clone URLs, e.g. "http://gitserver:8080".
	BaseURL string `yaml:"base_url"`
	// Username and the password from PasswordEnv are given to git as HTTP
	// credentials when the upstream asks for them.
	Username    string `yaml:"username"`
	PasswordEnv string `yaml:"password_env"`

	// Password is read from PasswordEnv by Load; it is never read from the file.
	Password string `yaml:"-"`
}

// UpstreamURL returns the clone URL and credentials for repo ("<host>/<path>").
func (c *Config) UpstreamURL(repo string) (url, username, password string, err error) {
	host, repoPath, _ := strings.Cut(repo, "/")
	upstream, ok := c.Upstream[host]
	if !ok {
		return "", "", "", fmt.Errorf("no upstream configured for host %q", host)
	}
	if upstream.BaseURL == "" {
		return "https://" + repo, upstream.Username, upstream.Password, nil
	}
	return strings.TrimRight(upstream.BaseURL, "/") + "/" + repoPath, upstream.Username, upstream.Password, nil
}

// InsecureUpstreams returns the hosts that would send a password over plain http.
func (c *Config) InsecureUpstreams() []string {
	var hosts []string
	for host, upstream := range c.Upstream {
		if upstream.Password != "" && strings.HasPrefix(strings.ToLower(upstream.BaseURL), "http://") {
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

	expanded := []byte(os.ExpandEnv(string(data)))
	if err := rejectSecretFields(expanded); err != nil {
		return nil, err
	}

	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(expanded))
	dec.KnownFields(true) // a typo or a removed field must not be ignored
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.loadUpstreamSecrets(); err != nil {
		return nil, err
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

// rejectSecretFields gives a clear error for secret fields that older configs
// used. Secrets must come from the environment (password_env).
func rejectSecretFields(data []byte) error {
	var raw struct {
		Upstream map[string]map[string]any `yaml:"upstream"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	for host, fields := range raw.Upstream {
		for _, key := range []string{"token", "password"} {
			if _, ok := fields[key]; ok {
				return fmt.Errorf("upstream.%s.%s: secrets must not be in the config file; put the secret in an env var and set password_env to its name", host, key)
			}
		}
	}
	return nil
}

// loadUpstreamSecrets checks each upstream and reads its password from the environment.
func (c *Config) loadUpstreamSecrets() error {
	for host, upstream := range c.Upstream {
		if upstream.BaseURL != "" {
			u, err := url.Parse(upstream.BaseURL)
			if err != nil {
				return fmt.Errorf("upstream.%s.base_url: %w", host, err)
			}
			if u.User != nil {
				return fmt.Errorf("upstream.%s.base_url must not contain credentials; use username and password_env", host)
			}
		}
		if upstream.PasswordEnv == "" {
			continue
		}
		if upstream.Username == "" {
			return fmt.Errorf("upstream.%s: password_env needs username", host)
		}
		upstream.Password = os.Getenv(upstream.PasswordEnv)
		if upstream.Password == "" {
			return fmt.Errorf("upstream.%s: env var %s (password_env) is empty or not set", host, upstream.PasswordEnv)
		}
		c.Upstream[host] = upstream
	}
	return nil
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
