package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

// TestOverviewEndpointLoadsTeamsAndAggregates drives the composite overview
// endpoint against the gateway contract. The handler used to pass a nil team
// list, so the task KPIs were always empty, and the cost rows kept their raw
// team ids because no resolver was supplied.
func TestOverviewEndpointLoadsTeamsAndAggregates(t *testing.T) {
	goclaw.Reset()
	t.Cleanup(goclaw.Reset)

	tenantID := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	teamID := "6ba7b811-9dad-11d1-80b4-00c04fd430c8"

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCHandler: func(method string, params json.RawMessage) (any, bool) {
			switch method {
			case "teams.list":
				return map[string]any{
					"teams": []interface{}{
						map[string]any{"id": teamID, "name": "Platform"},
					},
					"count": 1,
				}, true
			case "teams.tasks.list":
				var p struct {
					TeamID string `json:"teamId"`
					Status string `json:"status"`
				}
				_ = json.Unmarshal(params, &p)
				if p.TeamID != teamID {
					t.Errorf("teams.tasks.list teamId = %q, want %q", p.TeamID, teamID)
				}
				if p.Status == "in_review" {
					return map[string]any{
						"tasks": []interface{}{
							map[string]any{"status": "in_review"},
						},
						"count":   1,
						"hasMore": false,
					}, true
				}
				return map[string]any{
					"tasks": []interface{}{
						map[string]any{"status": "in_progress"},
						map[string]any{"status": "pending"},
					},
					"count":   2,
					"hasMore": false,
				}, true
			}
			return nil, false
		},
		UsageSummary: map[string]any{"cost_usd": 12.5, "calls": 3},
		UsageBreakdown: []map[string]any{
			{"key": teamID, "cost_usd": 12.5, "calls": 3, "total_tokens": 100},
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

	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sm.Create()})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("overview status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var payload struct {
		Live struct {
			ActiveRunsTotal int    `json:"active_runs_total"`
			Quality         string `json:"quality"`
		} `json:"live"`
		TaskKPI struct {
			Teams []struct {
				TeamID   string `json:"team_id"`
				TeamName string `json:"team_name"`
				Active   struct {
					Value int  `json:"value"`
					Exact bool `json:"exact"`
				} `json:"active"`
				InReview struct {
					Value int  `json:"value"`
					Exact bool `json:"exact"`
				} `json:"in_review"`
			} `json:"teams"`
			TotalActive struct {
				Value int  `json:"value"`
				Exact bool `json:"exact"`
			} `json:"total_active"`
			TotalInReview struct {
				Value int  `json:"value"`
				Exact bool `json:"exact"`
			} `json:"total_in_review"`
		} `json:"task_kpi"`
		Cost struct {
			SummaryUSD float64 `json:"summary_usd"`
			Rows       []struct {
				Key   string `json:"key"`
				Label string `json:"label"`
			} `json:"rows"`
		} `json:"cost"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode overview payload: %v", err)
	}

	if len(payload.TaskKPI.Teams) != 1 {
		t.Fatalf("task_kpi teams = %d, want 1 (the team list must be loaded)", len(payload.TaskKPI.Teams))
	}
	team := payload.TaskKPI.Teams[0]
	if team.TeamID != teamID {
		t.Errorf("team_id = %q, want %q", team.TeamID, teamID)
	}
	if team.TeamName != "Platform" {
		t.Errorf("team_name = %q, want Platform", team.TeamName)
	}
	if team.Active.Value != 2 || !team.Active.Exact {
		t.Errorf("active = %+v, want {2 true}", team.Active)
	}
	if team.InReview.Value != 1 || !team.InReview.Exact {
		t.Errorf("in_review = %+v, want {1 true}", team.InReview)
	}
	if payload.TaskKPI.TotalActive.Value != 2 {
		t.Errorf("total_active = %d, want 2", payload.TaskKPI.TotalActive.Value)
	}
	if payload.TaskKPI.TotalInReview.Value != 1 {
		t.Errorf("total_in_review = %d, want 1", payload.TaskKPI.TotalInReview.Value)
	}

	if payload.Cost.SummaryUSD != 12.5 {
		t.Errorf("cost.summary_usd = %v, want 12.5", payload.Cost.SummaryUSD)
	}
	if len(payload.Cost.Rows) != 1 {
		t.Fatalf("cost rows = %d, want 1", len(payload.Cost.Rows))
	}
	if payload.Cost.Rows[0].Label != "Platform" {
		t.Errorf("cost row label = %q, want the resolved team name", payload.Cost.Rows[0].Label)
	}

	// The gateway was asked for the team list before the task KPIs.
	var sawTeamsList, sawTaskList bool
	for _, call := range f.RecordedRPCCalls() {
		switch call.Method {
		case "teams.list":
			sawTeamsList = true
		case "teams.tasks.list":
			sawTaskList = true
		}
	}
	if !sawTeamsList {
		t.Error("the overview never called teams.list")
	}
	if !sawTaskList {
		t.Error("the overview never called teams.tasks.list")
	}
}
