import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { useSSE } from "./sse";
import type { LiveKPI } from "./types";

class FakeEventSource {
  static last: FakeEventSource | null = null;

  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;

  constructor(public url: string) {
    FakeEventSource.last = this;
  }

  close() {
    this.closed = true;
  }

  emit(payload: unknown) {
    this.onmessage?.(
      new MessageEvent("message", { data: JSON.stringify(payload) }),
    );
  }
}

let container: HTMLDivElement;
let root: ReactDOM.Root;
let latest: ReturnType<typeof useSSE<LiveKPI>> | null = null;

function Probe() {
  latest = useSSE<LiveKPI>();
  return <div data-testid="version">{String(latest.version)}</div>;
}

beforeEach(() => {
  FakeEventSource.last = null;
  latest = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
});

describe("useSSE", () => {
  it("subscribes to the real dashboard stream endpoint", async () => {
    await act(async () => {
      root.render(<Probe />);
    });
    expect(FakeEventSource.last).not.toBeNull();
    expect(FakeEventSource.last?.url).toBe("/api/stream");
  });

  it("applies the snapshot kpi and version", async () => {
    await act(async () => {
      root.render(<Probe />);
    });
    const es = FakeEventSource.last!;

    await act(async () => {
      es.emit({
        event: "snapshot",
        data: { version: 7, kpi: { active_runs_untraced: 2 } },
      });
    });

    expect(latest?.version).toBe(7);
    expect(latest?.snapshot?.active_runs_untraced).toBe(2);
    expect(latest?.stale).toBe(false);
  });

  it("ignores out-of-order deltas", async () => {
    await act(async () => {
      root.render(<Probe />);
    });
    const es = FakeEventSource.last!;

    await act(async () => {
      es.emit({
        event: "snapshot",
        data: { version: 7, kpi: { active_runs_untraced: 2 } },
      });
    });
    await act(async () => {
      es.emit({
        event: "delta",
        data: { version: 5, kpi: { active_runs_untraced: 99 } },
      });
    });

    expect(latest?.version).toBe(7);
    expect(latest?.snapshot?.active_runs_untraced).toBe(2);
  });

  it("marks the stream stale when the gateway reports a down delta", async () => {
    await act(async () => {
      root.render(<Probe />);
    });
    const es = FakeEventSource.last!;

    await act(async () => {
      es.emit({
        event: "snapshot",
        data: { version: 1, kpi: { active_runs_untraced: 0 } },
      });
    });
    expect(latest?.stale).toBe(false);

    await act(async () => {
      es.emit({
        event: "delta",
        data: { version: 2, kind: "down" },
      });
    });

    expect(latest?.stale).toBe(true);
    expect(latest?.version).toBe(2);
  });

  it("closes the event source on unmount", async () => {
    await act(async () => {
      root.render(<Probe />);
    });
    const es = FakeEventSource.last!;
    act(() => root.unmount());
    expect(es.closed).toBe(true);
    // Re-create so afterEach unmount is a no-op on an already unmounted root.
    container = document.createElement("div");
    document.body.appendChild(container);
    root = ReactDOM.createRoot(container);
  });
});
