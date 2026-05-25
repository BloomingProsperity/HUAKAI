Lane: codex (general-purpose subagent)
UTC: 2026-05-25T11:55:00Z

# Hermes Phase-1 Slice 2 — codex-lane Plan

## 0. Plan Status

- Phase: planning only — no migration / handler / runner code written by this document.
- Branch: `claude/hermes-phase-1` (HEAD `3a153df` Slice 1.3 closed).
- Predecessor: `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md` (Owner-approved §F decisions, executed via 4 commits b7f58df → 3a153df).
- Owner-approved scope deltas for Slice 2 (from synthesis §F): **D4=B→C** (HMAC → JWT) and **D5=B** (conversations+messages tables this slice, memory/tool_calls/runtime_events deferred to Slice 3).
- This plan is independent of Claude lane; both must be surfaced together; execution starts only after Owner cross-discusses §F.

## 1. Scope

### 1.1 In-scope (this slice)

| Area | Item |
|---|---|
| Schema (`0058`) | `hermes_conversations`, `hermes_messages`, `hermes_jwt_keys` (active+previous public-key registry), audit-event `outcome` whitelist expansion for `hermes.message.send`. |
| Auth | runner→gateway JWT (asymmetric, gateway signs / runner verifies); gateway→runner JWT (same direction reused for SSE chat). HMAC code path kept as **fallback** behind feature flag until Slice 2.5 cleanup. |
| Hermes-agent | Replace `main.py` 501 skeleton with a thin FastAPI shim that imports `hermes-agent==0.14.0` (already pinned + sha256 in `requirements.txt` since Slice 1.2) and routes `/chat`, `/conversations`, `/conversations/{id}/messages` to `agent.conversation_loop` (entry point identified at `~/refs-latest/hermes-agent-main/agent/conversation_loop.py:1-130`). |
| Chat SSE | Real upstream streaming through hermes-agent's existing `httpx`-based provider; HUAKAI gateway streams SSE bytes back to client; conversation/message rows persisted on assistant-complete event. |
| API source modes | `managed_huakai_api` + `dedicated_group` only (per Slice 1 synthesis §F D-1; `external_api` still deferred). |
| Audit | New action `hermes.message.send` with sanitized arg snapshot (tokens redacted by `audit.SanitizeArgs`). |
| Endpoints | Wire `POST /v1/hermes/chat` to actually stream, `GET /v1/hermes/conversations/{id}/messages` to PG-backed history (replaces Slice 1 proxy-only). |
| Tests | ≥6 new discriminating tests (JWT verify, JWT TTL, conv→message FK, SSE flush ordering, tenant-isolation on messages, HMAC→JWT migration path). |
| Compose | New env `HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH`, `HUAKAI_HERMES_JWT_KID`, `HUAKAI_HERMES_JWT_AUDIENCE`, `HUAKAI_HERMES_JWT_ISSUER`. |

### 1.2 Out-of-scope (Slice 3+)

- `hermes_memory_items`, `hermes_tool_calls`, `hermes_runtime_events` tables.
- HUAKAI MCP server exposing `huakai.*` 8 ops tools.
- `external_api` mode encryption pathway.
- Per-tenant docker volume (single volume + `tenants/{tid}/users/{uid}` path stays).
- mTLS between gateway and runner (deferred to production cert-rotation slice).
- Production hash lock (`pip --require-hashes` on every transitive dep) — Slice 1 used direct-pin only, full lock still tracked in `docs/process/reviews/DEFERRED-hermes-runner-hash-lock.md`.
- Frontend Hermes panel.
- JWT JWKS discovery endpoint (`/.well-known/jwks.json`) — public-key file mount is enough this slice; JWKS deferred to Slice 5 admin-UI.

## 2. Success Criteria (machine-checkable)

1. `migrate -path backend/sql/migrations up` lands `0058`; immediate `down` returns DB to `0057` state; `up` again is idempotent. Local PG `huakai_roundtrip` script must report no diff. (See Owner memory `reference_local_pg_verification.md`.)
2. `go test ./backend/internal/hermes/... ./backend/internal/hermeshttp/...` passes (Slice 1 16 tests + ≥6 new) with mutation-self-check evidence inline per test, per CLAUDE.md §14.
3. `python -m pytest backend/deploy/hermes-runner/tests/` passes a smoke suite (JWT verify happy + 3 reject branches + chat 200 + chat 401-on-expired).
4. End-to-end smoke against dev `docker compose up gateway postgres hermes-runner`:
   - `curl -X POST $GW/v1/hermes/settings/enable -H "Authorization: Bearer $APIKEY"` → `200 {"state":"enabled"}`.
   - `curl -N -X POST $GW/v1/hermes/chat -H "Authorization: Bearer $APIKEY" -d '{"message":"hello"}'` → SSE stream with at least one `event: token` and one `event: done`; correlation_id appears in `hermes_audit_events`.
   - `curl $GW/v1/hermes/conversations` → array length ≥ 1; `curl $GW/v1/hermes/conversations/{id}/messages` → role-paired rows.
   - Replace JWT `kid` env to a rotated key file → previous token rejected with `401 jwt.signature_invalid`, new token works. (D3 rotation check.)
5. Codex per-commit review (CLAUDE.md §8) returns no unresolved S0/S1 across 4 planned commits (schema → JWT module → runner shim → tests).
6. Frozen-package invariant holds: `git diff --stat 3a153df..HEAD -- backend/internal/gatewayhttp backend/internal/gateway backend/internal/proto` is **empty** (no new files; bug-fix edits to existing files only). Verified by an explicit `make check-frozen-packages` (target added in this slice).

## 3. Time Estimate

Total: **5–7 days** end-to-end including review rounds. Sub-day milestones:

| Day | Milestone | Commit |
|---|---|---|
| 0.5 | License + spec gate: re-verify hermes-agent v0.14.0 LICENSE SHA, finalize D1-D6, Owner sign §6. | — |
| 1.0 | Schema gate: migration 0058 + sqlc, PG round-trip. | `hermes-schema-gate Slice 2 migration 0058 + jwt key registry` |
| 1.5 | JWT signer + verifier (Go) + jwt_keys CRUD + middleware swap shared-secret → JWT. | `hermes-jwt Slice 2 asymmetric signer + verifier` |
| 1.5 | Runner shim rewrite: hermes-agent import + `/chat` SSE + JWT verify (PyJWT 2.12.1 already in requirements.txt). | `hermes-runner Slice 2 agent import + SSE chat` |
| 1.0 | Conversations + messages persistence handlers (read-back path, write on stream-complete). | `hermes-history Slice 2 conversations messages handlers` |
| 1.0 | Discriminating tests (6 Go + ≥5 Python) + mutation checklist + compose env wiring. | `hermes-tests Slice 2 jwt sse history tests` |
| Buffer 1.0 | Review ≤2-round S0/S1 fix budget per commit. | — |

Plan-before-execute (CLAUDE.md §10) applies: cannot start day 1 until Owner approves §6 decision matrix.

## 4. Blast Radius

| Surface | Impact |
|---|---|
| Schema | +3 tables / +1 audit action / +1 outcome whitelist row. Both `up` and `down` reversible. No backfill of existing data — Slice 1 deployed 0 production tenants. |
| API contract | New PG-backed body on `GET /v1/hermes/conversations/{id}/messages` (was proxy 501). `POST /v1/hermes/chat` shape: still SSE, now with real event types. Considered a v1 introduction, not a break. |
| Secrets | New JWT private key on gateway (file mount, `0600`, env-pointed path). Loss path: rotation procedure must regenerate, public-key file ships to runner via compose mount. Audit_args sanitizer already strips `authorization` header in `audit.SanitizeArgs`. |
| Deploy | `docker-compose.dev.yml` adds `HUAKAI_HERMES_JWT_*` env vars and a mount for `./secrets/hermes-jwt-pub.pem`. Removed: shared-secret env not yet deleted — kept as fallback through Slice 2.5. |
| Audit | One new action `hermes.message.send`; audit outcome whitelist (added by 0055) extended only if missing. |
| Performance | hermes-agent runs in-process inside runner; expected idle RSS rise from ~80 MB (FastAPI skeleton) to ~250-350 MB (agent + httpx) per dev compose Slice 1.2 measurement (re-check). |
| Cross-tenant risk | JWT `sub` must include `tenant_id|user_id`; runner-side authorize requires JWT.sub matches request header `X-Hermes-Tenant`/`X-Hermes-User`. Mutation test below enforces this. |

## 5. Reference comparison table (CLAUDE.md §15)

| Concern | Reference A | Reference B | HUAKAI delta |
|---|---|---|---|
| JWT alg whitelist | LiteLLM `proxy/auth/handle_jwt.py:80-92` — pins to `RS256/384/512, PS256/384/512, ES256/384/512, EdDSA`, refuses HS-mixing | Portkey gateway `src/providers/google-vertex-ai/utils.ts:122` — uses `RS256` for service-account JWT | HUAKAI gateway issues exactly one alg per deploy (`EdDSA`/Ed25519 default, `ES256` fallback) — narrower whitelist than LiteLLM because we are not a federated IdP. Refusing HS* in runner verifier same. |
| Asymmetric vs symmetric | LiteLLM uses asymmetric only (`handle_jwt.py:818-845` JWKS path); LiteLLM doc warns "do not mix HS and RS" | invariant-gateway `gateway/common/authorization.py:11-58` uses static API-key header (no signing) | HUAKAI delta: asymmetric (gateway private key + runner public key) is strictly better than invariant-style static header — public-key compromise does not allow forgery. |
| Key rotation | LiteLLM caches JWKS via `user_api_key_cache.async_get_cache` with TTL (`handle_jwt.py:551-595`) | hermes-agent `mcp_serve.py` (top file) uses stdio MCP — no key rotation concept | HUAKAI delta: pre-rotation window — issue both `kid=v1` and `kid=v2` simultaneously, runner trusts both for overlap window (15-min TTL × 2 rotation overlap = 30-min mixed-trust). Generation upgrade vs LiteLLM cache-flush model. |
| SSE streaming | invariant-gateway `gateway/routes/anthropic.py:111-120` uses FastAPI `StreamingResponse` with per-chunk `sse_buffer` accumulation (`anthropic.py:285-342`) | LiteLLM proxy `_experimental/mcp_server/sse_transport.py` mounts a generic SSE transport for MCP | HUAKAI delta: stream is end-to-end (gateway proxies SSE bytes verbatim from runner) — no parse-modify-re-emit in middle, lower latency than invariant-gateway interception pattern. Persistence happens **after** stream close (on `event: done`). |
| Conversation persistence | hermes-agent `mcp_serve.py:60-75` reads `HERMES_HOME/sessions/*.db` SQLite (file-local) | LiteLLM proxy `_experimental/spend_management_endpoints.py` writes audit rows + spend rows to Postgres | HUAKAI delta: dual-source-of-truth — runner keeps its hermes_state.SessionDB SQLite (in-runner ergonomics, supports `hermes mcp serve` MCP clients), gateway ALSO records conversations+messages in PG (admin queries, billing attribution, tenant isolation). Fuse pattern, not single source. |
| API-key purpose tag | one-api `model/token.go:23-37` single token table with type-flag | new-api `model/token.go` extended with `tag` column (paraphrased per LGPL/AGPL) | HUAKAI delta: Slice 1 already shipped `purpose='hermes_runner'` column + partial index; Slice 2 issues JWT signed against the api_keys row matching `purpose='hermes_runner'` — JWT is short-lived **derivation** of the long-lived api_keys row, not a parallel credential. |
| hermes-agent config injection | hermes-agent `hermes_constants.py` reads `HERMES_HOME` + env vars | hermes-agent `agent/conversation_loop.py:120-127` uses a lazy `import run_agent` so callers can patch | HUAKAI delta: runner shim sets `HERMES_HOME=/var/lib/huakai/hermes/tenants/{tid}/users/{uid}` per-request (single mount, path-isolation per CLAUDE.md §10 D-8 Slice 1) and passes provider config via env+CLI flags. No edit to `agent/*` files. |

Reference projects cited: LiteLLM, Portkey gateway, invariant-gateway, hermes-agent, one-api, new-api. ≥2 references per decision in §6.

## 6. Owner Decision Points (D1-D6)

### D1 — JWT algorithm

| Option | Trade-off | Reference cite | Note |
|---|---|---|---|
| A: **EdDSA (Ed25519)** — recommended | smallest signature (64B), fastest verify, modern; deterministic; OpenSSL 1.1.1+ everywhere | LiteLLM `handle_jwt.py:80-92` lists `EdDSA`; PyJWT 2.12.1 already bundled by hermes-agent (`pyproject.toml:48`) supports it via `cryptography` extra | Recommend |
| B: ES256 | 256-bit ECDSA; widely used in OAuth2/OIDC; slightly larger signature; non-deterministic | LiteLLM `handle_jwt.py:89` ES256; Portkey `google-vertex-ai/utils.ts:122` uses RS256 (Google requirement, not our case) | OK alternative |
| C: RS256 | 2048-bit RSA; biggest signature (256B), slowest verify but most familiar | LiteLLM `handle_jwt.py:83` `RS256`; Portkey `utils.ts:122` `alg:'RS256'` | Default if Owner doubts Ed25519 readiness |

**Synthesis-of-codex-lane recommendation**: **A (EdDSA)**. Ed25519 keys are 32B each, easy to ship via compose mount; no padding edge cases; OpenSSL 1.1.1+ supports it everywhere we deploy.

### D2 — JWT private-key management

| Option | Trade-off | Reference cite |
|---|---|---|
| A: **File mount + restricted mode** — recommended | `./secrets/hermes-jwt-priv.pem` mounted read-only to gateway at `/run/secrets/hermes/jwt-priv.pem`, `0400` permissions, gateway reads at startup; rotation = atomic file swap + SIGHUP | LiteLLM `handle_jwt.py:598` reads `JWT_PUBLIC_KEY_URL` from env (file URL counts); hermes-agent `hermes_constants.py` reads `HERMES_HOME` env path |
| B: Env var with base64-encoded PEM | simpler compose, but key leaks into `ps`, env-dump, audit logs | LiteLLM `handle_jwt.py:598` env-based JWKS URL pattern |
| C: External secret manager (Vault, AWS Secrets Manager) | strongest, but adds runtime dep; Owner has no AWS access (per memory `project_no_aws_credentials.md`) | LiteLLM `handle_jwt.py:551-595` JWKS URL discovery pattern — close equivalent |

**Recommendation**: **A (file mount)**. C is correct long-term but blocked by Owner-no-AWS; B leaks via env. A is the move now with documented rotation runbook; secret-manager upgrade tracked as roadmap entry.

### D3 — JWT TTL + refresh strategy

| Option | Trade-off | Reference cite |
|---|---|---|
| A: **15-min TTL + on-demand refresh** — recommended | gateway issues JWT on each runner call when cached one is `<2 min` from expiry; in-process token cache keyed by tenant_id|user_id | hermes-agent `agent/conversation_loop.py` (lazy run_agent module pattern); LiteLLM `handle_jwt.py:62` `leeway=0` default + `Token Expired` raise |
| B: 5-min TTL + rotating | tighter blast radius, higher CPU (more signs/s) | LiteLLM JWKS cache TTL pattern |
| C: 24-h TTL + revocation list | low signing cost, needs persistent revocation table | LiteLLM cache pattern but inverted |

**Recommendation**: **A**. 15 min strikes balance: short enough that key compromise window is bounded; long enough that signing cost is amortized. Refresh path = gateway-side; runner never refreshes (runner is one-direction verifier).

### D4 — `hermes_conversations` + `hermes_messages` → 1 migration or 2?

| Option | Trade-off | Reference cite |
|---|---|---|
| A: **1 migration `0058_hermes_phase1_slice2_history`** — recommended | atomic; both tables ship together; FK from messages → conversations must exist before any message row; aligns with Slice 1 single-migration pattern in `0057` | one-api `model/token.go:23-37` ships related tables together; CLIProxyAPI `internal/api/handlers/management/oauth_callback.go` config-style add together |
| B: 2 migrations `0058` (conversations) + `0059` (messages) | smaller per-migration risk; rollback granularity | LiteLLM separates `audit_logs` from history tables — historical pattern but lacks the FK ordering constraint we have |

**Recommendation**: **A**. Two tables are FK-coupled and ship together logically. Two migrations would be smaller blast units but break the "one closed slice = one schema gate" rhythm from Slice 1. Down-migration drops messages first, then conversations.

### D5 — chat SSE flow control / backpressure / disconnect

| Option | Trade-off | Reference cite |
|---|---|---|
| A: **Best-effort SSE + persist on `event: done`** — recommended | client disconnect → runner cancellation propagated via httpx context; partial messages **not** persisted (`event: done` is the commit fence) | invariant-gateway `routes/anthropic.py:111-117` `StreamingResponse(...)` pattern; hermes-agent `agent/conversation_loop.py` already cancels on `KeyboardInterrupt` |
| B: SSE + WebSocket fallback when SSE not supported | broader client support, ~2× implementation complexity | invariant-gateway uses SSE only; LiteLLM proxy uses SSE only — industry default is SSE |
| C: SSE + persist every N tokens (incremental commit) | survives client disconnect mid-stream; writes a lot of partial rows | LiteLLM streaming has no equivalent — they only persist on completion |

**Recommendation**: **A**. WebSocket fallback (B) is Slice 4+ work. Partial-commit (C) creates "messy half-rows" admin can't easily clean up. `event: done` is the commit fence; if client disconnects mid-stream, runner cancellation drops the partial state cleanly — admin reconstructs from `hermes_audit_events` row `hermes.chat.start` correlation_id if needed.

### D6 — hermes-agent config injection

| Option | Trade-off | Reference cite |
|---|---|---|
| A: **Per-request env-overlay** — recommended | runner shim sets `HERMES_HOME=...{tid}/{uid}`, `OPENAI_API_KEY=<derived>`, `OPENAI_BASE_URL=<gateway internal URL>` per chat request via `os.environ` patched in `contextvars`; no edits to `agent/*` source | hermes-agent `hermes_constants.py` reads `HERMES_HOME` env; hermes-agent `agent/conversation_loop.py:122-127` documents lazy `import run_agent` to allow patching |
| B: Config file written per-tenant at runner startup | simpler, but requires tenant onboarding step that doesn't fit "always-on runner" | invariant-gateway `gateway/common/config_manager.py` config-file pattern — but they're single-tenant |
| C: Runtime API (extend hermes-agent to accept config via HTTP body) | upstream PR + maintenance burden; violates "no edit to hermes-agent source" rule | none — would be HUAKAI invention |

**Recommendation**: **A**. Env-overlay keeps the clean-room separation: runner shim is HUAKAI code, hermes-agent stays untouched. Per-request `contextvars` swap is the documented way to multiplex; tests verify isolation.

## 7. Step-by-Step Implementation (post-§6 approval)

### Step 7.1 — License + dependency re-verification (gate)

1. `curl https://api.github.com/repos/NousResearch/hermes-agent | jq '.license,.archived,.pushed_at'` — record SHA + license, must remain MIT, archived=false, pushed_at within 90 days (CLAUDE.md §12 first-cite recency).
2. Confirm `hermes-agent==0.14.0` still resolves; check `~/refs-latest/hermes-agent-main/pyproject.toml:9` (`license = { text = "MIT" }` verified at lane time).
3. Confirm `PyJWT[crypto]==2.12.1` already in hermes-agent's own deps (`pyproject.toml:48`) — no new top-level dep for runner; runner inherits PyJWT through hermes-agent.
4. Confirm no new Go deps; reuse stdlib `crypto/ed25519`, `crypto/rand`, and existing `github.com/golang-jwt/jwt/v5` already present (verify via `go.mod` grep before claiming).

### Step 7.2 — Schema gate (commit 1)

Files (target package = `backend/sql/migrations/` — not a Go package, ignored by frozen-package rule):

- `backend/sql/migrations/0058_hermes_phase1_slice2_history.up.sql`
- `backend/sql/migrations/0058_hermes_phase1_slice2_history.down.sql`
- `backend/sql/queries/hermes_conversations.sql` (sqlc input)
- `backend/sql/queries/hermes_messages.sql`
- `backend/sql/queries/hermes_jwt_keys.sql`
- Regenerated `backend/internal/db/*.sql.go` (target package `db` — existing, not frozen).

Schema sketch (illustrative, final wording per Codex-impl lane after Owner approves §6):

```
CREATE TABLE hermes_conversations (
  conversation_id  UUID PRIMARY KEY,
  tenant_id        BIGINT NOT NULL,
  user_id          BIGINT NOT NULL,
  title            TEXT,
  api_profile_id   UUID REFERENCES hermes_api_profiles(profile_id),
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT hermes_conv_tenant_user_fk
    FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, user_id)  -- 0041 composite pattern
);

CREATE TABLE hermes_messages (
  message_id       UUID PRIMARY KEY,
  conversation_id  UUID NOT NULL REFERENCES hermes_conversations(conversation_id) ON DELETE CASCADE,
  tenant_id        BIGINT NOT NULL,
  user_id          BIGINT NOT NULL,
  role             TEXT NOT NULL CHECK (role IN ('user','assistant','tool','system')),
  content          TEXT NOT NULL,
  token_in         INT,
  token_out        INT,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT hermes_msg_tenant_user_fk
    FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, user_id)
);
CREATE INDEX idx_hermes_messages_conv ON hermes_messages(conversation_id, created_at);

CREATE TABLE hermes_jwt_keys (
  kid              TEXT PRIMARY KEY,
  alg              TEXT NOT NULL CHECK (alg IN ('EdDSA','ES256','RS256')),
  public_key_pem   TEXT NOT NULL,
  status           TEXT NOT NULL CHECK (status IN ('active','retiring','revoked')),
  not_before       TIMESTAMPTZ NOT NULL DEFAULT now(),
  not_after        TIMESTAMPTZ,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Local PG round-trip: `cd backend && migrate -path sql/migrations -database $LOCAL_PG up; migrate down 1; migrate up 1; psql -c '\d hermes_conversations'` — verify shape.

### Step 7.3 — JWT signer + verifier + middleware (commit 2)

Files:

- `backend/internal/hermes/jwt_signer.go` (Go — new file in **existing non-frozen** `hermes` package).
- `backend/internal/hermes/jwt_signer_test.go`
- `backend/internal/hermes/jwt_keys_store.go` (PG-backed registry of public keys).
- `backend/internal/hermes/jwt_keys_store_test.go`
- `backend/internal/hermes/runner_client_jwt.go` (extension of existing `runner_client.go` — replaces HMAC body-signing with `Authorization: Bearer <jwt>` header; HMAC kept as fallback when `HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH` unset).

Package check: `hermes` is the new package added in Slice 1.1 (not in frozen list `gatewayhttp/gateway/proto`). Verify via `git ls-files backend/internal/hermes | wc -l` (Slice 1 added ≤10 files; Slice 2 adds ~6 more → still well under 20-file/5000-LoC budget per CLAUDE.md §13).

Signer logic:
- Read private key PEM from `HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH` at startup; refuse to start if missing AND fallback HMAC also unset.
- Claims: `iss="huakai-gateway"`, `aud="hermes-runner"`, `sub=fmt.Sprintf("%d|%d", tenantID, userID)`, `iat`, `nbf`, `exp=iat+15min`, `kid` header, `jti` random 128-bit.
- Cache last issued token per `(tenant,user)` and reuse until `exp - 2min`.

Runner-side (Python) verify logic in `backend/deploy/hermes-runner/auth.py`:
- Load **all active+retiring** public keys at startup from a file mount `/run/secrets/hermes/jwt-pubs/<kid>.pem`.
- On request: extract `Authorization: Bearer <jwt>`, parse header to get `kid`, look up key, call `jwt.decode(token, key, algorithms=[<configured-alg>], audience='hermes-runner', issuer='huakai-gateway', options={"require":["exp","iat","nbf","sub","aud","iss"]})`.
- Strict alg whitelist enforced (mirrors LiteLLM `handle_jwt.py:80-92` discipline — refuse HS mixing).
- `sub` parsed back into `tenant_id|user_id`; must match `X-Hermes-Tenant`/`X-Hermes-User` headers else 401.

### Step 7.4 — Hermes-agent import + runner SSE chat (commit 3)

Files:

- `backend/deploy/hermes-runner/main.py` (rewrite — replace 501 skeleton).
- `backend/deploy/hermes-runner/auth.py` (JWT verify per 7.3).
- `backend/deploy/hermes-runner/chat_handler.py` (calls into `hermes-agent`'s `agent.conversation_loop.run_turn` or equivalent — final symbol picked at impl time after re-reading `~/refs-latest/hermes-agent-main/agent/conversation_loop.py` in full).
- `backend/deploy/hermes-runner/conversation_store.py` (read-back from runner's local hermes-state SessionDB; HUAKAI gateway holds the source-of-truth in PG, runner SQLite is per-process cache).
- `backend/deploy/hermes-runner/tests/test_auth.py`
- `backend/deploy/hermes-runner/tests/test_chat.py`
- Updated `backend/deploy/hermes-runner/entrypoint.sh` (load all `.pem` files from `/run/secrets/hermes/jwt-pubs/` into a dict at boot).
- Updated `backend/deploy/hermes-runner/requirements.txt` — **no** new pin needed (PyJWT inherited via hermes-agent).

Hermes-agent import path (verified at `~/refs-latest/hermes-agent-main/`):
- Top-level packages: `agent/`, plus root-level modules (`mcp_serve.py`, `run_agent.py`, `hermes_state.py`, `hermes_constants.py`, `cli.py`).
- Entry pattern: import `agent.conversation_loop`, set `HERMES_HOME` env to tenant-scoped path BEFORE first call into `agent.*` to ensure `hermes_state.SessionDB` picks the right location.
- `contextvars` to isolate concurrent tenant requests inside one runner process (single python process owns multiple concurrent tenants; isolation is path-keyed, not process-keyed — Slice 2 acceptance: documented limitation; per-tenant process is Slice 4+).

SSE handler shape:
- `POST /chat` body: `{"tenant_id":..., "user_id":..., "conversation_id":..., "message":"...", "api_source":"managed_huakai_api|dedicated_group", "api_profile_id":"..."}`.
- Returns FastAPI `StreamingResponse` with `media_type="text/event-stream"`.
- Stream events: `event: meta` (correlation_id) → `event: token` (chunk) ×N → `event: done` (full text, token counts) OR `event: error`.
- Persistence: runner does NOT write to PG; runner emits a final `meta` event with the full assistant message + token counts; gateway intercepts and writes `hermes_messages` row before forwarding `event: done` to client.

### Step 7.5 — Gateway-side history handlers (commit 4)

Files:

- `backend/internal/hermes/conversations_store.go` (new — PG read of conversations).
- `backend/internal/hermes/messages_store.go` (new — PG read/write).
- `backend/internal/hermeshttp/chat_handler.go` (existing — extend to persist messages on stream-complete).
- `backend/internal/hermeshttp/conversations_handler.go` (existing — replace 501 stub for `/messages` subpath with PG-backed query).

Package check: `hermes` and `hermeshttp` both non-frozen (added in Slice 1.1).

Persistence ordering inside `chat_handler.go`:
1. On user-side message receive: `BEGIN; INSERT hermes_conversations (if new); INSERT hermes_messages (role=user); COMMIT` then forward to runner.
2. On runner stream complete (`event: done` with full assistant message): `BEGIN; INSERT hermes_messages (role=assistant, content=..., token_*); UPDATE hermes_conversations SET updated_at=now(); INSERT hermes_audit_events(action='hermes.message.send', outcome='success', correlation_id=...); COMMIT`.
3. On runner error: audit `outcome='failure'`, do not persist assistant message, return SSE `event: error` to client.

Transactional invariant: assistant message + audit row land in **same transaction** (Slice 1 already enforces same pattern for `chat.start`).

### Step 7.6 — Discriminating tests (commit 5)

Go (new):

| Test | Mutation it catches |
|---|---|
| `TestJWTSign_VerifyHappyPath` | If gateway signs with wrong `aud`, runner-side verify rejects → test goes red on the gateway swapping `aud="hermes-runner"` to `aud="any"`. |
| `TestJWTVerify_RejectsExpired` | Set `exp=iat-1` and assert `401 jwt.expired`. Mutation: stripping `exp` check in runner-side `jwt.decode(... options={"require":["exp"...]})` → red. |
| `TestJWTVerify_RejectsBadKid` | Issue token with `kid=v1`, runner pubkey dict only has `kid=v2` → 401. Mutation: removing the `kid`-lookup step → 500 (key not found) ≠ 401 (signal mutates differently). |
| `TestJWTSubBindsTenantUser` | Issue JWT for `sub="42|7"`, send chat with header `X-Hermes-Tenant=42 X-Hermes-User=99` → 403 cross-tenant. Mutation: removing `sub` ↔ header reconcile → 200 (cross-tenant leak). |
| `TestMessagePersistOnDoneOnly` | Stream a chat, simulate client disconnect at `event: token` #3 → `hermes_messages` should have user row but no assistant row. Mutation: persist-on-every-token → assistant row exists with partial content. |
| `TestConversationsTenantIsolation` | Tenant A's `GET /conversations` must not see Tenant B's rows. Mutation: drop `tenant_id` WHERE → A sees B's conversation. |

Python (new):

| Test | Mutation it catches |
|---|---|
| `test_auth_rejects_hs256_token` | Forge an HS256 token with shared key, runner must reject. Mutation: leave `algorithms=["HS256","EdDSA"]` in `jwt.decode` (bug pattern from LiteLLM warning) → forged token passes. |
| `test_auth_rejects_unsigned_token` | `alg:"none"` token → 401. Mutation: relaxing `algorithms=[...]` to `algorithms=None` → 200. |
| `test_chat_streams_then_emits_done` | Mock hermes-agent to emit 3 tokens then complete → response has 3 `event: token` then 1 `event: done`. Mutation: drop the `done` finalize → test hangs/times out (use 5s timeout, assert receive within). |
| `test_env_overlay_isolates_tenants` | Two concurrent chats from different tenants, assert each saw its own `HERMES_HOME` path via a patched `os.environ.get` spy. Mutation: drop `contextvars` swap → second request sees first's HERMES_HOME. |
| `test_missing_audience_rejected` | JWT with no `aud` claim → 401. Mutation: removing `audience=` arg to `jwt.decode` → 200. |

Per CLAUDE.md §14, each test must include a 1-sentence comment naming the exact regression it catches AND a `// mutation:` comment showing the broken code path.

## 8. New File Package Assignment (frozen-package check)

| File | Target package | Frozen? | Note |
|---|---|---|---|
| `backend/sql/migrations/0058_*.up.sql` | (SQL, not a Go package) | n/a | |
| `backend/sql/queries/hermes_conversations.sql` | (sqlc input) | n/a | |
| `backend/sql/queries/hermes_messages.sql` | (sqlc input) | n/a | |
| `backend/sql/queries/hermes_jwt_keys.sql` | (sqlc input) | n/a | |
| `backend/internal/hermes/jwt_signer.go` | `hermes` | **no** | added in Slice 1 |
| `backend/internal/hermes/jwt_signer_test.go` | `hermes` | no | |
| `backend/internal/hermes/jwt_keys_store.go` | `hermes` | no | |
| `backend/internal/hermes/jwt_keys_store_test.go` | `hermes` | no | |
| `backend/internal/hermes/runner_client_jwt.go` | `hermes` | no | |
| `backend/internal/hermes/conversations_store.go` | `hermes` | no | |
| `backend/internal/hermes/messages_store.go` | `hermes` | no | |
| `backend/internal/hermes/*_test.go` | `hermes` | no | |
| `backend/deploy/hermes-runner/auth.py` | (Python, runner) | n/a | |
| `backend/deploy/hermes-runner/chat_handler.py` | (Python) | n/a | |
| `backend/deploy/hermes-runner/conversation_store.py` | (Python) | n/a | |
| `backend/deploy/hermes-runner/tests/*.py` | (Python) | n/a | |

**Zero new files in `gatewayhttp/gateway/proto`** — confirmed by listing above; wiring into `cmd/gateway/main.go` is a single-line append to the router registration (existing edit pattern). Slice 1 already attached the hermes router to main.go (see `e0a73bf` commit `hermes-service-handler Slice 1.1 ...`) — Slice 2 only changes handlers wired through the same registration.

`hermes` package post-Slice 2 file count estimate: ~16 files (~6 new + ~10 from Slice 1), well under 20-file budget. `hermeshttp` ~7 files (no new, only edits). Both stay within CLAUDE.md §13 limits.

## 9. Risks + Mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| Private key file leaked via compose-up dump | S0 | `0400` mode, gitignored secret dir, env points to mount path only — never to key body |
| JWT alg confusion (HS* vs EdDSA token-forge) | S0 | Runner verifier enforces strict allowlist of exactly one alg (the deploy-configured one); test `test_auth_rejects_hs256_token` covers; mirrors LiteLLM `handle_jwt.py:80-92` discipline |
| hermes-agent in-process state leak between tenants | S1 | `contextvars`-scoped env overlay + per-tenant `HERMES_HOME`; `test_env_overlay_isolates_tenants` validates; documented limitation = "single Python process, path isolation" (Slice 4 may move to subprocess-per-tenant) |
| Partial-message half-row on disconnect | S2 | Persistence fence on `event: done` only; mutation test enforces |
| `event: done` lost in network (legitimate timeout) | S2 | Gateway proxy has 60s read-timeout; on read-timeout audit `outcome='timeout'`, do not persist assistant; client retries with new `conversation_id` continuing |
| Rotation window misalignment (gateway issues new kid before runner picks it up) | S1 | Pre-rotation = put new pubkey in runner mount first, wait 30s, then gateway starts issuing new kid; runbook documented |
| hermes-agent 0.14.0 import fails (e.g., missing optional skill) | S1 | Runner-side import-test in healthcheck endpoint; CI must exercise `python -c "import agent.conversation_loop"` |
| SSE buffer growth (long assistant message) | S2 | Token-by-token forward (no in-gateway accumulation); gateway uses Go `http.ResponseWriter.Write` + `Flush()` per chunk; LiteLLM/invariant-gateway both use this pattern |
| HMAC fallback path becomes permanent dead code | S3 | Slice 2.5 cleanup ticket — remove HMAC code when JWT proven stable in prod ≥7 days; tracked in `docs/process/reviews/DEFERRED-hermes-hmac-removal.md` |
| New file count pushes `hermes` package over 20-file budget | S3 | Headroom check at end of slice; if over, Slice 3 begins with a split into `hermes/auth`, `hermes/store`, `hermes/runtime` sub-packages |

## 10. License + clean-room guards (CLAUDE.md §11, §12)

- hermes-agent v0.14.0: MIT (verified `~/refs-latest/hermes-agent-main/pyproject.toml:9`); permits direct `pip install` + `import`. Per CLAUDE.md "permitted-license vendoring policy" — hermes-agent runs **as a dependency**, not vendored; no NOTICE entry required for pip-installed deps (only required when vendored into source tree).
- PyJWT 2.12.1: MIT (Owner-known); via hermes-agent dep chain.
- No new Go module needed: `crypto/ed25519` + `crypto/rand` are stdlib; `github.com/golang-jwt/jwt/v5` (if used) must be verified in `go.mod`; if absent, prefer `crypto/ed25519` + hand-rolled JWT (60 LoC, no third-party trust) — recommendation: hand-roll given small surface.
- Code lane discipline: Codex impl lane is **specifier** (reads hermes-agent source to learn its `conversation_loop` entry-point shape); runner shim and Go code are **HUAKAI-original** — no copying of function names, struct fields, comments, or code blocks. Per-file Slice 2 review must verify no verbatim reuse.
- Source files Codex impl lane will read (per CLAUDE.md §11 closing requirement): `~/refs-latest/hermes-agent-main/agent/conversation_loop.py`, `~/refs-latest/hermes-agent-main/hermes_constants.py`, `~/refs-latest/hermes-agent-main/hermes_state.py`, `~/refs-latest/hermes-agent-main/mcp_serve.py`, `~/refs/litellm/litellm/proxy/auth/handle_jwt.py`, `~/refs/invariant-gateway/gateway/routes/anthropic.py` (SSE pattern only — not Go code).

## 11. Definition-of-Done Checklist

- [ ] Owner approves §6 D1-D6.
- [ ] License gate evidence captured (recency check ≤30d).
- [ ] Migration 0058 round-trip verified locally (PG `huakai_roundtrip`).
- [ ] JWT signer + verifier unit tests green (≥6 new Go).
- [ ] Python runner tests green (≥5 new).
- [ ] All 16 Slice-1 Go tests still green.
- [ ] `make check-frozen-packages` passes (zero diff in `gatewayhttp/gateway/proto`).
- [ ] `go test ./...` full repo green (Owner memory `feedback_full_suite_verification`).
- [ ] Docker compose dev e2e: `enable → chat SSE → conversations GET → messages GET` smoke passes.
- [ ] Codex per-commit review run with `--sandbox read-only` on each of 5 commits; no unresolved S0/S1.
- [ ] Reference comparison §5 referenced in commit messages where applicable.
- [ ] Slice 2 retro entry written: "what reference projects also have feature X, what HUAKAI delta" (per Owner memory `feedback_per_slice_ref_recompare`).

## 12. Source files read (codex-lane plan provenance)

- `docs/plans/2026-05-24-hermes-native-integration.md`
- `docs/process/plans/2026-05-25-hermes-phase-1-slice1-synthesis.md`
- `CLAUDE.md`, `AGENTS.md` (rules #8, #10, #11, #12, #13, #14, #15)
- `backend/sql/migrations/0057_hermes_phase1_slice1_core.up.sql` (head migration confirmation)
- `backend/internal/hermes/runner_client.go` (existing HMAC client to extend)
- `backend/deploy/hermes-runner/main.py` (existing 501 skeleton to replace)
- `backend/deploy/hermes-runner/requirements.txt` (hermes-agent 0.14.0 pin)
- `~/refs-latest/hermes-agent-main/pyproject.toml` (MIT license + PyJWT dep)
- `~/refs-latest/hermes-agent-main/agent/conversation_loop.py` (lines 1-130, module shape)
- `~/refs-latest/hermes-agent-main/mcp_serve.py` (lines 1-80, MCP toolset shape — confirms Slice 3 out-of-scope)
- `~/refs/litellm/litellm/proxy/auth/handle_jwt.py` (lines 60-140, 80-92 alg whitelist, 800-860 verify path)
- `~/refs/invariant-gateway/gateway/common/authorization.py` (header-auth baseline)
- `~/refs/invariant-gateway/gateway/routes/anthropic.py` (lines 8, 111-117, 285-342 SSE pattern)
- `~/refs/portkey-gateway/src/providers/google-vertex-ai/utils.ts` (line 122 RS256 service-account pattern)

Lane: codex (general-purpose subagent), single pass, independent of Claude lane.
UTC: 2026-05-25T11:55:00Z

## Owner 中文摘要

Slice 2 主目标三件:**(1) 真接 hermes-agent v0.14.0** — 已验证 MIT 许可证、PyJWT 已被 hermes-agent 自带 (`pyproject.toml:48`),不引新顶层依赖;**(2) 把 Slice 1 的 shared-secret + HMAC 升级到非对称 JWT** — 推荐 EdDSA(Ed25519)+ 文件挂载私钥 + 15 分钟 TTL + pre-rotation 双 kid 重叠窗口;**(3) 加 `hermes_conversations` 和 `hermes_messages` 两张表(单迁移 0058)+ 加 `hermes_jwt_keys` 公钥注册表**(便于热轮换)。6 个 Owner 决策(D1 算法 / D2 私钥存储 / D3 TTL / D4 单迁移 / D5 SSE 背压策略 / D6 hermes-agent 配置注入),每个都给了至少 2 个借鉴项目 cite。时间 5-7 天,5 次 commit(schema → JWT → runner-shim → history-handlers → tests),冻结包 gatewayhttp/gateway/proto 零新文件。
