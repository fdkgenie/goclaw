package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func TestMainIntegrationSmoke(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	// Start fake gateway.
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:     "admin",
		TenantID: tenantID,
		IsOwner:  false,
		Capabilities: []string{
			"events.cron_heartbeat_tenant_scoped",
			"events.team_link_tenant_tagged",
			"usage.events.team",
			"teams.tasks.list.has_more",
		},
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2, 3},
				"hasMore": false,
			},
		},
		UsageSummary: map[string]any{"cost_usd": 100.0},
		UsageBreakdown: []map[string]any{
			{"key": "team-a", "cost_usd": 100.0},
		},
		Agents: []testutil.AgentData{
			{ID: "a1", Name: "A1", TenantID: tenantID.String(), TeamID: "team-a", Role: "member", Model: "gpt-4", Status: "active"},
		},
	})

	// Required env for config.Load.
	os.Setenv("GOCLAW_API_URL", f.URL())
	os.Setenv("GOCLAW_WS_URL", f.WSURL())
	os.Setenv("GOCLAW_API_KEY", "ak")
	os.Setenv("DASHBOARD_PASSWORD", "pw")
	os.Setenv("GOCLAW_TENANT_ID", tenantID.String())
	os.Setenv("DASHBOARD_ADDR", "127.0.0.1:19797")
	t.Cleanup(func() {
		os.Unsetenv("GOCLAW_API_URL")
		os.Unsetenv("GOCLAW_WS_URL")
		os.Unsetenv("GOCLAW_API_KEY")
		os.Unsetenv("DASHBOARD_PASSWORD")
		os.Unsetenv("GOCLAW_TENANT_ID")
		os.Unsetenv("DASHBOARD_ADDR")
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	errc := make(chan error, 1)
	go func() {
		errc <- run(ctx, cfg)
	}()

	// Wait for the server to accept connections (overview returns 401 without a cookie).
	client := &http.Client{Timeout: 5 * time.Second}
	connected := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://127.0.0.1:19797/api/overview")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
				connected = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !connected {
		t.Fatal("dashboard HTTP server did not start")
	}

	// Login to get a session cookie.
	loginBody := strings.NewReader(`{"password":"pw"}`)
	loginReq, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:19797/api/login", loginBody)
	loginReq.Header.Set("Content-Type", "application/json")
	loginResp, err := client.Do(loginReq)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", loginResp.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range loginResp.Cookies() {
		if c.Name == "goclaw_dashboard_session" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("expected session cookie after login")
	}

	// Fetch /api/overview with the session cookie.
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:19797/api/overview", nil)
	req.AddCookie(cookie)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("overview request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview = %d, want 200", resp.StatusCode)
	}
	var ov map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&ov); err != nil {
		t.Fatalf("decode overview: %v", err)
	}
	if ov["task_kpi"] == nil {
		t.Fatal("expected task_kpi in overview")
	}

	// Cancel triggers graceful shutdown.
	cancel()
	select {
	case err := <-errc:
		if err != nil && err != context.Canceled {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}
