package aggregate

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	costEpsilon = 1e-6

	// DefaultCostWindow is the usage window the dashboard reports when the
	// caller does not ask for a specific one. It matches the gateway's
	// default summary period (24h).
	DefaultCostWindow = 24 * time.Hour

	// costBreakdownLimit is the row limit sent to the gateway breakdown
	// query. The gateway caps a breakdown query at 100 rows and defaults to
	// 25; the dashboard asks for the default-sized page.
	costBreakdownLimit = 25
)

// CostQuery narrows the usage query behind the cost page.
type CostQuery struct {
	// From/To bound the usage window. The gateway rejects a usage-events
	// query without both bounds, so a zero value is replaced by the default
	// window ending now.
	From time.Time
	To   time.Time
	// TeamID optionally restricts the query to a single team (UUID). Empty
	// means every team of the tenant.
	TeamID string
}

// window returns the effective usage window.
func (q CostQuery) window() (from, to time.Time) {
	to = q.To
	if to.IsZero() {
		to = time.Now().UTC()
	}
	from = q.From
	if from.IsZero() {
		from = to.Add(-DefaultCostWindow)
	}
	return from, to
}

// usageSummaryResponse is the /v1/usage/events/summary envelope.
type usageSummaryResponse struct {
	Summary usageEventSummary `json:"summary"`
}

// usageEventSummary mirrors the gateway UsageEventSummary payload.
type usageEventSummary struct {
	Calls         int     `json:"calls"`
	Errors        int     `json:"errors"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	TotalTokens   int64   `json:"total_tokens"`
	CostUSD       float64 `json:"cost_usd"`
	AvgDurationMS int     `json:"avg_duration_ms"`
}

// usageBreakdownResponse is the /v1/usage/events/breakdown envelope.
type usageBreakdownResponse struct {
	Rows []usageBreakdownRow `json:"rows"`
}

// usageBreakdownRow mirrors the gateway UsageEventBreakdown payload. The
// key is the group value, i.e. the team id when the query groups by team.
type usageBreakdownRow struct {
	Key          string  `json:"key"`
	Calls        int     `json:"calls"`
	Errors       int     `json:"errors"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// TeamResolver maps a team UUID to a display name.
type TeamResolver func(teamID string) (string, bool)

// Cost aggregates usage cost from the gateway usage-event endpoints.
//
// The gateway requires an explicit time window on both usage-events
// endpoints (it answers 400 without from/to) and the breakdown is grouped by
// team (group_by=team) so every row key is a team id; an empty key is usage
// that is not attributed to a team.
func Cost(ctx context.Context, client *goclaw.HTTPClient, resolve TeamResolver, q CostQuery) (*CostSummary, error) {
	from, to := q.window()

	var summary usageSummaryResponse
	if err := client.Get(ctx, usageEventsPath("/v1/usage/events/summary", from, to, q.TeamID, ""), &summary); err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}

	var breakdown usageBreakdownResponse
	if err := client.Get(ctx, usageEventsPath("/v1/usage/events/breakdown", from, to, q.TeamID, "team"), &breakdown); err != nil {
		return nil, fmt.Errorf("usage breakdown: %w", err)
	}

	rows := make([]CostBreakdownRow, 0, len(breakdown.Rows))
	var rowSum float64
	for _, r := range breakdown.Rows {
		label := r.Key
		var resolved string
		var ok bool
		if resolve != nil {
			resolved, ok = resolve(r.Key)
		}
		if ok {
			label = resolved
		} else if r.Key == "" {
			label = "Personal / Direct"
		} else {
			label = "Deleted Team (" + r.Key + ")"
		}
		rows = append(rows, CostBreakdownRow{
			Key:         r.Key,
			Label:       label,
			CostUSD:     r.CostUSD,
			Calls:       r.Calls,
			TotalTokens: r.TotalTokens,
		})
		rowSum += r.CostUSD
	}

	out := &CostSummary{
		SummaryUSD: summary.Summary.CostUSD,
		Calls:      summary.Summary.Calls,
		Rows:       rows,
	}
	diff := summary.Summary.CostUSD - rowSum
	if diff > costEpsilon {
		out.Other = &CostBreakdownRow{
			Key:     "other",
			Label:   "Other",
			CostUSD: diff,
		}
	}
	return out, nil
}

// usageEventsPath builds a usage-events request path with the filters the
// gateway understands: from/to (both required), team_id (optional) and
// group_by (optional).
func usageEventsPath(path string, from, to time.Time, teamID, groupBy string) string {
	values := url.Values{}
	values.Set("from", from.UTC().Format(time.RFC3339))
	values.Set("to", to.UTC().Format(time.RFC3339))
	if teamID != "" {
		values.Set("team_id", teamID)
	}
	if groupBy != "" {
		values.Set("group_by", groupBy)
	}
	return path + "?" + values.Encode()
}
