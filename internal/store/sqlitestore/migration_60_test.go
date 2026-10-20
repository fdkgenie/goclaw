//go:build sqlite || sqliteonly

package sqlitestore

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestSQLiteMigration60to61(t *testing.T) {
	db := openTestDBAtVersion(t, 60) // full schema, post-60 changes undone, version = 60
	tenantID := store.MasterTenantID

	bucket := time.Date(2026, 6, 12, 11, 0, 0, 0, time.UTC)
	teamA, teamB := uuid.New(), uuid.New()

	insert := func(teamID *uuid.UUID, cost float64, at time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_events
			(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, team_id, cost_usd, call_count)
			VALUES (?, ?, ?, ?, 'tool_call', 'tool', 'bash', ?, ?, 1)`,
			uuid.New().String(), tenantID.String(), at.Format(time.RFC3339), bucket.Format(time.RFC3339), teamID, cost); err != nil {
			t.Fatalf("insert usage_event: %v", err)
		}
	}
	insert(nil, 1, bucket.Add(5*time.Minute))
	insert(&teamA, 2, bucket.Add(10*time.Minute))
	insert(&teamB, 4, bucket.Add(15*time.Minute))

	// Runs migration 60 only because schema_version is 60.
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	var version int
	if err := db.QueryRow("SELECT version FROM schema_version").Scan(&version); err != nil {
		t.Fatalf("query schema_version: %v", err)
	}
	if version != 61 {
		t.Fatalf("schema_version = %d, want 61", version)
	}

	rows, err := db.Query(`SELECT COALESCE(team_id, ''), SUM(cost_usd)
		FROM usage_event_rollups WHERE tenant_id = ? GROUP BY team_id`, tenantID.String())
	if err != nil {
		t.Fatalf("query rollups: %v", err)
	}
	defer rows.Close()
	got := map[string]float64{}
	for rows.Next() {
		var key string
		var cost float64
		if err := rows.Scan(&key, &cost); err != nil {
			t.Fatalf("scan rollup: %v", err)
		}
		got[key] = cost
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if got[""] != 1 || got[teamA.String()] != 2 || got[teamB.String()] != 4 {
		t.Fatalf("rollups = %#v, want \"\":1 teamA:2 teamB:4", got)
	}

	// New unique index must include team_id. SQLite reports expression columns
	// with a NULL name, so verify the index definition itself.
	var uniqueIndexSQL string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_usage_event_rollups_unique'`).Scan(&uniqueIndexSQL); err != nil {
		t.Fatalf("query unique index definition: %v", err)
	}
	if !strings.Contains(uniqueIndexSQL, "COALESCE(team_id") {
		t.Fatalf("unique index definition = %q, want team_id included", uniqueIndexSQL)
	}

	var idxCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_usage_event_rollups_tenant_team_hour'`).Scan(&idxCount); err != nil {
		t.Fatalf("query index: %v", err)
	}
	if idxCount != 1 {
		t.Fatalf("idx_usage_event_rollups_tenant_team_hour missing")
	}
}

func TestSQLiteMigration60CutoffSkipsOpenHour(t *testing.T) {
	db := openTestDBAtVersion(t, 60)
	tenantID := store.MasterTenantID

	closed := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	open := closed.Add(time.Hour)
	insert := func(bucket time.Time, cost float64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_events
			(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, cost_usd, call_count)
			VALUES (?, ?, ?, ?, 'tool_call', 'tool', 'bash', ?, 1)`,
			uuid.New().String(), tenantID.String(),
			bucket.Add(5*time.Minute).Format(time.RFC3339),
			bucket.Format(time.RFC3339), cost); err != nil {
			t.Fatalf("insert usage_event: %v", err)
		}
	}
	ict := time.FixedZone("ICT", 7*60*60)
	insertOffset := func(bucket time.Time, cost float64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_events
			(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, cost_usd, call_count)
			VALUES (?, ?, ?, ?, 'tool_call', 'tool', 'bash', ?, 1)`,
			uuid.New().String(), tenantID.String(),
			bucket.Add(5*time.Minute).In(ict).Format(time.RFC3339),
			bucket.In(ict).Format(time.RFC3339), cost); err != nil {
			t.Fatalf("insert offset usage_event: %v", err)
		}
	}
	insert(closed, 2)
	insertOffset(closed, 3)
	insert(open, 8)
	insertOffset(open, 9)

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}

	count := func(bucket time.Time) (int, float64) {
		t.Helper()
		var rows int
		var cost float64
		if err := db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost_usd), 0)
			FROM usage_event_rollups
			WHERE tenant_id = ?
			  AND substr(replace(bucket_hour, 'T', ' '), 1, 19) = ?`,
			tenantID.String(), bucket.Format("2006-01-02 15:04:05")).Scan(&rows, &cost); err != nil {
			t.Fatalf("count rollups: %v", err)
		}
		return rows, cost
	}
	if rows, cost := count(closed); rows != 1 || cost != 5 {
		t.Fatalf("closed-hour rollups = (%d,%v), want (1,5)", rows, cost)
	}
	if rows, _ := count(open); rows != 0 {
		t.Fatalf("open-hour rollups = %d, want 0 (migration cutoff)", rows)
	}
}
