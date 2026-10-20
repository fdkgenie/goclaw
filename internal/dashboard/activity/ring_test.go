package activity

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

func item(id string, typ ActivityType, summary string) ActivityItem {
	return ActivityItem{ID: id, Type: typ, Timestamp: time.Now(), Summary: summary}
}

func TestRingPushAndReverseOrder(t *testing.T) {
	r := NewRingBuffer()
	t1 := item("a", TypeAgentEvent, "first")
	t1.Timestamp = time.Now().Add(-2 * time.Minute)
	t2 := item("b", TypeAgentEvent, "second")
	t2.Timestamp = time.Now().Add(-1 * time.Minute)

	if !r.Add(t1) {
		t.Fatal("first add should succeed")
	}
	if !r.Add(t2) {
		t.Fatal("second add should succeed")
	}

	list := r.List(10)
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
	if list[0].ID != "b" || list[1].ID != "a" {
		t.Fatalf("expected reverse chronological [b,a], got [%s,%s]", list[0].ID, list[1].ID)
	}
}

func TestRingCapAndEvict(t *testing.T) {
	r := NewRingBuffer()
	for i := 0; i < 600; i++ {
		r.Add(item(strconv.Itoa(i), TypeAgentEvent, "item"))
	}
	if r.List(1000) != nil && len(r.List(1000)) != 500 {
		t.Fatalf("list = %d, want 500 (cap)", len(r.List(1000)))
	}
}

func TestRingDedup(t *testing.T) {
	r := NewRingBuffer()
	it := item("dup", TypeAgentEvent, "dup")
	if !r.Add(it) {
		t.Fatal("first add should succeed")
	}
	if r.Add(it) {
		t.Fatal("duplicate add should be ignored")
	}
	if r.List(10) != nil && len(r.List(10)) != 1 {
		t.Fatalf("list = %d, want 1 after dedup", len(r.List(10)))
	}
}

type fakeHTTP struct {
	logs []gatewayActivityLog
	err  error
}

func (f *fakeHTTP) Get(ctx context.Context, path string, out any) error {
	if f.err != nil {
		return f.err
	}
	resp, ok := out.(*activityResponse)
	if !ok {
		return errors.New("unexpected out type")
	}
	resp.Logs = f.logs
	return nil
}

func auditLog(id, action string) gatewayActivityLog {
	return gatewayActivityLog{
		ID:         id,
		ActorType:  "user",
		ActorID:    "dashboard",
		Action:     action,
		EntityType: "agent",
		EntityID:   "agent-1",
		CreatedAt:  time.Now(),
	}
}

func TestRingBackfillDedup(t *testing.T) {
	r := NewRingBuffer()
	// Live stream already has item "x".
	r.Add(item("x", TypeAgentEvent, "live"))

	fake := &fakeHTTP{logs: []gatewayActivityLog{
		auditLog("x", "live"), // duplicate
		auditLog("y", "audit"),
	}}
	if err := r.Backfill(context.Background(), fake); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	ids := map[string]bool{}
	for _, it := range r.List(10) {
		ids[it.ID] = true
	}
	if !ids["x"] || !ids["y"] {
		t.Fatalf("expected x and y, got %v", ids)
	}
	// x must appear only once.
	count := 0
	for _, it := range r.List(10) {
		if it.ID == "x" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("x count = %d, want 1 (dedup against live)", count)
	}
}

// TestRingBackfillDecodesGatewayLogs pins the gateway contract of
// /v1/activity: the audit rows are returned under "logs". The dashboard used
// to look for "items", so the whole history was silently dropped.
func TestRingBackfillDecodesGatewayLogs(t *testing.T) {
	r := NewRingBuffer()
	log := auditLog("log-1", "agent.created")
	log.CreatedAt = time.Now().Add(-time.Minute)

	fake := &fakeHTTP{logs: []gatewayActivityLog{log}}
	if err := r.Backfill(context.Background(), fake); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	list := r.List(10)
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1 (the gateway rows must be decoded)", len(list))
	}
	if list[0].ID != "log-1" {
		t.Errorf("id = %q, want log-1", list[0].ID)
	}
	if list[0].Type != TypeAuditLog {
		t.Errorf("type = %q, want %q", list[0].Type, TypeAuditLog)
	}
	if list[0].Summary == "" {
		t.Error("summary is empty, want the audit action")
	}
	if list[0].Timestamp.IsZero() {
		t.Error("timestamp is zero, want the gateway created_at")
	}
}

func TestRingGapMarker(t *testing.T) {
	r := NewRingBuffer()
	gapTime := time.Now()
	r.InsertGapMarker(gapTime)

	list := r.List(10)
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1 (gap marker)", len(list))
	}
	if list[0].Type != TypeGap {
		t.Fatalf("type = %s, want gap", list[0].Type)
	}
	if !list[0].Timestamp.Equal(gapTime) {
		t.Fatalf("timestamp = %v, want %v", list[0].Timestamp, gapTime)
	}
}

func TestRingBackfillError(t *testing.T) {
	r := NewRingBuffer()
	fake := &fakeHTTP{err: errors.New("boom")}
	if err := r.Backfill(context.Background(), fake); err == nil {
		t.Fatal("expected Backfill to propagate HTTP error")
	}
}

// fakeRPC answers teams.events.list with canned team events.
type fakeRPC struct {
	events []teamTaskEvent
	err    error
	calls  []map[string]any
}

func (f *fakeRPC) Call(ctx context.Context, method string, params any) (*goclaw.ResponseFrame, error) {
	if f.err != nil {
		return nil, f.err
	}
	if method != "teams.events.list" {
		return &goclaw.ResponseFrame{OK: true, Payload: map[string]any{}}, nil
	}
	p, _ := params.(map[string]any)
	f.calls = append(f.calls, p)
	return &goclaw.ResponseFrame{
		OK: true,
		Payload: map[string]any{
			"events": f.events,
			"count":  len(f.events),
		},
	}, nil
}

// TestBackfillTeamsLoadsTaskEvents pins the team history backfill: the task
// events of every team are loaded from the gateway so the feed shows history
// and not only the live stream.
func TestBackfillTeamsLoadsTaskEvents(t *testing.T) {
	r := NewRingBuffer()
	ev := teamTaskEvent{
		ID:        "ev-1",
		TaskID:    "task-1",
		EventType: "task_created",
		ActorID:   "agent-1",
		CreatedAt: time.Now(),
	}
	rpc := &fakeRPC{events: []teamTaskEvent{ev}}

	if err := r.BackfillTeams(context.Background(), rpc, []string{"team-a", ""}); err != nil {
		t.Fatalf("BackfillTeams: %v", err)
	}
	if len(rpc.calls) != 1 {
		t.Fatalf("teams.events.list calls = %d, want 1 (the empty team is skipped)", len(rpc.calls))
	}
	if rpc.calls[0]["team_id"] != "team-a" {
		t.Errorf("team_id = %v, want team-a", rpc.calls[0]["team_id"])
	}

	list := r.List(10)
	if len(list) != 1 {
		t.Fatalf("items = %d, want 1", len(list))
	}
	if list[0].Type != TypeTaskEvent {
		t.Errorf("type = %q, want %q", list[0].Type, TypeTaskEvent)
	}
	if list[0].TeamID != "team-a" {
		t.Errorf("team_id = %q, want team-a", list[0].TeamID)
	}
	if list[0].Summary == "" {
		t.Error("summary is empty, want the event type")
	}
}

// TestBackfillTeamsErrorIsReported pins the fail-soft contract: a failed
// call is reported but the other teams are still loaded.
func TestBackfillTeamsErrorIsReported(t *testing.T) {
	r := NewRingBuffer()
	rpc := &fakeRPC{err: errors.New("boom")}
	if err := r.BackfillTeams(context.Background(), rpc, []string{"team-a"}); err == nil {
		t.Fatal("expected BackfillTeams to report the error")
	}
}

// TestFromTeamTaskEvent pins the decoding of the team.task.* WS payload.
func TestFromTeamTaskEvent(t *testing.T) {
	it, ok := FromTeamTaskEvent("team.task.created", map[string]any{
		"team_id":   "team-a",
		"task_id":   "task-1",
		"status":    "in_progress",
		"subject":   "Ship it",
		"actor_id":  "agent-1",
		"timestamp": "2026-10-19T10:00:00Z",
	})
	if !ok {
		t.Fatal("expected the event to be decoded")
	}
	if it.Type != TypeTaskEvent {
		t.Errorf("type = %q, want %q", it.Type, TypeTaskEvent)
	}
	if it.TeamID != "team-a" {
		t.Errorf("team_id = %q, want team-a", it.TeamID)
	}
	if it.AgentID != "agent-1" {
		t.Errorf("agent_id = %q, want agent-1", it.AgentID)
	}
	if it.Timestamp.IsZero() {
		t.Error("timestamp is zero, want the gateway timestamp")
	}
	if it.ID == "" {
		t.Error("id is empty, want a persistent id for the dedup")
	}

	// A payload without a task is not a feed item.
	if _, ok := FromTeamTaskEvent("team.task.created", map[string]any{"team_id": "team-a"}); ok {
		t.Error("expected a payload without task_id to be rejected")
	}
}
