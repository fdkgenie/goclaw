import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { CostPage } from "./CostPage";

let container: HTMLDivElement;
let root: ReactDOM.Root;

function mockCost(body: unknown) {
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

async function render() {
  await act(async () => {
    root.render(<CostPage />);
  });
  return container;
}

describe("CostPage", () => {
  it("renders the fetched summary and breakdown rows", async () => {
    mockCost({
      summary_usd: 130,
      calls: 12,
      rows: [
        { key: "team-a", cost_usd: 70, calls: 7, total_tokens: 700 },
        { key: "team-b", cost_usd: 50, calls: 4, total_tokens: 400 },
      ],
      other: { key: "other", label: "Other", cost_usd: 10, calls: 1, total_tokens: 100 },
    });
    await render();
    expect(container.textContent).toContain("Cost (last 24h)");
    expect(container.textContent).toContain("$130.00");
    expect(container.textContent).toContain("$70.00");
    expect(container.textContent).toContain("Top teams by calls");
  });

  it("wraps table in overflow-x-auto with min width", async () => {
    mockCost({ summary_usd: 0, calls: 0, rows: [] });
    await render();
    const wrapper = container.querySelector(".overflow-x-auto");
    expect(wrapper).not.toBeNull();
    const table = container.querySelector("table");
    expect(table).not.toBeNull();
    expect(table?.className).toContain("min-w-[600px]");
  });

  it("renders the Other row when the API returns a remainder", async () => {
    mockCost({
      summary_usd: 130,
      calls: 12,
      rows: [
        { key: "team-a", cost_usd: 70, calls: 7, total_tokens: 700 },
        { key: "team-b", cost_usd: 50, calls: 4, total_tokens: 400 },
      ],
      other: { key: "other", label: "Other", cost_usd: 10, calls: 1, total_tokens: 100 },
    });
    await render();
    expect(container.querySelector('[data-testid="other-row"]')).not.toBeNull();
    expect(container.textContent).toContain("$10.00");
  });

  it("hides the Other row when there is no remainder", async () => {
    mockCost({
      summary_usd: 50,
      calls: 5,
      rows: [{ key: "team-a", cost_usd: 50, calls: 5, total_tokens: 500 }],
    });
    await render();
    expect(container.querySelector('[data-testid="other-row"]')).toBeNull();
  });

  it("renders the call count of every breakdown row", async () => {
    mockCost({
      summary_usd: 120,
      calls: 11,
      rows: [
        { key: "team-a", label: "Team A", cost_usd: 70, calls: 7, total_tokens: 700 },
        { key: "team-b", label: "Team B", cost_usd: 50, calls: 4, total_tokens: 400 },
      ],
    });
    await render();
    const cells = Array.from(container.querySelectorAll('[data-testid="calls-cell"]')).map(
      (c) => c.textContent,
    );
    expect(cells).toEqual(["7", "4"]);
    expect(container.textContent).toContain("Calls");
  });

  it("prefers the resolved team label over the raw team id", async () => {
    mockCost({
      summary_usd: 70,
      calls: 7,
      rows: [
        { key: "6ba7b810-9dad-11d1-80b4-00c04fd430c8", label: "Platform", cost_usd: 70, calls: 7, total_tokens: 700 },
      ],
    });
    await render();
    expect(container.textContent).toContain("Platform");
    expect(container.textContent).not.toContain("6ba7b810-9dad-11d1-80b4-00c04fd430c8");
  });

  it("renders a header cell for every cost column", async () => {
    mockCost({
      summary_usd: 70,
      calls: 7,
      rows: [{ key: "team-a", label: "Team A", cost_usd: 70, calls: 7, total_tokens: 700 }],
    });
    await render();

    const heads = Array.from(container.querySelectorAll("thead th")).map(
      (h) => h.textContent?.trim(),
    );
    expect(heads).toEqual(["Group", "Calls", "Tokens", "Cost (USD)"]);
  });

  it("renders one breakdown row per team", async () => {
    mockCost({
      summary_usd: 120,
      calls: 11,
      rows: [
        { key: "team-a", label: "Team A", cost_usd: 70, calls: 7, total_tokens: 700 },
        { key: "team-b", label: "Team B", cost_usd: 50, calls: 4, total_tokens: 400 },
      ],
    });
    await render();

    const bodyRows = container.querySelectorAll("tbody tr");
    expect(bodyRows).toHaveLength(2);
    expect(bodyRows[0].textContent).toContain("Team A");
    expect(bodyRows[1].textContent).toContain("Team B");
  });

  it("renders the summary strip with total cost, calls and tokens", async () => {
    mockCost({
      summary_usd: 120,
      calls: 11,
      rows: [
        { key: "team-a", label: "Team A", cost_usd: 70, calls: 7, total_tokens: 700 },
        { key: "team-b", label: "Team B", cost_usd: 50, calls: 4, total_tokens: 400 },
      ],
    });
    await render();

    const tiles = container.querySelectorAll('[data-slot="stat-tile"]');
    expect(tiles).toHaveLength(3);
    const text = container.textContent ?? "";
    expect(text).toContain("Total cost");
    expect(text).toContain("Tokens");
  });

  it("offers a team filter control bound to the team_id query parameter", async () => {
    mockCost({ summary_usd: 0, calls: 0, rows: [] });
    await render();

    const input = container.querySelector<HTMLInputElement>("#cost-team-filter");
    expect(input).not.toBeNull();
    const label = container.querySelector("label[for='cost-team-filter']");
    expect(label?.textContent).toBe("Team id");
  });
});
