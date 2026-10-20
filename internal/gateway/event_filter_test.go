package gateway

import (
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

// makeClient builds a minimal Client for filter tests.
// conn and server are nil — only role/userID/tenantID fields are used.
func makeClient(role permissions.Role, userID string, tenantID uuid.UUID) *Client {
	c := &Client{}
	c.role = role
	c.userID = userID
	c.tenantID = tenantID
	return c
}

// makeEvent builds a bus.Event for testing.
func makeEvent(name string, tenantID uuid.UUID, payload any) bus.Event {
	return bus.Event{Name: name, TenantID: tenantID, Payload: payload}
}

var masterTenant = uuid.MustParse("00000000-0000-0000-0000-000000000001")
var otherTenant = uuid.MustParse("00000000-0000-0000-0000-000000000002")

// ---- isSystemEvent ----

func TestIsSystemEvent_KnownNames(t *testing.T) {
	systemEvents := []string{
		protocol.EventHealth,
		protocol.EventPresence,
		protocol.EventVoicewakeChanged,
		protocol.EventTick,
		protocol.EventShutdown,
		protocol.EventConnectChallenge,
		protocol.EventTalkMode,
	}
	for _, name := range systemEvents {
		if !isSystemEvent(name) {
			t.Errorf("isSystemEvent(%q) = false, want true", name)
		}
	}
}

func TestIsSystemEvent_UnknownName(t *testing.T) {
	if isSystemEvent("agent") {
		t.Error("isSystemEvent('agent') should return false")
	}
}

// ---- isAdminOnlyEvent ----

func TestIsAdminOnlyEvent_KnownAdminEvents(t *testing.T) {
	adminEvents := []string{
		protocol.EventNodePairRequested,
		protocol.EventDevicePairReq,
		protocol.EventAgentLinkCreated,
		protocol.EventWorkspaceFileChanged,
		protocol.EventHeartbeat,
	}
	for _, name := range adminEvents {
		if !isAdminOnlyEvent(name) {
			t.Errorf("isAdminOnlyEvent(%q) = false, want true", name)
		}
	}
}

func TestIsAdminOnlyEvent_NonAdminEvent(t *testing.T) {
	if isAdminOnlyEvent(protocol.EventAgent) {
		t.Error("isAdminOnlyEvent('agent') should return false")
	}
}

// ---- Internal event filtering (cache., audit.log) ----

func TestClientCanReceiveEvent_InternalCacheEvent_Blocked(t *testing.T) {
	c := makeClient(permissions.RoleAdmin, "admin", masterTenant)
	c.role = permissions.RoleOwner // even owner shouldn't get cache events
	evt := makeEvent("cache.invalidate", masterTenant, nil)
	if clientCanReceiveEvent(c, evt) {
		t.Error("cache.* events must never be forwarded to any client")
	}
}

func TestClientCanReceiveEvent_AuditLog_Blocked(t *testing.T) {
	c := makeClient(permissions.RoleOwner, "admin", masterTenant)
	evt := makeEvent(protocol.EventAuditLog, masterTenant, nil)
	if clientCanReceiveEvent(c, evt) {
		t.Error("audit.log events must never be forwarded")
	}
}

// ---- System events broadcast to everyone ----

func TestClientCanReceiveEvent_SystemEvent_AllowedToAnyClient(t *testing.T) {
	c := makeClient(permissions.RoleViewer, "viewer", masterTenant)
	evt := makeEvent(protocol.EventHealth, uuid.Nil, nil) // system events have no tenant
	// system events bypass tenant checks and go to everyone
	if !clientCanReceiveEvent(c, evt) {
		t.Error("system events (health) should reach all clients")
	}
}

// ---- Tenant isolation ----

func TestClientCanReceiveEvent_NoTenantOnClient_Blocked(t *testing.T) {
	c := makeClient(permissions.RoleAdmin, "admin", uuid.Nil) // no tenant assigned
	evt := makeEvent(protocol.EventAgent, masterTenant, nil)
	if clientCanReceiveEvent(c, evt) {
		t.Error("client with no tenant should not receive any non-system events")
	}
}

func TestClientCanReceiveEvent_TenantMismatch_Blocked(t *testing.T) {
	c := makeClient(permissions.RoleAdmin, "admin", masterTenant)
	evt := makeEvent(protocol.EventAgent, otherTenant, nil) // different tenant
	if clientCanReceiveEvent(c, evt) {
		t.Error("event from different tenant should be blocked")
	}
}

func TestClientCanReceiveEvent_UnscopedEvent_OnlyOwnerReceives(t *testing.T) {
	// Event with no tenant — only owner-role clients should get it.
	ownerClient := makeClient(permissions.RoleOwner, "owner", masterTenant)
	adminClient := makeClient(permissions.RoleAdmin, "admin", masterTenant)

	evt := makeEvent(protocol.EventAgent, uuid.Nil, nil) // no tenant on event

	if !clientCanReceiveEvent(ownerClient, evt) {
		t.Error("owner should receive unscoped events")
	}
	if clientCanReceiveEvent(adminClient, evt) {
		t.Error("non-owner admin should NOT receive unscoped events (fail-closed)")
	}
}

// ---- User-scoped events (agent, chat) ----

func TestClientCanReceiveEvent_AgentEvent_FilteredByUserID(t *testing.T) {
	userA := makeClient(permissions.RoleOperator, "user-a", masterTenant)
	userB := makeClient(permissions.RoleOperator, "user-b", masterTenant)

	evt := makeEvent(protocol.EventAgent, masterTenant, map[string]any{"userId": "user-a"})

	if !clientCanReceiveEvent(userA, evt) {
		t.Error("user-a should receive their own agent event")
	}
	if clientCanReceiveEvent(userB, evt) {
		t.Error("user-b should NOT receive user-a's agent event")
	}
}

func TestClientCanReceiveEvent_AgentEvent_AdminSeesAll(t *testing.T) {
	admin := makeClient(permissions.RoleAdmin, "admin", masterTenant)
	evt := makeEvent(protocol.EventAgent, masterTenant, map[string]any{"userId": "user-x"})
	if !clientCanReceiveEvent(admin, evt) {
		t.Error("admin should receive all agent events")
	}
}

// ---- Team events ----

func TestClientCanReceiveEvent_TeamEvent_FilteredByTeamID(t *testing.T) {
	c := makeClient(permissions.RoleOperator, "user", masterTenant)
	c.SetTeamAccess([]string{"team-1"})

	evtMyTeam := makeEvent("team.task.created", masterTenant, map[string]any{"team_id": "team-1"})
	evtOtherTeam := makeEvent("team.task.created", masterTenant, map[string]any{"team_id": "team-2"})

	if !clientCanReceiveEvent(c, evtMyTeam) {
		t.Error("user should receive events for their team")
	}
	if clientCanReceiveEvent(c, evtOtherTeam) {
		t.Error("user should NOT receive events for teams they don't belong to")
	}
}

// ---- Admin-only events ----

func TestClientCanReceiveEvent_AdminOnlyEvent_BlockedForNonAdmin(t *testing.T) {
	c := makeClient(permissions.RoleOperator, "user", masterTenant)
	// Admin-only events land in the default deny path for non-admin.
	evt := makeEvent(protocol.EventNodePairRequested, masterTenant, nil)
	if clientCanReceiveEvent(c, evt) {
		t.Error("admin-only events should not reach operator-role clients")
	}
}

// ---- Skill events broadcast ----

func TestClientCanReceiveEvent_SkillEvent_BroadcastToTenantClients(t *testing.T) {
	ownerClient := makeClient(permissions.RoleOwner, "owner", masterTenant)
	// skill.deps.checked is a skill event — should broadcast
	evt := makeEvent(protocol.EventSkillDepsChecked, uuid.Nil, nil)
	// owner receives unscoped skill events
	if !clientCanReceiveEvent(ownerClient, evt) {
		t.Error("owner should receive skill events")
	}
}

// ---- Tenant access revocation ----

func TestClientCanReceiveEvent_TenantAccessRevoked_DeliveredToCorrectUser(t *testing.T) {
	userA := makeClient(permissions.RoleOperator, "user-a", masterTenant)
	userB := makeClient(permissions.RoleOperator, "user-b", masterTenant)

	evt := makeEvent(protocol.EventTenantAccessRevoked, masterTenant, map[string]any{"user_id": "user-a"})

	if !clientCanReceiveEvent(userA, evt) {
		t.Error("revocation event should be delivered to the affected user")
	}
	if clientCanReceiveEvent(userB, evt) {
		t.Error("revocation event should NOT be delivered to a different user")
	}
}

// ---- extractMapField ----

func TestExtractMapField_StringAnyMap(t *testing.T) {
	m := map[string]any{"userId": "abc", "teamId": "team-1"}
	if got := extractMapField(m, "userId"); got != "abc" {
		t.Errorf("extractMapField = %q, want %q", got, "abc")
	}
}

func TestExtractMapField_StringStringMap(t *testing.T) {
	m := map[string]string{"user_id": "xyz"}
	if got := extractMapField(m, "user_id"); got != "xyz" {
		t.Errorf("extractMapField = %q, want %q", got, "xyz")
	}
}

func TestExtractMapField_MissingKey(t *testing.T) {
	m := map[string]any{"other": "value"}
	if got := extractMapField(m, "userId"); got != "" {
		t.Errorf("extractMapField missing key = %q, want empty", got)
	}
}

func TestExtractMapField_StructPayload_JSONFallback(t *testing.T) {
	type payload struct {
		UserID string `json:"userId"`
	}
	p := payload{UserID: "from-struct"}
	if got := extractMapField(p, "userId"); got != "from-struct" {
		t.Errorf("extractMapField JSON fallback = %q, want %q", got, "from-struct")
	}
}

// ---- MCP OAuth Complete events ----

func TestClientCanReceiveEvent_MCPOAuthComplete_RoutedToInitiatingUser(t *testing.T) {
	userA := makeClient(permissions.RoleOperator, "user-a", masterTenant)
	userB := makeClient(permissions.RoleOperator, "user-b", masterTenant)

	evt := makeEvent(protocol.EventMCPOAuthComplete, masterTenant, map[string]any{
		"serverId":         "server-1",
		"initiatingUserId": "user-a",
		"status":           "success",
	})

	if !clientCanReceiveEvent(userA, evt) {
		t.Error("initiating user should receive mcp.oauth_complete")
	}
	if clientCanReceiveEvent(userB, evt) {
		t.Error("non-initiating user should NOT receive mcp.oauth_complete")
	}
}

func TestClientCanReceiveEvent_MCPOAuthComplete_NotLeakAcrossTenant(t *testing.T) {
	// Same user ID, different tenants.
	userAMaster := makeClient(permissions.RoleOperator, "user-a", masterTenant)
	userAOther := makeClient(permissions.RoleOperator, "user-a", otherTenant)

	evt := makeEvent(protocol.EventMCPOAuthComplete, masterTenant, map[string]any{
		"serverId":         "server-1",
		"initiatingUserId": "user-a",
		"status":           "success",
	})

	if !clientCanReceiveEvent(userAMaster, evt) {
		t.Error("user-a in masterTenant should receive the event they initiated")
	}
	if clientCanReceiveEvent(userAOther, evt) {
		t.Error("user-a in otherTenant must NOT receive masterTenant event (tenant isolation)")
	}
}

func TestClientCanReceiveEvent_MCPOAuthComplete_AdminReceivesInTenant(t *testing.T) {
	admin := makeClient(permissions.RoleAdmin, "admin", masterTenant)

	evt := makeEvent(protocol.EventMCPOAuthComplete, masterTenant, map[string]any{
		"serverId":         "server-1",
		"initiatingUserId": "other-user",
		"status":           "success",
	})

	if !clientCanReceiveEvent(admin, evt) {
		t.Error("admin should receive mcp.oauth_complete events for any user in their tenant")
	}
}

func TestClientCanReceiveEvent_MCPOAuthComplete_NoInitiatingUserIDBlocked(t *testing.T) {
	c := makeClient(permissions.RoleOperator, "user-x", masterTenant)

	// Event with no initiatingUserId — blocked for non-admin (fail-closed).
	evt := makeEvent(protocol.EventMCPOAuthComplete, masterTenant, map[string]any{
		"serverId": "server-1",
		"status":   "success",
	})

	if clientCanReceiveEvent(c, evt) {
		t.Error("mcp.oauth_complete without initiatingUserId should be blocked for non-admin (fail-closed)")
	}
}

// ---- Cron & heartbeat tenant isolation ----

func TestHeartbeat_IsNoLongerSystemEvent(t *testing.T) {
	if isSystemEvent(protocol.EventHeartbeat) {
		t.Error("EventHeartbeat must NOT be a system event — system events bypass tenant isolation")
	}
}

func TestHeartbeat_TenantIsolationAndRoleGating(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	tenantB := uuid.MustParse("00000000-0000-0000-0000-00000000000b")

	adminA := makeClient(permissions.RoleAdmin, "admin-a", tenantA)
	viewerA := makeClient(permissions.RoleViewer, "viewer-a", tenantA)
	adminB := makeClient(permissions.RoleAdmin, "admin-b", tenantB)

	hbEvt := makeEvent(protocol.EventHeartbeat, tenantA, store.HeartbeatEvent{
		Action:   "running",
		AgentID:  "agent-1",
		TenantID: tenantA,
	})

	if !clientCanReceiveEvent(adminA, hbEvt) {
		t.Error("admin of tenant A should receive the tenant A heartbeat event")
	}
	if clientCanReceiveEvent(adminB, hbEvt) {
		t.Error("admin of tenant B must NOT receive the tenant A heartbeat event (tenant isolation)")
	}
	if clientCanReceiveEvent(viewerA, hbEvt) {
		t.Error("viewer must NOT receive heartbeat events (admin-only policy)")
	}
}

func TestCron_TenantIsolationAndUserScoping(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	tenantB := uuid.MustParse("00000000-0000-0000-0000-00000000000b")

	adminA := makeClient(permissions.RoleAdmin, "admin-a", tenantA)
	ownerA := makeClient(permissions.RoleViewer, "user-1", tenantA)
	otherA := makeClient(permissions.RoleViewer, "user-2", tenantA)
	adminB := makeClient(permissions.RoleAdmin, "admin-b", tenantB)

	cronEvt := makeEvent(protocol.EventCron, tenantA, store.CronEvent{
		Action:   "running",
		JobID:    "job-1",
		UserID:   "user-1",
		TenantID: tenantA,
	})

	if !clientCanReceiveEvent(adminA, cronEvt) {
		t.Error("admin of tenant A should receive the tenant A cron event")
	}
	if !clientCanReceiveEvent(ownerA, cronEvt) {
		t.Error("the job owner should receive their own cron event")
	}
	if clientCanReceiveEvent(otherA, cronEvt) {
		t.Error("a different user of tenant A must NOT receive user-1's cron event")
	}
	if clientCanReceiveEvent(adminB, cronEvt) {
		t.Error("admin of tenant B must NOT receive the tenant A cron event (tenant isolation)")
	}
}

func TestCronAndHeartbeat_NilTenantOnlyOwner(t *testing.T) {
	adminA := makeClient(permissions.RoleAdmin, "admin-a", masterTenant)
	ownerA := makeClient(permissions.RoleOwner, "owner-a", masterTenant)

	unscopedCron := makeEvent(protocol.EventCron, uuid.Nil, store.CronEvent{Action: "running"})
	unscopedHeartbeat := makeEvent(protocol.EventHeartbeat, uuid.Nil, store.HeartbeatEvent{Action: "running"})

	if clientCanReceiveEvent(adminA, unscopedCron) {
		t.Error("non-owner admin must NOT receive an unscoped cron event")
	}
	if !clientCanReceiveEvent(ownerA, unscopedCron) {
		t.Error("owner should receive an unscoped cron event")
	}
	if clientCanReceiveEvent(adminA, unscopedHeartbeat) {
		t.Error("non-owner admin must NOT receive an unscoped heartbeat event")
	}
	if !clientCanReceiveEvent(ownerA, unscopedHeartbeat) {
		t.Error("owner should receive an unscoped heartbeat event")
	}
}

// ---- Team/link event delivery ----

func TestTeamAndLinkEvents_TenantIsolation(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	tenantB := uuid.MustParse("00000000-0000-0000-0000-00000000000b")

	adminA := makeClient(permissions.RoleAdmin, "admin-a", tenantA)
	adminB := makeClient(permissions.RoleAdmin, "admin-b", tenantB)

	names := []string{
		protocol.EventTeamCreated,
		protocol.EventTeamUpdated,
		protocol.EventTeamDeleted,
		protocol.EventTeamMemberAdded,
		protocol.EventTeamMemberRemoved,
		protocol.EventAgentLinkCreated,
		protocol.EventAgentLinkUpdated,
		protocol.EventAgentLinkDeleted,
	}

	for _, name := range names {
		evt := makeEvent(name, tenantA, map[string]any{"team_id": "team-a"})
		if !clientCanReceiveEvent(adminA, evt) {
			t.Errorf("%s: tenant A admin should receive tenant A event", name)
		}
		if clientCanReceiveEvent(adminB, evt) {
			t.Errorf("%s: tenant B admin must NOT receive tenant A event", name)
		}
	}
}

func TestAgentLinkEvents_BlockedForViewer(t *testing.T) {
	tenantA := uuid.MustParse("00000000-0000-0000-0000-00000000000a")
	viewerA := makeClient(permissions.RoleViewer, "viewer-a", tenantA)
	viewerA.SetTeamAccess([]string{"team-a"})

	for _, name := range []string{
		protocol.EventAgentLinkCreated,
		protocol.EventAgentLinkUpdated,
		protocol.EventAgentLinkDeleted,
	} {
		evt := makeEvent(name, tenantA, map[string]any{"team_id": "team-a"})
		if clientCanReceiveEvent(viewerA, evt) {
			t.Errorf("%s: viewer must NOT receive agent-link events (admin-only)", name)
		}
	}
}
