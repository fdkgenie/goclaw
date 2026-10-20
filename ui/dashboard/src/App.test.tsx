import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import App from "./App";

class FakeEventSource {
  onmessage: ((event: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {}
  close() {}
}

let container: HTMLDivElement;
let root: ReactDOM.Root;
let authenticated = false;
let loginCalls = 0;

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}

function setInputValue(input: HTMLInputElement, value: string) {
  const setter = Object.getOwnPropertyDescriptor(
    window.HTMLInputElement.prototype,
    "value",
  )?.set;
  setter?.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
}

beforeEach(() => {
  authenticated = false;
  loginCalls = 0;
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/session") {
        return authenticated
          ? json({ authenticated: true })
          : json({ authenticated: false }, 401);
      }
      if (url === "/api/login") {
        authenticated = true;
        loginCalls++;
        return json({ authenticated: true });
      }
      if (url === "/api/logout") {
        authenticated = false;
        return json({ authenticated: false });
      }
      if (
        url.startsWith("/api/overview") ||
        url.startsWith("/api/stream") ||
        url.startsWith("/api/activity")
      ) {
        return json({ version: 1, entries: [], items: [] });
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
  document.cookie = "goclaw_dashboard_session=; path=/; expires=Thu, 01 Jan 1970 00:00:00 GMT";
  vi.unstubAllGlobals();
});

describe("App auth flow", () => {
  it("logs in without reading or storing the HttpOnly token in JS", async () => {
    await act(async () => {
      root.render(<App />);
    });
    expect(container.textContent).toContain("Sign in");

    const input = container.querySelector<HTMLInputElement>("#dashboard-password");
    if (!input) throw new Error("password input not rendered");
    await act(async () => {
      setInputValue(input, "secret");
    });

    const submit = container.querySelector<HTMLButtonElement>("button[type=submit]");
    if (!submit) throw new Error("submit button not rendered");
    await act(async () => {
      submit.click();
    });

    expect(loginCalls).toBe(1);
    expect(container.textContent).toContain("Overview");
    expect(document.cookie).not.toContain("goclaw_dashboard_session=");
  });

  it("restores authenticated state on reload through /api/session", async () => {
    authenticated = true;
    document.cookie = "goclaw_dashboard_session=bogus-js-visible-cookie";

    await act(async () => {
      root.render(<App />);
    });

    expect(container.textContent).toContain("Overview");
    expect(container.textContent).not.toContain("Sign in");

    const fetchMock = vi.mocked(fetch);
    const sessionCall = fetchMock.mock.calls.find(([url]) => String(url) === "/api/session");
    expect(sessionCall).toBeDefined();
    expect(sessionCall?.[1]?.credentials).toBe("same-origin");
    expect(fetchMock.mock.calls.some(([url]) => String(url) === "/api/login")).toBe(false);
  });
});
