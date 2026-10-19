package protocol

// Gateway capability constants advertised in the WebSocket connect response
// under server.capabilities. External clients (including the GoClaw Dashboard)
// probe this list to verify server runtime prerequisites before operating.
const (
	// CapEventsCronHeartbeatTenantScoped indicates cron and heartbeat WS events
	// are scoped to the client's tenant and heartbeat events are admin-only.
	CapEventsCronHeartbeatTenantScoped = "events.cron_heartbeat_tenant_scoped"
)
