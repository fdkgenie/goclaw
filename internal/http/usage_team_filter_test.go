package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestParseUsageEventFilters_TeamID(t *testing.T) {
	teamID := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/v1/usage/events/summary?team_id="+teamID.String(), nil)

	q, err := parseUsageEventFilters(req)
	if err != nil {
		t.Fatalf("parseUsageEventFilters failed: %v", err)
	}
	if q.TeamID == nil || *q.TeamID != teamID {
		t.Fatalf("q.TeamID = %v, want %v", q.TeamID, teamID)
	}

	badReq := httptest.NewRequest(http.MethodGet, "/v1/usage/events/summary?team_id=not-a-uuid", nil)
	_, err = parseUsageEventFilters(badReq)
	if err == nil {
		t.Fatal("expected error for invalid team_id UUID, got nil")
	}
}
