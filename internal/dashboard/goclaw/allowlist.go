// Package goclaw allowlist defines the read-only RPC methods and HTTP paths the
// dashboard is permitted to invoke. Everything else is rejected (fail-closed)
// and logged as a security event. The dashboard is intentionally read-only: no
// mutation RPCs (chat.send, config.apply, agents.delete, etc.) are allowed.
package goclaw

import (
	"log/slog"
	"strings"
)

var (
	// allowedRPCMethods are the RPC methods the dashboard may call via WS.
	allowedRPCMethods = map[string]struct{}{
		"teams.list":        {},
		"teams.get":         {},
		"teams.tasks.list":  {},
		"teams.events.list": {},
		"agents.links.list": {},
	}

	// allowedHTTPPaths are "method path" pairs the dashboard may call via HTTP.
	allowedHTTPPaths = map[string]struct{}{
		"GET /v1/agents":                  {},
		"GET /v1/traces":                  {},
		"GET /v1/usage/events/summary":    {},
		"GET /v1/usage/events/breakdown":  {},
		"GET /v1/usage/events/timeseries": {},
		"GET /v1/activity":                {},
	}
)

// IsAllowedRPCMethod reports whether method is on the allowlist.
func IsAllowedRPCMethod(method string) bool {
	_, ok := allowedRPCMethods[method]
	return ok
}

// IsAllowedHTTPPath reports whether method+path is on the allowlist.
func IsAllowedHTTPPath(method, path string) bool {
	key := strings.TrimSpace(method) + " " + strings.TrimSpace(path)
	_, ok := allowedHTTPPaths[key]
	return ok
}

// LogBlocked logs a security event when a method or path is rejected.
func LogBlocked(kind, method, path string) {
	slog.Warn("security.dashboard.method_blocked",
		"kind", kind,
		"method", method,
		"path", path,
	)
}
