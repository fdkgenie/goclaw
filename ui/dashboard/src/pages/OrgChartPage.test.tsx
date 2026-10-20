import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { OrgChartPage } from "./OrgChartPage";

const dag = {
  nodes: [
    { id: "team:t1", type: "team", label: "Alpha Team", team_id: "t1" },
    { id: "agent:lead", type: "lead", label: "Lead Bot", team_id: "t1" },
    { id: "agent:member", type: "member", label: "Member Bot", team_id: "t1" },
    { id: "agent:helper", type: "agent", label: "Helper Bot" },
  ],
  edges: [
    { from: "team:t1", to: "agent:lead", kind: "hierarchy" },
    { from: "agent:lead", to: "agent:member", kind: "hierarchy" },
    { from: "agent:lead", to: "agent:helper", kind: "delegation" },
  ],
};

let container: HTMLDivElement;
let root: ReactDOM.Root;

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(dag), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
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

describe("OrgChartPage", () => {
  it("renders the fetched DAG nodes and directed edges", async () => {
    await act(async () => {
      root.render(<OrgChartPage />);
    });

    expect(container.textContent).toContain("Teams:");
    expect(container.textContent).toContain("Alpha Team");
    expect(container.textContent).toContain("Lead Bot");
    expect(container.textContent).toContain("Member Bot");

    const teamNode = container.querySelector('[data-node-id="team:t1"]');
    expect(teamNode?.getAttribute("data-node-type")).toBe("team");

    const edges = container.querySelectorAll("line[data-edge-from]");
    expect(edges).toHaveLength(3);
    expect(edges[0].getAttribute("data-edge-from")).toBe("team:t1");
    expect(edges[0].getAttribute("data-edge-to")).toBe("agent:lead");
  });

  it("separates hierarchy edges from delegation links", async () => {
    await act(async () => {
      root.render(<OrgChartPage />);
    });

    expect(container.textContent).toContain("Hierarchy links:");
    expect(container.textContent).toContain("Delegation links:");

    const delegation = container.querySelector('line[data-edge-kind="delegation"]');
    expect(delegation).not.toBeNull();
    expect(delegation?.getAttribute("data-edge-from")).toBe("agent:lead");
    expect(delegation?.getAttribute("data-edge-to")).toBe("agent:helper");

    const hierarchy = container.querySelectorAll('line[data-edge-kind="hierarchy"]');
    expect(hierarchy).toHaveLength(2);
  });

  it("renders a delegation partner that is not a team member", async () => {
    await act(async () => {
      root.render(<OrgChartPage />);
    });
    const helper = container.querySelector('[data-node-id="agent:helper"]');
    expect(helper?.getAttribute("data-node-type")).toBe("agent");
    expect(container.textContent).toContain("Helper Bot");
  });
});
