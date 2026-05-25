# 2026-05-25 Hermes Slice 1.2 docker-compose hermes-runner

| Owner directive | "你是 codex 实施 lane,执行 Hermes phase-1 Slice 1.2: docker-compose hermes-runner。" |
| --- | --- |
| Scope | In: add `backend/deploy/hermes-runner/` Docker wrapper files, wire `backend/docker-compose.dev.yml`, add backend dev env example. Out: real `hermes-agent` integration, SSE streaming, tests, production manifests. |
| Success criteria | Runner image definition uses Python 3.12 slim, non-root uid 10101, tini, curl healthcheck, hash-mode pip install; FastAPI skeleton has unsigned `/healthz`, signed stubs for `/chat`, `/conversations`, `/conversations/{id}/messages`; compose config validates when Docker Compose is available. |
| Time estimate | 45-75 minutes wall clock; single Codex implementation pass. |
| Blast radius | Dev-only Docker Compose and a new runner directory. No frozen package new files; no schema/auth/billing/quota changes; no production deployment files. |
| Failure modes | HMAC canonical mismatch with Go client: compare directly against `backend/internal/hermes/runner_client.go`; incomplete pip hash lock: direct hashes only in Slice 1.2 with explicit Slice 1.3 transitive lock note; Compose env interpolation warning: use safe default placeholder in dev compose. |
| Decision points | No further Owner confirmation needed for dev-only wrapper files. Stop only if implementation requires real secrets, production deploy changes, database schema changes, or frozen package new files. |
| Pre-execution checklist | 1. Read `docs/RULES.md`; 2. Read `backend/internal/hermes/runner_client.go`; 3. Read current `backend/docker-compose.dev.yml`; 4. Verify package version/hash metadata from PyPI where available; 5. Confirm target packages are not frozen packages. |

## Concrete execution order

1. Create `backend/deploy/hermes-runner/requirements.txt` with direct pinned dependencies and hash comments.
2. Create `backend/deploy/hermes-runner/main.py` with FastAPI app, healthz bypass, HMAC middleware, and 501 route stubs.
3. Create `backend/deploy/hermes-runner/entrypoint.sh` with fail-closed shared-secret check and bind parsing.
4. Create `backend/deploy/hermes-runner/Dockerfile` with Python 3.12 slim, curl/tini/gcc, non-root `hermes`, healthcheck, and tini entrypoint.
5. Update `backend/docker-compose.dev.yml` to add `hermes-runner`, `huakai-hermes-data`, healthcheck, env, port, and Postgres health dependency.
6. Add `backend/.env.dev.example` with dev placeholders only.
7. Run available validation: `docker compose -f backend/docker-compose.dev.yml config` or `docker-compose -f backend/docker-compose.dev.yml config`; if unavailable, record skip.
8. Run lightweight Python syntax check for `main.py` if Python can parse it without installed FastAPI; otherwise record dependency-limited skip.

## Assumptions and risks

- `hermes-agent==0.14.0` is MIT per Owner instruction and PyPI metadata check.
- FastAPI is MIT and Uvicorn is BSD-3-Clause; both are permissive for MIT clean-room usage.
- Slice 1.2 intentionally does not import `hermes-agent`; Slice 2 owns real agent integration.
- `pip install --require-hashes` will require all transitive dependency hashes for an actual Docker build. This slice records direct hashes and leaves full generated lock verification to Slice 1.3 per Owner scope.
