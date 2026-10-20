import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import React, { act } from "react";
import ReactDOM from "react-dom/client";
import { CostPage } from "./CostPage";

vi.stubGlobal(
  "fetch",
  async (): Promise<Response> =>
    new Response(JSON.stringify({
      summary_usd: 130,
      rows: [{ key: "team-a", cost_usd: 70 }, { key: "team-b", cost_usd: 50 }],
      other: { key: "other", label: "Other", cost_usd: 10 },
    }), {
      status: 200,
      headers: { "content-type": "application/json" },
    }),
);

let container: HTMLDivElement;
let root: ReactDOM.Root;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  root.unmount();
  container.remove();
});

function render() {
  act(() => {
    root.render(<CostPage />);
  });
  return container;
}

describe("CostPage", () => {
  it("renders cost page without crashing", async () => {
    await act(async () => {
      root.render(<CostPage />);
    });
    expect(container.textContent).toContain("Burn Rate");
  });

  it("wraps table in overflow-x-auto with min width", async () => {
    await act(async () => {
      root.render(<CostPage />);
    });
    const wrapper = container.querySelector(".overflow-x-auto");
    expect(wrapper).not.toBeNull();
    const table = container.querySelector("table");
    expect(table).not.toBeNull();
    expect(table?.className).toContain("min-w-[600px]");
  });

  it("renders breakdown rows and Other row", async () => {
    await act(async () => {
      root.render(<CostPage />);
    });
    expect(container.textContent).toContain("Other");
    expect(container.textContent).toContain("$10.00");
  });
});
