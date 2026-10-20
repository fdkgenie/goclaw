package live

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

// TestHubDownIsTerminal proves the down state is terminal for the live
// stream: once the hub publishes the "down" delta, subscribers must not
// receive any further data frame.
func TestHubDownIsTerminal(t *testing.T) {
	state := NewState()
	hub := NewHub(state)

	_, ch, unsub := hub.Subscribe()
	defer unsub()

	hub.ApplyAndPublish(goclaw.AgentEventPayload{RunID: "r1", Event: "run.started"})
	select {
	case d := <-ch:
		if d.Kind != "event" {
			t.Fatalf("delta kind = %q, want event", d.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected an event delta")
	}

	hub.MarkDegradedAndPublish("gateway disconnected")
	select {
	case d := <-ch:
		if d.Kind != "down" {
			t.Fatalf("delta kind = %q, want down", d.Kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a down delta")
	}

	// Anything published after the down frame must not reach the subscriber.
	hub.ApplyAndPublish(goclaw.AgentEventPayload{RunID: "r2", Event: "run.started"})
	hub.PublishDelta(Delta{Kind: "merge"})
	hub.MergeAndPublish([]TraceData{{RunID: "r3", TraceID: "t3"}}, true)

	select {
	case d := <-ch:
		t.Fatalf("received %+v after the down frame, want no further data frames", d)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestHubServeHTTPClosesAfterDownFrame asserts the SSE stream itself emits
// the terminal down frame and then closes: no data frame may follow it.
func TestHubServeHTTPClosesAfterDownFrame(t *testing.T) {
	state := NewState()
	hub := NewHub(state)

	srv := httptest.NewServer(hub)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("get stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, want 200", resp.StatusCode)
	}

	frames := make(chan string, 64)
	go func() {
		defer close(frames)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			frames <- line
		}
	}()

	// The initial snapshot frame.
	select {
	case f := <-frames:
		if !strings.Contains(f, `"snapshot"`) {
			t.Fatalf("first frame = %q, want the snapshot frame", f)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for the snapshot frame")
	}

	hub.MarkDegradedAndPublish("gateway disconnected")

	var downSeen bool
	deadline := time.After(5 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				// The stream closed: the down frame was the last frame.
				if !downSeen {
					t.Fatal("stream closed without a terminal down frame")
				}
				return
			}
			if strings.Contains(f, `"kind":"down"`) {
				if downSeen {
					t.Fatal("received more than one down frame")
				}
				downSeen = true
				continue
			}
			if downSeen {
				t.Fatalf("received a data frame after the down frame: %q", f)
			}
			t.Fatalf("received a data frame before the down frame: %q", f)
		case <-deadline:
			t.Fatal("stream did not close after the down frame")
		}
	}
}
