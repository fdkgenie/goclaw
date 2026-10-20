import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import React, { act } from "react";
import ReactDOM from "react-dom/client";
import { OrgChartPage } from "./OrgChartPage";

vi.stubGlobal(
  "fetch",
  async (): Promise<Response> =>
    new Response(JSON.stringify({ nodes: [], edges: [] }), {
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

describe("OrgChartPage", () => {
  it("renders org chart with teams/links counts", async () => {
    await act(async () => {
      root.render(<OrgChartPage />);
    });
    expect(container.textContent).toContain("Teams:");
    expect(container.textContent).toContain("Links:");
  });
});
