package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/activity"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/config"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

// TestCostEndpointUsesGatewayContract drives the whole cost path: the
// dashboard API queries the gateway usage-events endpoints with a time
// window, groups the breakdown by team and resolves the team id to the team
// name.
func TestCostEndpointUsesGatewayContract(t *testing.T) {
	goclaw.Reset()
	t.Cleanup(goclaw.Reset)

	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	teamID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.list": map[string]any{
				"teams": []interface{}{
					map[string]any{"id": teamID, "name": "Platform"},
				},
				"count": 1,
			},
		},
		UsageSummary: map[string]any{"cost_usd": 42.5, "calls": 4},
		UsageBreakdown: []map[string]any{
			{"key": teamID, "cost_usd": 42.5, "calls": 4, "total_tokens": 900},
		},
	})

	ws := goclaw.NewClient(goclaw.Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "dashboard",
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := ws.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { ws.Close() })

	httpClient := goclaw.NewHTTPClient(f.URL(), "ak", tenantID.String(), 0)
	state := live.NewState()
	sm := NewSessionManager(time.Hour)
	router := NewRouter(
		&config.Config{DashboardPassword: "secret"},
		state,
		ws,
		httpClient,
		live.NewHub(state),
		sm,
		NewRateLimiter(rate.Every(time.Minute), 5),
		activity.NewRingBuffer(),
	)

	req := httptest.NewRequest(http.MethodGet, "/api/cost", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sm.Create()})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("cost status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var payload struct {
		SummaryUSD float64 `json:"summary_usd"`
		Calls      int     `json:"calls"`
		Rows       []struct {
			Key         string  `json:"key"`
			Label       string  `json:"label"`
			CostUSD     float64 `json:"cost_usd"`
			Calls       int     `json:"calls"`
			TotalTokens int64   `json:"total_tokens"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode cost payload: %v", err)
	}
	if payload.SummaryUSD != 42.5 {
		t.Fatalf("summary_usd = %v, want 42.5", payload.SummaryUSD)
	}
	if payload.Calls != 4 {
		t.Fatalf("calls = %d, want 4", payload.Calls)
	}
	if len(payload.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(payload.Rows))
	}
	if payload.Rows[0].Label != "Platform" {
		t.Fatalf("row label = %q, want the resolved team name", payload.Rows[0].Label)
	}
	if payload.Rows[0].Calls != 4 || payload.Rows[0].TotalTokens != 900 {
		t.Fatalf("row metrics = %+v", payload.Rows[0])
	}

	// The gateway was queried with the real contract.
	var sawSummary, sawBreakdown bool
	for _, request := range f.RecordedRequests() {
		switch {
		case strings.HasPrefix(request, "/v1/usage/events/summary?"):
			sawSummary = true
		case strings.HasPrefix(request, "/v1/usage/events/breakdown?"):
			sawBreakdown = true
		}
	}
	if !sawSummary || !sawBreakdown {
		t.Fatalf("gateway requests = %v, want a summary and a breakdown query", f.RecordedRequests())
	}
	if !strings.Contains(f.RecordedRequests()[0], "from=") {
		t.Fatalf("summary request %q is missing the time window", f.RecordedRequests()[0])
	}
}
