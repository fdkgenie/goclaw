package protocol

import "testing"

func TestCapEventsCronHeartbeatTenantScoped(t *testing.T) {
	if CapEventsCronHeartbeatTenantScoped != "events.cron_heartbeat_tenant_scoped" {
		t.Errorf("got %q, want %q", CapEventsCronHeartbeatTenantScoped, "events.cron_heartbeat_tenant_scoped")
	}
}

func TestB2CapabilityValues(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"cron_heartbeat_tenant_scoped", CapEventsCronHeartbeatTenantScoped, "events.cron_heartbeat_tenant_scoped"},
		{"team_link_tenant_tagged", CapEventsTeamLinkTenantTagged, "events.team_link_tenant_tagged"},
		{"usage_events_team", CapUsageEventsTeam, "usage.events.team"},
		{"teams_tasks_list_has_more", CapTeamsTasksListHasMore, "teams.tasks.list.has_more"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}
