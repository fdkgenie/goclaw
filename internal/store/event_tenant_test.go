package store

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// assertNoTenantInJSON fails when a tenant identifier leaks into the wire payload.
func assertNoTenantInJSON(t *testing.T, v any) {
	t.Helper()

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"TenantID", "tenant_id", "tenantId"} {
		if _, ok := m[key]; ok {
			t.Errorf("tenant field %q leaked into JSON: %s", key, string(data))
		}
	}
}

func TestCronEventTenantIDNotSerialized(t *testing.T) {
	assertNoTenantInJSON(t, CronEvent{Action: "running", JobID: "job-1", TenantID: uuid.New()})
}

func TestHeartbeatEventTenantIDNotSerialized(t *testing.T) {
	assertNoTenantInJSON(t, HeartbeatEvent{Action: "running", AgentID: "agent-1", TenantID: uuid.New()})
}
