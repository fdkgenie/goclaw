//go:build integration

package pg

import (
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
)

func TestMigration000100_UpAndDown(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db := hooksTestDB(t) // fresh DB migrated to head (100 once B2 lands)

	tenant1, agent1 := seedTenantAndAgent(t, db)
	tenant2, agent2 := seedTenantAndAgent(t, db) // required: usage_events.tenant_id FK → tenants

	m, err := migrate.New("file://../../../migrations", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()

	// Force back to v99 so the following Up() exercises 000100.
	if err := m.Migrate(99); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 99: %v", err)
	}

	hour := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	teamA, teamB, teamA2 := uuid.New(), uuid.New(), uuid.New()

	insert := func(id, tenantID, agentID uuid.UUID, teamID *uuid.UUID, cost float64, at time.Time) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_events
			(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, agent_id, team_id, cost_usd, call_count)
			VALUES ($1,$2,$3,$4,'llm_call','model','claude-3-5',$5,$6,$7,1)`,
			id, tenantID, at, hour, agentID, teamID, cost); err != nil {
			t.Fatalf("insert usage_event: %v", err)
		}
	}
	insert(uuid.New(), tenant1, agent1, nil, 1, hour.Add(5*time.Minute))
	insert(uuid.New(), tenant1, agent1, &teamA, 2, hour.Add(10*time.Minute))
	insert(uuid.New(), tenant1, agent1, &teamB, 4, hour.Add(15*time.Minute))
	insert(uuid.New(), tenant2, agent2, &teamA2, 8, hour.Add(20*time.Minute))

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up to 100: %v", err)
	}

	rollupsByTenant := func(tenantID uuid.UUID) map[string]float64 {
		t.Helper()
		rows, err := db.Query(`SELECT COALESCE(team_id::text, ''), SUM(cost_usd)
			FROM usage_event_rollups WHERE tenant_id = $1 GROUP BY team_id`, tenantID)
		if err != nil {
			t.Fatalf("query rollups: %v", err)
		}
		defer rows.Close()
		out := map[string]float64{}
		for rows.Next() {
			var key string
			var cost float64
			if err := rows.Scan(&key, &cost); err != nil {
				t.Fatalf("scan rollup: %v", err)
			}
			out[key] = cost
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("rows: %v", err)
		}
		return out
	}

	t1 := rollupsByTenant(tenant1)
	if t1[""] != 1 || t1[teamA.String()] != 2 || t1[teamB.String()] != 4 {
		t.Fatalf("tenant1 rollups = %#v, want \"\":1 teamA:2 teamB:4", t1)
	}
	for k := range t1 {
		if k != "" && k != teamA.String() && k != teamB.String() {
			t.Fatalf("tenant1 leaked foreign team key %q", k)
		}
	}
	t2 := rollupsByTenant(tenant2)
	if len(t2) != 1 || t2[teamA2.String()] != 8 {
		t.Fatalf("tenant2 rollups = %#v, want only teamA2:8", t2)
	}

	// Down migration must drop team_id.
	if err := m.Steps(-1); err != nil {
		t.Fatalf("migrate down one step: %v", err)
	}
	var colCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'usage_event_rollups' AND column_name = 'team_id'`).Scan(&colCount); err != nil {
		t.Fatalf("query information_schema: %v", err)
	}
	if colCount != 0 {
		t.Fatalf("team_id column still present after down migration")
	}

	// Up migration restores the column and rebuilds rollups.
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up after down: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_name = 'usage_event_rollups' AND column_name = 'team_id'`).Scan(&colCount); err != nil {
		t.Fatalf("query information_schema after up: %v", err)
	}
	if colCount != 1 {
		t.Fatalf("team_id column missing after re-up")
	}
	afterUp := rollupsByTenant(tenant1)
	if afterUp[""] != 1 || afterUp[teamA.String()] != 2 || afterUp[teamB.String()] != 4 {
		t.Fatalf("tenant1 rollups after re-up = %#v, want \"\":1 teamA:2 teamB:4", afterUp)
	}
}

func TestMigration000100_CutoffSkipsOpenHour(t *testing.T) {
	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db := hooksTestDB(t)
	tenantID, agentID := seedTenantAndAgent(t, db)
	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_events WHERE tenant_id = $1", tenantID)
		db.Exec("DELETE FROM usage_event_rollups WHERE tenant_id = $1", tenantID)
	})

	m, err := migrate.New("file://../../../migrations", os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("migrate.New: %v", err)
	}
	defer m.Close()
	if err := m.Migrate(99); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate to 99: %v", err)
	}

	closed := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
	open := closed.Add(time.Hour)
	insert := func(bucket time.Time, cost float64) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO usage_events
			(id, tenant_id, event_time, bucket_hour, event_type, resource_type, resource_name, agent_id, cost_usd, call_count)
			VALUES ($1,$2,$3,$4,'llm_call','model','claude-3-5',$5,$6,1)`,
			uuid.New(), tenantID, bucket.Add(5*time.Minute), bucket, agentID, cost); err != nil {
			t.Fatalf("insert usage_event: %v", err)
		}
	}
	insert(closed, 2)
	insert(open, 8)

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up to 100: %v", err)
	}

	count := func(bucket time.Time) int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM usage_event_rollups
			WHERE tenant_id = $1 AND bucket_hour = $2`, tenantID, bucket).Scan(&n); err != nil {
			t.Fatalf("count rollups: %v", err)
		}
		return n
	}
	if got := count(closed); got != 1 {
		t.Fatalf("closed-hour rollups = %d, want 1", got)
	}
	if got := count(open); got != 0 {
		t.Fatalf("open-hour rollups = %d, want 0 (migration cutoff)", got)
	}
}
