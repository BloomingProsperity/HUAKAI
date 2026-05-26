Lane: codex independent plan
UTC: 2026-05-26T00:00:00Z
Scope artifact: Hermes Phase 1 Slice 2.2 planning only
Predecessor commit: `4b02edf988a52b80afe198280329cd3514ebe352`
Observed regions: 32+
Inferences: 9
Open questions: 5

# Hermes Phase 1 Slice 2.2 - Codex Independent Plan

## 0. Status And Truth Note

- This is a planning artifact only. It does not implement, stage, commit, or mutate runtime code.
- Owner-approved invariants are treated as fixed inputs: D1 EdDSA, D5 SSE plus 20s heartbeat plus persist-on-`event: done`, and D6 per-request `HERMES_HOME` ContextVar overlay.
- Slice 2.0 schema and Slice 2.1 JWT/bootstrap are treated as completed at `4b02edf`.
- Independence caveat: during a broad citation search, a few lines from the Claude parallel plan path were accidentally exposed by `rg` output. I did not open or read that plan as an input. This document is derived from the source regions listed in §13. Owner should decide whether this caveat is acceptable or whether to request a fresh isolated re-run.
- No non-MIT reference project source was read for this plan. GPL/AGPL/LGPL references are intentionally excluded from this slice planning lane.

## 1. Slice Goal, Scope, And Boundaries

### 1.1 Goal

Slice 2.2 turns the current Hermes skeleton into an end-to-end gateway-owned chat SSE path:

1. The browser/client calls only HUAKAI gateway `/v1/hermes/chat`.
2. Gateway authenticates the caller, verifies Hermes is enabled, calls `hermes-runner` over the existing internal runner client, and streams SSE to the client.
3. `hermes-runner` imports `hermes-agent==0.14.0` and runs one chat turn with request-scoped `HERMES_HOME`.
4. Runner emits SSE through Python, including heartbeat comments every 20s and a terminal `event: done`.
5. Gateway treats `event: done` as the commit fence: persist user/assistant messages and `hermes.message.send` audit in one transaction, then forward the final done event.
6. Runner never talks to PostgreSQL and never stores upstream LLM keys. Gateway owns persistence, audit, tenant isolation, and credential resolution.

Current source state:

- Python runner still returns `501` for `/chat`, `/conversations`, and `/conversations/{id}/messages`: `backend/deploy/hermes-runner/main.py:143`.
- Runner JWT verification already pins EdDSA, issuer/audience, required claims, `kid`, and 15-minute max TTL: `backend/deploy/hermes-runner/jwt_verify.py:20`.
- Gateway runner client can sign EdDSA JWTs and attach tenant/user headers: `backend/internal/hermes/runner_client.go:204`.
- Gateway only mounts `/v1/hermes` when both service and runner are configured, matching fail-closed route behavior: `backend/cmd/gateway/routes.go:96`.
- Gateway currently proxy-copies runner chat bytes and audits `hermes.chat.start` before it has a completion fence: `backend/internal/hermeshttp/chat_handler.go:35`.
- Conversation/message SQLC queries already exist from Slice 2.0: `backend/sql/queries/hermes.sql:139`, `backend/sql/queries/hermes.sql:178`, `backend/sql/queries/hermes.sql:190`.

### 1.2 In Scope

- Python runner:
  - Replace `/chat` 501 with a thin FastAPI/SSE shim around `hermes-agent==0.14.0`.
  - Use per-request `hermes_constants.set_hermes_home_override(...)` and always reset the token.
  - Emit structured SSE events: `event: conversation`, `event: token`, `event: status`, `event: error`, `event: done`, plus heartbeat comments.
  - Keep runner DB-free and key-free.
  - Fix runner startup gating so JWT mode does not still require `HUAKAI_HERMES_SHARED_SECRET`.

- Go gateway:
  - Add a non-frozen bridge package, recommended path `backend/internal/hermeschat`, for parsing runner SSE and enforcing the done fence.
  - Extend `backend/internal/hermes` service methods to create/list conversations, list messages, and append completed user/assistant messages atomically with audit.
  - Modify existing `backend/internal/hermeshttp` handlers to call the new service/bridge instead of proxying conversation reads to runner.
  - Add `hermes.message.send` to audit action validation and enforce `SanitizeArgs` for sensitive request fields.
  - Keep `/v1/hermes/*` unmounted when runner/service config is missing; other gateway routes remain normal.

- Tests:
  - Add discriminating Go tests for atomic persistence/audit, done-fence ordering, tenant ownership, runner-not-called list paths, and sanitizer coverage.
  - Add discriminating Python unittest coverage for JWT auth, ContextVar isolation, SSE heartbeat, done event, and no raw upstream key leakage.

### 1.3 Out Of Scope

- No browser-to-runner route.
- No runner-to-DB connection.
- No raw upstream LLM key persistence in runner or runner logs.
- No schema migration in Slice 2.2 unless Owner explicitly reopens Slice 2.0. Existing `0058` tables/queries are the intended storage surface.
- No edits to frozen packages: `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`.
- No new files in frozen packages.
- No changes to billing ledger, quota enforcement, auth core, or production deployment scripts.
- No MCP tool execution, memory UI, runtime event tables, admin UI, or websocket fallback in this slice.
- No reading or copying non-MIT reference source.

## 2. Hard Points To Front-Load

1. SSE is not just byte proxying. Gateway must parse enough runner SSE to identify the terminal `event: done`, aggregate final assistant content, and commit persistence/audit before forwarding done.
2. Streaming has already flushed headers by the time persistence happens. The safe pattern is to stream token events, withhold terminal done, commit in gateway, then forward done. If commit fails, emit an error event and never emit done.
3. `hermes-agent` is synchronous around `run_conversation(...)` but provides stream callbacks. Runner needs a queue bridge between the callback thread and async SSE response.
4. `HERMES_HOME` isolation must use ContextVar overlay, not `os.environ` mutation. The upstream code explicitly provides this to avoid cross-thread leakage: `NousResearch/hermes-agent@v0.14.0:hermes_constants.py:20`.
5. The current Python entrypoint still requires `HUAKAI_HERMES_SHARED_SECRET`, which conflicts with Slice 2.1 JWT mode: `backend/deploy/hermes-runner/entrypoint.sh:8`.
6. `requirements.txt` currently lacks `sse-starlette`: `backend/deploy/hermes-runner/requirements.txt:3`. Adding it is a new runtime dependency and needs dependency/license gate handling.
7. Runner credential injection is the highest security ambiguity. Hermes-agent accepts `base_url` and `api_key` constructor arguments: `NousResearch/hermes-agent@v0.14.0:run_agent.py:350`. HUAKAI must ensure any value passed as `api_key` is an internal, short-lived gateway credential or request token, not the upstream vendor key.
8. The existing `Store` interface lacks conversation/message methods even though SQLC queries exist: `backend/internal/hermes/types.go:102`.
9. Existing conversation handlers still proxy reads to runner: `backend/internal/hermeshttp/conversations_handler.go:18`. Slice 2.2 must move reads to gateway PostgreSQL.
10. Existing proto SSE adapters are useful context but frozen. Anthropic terminal handling exists at `backend/internal/proto/anthropic/sse.go:207`, and OpenAI `[DONE]` handling exists at `backend/internal/proto/openai/sse.go:213`; Slice 2.2 must not edit either file.

## 3. Decision Register

Every decision below includes Owner options. D1, D5, and D6 are already Owner-approved, but are kept here to record the reasoning and the execution interpretation.

### D1 - Runner auth algorithm and route fail-closed behavior

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | EdDSA JWT only for Slice 2.2 chat path; HMAC remains compatibility code but dev/prod config should prefer JWT. Missing runner config means `/v1/hermes/*` is not mounted. | `NousResearch/hermes-agent@v0.14.0:pyproject.toml:54` shows PyJWT crypto is already in the hermes-agent dependency set; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/providers/google-vertex-ai/utils.ts:121` shows gateway-side JWT signing as a common integration pattern, though with RS256. | Approved by Owner. Implement as locked input. |
| B | ES256 fallback if EdDSA deployment support fails. | Same two references as A; this option would use the same asymmetric pattern but different alg. | Keep only as emergency fallback; do not implement unless Owner reopens D1. |
| C | Revert chat path to HMAC only. | `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:228` uses request-scoped cancellation, not static shared-secret coupling; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/providers/google-vertex-ai/utils.ts:121` demonstrates asymmetric JWT. | Reject. It would undo Slice 2.1 and shrink security posture. |

Implementation interpretation:

- `backend/internal/hermes/runner_client.go:217` stays the gateway->runner signing path.
- `backend/deploy/hermes-runner/main.py:124` stays the runner verifier path.
- Entry point gating must accept JWT mode without shared secret.

### D2 - How runner invokes hermes-agent

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | In-process import: instantiate `AIAgent`, pass base URL/model/provider/session values, and call `run_conversation(...)` with a stream callback. | `NousResearch/hermes-agent@v0.14.0:run_agent.py:350` accepts `base_url`, `api_key`, provider, callbacks, and session IDs; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:228` keeps streaming execution tied to a request context. | Recommended. Lowest latency and uses the MIT package as intended. |
| B | Per-request subprocess invoking the Hermes CLI. | `NousResearch/hermes-agent@v0.14.0:mcp_serve.py:62` shows file-backed session roots keyed by `HERMES_HOME`; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:228` keeps stream work request-scoped in-process instead of shelling out. | Defer. Better isolation but worse latency and cancellation. |
| C | Bypass hermes-agent and call upstream LLM directly. | `NousResearch/hermes-agent@v0.14.0:agent/conversation_loop.py:375` hydrates conversation state and Hermes-specific turn behavior; `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/anthropic.py:110` demonstrates normal gateway streaming without replacing the domain engine behind it. | Reject. It removes the actual Hermes-agent feature from Slice 2.2. |

### D3 - Runner SSE response shape and heartbeat

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | Runner emits `text/event-stream` with structured events and heartbeat comments every 20s. Gateway parses event names and data. | `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/anthropic.py:110` returns a streaming response for Anthropic streams; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/openai/openai_handlers.go:474` sets SSE headers and flushes stream chunks. | Approved by Owner through D5. |
| B | Runner returns raw upstream provider SSE and gateway byte-proxies. | `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:280` parses streaming response lines for usage; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:139` reads and transforms stream chunks. | Reject for HUAKAI. Byte proxying cannot enforce persist-before-done. |
| C | Implement stream semantics by editing existing proto SSE files. | `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:392` shows stream normalization can live outside core protocol packages; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:280` performs stream-aware processing outside the HTTP handler. | Reject. Violates frozen-package rule. HUAKAI-specific read-only context: `backend/internal/proto/anthropic/sse.go:180`, `backend/internal/proto/openai/sse.go:208`. |

### D4 - Gateway persistence fence

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | Persist only after runner `event: done`; commit messages and `hermes.message.send` audit in one gateway transaction; only then forward done. | `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:280` demonstrates stream-aware post-processing; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:70` keeps state while draining a stream. | Approved by Owner through D5. |
| B | Persist user message before streaming, assistant on done. | `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/open_ai.py:157` streams before final response completion; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:285` parses chunks during stream. | Not recommended. Leaves orphan user rows on runner failure unless extra cleanup is added. |
| C | Persist every token. | `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:80` transforms chunks incrementally; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:232` obtains a stream channel. | Reject for Slice 2.2. It creates high write volume and partial-message semantics not required by Owner. |

HUAKAI-specific enforcement:

- Use the existing serializable transaction helper: `backend/internal/hermes/types.go:153`.
- Follow Slice 1 atomic audit pattern: `backend/internal/hermes/settings.go:21`.
- `SanitizeArgs` already redacts `api_key`, `token`, `password`, and `secret`: `backend/internal/hermes/audit.go:64`.

### D5 - Per-request `HERMES_HOME`

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | Use `set_hermes_home_override` and reset in `finally`; path derived from signed tenant/user headers. | `NousResearch/hermes-agent@v0.14.0:hermes_constants.py:20` provides a ContextVar setter; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:228` demonstrates per-request stream work scoped by cancellable context. | Approved by Owner through D6. |
| B | Mutate `os.environ["HERMES_HOME"]` per request. | `NousResearch/hermes-agent@v0.14.0:hermes_constants.py:23` explicitly avoids environment mutation for per-task scoping; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:228` keeps streaming state bound to request context. | Reject. It can leak tenant home across concurrent requests. |
| C | Spawn one runner process per tenant/user home. | `NousResearch/hermes-agent@v0.14.0:mcp_serve.py:62` uses `HERMES_HOME` to locate session files; `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/anthropic.py:111` shows a single service process can stream per request. | Defer. Isolation is stronger but operations overhead is high. |

### D6 - Upstream LLM credential bridge

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | Gateway resolves the existing credential path and passes runner only an internal gateway base URL plus a short-lived internal credential/token. Runner does not receive raw vendor keys. | `NousResearch/hermes-agent@v0.14.0:run_agent.py:350` supports `base_url` and `api_key`; `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:232` routes streaming through an auth manager instead of exposing all backend credential details to the stream handler. | Recommended, but needs implementation design review before code. |
| B | Pass the raw upstream provider key from gateway to runner per request. | `NousResearch/hermes-agent@v0.14.0:run_agent.py:352` proves the runner could accept an API key; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/providers/google-vertex-ai/utils.ts:112` keeps provider JWT material inside gateway-side request construction rather than a separate runner. | Reject. It violates Owner invariant that runner does not store keys and expands leakage blast radius. |
| C | Let runner load provider keys from its own env or local profile. | `NousResearch/hermes-agent@v0.14.0:mcp_serve.py:71` reads local session state; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/providers/google-vertex-ai/utils.ts:112` keeps JWT issuer material inside gateway-side code. | Reject for Slice 2.2. It makes runner a credential owner. |

Open question OQ-1: the exact internal credential shape is not visible in the Slice 2.2 input set. Before implementation, read the existing HUAKAI credential/provider route code and choose the smallest non-core adapter that does not touch auth core, billing, or quota enforcement.

### D7 - `sse-starlette` dependency

| Option | Decision | References | Recommendation |
|---|---|---|---|
| A | Add exact-pinned `sse-starlette` to runner requirements and use `EventSourceResponse`. | `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/anthropic.py:117` uses FastAPI `StreamingResponse`; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:401` wraps streams with `text/event-stream`. | Owner requested sse-starlette output; proceed only after dependency-license audit. |
| B | Use FastAPI/Starlette `StreamingResponse` without a new dependency. | `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/open_ai.py:164` does exactly this; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:401` returns `text/event-stream` without relying on Python-specific SSE helpers. | Fallback if dependency gate fails. Requires Owner confirmation because it deviates from the stated constraint. HUAKAI current deps: `backend/deploy/hermes-runner/requirements.txt:3`. |
| C | Custom ASGI response class. | `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/openai/openai_handlers.go:457` shows manual stream handling is possible in Go; `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:139` shows custom stream readers are common in gateway code. | Reject unless A and B fail. Adds maintenance burden for no product gain. |

## 4. Implementation Steps And Sub-Slices

Each sub-slice must close with no unresolved S0/S1, local tests for the touched surface, and per-commit review before commit. This plan does not perform the work.

### 2.2a - Runner SSE adapter

Target files/packages:

- Modify `backend/deploy/hermes-runner/main.py`.
- Add Python helper files under `backend/deploy/hermes-runner/`, for example `hermes_chat.py` and `sse_events.py`.
- Modify `backend/deploy/hermes-runner/requirements.txt` only after dependency-license gate for `sse-starlette`.
- Modify `backend/deploy/hermes-runner/entrypoint.sh` to allow JWT-mode startup without `HUAKAI_HERMES_SHARED_SECRET`.

Execution:

1. Parse chat request body into a narrow internal DTO: message, optional conversation_id, model/profile hints, and history.
2. Validate JWT/HMAC middleware still owns tenant/user identity.
3. Derive `HERMES_HOME=/var/lib/huakai/hermes/tenants/{tenant_id}/users/{user_id}`.
4. Set ContextVar override, instantiate `AIAgent`, call `run_conversation(...)` with stream callback.
5. Bridge callback events to an async queue consumed by SSE response.
6. Emit heartbeat comments every 20 seconds while no events are pending.
7. Emit terminal `event: done` with assistant content and token/usage metadata if available.
8. On cancellation, stop the worker cleanly and do not emit done.

No-S0/S1 close condition:

- JWT chat request accepts valid token and rejects mismatched `sub`.
- Two concurrent requests cannot observe each other's `HERMES_HOME`.
- Missing JWT public key config makes runner unavailable or auth fail-closed, not open.
- No raw upstream API key appears in SSE payload or error body.

### 2.2b - Gateway SSE bridge package

Target files/packages:

- Add `backend/internal/hermeschat` as a new package. This package is not frozen.
- Do not add or edit `backend/internal/proto`.
- Modify existing `backend/internal/hermeshttp/chat_handler.go` to use the bridge.

Execution:

1. Implement a small SSE scanner for runner events: comments, `event:`, `data:`, blank-line dispatch, multi-line data.
2. Preserve token/status events for client flush.
3. Accumulate assistant content from token/done payloads.
4. Withhold `event: done` until gateway persistence/audit transaction commits.
5. On tx failure, emit `event: error` and close without done.
6. Propagate request cancellation to runner response body close.
7. Use `Accept: text/event-stream` and avoid compression on runner requests if needed.

No-S0/S1 close condition:

- A test that removes the done withholding behavior must fail.
- A test that ignores SSE comments/heartbeats must fail.
- A test that forwards done before tx commit must fail.

### 2.2c - Gateway conversation/message service

Target files/packages:

- Modify `backend/internal/hermes/types.go` to include existing SQLC conversation/message methods in `Store`.
- Add service code under `backend/internal/hermes`, for example `chat_history.go`.
- Modify `backend/internal/hermes/audit.go` to add `ActionMessageSend = "hermes.message.send"` and validate it.

Execution:

1. Add `CreateOrGetConversationForOwner`, `ListConversationsForOwner`, and `ListMessagesForConversation`.
2. Add `PersistCompletedTurnWithAudit(ctx, tenantID, userID, conversationID, userContent, assistantContent, usage, auditFields)`.
3. Inside one `s.withTx`, append user message, append assistant message, update conversation last_message_at, and insert `hermes.message.send` audit.
4. Ensure audit args use `SanitizeArgs` and include redaction tests for `api_key`, `token`, `password`, and `secret`.
5. Do not alter migration `0058` unless a real schema defect is found; schema changes are high-risk and require Owner confirmation.

No-S0/S1 close condition:

- Audit insert failure rolls back both message inserts.
- Message insert failure rolls back audit.
- Cross-tenant or non-owner conversation read returns not found/forbidden.

### 2.2d - HTTP handlers and route behavior

Target files/packages:

- Modify existing `backend/internal/hermeshttp/chat_handler.go`.
- Modify existing `backend/internal/hermeshttp/conversations_handler.go`.
- Modify router tests if needed under `backend/internal/hermeshttp`.
- Do not edit `backend/internal/gatewayhttp`.

Execution:

1. `POST /v1/hermes/chat` reads request, verifies settings, calls runner, then streams through `hermeschat` bridge.
2. `GET /v1/hermes/conversations` reads PostgreSQL through `hermes.Service`, not runner.
3. `GET /v1/hermes/conversations/{id}/messages` reads PostgreSQL through `hermes.Service`, not runner.
4. Preserve disabled-Hermes response behavior.
5. Preserve fail-closed mount behavior in `backend/cmd/gateway/routes.go:96`.

No-S0/S1 close condition:

- Conversation list/message handlers do not call `RunnerClient`.
- Disabled Hermes blocks chat before runner call.
- Missing runner config leaves `/v1/hermes/*` unmounted while unrelated routes still mount.

### 2.2e - Config, docs, and verification

Target files:

- Runner env docs and example compose/env files, if already present in Slice 2 docs.
- OpenAPI docs only if current API contract files already include Hermes endpoints.
- No production deployment script edits without Owner confirmation.

Execution:

1. Document required runner envs for JWT mode and chat mode.
2. Document that runner must not hold vendor keys.
3. Document `sse-starlette` dependency decision and license result.
4. Add deferred follow-up if internal credential bridge cannot be fully resolved without touching high-risk auth/billing/quota code.

No-S0/S1 close condition:

- Docs do not claim features that tests do not cover.
- Clean-room checklist updated with references read.

## 5. Test Strategy

All new tests must be discriminating. Each test must say what mutation would make it fail.

### 5.1 Python runner tests

1. `test_chat_rejects_mismatched_jwt_subject`
   - Guards: tenant/user headers must match JWT subject.
   - Mutation self-check: remove `claims["sub"] == f"{tenant}:{user}"` in `main.py:112`; test must fail.

2. `test_chat_uses_contextvar_home_per_request`
   - Guards: two concurrent chat calls see different `get_hermes_home()` values.
   - Mutation self-check: replace ContextVar overlay with `os.environ` mutation; concurrency assertion must fail.

3. `test_chat_sse_emits_heartbeat_before_done`
   - Guards: idle stream emits heartbeat comment at configured short test interval.
   - Mutation self-check: remove heartbeat task/timer; test must time out or miss comment.

4. `test_chat_sse_done_contains_final_assistant_content`
   - Guards: final done payload reflects the complete assistant message, not only the first token.
   - Mutation self-check: set final content from first callback token; test must fail.

5. `test_chat_cancellation_does_not_emit_done`
   - Guards: client disconnect cancels worker and closes without false completion.
   - Mutation self-check: always emit done in `finally`; test must fail.

6. `test_runner_jwt_mode_entrypoint_does_not_require_hmac_secret`
   - Guards: JWT config is sufficient for runner startup.
   - Mutation self-check: keep old `entrypoint.sh:8` requirement; test must fail.

7. `test_no_raw_upstream_key_in_sse_or_errors`
   - Guards: API key/token/password/secret values are not serialized into event payloads.
   - Mutation self-check: include request config in error data; test must fail.

### 5.2 Go service tests

1. `TestPersistCompletedTurnWithAudit_AtomicWithAudit`
   - Guards: user message, assistant message, last_message_at update, and `hermes.message.send` audit use the same tx.
   - Mutation self-check: call `RecordAudit` outside `withTx`; audit failure leaves messages and test fails.

2. `TestPersistCompletedTurnWithAudit_SanitizesSensitiveArgs`
   - Guards: nested `api_key`, `token`, `password`, `secret`, and hyphen/dot variants are redacted.
   - Mutation self-check: remove recursive `SanitizeArgs`; nested token leaks and test fails.

3. `TestListMessagesByConversation_RequiresOwner`
   - Guards: owner_user_id condition in `ListMessagesByConversation` remains effective.
   - Mutation self-check: remove owner filter from stub/query; cross-user fixture becomes visible and test fails.

4. `TestConversationHandlers_ReadFromStoreNotRunner`
   - Guards: list endpoints are PG-backed and runner-free.
   - Mutation self-check: route back through `h.runner.Conversations`; runner spy fails the test.

5. `TestChatBridge_WithholdsDoneUntilPersistCommit`
   - Guards: client receives token events before commit, but done only after commit.
   - Mutation self-check: forward runner done immediately; test observes done before tx and fails.

6. `TestChatBridge_PersistFailureEmitsErrorNoDone`
   - Guards: tx failure is visible and not mislabeled as completed.
   - Mutation self-check: ignore persistence error; test receives done and fails.

7. `TestChatBridge_HeartbeatCommentDoesNotBreakParser`
   - Guards: SSE comment lines are tolerated and not treated as data events.
   - Mutation self-check: parse comments as events; test event sequence fails.

8. `TestHermesRouteFailClosedWhenRunnerMissing`
   - Guards: `/v1/hermes/*` is absent when runner missing, but other gateway routes remain mounted.
   - Mutation self-check: mount Hermes with nil runner; test fails.

### 5.3 Read-only proto regression tests

No new files in `backend/internal/proto`. If existing proto tests are runnable, include:

- `cd backend && go test ./internal/proto/...`

The intent is regression detection only. Slice 2.2 bridge must not depend on editing proto adapters.

## 6. Verification Checklist

Run from repo root unless noted:

1. Workspace sanity:
   - `git status --short`
   - `git diff --name-only -- backend/internal/gatewayhttp backend/internal/gateway backend/internal/proto`

2. Go generation/build checks:
   - `cd backend && sqlc generate` if SQL query signatures are touched.
   - `cd backend && go test ./internal/hermes/... ./internal/hermeshttp/... ./cmd/gateway/...`
   - `cd backend && go test ./internal/proto/...`
   - `cd backend && go test -race ./internal/hermes/... ./internal/hermeshttp/...`
   - `cd backend && go vet ./...`

3. Python runner checks:
   - `cd backend/deploy/hermes-runner && python -m unittest -v`
   - `cd backend/deploy/hermes-runner && python -m compileall .`

4. End-to-end smoke, after implementation and local env:
   - Start gateway, postgres, and hermes-runner.
   - Enable Hermes for a test user.
   - `curl -N` POST `/v1/hermes/chat` and observe token/status events, heartbeat if idle, and terminal done.
   - Query `/v1/hermes/conversations` and `/v1/hermes/conversations/{id}/messages` and confirm PG-backed rows.
   - Confirm `hermes_audit_events` has `hermes.message.send` with redacted sanitized_args.

5. Per-commit review:
   - Stage intended diff only.
   - `codex exec review --uncommitted --full-auto --sandbox read-only`
   - Normalize findings to S0/S1/S2/S3 and close all S0/S1 before commit.

## 7. Risks And Rollback

| Risk | Severity | Mitigation | Rollback |
|---|---:|---|---|
| Raw upstream credential leaks to runner logs/SSE | S0 | Do not pass raw vendor keys. Use gateway-internal short-lived credential. Add no-leak tests. | Disable `/v1/hermes/*` mount by removing runner config; rotate any exposed test keys. |
| Done event reaches client before persistence/audit commit | S1 | Withhold done until tx commit. Add ordering test. | Revert bridge handler to failure response path; keep runner route disabled. |
| ContextVar home leaks across concurrent users | S1 | Use `set_hermes_home_override` token and reset in `finally`. Concurrent test. | Disable runner chat route; keep settings/profile routes. |
| New `sse-starlette` dependency has license/supply-chain issue | S1 if unresolved | Run dependency-license audit and exact pin. | Use FastAPI `StreamingResponse` fallback only with Owner approval. |
| Runner import of hermes-agent is heavy or slow | S2 | Lazy import after auth; healthcheck remains light. | Feature flag chat route off while keeping runner health/JWT tests. |
| Persistence tx fails after visible tokens | S1 product trust | Emit error event, no done. Audit failure must fail closed for message.send. | Disable chat handler, keep list/read routes. |
| Query/store interface drift from sqlc output | S1 if build fails | Run `sqlc generate` and Go tests in the same sub-slice. | Revert service method additions; keep schema. |
| Frozen-package accidental edit | S1 structure violation | Explicit diff check on frozen paths before review. | Revert only the accidental frozen-package changes. |

## 8. Clean-Room Checklist

Read and allowed:

- `NousResearch/hermes-agent@v0.14.0` - MIT per `pyproject.toml:12`.
  - `pyproject.toml:5`
  - `hermes_constants.py:15`
  - `agent/conversation_loop.py:232`
  - `run_agent.py:350`
  - `mcp_serve.py:62`
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6` - MIT per local LICENSE.
  - `sdk/api/handlers/claude/code_handlers.go:67`
  - `sdk/api/handlers/claude/code_handlers.go:212`
  - `sdk/api/handlers/openai/openai_handlers.go:457`
  - `internal/runtime/executor/claude_executor.go:280`
  - `internal/runtime/executor/claude_executor.go:998`
- `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a` - Apache-2.0 local ref.
  - `gateway/routes/anthropic.py:109`
  - `gateway/routes/open_ai.py:156`
- `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da` - MIT local ref.
  - `src/handlers/streamHandler.ts:70`
  - `src/handlers/streamHandler.ts:139`
  - `src/handlers/streamHandler.ts:392`
  - `src/providers/google-vertex-ai/utils.ts:121`

Not read:

- Sub2API source.
- New-API source.
- All-API-Hub source.
- One-API source for this plan.
- LiteLLM source for this plan.
- GPL Helicone AI gateway source.

Clean-room rules for implementation:

- Do not vendor or copy upstream implementation.
- Do not preserve upstream file structure.
- Use behavior-only references.
- Keep source citation comments out of runtime code unless they are HUAKAI-internal design docs.
- Any future non-MIT source read must use the exact clean-room lane guard from `AGENTS.md`.

## 9. Estimate And Blast Radius

Estimate:

| Work item | Wall time | Agent time |
|---|---:|---:|
| 2.2a Runner adapter and Python tests | 1.0-1.5 days | 4-6 hours |
| 2.2b Gateway SSE bridge and parser tests | 1.0-1.5 days | 5-7 hours |
| 2.2c Service persistence/audit and tests | 1.0 day | 4-5 hours |
| 2.2d Handler integration and route tests | 0.5-1.0 day | 3-4 hours |
| 2.2e validation, review, docs | 0.5-1.0 day | 2-4 hours |
| Review/fix budget | 0.5-1.0 day | 2-4 hours |

Total: 4.5-7 days wall time, 20-30 focused agent hours.

Blast radius:

- Python runner behavior changes from auth-only skeleton to real in-process Hermes chat.
- Gateway Hermes API changes from proxy-only/501 behavior to PG-backed history and streamed chat.
- Hermes DB writes become user-visible through `/v1/hermes/conversations*`.
- Audit expands from chat-start to message-send completion.
- No database schema change expected in this slice.
- No frozen package changes expected.
- No billing/quota/auth-core changes expected. Credential bridge may need a later dedicated plan if it touches those high-risk surfaces.

## 10. Frozen Package Constraint Record

Frozen packages:

- `backend/internal/gatewayhttp`
- `backend/internal/gateway`
- `backend/internal/proto`

Allowed in Slice 2.2:

- Read `backend/internal/proto/anthropic/sse.go` and `backend/internal/proto/openai/sse.go` to understand existing SSE terminal semantics.
- Add new package `backend/internal/hermeschat`.
- Modify existing `backend/internal/hermes` files and add new files there if package size remains under budget.
- Modify existing `backend/internal/hermeshttp` files and add tests there if needed.
- Modify Python runner files under `backend/deploy/hermes-runner`.

Forbidden in Slice 2.2 without Owner confirmation:

- Any new file under frozen packages.
- Any edit under `backend/internal/proto`, even for a tempting helper, unless Owner explicitly confirms a high-risk exception.
- Any new gateway route wiring under `backend/internal/gatewayhttp`; Hermes HTTP surface stays in `backend/internal/hermeshttp`.

Required guard command before review:

```bash
git diff --name-only -- backend/internal/gatewayhttp backend/internal/gateway backend/internal/proto
```

Expected output: empty.

## 11. Open Questions For Owner Or Next Planning Diff

1. OQ-1: What exact gateway-internal credential/token should runner use as `api_key` when calling the gateway-owned upstream path? This likely needs a small dedicated credential bridge plan because auth/billing/quota surfaces are high-risk.
2. OQ-2: Is adding `sse-starlette` explicitly approved after dependency-license audit, or should implementation use `StreamingResponse` fallback if the audit is not ready?
3. OQ-3: Should chat create a conversation row before runner call when request lacks `conversation_id`, or should it buffer until done and create only on completion? Recommendation: create in the same completion tx if no first event needs a conversation id; create before runner only if UI must show id immediately.
4. OQ-4: Should `hermes.chat.start` remain as a separate audit event, or should Slice 2.2 collapse successful chat completion into only `hermes.message.send` plus failure-start audit? Recommendation: keep `chat.start` for runner-call attempts and add `message.send` for committed completion.
5. OQ-5: Should retry/idempotency be in Slice 2.2? Current schema has no request-id uniqueness constraint for messages. Recommendation: defer idempotency to a later schema slice unless Owner requires it now.

## 12. Pre-Execution Checklist

Before implementation begins:

1. Owner accepts or rejects the independence caveat in §0.
2. Owner confirms D6 credential bridge approach or authorizes a separate pre-implementation mini-plan.
3. Dependency-license audit is run for `sse-starlette` if D7-A is chosen.
4. Confirm working tree is clean or intended dirty files are documented.
5. Confirm target packages are not frozen.
6. Confirm Slice 2.0 migration and SQLC output are current after `4b02edf`.
7. Confirm no non-MIT reference source is needed for implementation.
8. Confirm sub-slice order: 2.2c service primitives can be built before 2.2a runner if gateway tests need early fixtures; otherwise runner adapter can proceed first.

## 13. Source Coverage Proof

HUAKAI source/doc regions read:

- `CLAUDE.md` rules #8, #10, #11, #12, #13, #14, #15.
- `AGENTS.md` Owner start/proactive execution, clean-room, truth-first, planning, frozen packages, test quality, summary rules.
- `docs/process/plans/2026-05-25-hermes-phase-1-slice2-synthesis.md`.
- `docs/process/plans/2026-05-25-hermes-phase-1-slice2-codex.md` for prior Codex plan style.
- `backend/deploy/hermes-runner/main.py:1`.
- `backend/deploy/hermes-runner/jwt_verify.py:1`.
- `backend/deploy/hermes-runner/requirements.txt:1`.
- `backend/deploy/hermes-runner/entrypoint.sh:1`.
- `backend/internal/hermes/runner_client.go:1`.
- `backend/internal/hermes/audit.go:1`.
- `backend/internal/hermes/types.go:102`.
- `backend/internal/hermes/settings.go:21`.
- `backend/internal/hermeshttp/chat_handler.go:12`.
- `backend/internal/hermeshttp/conversations_handler.go:10`.
- `backend/cmd/gateway/routes.go:96`.
- `backend/sql/queries/hermes.sql:139`.
- `backend/internal/proto/anthropic/sse.go:180` read-only.
- `backend/internal/proto/openai/sse.go:176` read-only.

Reference source regions read:

- `NousResearch/hermes-agent@v0.14.0:pyproject.toml:5`.
- `NousResearch/hermes-agent@v0.14.0:hermes_constants.py:15`.
- `NousResearch/hermes-agent@v0.14.0:agent/conversation_loop.py:232`.
- `NousResearch/hermes-agent@v0.14.0:run_agent.py:327`.
- `NousResearch/hermes-agent@v0.14.0:mcp_serve.py:62`.
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:67`.
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/claude/code_handlers.go:212`.
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:sdk/api/handlers/openai/openai_handlers.go:457`.
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:280`.
- `router-for-me/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/claude_executor.go:998`.
- `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/anthropic.py:109`.
- `invariant-gateway@9baeade022cc55de2412ba3dcae98069bd6f794a:gateway/routes/open_ai.py:156`.
- `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:70`.
- `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:139`.
- `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/handlers/streamHandler.ts:392`.
- `Portkey-AI/gateway@d2ea41f4e17c65112b6289a939014bd6b1df62da:src/providers/google-vertex-ai/utils.ts:121`.

## 14. Owner Chinese Summary

本计划把 Slice 2.2 定义为“gateway 统一入口 + runner 真实 hermes-agent 执行 + SSE done 栅栏 + gateway 原子持久化和审计”的小闭环；不做 schema 迁移、不让浏览器直连 runner、不让 runner 连接 DB 或保存上游 key、不碰 frozen 包。真实观察来自 HUAKAI 当前 runner/JWT/handler/SQL/proto 代码，以及 MIT/Apache 参考项目的 SSE、ContextVar、stream-aware gateway 行为；合理推断主要集中在 runner callback 到 async SSE queue、gateway done 前置持久化、内部 credential bridge 的最小安全形态。Open questions 共 5 个，其中最高优先级是 D6 credential bridge 的精确形态和 D7 `sse-starlette` 依赖批准。没有功能缩水；clean-room 风险低，但需要 Owner 接受 §0 中 accidental Claude-plan grep exposure 的独立性 caveat。
