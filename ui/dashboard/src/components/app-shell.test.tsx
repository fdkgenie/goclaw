import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { AppShell } from "./app-shell";
import { ThemeProvider } from "../theme/theme-context";

class FakeEventSource {
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {}
  close() {}
}

let container: HTMLDivElement;
let root: ReactDOM.Root;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

beforeEach(() => {
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/overview")) {
        return json({
          live: {
            active_runs_traced: 0,
            active_runs_untraced: 0,
            active_runs_total: 0,
            quality: "approx",
            traced_quality: "approx",
          },
          task_kpi: {
            teams: [],
            total_active: { value: 0, exact: true },
            total_in_review: { value: 0, exact: true },
            active_by_status: [],
          },
          cost: { summary_usd: 0, calls: 0, rows: [] },
        });
      }
      if (url.startsWith("/api/cost")) {
        return json({ summary_usd: 0, calls: 0, rows: [] });
      }
      if (url.startsWith("/api/orgchart")) {
        return json({ nodes: [], edges: [] });
      }
      if (url.startsWith("/api/activity")) {
        return json({ items: [] });
      }
      return json({}, 404);
    }),
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

async function render() {
  await act(async () => {
    root.render(
      <ThemeProvider>
        <AppShell onLogout={() => {}} />
      </ThemeProvider>,
    );
  });
  return container;
}

function navButtons(): HTMLButtonElement[] {
  const nav = container.querySelector("nav");
  return Array.from(nav?.querySelectorAll("button") ?? []);
}

describe("AppShell", () => {
  it("renders the four dashboard sections in the nav", async () => {
    await render();

    const labels = navButtons().map((b) => b.textContent?.trim());
    expect(labels).toEqual(["Overview", "Org Chart", "Cost", "Activity"]);
  });

  it("marks the active section for the current page", async () => {
    await render();

    const active = container.querySelectorAll('button[aria-current="page"]');
    expect(active).toHaveLength(1);
    expect(active[0].textContent?.trim()).toBe("Overview");
  });

  it("switches the page and the active nav item", async () => {
    await render();

    const cost = navButtons().find(
      (b) => b.textContent?.trim() === "Cost",
    );
    if (!cost) throw new Error("cost nav item not rendered");
    await act(async () => {
      cost.click();
    });

    const active = container.querySelector('button[aria-current="page"]');
    expect(active?.textContent?.trim()).toBe("Cost");
    expect(container.textContent).toContain("Cost (last 24h)");
  });

  it("keeps the theme toggle and the logout control in the shell", async () => {
    await render();

    expect(
      container.querySelector('button[aria-label="Toggle theme"]'),
    ).not.toBeNull();
    expect(container.textContent).toContain("Logout");
  });

  it("shows the stale banner when the stream reports a degraded connection", async () => {
    class DegradedEventSource extends FakeEventSource {
      constructor(url: string) {
        super(url);
        setTimeout(() => {
          this.onerror?.();
        }, 0);
      }
    }
    vi.stubGlobal("EventSource", DegradedEventSource);
    await render();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    expect(container.textContent).toContain("Connection stale");
  });
});
