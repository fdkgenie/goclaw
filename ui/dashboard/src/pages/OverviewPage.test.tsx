import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { OverviewPage } from "./OverviewPage";
import type { LiveKPI, OverviewKPI } from "../api/types";

let container: HTMLDivElement;
let root: ReactDOM.Root;

function mockOverview(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    ),
  );
}

function emptyOverview(): OverviewKPI {
  return {
    live: { active_runs_traced: 0, active_runs_untraced: 0, active_runs_total: 0, quality: "approx", traced_quality: "approx" },
    task_kpi: {
      teams: [],
      total_active: { value: 0, exact: true },
      total_in_review: { value: 0, exact: true },
      active_by_status: [],
    },
    cost: { summary_usd: 0, calls: 0, rows: [] },
  };
}

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

async function render(snapshot: LiveKPI | null) {
  await act(async () => {
    root.render(<OverviewPage snapshot={snapshot} />);
  });
  return container;
}

describe("OverviewPage", () => {
  it("renders traced and untraced runs as separate numbers", async () => {
    mockOverview(emptyOverview());
    await render({ active_runs_traced: 3, active_runs_untraced: 2, active_runs_total: 5, quality: "exact", traced_quality: "exact" });
    expect(container.textContent).toContain("3");
    expect(container.textContent).toContain("2");
    expect(container.textContent).toContain("5");
  });

  it("renders exact quality badge when quality is exact", async () => {
    mockOverview(emptyOverview());
    await render({ active_runs_traced: 1, active_runs_untraced: 0, active_runs_total: 1, quality: "exact", traced_quality: "exact" });
    expect(container.textContent).toContain("Exact");
  });

  it("renders approx quality badge when quality is approx", async () => {
    mockOverview(emptyOverview());
    await render({ active_runs_traced: 0, active_runs_untraced: 1, active_runs_total: 1, quality: "approx", traced_quality: "approx" });
    expect(container.textContent).toContain("Approx");
  });

  // The traced count and the merged total carry their own quality: an
  // event-only run makes the total approximate while the traced count stays
  // exact.
  it("renders the traced quality apart from the total quality", async () => {
    mockOverview(emptyOverview());
    await render({ active_runs_traced: 3, active_runs_untraced: 1, active_runs_total: 4, quality: "approx", traced_quality: "exact" });
    expect(container.querySelector('[data-testid="traced-quality"]')?.textContent).toBe("Exact");
    expect(container.querySelector('[data-testid="total-quality"]')?.textContent).toBe("Approx");
  });

  // The task tile used to show the live run count; it must show the task KPI
  // of the composite /api/overview endpoint instead.
  it("renders the task and cost tiles from the overview endpoint", async () => {
    const overview = emptyOverview();
    overview.task_kpi.total_active = { value: 7, exact: true };
    overview.task_kpi.total_in_review = { value: 2, exact: true };
    overview.cost = { summary_usd: 42.5, calls: 4, rows: [] };
    mockOverview(overview);

    await render({ active_runs_traced: 3, active_runs_untraced: 2, active_runs_total: 5, quality: "exact", traced_quality: "exact" });

    expect(container.querySelector('[data-testid="tasks-active"]')?.textContent).toBe("7");
    expect(container.querySelector('[data-testid="tasks-in-review"]')?.textContent).toContain("2");
    expect(container.querySelector('[data-testid="cost-total"]')?.textContent).toBe("$42.50");
  });

  it("renders the per-status task groups with the lower bound marker", async () => {
    const overview = emptyOverview();
    overview.task_kpi.total_active = { value: 200, exact: false };
    overview.task_kpi.active_by_status = [
      { status: "in_progress", count: { value: 120, exact: false } },
      { status: "pending", count: { value: 50, exact: false } },
      { status: "blocked", count: { value: 30, exact: false } },
    ];
    mockOverview(overview);

    await render(null);

    const groups = container.querySelector('[data-testid="tasks-by-status"]')?.textContent ?? "";
    expect(groups).toContain("in_progress: ≥120");
    expect(groups).toContain("pending: ≥50");
    expect(groups).toContain("blocked: ≥30");
  });

  it("requests the overview endpoint", async () => {
    mockOverview(emptyOverview());
    await render(null);
    const fetchMock = globalThis.fetch as unknown as ReturnType<typeof vi.fn>;
    expect(fetchMock).toHaveBeenCalled();
    const called = fetchMock.mock.calls.map((c) => String(c[0]));
    expect(called.some((u) => u.includes("/api/overview"))).toBe(true);
  });

  it("renders truncated task count with ≥ prefix when the task KPI is not exact", async () => {
    const overview = emptyOverview();
    overview.task_kpi.total_active = { value: 200, exact: false };
    mockOverview(overview);

    await render({ active_runs_traced: 0, active_runs_untraced: 0, active_runs_total: 0, quality: "approx", traced_quality: "approx" });
    expect(container.querySelector('[data-testid="tasks-active"]')?.textContent).toBe("≥200");
  });

  it("keeps the live tiles when the overview endpoint fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response("boom", { status: 500, statusText: "Internal Server Error" })),
    );
    await render({ active_runs_traced: 1, active_runs_untraced: 0, active_runs_total: 1, quality: "exact", traced_quality: "exact" });
    expect(container.textContent).toContain("1");
    expect(container.querySelector('[data-testid="tasks-active"]')?.textContent).toBe("—");
  });

  it("handles null snapshot gracefully", async () => {
    mockOverview(emptyOverview());
    await render(null);
    expect(container.textContent).toContain("0");
  });

  it("renders a stat tile per overview metric with its label and value", async () => {
    const overview = emptyOverview();
    overview.task_kpi.total_active = { value: 4, exact: true };
    overview.cost = { summary_usd: 12.5, calls: 3, rows: [] };
    mockOverview(overview);

    await render({
      active_runs_traced: 3,
      active_runs_untraced: 2,
      active_runs_total: 5,
      quality: "exact",
      traced_quality: "exact",
    });

    const tiles = container.querySelectorAll('[data-slot="stat-tile"]');
    expect(tiles).toHaveLength(4);

    const text = container.textContent ?? "";
    expect(text).toContain("Live Runs");
    expect(text).toContain("Active Runs");
    expect(text).toContain("Active Tasks");
    expect(text).toContain("Cost (last 24h)");
    expect(container.querySelector('[data-testid="tasks-active"]')?.textContent).toBe("4");
    expect(container.querySelector('[data-testid="cost-total"]')?.textContent).toBe("$12.50");
  });

  it("renders a per-team task breakdown row for every team", async () => {
    const overview = emptyOverview();
    overview.task_kpi.teams = [
      {
        team_id: "t1",
        team_name: "Alpha",
        active: { value: 3, exact: true },
        in_review: { value: 1, exact: true },
        active_by_status: [{ status: "in_progress", count: { value: 3, exact: true } }],
      },
      {
        team_id: "t2",
        team_name: "Beta",
        active: { value: 2, exact: true },
        in_review: { value: 0, exact: true },
        active_by_status: [],
      },
    ];
    mockOverview(overview);

    await render(null);

    expect(container.textContent).toContain("Tasks by team");
    const rows = container.querySelectorAll("[data-team-id]");
    expect(rows).toHaveLength(2);
    expect(container.textContent).toContain("Alpha");
    expect(container.textContent).toContain("Beta");
  });

  it("shows the team breakdown empty state when no team reports tasks", async () => {
    mockOverview(emptyOverview());
    await render(null);

    expect(container.textContent).toContain("No team task data yet");
  });
});
