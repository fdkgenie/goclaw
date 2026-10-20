package goclaw

import (
	"testing"
)

func TestAllowlistPermittedRPC(t *testing.T) {
	for _, m := range []string{
		"teams.list", "teams.get", "teams.tasks.list", "teams.events.list", "agents.links.list",
	} {
		if !IsAllowedRPCMethod(m) {
			t.Errorf("RPC method %q should be allowed", m)
		}
	}
}

func TestAllowlistBlockedRPC(t *testing.T) {
	for _, m := range []string{
		"chat.send", "chat.stream", "config.apply", "agents.delete", "agents.update",
		"cron.create", "skill.publish", "teams.create",
	} {
		if IsAllowedRPCMethod(m) {
			t.Errorf("RPC method %q should be blocked", m)
		}
	}
}

func TestAllowlistPermittedHTTP(t *testing.T) {
	for _, p := range []string{
		"GET /v1/agents",
		"GET /v1/traces",
		"GET /v1/usage/events/summary",
		"GET /v1/usage/events/breakdown",
		"GET /v1/usage/events/timeseries",
		"GET /v1/activity",
	} {
		method, path := splitHTTP(p)
		if !IsAllowedHTTPPath(method, path) {
			t.Errorf("HTTP path %q should be allowed", p)
		}
	}
}

func TestAllowlistBlockedHTTP(t *testing.T) {
	for _, p := range []string{
		"POST /v1/chat/completions",
		"DELETE /v1/agents/a1",
		"PUT /v1/config",
		"GET /v1/admin/tenants",
		"POST /v1/usage/events",
	} {
		method, path := splitHTTP(p)
		if IsAllowedHTTPPath(method, path) {
			t.Errorf("HTTP path %q should be blocked", p)
		}
	}
}

func splitHTTP(p string) (string, string) {
	for i := range p {
		if p[i] == ' ' {
			return p[:i], p[i+1:]
		}
	}
	return "GET", p
}
