package config

import (
	"os"
	"testing"
)

func setenv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		if v == "" {
			os.Unsetenv(k)
		} else {
			os.Setenv(k, v)
		}
	}
}

func unset(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		os.Unsetenv(k)
	}
}

func TestDefaults(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_WS_URL":      "",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
		"DASHBOARD_WEB_DIR":  "",
	})
	unset(t, "GOCLAW_WS_URL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GoClawAPIURL != "http://localhost:18790" {
		t.Errorf("APIURL = %q, want %q", cfg.GoClawAPIURL, "http://localhost:18790")
	}
	if cfg.GoClawWSURL != "ws://localhost:18790/ws" {
		t.Errorf("WSURL = %q, want %q", cfg.GoClawWSURL, "ws://localhost:18790/ws")
	}
	if cfg.DashboardAddr != "127.0.0.1:18795" {
		t.Errorf("DashboardAddr = %q, want %q", cfg.DashboardAddr, "127.0.0.1:18795")
	}
	if cfg.GoClawAPIKey != "ak" {
		t.Errorf("APIKey = %q, want %q", cfg.GoClawAPIKey, "ak")
	}
	if cfg.TenantID.String() != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" {
		t.Errorf("TenantID = %s", cfg.TenantID)
	}
}

func TestMissingAPIKey(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_API_KEY":     "",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing GOCLAW_API_KEY")
	}
}

func TestMissingPassword(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "",
		"GOCLAW_TENANT_ID":   "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing DASHBOARD_PASSWORD")
	}
}

func TestMissingTenantID(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "",
	})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing GOCLAW_TENANT_ID")
	}
}

func TestInvalidTenantID(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "not-a-uuid",
	})
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid GOCLAW_TENANT_ID")
	}
}

func TestWSURLOverridePreserved(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "http://localhost:18790",
		"GOCLAW_WS_URL":      "wss://custom.example.com:9999/custom",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GoClawWSURL != "wss://custom.example.com:9999/custom" {
		t.Errorf("WSURL = %q, want override preserved", cfg.GoClawWSURL)
	}
}

func TestTLSDerivesWSS(t *testing.T) {
	setenv(t, map[string]string{
		"GOCLAW_API_URL":     "https://gateway.example.com/v1",
		"GOCLAW_WS_URL":      "",
		"GOCLAW_API_KEY":     "ak",
		"DASHBOARD_PASSWORD": "pw",
		"GOCLAW_TENANT_ID":   "6ba7b810-9dad-11d1-80b4-00c04fd430c8",
	})
	unset(t, "GOCLAW_WS_URL")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.GoClawWSURL != "wss://gateway.example.com/ws" {
		t.Errorf("WSURL = %q, want %q", cfg.GoClawWSURL, "wss://gateway.example.com/ws")
	}
}
