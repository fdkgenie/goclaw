package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type mockPendingHTTPStore struct {
	entries            []store.PendingMessage
	compactedDeleteIDs []uuid.UUID
	compactedSummary   *store.PendingMessage

	// compacted is closed once Compact has finished. The compaction runs in a
	// background goroutine, so tests must wait on this channel instead of
	// sleeping — a sleep is not a happens-before edge and trips the race detector.
	compacted   chan struct{}
	compactOnce sync.Once
}

func (m *mockPendingHTTPStore) AppendBatch(ctx context.Context, msgs []store.PendingMessage) error {
	m.entries = append(m.entries, msgs...)
	return nil
}

func (m *mockPendingHTTPStore) ListByKey(ctx context.Context, channelName, historyKey string) ([]store.PendingMessage, error) {
	var matched []store.PendingMessage
	for _, e := range m.entries {
		if e.ChannelName == channelName && e.HistoryKey == historyKey {
			matched = append(matched, e)
		}
	}
	return matched, nil
}

func (m *mockPendingHTTPStore) DeleteByKey(ctx context.Context, channelName, historyKey string) error {
	var kept []store.PendingMessage
	for _, e := range m.entries {
		if !(e.ChannelName == channelName && e.HistoryKey == historyKey) {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	return nil
}

func (m *mockPendingHTTPStore) Compact(ctx context.Context, deleteIDs []uuid.UUID, summary *store.PendingMessage) error {
	m.compactedDeleteIDs = deleteIDs
	m.compactedSummary = summary

	delMap := make(map[uuid.UUID]bool, len(deleteIDs))
	for _, id := range deleteIDs {
		delMap[id] = true
	}

	var kept []store.PendingMessage
	for _, e := range m.entries {
		if !delMap[e.ID] {
			kept = append(kept, e)
		}
	}
	if summary != nil {
		kept = append([]store.PendingMessage{*summary}, kept...)
	}
	m.entries = kept
	if m.compacted != nil {
		m.compactOnce.Do(func() { close(m.compacted) })
	}
	return nil
}

func (m *mockPendingHTTPStore) DeleteStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockPendingHTTPStore) ListArchivedByKey(ctx context.Context, channelName, historyKey string, since time.Time, limit int) ([]store.ArchivedMessage, error) {
	return nil, nil
}

func (m *mockPendingHTTPStore) ListGroups(ctx context.Context) ([]store.PendingMessageGroup, error) {
	return nil, nil
}

func (m *mockPendingHTTPStore) CountAll(ctx context.Context) (int64, error) {
	return int64(len(m.entries)), nil
}

func (m *mockPendingHTTPStore) CountByKey(ctx context.Context, channelName, historyKey string) (int, error) {
	return len(m.entries), nil
}

func (m *mockPendingHTTPStore) ResolveGroupTitles(ctx context.Context, groups []store.PendingMessageGroup) (map[string]string, error) {
	return nil, nil
}

type mockPendingHTTPProvider struct {
	chatCalls int
}

func (m *mockPendingHTTPProvider) Chat(ctx context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	m.chatCalls++
	return &providers.ChatResponse{
		Content: "Summarized group messages",
	}, nil
}

func (m *mockPendingHTTPProvider) ChatStream(ctx context.Context, req providers.ChatRequest, onChunk func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return m.Chat(ctx, req)
}

func (m *mockPendingHTTPProvider) DefaultModel() string {
	return "mock-model"
}

func (m *mockPendingHTTPProvider) Name() string {
	return "mock-provider"
}

func TestHandleCompact_ManualTriggerCompactsRawMessages(t *testing.T) {
	tenantID := uuid.New()
	pendingStore := &mockPendingHTTPStore{
		entries: []store.PendingMessage{
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 1", Sender: "user1"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 2", Sender: "user2"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 3", Sender: "user1"},
		},
		compacted: make(chan struct{}),
	}

	reg := providers.NewRegistry(store.TenantIDFromContext)
	mockProv := &mockPendingHTTPProvider{}
	reg.RegisterForTenant(tenantID, mockProv)

	h := NewPendingMessagesHandler(pendingStore, nil, reg)
	h.SetProviderModel("mock-provider", "mock-model")

	body, _ := json.Marshal(map[string]any{
		"channel_name": "tg",
		"history_key":  "chat-1",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/pending-messages/compact", bytes.NewReader(body))
	req = req.WithContext(store.WithTenantID(req.Context(), tenantID))

	w := httptest.NewRecorder()
	h.handleCompact(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusAccepted)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res["status"] != "accepted" || res["method"] != "summarizing" {
		t.Fatalf("unexpected response: %+v", res)
	}

	// Wait for the background goroutine to finish the compaction. The channel
	// is closed by the store at the end of Compact, which gives the test a
	// happens-before edge on the compacted entries and the provider call count.
	select {
	case <-pendingStore.compacted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for background compaction to finish")
	}

	if mockProv.chatCalls != 1 {
		t.Fatalf("expected 1 LLM chat call, got %d", mockProv.chatCalls)
	}
	if len(pendingStore.entries) != 1 || !pendingStore.entries[0].IsSummary {
		t.Fatalf("expected 1 summary entry, got %+v", pendingStore.entries)
	}
}

func TestHandleCompact_EmptyGroup(t *testing.T) {
	tenantID := uuid.New()
	pendingStore := &mockPendingHTTPStore{}

	reg := providers.NewRegistry(store.TenantIDFromContext)
	mockProv := &mockPendingHTTPProvider{}
	reg.RegisterForTenant(tenantID, mockProv)

	h := NewPendingMessagesHandler(pendingStore, nil, reg)
	h.SetProviderModel("mock-provider", "mock-model")

	body, _ := json.Marshal(map[string]any{
		"channel_name": "tg",
		"history_key":  "chat-empty",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/pending-messages/compact", bytes.NewReader(body))
	req = req.WithContext(store.WithTenantID(req.Context(), tenantID))

	w := httptest.NewRecorder()
	h.handleCompact(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res["method"] != "none" {
		t.Fatalf("expected method none, got %v", res["method"])
	}
	if mockProv.chatCalls != 0 {
		t.Fatalf("expected 0 LLM calls, got %d", mockProv.chatCalls)
	}
}

func TestHandleCompact_AlreadyCompacted(t *testing.T) {
	tenantID := uuid.New()
	pendingStore := &mockPendingHTTPStore{
		entries: []store.PendingMessage{
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-summary", Body: "old summary", IsSummary: true},
		},
	}

	reg := providers.NewRegistry(store.TenantIDFromContext)
	mockProv := &mockPendingHTTPProvider{}
	reg.RegisterForTenant(tenantID, mockProv)

	h := NewPendingMessagesHandler(pendingStore, nil, reg)
	h.SetProviderModel("mock-provider", "mock-model")

	body, _ := json.Marshal(map[string]any{
		"channel_name": "tg",
		"history_key":  "chat-summary",
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/pending-messages/compact", bytes.NewReader(body))
	req = req.WithContext(store.WithTenantID(req.Context(), tenantID))

	w := httptest.NewRecorder()
	h.handleCompact(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res["method"] != "already_compacted" {
		t.Fatalf("expected method already_compacted, got %v", res["method"])
	}
	if mockProv.chatCalls != 0 {
		t.Fatalf("expected 0 LLM calls, got %d", mockProv.chatCalls)
	}
}
