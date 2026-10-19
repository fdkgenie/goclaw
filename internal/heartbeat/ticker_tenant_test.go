package heartbeat

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/agent"
	"github.com/nextlevelbuilder/goclaw/internal/scheduler"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

// tenantFakeHeartbeatStore implements the store calls the ticker makes.
type tenantFakeHeartbeatStore struct {
	store.HeartbeatStore
	logs []*store.HeartbeatRunLog
}

func (f *tenantFakeHeartbeatStore) Get(ctx context.Context, agentID uuid.UUID) (*store.AgentHeartbeat, error) {
	return nil, nil
}

func (f *tenantFakeHeartbeatStore) ListDue(ctx context.Context, now time.Time) ([]store.AgentHeartbeat, error) {
	return nil, nil
}

func (f *tenantFakeHeartbeatStore) InsertLog(ctx context.Context, log *store.HeartbeatRunLog) error {
	f.logs = append(f.logs, log)
	return nil
}

func (f *tenantFakeHeartbeatStore) UpdateState(ctx context.Context, id uuid.UUID, state store.HeartbeatState) error {
	return nil
}

// tenantFakeAgentStore resolves one agent and returns a fixed checklist.
type tenantFakeAgentStore struct {
	store.AgentStore
	agent     *store.AgentData
	checklist string
}

func (f *tenantFakeAgentStore) GetByIDUnscoped(ctx context.Context, id uuid.UUID) (*store.AgentData, error) {
	return f.agent, nil
}

func (f *tenantFakeAgentStore) GetAgentContextFiles(ctx context.Context, agentID uuid.UUID) ([]store.AgentContextFileData, error) {
	if f.checklist == "" {
		return nil, nil
	}
	return []store.AgentContextFileData{{AgentID: agentID, FileName: "HEARTBEAT.md", Content: f.checklist}}, nil
}

func newTenantTicker(t *testing.T, tenantID uuid.UUID, checklist string) (*Ticker, *[]store.HeartbeatEvent, uuid.UUID) {
	t.Helper()

	agentID := uuid.New()
	ticker := NewTicker(TickerConfig{
		Store: &tenantFakeHeartbeatStore{},
		Agents: &tenantFakeAgentStore{
			agent: &store.AgentData{
				BaseModel: store.BaseModel{ID: agentID},
				AgentKey:  "tenant-agent",
				TenantID:  tenantID,
			},
			checklist: checklist,
		},
		// The ticker calls RunAgent without a nil check; return a completed run.
		RunAgent: func(ctx context.Context, req agent.RunRequest) <-chan scheduler.RunOutcome {
			ch := make(chan scheduler.RunOutcome, 1)
			ch <- scheduler.RunOutcome{Result: &agent.RunResult{Content: "heartbeat done"}}
			return ch
		},
	})

	var events []store.HeartbeatEvent
	ticker.SetOnEvent(func(ev store.HeartbeatEvent) { events = append(events, ev) })

	return ticker, &events, agentID
}

func TestTicker_EmitPopulatesTenantID(t *testing.T) {
	tenantID := uuid.New()
	ticker, events, agentID := newTenantTicker(t, tenantID, "checklist content")

	hb := store.AgentHeartbeat{
		ID:          uuid.New(),
		AgentID:     agentID,
		Enabled:     true,
		IntervalSec: 60,
	}

	ticker.runOne(context.Background(), hb)

	if len(*events) < 2 {
		t.Fatalf("expected running + final events, got %d (%+v)", len(*events), *events)
	}
	for i, ev := range *events {
		if ev.TenantID != tenantID {
			t.Errorf("event %d (%s) TenantID = %v, want %v", i, ev.Action, ev.TenantID, tenantID)
		}
	}
	if (*events)[0].Action != "running" {
		t.Errorf("first event action = %q, want %q", (*events)[0].Action, "running")
	}
}

func TestTicker_SkippedEventCarriesTenantID(t *testing.T) {
	tenantID := uuid.New()
	// Empty checklist → the run is skipped before the agent loop.
	ticker, events, agentID := newTenantTicker(t, tenantID, "")

	hb := store.AgentHeartbeat{
		ID:          uuid.New(),
		AgentID:     agentID,
		Enabled:     true,
		IntervalSec: 60,
	}

	ticker.runOne(context.Background(), hb)

	if len(*events) != 1 {
		t.Fatalf("expected 1 skipped event, got %d (%+v)", len(*events), *events)
	}
	if (*events)[0].Action != "skipped" {
		t.Errorf("event action = %q, want %q", (*events)[0].Action, "skipped")
	}
	if (*events)[0].TenantID != tenantID {
		t.Errorf("skipped event TenantID = %v, want %v", (*events)[0].TenantID, tenantID)
	}
}

func TestTicker_EmitFallsBackToMasterTenant(t *testing.T) {
	// An agent without a tenant falls back to the master tenant instead of
	// emitting a zero-tenant (unscoped) event.
	ticker, events, agentID := newTenantTicker(t, uuid.Nil, "")

	hb := store.AgentHeartbeat{
		ID:          uuid.New(),
		AgentID:     agentID,
		Enabled:     true,
		IntervalSec: 60,
	}

	ticker.runOne(context.Background(), hb)

	if len(*events) != 1 {
		t.Fatalf("expected 1 skipped event, got %d (%+v)", len(*events), *events)
	}
	if (*events)[0].TenantID != store.MasterTenantID {
		t.Errorf("event TenantID = %v, want MasterTenantID %v", (*events)[0].TenantID, store.MasterTenantID)
	}
}
