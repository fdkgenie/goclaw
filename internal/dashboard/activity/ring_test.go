package activity

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
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
	items []ActivityItem
	err   error
}

func (f *fakeHTTP) Get(ctx context.Context, path string, out any) error {
	if f.err != nil {
		return f.err
	}
	resp, ok := out.(*activityResponse)
	if !ok {
		return errors.New("unexpected out type")
	}
	resp.Items = f.items
	return nil
}

func TestRingBackfillDedup(t *testing.T) {
	r := NewRingBuffer()
	// Live stream already has item "x".
	r.Add(item("x", TypeAgentEvent, "live"))

	fake := &fakeHTTP{items: []ActivityItem{
		item("x", TypeAgentEvent, "live"), // duplicate
		item("y", TypeAuditLog, "audit"),
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
