package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/testutil"
)

func newAggWS(t *testing.T, f *testutil.FakeGoClaw, tenantID uuid.UUID) *goclaw.Client {
	t.Helper()
	c := goclaw.NewClient(goclaw.Config{
		WSURL:        f.WSURL(),
		APIToken:     "ak",
		TenantID:     tenantID,
		UserID:       "agg",
		ReconnectMin: 50 * time.Millisecond,
		ReconnectMax: 2 * time.Second,
		OnDisconnect: func(err error) {},
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func mustUUIDAgg(t *testing.T, s string) uuid.UUID {
	t.Helper()
	uid, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("parse uuid %q: %v", s, err)
	}
	return uid
}

func TestTaskKPIExactAndTruncated(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2, 3, 4, 5},
				"hasMore": false,
			},
		},
	})
	ws := newAggWS(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := TaskKPIs(ctx, ws, []TeamRef{{ID: "team1", Name: "Team 1"}})
	if err != nil {
		t.Fatalf("TaskKPIs: %v", err)
	}
	if len(res.Teams) != 1 {
		t.Fatalf("teams = %d, want 1", len(res.Teams))
	}
	ttk := res.Teams[0]
	if ttk.Active.Value != 5 || ttk.Active.Exact != true {
		t.Fatalf("active = %+v, want {5, true}", ttk.Active)
	}
	if res.TotalActive.Value != 5 || res.TotalActive.Exact != true {
		t.Fatalf("total active = %+v, want {5, true}", res.TotalActive)
	}
}

func TestTaskKPITruncatedNotExact(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2, 3},
				"hasMore": true,
			},
		},
	})
	ws := newAggWS(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := TaskKPIs(ctx, ws, []TeamRef{{ID: "team1", Name: "Team 1"}})
	if err != nil {
		t.Fatalf("TaskKPIs: %v", err)
	}
	ttk := res.Teams[0]
	if ttk.Active.Value != 3 || ttk.Active.Exact != false {
		t.Fatalf("active = %+v, want {3, false}", ttk.Active)
	}
	if res.TotalActive.Value != 3 || res.TotalActive.Exact != false {
		t.Fatalf("total active = %+v, want {3, false}", res.TotalActive)
	}
}

func TestTaskKPIMultiTeamAggregateExact(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   []interface{}{1, 2},
				"hasMore": false,
			},
		},
	})
	ws := newAggWS(t, f, tenantID)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := TaskKPIs(ctx, ws, []TeamRef{
		{ID: "teamA", Name: "A"},
		{ID: "teamB", Name: "B"},
	})
	if err != nil {
		t.Fatalf("TaskKPIs: %v", err)
	}
	if res.TotalActive.Value != 4 || res.TotalActive.Exact != true {
		t.Fatalf("total active = %+v, want {4, true}", res.TotalActive)
	}
	if len(res.Teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(res.Teams))
	}
}
