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

  it("renders a feed row with the event type, actor and time", async () => {
    await act(async () => {
      root.render(<ActivityPage />);
    });

    const row = container.querySelector('[data-testid="activity-item"]');
    expect(row).not.toBeNull();
    expect(row?.textContent).toContain("agent_event");
    expect(row?.textContent).toContain("agent-1");
    expect(row?.textContent).toContain("run.started");
  });

  it("renders a header cell for every feed column", async () => {
    await act(async () => {
      root.render(<ActivityPage />);
    });

    const heads = Array.from(container.querySelectorAll("thead th")).map(
      (h) => h.textContent?.trim(),
    );
    expect(heads).toEqual(["Type", "Actor", "Event", "Time"]);
  });

  it("renders the empty state when no events are reported", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      ),
    );
    await act(async () => {
      root.render(<ActivityPage />);
    });

    expect(container.textContent).toContain("No activity yet.");
    expect(container.querySelector('[data-slot="empty-state"]')).not.toBeNull();
  });

  it("offers a limit control bound to the limit query parameter", async () => {
    await act(async () => {
      root.render(<ActivityPage />);
    });

    const input = container.querySelector<HTMLInputElement>("#activity-limit");
    expect(input).not.toBeNull();
    const label = container.querySelector("label[for='activity-limit']");
    expect(label?.textContent).toBe("Limit");
  });
});
