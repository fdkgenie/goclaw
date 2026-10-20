# GoClaw Dashboard — Deployment Runbook

This runbook documents deploying and operating the standalone GoClaw Dashboard
service (`goclaw-dashboard`), a read-only operational visibility layer over the
GoClaw Gateway (Standard edition / PostgreSQL).

## 1. Prerequisites

- GoClaw Gateway (Standard edition, PostgreSQL) running and reachable.
  - The dashboard never touches the database directly; it consumes gateway APIs
    via WebSocket RPC + event streaming and outbound HTTP with a Bearer API key.
- B1 (`fix/cron-heartbeat-tenant-leak`) and B2 (`feat/dashboard-goclaw-api`)
  merged into `origin/dev` with green CI. The dashboard stacks on `origin/dev`.
- A tenant-scoped API key provisioned for the target tenant (see §3).

## 2. Required Environment Variables

Provided via `/etc/goclaw/dashboard.env` (referenced by the systemd unit's
`EnvironmentFile`):

| Variable | Required | Description |
|----------|----------|-------------|
| `GOCLAW_API_URL` | Yes | Gateway HTTP base URL, e.g. `http://127.0.0.1:18790` |
| `GOCLAW_WS_URL` | No | Gateway WS URL. Defaults to `GOCLAW_API_URL` with `ws(s)` scheme + `/ws` |
| `GOCLAW_API_KEY` | Yes | Tenant-scoped Bearer API key (see §3) |
| `DASHBOARD_PASSWORD` | Yes | Password for the dashboard web login |
| `DASHBOARD_ADDR` | No | HTTP listen address, default `127.0.0.1:18795` |
| `GOCLAW_TENANT_ID` | Yes | Tenant UUID the dashboard is pinned to (fail-closed) |
| `DASHBOARD_WEB_DIR` | No | Optional directory to serve the frontend UI from |
| `DASHBOARD_TRUSTED_PROXY` | No | `true` when a reverse proxy terminates TLS in front of the dashboard. The `X-Forwarded-Proto` header is then trusted to decide whether a request arrived over HTTPS. Default `false` (the header is ignored) |
| `DASHBOARD_PRODUCTION` | No | `true` enables the production hardening: a login over plain HTTP is refused and the session cookie is always `Secure`. Default `false` |

## 3. Tenant-Bound API Key Provisioning (Critical)

The dashboard is **tenant-pinned with fail-closed security**. It only serves data
for the tenant in `GOCLAW_TENANT_ID`, and startup aborts if the gateway's
connect response or `/v1/agents` lists any agent outside that tenant.

The operator **must** provision an API key scoped to the target tenant — do NOT
use an owner/system-level key:

```
# Create the API key within the target tenant's scope
curl -X POST http://gateway:18790/v1/api_keys.create \
  -H "Authorization: Bearer <owner-key>" \
  -H "Content-Type: application/json" \
  -d '{"tenant_id": "<tenant-uuid>", "scopes": ["agents.read", "traces.read", "usage.read", "teams.read"]}'
```

Use the returned key as `GOCLAW_API_KEY`. The gateway enforces tenant scope
server-side, so the dashboard can never read another tenant's data even if
misconfigured.

### 3.1 Fail-Closed Behaviour

The tenant canary (`GET /v1/agents` on the gateway) is re-run on **every**
gateway reconnect, not just at startup:

| Phase | Behaviour |
|-------|-----------|
| Startup | The canary runs before the HTTP server starts. A mismatch aborts the daemon. |
| Gateway disconnect | The circuit breaker opens. The data API and the SSE stream answer `503`. |
| Reconnect | The canary re-runs. If it fails, the client keeps backing off and stays disconnected, and the service stays `503`. |
| Canary passes | The breaker closes, the live state is marked unverified, and the service resumes. |

`GET /api/session` stays reachable while degraded so the SPA can still render
its shell instead of the login screen. A `503` from the data API therefore
means "the gateway connection dropped and the tenant is not verified yet" —
check the gateway, not the dashboard credentials.

## 3. Provisioning Steps

1. Install the binary:
   ```bash
   install -m 0755 goclaw-dashboard /usr/local/bin/goclaw-dashboard
   ```

2. Create the system user and directories:
   ```bash
   useradd --system --no-create-home --shell /usr/bin/nologin goclaw
   mkdir -p /var/lib/goclaw /var/log/goclaw /etc/goclaw
   chown -R goclaw:goclaw /var/lib/goclaw /var/log/goclaw
   ```

3. Write the environment file `/etc/goclaw/dashboard.env` with the variables from
   §2 (never commit secrets to version control).

4. Optional: build and copy the frontend UI.
   The release binary embeds the built SPA, so a plain install needs no extra
   step. To rebuild it from source:
   ```bash
   make dashboard-build   # builds the SPA and links it into ./goclaw-dashboard
   ```
   To serve the UI from a directory instead (e.g. for hot iteration), point
   `DASHBOARD_WEB_DIR` at a built `ui/dashboard/dist` directory or pass
   `--web-dir` to the binary.

5. Install the systemd unit:
   ```bash
   cp deploy/systemd/goclaw-dashboard.service /etc/systemd/system/
   systemctl daemon-reload
   systemctl enable --now goclaw-dashboard
   ```

6. Verify:
   ```bash
   systemctl status goclaw-dashboard
   journalctl -u goclaw-dashboard -f
   curl -s http://127.0.0.1:18795/api/overview  # returns 401 without session
   ```

## 4. Reverse Proxy (Nginx / Caddy)

The dashboard listens on `127.0.0.1:18795` by default (localhost only). For
remote access, place Nginx or Caddy in front with TLS and optional auth:

### Nginx example

```nginx
server {
    listen 443 ssl;
    server_name dashboard.example.com;

    ssl_certificate /etc/ssl/certs/dashboard.example.com.crt;
    ssl_certificate_key /etc/ssl/certs/dashboard.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:18795;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;   # required for the Secure cookie flag
        proxy_set_header Origin $scheme://$host;  # required for CSRF check
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

The dashboard's CSRF middleware verifies the `Origin` header matches the
request host, so the reverse proxy must forward the client's `Origin`.

### 4.1 Session cookie and TLS termination

The session cookie carries the `Secure` flag when the request arrived over
HTTPS. With a TLS-terminating proxy the dashboard sees a plain HTTP request,
so it needs two things to issue a `Secure` cookie:

1. the proxy forwards `X-Forwarded-Proto: $scheme`, and
2. `DASHBOARD_TRUSTED_PROXY=true` in `/etc/goclaw/dashboard.env`.

Without `DASHBOARD_TRUSTED_PROXY` the header is ignored on purpose: an
attacker could otherwise forge it and obtain a `Secure` cookie for a request
that never crossed TLS.

Set `DASHBOARD_PRODUCTION=true` for a hardened deployment: the dashboard then
refuses a login that did not arrive over HTTPS (plain `400 https required`)
and always issues the cookie with the `Secure` flag. The proxy must
therefore terminate TLS, and the dashboard must be reachable only through it.

## 5. Logs

```bash
journalctl -u goclaw-dashboard -f
```

Security-relevant events are logged with `slog.Warn` at keys:
- `security.dashboard.method_blocked` — rejected RPC method or HTTP path
- `security.dashboard.tenant_mismatch` — tenant canary failure (fail-closed)

## 6. Upgrades

```bash
systemctl stop goclaw-dashboard
install -m 0755 goclaw-dashboard.new /usr/local/bin/goclaw-dashboard
systemctl start goclaw-dashboard
journalctl -u goclaw-dashboard -n 50
```

## 7. HTTP Surface

All API paths require the session cookie issued by `POST /api/login` (the
cookie is `HttpOnly`; the login response body never contains the token).

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/api/login` | Exchange `DASHBOARD_PASSWORD` for the session cookie |
| `POST` | `/api/logout` | Invalidate the session and clear the cookie |
| `GET` | `/api/session` | Authenticated state; used by the SPA to restore a session on reload |
| `GET` | `/api/overview` | Composite KPI (live runs, task KPI, cost) |
| `GET` | `/api/orgchart` | Team/agent DAG |
| `GET` | `/api/cost` | Cost summary and top-N breakdown |
| `GET` | `/api/activity` | Recent activity from the in-memory ring buffer (`?limit=`, 1..500) |
| `GET` | `/api/stream` | Server-Sent Events: initial snapshot then live deltas |

`GET /api/stream` is the live SSE channel the SPA subscribes to. It requires a
valid session and is gated by the same fail-closed breaker as the data API.

## 8. Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| `exit status 1` on startup, `tenant canary` error | Gateway tenant != `GOCLAW_TENANT_ID` | Verify `/v1/agents` all match the tenant UUID |
| `exit status 1`, `missing capability` | Gateway missing B1/B2 capabilities | Upgrade gateway to a build with B1+B2 merged |
| `401` on all API calls | Missing/invalid session cookie | Log in via `/api/login` with `DASHBOARD_PASSWORD` |
| `403` on POST from reverse proxy | Missing/mismatched `Origin` header | Configure proxy to forward `Origin` |
| `400 https required` on login | `DASHBOARD_PRODUCTION=true` and the request did not arrive over HTTPS | Terminate TLS at the proxy, forward `X-Forwarded-Proto` and set `DASHBOARD_TRUSTED_PROXY=true` |
| `503` on data API and `/api/stream` | Gateway disconnected; tenant not re-verified yet | Check the gateway and the `security.dashboard.tenant_mismatch` log |
| `404` on `/` with "dashboard UI not built" | Binary built without the `dashboardui` tag | Rebuild with `make dashboard-build`, or set `DASHBOARD_WEB_DIR` |
| Service won't start, sandbox denied | `ProtectSystem=strict` blocking a path | Add path to `ReadWritePaths` |

