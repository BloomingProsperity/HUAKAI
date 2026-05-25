Lane: codex (general-purpose subagent) — independent draft, has NOT read claude-lane file
UTC: 2026-05-25T (drafted in single pass under auto-mode)

# Hermes Phase-1 Slice 1 — codex lane plan

Drives the 5-item "First Implementation Slice" of `docs/plans/2026-05-24-hermes-native-integration.md`.
Independent of the claude-lane file with the same date stem.

## 1. Scope (in/out)

In (this slice ships):
- DB: 3 new tables only — `hermes_settings`, `hermes_api_profiles`, `hermes_audit_events` — via migration `0057_hermes_phase1_slice1_core.up.sql` (+ `.down.sql`).
- Go package: `backend/internal/hermes/` (new, non-frozen) — config types, profile crypto, settings store, audit emit, runner-bridge client.
- Go package: `backend/internal/hermeshttp/` (new, non-frozen) — Gin handlers for the 6 endpoints listed below; mounted from the existing top-level mux assembly (not edited inside `gatewayhttp/`; instead wired in `cmd/gateway/main.go`).
- Endpoints behind operator+tenant auth (re-uses `backend/internal/auth.APIKeyResolver`):
  - `GET  /v1/hermes/settings`
  - `POST /v1/hermes/settings/enable`
  - `POST /v1/hermes/settings/disable`
  - `POST /v1/hermes/chat`              (server-side passthrough to runner; SSE streaming)
  - `GET  /v1/hermes/conversations`     (proxy to runner — runner is source of truth this slice)
  - `GET  /v1/hermes/conversations/{id}/messages`
- Dedicated Hermes inbound API-key issuance: re-uses `backend/internal/admin/keygen.GenerateBearer` with new namespace `hk_hermes_`; rows live in existing `api_keys` table tagged `purpose='hermes_runner'`.
- `hermes-runner` Docker Compose service definition added to `backend/docker-compose.dev.yml` — image built locally (`backend/deploy/hermes-runner/Dockerfile`), pip installs `hermes-agent==0.14.0` with sha256 hash pin, mTLS-or-shared-secret bridge to gateway.
- Audit events for: `hermes.enable`, `hermes.disable`, `hermes.profile.create`, `hermes.profile.rotate`, `hermes.chat.start`.

Out of scope (deferred slices):
- `hermes_conversations`, `hermes_messages`, `hermes_memory_items`, `hermes_tool_calls`, `hermes_runtime_events` tables — runner keeps these in its own volume this slice; Slice 2 lifts into PG.
- HUAKAI skill-pack tools (`huakai.request_diagnose` etc.) — Slice 3.
- UI/frontend.
- Per-tenant filesystem isolation hardening beyond `/var/lib/huakai/hermes/tenants/{tid}/`.
- External `external_api` source mode — Slice 1 ships `managed_huakai_api` + `dedicated_group` only.

## 2. Success criteria (machine-checkable)

- `go build ./...` clean.
- `go test ./backend/internal/hermes/... ./backend/internal/hermeshttp/...` green; each test states the regression it catches in a top-of-file comment (CLAUDE.md #14).
- `migrate -path backend/sql/migrations -database $DEV_DSN up` and reverse `down 1` both succeed without orphan FKs (round-trip test in CI lane).
- `docker compose -f backend/docker-compose.dev.yml up -d hermes-runner postgres gateway` brings hermes-runner healthy within 60 s (`/healthz` 200).
- Manual curl smoke (recorded in PR body, not committed):
  1. `POST /v1/hermes/settings/enable` returns 200 + new `hermes_audit_events` row with action=`hermes.enable`.
  2. `POST /v1/hermes/chat` with `{"messages":[{"role":"user","content":"ping"}]}` returns SSE stream containing at least one assistant delta event.
  3. `POST /v1/hermes/settings/disable` flips state and emits second audit row.
  4. Disabling tenant A does not affect tenant B's `GET /v1/hermes/settings`.
- Zero new file under `backend/internal/{gatewayhttp,gateway,proto}` (mechanical grep gate, see §7).

## 3. Time estimate (3-day base, 5-day with mTLS, 7-day with everything)

Sub-day milestones:
- D1 AM: schema gate consultation; D1 PM: migration + sqlc regen; D1 EOD: hermes package skeleton.
- D2 AM: settings + profile crypto; D2 PM: audit emit; D2 EOD: keygen wiring + tests.
- D3 AM: handler scaffold + auth middleware reuse; D3 PM: hermes-runner Dockerfile + compose stanza; D3 EOD: bridge auth (shared-secret minimum).
- D4 (optional): switch shared-secret to mTLS; certificate generation script.
- D5 (optional): SSE backpressure tuning + concurrent-tenant integration test on real PG.
- D6–D7 (buffer): codex review spirals + S0/S1 fixes.

Probability split: 60% finish ≤ D4, 30% finish D5–D6, 10% blocked on Owner mTLS decision → ship shared-secret.

## 4. Blast radius

- Schema: 3 new tables, all `hermes_*` prefix, no FK into core tables except `api_keys(id)` on `hermes_api_profiles.api_key_id` (nullable) and `tenants(id)`. **Tenant FK is composite** `(tenant_id, …)` to match the existing tenant-composite-FK pattern from migration `0041_tenant_composite_foreign_keys`. Production impact: **low** — additive only, no backfill.
- API: 6 new endpoints under `/v1/hermes/*`. Existing routes untouched. Production impact: **none** until feature is enabled per-tenant.
- Deploy: introduces a NEW service `hermes-runner` to Compose. In production deployments the service can be left scaled-to-zero until Slice 2 ships memory persistence. Production impact: **none** for installs that don't enable it; **medium** for installs that do (new process under supervision).
- Frozen-package risk: zero — wiring touches only `cmd/gateway/main.go` (mux registration), not the three frozen packages.
- Secret material: per-tenant `external_api` key encryption is deferred (out of scope); the slice's `hk_hermes_*` keys reuse the existing bcrypt-stored `api_keys` row pattern from `backend/internal/admin/keygen.go:55`.

**Overall production-impact level**: **LOW-to-MEDIUM** (low if hermes-runner scaled to zero; medium if enabled).

## 5. Decision points (D1..D8 — each must surface to Owner before execution)

### D1 — Build 3 tables now vs all 7

**A**: ship 3 (`hermes_settings`, `hermes_api_profiles`, `hermes_audit_events`) only — runner owns conversation/memory state in its volume.
**B**: ship 5 (add `hermes_conversations`, `hermes_messages`) so chat history is queryable from PG day-1.
**C**: ship all 7 in one migration.
**D**: ship 1 (`hermes_settings`) only; profiles + audit Slice 2.

Recommendation: **A**. Smallest schema surface that still satisfies the audit + enable-toggle requirement; conversations/messages can be lifted in Slice 2 once runner contract is observed.

参考项目对照:
- one-api `model/token.go:23-37` — ships a single `Token` table for issued keys; gateway concerns live in `model/user.go` separately, never co-migrated. Confirms incremental-table discipline.
- LiteLLM `litellm/proxy/management_helpers/audit_logs.py:182-220` — audit-log row uses `object_id` foreign-id rather than a typed FK, so audit table is decoupled from any specific entity table. Supports landing audit first, history later.
- CLIProxyAPI `internal/api/handlers/management/api_key_usage.go:11-30` — usage telemetry is read from per-key auth state in memory, not a dedicated PG table; recent-request buckets are computed lazily. Confirms history table is optional in v1.

### D2 — Dedicated Hermes inbound key: reuse `api_keys` table vs new `hermes_api_keys`

**A**: reuse `api_keys` with new `purpose='hermes_runner'` column or tag and `key_prefix='hk_hermes_'`.
**B**: new dedicated `hermes_api_keys` table with own FK to tenant.
**C**: piggyback admin token (no new key concept).
**D**: managed_huakai mode mints an ephemeral JWT scoped to Hermes, no DB row at all.

Recommendation: **A**. Resolver code path stays identical (single bcrypt-fanout lookup), zero new SQL queries, audit-log linkage uses existing `api_key_id` column. Adds a single `purpose TEXT` column to `api_keys` (also needed for future runner/skill-pack keys).

参考项目对照:
- one-api `model/token.go:23-37` — single `Token` table serves all key purposes (user, admin, dedicated); discriminator is `Name string` + `Models` list. Matches option A.
- new-api `model/token.go` — same single-table-with-tags pattern (AGPL — paraphrased mechanism only, no source borrow).
- LiteLLM `litellm/proxy/_experimental/mcp_server/byok_oauth_endpoints.py:11-13` — separate OAuth flow per-MCP-server (option D analogue); only justified when the credential is user-supplied at runtime, which is NOT the case for Hermes managed key.

### D3 — Runner startup: lazy on enable vs always-on

**A**: always-on (Compose `restart: unless-stopped`); idle if no tenant has Hermes enabled.
**B**: lazy — gateway sends a `docker start hermes-runner` shell-out on first enable.
**C**: hybrid — always-on but runner self-suspends when zero active tenants for 10 min.
**D**: per-enable subprocess (no daemon).

Recommendation: **A**. The plan §Deployment explicitly says "The UI switch should not need to create Docker containers at runtime". Avoids Docker-socket privilege escalation in the gateway. Idle cost is one Python process holding ~150 MiB.

参考项目对照:
- invariant-gateway `gateway/serve.py` + `run.sh` — single-process always-on FastAPI runner, sidecar to whatever LLM gateway is calling it. Matches option A.
- CLIProxyAPI `internal/api/handlers/management/oauth_callback.go` — runtime config changes (auth file rotation) handled in-process without container restart; reinforces "config change ≠ process restart".

### D4 — Runner→gateway auth: mTLS vs shared secret vs signed JWT

**A**: mTLS with internal CA, runner-side cert mounted from Compose secret.
**B**: HMAC shared-secret in env var; rotate by rolling Compose deploy.
**C**: short-lived signed JWT issued by gateway at runner startup via a `/internal/runner/bootstrap` call.
**D**: rely on Docker network isolation only (no app-layer auth).

Recommendation: **B for Slice 1, C as Slice 2 migration target, A only if Owner requires it for compliance**. B ships in hours; C is the right long-term answer because it gives per-process audit identity without manual cert rotation; A is overkill for a single-machine Compose deploy and adds operational toil.

参考项目对照:
- LiteLLM `litellm/proxy/_experimental/mcp_server/byok_oauth_endpoints.py:50-92` — uses bearer JWT for MCP-client auth with `(type="byok_session")` claim. Matches option C target.
- Portkey gateway `src/middlewares/log/index.ts` + `src/handlers/services/logsService.ts` — internal service calls use header-based auth, no mTLS overhead. Supports option B for slice-1.
- one-api `model/token.go:26` — `Key char(48) uniqueIndex` plaintext-token model; equivalent to option B with bcrypt over the wire. Confirms shared-secret is a normal pattern in MIT gateways.

### D5 — When to land the remaining 4 tables (`conversations`, `messages`, `memory_items`, `tool_calls`, `runtime_events`)

**A**: Slice 2 (immediately after this one), one migration `0058_hermes_phase1_slice2_history.up.sql`.
**B**: Split — `conversations` + `messages` in Slice 2; `memory_items` + `tool_calls` + `runtime_events` in Slice 3.
**C**: Wait for runner v0.14.0 to declare which fields it stabilises; defer all 4 to Slice 3.
**D**: Never persist in PG — runner volume only.

Recommendation: **B**. Conversation history is the most-requested view per plan §Product Shape; it should be queryable from PG so admins can audit per-tenant. Memory + tool-call ledgers can wait because they need careful PII review (CLAUDE.md trust-chain feedback).

参考项目对照:
- LiteLLM `litellm/proxy/memory/memory_endpoints.py:278-541` — memory CRUD endpoints with `@router.post/get/put/delete`; clean separation between conversation memory and tool-invocation audit. Supports staged rollout (option B).
- LiteLLM `litellm/proxy/management_helpers/audit_logs.py:160-220` — audit row uses generic `object_id` so any future entity (conversation, message, tool-call) can be referenced without schema change. Lets Slice 1 audit table outlive Slice 2 schema work.

### D6 — `hermes-agent v0.14.0` lock policy: sha pin vs version range

**A**: pin `hermes-agent==0.14.0` + `--require-hashes` with sha256 in `requirements.txt`.
**B**: range `hermes-agent>=0.14.0,<0.15.0`.
**C**: vendor the wheel into `backend/vendor/hermes-agent-0.14.0/`.
**D**: pin sha + supply-chain attest (sigstore / cosign verify in Dockerfile).

Recommendation: **A with documented escalation path to D in Slice 3**. Supply-chain attest is the right answer but adds CI complexity; Slice 1 should ship deterministic builds without forcing the attest infrastructure.

参考项目对照:
- LiteLLM `pyproject.toml` (see `dependencies = [...]` block, exact pins) — production proxy uses exact pins for security-critical deps.
- invariant-gateway `pyproject.toml` + `uv.lock` — uses uv lockfile which is sha-pinned by construction; matches option A semantics with a lock-file mechanism.
- CLIProxyAPI `go.sum` + Go modules — entire ecosystem is sha-locked by default; confirms hash-pin is industry standard.

### D7 — MCP server (HUAKAI exposes `huakai.*` tools to runner): new package vs reuse admin handler

**A**: new package `backend/internal/hermesmcp/` (Slice 3 mostly, stub registered in Slice 1).
**B**: reuse `backend/internal/adminhttp/api_keys_handler.go`-shaped registry; add an MCP adapter layer.
**C**: defer entirely to Slice 3, don't even stub.
**D**: expose tools directly from `backend/internal/hermes/` (single package).

Recommendation: **C for Slice 1**. Plan §First Implementation Slice line 169-171 explicitly says "Operations tools should come after the chat and persistence path is stable". Slice 1 should not ship MCP surface, period — adding even a stub creates premature contract.

参考项目对照:
- LiteLLM `litellm/proxy/_experimental/mcp_server/mcp_server_manager.py` (file exists at this exact path; see `ls` output line 8) — MCP server is in its own package separate from the proxy mainline. Confirms Slice 3 should be a NEW package, not folded into Hermes core (rejects option D).
- invariant-gateway `gateway/serve.py` — sidecar service, NOT an MCP server; tools are exposed via in-process registry. Shows there are alternative architectures; reinforces "decide carefully in Slice 3, not now".

### D8 — Tenant isolation: per-tenant volume vs single-volume-path-prefix

**A**: per-tenant Docker volume (`hermes-runner-tenant-${tid}`) — strongest filesystem isolation.
**B**: single volume `/var/lib/huakai/hermes/` with path-prefix per tenant — runner enforces isolation in code.
**C**: ephemeral tmpfs + PG-backed durable state (couples to D5).
**D**: per-tenant subprocess inside runner container, each chrooted.

Recommendation: **B**. Single-volume + path-prefix matches the plan §Data Isolation diagram literally; per-volume hits Docker's volume-count limits at scale; chroot inside container adds attack surface without buying much over filesystem permissions. The runner enforces tenant scoping; gateway double-checks at request boundary.

参考项目对照:
- LiteLLM proxy `db.py` (in `_experimental/mcp_server/` per `ls` output line 5) — single shared store, tenant scoping enforced at query layer. Matches option B.
- invariant-gateway `gateway/__init__.py` + `serve.py` — single FastAPI process, per-request tenant in context; no per-tenant filesystem split. Same shape as option B.
- LiteLLM `litellm/proxy/memory/memory_endpoints.py:399-541` — memory endpoints accept user/tenant scoping in URL path and enforce in handler. Supports query-level isolation over filesystem-level.

## 6. Step-by-step implementation order

1. **Schema gate (Owner approval required)** — surface D1, D2, D5, D8 to Owner via AskUserQuestion; do NOT write migrations until decisions land. Each option carries the references above (CLAUDE.md #15 compliant).
2. **Migration `0057_hermes_phase1_slice1_core`** — assuming D1=A, D2=A, D8=B:
   - `ALTER TABLE api_keys ADD COLUMN purpose TEXT NOT NULL DEFAULT 'user';` + partial index `WHERE purpose != 'user'`.
   - `CREATE TABLE hermes_settings (tenant_id BIGINT, user_id BIGINT, enabled BOOLEAN, api_source TEXT CHECK IN ('managed_huakai_api','dedicated_group'), profile_id BIGINT, updated_at TIMESTAMPTZ, PRIMARY KEY(tenant_id,user_id))` + composite tenant FK.
   - `CREATE TABLE hermes_api_profiles (id BIGSERIAL, tenant_id BIGINT, owner_user_id BIGINT, name TEXT, api_key_id BIGINT REFERENCES api_keys(id), created_at TIMESTAMPTZ, ...)`.
   - `CREATE TABLE hermes_audit_events (id BIGSERIAL, ts TIMESTAMPTZ, tenant_id BIGINT, actor_user_id BIGINT, action TEXT, sanitized_args JSONB, result TEXT, correlation_id TEXT, request_id TEXT)`.
   - Reverse `.down.sql` drops in reverse dependency order.
   - Round-trip test: apply, `migrate down 1`, re-apply, no orphans (uses local PG per `reference_local_pg_verification`).
3. **`backend/internal/hermes/` package** — settings store (`settings.go`), profile crypto + storage (`profiles.go`), audit emitter (`audit.go`), runner bridge client (`runner_client.go`), config types (`types.go`). One file per responsibility; <200 LoC each. Each `_test.go` declares the regression at top-of-file.
4. **`backend/internal/hermeshttp/` package** — Gin handlers per endpoint, one file per route family (`settings_handler.go`, `chat_handler.go`, `conversations_handler.go`, `profiles_handler.go`). Re-uses `backend/internal/auth.APIKeyResolver` middleware.
5. **Runner Dockerfile** at `backend/deploy/hermes-runner/Dockerfile`: `FROM python:3.12-slim`; `pip install --require-hashes -r requirements.txt`; non-root user; healthcheck.
6. **`backend/docker-compose.dev.yml`** — add `hermes-runner` service stanza alongside `postgres`; depends_on PG-ready; mounts `hermes-runtime-data` named volume; env wires shared-secret from `.env.dev`.
7. **Wiring in `cmd/gateway/main.go`** — register `hermeshttp.NewRouter(...)` into the existing mux. This is the ONLY edit to `cmd/gateway/main.go`; no edit to `internal/{gatewayhttp,gateway,proto}`.
8. **Audit wiring** — every enable/disable/profile-mutation/chat-start writes a row via `hermes.audit.Emit(ctx, action, sanitizedArgs)`. Test: each handler emits exactly one row per success path (mutation test: delete the Emit call → test goes red).
9. **Tests** (mutation-checked per CLAUDE.md #14):
   - Settings round-trip — passes only if `enabled` is persisted; fails if storage stub returns nil.
   - Audit fixture — fails if `sanitized_args` retains a key field (use a fixture that includes a literal `"api_key"` substring; assertion checks redaction).
   - Tenant isolation — tenant A enable does NOT flip tenant B's setting; fails if WHERE clause omits tenant_id.
   - Profile crypto — encrypt(plain) → decrypt → matches; fails if either side stubs to identity.
   - Bridge shared-secret — wrong-secret request returns 401 from runner mock; fails if middleware is skipped.
10. **Codex review per CLAUDE.md #8** — `codex exec review --uncommitted --full-auto --sandbox read-only`; resolve S0/S1; defer S2/S3 with note.
11. **Manual curl smoke recorded in PR body**, full-suite `go test ./...` before commit.

## 7. New-file target-package roster (frozen-list compliance)

| File                                                          | Target package                       | Frozen? | Why OK                                                  |
| ------------------------------------------------------------- | ------------------------------------ | ------- | ------------------------------------------------------- |
| `backend/sql/migrations/0057_hermes_phase1_slice1_core.up.sql`  | (n/a — SQL)                         | n/a     | Migrations dir is not a Go package.                     |
| `backend/sql/migrations/0057_hermes_phase1_slice1_core.down.sql` | (n/a — SQL)                       | n/a     | "                                                       |
| `backend/internal/hermes/types.go`                            | `hermes`                             | NO      | New package — not in frozen list.                       |
| `backend/internal/hermes/settings.go`                         | `hermes`                             | NO      | "                                                       |
| `backend/internal/hermes/profiles.go`                         | `hermes`                             | NO      | "                                                       |
| `backend/internal/hermes/audit.go`                            | `hermes`                             | NO      | "                                                       |
| `backend/internal/hermes/runner_client.go`                    | `hermes`                             | NO      | "                                                       |
| `backend/internal/hermes/*_test.go`                           | `hermes`                             | NO      | "                                                       |
| `backend/internal/hermeshttp/settings_handler.go`             | `hermeshttp`                         | NO      | New package.                                            |
| `backend/internal/hermeshttp/chat_handler.go`                 | `hermeshttp`                         | NO      | "                                                       |
| `backend/internal/hermeshttp/conversations_handler.go`        | `hermeshttp`                         | NO      | "                                                       |
| `backend/internal/hermeshttp/profiles_handler.go`             | `hermeshttp`                         | NO      | "                                                       |
| `backend/internal/hermeshttp/router.go`                       | `hermeshttp`                         | NO      | "                                                       |
| `backend/internal/hermeshttp/*_test.go`                       | `hermeshttp`                         | NO      | "                                                       |
| `backend/deploy/hermes-runner/Dockerfile`                     | (n/a — Docker)                       | n/a     | Not a Go package.                                       |
| `backend/deploy/hermes-runner/requirements.txt`               | (n/a — Python)                       | n/a     | "                                                       |
| `backend/deploy/hermes-runner/entrypoint.sh`                  | (n/a — shell)                        | n/a     | "                                                       |

Edits (no new file in frozen package):
- `cmd/gateway/main.go` — single edit to register `hermeshttp` router.
- `backend/docker-compose.dev.yml` — add `hermes-runner` service stanza.
- `backend/sql/queries/hermes_*.sql` — new sqlc query files (separate dir, not a Go package).

Frozen-package compliance: zero new files under `backend/internal/{gatewayhttp,gateway,proto}`. Mechanical gate in CI: `git diff --name-only main..HEAD | grep -E '^backend/internal/(gatewayhttp|gateway|proto)/[^/]+\.go$' | wc -l` must be 0 for new files (existing-file edits allowed only if minimal).

## 8. Risks not yet decided

- Owner has not confirmed mTLS vs shared-secret (D4). Default to B if not surfaced by D2 EOD.
- `hermes-agent v0.14.0` upstream license must be verified MIT or Apache-2.0 before pinning (per CLAUDE.md permitted-license-vendoring rule); if LGPL/AGPL, switch to subprocess isolation pattern instead of pip install in the container we own.
- SSE backpressure between gateway and runner — needs load-test in D5 buffer day; if runner can't keep up with concurrent tenants, switch chat passthrough to WebSocket in Slice 2.

End of codex lane plan.
