const BASE = "";

export async function apiRequest(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string> | undefined),
  };
  if (!headers["Content-Type"] && init?.body) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(String(input), {
    credentials: "same-origin",
    ...init,
    headers,
  });
  if (res.status === 401 && !String(input).startsWith("/api/session")) {
    window.dispatchEvent(new Event("dashboard:unauthorized"));
  }
  return res;
}

export async function apiJson<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const res = await apiRequest(input, init);
  if (!res.ok) {
    const err: ApiError = new Error(`${res.status} ${res.statusText}`);
    err.status = res.status;
    throw err;
  }
  return (await res.json()) as T;
}

export function apiGet<T>(path: string): Promise<T> {
  return apiJson<T>(`${BASE}${path}`, {
    headers: { "X-CSRF-Token": "" },
  });
}

export function apiPost(path: string, body: unknown): Promise<Response> {
  return apiRequest(`${BASE}${path}`, {
    method: "POST",
    body: JSON.stringify(body),
  });
}

export interface ApiError extends Error {
  status?: number;
}
