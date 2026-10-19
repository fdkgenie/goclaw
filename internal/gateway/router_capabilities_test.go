package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// stubPairingStore answers pairing requests so the pending_pairing connect
// path can be exercised without a database.
type stubPairingStore struct {
	store.PairingStore
	code string
}

func (s *stubPairingStore) RequestPairing(ctx context.Context, senderID, channel, chatID, accountID string, metadata map[string]string) (string, error) {
	return s.code, nil
}

// connectResponse runs the connect method and returns the decoded response.
func connectResponse(t *testing.T, server *Server, client *Client, params map[string]any) *protocol.ResponseFrame {
	t.Helper()

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	req := &protocol.RequestFrame{ID: "req-connect", Method: protocol.MethodConnect, Params: raw}
	server.router.Handle(context.Background(), client, req)

	select {
	case data := <-client.send:
		var resp protocol.ResponseFrame
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		return &resp
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected a connect response frame")
		return nil
	}
}

// assertCapabilityAdvertised checks that the connect payload advertises every
// requested capability.
func assertCapabilityAdvertised(t *testing.T, payload any, want ...string) {
	t.Helper()

	body, ok := payload.(map[string]any)
	if !ok {
		t.Fatalf("connect payload is %T, want map", payload)
	}
	serverMeta, ok := body["server"].(map[string]any)
	if !ok {
		t.Fatalf("connect payload has no server object: %+v", body)
	}
	caps, ok := serverMeta["capabilities"].([]any)
	if !ok {
		t.Fatalf("server.capabilities is %T, want array", serverMeta["capabilities"])
	}
	advertised := make(map[string]bool, len(caps))
	for _, c := range caps {
		if s, ok := c.(string); ok {
			advertised[s] = true
		}
	}
	var missing []string
	for _, capability := range want {
		if !advertised[capability] {
			missing = append(missing, capability)
		}
	}
	if len(missing) > 0 {
		t.Errorf("capabilities not advertised: %v (got %v)", missing, caps)
	}
}

func TestConnectResponse_IncludesCapabilities(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Token = "test-token"

	server := NewServer(cfg, nil, nil, nil)
	client := NewClient(nil, server, "203.0.113.10")

	resp := connectResponse(t, server, client, map[string]any{
		"token":   "test-token",
		"user_id": "admin-user",
	})
	if !resp.OK {
		t.Fatalf("connect failed: %+v", resp.Error)
	}
	assertCapabilityAdvertised(t, resp.Payload,
		protocol.CapEventsCronHeartbeatTenantScoped,
		protocol.CapEventsTeamLinkTenantTagged,
		protocol.CapUsageEventsTeam,
		protocol.CapTeamsTasksListHasMore,
	)
}

func TestConnectResponse_PendingPairingIncludesCapabilities(t *testing.T) {
	cfg := config.Default()
	cfg.Gateway.Host = "127.0.0.1"
	cfg.Gateway.Token = "test-token"

	server := NewServer(cfg, nil, nil, nil)
	server.SetPairingService(&stubPairingStore{code: "ABCD1234"})
	client := NewClient(nil, server, "203.0.113.10")

	// No token → browser pairing path → pending_pairing response.
	resp := connectResponse(t, server, client, map[string]any{})
	if !resp.OK {
		t.Fatalf("connect failed: %+v", resp.Error)
	}
	body, ok := resp.Payload.(map[string]any)
	if !ok {
		t.Fatalf("connect payload is %T, want map", resp.Payload)
	}
	if body["status"] != "pending_pairing" {
		t.Fatalf("status = %v, want pending_pairing", body["status"])
	}
	assertCapabilityAdvertised(t, resp.Payload,
		protocol.CapEventsCronHeartbeatTenantScoped,
		protocol.CapEventsTeamLinkTenantTagged,
		protocol.CapUsageEventsTeam,
		protocol.CapTeamsTasksListHasMore,
	)
}
