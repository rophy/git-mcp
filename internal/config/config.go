package config

import (
	"fmt"
	"os"
	"path/filepath"
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
	Listen       string        `yaml:"listen"`
	CacheDir     string        `yaml:"cache_dir"`
	MaxCacheSize string        `yaml:"max_cache_size"`
	MaxFileSize  string        `yaml:"max_file_size"`
	MaxMatches   int           `yaml:"max_matches"`
	MaxDiffSize  string        `yaml:"max_diff_size"`
	GitTimeout   time.Duration `yaml:"git_timeout"`
}

type UpstreamConfig struct {
	Token string `yaml:"token"`
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
	for _, pattern := range s.Repos {
		if pattern == repo {
			return true
		}
		if strings.HasSuffix(pattern, "/*") {
			prefix := strings.TrimSuffix(pattern, "/*")
			if filepath.Dir(repo) == prefix {
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
