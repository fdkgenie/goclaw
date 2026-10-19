//go:build integration

package pg

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestPGUsageEventStore_CostByTeam(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	tenant2ID, agent2ID := seedTenantAndAgent(t, db) // tenant row required: usage_events.tenant_id FK → tenants
	teamA := uuid.New()
	teamB := uuid.New()
	teamA2 := uuid.New()

	hour := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	s := NewPGUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)

	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_events WHERE tenant_id IN ($1, $2)", tenantID, tenant2ID)
		db.Exec("DELETE FROM usage_event_rollups WHERE tenant_id IN ($1, $2)", tenantID, tenant2ID)
	})

	// Fixtures:
	// X personal (team NULL) cost 1.0
	// X team A cost 2.0
	// X team B cost 4.0
	// Tenant 2 team A' cost 8.0
	events := []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, EventTime: hour.Add(5 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude-3-5", CostUSD: 1.0, CallCount: 1},
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, TeamID: &teamA, EventTime: hour.Add(10 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude-3-5", CostUSD: 2.0, CallCount: 1},
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, TeamID: &teamB, EventTime: hour.Add(15 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude-3-5", CostUSD: 4.0, CallCount: 1},
		{ID: uuid.New(), TenantID: tenant2ID, AgentID: &agent2ID, TeamID: &teamA2, EventTime: hour.Add(20 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude-3-5", CostUSD: 8.0, CallCount: 1},
	}
	if err := s.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	// 1. RefreshEventRollupHour
	if err := s.RefreshEventRollupHour(ctx, hour); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}

	// 2. Breakdown group_by=team
	breakdown, err := s.GetEventBreakdown(ctx, store.UsageEventQuery{
		From:    hour,
		To:      hour.Add(time.Hour),
		GroupBy: "team",
	})
	if err != nil {
		t.Fatalf("GetEventBreakdown team: %v", err)
	}
	costMap := make(map[string]float64)
	for _, row := range breakdown {
		costMap[row.Key] = row.CostUSD
	}
	if costMap[""] != 1.0 || costMap[teamA.String()] != 2.0 || costMap[teamB.String()] != 4.0 {
		t.Fatalf("breakdown map = %+v, want empty:1.0, A:2.0, B:4.0", costMap)
	}

	// 3. Summary filter team_id=teamA
	summaryA, err := s.GetEventSummary(ctx, store.UsageEventQuery{
		From:   hour,
		To:     hour.Add(time.Hour),
		TeamID: &teamA,
	})
	if err != nil {
		t.Fatalf("GetEventSummary teamA: %v", err)
	}
	if summaryA.CostUSD != 2.0 {
		t.Fatalf("summaryA.CostUSD = %f, want 2.0", summaryA.CostUSD)
	}

	// 4. Changed grouping (simulating team delete / group key change)
	if _, err := db.Exec("UPDATE usage_events SET team_id = NULL WHERE team_id = $1", teamA); err != nil {
		t.Fatalf("update team_id to null: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, hour); err != nil {
		t.Fatalf("Refresh after team null: %v", err)
	}
	breakdownAfter, err := s.GetEventBreakdown(ctx, store.UsageEventQuery{
		From:    hour,
		To:      hour.Add(time.Hour),
		GroupBy: "team",
	})
	if err != nil {
		t.Fatalf("GetEventBreakdown after: %v", err)
	}
	costMapAfter := make(map[string]float64)
	for _, row := range breakdownAfter {
		costMapAfter[row.Key] = row.CostUSD
	}
	if costMapAfter[teamA.String()] != 0 {
		t.Fatalf("deleted team key %s still present: %f", teamA.String(), costMapAfter[teamA.String()])
	}
	if costMapAfter[""] != 3.0 { // 1.0 personal + 2.0 moved to NULL
		t.Fatalf("empty team key cost = %f, want 3.0", costMapAfter[""])
	}

	// 5. Concurrency: 2 parallel refreshes on the same hour must both succeed
	// and produce the correct totals (no double counting, no discarded errors).
	errCh := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { errCh <- s.RefreshEventRollupHour(ctx, hour) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Errorf("concurrent RefreshEventRollupHour: %v", err)
		}
	}
	breakdownConc, err := s.GetEventBreakdown(ctx, store.UsageEventQuery{
		From:    hour,
		To:      hour.Add(time.Hour),
		GroupBy: "team",
	})
	if err != nil {
		t.Fatalf("GetEventBreakdown after concurrent refresh: %v", err)
	}
	costMapConc := make(map[string]float64)
	for _, row := range breakdownConc {
		costMapConc[row.Key] = row.CostUSD
	}
	if costMapConc[""] != 3.0 || costMapConc[teamB.String()] != 4.0 {
		t.Fatalf("concurrent breakdown = %+v, want \"\":3.0 teamB:4.0", costMapConc)
	}
	if _, ok := costMapConc[teamA.String()]; ok {
		t.Fatalf("teamA key present after concurrent refresh: %+v", costMapConc)
	}
}
