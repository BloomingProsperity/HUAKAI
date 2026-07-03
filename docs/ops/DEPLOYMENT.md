# HUAKAI — Production Self-Host Deployment Runbook

Brings up the full relay stack with **one** `docker compose` command behind Caddy TLS:
**Caddy** (auto-HTTPS) → **gateway** (relay + admin API) + **frontend** (Next.js Admin UI / user portal),
**postgres**, and a one-shot **migrate** step. Payment is optional — manual admin credit works out of the box;
a real payment provider is a separate commercial add-on, not required to run.

> ⚠️ **Honesty note.** This stack is validated by static checks + per-component builds (`go build` and
> `next build` both pass), and the relay request path is tested against a real Postgres. It has **not** been
> booted end-to-end in CI (the build sandbox has no Docker daemon). Treat the **first** `docker compose up` on
> your host as the real integration test and budget a little time for first-boot surprises. Where a step is
> unverified, it is flagged below.

---

## 0. Prerequisites
- Docker Engine + Docker Compose v2 (`docker compose version`).
- A domain name with a DNS **A/AAAA** record already pointing at this host (needed for Caddy to obtain a
  Let's Encrypt certificate). For a local/staging run without a domain, see §7.
- `openssl` on the host (for secret generation).

## 1. Pre-flight: align migration state (one-time check)
The readiness audit observed the dev database drifting from on-disk migrations (version numbers seen as
146 / 147 / 149). For a **fresh** production database this is a non-issue (the migrate step applies every
file from zero). Only if you are pointing at an **existing** database, first confirm the on-disk count and the
DB's `schema_migrations` version agree, and resolve any dirty state, before continuing:
```bash
ls backend/sql/migrations/*.up.sql | wc -l          # on-disk migration count
# (against an existing DB) SELECT version, dirty FROM schema_migrations;
```

## 2. Generate secrets + the audit key
```bash
cd backend
./deploy/gen-secrets.sh
```
This writes `backend/.env.prod` (gitignored) and `backend/deploy/keys/audit_ed25519.key` (gitignored), and
prints a one-time **admin bootstrap token** — save it. To do it by hand instead, copy `.env.prod.example` to
`.env.prod` and fill it (the generation commands are in that file's header).

## 3. Set your domain
Edit `backend/Caddyfile` and replace `huakai.example.com` with your real domain (the one whose DNS points
here). Caddy will fetch and renew the TLS cert automatically on first start.

## 4. Bring it up
```bash
cd backend
docker compose --env-file .env.prod -f docker-compose.prod.yml up -d --build
```
Order is enforced by the compose: **postgres** (health-gated) → **migrate** (applies all migrations, then
exits) → **gateway** (waits for migrate to complete) + **frontend**, with **caddy** fronting both.

## 5. Smoke test
```bash
# Gateway health (from the host, via Caddy — replace the domain):
curl -fsS https://YOUR_DOMAIN/v1/healthz || curl -fsS http://gateway:8080/healthz
docker compose -f docker-compose.prod.yml ps         # all services Up / healthy; migrate Exited(0)
docker compose -f docker-compose.prod.yml logs gateway | tail -50
```
Then open `https://YOUR_DOMAIN/` in a browser — the Admin UI should load (served by the frontend container;
its `/v1/*` calls are routed to the gateway by Caddy).

*Unverified in CI:* the exact first-boot of all five containers together, the 146 migrations applying cleanly
against a real Postgres, and the in-network `frontend → gateway:8080` connectivity. Watch the `migrate` and
`gateway` logs on first run.

## 6. First admin + registration
- **Bootstrap admin:** use the `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` from step 2 to create the first admin, then
  rotate it (it is one-time by design).
- **User signups:** production defaults to `HUAKAI_USER_REGISTRATION_MODE=disabled` (a fail-closed default).
  To allow signups, set it to `open` or `invite_required` in `.env.prod` and recreate the gateway container.
- **Top-up without a payment provider:** an admin can credit user balances directly (manual credit) — this is
  fully built and needs no external payment integration.

## 7. Local / staging without a domain (HTTP only, no auto-TLS)
In `backend/Caddyfile`, replace the `huakai.example.com {` site line with `:80 {` (and remove the `tls {…}`
block). Caddy then serves plain HTTP on port 80 — fine for a private/staging box, not for public traffic.

## 8. Known limitations (carried from the readiness audit)
- **Not yet booted in CI** (no Docker in the build sandbox) — first real `up` is the integration test.
- **uTLS mimicry templates are not in the gateway image** (they live at repo-root `tools/`, outside the
  backend build context), so mimicry runs with empty templates in Docker. The gateway degrades gracefully
  (warns, continues) and this matches the current gentle-mimicry posture; revisit if mimicry is enabled.
- **Real upstream provider behavior (SSE / 429) is mock-tested only** — validate against a real provider
  account before relying on streaming/rate-limit edge handling in production.
- **Real payment providers are not wired** (manual admin credit only) — a commercial add-on, see the roadmap.

## 9. Follow-ups (tracked, not in this bundle)
- `backend/config.example.yaml` is misleading (the gateway reads env only, not YAML) — to be deprecated.
- Wire `integration_pg` + smoke into CI with the correct DSN env name before commercial launch.
