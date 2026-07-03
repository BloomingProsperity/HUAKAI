# Plan — self-host go-live deploy bundle (Caddy TLS + frontend container + migrate sidecar + audit key + runbook)

Date: 2026-06-19 · Author: Claude PM · Owner-authorized (go-live engineering green-light + "TLS like the reference projects")

## Goal
Close the self-host go-live blockers found by the readiness audit (workflow w9ggcdbh9) so a `docker compose up`
brings up the WHOLE relay stack (Caddy TLS → gateway + frontend, postgres, migrations) — relay core, not payment.
All blockers are deploy-orchestration plumbing; no core feature code is missing.

## #16 triple-mirror — how the references deploy (real configs read)
- sub2api `deploy/Caddyfile` + `deploy/docker-compose.yml` — single app container serves API + an EMBEDDED
  frontend (`backend/internal/web/embed_on.go:27` go:embed all:dist); **Caddy** fronts it for TLS
  (`reverse_proxy localhost:8080`, auto-HTTPS from the domain block, TLS 1.2/1.3, `header_up X-Forwarded-*`).
- new-api `main.go:38` — same embed approach (`go:embed web/default/dist`); compose exposes the app port and
  assumes an external/your-own reverse proxy.
- CLIProxyAPI `docker-compose.yml` — single relay container, exposes a port, external proxy.
- **HUAKAI delta / why not identical**: HUAKAI's frontend is **Next.js** and relies on runtime `rewrites()`
  (`frontend/next.config.mjs:12`) to proxy `/v1`,`/admin/v1`,`/debug` to the gateway — it canNOT be a static
  export embedded into the Go binary the way the references' SPA dist can. So HUAKAI matches the references on
  the **TLS mechanism (Caddy auto-HTTPS)** but runs the frontend as its own Node container; Caddy path-routes
  to two upstreams instead of one. (Embedding the frontend into the gateway binary is a possible later
  optimization requiring a frontend static-export rewrite — out of scope here.)

## Design (Caddy front, two upstreams)
- **caddy** service — terminates TLS (auto Let's Encrypt from the configured domain), routes
  `/v1/*` `/admin/v1/*` `/debug/*` `/.well-known/*` → `gateway:8080`, everything else → `frontend:3000`,
  forwards `X-Forwarded-*`. Mirrors sub2api's Caddy TLS posture (1.2/1.3, health checks).
- **gateway** — unchanged binary; prod compose now passes `HUAKAI_AUDIT_PRIVATE_KEY_PATH` (+ mounts the
  ed25519 key) and `HUAKAI_AUDIT_LEDGER_BACKEND=postgres` (both required in production mode — audit blocker #2),
  no longer exposes 8080 publicly (Caddy fronts it).
- **frontend** — new `frontend/Dockerfile` (`next build` → `next start`, Node server) with
  `HUAKAI_GATEWAY_URL=http://gateway:8080`; runs as a container (audit blocker #1).
- **migrate** — `migrate/migrate:v4.18.1` one-shot sidecar that applies `backend/sql/migrations` against the
  prod DB; gateway `depends_on: migrate: service_completed_successfully` (audit blocker #3).
- **.env.prod.example** + **deploy/gen-secrets.sh** — generate `HUAKAI_CREDENTIAL_KEY_B64` (32B),
  `HUAKAI_SESSION_SIGNING_KEY_B64` (≥32B), the ed25519 audit key, and a `hk_admin_`-prefixed bootstrap token,
  plus document `HUAKAI_USER_REGISTRATION_MODE` (prod defaults disabled — audit blocker #5).
- **docs/ops/DEPLOYMENT.md** — the first-boot runbook (secret gen → migrate → up → smoke /healthz → bootstrap
  admin), the migration-version pre-flight (audit found 146/147/149 drift — align before first boot), and the
  honest note that this stack has NOT been booted in CI (no docker in the build sandbox).

## Changes (files)
1. `backend/Caddyfile` (new) — TLS + path routing.
2. `frontend/Dockerfile` (new) — Next.js Node server image.
3. `backend/docker-compose.prod.yml` (edit) — add caddy, frontend, migrate services; add audit-key env+volume to
   gateway; gateway depends_on migrate; drop public 8080 (Caddy fronts).
4. `backend/.env.prod.example` (new) — every required env + how to generate.
5. `backend/deploy/gen-secrets.sh` (new) — one-shot secret/key generator.
6. `docs/ops/DEPLOYMENT.md` (new) — first-boot runbook.
7. `backend/config.example.yaml` — add a DEPRECATED banner (config loads env only, not YAML — audit blocker #6).

## Success criteria
- `docker compose -f docker-compose.prod.yml config` parses (static validate); Caddyfile `caddy validate` clean
  if available; gateway still `go build` clean.
- Compose passes every required prod env the audit flagged (audit key path + ledger backend + the 3 secrets +
  registration mode documented); no service references an undefined dependency.
- Runbook carries the exact first-boot sequence + the migration-drift pre-flight.

## Blast radius / risk
Deploy artifacts only — no Go/feature code change (gateway binary unchanged). CANNOT be boot-tested in this
sandbox (no docker daemon); first real `docker compose up` validation is an explicit runbook step on the Owner's
machine. Marked accordingly. Owner-authorized (deploy gate satisfied).

## Owner decision points (resolved)
- Go-live engineering: GREEN-LIT ("开始落地"). · TLS: "like sub2/refs" → Caddy auto-HTTPS (this plan).
- Remaining operator inputs (documented in runbook, not code): real domain, DNS, real DB creds, registration mode.
