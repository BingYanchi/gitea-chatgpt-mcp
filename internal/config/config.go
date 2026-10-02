package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"
)

type Config struct {
	ListenAddr             string
	MCPPath                string
	AuthMode               string
	PublicBaseURL          string
	GiteaBaseURL           string
	GiteaToken             string
	GiteaOAuthClientID     string
	GiteaOAuthClientSecret string
	GiteaOAuthScopes       string
	OAuthEncryptionKey     string
	RequestTimeout         time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		ListenAddr:             value("LISTEN_ADDR", ":8080"),
		MCPPath:                value("MCP_PATH", "/mcp"),
		AuthMode:               strings.ToLower(strings.TrimSpace(os.Getenv("AUTH_MODE"))),
		PublicBaseURL:          strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
		GiteaBaseURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("GITEA_BASE_URL")), "/"),
		GiteaToken:             strings.TrimSpace(os.Getenv("GITEA_TOKEN")),
		GiteaOAuthClientID:     strings.TrimSpace(os.Getenv("GITEA_OAUTH_CLIENT_ID")),
		GiteaOAuthClientSecret: strings.TrimSpace(os.Getenv("GITEA_OAUTH_CLIENT_SECRET")),
		GiteaOAuthScopes:       value("GITEA_OAUTH_SCOPES", "read:user write:repository"),
		OAuthEncryptionKey:     strings.TrimSpace(os.Getenv("OAUTH_ENCRYPTION_KEY")),
		RequestTimeout:         30 * time.Second,
	}

	if cfg.GiteaBaseURL == "" {
		return Config{}, fmt.Errorf("GITEA_BASE_URL is required")
	}
	if !strings.HasPrefix(cfg.MCPPath, "/") {
		return Config{}, fmt.Errorf("MCP_PATH must start with /")
	}

	if cfg.AuthMode == "" {
		if cfg.GiteaOAuthClientID != "" || cfg.GiteaOAuthClientSecret != "" || cfg.PublicBaseURL != "" {
			cfg.AuthMode = "oauth"
		} else {
			cfg.AuthMode = "token"
		}
	}

	switch cfg.AuthMode {
	case "token":
		if cfg.GiteaToken == "" {
			return Config{}, fmt.Errorf("GITEA_TOKEN is required when AUTH_MODE=token")
		}
	case "oauth":
		if cfg.PublicBaseURL == "" {
			return Config{}, fmt.Errorf("PUBLIC_BASE_URL is required when AUTH_MODE=oauth")
		}
		if cfg.GiteaOAuthClientID == "" || cfg.GiteaOAuthClientSecret == "" {
			return Config{}, fmt.Errorf("GITEA_OAUTH_CLIENT_ID and GITEA_OAUTH_CLIENT_SECRET are required when AUTH_MODE=oauth")
		}
		key, err := base64.StdEncoding.DecodeString(cfg.OAuthEncryptionKey)
		if err != nil || len(key) != 32 {
			return Config{}, fmt.Errorf("OAUTH_ENCRYPTION_KEY must be base64 encoding of exactly 32 random bytes")
		}
	default:
		return Config{}, fmt.Errorf("AUTH_MODE must be token or oauth")
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
