package protocol

import "testing"

func TestCapEventsCronHeartbeatTenantScoped(t *testing.T) {
	if CapEventsCronHeartbeatTenantScoped != "events.cron_heartbeat_tenant_scoped" {
		t.Errorf("got %q, want %q", CapEventsCronHeartbeatTenantScoped, "events.cron_heartbeat_tenant_scoped")
	}
}
