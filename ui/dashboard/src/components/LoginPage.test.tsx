import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { act } from "react";
import ReactDOM from "react-dom/client";
import { LoginPage } from "./LoginPage";
import { ThemeProvider } from "../theme/theme-context";

let container: HTMLDivElement;
let root: ReactDOM.Root;
let onLogin: ReturnType<typeof vi.fn>;
let loginStatus: number;

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

function text(): string {
  return container.textContent ?? "";
}

function alertText(): string {
  return container.querySelector('[role="alert"]')?.textContent ?? "";
}

async function render() {
  await act(async () => {
    root.render(
      <ThemeProvider>
        <LoginPage onLogin={onLogin} />
      </ThemeProvider>,
    );
  });
}

async function submitPassword(password: string) {
  const input = container.querySelector<HTMLInputElement>("#dashboard-password");
  if (!input) throw new Error("password input not rendered");
  await act(async () => {
    setInputValue(input, password);
  });

  const submit = container.querySelector<HTMLButtonElement>("button[type=submit]");
  if (!submit) throw new Error("submit button not rendered");
  await act(async () => {
    submit.click();
  });
}

beforeEach(() => {
  loginStatus = 401;
  onLogin = vi.fn();
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => json({ authenticated: false }, loginStatus)),
  );

  container = document.createElement("div");
  document.body.appendChild(container);
  root = ReactDOM.createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
  vi.unstubAllGlobals();
  window.localStorage?.clear();
});

describe("LoginPage", () => {
  it("renders the GoClaw brand header, password field and submit button", async () => {
    await render();

    const heading = container.querySelector("h1");
    expect(heading?.textContent).toBe("GoClaw");

    const logo = container.querySelector("img");
    expect(logo?.getAttribute("alt")).toBe("GoClaw");
    expect(logo?.getAttribute("src")).toBe("/goclaw-icon.svg");

    expect(text()).toContain("Sign in to the GoClaw Dashboard");

    const label = container.querySelector("label[for='dashboard-password']");
    expect(label?.textContent).toBe("Password");

    const input = container.querySelector<HTMLInputElement>("#dashboard-password");
    expect(input?.type).toBe("password");

    const submit = container.querySelector<HTMLButtonElement>("button[type=submit]");
    expect(submit?.textContent).toBe("Sign in");
  });

  it("submits the password to /api/login and reports success", async () => {
    loginStatus = 200;
    await render();

    await submitPassword("s3cret");

    const fetchMock = vi.mocked(fetch);
    const call = fetchMock.mock.calls.find(([url]) => String(url) === "/api/login");
    expect(call).toBeDefined();
    expect(call?.[1]?.method).toBe("POST");
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({ password: "s3cret" });
    expect(onLogin).toHaveBeenCalledTimes(1);
    expect(alertText()).toBe("");
  });

  it("shows the invalid password message on 401", async () => {
    loginStatus = 401;
    await render();

    await submitPassword("wrong");

    expect(alertText()).toContain("Invalid password");
    expect(onLogin).not.toHaveBeenCalled();
  });

  it("shows the forbidden origin message on 403", async () => {
    loginStatus = 403;
    await render();

    await submitPassword("s3cret");

    expect(alertText()).toContain("request origin not allowed");
    expect(onLogin).not.toHaveBeenCalled();
  });

  it("shows the too many attempts message on 429", async () => {
    loginStatus = 429;
    await render();

    await submitPassword("s3cret");

    expect(alertText()).toContain("Too many login attempts");
    expect(onLogin).not.toHaveBeenCalled();
  });

  it("shows the invalid password message when the request fails", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => {
        throw new Error("network down");
      }),
    );
    await render();

    await submitPassword("s3cret");

    expect(alertText()).toContain("Invalid password");
    expect(onLogin).not.toHaveBeenCalled();
  });

  it("disables the submit button while the login request is in flight", async () => {
    let release: (() => void) | undefined;
    vi.stubGlobal(
      "fetch",
      vi.fn(
        () =>
          new Promise<Response>((resolve) => {
            release = () => resolve(json({ authenticated: true }, 200));
          }),
      ),
    );
    await render();

    const input = container.querySelector<HTMLInputElement>("#dashboard-password");
    if (!input) throw new Error("password input not rendered");
    await act(async () => {
      setInputValue(input, "s3cret");
    });

    const submit = container.querySelector<HTMLButtonElement>("button[type=submit]");
    if (!submit) throw new Error("submit button not rendered");
    await act(async () => {
      submit.click();
    });

    expect(submit.disabled).toBe(true);
    expect(submit.textContent).toBe("Signing in…");

    await act(async () => {
      release?.();
    });

    expect(onLogin).toHaveBeenCalledTimes(1);
  });

  it("exposes a theme toggle that flips the document theme class", async () => {
    await render();

    const toggle = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Toggle theme"]',
    );
    expect(toggle).not.toBeNull();

    const before = document.documentElement.className;
    await act(async () => {
      toggle?.click();
    });
    expect(document.documentElement.className).not.toBe(before);
    expect(["light", "dark"]).toContain(document.documentElement.className);
  });
});
