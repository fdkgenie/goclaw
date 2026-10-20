//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
	"database/sql"
	"strings"
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
	prefix := sqliteCanonicalTime(bucket)
	if err := db.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(cost_usd), 0), COALESCE(MIN(id), '')
		FROM usage_event_rollups
		WHERE tenant_id = ?
		  AND bucket_hour = ?`,
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

// Stored bucket_hour is canonical UTC text, so a same-bucket refresh replaces
// the whole bucket with a plain index-usable equality delete.
func TestSQLiteUsageEventStore_RefreshReplacesExistingBucketRollup(t *testing.T) {
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

	// Stale row for the same canonical bucket but a group key not present in
	// the current events; refresh must delete it via plain bucket_hour equality.
	if _, err := db.Exec(`INSERT INTO usage_event_rollups
		(id, tenant_id, bucket_hour, event_type, resource_type, resource_name, source, channel, provider, model, status, cost_usd, call_count)
		VALUES (?, ?, ?, 'tool_call', 'tool', 'stale', '', '', '', '', 'completed', 99, 1)`,
		uuid.NewString(), store.MasterTenantID.String(), sqliteCanonicalTime(bucket)); err != nil {
		t.Fatalf("seed stale rollup: %v", err)
	}

	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}
	if rows, cost, _ := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket); rows != 1 || cost != 2 {
		t.Fatalf("stale bucket row survived refresh: rows=%d cost=%v, want rows=1 cost=2", rows, cost)
	}
}

func TestSQLiteUsageEventStore_RefreshIncludesMixedEventTimeFormats(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	eventStore := NewSQLiteUsageEventStore(db)
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	bucket := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)

	// Driver form, written through the store.
	if err := eventStore.InsertEvent(ctx, &store.UsageEvent{
		ID: uuid.New(), TenantID: store.MasterTenantID, EventTime: bucket.Add(5 * time.Minute), BucketHour: bucket,
		EventType: "tool_call", ResourceType: "tool", ResourceName: "bash", CostUSD: 2, CallCount: 1,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	// Legacy RFC3339 Z form in the same bucket.
	if _, err := db.Exec(`INSERT INTO usage_events
		(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, status, cost_usd, call_count)
		VALUES (?, ?, '2026-06-12T11:10:00Z', '2026-06-12T11:00:00Z', 'tool_call', 'tool', 'bash', 'completed', 3, 1)`,
		uuid.NewString(), store.MasterTenantID.String()); err != nil {
		t.Fatalf("seed RFC3339 event: %v", err)
	}

	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}
	if rows, cost, _ := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket); rows != 1 || cost != 5 {
		t.Fatalf("mixed event formats rollups = rows=%d cost=%v, want rows=1 cost=5", rows, cost)
	}
}

func TestSQLiteUsageEventStore_RefreshNormalizesRFC3339Offset(t *testing.T) {
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
	// 18:10+07:00 == 11:10Z. Both event_time and bucket_hour carry the offset.
	if _, err := db.Exec(`INSERT INTO usage_events
		(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, status, cost_usd, call_count)
		VALUES (?, ?, '2026-06-12T18:10:00+07:00', '2026-06-12T18:00:00+07:00', 'tool_call', 'tool', 'bash', 'completed', 4, 1)`,
		uuid.NewString(), store.MasterTenantID.String()); err != nil {
		t.Fatalf("seed RFC3339 offset event: %v", err)
	}

	if err := eventStore.RefreshEventRollupHour(ctx, bucket); err != nil {
		t.Fatalf("RefreshEventRollupHour: %v", err)
	}
	if rows, cost, _ := sqliteRollupSnapshot(t, db, store.MasterTenantID, bucket); rows != 1 || cost != 6 {
		t.Fatalf("offset event rollups = rows=%d cost=%v, want rows=1 cost=6", rows, cost)
	}
	var storedBucket string
	if err := db.QueryRow(`SELECT bucket_hour FROM usage_event_rollups WHERE tenant_id = ?`, store.MasterTenantID.String()).Scan(&storedBucket); err != nil {
		t.Fatalf("query rollup bucket_hour: %v", err)
	}
	if want := sqliteCanonicalTime(bucket); storedBucket != want {
		t.Fatalf("rollup bucket_hour = %q, want canonical %q", storedBucket, want)
	}
}

// Rollup reads must keep bucket_hour unwrapped so SQLite can range-seek
// idx_usage_event_rollups_tenant_hour instead of scanning every tenant rollup.
func TestSQLiteUsageEventStore_RollupReadsUseTenantHourIndex(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	ctx := store.WithTenantID(context.Background(), store.MasterTenantID)
	from := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		build func() (string, []any)
	}{
		{"timeseries", func() (string, []any) {
			return sqliteUsageEventTimeSeriesQuery(ctx, store.UsageEventQuery{From: from, To: to})
		}},
		{"timeseries-day", func() (string, []any) {
			return sqliteUsageEventTimeSeriesQuery(ctx, store.UsageEventQuery{From: from, To: to, GroupBy: "day"})
		}},
		{"breakdown", func() (string, []any) {
			return sqliteUsageEventBreakdownQuery(ctx, store.UsageEventQuery{From: from, To: to, GroupBy: "model"})
		}},
		{"breakdown-resource", func() (string, []any) {
			return sqliteUsageEventBreakdownQuery(ctx, store.UsageEventQuery{From: from, To: to})
		}},
		{"summary", func() (string, []any) {
			return sqliteUsageEventSummaryQuery(ctx, store.UsageEventQuery{From: from, To: to})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			query, args := tc.build()
			rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
			if err != nil {
				t.Fatalf("explain %s: %v", tc.name, err)
			}
			defer rows.Close()

			var details []string
			for rows.Next() {
				var id, parent, notused int
				var detail string
				if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
					t.Fatalf("scan plan: %v", err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatalf("plan rows: %v", err)
			}

			joined := strings.Join(details, "\n")
			if !strings.Contains(joined, "USING INDEX idx_usage_event_rollups_tenant_hour") {
				t.Fatalf("%s plan does not seek the tenant+hour index:\n%s", tc.name, joined)
			}
			for _, detail := range details {
				if strings.HasPrefix(detail, "SCAN usage_event_rollups") {
					t.Fatalf("%s plan full-scans rollups:\n%s", tc.name, joined)
				}
			}
		})
	}
}
