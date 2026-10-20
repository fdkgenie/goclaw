//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestSQLiteUsageEventStoreRefreshRollupHourIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	eventStore := NewSQLiteUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	bucket := time.Date(2026, 6, 12, 8, 0, 0, 0, time.UTC)

	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID:           uuid.New(),
		TenantID:     store.MasterTenantID,
		EventTime:    bucket.Add(10 * time.Minute),
		EventType:    store.UsageEventTypeRuntimeToolCall,
		ResourceType: store.UsageResourceTypeRuntimeTool,
		ResourceName: "exec",
		ResourceID:   "exec",
		Source:       store.UsageSourceToolCall,
		Status:       "completed",
		InputTokens:  10,
		TotalTokens:  10,
		DurationMS:   50,
		CallCount:    1,
	}); err != nil {
		t.Fatalf("InsertEvent first: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("Refresh first: %v", err)
	}

	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID:           uuid.New(),
		TenantID:     store.MasterTenantID,
		EventTime:    bucket.Add(20 * time.Minute),
		EventType:    store.UsageEventTypeRuntimeToolCall,
		ResourceType: store.UsageResourceTypeRuntimeTool,
		ResourceName: "exec",
		ResourceID:   "exec",
		Source:       store.UsageSourceToolCall,
		Status:       "error",
		OutputTokens: 5,
		TotalTokens:  5,
		DurationMS:   150,
		CallCount:    1,
		ErrorCount:   1,
	}); err != nil {
		t.Fatalf("InsertEvent second: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("Refresh second: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("Refresh idempotent: %v", err)
	}

	summary, err := eventStore.GetEventSummary(ctx, store.UsageEventQuery{
		From:         bucket,
		To:           bucket.Add(time.Hour),
		ResourceType: store.UsageResourceTypeRuntimeTool,
	})
	if err != nil {
		t.Fatalf("GetEventSummary: %v", err)
	}
	if summary.Calls != 2 || summary.Errors != 1 {
		t.Fatalf("summary calls/errors = %d/%d, want 2/1", summary.Calls, summary.Errors)
	}
	if summary.TotalTokens != 15 {
		t.Fatalf("summary tokens = %d, want 15", summary.TotalTokens)
	}
	if summary.AvgDurationMS != 100 {
		t.Fatalf("summary avg duration = %d, want 100", summary.AvgDurationMS)
	}
}

func TestSQLiteUsageEventStore_CostByTeamAndReplacement(t *testing.T) {
	db := openTestDB(t) // openTestDB only opens the DB; EnsureSchema applies the full schema
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	eventStore := NewSQLiteUsageEventStore(db)
	tenantID := store.MasterTenantID
	ctx := store.WithTenantID(context.Background(), tenantID)
	bucket := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	teamA := uuid.New()
	teamB := uuid.New()

	// 1. Insert events with team dimension
	events := []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenantID, EventTime: bucket.Add(5 * time.Minute), BucketHour: bucket, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 1.0, CallCount: 1},
		{ID: uuid.New(), TenantID: tenantID, TeamID: &teamA, EventTime: bucket.Add(10 * time.Minute), BucketHour: bucket, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 2.0, CallCount: 1},
		{ID: uuid.New(), TenantID: tenantID, TeamID: &teamB, EventTime: bucket.Add(15 * time.Minute), BucketHour: bucket, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 4.0, CallCount: 1},
	}
	if err := eventStore.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}

	// 2. Breakdown group_by=team
	breakdown, err := eventStore.GetEventBreakdown(ctx, store.UsageEventQuery{
		From:    bucket,
		To:      bucket.Add(time.Hour),
		GroupBy: "team",
	})
	if err != nil {
		t.Fatalf("GetEventBreakdown: %v", err)
	}
	costMap := make(map[string]float64)
	for _, row := range breakdown {
		costMap[row.Key] = row.CostUSD
	}
	if costMap[""] != 1.0 || costMap[teamA.String()] != 2.0 || costMap[teamB.String()] != 4.0 {
		t.Fatalf("breakdown map = %+v, want empty:1.0, A:2.0, B:4.0", costMap)
	}

	// 3. Summary filter team_id
	summaryA, err := eventStore.GetEventSummary(ctx, store.UsageEventQuery{
		From:   bucket,
		To:     bucket.Add(time.Hour),
		TeamID: &teamA,
	})
	if err != nil {
		t.Fatalf("GetEventSummary teamA: %v", err)
	}
	if summaryA.CostUSD != 2.0 {
		t.Fatalf("summaryA.CostUSD = %f, want 2.0", summaryA.CostUSD)
	}

	// 4. Test whole-bucket replacement when group key changed
	if _, err := db.Exec("UPDATE usage_events SET team_id = NULL WHERE team_id = ?", teamA.String()); err != nil {
		t.Fatalf("update team_id to NULL: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("Refresh after group change: %v", err)
	}
	breakdownAfter, err := eventStore.GetEventBreakdown(ctx, store.UsageEventQuery{
		From:    bucket,
		To:      bucket.Add(time.Hour),
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
		t.Fatalf("teamA row still exists after reassignment: %f", costMapAfter[teamA.String()])
	}
	if costMapAfter[""] != 3.0 {
		t.Fatalf("empty team cost = %f, want 3.0", costMapAfter[""])
	}
	if costMapAfter[teamB.String()] != 4.0 {
		t.Fatalf("teamB cost after group change = %f, want 4.0", costMapAfter[teamB.String()])
	}

	// Tenant isolation: ctx tenant is store.MasterTenantID and no foreign rows exist.
	var foreign int
	if err := db.QueryRow(`SELECT COUNT(*) FROM usage_event_rollups WHERE tenant_id <> ?`, tenantID.String()).Scan(&foreign); err != nil {
		t.Fatalf("query tenant isolation: %v", err)
	}
	if foreign != 0 {
		t.Fatalf("found %d rollups outside tenant %s", foreign, tenantID)
	}
}

func sqliteRollupSnapshot(t *testing.T, db *sql.DB, tenantID uuid.UUID, bucket time.Time) (rows int, cost float64, id string) {
	t.Helper()
	prefix := bucket.UTC().Format("2006-01-02 15:04:05")
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost_usd), 0), COALESCE(MIN(id), '')
		FROM usage_event_rollups
		WHERE tenant_id = ?
		  AND substr(replace(bucket_hour, 'T', ' '), 1, 19) = ?`,
		tenantID.String(), prefix).Scan(&rows, &cost, &id); err != nil {
		t.Fatalf("rollup snapshot: %v", err)
	}
	return rows, cost, id
}

func TestSQLiteUsageEventStore_RefreshBoundariesAndEmptyBucket(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	tenant1 := store.MasterTenantID
	tenant2 := uuid.New()
	if _, err := db.Exec(`INSERT INTO tenants (id, name, slug, status) VALUES (?, 'T2', 't2-usage-boundary', 'active')`, tenant2.String()); err != nil {
		t.Fatalf("insert tenant2: %v", err)
	}

	prev := time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)
	cur := prev.Add(time.Hour)
	next := cur.Add(time.Hour)

	eventStore := NewSQLiteUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), tenant1)

	curID, tenant2CurID := uuid.New(), uuid.New()
	events := []store.UsageEvent{
		{ID: uuid.New(), TenantID: tenant1, EventTime: prev.Add(5 * time.Minute), BucketHour: prev, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 1, CallCount: 1},
		{ID: curID, TenantID: tenant1, EventTime: cur.Add(5 * time.Minute), BucketHour: cur, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 2, CallCount: 1},
		{ID: uuid.New(), TenantID: tenant1, EventTime: next.Add(5 * time.Minute), BucketHour: next, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 4, CallCount: 1},
		{ID: tenant2CurID, TenantID: tenant2, EventTime: cur.Add(10 * time.Minute), BucketHour: cur, EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 8, CallCount: 1},
	}
	if err := eventStore.InsertEvents(ctx, events); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	for _, bucket := range []time.Time{prev, cur, next} {
		if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
			t.Fatalf("Refresh %s: %v", bucket, err)
		}
	}

	prevRows, prevCost, prevID := sqliteRollupSnapshot(t, db, tenant1, prev)
	nextRows, nextCost, nextID := sqliteRollupSnapshot(t, db, tenant1, next)
	if prevRows != 1 || prevCost != 1 || nextRows != 1 || nextCost != 4 {
		t.Fatalf("initial neighbors = prev(%d,%v) next(%d,%v), want (1,1) and (1,4)", prevRows, prevCost, nextRows, nextCost)
	}

	if _, err := db.Exec(`UPDATE usage_events SET cost_usd = 5 WHERE id = ?`, curID.String()); err != nil {
		t.Fatalf("update current event: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, cur); err != nil {
		t.Fatalf("Refresh current: %v", err)
	}
	cur1Rows, cur1Cost, _ := sqliteRollupSnapshot(t, db, tenant1, cur)
	cur2Rows, cur2Cost, _ := sqliteRollupSnapshot(t, db, tenant2, cur)
	if cur1Rows != 1 || cur1Cost != 5 || cur2Rows != 1 || cur2Cost != 8 {
		t.Fatalf("current rollups = tenant1(%d,%v) tenant2(%d,%v), want (1,5) and (1,8)", cur1Rows, cur1Cost, cur2Rows, cur2Cost)
	}
	prevRows, prevCost, gotPrevID := sqliteRollupSnapshot(t, db, tenant1, prev)
	nextRows, nextCost, gotNextID := sqliteRollupSnapshot(t, db, tenant1, next)
	if prevRows != 1 || prevCost != 1 || gotPrevID != prevID {
		t.Fatalf("H-1 changed: rows=%d cost=%v id=%s, want rows=1 cost=1 id=%s", prevRows, prevCost, gotPrevID, prevID)
	}
	if nextRows != 1 || nextCost != 4 || gotNextID != nextID {
		t.Fatalf("H+1 changed: rows=%d cost=%v id=%s, want rows=1 cost=4 id=%s", nextRows, nextCost, gotNextID, nextID)
	}

	if _, err := db.Exec(`DELETE FROM usage_events WHERE id IN (?, ?)`, curID.String(), tenant2CurID.String()); err != nil {
		t.Fatalf("delete current events: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, cur); err != nil {
		t.Fatalf("Refresh empty current: %v", err)
	}
	if rows, _, _ := sqliteRollupSnapshot(t, db, tenant1, cur); rows != 0 {
		t.Fatalf("tenant1 current rollups = %d, want 0 after empty refresh", rows)
	}
	if rows, _, _ := sqliteRollupSnapshot(t, db, tenant2, cur); rows != 0 {
		t.Fatalf("tenant2 current rollups = %d, want 0 after empty refresh", rows)
	}
	if _, _, gotPrevID := sqliteRollupSnapshot(t, db, tenant1, prev); gotPrevID != prevID {
		t.Fatalf("H-1 changed after empty refresh: id=%s, want %s", gotPrevID, prevID)
	}
	if _, _, gotNextID := sqliteRollupSnapshot(t, db, tenant1, next); gotNextID != nextID {
		t.Fatalf("H+1 changed after empty refresh: id=%s, want %s", gotNextID, nextID)
	}
}

func TestSQLiteUsageEventStore_RefreshRollbackKeepsPriorRollups(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	eventStore := NewSQLiteUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	bucket := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)

	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID: uuid.New(), TenantID: store.MasterTenantID, EventTime: bucket.Add(5 * time.Minute), BucketHour: bucket,
		EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 2, CallCount: 1,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	wantRows, wantCost, wantID := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket)

	if _, err := db.Exec(`DROP TRIGGER IF EXISTS fail_usage_rollup_insert`); err != nil {
		t.Fatalf("drop stale trigger: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TRIGGER IF EXISTS fail_usage_rollup_insert`)
	})
	if _, err := db.Exec(`CREATE TRIGGER fail_usage_rollup_insert BEFORE INSERT ON usage_event_rollups
		BEGIN
			SELECT RAISE(ABORT, 'injected rollup insert failure');
		END`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}

	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID: uuid.New(), TenantID: store.MasterTenantID, EventTime: bucket.Add(20 * time.Minute), BucketHour: bucket,
		EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 5, CallCount: 1,
	}); err != nil {
		t.Fatalf("InsertEvent second: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err == nil {
		t.Fatal("refresh should fail after injected insert error")
	}

	gotRows, gotCost, gotID := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket)
	if gotRows != wantRows || gotCost != wantCost || gotID != wantID {
		t.Fatalf("rollback left rows=%d cost=%v id=%s, want rows=%d cost=%v id=%s", gotRows, gotCost, gotID, wantRows, wantCost, wantID)
	}

	if _, err := db.Exec(`DROP TRIGGER IF EXISTS fail_usage_rollup_insert`); err != nil {
		t.Fatalf("drop failure trigger: %v", err)
	}
	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("refresh after trigger removal: %v", err)
	}
	if rows, cost, _ := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket); rows != 1 || cost != 7 {
		t.Fatalf("post-recovery rollups = (%d,%v), want (1,7)", rows, cost)
	}
}

func TestSQLiteUsageEventStore_RefreshDeletesLegacyBucketFormats(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	eventStore := NewSQLiteUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	bucket := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)

	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID: uuid.New(), TenantID: store.MasterTenantID, EventTime: bucket.Add(5 * time.Minute), BucketHour: bucket,
		EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 2, CallCount: 1,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	seedLegacy := func(bucketHour string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_event_rollups
			(id, tenant_id, bucket_hour, event_type, resource_type, resource_name, source, channel, provider, model, status, cost_usd, call_count)
			VALUES (?, ?, ?, 'tool_call', 'tool', 'bash', '', '', '', '', 'completed', 99, 1)`,
			uuid.NewString(), store.MasterTenantID.String(), bucketHour); err != nil {
			t.Fatalf("seed legacy rollup %q: %v", bucketHour, err)
		}
	}
	seedLegacy("2026-06-12T11:00:00Z")
	seedLegacy("2026-06-12T11:00:00+07:00")

	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}
	if rows, cost, _ := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket); rows != 1 || cost != 2 {
		t.Fatalf("legacy formats survived refresh: rows=%d cost=%v, want rows=1 cost=2", rows, cost)
	}
}
