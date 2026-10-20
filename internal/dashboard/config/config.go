package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"
)

const (
	defaultAPIURL        = "http://localhost:18790"
	defaultDashboardAddr = "127.0.0.1:18795"
)

// Config holds dashboard service configuration loaded from environment.
type Config struct {
	GoClawAPIURL      string
	GoClawWSURL       string
	GoClawAPIKey      string
	DashboardAddr     string
	DashboardPassword string
	TenantID          uuid.UUID
	WebDir            string // optional --web-dir flag

	// TrustedProxy marks the dashboard as running behind a reverse proxy
	// that terminates TLS. The proxy protocol header is then trusted to
	// decide whether a request arrived over HTTPS.
	TrustedProxy bool

	// Production enables the production hardening: the dashboard only
	// accepts a login over HTTPS and the session cookie is always Secure.
	Production bool
}

// Load parses env vars and validates required fields.
func Load() (*Config, error) {
	cfg := &Config{
		GoClawAPIURL:      envOr("GOCLAW_API_URL", defaultAPIURL),
		GoClawWSURL:       os.Getenv("GOCLAW_WS_URL"),
		GoClawAPIKey:      os.Getenv("GOCLAW_API_KEY"),
		DashboardAddr:     envOr("DASHBOARD_ADDR", defaultDashboardAddr),
		DashboardPassword: os.Getenv("DASHBOARD_PASSWORD"),
		WebDir:            os.Getenv("DASHBOARD_WEB_DIR"),
		TrustedProxy:      envBool("DASHBOARD_TRUSTED_PROXY"),
		Production:        envBool("DASHBOARD_PRODUCTION"),
	}

	if cfg.GoClawAPIKey == "" {
		return nil, fmt.Errorf("GOCLAW_API_KEY is required")
	}
	if cfg.DashboardPassword == "" {
		return nil, fmt.Errorf("DASHBOARD_PASSWORD is required")
	}
	if cfg.GoClawAPIURL == "" {
		return nil, fmt.Errorf("GOCLAW_API_URL is required")
	}

	tenantIDStr := os.Getenv("GOCLAW_TENANT_ID")
	if tenantIDStr == "" {
		return nil, fmt.Errorf("GOCLAW_TENANT_ID is required")
	}
	tenantID, err := uuid.Parse(tenantIDStr)
	if err != nil {
		return nil, fmt.Errorf("GOCLAW_TENANT_ID %q is not a valid UUID: %w", tenantIDStr, err)
	}
	cfg.TenantID = tenantID

	// Derive WS URL from API URL when not explicitly set.
	if cfg.GoClawWSURL == "" {
		derived, err := deriveWSURL(cfg.GoClawAPIURL)
		if err != nil {
			return nil, fmt.Errorf("invalid GOCLAW_API_URL %q: %w", cfg.GoClawAPIURL, err)
		}
		cfg.GoClawWSURL = derived
	}

	return cfg, nil
}

// envOr returns value or default when unset/empty.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envBool parses a boolean environment variable. An unset or empty value is
// false; "1", "true", "yes" and "on" (any case) are true.
func envBool(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// deriveWSURL converts http(s)://host[:port]/path to ws(s)://host[:port]/ws.
// A trailing path other than /ws is replaced by /ws.
func deriveWSURL(apiURL string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", err
	}
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	u.Scheme = scheme
	u.Path = "/ws"
	u.RawQuery = ""
	return u.String(), nil
}

// MustLoad is like Load but panics on error. Useful at daemon startup.
func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}

// Ensure WS URL has no trailing slash and ends with /ws.
func (cfg *Config) Sanitize() {
	cfg.GoClawAPIURL = strings.TrimRight(cfg.GoClawAPIURL, "/")
	cfg.GoClawWSURL = strings.TrimRight(cfg.GoClawWSURL, "/")
	if !strings.HasSuffix(cfg.GoClawWSURL, "/ws") {
		cfg.GoClawWSURL += "/ws"
	}
}
