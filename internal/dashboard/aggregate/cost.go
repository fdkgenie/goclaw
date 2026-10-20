package aggregate

import (
	"context"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
)

const (
	costEpsilon = 1e-6
)

// usageSummaryResponse is the /v1/usage/events/summary envelope.
type usageSummaryResponse struct {
	CostUSD float64 `json:"cost_usd"`
}

// usageBreakdownResponse is the /v1/usage/events/breakdown envelope.
type usageBreakdownResponse struct {
	Rows []usageBreakdownRow `json:"rows"`
}

type usageBreakdownRow struct {
	Key     string  `json:"key"`
	CostUSD float64 `json:"cost_usd"`
}

// TeamResolver maps a team UUID to a display name.
type TeamResolver func(teamID string) (string, bool)

// Cost aggregates usage cost: Top-N breakdown plus an "Other" row equal to
// summary minus the sum of rows (when the difference exceeds epsilon).
func Cost(ctx context.Context, client *goclaw.HTTPClient, resolve TeamResolver) (*CostSummary, error) {
	var summary usageSummaryResponse
	if err := client.Get(ctx, "/v1/usage/events/summary", &summary); err != nil {
		return nil, fmt.Errorf("usage summary: %w", err)
	}

	var breakdown usageBreakdownResponse
	if err := client.Get(ctx, "/v1/usage/events/breakdown", &breakdown); err != nil {
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
		rows = append(rows, CostBreakdownRow{Key: r.Key, Label: label, CostUSD: r.CostUSD})
		rowSum += r.CostUSD
	}

	out := &CostSummary{
		SummaryUSD: summary.CostUSD,
		Rows:       rows,
	}
	diff := summary.CostUSD - rowSum
	if diff > costEpsilon {
		out.Other = &CostBreakdownRow{
			Key:     "other",
			Label:   "Other",
			CostUSD: diff,
		}
	}
	return out, nil
}
