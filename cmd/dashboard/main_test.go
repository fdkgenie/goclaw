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

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

func TestMainIntegrationSmoke(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	// Start fake gateway.
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:     "admin",
		TenantID: tenantID,
		IsOwner:  false,
		Capabilities: []string{
			protocol.CapEventsCronHeartbeatTenantScoped,
			protocol.CapEventsTeamLinkTenantTagged,
			protocol.CapUsageEventsTeam,
			protocol.CapTeamsTasksListHasMore,
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
		ActivityLogs: []map[string]any{
			{
				"id":          "log-1",
				"actor_type":  "user",
				"actor_id":    "operator",
				"action":      "agent.created",
				"entity_type": "agent",
				"entity_id":   "a1",
				"created_at":  "2026-10-19T10:00:00Z",
			},
		},
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
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
	loginReq.Header.Set("Origin", "http://127.0.0.1:19797")
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

	// The audit history of the gateway must reach the activity feed. The
	// gateway returns the rows under "logs"; the dashboard used to look for
	// "items" and dropped the whole history.
	waitForActivityItem(t, client, "http://127.0.0.1:19797", cookie, 10*time.Second, func(item map[string]any) bool {
		return item["id"] == "log-1"
	})

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

// TestDisconnectFailsClosedUntilCanaryReverifies is the P1-5 regression
// test: after the gateway connection drops, the tenant canary is no longer
// verified, so the dashboard must fail closed (503 on the data API and on the
// SSE stream) until a reconnect re-runs the canary successfully. Once the
// canary passes the service must recover.
func TestDisconnectFailsClosedUntilCanaryReverifies(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	otherTenant := uuid.MustParse("6ba7b811-9dad-11d1-80b4-00c04fd430c8")

	goodAgents := []testutil.AgentData{
		{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
	}

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2, 3},
				"hasMore": false,
			},
		},
		UsageSummary:   map[string]any{"cost_usd": 100.0},
		UsageBreakdown: []map[string]any{{"key": "team-a", "cost_usd": 100.0}},
		Agents:         goodAgents,
	})

	const addr = "127.0.0.1:19798"
	cfg := &config.Config{
		GoClawAPIURL:      f.URL(),
		GoClawWSURL:       f.WSURL(),
		GoClawAPIKey:      "ak",
		DashboardAddr:     addr,
		DashboardPassword: "pw",
		TenantID:          tenantID,
	}
	cfg.Sanitize()

	goclaw.Reset()
	t.Cleanup(goclaw.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errc := make(chan error, 1)
	go func() {
		errc <- runWithOptions(ctx, cfg, serviceOptions{
			ReconnectMin: 20 * time.Millisecond,
			ReconnectMax: 100 * time.Millisecond,
		})
	}()

	base := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second}

	// Wait for the dashboard to accept connections.
	waitForStatus(t, 15*time.Second, base+"/api/overview", nil, func(code int) bool {
		return code == http.StatusUnauthorized
	}, "dashboard HTTP server did not start")

	// Log in and keep the session cookie.
	cookie := login(t, client, base)

	// Baseline: the data API answers while the tenant is verified.
	waitForStatus(t, 15*time.Second, base+"/api/overview", cookie, func(code int) bool {
		return code == http.StatusOK
	}, "overview never returned 200 before the disconnect")

	// Break the tenant binding, then force the gateway connection to drop.
	// The reconnect canary must fail, so the service stays fail-closed.
	f.SetAgents([]testutil.AgentData{
		{ID: "a2", AgentKey: "a2", DisplayName: "A2", TenantID: otherTenant.String(), Model: "gpt-4", Status: "active"},
	})
	f.DropConnections()

	waitForStatus(t, 20*time.Second, base+"/api/overview", cookie, func(code int) bool {
		return code == http.StatusServiceUnavailable
	}, "overview never failed closed with 503 after the forced disconnect")

	// The SSE stream must fail closed too.
	if code := getStatus(t, client, base+"/api/stream", cookie); code != http.StatusServiceUnavailable {
		t.Fatalf("stream status = %d, want 503 while degraded", code)
	}

	// Heal the tenant: the next reconnect re-runs the canary and the service
	// must recover.
	f.SetAgents(goodAgents)

	waitForStatus(t, 30*time.Second, base+"/api/overview", cookie, func(code int) bool {
		return code == http.StatusOK
	}, "overview never recovered after the canary re-verified")

	// The feed lost the window the gateway was down: it must show the gap.
	waitForActivityGap(t, client, base, cookie, 10*time.Second)

	// Graceful shutdown.
	cancel()
	select {
	case err := <-errc:
		if err != nil && err != context.Canceled {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

// TestStartupDoesNotBlockOnSilentGateway proves the startup is bounded: a
// gateway that accepts the connection but never answers the backfill RPC
// must not keep the HTTP server from coming up.
//
// The startup backfill used to run with the signal-only context, so a
// gateway that held the connection open without answering blocked the HTTP
// startup forever. The backfill now runs under a deadline: the startup ends
// when the deadline fires, the failure is logged, and the service keeps
// serving.
func TestStartupDoesNotBlockOnSilentGateway(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: goclaw.RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{
				"teams": []map[string]any{{"id": "team-a", "name": "Team A"}},
			},
		},
		// The gateway accepts the connection and never answers the task
		// event RPC the startup backfill issues.
		SilentRPCMethods: []string{"teams.events.list"},
	})

	const addr = "127.0.0.1:19799"
	cfg := &config.Config{
		GoClawAPIURL:      f.URL(),
		GoClawWSURL:       f.WSURL(),
		GoClawAPIKey:      "ak",
		DashboardAddr:     addr,
		DashboardPassword: "pw",
		TenantID:          tenantID,
	}
	cfg.Sanitize()

	goclaw.Reset()
	t.Cleanup(goclaw.Reset)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// A short, injectable bound: the test must not sleep for the
	// production timeout.
	const backfillTimeout = 500 * time.Millisecond

	errc := make(chan error, 1)
	start := time.Now()
	go func() {
		errc <- runWithOptions(ctx, cfg, serviceOptions{
			ReconnectMin:    20 * time.Millisecond,
			ReconnectMax:    100 * time.Millisecond,
			BackfillTimeout: backfillTimeout,
		})
	}()

	base := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second}

	// The server must come up even though the gateway never answers the
	// backfill RPC. Without the bound the startup blocks here forever.
	waitForStatus(t, 15*time.Second, base+"/api/overview", nil, func(code int) bool {
		return code == http.StatusUnauthorized
	}, "dashboard HTTP server did not start while the gateway stayed silent")

	// The startup must have been released by the deadline, not later.
	if elapsed := time.Since(start); elapsed > backfillTimeout+5*time.Second {
		t.Fatalf("dashboard startup took %s, want it bounded by the %s backfill timeout", elapsed, backfillTimeout)
	}

	// The backfill did try: the silent RPC was requested.
	silent := false
	for _, call := range f.RecordedRPCCalls() {
		if call.Method == "teams.events.list" {
			silent = true
			break
		}
	}
	if !silent {
		t.Fatal("the startup backfill never issued the teams.events.list RPC")
	}

	// The failed backfill is not fatal: the tenant canary passed, so the
	// service keeps serving.
	cookie := login(t, client, base)
	waitForStatus(t, 15*time.Second, base+"/api/overview", cookie, func(code int) bool {
		return code == http.StatusOK
	}, "overview never returned 200 after the silent gateway")

	// Graceful shutdown.
	cancel()
	select {
	case err := <-errc:
		if err != nil && err != context.Canceled {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func login(t *testing.T, client *http.Client, base string) *http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"password":"pw"}`)
	req, err := http.NewRequest(http.MethodPost, base+"/api/login", body)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", strings.TrimSuffix(base, "/"))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login = %d, want 200", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "goclaw_dashboard_session" {
			return c
		}
	}
	t.Fatal("expected session cookie after login")
	return nil
}

func getStatus(t *testing.T, client *http.Client, url string, cookie *http.Cookie) int {
	t.Helper()
	code, err := tryStatus(client, url, cookie)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return code
}

// waitForActivityItem polls the activity feed until an item matches the
// predicate.
func waitForActivityItem(t *testing.T, client *http.Client, base string, cookie *http.Cookie, timeout time.Duration, match func(map[string]any) bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, base+"/api/activity", nil)
		if err == nil {
			req.AddCookie(cookie)
			resp, err := client.Do(req)
			if err == nil {
				var payload struct {
					Items []map[string]any `json:"items"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
				resp.Body.Close()
				if decodeErr == nil {
					for _, it := range payload.Items {
						if match(it) {
							return
						}
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the activity feed never showed the expected item")
}

// waitForActivityGap polls the activity feed until a gap marker shows up. The
// feed lost the window the gateway connection was down, so the reconnect must
// mark the gap.
func waitForActivityGap(t *testing.T, client *http.Client, base string, cookie *http.Cookie, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet, base+"/api/activity", nil)
		if err == nil {
			req.AddCookie(cookie)
			resp, err := client.Do(req)
			if err == nil {
				var payload struct {
					Items []struct {
						Type string `json:"type"`
					} `json:"items"`
				}
				decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
				resp.Body.Close()
				if decodeErr == nil {
					for _, it := range payload.Items {
						if it.Type == string(activity.TypeGap) {
							return
						}
					}
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the activity feed never showed the reconnection gap marker")
}

// tryStatus performs a GET and returns the status code. A transport error
// yields code 0 so waiters can tolerate a server that is not up yet.
func tryStatus(client *http.Client, url string, cookie *http.Cookie) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

func waitForStatus(t *testing.T, timeout time.Duration, url string, cookie *http.Cookie, ok func(int) bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := 0
	for time.Now().Before(deadline) {
		code, err := tryStatus(&http.Client{Timeout: 5 * time.Second}, url, cookie)
		if err == nil {
			last = code
			if ok(code) {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s (last status %d for %s)", msg, last, url)
}

// TestGatewayEventReachesSSESubscriber proves the live pipeline end to end:
// a gateway WebSocket event frame is converted, applied to the live state,
// and published as an SSE delta that a subscriber receives.
func TestGatewayEventReachesSSESubscriber(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: goclaw.RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	state := live.NewState()
	hub := live.NewHub(state)
	ring := activity.NewRingBuffer()

	wsClient := goclaw.NewClient(goclaw.Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "dashboard",
		ReconnectMin: 20 * time.Millisecond,
		ReconnectMax: 100 * time.Millisecond,
		OnDisconnect: func(err error) {},
	})
	if err := wsClient.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { wsClient.Close() })

	httpClient := goclaw.NewHTTPClient(f.URL(), "ak", tenantID.String(), 5*time.Second)
	reconciler := live.NewReconciler(state, httpClient, hub)

	pumpGatewayEvents(ctx, wsClient, reconciler, ring)

	// Subscribe before the event so the delta must travel through the hub.
	_, ch, unsub := hub.Subscribe()
	defer unsub()

	// The gateway wire shape for an agent event (camelCase, see
	// internal/agent.AgentEvent).
	f.BroadcastEvent(protocol.EventAgent, map[string]any{
		"type":    "run.started",
		"agentId": "a1",
		"runId":   "run-42",
		"teamId":  "team-a",
	})

	deadline := time.After(15 * time.Second)
	for {
		select {
		case delta := <-ch:
			if delta.Kind != "event" {
				continue
			}
			if delta.KPI == nil || delta.KPI.ActiveRunsUntraced != 1 {
				t.Fatalf("delta KPI = %+v, want one untraced run", delta.KPI)
			}
			items := ring.List(10)
			if len(items) != 1 {
				t.Fatalf("activity ring items = %d, want 1", len(items))
			}
			if items[0].Summary != "run.started" {
				t.Fatalf("activity summary = %q, want run.started", items[0].Summary)
			}
			return
		case <-deadline:
			t.Fatal("subscriber never received the gateway event delta")
		}
	}
}

// TestTeamTaskEventReachesActivityFeed proves the live pump loads the team
// task events: the team.task.* family was never consumed, so the feed only
// showed the agent events.
func TestTeamTaskEventReachesActivityFeed(t *testing.T) {
	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		Role:         "admin",
		TenantID:     tenantID,
		IsOwner:      false,
		Capabilities: goclaw.RequiredCapabilities,
		Agents: []testutil.AgentData{
			{ID: "a1", AgentKey: "a1", DisplayName: "A1", TenantID: tenantID.String(), Model: "gpt-4", Status: "active"},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	state := live.NewState()
	hub := live.NewHub(state)
	ring := activity.NewRingBuffer()

	wsClient := goclaw.NewClient(goclaw.Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "dashboard",
		ReconnectMin: 20 * time.Millisecond,
		ReconnectMax: 100 * time.Millisecond,
		OnDisconnect: func(err error) {},
	})
	if err := wsClient.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { wsClient.Close() })

	httpClient := goclaw.NewHTTPClient(f.URL(), "ak", tenantID.String(), 5*time.Second)
	reconciler := live.NewReconciler(state, httpClient, hub)
	pumpGatewayEvents(ctx, wsClient, reconciler, ring)

	// The gateway wire shape of a team task event.
	f.BroadcastEvent(protocol.EventTeamTaskCreated, map[string]any{
		"team_id":   "team-a",
		"task_id":   "task-1",
		"status":    "in_progress",
		"subject":   "Ship it",
		"actor_id":  "agent-1",
		"timestamp": "2026-10-19T10:00:00Z",
	})

	deadline := time.After(15 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("the team task event never reached the activity feed")
		default:
		}
		items := ring.List(10)
		for _, it := range items {
			if it.Type != activity.TypeTaskEvent {
				continue
			}
			if it.TeamID != "team-a" {
				t.Errorf("team_id = %q, want team-a", it.TeamID)
			}
			if it.Summary == "" {
				t.Error("summary is empty, want the event name")
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}
