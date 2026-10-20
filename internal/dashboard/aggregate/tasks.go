package aggregate

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

// TeamRef is a lightweight team reference for KPI aggregation.
type TeamRef struct {
	ID   string
	Name string
}

// tasksListResponse is the gateway teams.tasks.list response envelope.
type tasksListResponse struct {
	Tasks   []taskSummary `json:"tasks"`
	HasMore bool          `json:"hasMore"`
}

// taskSummary carries the task fields the dashboard groups by.
type taskSummary struct {
	Status string `json:"status"`
}

// TaskKPIs queries teams.tasks.list for active and in_review tasks per team
// and returns {value, exact} KPIs. value is the observed count; exact is false
// when hasMore is true (truncated, so value is a lower bound).
//
// The active query is also grouped per status. When the query is truncated
// every group is a lower bound of its own observed count (not of the query
// cap), because the hidden rows can hold any status.
func TaskKPIs(ctx context.Context, client *goclaw.Client, teams []TeamRef) (*TaskKPIResult, error) {
	result := &TaskKPIResult{
		Teams:         make([]TeamTaskKPI, 0, len(teams)),
		TotalActive:   TaskKPI{Exact: true},
		TotalInReview: TaskKPI{Exact: true},
	}
	aggregateCounts := make(map[string]int)

	for _, team := range teams {
		ttk := TeamTaskKPI{
			TeamID:   team.ID,
			TeamName: team.Name,
			Active:   TaskKPI{Exact: true},
			InReview: TaskKPI{Exact: true},
		}
		for _, status := range []string{"active", "in_review"} {
			// The gateway param is camelCase "teamId"; "team_id" is silently
			// ignored and the handler answers "teamId is required".
			params := map[string]any{"teamId": team.ID, "status": status}
			resp, err := client.Call(ctx, "teams.tasks.list", params)
			if err != nil {
				return nil, fmt.Errorf("teams.tasks.list team=%s status=%s: %w", team.ID, status, err)
			}
			kpi, counts, err := taskKPIFromResponse(resp)
			if err != nil {
				return nil, fmt.Errorf("teams.tasks.list team=%s status=%s: %w", team.ID, status, err)
			}
			if status == "active" {
				ttk.Active = kpi
				ttk.ActiveByStatus = statusKPIs(counts, kpi.Exact)
				result.TotalActive.Value += kpi.Value
				if !kpi.Exact {
					result.TotalActive.Exact = false
				}
				for _, g := range ttk.ActiveByStatus {
					aggregateCounts[g.Status] += g.Count.Value
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
	// A truncated team query hides rows of any status, so every aggregate
	// group is a lower bound as soon as one team was truncated.
	result.ActiveByStatus = statusKPIs(aggregateCounts, result.TotalActive.Exact)
	return result, nil
}

func unmarshalPayload(payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// taskKPIFromResponse turns a teams.tasks.list response into a task KPI and
// the observed per-status counts.
//
// A failed gateway response carries no task list, so the count is unknown:
// the KPI is reported as a lower bound (exact=false) instead of a confident
// zero. Decoding an error payload produced a zero-value struct that was then
// reported as exact.
func taskKPIFromResponse(resp *goclaw.ResponseFrame) (TaskKPI, map[string]int, error) {
	if resp == nil {
		return TaskKPI{Exact: false}, nil, fmt.Errorf("empty response")
	}
	if !resp.OK {
		code, message := "", ""
		if resp.Error != nil {
			code, message = resp.Error.Code, resp.Error.Message
		}
		slog.Warn("dashboard teams.tasks.list failed",
			"code", code,
			"message", message,
		)
		return TaskKPI{Exact: false}, nil, nil
	}
	var parsed tasksListResponse
	if err := unmarshalPayload(resp.Payload, &parsed); err != nil {
		return TaskKPI{}, nil, fmt.Errorf("parse payload: %w", err)
	}
	counts := make(map[string]int, len(parsed.Tasks))
	for _, task := range parsed.Tasks {
		if task.Status == "" {
			continue
		}
		counts[task.Status]++
	}
	return TaskKPI{Value: len(parsed.Tasks), Exact: !parsed.HasMore}, counts, nil
}

// statusKPIs converts the observed per-status counts into KPI groups, sorted
// by status name so the output is stable. When the query was truncated every
// group carries the lower-bound flag.
func statusKPIs(counts map[string]int, exact bool) []StatusTaskKPI {
	out := make([]StatusTaskKPI, 0, len(counts))
	for _, status := range sortedKeys(counts) {
		out = append(out, StatusTaskKPI{
			Status: status,
			Count:  TaskKPI{Value: counts[status], Exact: exact},
		})
	}
	return out
}

func sortedKeys(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
