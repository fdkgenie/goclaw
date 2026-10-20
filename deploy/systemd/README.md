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

4. Optional: build and copy the frontend UI:
   ```bash
   cd ui/dashboard && pnpm install --frozen-lockfile && pnpm build
   # Copy dist to /var/lib/goclaw/dashboard-ui/dist (or set DASHBOARD_WEB_DIR)
   ```

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
        proxy_set_header Origin $scheme://$host;  # required for CSRF check
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

The dashboard's CSRF middleware verifies the `Origin` header matches the
request host, so the reverse proxy must forward the client's `Origin`.

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

## 7. Troubleshooting

| Symptom | Likely Cause | Fix |
|---------|--------------|-----|
| `exit status 1` on startup, `tenant canary` error | Gateway tenant != `GOCLAW_TENANT_ID` | Verify `/v1/agents` all match the tenant UUID |
| `exit status 1`, `missing capability` | Gateway missing B1/B2 capabilities | Upgrade gateway to a build with B1+B2 merged |
| `401` on all API calls | Missing/invalid session cookie | Log in via `/api/login` with `DASHBOARD_PASSWORD` |
| `403` on POST from reverse proxy | Missing/mismatched `Origin` header | Configure proxy to forward `Origin` |
| Service won't start, sandbox denied | `ProtectSystem=strict` blocking a path | Add path to `ReadWritePaths` |
