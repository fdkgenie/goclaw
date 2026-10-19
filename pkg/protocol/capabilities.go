package protocol

// Gateway capability constants advertised in the WebSocket connect response
// under server.capabilities. External clients (including the GoClaw Dashboard)
// probe this list to verify server runtime prerequisites before operating.
const (
	// CapEventsCronHeartbeatTenantScoped indicates cron and heartbeat WS events
	// are scoped to the client's tenant and heartbeat events are admin-only.
	CapEventsCronHeartbeatTenantScoped = "events.cron_heartbeat_tenant_scoped"

	// CapEventsTeamLinkTenantTagged indicates team, team member, and agent link
	// events carry the context tenant ID for tenant-scoped delivery.
	CapEventsTeamLinkTenantTagged = "events.team_link_tenant_tagged"

	// CapUsageEventsTeam indicates usage event analytics support the team
	// dimension for filtering and grouping.
	CapUsageEventsTeam = "usage.events.team"

	// CapTeamsTasksListHasMore indicates teams.tasks.list reports whether the
	// result set was truncated by the dashboard task limit.
	CapTeamsTasksListHasMore = "teams.tasks.list.has_more"
)
