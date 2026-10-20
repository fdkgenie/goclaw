import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { ActivityPage } from "./ActivityPage";

let container: HTMLDivElement;
let root: ReactDOM.Root;

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(
        JSON.stringify({
          items: [
            {
              id: "a1",
              type: "agent_event",
              timestamp: "2026-10-20T03:00:00Z",
              agent_id: "agent-1",
              team_id: "team-1",
              summary: "run.started",
              details: { runId: "r1" },
            },
            {
              id: "gap:1",
              type: "gap",
              timestamp: "2026-10-20T02:59:00Z",
              summary: "Reconnection gap",
            },
          ],
        }),
        { status: 200, headers: { "content-type": "application/json" } },
      ),
    ),
  );
  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe("ActivityPage", () => {
  it("renders fetched activity items with agent and action", async () => {
    await act(async () => {
      root.render(<ActivityPage />);
    });

    const items = container.querySelectorAll('[data-testid="activity-item"]');
    expect(items).toHaveLength(1);
    expect(container.textContent).toContain("agent-1");
    expect(container.textContent).toContain("run.started");
    expect(container.textContent).toContain("Details");
  });

  it("renders a distinct gap marker", async () => {
    await act(async () => {
      root.render(<ActivityPage />);
    });

    expect(container.querySelector('[data-testid="activity-gap"]')).not.toBeNull();
    expect(container.textContent).toContain("Reconnection gap");
  });
});
