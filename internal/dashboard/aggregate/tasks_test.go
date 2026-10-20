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
				"tasks": []interface{}{
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "pending"},
					map[string]any{"status": "pending"},
				},
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
				"tasks": []interface{}{
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "pending"},
					map[string]any{"status": "blocked"},
				},
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
				"tasks": []interface{}{
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "pending"},
				},
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

// TestTaskKPIErrorResponseIsNotExact pins the fail-soft contract: a failed
// gateway response carries no task list, so the count is unknown and must be
// reported as a lower bound instead of a confident exact zero.
func TestTaskKPIErrorResponseIsNotExact(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCErrors: map[string]string{
			"teams.tasks.list": "team store unavailable",
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
	if ttk.Active.Exact {
		t.Errorf("active exact = true, want false after a failed gateway response")
	}
	if ttk.InReview.Exact {
		t.Errorf("in_review exact = true, want false after a failed gateway response")
	}
	if res.TotalActive.Exact {
		t.Errorf("total active exact = true, want false after a failed gateway response")
	}
	if res.TotalInReview.Exact {
		t.Errorf("total in_review exact = true, want false after a failed gateway response")
	}
}

// TestTaskKPIStatusGroupsWhenTruncated pins the spec Q29 semantics: a
// truncated active query reports the observed count of every status as a
// lower bound (>= observedCount, not >= the query cap) and every group is
// inexact.
func TestTaskKPIStatusGroupsWhenTruncated(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")

	tasks := make([]interface{}, 0, 200)
	for i := 0; i < 120; i++ {
		tasks = append(tasks, map[string]any{"status": "in_progress"})
	}
	for i := 0; i < 50; i++ {
		tasks = append(tasks, map[string]any{"status": "pending"})
	}
	for i := 0; i < 30; i++ {
		tasks = append(tasks, map[string]any{"status": "blocked"})
	}

	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks":   tasks,
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

	if res.TotalActive.Value != 200 {
		t.Fatalf("total active = %d, want the observed 200", res.TotalActive.Value)
	}
	if res.TotalActive.Exact {
		t.Error("total active exact = true, want false for a truncated query")
	}

	groups := res.Teams[0].ActiveByStatus
	if len(groups) != 3 {
		t.Fatalf("status groups = %d, want 3", len(groups))
	}
	want := map[string]int{"in_progress": 120, "pending": 50, "blocked": 30}
	for _, g := range groups {
		w, ok := want[g.Status]
		if !ok {
			t.Fatalf("unexpected status group %q", g.Status)
		}
		if g.Count.Value != w {
			t.Errorf("group %q value = %d, want the observed %d", g.Status, g.Count.Value, w)
		}
		if g.Count.Exact {
			t.Errorf("group %q exact = true, want false for a truncated query", g.Status)
		}
	}

	// The aggregate groups carry the same semantics.
	if len(res.ActiveByStatus) != 3 {
		t.Fatalf("aggregate status groups = %d, want 3", len(res.ActiveByStatus))
	}
	for _, g := range res.ActiveByStatus {
		if g.Count.Exact {
			t.Errorf("aggregate group %q exact = true, want false", g.Status)
		}
		if g.Count.Value != want[g.Status] {
			t.Errorf("aggregate group %q value = %d, want %d", g.Status, g.Count.Value, want[g.Status])
		}
	}
}

// TestTaskKPIStatusGroupsExact pins the exact case: an untruncated query
// reports the per-status counts as exact.
func TestTaskKPIStatusGroupsExact(t *testing.T) {
	tenantID := mustUUIDAgg(t, "6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	f := testutil.NewFakeGoClaw(t, testutil.Config{
		TenantID:     tenantID,
		Capabilities: goclaw.RequiredCapabilities,
		RPCPayloads: map[string]any{
			"teams.tasks.list": map[string]any{
				"tasks": []interface{}{
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "in_progress"},
					map[string]any{"status": "pending"},
				},
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
	if len(res.Teams[0].ActiveByStatus) != 2 {
		t.Fatalf("status groups = %d, want 2", len(res.Teams[0].ActiveByStatus))
	}
	for _, g := range res.Teams[0].ActiveByStatus {
		if !g.Count.Exact {
			t.Errorf("group %q exact = false, want true for an untruncated query", g.Status)
		}
	}
	// Sorted by status name for a stable payload.
	if res.Teams[0].ActiveByStatus[0].Status != "in_progress" {
		t.Errorf("first group = %q, want in_progress", res.Teams[0].ActiveByStatus[0].Status)
	}
	if res.Teams[0].ActiveByStatus[1].Status != "pending" {
		t.Errorf("second group = %q, want pending", res.Teams[0].ActiveByStatus[1].Status)
	}
}
