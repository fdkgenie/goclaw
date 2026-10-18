package channels

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/internal/providers"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

type mockCompactPendingStore struct {
	entries []store.PendingMessage
	compactedDeleteIDs []uuid.UUID
	compactedSummary   *store.PendingMessage
}

func (m *mockCompactPendingStore) AppendBatch(ctx context.Context, msgs []store.PendingMessage) error {
	m.entries = append(m.entries, msgs...)
	return nil
}

func (m *mockCompactPendingStore) ListByKey(ctx context.Context, channelName, historyKey string) ([]store.PendingMessage, error) {
	var matched []store.PendingMessage
	for _, e := range m.entries {
		if e.ChannelName == channelName && e.HistoryKey == historyKey {
			matched = append(matched, e)
		}
	}
	return matched, nil
}

func (m *mockCompactPendingStore) DeleteByKey(ctx context.Context, channelName, historyKey string) error {
	var kept []store.PendingMessage
	for _, e := range m.entries {
		if !(e.ChannelName == channelName && e.HistoryKey == historyKey) {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	return nil
}

func (m *mockCompactPendingStore) Compact(ctx context.Context, deleteIDs []uuid.UUID, summary *store.PendingMessage) error {
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
	return nil
}

func (m *mockCompactPendingStore) DeleteStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}

func (m *mockCompactPendingStore) ListArchivedByKey(ctx context.Context, channelName, historyKey string, since time.Time, limit int) ([]store.ArchivedMessage, error) {
	return nil, nil
}

func (m *mockCompactPendingStore) ListGroups(ctx context.Context) ([]store.PendingMessageGroup, error) {
	return nil, nil
}

func (m *mockCompactPendingStore) CountAll(ctx context.Context) (int64, error) {
	return int64(len(m.entries)), nil
}

func (m *mockCompactPendingStore) CountByKey(ctx context.Context, channelName, historyKey string) (int, error) {
	count := 0
	for _, e := range m.entries {
		if e.ChannelName == channelName && e.HistoryKey == historyKey {
			count++
		}
	}
	return count, nil
}

func (m *mockCompactPendingStore) ResolveGroupTitles(ctx context.Context, groups []store.PendingMessageGroup) (map[string]string, error) {
	return nil, nil
}

type mockCompactProvider struct {
	chatCalls int
	lastReq   providers.ChatRequest
}

func (m *mockCompactProvider) Chat(ctx context.Context, req providers.ChatRequest) (*providers.ChatResponse, error) {
	m.chatCalls++
	m.lastReq = req
	return &providers.ChatResponse{
		Content: "Summarized conversation content",
	}, nil
}

func (m *mockCompactProvider) ChatStream(ctx context.Context, req providers.ChatRequest, onChunk func(providers.StreamChunk)) (*providers.ChatResponse, error) {
	return m.Chat(ctx, req)
}

func (m *mockCompactProvider) DefaultModel() string {
	return "gpt-4o"
}

func (m *mockCompactProvider) Name() string {
	return "mock-llm"
}

func TestCompactGroup_KeepRecentZero_SummarizesAll(t *testing.T) {
	st := &mockCompactPendingStore{
		entries: []store.PendingMessage{
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 1", Sender: "Alice"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 2", Sender: "Bob"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "msg 3", Sender: "Alice"},
		},
	}
	prov := &mockCompactProvider{}

	rem, err := CompactGroup(context.Background(), st, "tg", "chat-1", prov, "gpt-4o", 0, 4096, nil)
	if err != nil {
		t.Fatalf("CompactGroup failed: %v", err)
	}

	if prov.chatCalls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", prov.chatCalls)
	}
	if rem != 1 {
		t.Fatalf("expected remaining count 1, got %d", rem)
	}
	if len(st.compactedDeleteIDs) != 3 {
		t.Fatalf("expected 3 deleted IDs, got %d", len(st.compactedDeleteIDs))
	}
	if st.compactedSummary == nil || st.compactedSummary.Body != "Summarized conversation content" {
		t.Fatalf("summary not recorded properly: %+v", st.compactedSummary)
	}
}

func TestCompactGroup_KeepRecentPartial(t *testing.T) {
	st := &mockCompactPendingStore{
		entries: []store.PendingMessage{
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "old 1", Sender: "Alice"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "old 2", Sender: "Bob"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "recent 1", Sender: "Alice"},
			{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "recent 2", Sender: "Bob"},
		},
	}
	prov := &mockCompactProvider{}

	rem, err := CompactGroup(context.Background(), st, "tg", "chat-1", prov, "gpt-4o", 2, 4096, nil)
	if err != nil {
		t.Fatalf("CompactGroup failed: %v", err)
	}

	if prov.chatCalls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", prov.chatCalls)
	}
	if rem != 3 { // 1 summary + 2 kept = 3
		t.Fatalf("expected remaining count 3, got %d", rem)
	}
	if len(st.compactedDeleteIDs) != 2 {
		t.Fatalf("expected 2 deleted IDs, got %d", len(st.compactedDeleteIDs))
	}
}

func TestCompactGroup_EmptyOrAlreadySummarized(t *testing.T) {
	st := &mockCompactPendingStore{}
	prov := &mockCompactProvider{}

	// Empty
	rem, err := CompactGroup(context.Background(), st, "tg", "chat-1", prov, "gpt-4o", 0, 4096, nil)
	if err != nil || rem != 0 {
		t.Fatalf("expected 0 remaining, got %d, err: %v", rem, err)
	}
	if prov.chatCalls != 0 {
		t.Fatalf("expected 0 LLM calls, got %d", prov.chatCalls)
	}

	// Single summary already
	st.entries = []store.PendingMessage{
		{ID: uuid.New(), ChannelName: "tg", HistoryKey: "chat-1", Body: "existing summary", IsSummary: true},
	}
	rem, err = CompactGroup(context.Background(), st, "tg", "chat-1", prov, "gpt-4o", 0, 4096, nil)
	if err != nil || rem != 1 {
		t.Fatalf("expected 1 remaining, got %d, err: %v", rem, err)
	}
	if prov.chatCalls != 0 {
		t.Fatalf("expected 0 LLM calls for already compacted group, got %d", prov.chatCalls)
	}
}
