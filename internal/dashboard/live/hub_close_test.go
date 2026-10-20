package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

// TestHubSlowConsumerDropThenUnsubscribeClosesOnce proves the subscriber
// channel has a single close owner: the hub drops a slow consumer and closes
// its channel, and the later unsubscribe must not close it again (which would
// panic with "close of closed channel").
func TestHubSlowConsumerDropThenUnsubscribeClosesOnce(t *testing.T) {
	state := NewState()
	hub := NewHub(state)

	_, ch, unsub := hub.Subscribe()

	// Fill the buffer to simulate a slow consumer with no reader.
	for i := 0; i < subscriberChannelCap; i++ {
		ch <- Delta{Kind: "x"}
	}

	// The publish drops the slow consumer and closes its channel.
	hub.PublishDelta(Delta{Kind: "event"})

	// Unsubscribing afterwards must not close the channel a second time.
	unsub()
	unsub()

	// The channel is closed: draining it terminates.
	drained := 0
	for range ch {
		drained++
		if drained > subscriberChannelCap+1 {
			t.Fatal("channel never closed")
		}
	}
	if drained != subscriberChannelCap {
		t.Fatalf("drained %d items, want %d", drained, subscriberChannelCap)
	}
}

// TestHubUnsubscribeTwiceIsSafe proves the unsubscribe path itself is
// idempotent: calling it twice must not panic.
func TestHubUnsubscribeTwiceIsSafe(t *testing.T) {
	state := NewState()
	hub := NewHub(state)

	_, _, unsub := hub.Subscribe()
	unsub()
	unsub()

	// Publishing afterwards must not panic or block.
	hub.PublishDelta(Delta{Kind: "event"})
}

// blockingWriter blocks every write until release is closed, simulating an
// SSE client that stopped reading.
type blockingWriter struct {
	mu      sync.Mutex
	header  http.Header
	release chan struct{}
}

func newBlockingWriter() *blockingWriter {
	return &blockingWriter{header: make(http.Header), release: make(chan struct{})}
}

func (w *blockingWriter) Header() http.Header { return w.header }

func (w *blockingWriter) Write(p []byte) (int, error) {
	<-w.release
	return len(p), nil
}

func (w *blockingWriter) WriteHeader(int) {}

func (w *blockingWriter) Flush() {}

// TestHubServeHTTPSlowConsumerDoesNotDoubleClose drives the production path:
// the SSE handler unsubscribes when the request ends, and the hub may have
// already dropped the very same subscriber as a slow consumer. Both paths
// used to close the channel, which panicked with "close of closed channel".
func TestHubServeHTTPSlowConsumerDoesNotDoubleClose(t *testing.T) {
	state := NewState()
	hub := NewHub(state)

	w := newBlockingWriter()
	req := httptest.NewRequest(http.MethodGet, "/api/stream", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		hub.ServeHTTP(w, req)
	}()

	// The handler registers the subscriber, then blocks writing the snapshot.
	time.Sleep(100 * time.Millisecond)

	// The handler never drains its channel, so it is a slow consumer: fill the
	// buffer and publish once more so the hub drops and closes it.
	for i := 0; i < subscriberChannelCap+1; i++ {
		hub.PublishDelta(Delta{Kind: "event"})
	}

	// Let the handler finish; it unsubscribes on the way out.
	close(w.release)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SSE handler did not return")
	}

	// The hub is still usable.
	hub.ApplyAndPublish(goclaw.AgentEventPayload{RunID: "r1", Event: "run.started"})
	if hub.Version() == 0 {
		t.Fatal("expected version to advance")
	}
}
