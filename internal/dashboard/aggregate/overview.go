package aggregate

import (
	"context"

	"github.com/nextlevelbuilder/goclaw/internal/dashboard/goclaw"
	"github.com/nextlevelbuilder/goclaw/internal/dashboard/live"
	"golang.org/x/sync/errgroup"
)

// Overview builds the composite dashboard overview KPI by fetching live state,
// task KPIs, and cost summary in parallel via errgroup.
func Overview(ctx context.Context, state *live.State, ws *goclaw.Client, httpClient *goclaw.HTTPClient, teams []TeamRef, resolve TeamResolver) (*OverviewKPI, error) {
	type result struct {
		taskKPI *TaskKPIResult
		cost    *CostSummary
	}

	resCh := make(chan result, 2)

	g := new(errgroup.Group)
	g.Go(func() error {
		tk, err := TaskKPIs(ctx, ws, teams)
		resCh <- result{taskKPI: tk}
		return err
	})
	g.Go(func() error {
		cs, err := Cost(ctx, httpClient, resolve)
		resCh <- result{cost: cs}
		return err
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	close(resCh)

	var taskKPI *TaskKPIResult
	var cost *CostSummary
	for r := range resCh {
		if r.taskKPI != nil {
			taskKPI = r.taskKPI
		}
		if r.cost != nil {
			cost = r.cost
		}
	}

	return &OverviewKPI{
		Live:    state.ComputeKPI(),
		TaskKPI: taskKPI,
		Cost:    cost,
	}, nil
}
