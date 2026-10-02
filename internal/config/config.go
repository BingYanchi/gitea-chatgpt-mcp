package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr     string
	MCPPath        string
	GiteaBaseURL   string
	GiteaToken     string
	RequestTimeout time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:     value("LISTEN_ADDR", ":8080"),
		MCPPath:        value("MCP_PATH", "/mcp"),
		GiteaBaseURL:   strings.TrimRight(os.Getenv("GITEA_BASE_URL"), "/"),
		GiteaToken:     strings.TrimSpace(os.Getenv("GITEA_TOKEN")),
		RequestTimeout: 30 * time.Second,
	}
	if cfg.GiteaBaseURL == "" {
		return Config{}, fmt.Errorf("GITEA_BASE_URL is required")
	}
	if cfg.GiteaToken == "" {
		return Config{}, fmt.Errorf("GITEA_TOKEN is required")
	}
	if !strings.HasPrefix(cfg.MCPPath, "/") {
		return Config{}, fmt.Errorf("MCP_PATH must start with /")
	}
	if v := strings.TrimSpace(os.Getenv("REQUEST_TIMEOUT")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("REQUEST_TIMEOUT: %w", err)
		}
		cfg.RequestTimeout = d
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
