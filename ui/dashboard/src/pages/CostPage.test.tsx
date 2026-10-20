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
      rows: [{ key: "team-a", cost_usd: 70 }, { key: "team-b", cost_usd: 50 }],
      other: { key: "other", label: "Other", cost_usd: 10 },
    });
    await render();
    expect(container.textContent).toContain("Burn Rate");
    expect(container.textContent).toContain("$130.00");
    expect(container.textContent).toContain("$70.00");
    expect(container.textContent).toContain("Top N by calls");
  });

  it("wraps table in overflow-x-auto with min width", async () => {
    mockCost({ summary_usd: 0, rows: [] });
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
      rows: [{ key: "team-a", cost_usd: 70 }, { key: "team-b", cost_usd: 50 }],
      other: { key: "other", label: "Other", cost_usd: 10 },
    });
    await render();
    expect(container.querySelector('[data-testid="other-row"]')).not.toBeNull();
    expect(container.textContent).toContain("$10.00");
  });

  it("hides the Other row when there is no remainder", async () => {
    mockCost({ summary_usd: 50, rows: [{ key: "team-a", cost_usd: 50 }] });
    await render();
    expect(container.querySelector('[data-testid="other-row"]')).toBeNull();
  });
});
