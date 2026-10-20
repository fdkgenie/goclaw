//go:build integration

package pg

import (
	"context"
	"database/sql"
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

func pgRollupSnapshot(t *testing.T, db *sql.DB, tenantID uuid.UUID, bucket time.Time) (rows int, cost float64, id string) {
	t.Helper()
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost_usd), 0), COALESCE(MIN(id::text), '')
		FROM usage_event_rollups
		WHERE tenant_id = $1 AND bucket_hour = $2`, tenantID, bucket).Scan(&rows, &cost, &id); err != nil {
		t.Fatalf("rollup snapshot: %v", err)
	}
	return rows, cost, id
}

func TestPGUsageEventStore_RefreshBoundariesAndEmptyBucket(t *testing.T) {
	db := hooksTestDB(t)
	tenant1, agent1 := seedTenantAndAgent(t, db)
	tenant2, agent2 := seedTenantAndAgent(t, db)

	prev := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)
	cur := prev.Add(time.Hour)
	next := cur.Add(time.Hour)
	teamA := uuid.New()

	s := NewPGUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), tenant1)
	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_events WHERE tenant_id IN ($1, $2)", tenant1, tenant2)
		db.Exec("DELETE FROM usage_event_rollups WHERE tenant_id IN ($1, $2)", tenant1, tenant2)
	})

	curEventID := uuid.New()
	events := []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenant1, AgentID: &agent1, EventTime: prev.Add(5 * time.Minute), BucketHour: prev, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 1, CallCount: 1},
		{ID: curEventID, TenantID: tenant1, AgentID: &agent1, TeamID: &teamA, EventTime: cur.Add(5 * time.Minute), BucketHour: cur, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 2, CallCount: 1},
		{ID: uuid.New(), TenantID: tenant1, AgentID: &agent1, EventTime: next.Add(5 * time.Minute), BucketHour: next, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 4, CallCount: 1},
		{ID: uuid.New(), TenantID: tenant2, AgentID: &agent2, EventTime: cur.Add(10 * time.Minute), BucketHour: cur, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 8, CallCount: 1},
	}
	if err := s.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	for _, bucket := range []time.Time{prev, cur, next} {
		if err := s.RefreshEventRollupHour(ctx, bucket); err != nil {
			t.Fatalf("Refresh %s: %v", bucket, err)
		}
	}

	prevRows, prevCost, prevID := pgRollupSnapshot(t, db, tenant1, prev)
	nextRows, nextCost, nextID := pgRollupSnapshot(t, db, tenant1, next)
	if prevRows != 1 || prevCost != 1 || nextRows != 1 || nextCost != 4 {
		t.Fatalf("initial neighbors = prev(%d,%v) next(%d,%v), want (1,1) and (1,4)", prevRows, prevCost, nextRows, nextCost)
	}

	// A refresh of H must not touch H-1/H+1, but must rebuild both tenants in H.
	if _, err := db.Exec("UPDATE usage_events SET cost_usd = 5 WHERE id = $1", curEventID); err != nil {
		t.Fatalf("update current event: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, cur); err != nil {
		t.Fatalf("Refresh current: %v", err)
	}
	cur1Rows, cur1Cost, _ := pgRollupSnapshot(t, db, tenant1, cur)
	cur2Rows, cur2Cost, _ := pgRollupSnapshot(t, db, tenant2, cur)
	if cur1Rows != 1 || cur1Cost != 5 || cur2Rows != 1 || cur2Cost != 8 {
		t.Fatalf("current rollups = tenant1(%d,%v) tenant2(%d,%v), want (1,5) and (1,8)", cur1Rows, cur1Cost, cur2Rows, cur2Cost)
	}
	prevRows, prevCost, gotPrevID := pgRollupSnapshot(t, db, tenant1, prev)
	nextRows, nextCost, gotNextID := pgRollupSnapshot(t, db, tenant1, next)
	if prevRows != 1 || prevCost != 1 || gotPrevID != prevID {
		t.Fatalf("H-1 changed: rows=%d cost=%v id=%s, want rows=1 cost=1 id=%s", prevRows, prevCost, gotPrevID, prevID)
	}
	if nextRows != 1 || nextCost != 4 || gotNextID != nextID {
		t.Fatalf("H+1 changed: rows=%d cost=%v id=%s, want rows=1 cost=4 id=%s", nextRows, nextCost, gotNextID, nextID)
	}

	// Empty bucket: deleting all events for H removes H's rollups only.
	if _, err := db.Exec("DELETE FROM usage_events WHERE bucket_hour = $1 AND tenant_id IN ($2, $3)", cur, tenant1, tenant2); err != nil {
		t.Fatalf("delete current events: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, cur); err != nil {
		t.Fatalf("Refresh empty current: %v", err)
	}
	if rows, _, _ := pgRollupSnapshot(t, db, tenant1, cur); rows != 0 {
		t.Fatalf("tenant1 current rollups = %d, want 0 after empty refresh", rows)
	}
	if rows, _, _ := pgRollupSnapshot(t, db, tenant2, cur); rows != 0 {
		t.Fatalf("tenant2 current rollups = %d, want 0 after empty refresh", rows)
	}
	if _, _, gotPrevID := pgRollupSnapshot(t, db, tenant1, prev); gotPrevID != prevID {
		t.Fatalf("H-1 changed after empty refresh: id=%s, want %s", gotPrevID, prevID)
	}
	if _, _, gotNextID := pgRollupSnapshot(t, db, tenant1, next); gotNextID != nextID {
		t.Fatalf("H+1 changed after empty refresh: id=%s, want %s", gotNextID, nextID)
	}
}

func TestPGUsageEventStore_RefreshRollbackKeepsPriorRollups(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	hour := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)

	s := NewPGUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_events WHERE tenant_id = $1", tenantID)
		db.Exec("DELETE FROM usage_event_rollups WHERE tenant_id = $1", tenantID)
	})

	if err := s.InsertEvents(ctx, []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, EventTime: hour.Add(5 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 2, CallCount: 1},
	}); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, hour); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	wantRows, wantCost, wantID := pgRollupSnapshot(t, db, tenantID, hour)

	if _, err := db.Exec(`CREATE OR REPLACE FUNCTION goclaw_test_fail_rollup_insert() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			RAISE EXCEPTION 'injected rollup insert failure';
		END;
	$$`); err != nil {
		t.Fatalf("create failure function: %v", err)
	}
	t.Cleanup(func() {
		db.Exec("DROP TRIGGER IF EXISTS goclaw_test_fail_rollup_insert ON usage_event_rollups")
		db.Exec("DROP FUNCTION IF EXISTS goclaw_test_fail_rollup_insert()")
	})
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS goclaw_test_fail_rollup_insert ON usage_event_rollups`); err != nil {
		t.Fatalf("drop stale failure trigger: %v", err)
	}
	if _, err := db.Exec(`CREATE TRIGGER goclaw_test_fail_rollup_insert BEFORE INSERT ON usage_event_rollups
		FOR EACH ROW EXECUTE FUNCTION goclaw_test_fail_rollup_insert()`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if err := s.InsertEvents(ctx, []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, EventTime: hour.Add(20 * time.Minute), BucketHour: hour, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 5, CallCount: 1},
	}); err != nil {
		t.Fatalf("InsertEvents second: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, hour); err == nil {
		t.Fatal("refresh should fail after injected insert error")
	}

	gotRows, gotCost, gotID := pgRollupSnapshot(t, db, tenantID, hour)
	if gotRows != wantRows || gotCost != wantCost || gotID != wantID {
		t.Fatalf("rollback left rows=%d cost=%v id=%s, want rows=%d cost=%v id=%s", gotRows, gotCost, gotID, wantRows, wantCost, wantID)
	}

	if _, err := db.Exec("DROP TRIGGER IF EXISTS goclaw_test_fail_rollup_insert ON usage_event_rollups"); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, hour); err != nil {
		t.Fatalf("refresh after trigger removal: %v", err)
	}
	if rows, cost, _ := pgRollupSnapshot(t, db, tenantID, hour); rows != 1 || cost != 7 {
		t.Fatalf("post-recovery rollups = (%d,%v), want (1,7)", rows, cost)
	}
}

func TestPGUsageEventStore_RefreshNormalizesNonUTCInput(t *testing.T) {
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	ict := time.FixedZone("ICT", 7*60*60)
	bucketUTC := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	bucketICT := bucketUTC.In(ict)

	s := NewPGUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), tenantID)
	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_events WHERE tenant_id = $1", tenantID)
		db.Exec("DELETE FROM usage_event_rollups WHERE tenant_id = $1", tenantID)
	})

	if err := s.InsertEvents(ctx, []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenantID, AgentID: &agentID, EventTime: bucketICT.Add(5 * time.Minute), BucketHour: bucketICT, EventType: "llm_call", ResourceType: "model", ResourceName: "claude", CostUSD: 3, CallCount: 1},
	}); err != nil {
		t.Fatalf("InsertEvents non-UTC: %v", err)
	}
	if err := s.RefreshEventRollupHour(ctx, bucketUTC); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}
	if rows, cost, _ := pgRollupSnapshot(t, db, tenantID, bucketUTC); rows != 1 || cost != 3 {
		t.Fatalf("non-UTC input rollups = (%d,%v), want (1,3)", rows, cost)
	}
}
