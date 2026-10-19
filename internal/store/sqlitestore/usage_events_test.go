//go:build sqlite || sqliteonly

package sqlitestore

import (
	"context"
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
