import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { OverviewPage } from "./OverviewPage";
import type { LiveKPI } from "../api/types";

let container: HTMLDivElement;
let root: ReactDOM.Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

function render(snapshot: LiveKPI | null) {
  act(() => {
    root.render(<OverviewPage snapshot={snapshot} />);
  });
  return container;
}

describe("OverviewPage", () => {
  it("renders traced and untraced runs as separate numbers", () => {
    render({ active_runs_traced: 3, active_runs_untraced: 2, active_runs_total: 5, quality: "exact" });
    expect(container.textContent).toContain("3");
    expect(container.textContent).toContain("2");
    expect(container.textContent).toContain("5");
  });

  it("renders exact quality badge when quality is exact", () => {
    render({ active_runs_traced: 1, active_runs_untraced: 0, active_runs_total: 1, quality: "exact" });
    expect(container.textContent).toContain("Exact");
  });

  it("renders approx quality badge when quality is approx", () => {
    render({ active_runs_traced: 0, active_runs_untraced: 1, active_runs_total: 1, quality: "approx" });
    expect(container.textContent).toContain("Approx");
  });

  it("renders truncated task count with ≥ prefix when not exact", () => {
    render({ active_runs_traced: 0, active_runs_untraced: 0, active_runs_total: 0, quality: "approx" });
    expect(container.textContent).toMatch(/≥0/);
  });

  it("handles null snapshot gracefully", () => {
    render(null);
    expect(container.textContent).toContain("0");
  });
});
