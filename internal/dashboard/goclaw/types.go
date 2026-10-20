// Package goclaw provides the dashboard's client-side access to the GoClaw
// Gateway: a long-lived WebSocket for RPC + event streaming, an HTTP client
// with Bearer auth + tenant pinning, and a tenant canary for fail-closed
// security.
//
// IMPORTANT: this package does NOT import internal/store or any other
// gateway-internal package. It depends only on pkg/protocol, stdlib, and
// third-party modules. Gateway is the single source of truth; the dashboard
// never touches the database directly.
package goclaw

import (
	"time"

	"github.com/google/uuid"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// RequiredCapabilities lists the gateway capabilities the dashboard requires
// at connect handshake. All are exported by pkg/protocol (B1 + B2 contracts).
var RequiredCapabilities = []string{
	protocol.CapEventsCronHeartbeatTenantScoped,
	protocol.CapEventsTeamLinkTenantTagged,
	protocol.CapUsageEventsTeam,
	protocol.CapTeamsTasksListHasMore,
}

// ConnectResponse is the dashboard-owned DTO for the WS connect response.
type ConnectResponse struct {
	Protocol   int        `json:"protocol"`
	Role       string     `json:"role"`
	UserID     string     `json:"user_id"`
	TenantID   string     `json:"tenant_id"`
	IsOwner    bool       `json:"is_owner"`
	Edition    string     `json:"edition"`
	ServerInfo ServerInfo `json:"server"`
	TenantName string     `json:"tenant_name,omitempty"`
	TenantSlug string     `json:"tenant_slug,omitempty"`
}

// ServerInfo describes the gateway as advertised in connect response.
type ServerInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// AgentEventPayload is an agent lifecycle event pushed via WS.
type AgentEventPayload struct {
	RunID     string    `json:"run_id"`
	TraceID   string    `json:"trace_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	TeamID    string    `json:"team_id,omitempty"`
	Event     string    `json:"event"`
	Tool      string    `json:"tool,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// TraceStatusPayload is a trace status event pushed via WS.
type TraceStatusPayload struct {
	TraceID   string    `json:"trace_id"`
	RunID     string    `json:"run_id,omitempty"`
	Status    string    `json:"status"`
	TenantID  string    `json:"tenant_id,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// HasCapability reports whether the gateway advertised the given capability.
func HasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// ValidateConnect checks the connect response against fail-closed security
// rules. Returns error describing the first violation.
func ValidateConnect(resp ConnectResponse, expectedTenant uuid.UUID) error {
	if resp.Role != "admin" {
		return &ConnectError{Code: "invalid_role", Message: "expected role admin, got " + resp.Role}
	}
	if resp.IsOwner {
		return &ConnectError{Code: "owner_not_allowed", Message: "dashboard must not connect as owner"}
	}
	if resp.TenantID != expectedTenant.String() {
		return &ConnectError{Code: "tenant_mismatch", Message: "connect tenant " + resp.TenantID + " != expected " + expectedTenant.String()}
	}
	for _, want := range RequiredCapabilities {
		if !HasCapability(resp.ServerInfo.Capabilities, want) {
			return &ConnectError{Code: "missing_capability", Message: "missing capability " + want}
		}
	} // close for loop
	return nil
}

// ConnectError is a connect-handshake failure.
type ConnectError struct {
	Code    string
	Message string
}

func (e *ConnectError) Error() string {
	return e.Code + ": " + e.Message
}

// IsCapabilityMissing reports whether err is a missing-capability connect error.
func IsCapabilityMissing(err error) bool {
	if e, ok := err.(*ConnectError); ok {
		return e.Code == "missing_capability"
	}
	return false
}
