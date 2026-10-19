//go:build sqlite || sqliteonly

package sqlitestore

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestSQLiteCronStore_EmitPopulatesTenantID(t *testing.T) {
	cronStore, ctx, db := newTestSQLiteCronStore(t)

	tenantID := uuid.New()
	if _, err := db.Exec(
		`INSERT INTO tenants (id, name, slug, status) VALUES (?,?,?,'active')`,
		tenantID.String(), "cron-tenant-"+tenantID.String()[:8], "ct"+tenantID.String()[:8]); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	ctx = store.WithTenantID(ctx, tenantID)

	var received []store.CronEvent
	cronStore.SetOnEvent(func(evt store.CronEvent) { received = append(received, evt) })
	cronStore.SetOnJob(func(job *store.CronJob) (*store.CronJobResult, error) {
		return &store.CronJobResult{Content: "ok"}, nil
	})

	everyMS := int64(time.Hour / time.Millisecond)
	job, err := cronStore.AddJob(ctx, "tenant-event-job", store.CronSchedule{
		Kind:    "every",
		EveryMS: &everyMS,
	}, "ping", false, "", "", "", "user-1")
	if err != nil {
		t.Fatalf("AddJob error: %v", err)
	}

	if _, _, err := cronStore.RunJob(ctx, job.ID, true); err != nil {
		t.Fatalf("RunJob error: %v", err)
	}

	if len(received) < 2 {
		t.Fatalf("expected running+completed events, got %d", len(received))
	}
	for i, ev := range received {
		if ev.TenantID != tenantID {
			t.Errorf("event %d (%s) TenantID = %v, want %v", i, ev.Action, ev.TenantID, tenantID)
		}
	}
}
