package aggregate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

// TeamRef is a lightweight team reference for KPI aggregation.
type TeamRef struct {
	ID   string
	Name string
}

// tasksListResponse is the gateway teams.tasks.list response envelope.
type tasksListResponse struct {
	Tasks   []interface{} `json:"tasks"`
	HasMore bool          `json:"hasMore"`
}

// TaskKPIs queries teams.tasks.list for active and in_review tasks per team
// and returns {value, exact} KPIs. value is the observed count; exact is false
// when hasMore is true (truncated, so value is a lower bound).
func TaskKPIs(ctx context.Context, client *goclaw.Client, teams []TeamRef) (*TaskKPIResult, error) {
	result := &TaskKPIResult{
		Teams:         make([]TeamTaskKPI, 0, len(teams)),
		TotalActive:   TaskKPI{Exact: true},
		TotalInReview: TaskKPI{Exact: true},
	}

	for _, team := range teams {
		ttk := TeamTaskKPI{
			TeamID:   team.ID,
			TeamName: team.Name,
			Active:   TaskKPI{Exact: true},
			InReview: TaskKPI{Exact: true},
		}
		for _, status := range []string{"active", "in_review"} {
			params := map[string]any{"team_id": team.ID, "status": status, "limit": 200}
			resp, err := client.Call(ctx, "teams.tasks.list", params)
			if err != nil {
				return nil, fmt.Errorf("teams.tasks.list team=%s status=%s: %w", team.ID, status, err)
			}
			var parsed tasksListResponse
			if err := unmarshalPayload(resp.Payload, &parsed); err != nil {
				return nil, fmt.Errorf("parse teams.tasks.list team=%s: %w", team.ID, err)
			}
			kpi := TaskKPI{Value: len(parsed.Tasks), Exact: !parsed.HasMore}
			if status == "active" {
				ttk.Active = kpi
				result.TotalActive.Value += kpi.Value
				if !kpi.Exact {
					result.TotalActive.Exact = false
				}
			} else {
				ttk.InReview = kpi
				result.TotalInReview.Value += kpi.Value
				if !kpi.Exact {
					result.TotalInReview.Exact = false
				}
			}
		}
		result.Teams = append(result.Teams, ttk)
	}
	return result, nil
}

func unmarshalPayload(payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
