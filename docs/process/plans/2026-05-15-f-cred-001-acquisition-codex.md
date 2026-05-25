# 2026-05-15 F-CRED-001 automated credential acquisition flow — Codex SPECIFIER plan

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: sub2api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

## metadata

| Field | Value |
| --- | --- |
| Owner directive | Owner 2026-05-15: "对了，怎么获取这个功能你们也要做！看看sub2api" |
| Work unit | F-CRED-001 automated credential acquisition flow — Go backend plan only |
| Current action | SPECIFIER plan + parity matrix row update; no implementation; no commit |
| Observed regions | 58 source regions across requested Sub2API files plus targeted adjacent files needed for missing mechanisms |
| Inferences | 9, each marked as HUAKAI design inference or "已读区域未观察到" |
| Open questions | 6 |
| HUAKAI anchor | F-AUTH-005 manages stored credentials and refresh; F-CRED-001 must acquire credentials and hand them to F-AUTH-005 without weakening storage |

## scope

F-CRED-001 covers credential acquisition, not long-term credential management.

In scope:

- Admin/Owner starts an acquisition flow for one `(tenant_id, provider_account_id, vendor, auth_mode)` pair.
- The flow collects or obtains upstream credential material by browser OAuth, cookie/session bootstrap, CLI auth-file import, cloud bootstrap, API-key paste, token endpoint exchange, or special vendor path.
- The flow validates shape, redacts secrets, emits F-TRUST-visible audit, and finalizes by calling F-AUTH-005 storage (`account_credentials`) with encrypted-at-rest payload.
- The flow records enough state to recover from browser callback, retry UI polling, and operator cancellation.

Out of scope:

- No change to gateway request routing, pool selection, billing ledger, quota enforcement, or auth core.
- No change to the F-AUTH-005 refresh scheduler semantics.
- No server-side blind read of an operator workstation path such as `/home/codex/.codex/auth.json`; the safe path is admin-upload/paste unless Owner explicitly approves a local-agent connector.
- No production secrets, OAuth client secrets, cloud keys, or real credentials committed to the repo.

Boundary with F-AUTH-005:

- F-AUTH-005 already defines the 15 allowed `(vendor, auth_mode)` cells in `account_credentials` and encrypted payload metadata (`backend/sql/migrations/0016_account_credentials.up.sql:49`, `backend/sql/migrations/0016_account_credentials.up.sql:80`).
- F-AUTH-005 runtime handlers already parse the 15 modes into runtime material (`backend/internal/credentialstore/types.go:18`, `backend/internal/credentialstore/types.go:184`) and the scheduler scans refreshable credentials (`backend/internal/credentialworker/mode_refresh.go:49`, `backend/internal/credentialworker/mode_refresh.go:171`).
- F-CRED-001 should end at `credentialstore.Create` / `credentialstore.Rotate`; it must not duplicate refresh, CAS, or token-cache code.

## upstream mechanism summary

Sub2API has multiple acquisition and refresh-adjacent flows, but not one unified 15-mode acquisition contract. The observed behavior is a set of provider-specific flows: browser OAuth for OpenAI/Gemini/Antigravity, Claude cookie-assisted bootstrap, Codex CLI auth content import, manual Bedrock credential form, and Vertex service-account JSON import. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:96`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:32`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:353`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:1396`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:92`.

Mechanism observations:

- OAuth callback flow: provider service creates a state value, PKCE verifier/challenge, callback redirect, proxy setting, and a short-lived session id before sending the admin to the vendor authorization URL. Callback exchange reloads that session, checks state, exchanges the code at the provider token endpoint, deletes the session, and enriches account metadata when available. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:130`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:445`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97`.
- Refresh token rotation: token refresh writes a new refresh token only when the upstream response includes one; otherwise the existing refresh token is preserved. Refresh workers invalidate cache, clear temporary error flags on success, and classify permanent OAuth errors from known denial/revocation strings. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:879`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:245`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:345`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:411`.
- Cookie-based session acquisition: Claude path accepts a browser session value from the admin, uses it to fetch account/org metadata, generates PKCE parameters, obtains an authorization result, and exchanges it for a token response. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:94`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174`.
- CLI auth-file import: observed Codex flow accepts pasted/uploaded content rather than reading a workstation path directly, parses single JSON, arrays, JSON streams, JSON-lines, plain token lines, and merges/upserts by identity hints. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:112`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:353`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:461`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:823`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/OAuthAuthorizationFlow.vue:182`.
- Bedrock acquisition: in the read regions, the observed flow is manual SigV4/API-key entry plus server-side request signing; no automatic STS AssumeRole bootstrap was observed in those Bedrock-specific regions. Evidence for manual form and signing: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:1396`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:4268`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/bedrock_signer.go:35`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/bedrock_signer.go:54`.
- Vertex service-account import: the observed flow accepts service-account JSON, validates required fields, uses a fixed Google token endpoint instead of trusting a JSON-supplied endpoint, signs a bearer assertion, exchanges for an access token, and builds Vertex URLs from validated project/location/model values. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:804`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:4200`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:92`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:186`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:251`.
- Gemini variants: Code Assist and Google One share the Google token endpoint but differ in client selection, project discovery, and tier detection; AI Studio requires configured OAuth client input in the observed region. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/gemini_oauth_client.go:28`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:531`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:577`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/pkg/geminicli/oauth.go:151`.
- Antigravity special path: observed flow has its own authorization URL, code exchange, user info, project/plan lookup, refresh, and privacy-mode follow-up. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:32`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:336`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:478`.
- Auth-flow hygiene lesson: one migration strips token-shaped fields from pending local flow state, and another broadens provider constraints for user-auth identity sources; this is evidence for a redaction/migration discipline, not a credential-acquisition table shape to copy. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/122_pending_auth_completion_token_cleanup.sql:1`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/135_allow_email_oauth_provider_types.sql:1`.
- Affiliate file scope: the observed frontend utility stores referral codes in local/session storage and builds request payload fragments for user signup flows; it is not evidence of upstream credential acquisition by itself. Evidence: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/utils/oauthAffiliate.ts:25`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/utils/oauthAffiliate.ts:87`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/utils/oauthAffiliate.ts:130`.

## HUAKAI upgrade delta

HUAKAI should not reproduce provider-specific acquisition flows one by one. The upgrade is a provider-neutral acquisition session layer whose finalizer writes to existing F-AUTH-005 storage. HUAKAI already has a 15-mode vendor/auth-mode lattice in `account_credentials`, whereas the observed Sub2API behavior is partial and provider-specific. Local basis: `backend/sql/migrations/0016_account_credentials.up.sql:49`, `backend/internal/credentialstore/types.go:18`, `backend/internal/credentialworker/mode_refresh.go:49`.

Delta table:

| Area | Observed upstream evidence | HUAKAI delta | Upgrade taxonomy |
| --- | --- | --- | --- |
| Browser OAuth | OpenAI/Gemini/Antigravity store PKCE/state sessions and exchange code after state check: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:445`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97` | One `credential_acquisition_flow_sessions` contract for every browser-capable mode; state stored hashed, verifier encrypted, callback finalizer calls F-AUTH-005 store. | 架构 + 生态 |
| Cookie/session bootstrap | Claude browser session bootstrap observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161` | Treat cookie/session value as a transient acquisition input; never persist raw cookie/session beyond final encrypted credential payload. | 架构 + 安全生态 |
| Refresh token rotation | Upstream preserves old refresh token when refresh response omits a replacement: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:879` | Keep this rule inside F-AUTH-005 refresh adapters; acquisition only initializes fingerprints and `refresh_before_at`. | 算法 |
| CLI import | Codex import accepts pasted/uploaded auth content and handles multiple shapes: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:353`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:461` | Support `codex_cli_oauth` and `claude_code` through a common `cli_import` parser interface; default UI upload/paste, optional local-agent connector only after Owner OCAW. | 架构 + 生态 |
| Bedrock | Manual SigV4/API-key entry plus server signing observed; no automatic STS bootstrap observed in the read Bedrock regions: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:1396`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/bedrock_signer.go:35` | Add optional STS bootstrap as HUAKAI upgrade: role ARN/external id/session duration -> short-lived AWS material -> encrypted Bedrock mode payload. Manual SigV4/API-key stays available. | 架构 + 生态 |
| Vertex SA | JSON validation and fixed token endpoint observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:92`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:186` | Reuse the safety rule: ignore untrusted token endpoint from uploaded JSON, validate project/location/client email, emit audit proof, store only encrypted minimized payload. | 架构 + 安全生态 |
| Audit visibility | In the read Sub2API acquisition regions, I observed local persistence, logs, UI status, and cleanup migrations, but did not observe a HUAKAI-style public verification chain. | Emit `credential_acquisition_started`, `credential_acquisition_completed`, `credential_acquisition_failed`, `credential_acquisition_cancelled` into admin audit and F-TRUST chain with secret-free payloads. | 生态 |
| 1-click admin UX | UI has separate panels for OAuth, Codex import, Bedrock, and Vertex import: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/OAuthAuthorizationFlow.vue:84`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:4667` | Gemini frontend slice should expose one acquisition wizard backed by mode registry metadata, not hand-coded per provider screens. | 生态 |

## file-by-file impact

Implementation is not authorized by this plan; these are planned touchpoints.

Backend package additions:

- `backend/internal/credentialacq/types.go` — HUAKAI-owned domain types: `FlowKind`, `FlowStatus`, `ModePlan`, `StartInput`, `CallbackInput`, `FinalizeResult`. This package owns acquisition only.
- `backend/internal/credentialacq/session_store.go` — Postgres-backed acquisition session CRUD. Stores hashed state, encrypted PKCE verifier, flow kind, mode, expiry, status, redacted context.
- `backend/internal/credentialacq/oauth.go` — PKCE/state generator, provider-neutral browser OAuth start/callback orchestration. Provider-specific endpoints live behind local adapters.
- `backend/internal/credentialacq/cli_import.go` — parser for admin-uploaded Codex/Claude Code auth JSON or pasted token content. It must not read local filesystem paths by default.
- `backend/internal/credentialacq/cloud_bootstrap.go` — Bedrock STS, Azure token exchange, Vertex service-account validation/bootstrap adapters. Each adapter accepts explicit admin input and returns a normalized credential payload.
- `backend/internal/credentialacq/finalizer.go` — validates target 15-mode cell through `credentialstore.HandlerRegistry`, then calls `credentialstore.Create` or `credentialstore.Rotate`.
- `backend/internal/credentialacq/audit.go` — admin audit + F-TRUST event writer with redacted payload schema.

Backend file modifications:

- `backend/sql/migrations/00xx_credential_acquisition_sessions.up.sql` / `.down.sql` — future migration for acquisition sessions. High-risk DB schema; requires Owner OCAW before implementation.
- `backend/internal/db/models.go`, `backend/internal/db/querier.go`, `backend/sql/queries/*.sql`, generated sqlc files — add acquisition-session query surface after OCAW.
- `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go` — new admin routes:
  - `POST /v1/admin/pool-accounts/{id}/credential-acquisitions`
  - `GET /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}`
  - `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/callback`
  - `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/cancel`
  - `POST /v1/admin/pool-accounts/{id}/credential-acquisitions/{flow_id}/finalize`
- `backend/internal/gatewayhttp/admin_credentials_handler.go` — keep direct paste/rotate endpoints; add comments/tests confirming F-CRED-001 uses the same store boundary.
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go` — new account creation may optionally return `credential_acquisition_next_step` when no credential is supplied yet.
- `backend/cmd/gateway/main.go` — wire acquisition service, admin auth, credential store, audit ledger, and route mount. Existing F-AUTH-005 scheduler remains unchanged (`backend/cmd/gateway/main.go:323`).
- `docs/openapi/openapi.yaml` — add acquisition request/response schemas and route docs after the backend spec exists; current provider-account contract only supports direct create/update (`docs/openapi/openapi.yaml:415`, `docs/openapi/openapi.yaml:1201`).

Files explicitly out of scope:

- `backend/internal/gateway/`, `backend/internal/router/`, `backend/internal/billing/`, `backend/internal/rate/` — acquisition must not touch request-path routing, billing, quota, or cooldown logic.
- `LICENSE`, deployment scripts, real secrets, production env files — no changes.

## flow per mode

The 15 rows are from HUAKAI's current vendor/mode lattice (`backend/sql/migrations/0016_account_credentials.up.sql:49`, `backend/internal/credentialstore/types.go:18`).

| # | vendor/auth_mode | Acquisition flow path | Final F-AUTH-005 sink | Notes / citations |
| --- | --- | --- | --- | --- |
| 1 | `anthropic/api_key` | API key paste | `credentialstore.Create` with `api_key` runtime kind | Manual path; no upstream acquisition needed. |
| 2 | `anthropic/claude_ai_oauth` | OAuth browser flow; optional cookie/session bootstrap | encrypted OAuth/upstream-passthrough payload | Claude cookie-assisted bootstrap observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`. |
| 3 | `anthropic/claude_code` | CLI auth import + optional Claude browser flow | encrypted session/OAuth payload | Treat as HUAKAI upgrade over observed cookie/OAuth behavior; no direct Claude Code file import was observed in requested Sub2API backend files. |
| 4 | `anthropic/bedrock` | Cloud SDK bootstrap: manual SigV4/API key first, optional STS after OCAW | encrypted AWS SigV4 payload | Manual Bedrock path observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:1396`. |
| 5 | `anthropic/vertex_anthropic` | Cloud SDK bootstrap: Vertex service-account JSON import | encrypted service-account / token payload | Vertex fixed-endpoint exchange observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:186`. |
| 6 | `openai/api_key` | API key paste | encrypted API key payload | Direct manual path; F-CRED-001 only improves validation/audit. |
| 7 | `openai/chatgpt_oauth` | OAuth browser flow | encrypted session/OAuth payload | OpenAI browser flow observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`. |
| 8 | `openai/codex_cli_oauth` | CLI auth import from admin-uploaded `/home/codex/.codex/auth.json` content | encrypted session/OAuth payload | Codex import parser observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:353`. |
| 9 | `openai/azure` | Cloud SDK bootstrap: Azure token endpoint / managed identity / API key, exact path OCAW | encrypted API-key or access-token passthrough payload | HUAKAI existing mode allows `azure`; acquisition adapter design must be HUAKAI-owned. |
| 10 | `openai/refresh_token` | Token endpoint exchange from pasted refresh token + client metadata | encrypted OAuth payload | Refresh grant behavior observed for OpenAI-style flow: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/openai_oauth_service.go:74`. |
| 11 | `gemini/aistudio_api_key` | API key paste | encrypted API key payload | Direct manual path; UI can probe key shape before save. |
| 12 | `gemini/vertex_sa` | Cloud SDK bootstrap: service-account JSON upload/import | encrypted service-account / token payload | Service-account JSON validation observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:92`. |
| 13 | `gemini/code_assist` | OAuth browser flow | encrypted session/OAuth payload with project metadata | Gemini Code Assist project path observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:531`. |
| 14 | `gemini/google_one` | OAuth browser flow | encrypted session/OAuth payload with tier metadata | Google One tier/project handling observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:577`. |
| 15 | `gemini/antigravity` | Antigravity special OAuth + project/plan follow-up | encrypted session/OAuth payload | Special exchange/metadata path observed: `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97`. |

## data model

Proposed table: `credential_acquisition_flow_sessions` (name is HUAKAI-owned and not copied from upstream).

Purpose:

- Durable callback correlation and operator-visible state.
- No raw access token, refresh token, API key, cookie, private key, or cloud secret persisted in this table.
- Any transient secret needed for OAuth, such as PKCE verifier, is encrypted using the same key-provider discipline as F-AUTH-005 or stored as a secret reference with TTL.

Draft fields:

| Field | Purpose |
| --- | --- |
| `id` | UUID or bigint flow id, externally opaque |
| `tenant_id`, `provider_account_id` | tenant/account scope |
| `vendor`, `auth_mode`, `flow_kind` | one of the 15 target cells plus acquisition mechanism |
| `status` | `started`, `waiting_for_user`, `callback_received`, `validated`, `finalized`, `cancelled`, `expired`, `failed` |
| `actor_id`, `actor_role` | admin identity and later RBAC/audit |
| `state_hash`, `nonce_hash` | never raw state; compare constant-time after hashing |
| `encrypted_transient_payload` | optional encrypted PKCE verifier / cloud bootstrap nonce; no provider tokens |
| `redirect_uri`, `proxy_profile_id` | callback/proxy context, redacted in logs |
| `requested_scopes`, `provider_context` | redacted JSON: project id, region, account email hash, tier label |
| `expires_at`, `consumed_at`, `cancelled_at` | lifecycle |
| `result_account_credential_id` | set after finalizer writes to `account_credentials` |
| `error_class`, `error_message_redacted` | operator diagnosis without secret leakage |
| `idempotency_key` | prevents duplicate callback/finalize |
| `created_at`, `updated_at` | timeline |

Retention:

- Successful and failed sessions keep redacted metadata for audit window.
- Expired/cancelled sessions are pruned by a small worker or SQL job.
- Redacted audit stays append-only; flow table can be pruned.

High-risk note:

- Adding this table is a database schema change. Implementation must stop for Owner OCAW before migration creation.

## frontend admin UI

Gemini frontend slice should build a single "获取凭据" wizard:

- Step 1: choose vendor/auth mode from backend registry metadata.
- Step 2: show the safe acquisition method for that mode:
  - Browser OAuth: start URL + callback polling + paste callback URL fallback.
  - CLI import: upload/paste JSON/text; show the safe local path hint only as operator guidance.
  - Cloud SDK bootstrap: provider-specific fields with explicit "test/validate" before finalize.
  - API key paste: masked input + probe option.
  - Token exchange: refresh-token/client metadata fields + dry-run exchange.
  - Antigravity: special OAuth + project/plan result preview.
- Step 3: preview redacted metadata and final account/credential target.
- Step 4: finalize into `account_credentials`, then show audit id and next refresh window.

Coordination rule:

- Frontend must not embed upstream OAuth client secrets or store raw tokens in localStorage.
- Frontend can store only flow id and UI state in session storage; raw pasted credential content is sent once over admin API.
- UI copy must distinguish "获取凭据" from "管理已存凭据" to avoid confusing F-CRED-001 with F-AUTH-005.

## clean-room: Sub2API citation table

| Mechanism | Evidence | HUAKAI delta |
| --- | --- | --- |
| OAuth start/session | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:96`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:32` | One provider-neutral acquisition session table + mode registry. |
| OAuth callback/code exchange | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:130`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:445`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97` | Constant-time state hash compare, idempotency key, finalizer to encrypted store. |
| Claude cookie/session bootstrap | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174` | Treat as transient operator-provided bootstrap input; redact and expire aggressively. |
| Refresh token rotation | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:879`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:245` | Keep in F-AUTH-005 refresh adapters; acquisition initializes material only. |
| Codex CLI import | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:112`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:353`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:461`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/handler/admin/account_codex_import.go:823` | Common CLI importer for `codex_cli_oauth` and `claude_code`; no server-side workstation file reads by default. |
| Bedrock manual credentials | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:1396`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/bedrock_signer.go:35` | Add STS bootstrap as HUAKAI upgrade after Owner OCAW; preserve manual path. |
| Vertex service account | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:frontend/src/components/account/CreateAccountModal.vue:804`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:92`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/vertex_service_account.go:186` | Server validates minimized JSON and never trusts uploaded token endpoint. |
| Antigravity special | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:97`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:336`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresh_service.go:478` | Separate adapter behind same acquisition interface; special metadata becomes redacted context. |
| Flow state cleanup lesson | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/122_pending_auth_completion_token_cleanup.sql:1`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/135_allow_email_oauth_provider_types.sql:1` | Acquisition table forbids provider-token fields and stores only redacted metadata after completion. |

## fusion-upgrade taxonomy

| Upgrade | 架构 | 算法 | 生态 |
| --- | --- | --- | --- |
| 15-mode unified acquisition registry | One service boundary maps mode -> flow adapter -> F-AUTH-005 finalizer. | Adapter selection is deterministic from `(vendor, auth_mode, flow_kind)`. | Admin UI shows one workflow instead of provider-specific scattered panels. |
| Durable acquisition session | Postgres state with hashed callback fields and idempotency. | Expiry/cancel/finalize state machine prevents duplicate callback writes. | Operators can see active, failed, and expired flows. |
| Secret-free audit chain | Acquisition emits F-TRUST events separate from stored credential audit. | Redaction validator blocks token-shaped payload fields before audit. | Owner can inspect who acquired which account without seeing secrets. |
| CLI import safe equivalent | Upload/paste content is parsed server-side; no default local filesystem read. | Parser handles JSON, arrays, JSON-lines, and single-token shapes with deterministic rejection. | UI can guide `/home/codex/.codex/auth.json` without violating workstation boundary. |
| Cloud bootstrap | Cloud-specific acquisition adapters normalize to the same encrypted payload contract. | Token endpoint/STS responses get TTL-derived `refresh_before_at`. | One-click bootstrap for Bedrock/Azure/Vertex becomes a commercial ops feature. |

## test plan

Unit tests:

- `backend/internal/credentialacq`: state generation, state-hash compare, expiry, cancellation, idempotent finalize.
- OAuth adapters: callback with wrong state rejects; callback replay rejects; provider error returns sanitized message; success calls a fake finalizer once.
- CLI import parser: valid Codex JSON, valid raw access token with explicit expiry, multi-entry array, JSON-lines, malformed JSON, missing expiry, duplicate identity.
- Cloud adapters: Bedrock STS mock success/failure; Azure token endpoint mock; Vertex service-account JSON validation rejects missing project/client/private key and ignores untrusted token endpoint.
- Audit redaction: no token-shaped substring appears in acquisition audit payload or log error.

Integration tests:

- Admin auth required for start/callback/finalize/cancel.
- Create flow -> fake provider callback -> encrypted credential row exists -> acquisition session finalized -> credential audit and acquisition audit exist.
- Expired flow cannot finalize.
- Cancelled flow cannot callback/finalize.
- Finalizer uses `credentialstore.HandlerRegistry` so all 15 modes validate through the existing mode contract.
- Existing direct credential endpoints continue to work; F-CRED-001 does not break F-AUTH-005 tests.

Acceptance IDs:

- `AT-CRED-001-001` browser OAuth happy path for `chatgpt_oauth`.
- `AT-CRED-001-002` callback state mismatch is rejected and audited.
- `AT-CRED-001-003` `claude_ai_oauth` cookie bootstrap never stores raw cookie in flow table.
- `AT-CRED-001-004` `codex_cli_oauth` import from uploaded `/home/codex/.codex/auth.json` content creates encrypted credential.
- `AT-CRED-001-005` `claude_code` CLI import uses same parser boundary and does not read local path.
- `AT-CRED-001-006` Bedrock manual SigV4 save stays supported.
- `AT-CRED-001-007` Bedrock STS bootstrap mock produces short-lived payload and refresh window.
- `AT-CRED-001-008` Vertex SA upload validates and ignores uploaded token endpoint.
- `AT-CRED-001-009` `code_assist` stores project metadata only in redacted context/audit.
- `AT-CRED-001-010` `google_one` stores tier metadata without exposing token bytes.
- `AT-CRED-001-011` `antigravity` special metadata failure does not leak secrets and marks operator attention.
- `AT-CRED-001-012` API key paste emits credential-created audit with `credentials_present=true` only.
- `AT-CRED-001-013` token endpoint exchange preserves existing refresh-token rotation rule after F-AUTH-005 refresh.
- `AT-CRED-001-014` concurrent finalize replay creates exactly one credential.
- `AT-CRED-001-015` all 15 mode cells have either implemented acquisition path or explicit manual-first path.

Checks:

- `go test ./backend/internal/credentialacq/...`
- `go test ./backend/internal/gatewayhttp -run CredentialAcquisition`
- `go test ./backend/internal/credentialstore ./backend/internal/credentialworker`
- `go test ./backend/cmd/gateway -run TestRoutes`
- OpenAPI YAML parse if contract changes.

## time estimate

Planning and review:

- This specifier plan + matrix update: 1.5-2.5 hours.
- Independent reviewer-lane audit after Owner synthesis: 1-2 hours.

Implementation estimate after Owner OCAW:

- Contract/schema/OCAW synthesis: 0.5-1 engineering day.
- Backend acquisition core + admin handlers: 2-3 engineering days.
- OAuth/CLI/cloud adapters across 15 modes: 2-4 engineering days, depending on cloud bootstrap depth.
- Tests and redaction/audit verification: 1.5-2 engineering days.
- Frontend coordination with Gemini: 1-2 engineering days.

## blast radius

Plan-only blast radius: docs and parity matrix only.

Implementation blast radius:

- High: new DB migration, admin API that handles raw credential material, outbound OAuth/cloud calls, F-TRUST audit payload correctness.
- Medium: route wiring in `backend/cmd/gateway/main.go`, OpenAPI schemas, admin UI flow.
- Low: parser-only helpers and mock-only adapter tests.

Failure modes and mitigations:

- Token leakage in logs/audit -> central redaction validator, tests with token fragments, audit payload allowlist.
- Callback replay -> idempotency key + consumed timestamp + unique `(flow_id, finalize)` guard.
- Wrong tenant/account finalization -> require tenant/account/mode in flow row and re-check admin identity at finalize.
- OAuth client drift -> provider adapter config with explicit version/audit; no hard-coded secret in repo.
- Cloud bootstrap abuse -> default manual-first for cloud paths; STS/Azure bootstrap behind Owner OCAW and per-tenant enable flag.
- Refresh storm after acquisition -> finalizer sets F-AUTH-005 `refresh_before_at` and uses existing scheduler/storm controller.
- ID collision in parity matrix -> existing `F-CRED-001` row is updated to this acquisition scope rather than duplicating Feature ID.

## decision points (5 Owner OCAW)

1. OCAW-F-CRED-001-01 — approve new DB table `credential_acquisition_flow_sessions` and related sqlc/OpenAPI schema.
2. OCAW-F-CRED-001-02 — approve supported OAuth client registrations, redirect URI shape, and whether provider client ids/secrets are built-in config, tenant config, or environment-only.
3. OCAW-F-CRED-001-03 — approve cloud bootstrap depth: Bedrock STS AssumeRole, Azure token exchange/managed identity, Vertex SA import, and which paths are default-on vs feature-flagged.
4. OCAW-F-CRED-001-04 — decide CLI import boundary: admin upload/paste only (recommended) vs optional local-agent connector allowed to read `/home/codex/.codex/auth.json`.
5. OCAW-F-CRED-001-05 — decide F-AUTH-006 relationship: keep as narrow historical OAuth-bootstrap row, mark it merged into F-CRED-001, or rename it after synthesized plan.

## open questions

1. Should F-AUTH-006 be superseded by F-CRED-001 or remain as a narrower Sub2API OAuth-bootstrap blocker?
2. Which provider OAuth client ids/secrets can HUAKAI legally ship or require operator-supplied config for?
3. Does Owner want Bedrock STS bootstrap in L1, or should L1 preserve manual SigV4/API-key and defer STS?
4. Does Azure acquisition mean API key paste, OAuth client credential, managed identity, or all three?
5. Should admin acquisition routes stay under current `/v1/admin/pool-accounts` surface or be normalized to `/admin/v1/provider-accounts` in OpenAPI first?
6. For Antigravity, which project/plan metadata must block credential creation vs become operator-attention metadata?

## sources read

Sub2API source files read:

- `backend/internal/repository/claude_oauth_service.go`
- `backend/internal/repository/openai_oauth_service.go`
- `backend/internal/repository/gemini_oauth_client.go`
- `backend/internal/service/openai_oauth_service.go`
- `backend/internal/service/gemini_oauth.go`
- `backend/internal/service/gemini_oauth_service.go`
- `backend/internal/service/oauth_service.go`
- `backend/internal/service/token_refresh_service.go`
- `backend/internal/service/token_refresh_service_test.go`
- `backend/internal/service/auth_oauth_email_flow.go`
- `backend/internal/service/antigravity_oauth_service.go`
- `backend/internal/service/antigravity_token_refresher.go`
- `backend/internal/service/bedrock_signer.go`
- `backend/internal/service/vertex_service_account.go`
- `backend/internal/pkg/geminicli/oauth.go`
- `backend/internal/handler/admin/account_handler.go`
- `backend/internal/handler/admin/gemini_oauth_handler.go`
- `backend/internal/handler/admin/account_codex_import.go`
- `frontend/src/utils/oauthAffiliate.ts`
- `frontend/src/components/account/OAuthAuthorizationFlow.vue`
- `frontend/src/components/account/CreateAccountModal.vue`
- `frontend/src/api/admin/accounts.ts`
- `backend/migrations/122_pending_auth_completion_token_cleanup.sql`
- `backend/migrations/135_allow_email_oauth_provider_types.sql`

HUAKAI internal files read:

- `docs/03_FEATURE_PARITY_MATRIX.md`
- `docs/specs/upstream-credential-management.md`
- `backend/sql/migrations/0016_account_credentials.up.sql`
- `backend/internal/credentialstore/types.go`
- `backend/internal/credentialstore/postgres_store.go`
- `backend/internal/credentialworker/scheduler.go`
- `backend/internal/credentialworker/mode_refresh.go`
- `backend/internal/credentialworker/adapters/openai.go`
- `backend/internal/credentialworker/adapters/gemini.go`
- `backend/internal/credentialworker/adapters/anthropic.go`
- `backend/internal/db/models.go`
- `backend/cmd/gateway/main.go`
- `backend/internal/gatewayhttp/admin_credentials_handler.go`
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`
- `docs/openapi/openapi.yaml`

## Owner 中文摘要

本计划真实观察到：Sub2API 有浏览器 OAuth、Claude cookie/session bootstrap、Codex auth 内容导入、Vertex SA JSON、Bedrock 手动凭据、Antigravity 特殊 OAuth、refresh-token 轮换保护等机制；合理推断是 HUAKAI 应把这些拆成一个自研 `credential acquisition` session 层，再统一落到 F-AUTH-005 的 15 mode 加密存储与 refresh worker；open questions 共 6 个，最高优先级是 DB schema OCAW、OAuth client 合规边界、CLI 本地文件读取边界、以及 F-AUTH-006 是否并入 F-CRED-001。没有功能缩水；clean-room 风险通过 SPECIFIER lane、逐条 source citation、禁止复制代码/字段结构来控制；安全风险集中在 token 泄漏、callback replay、云 bootstrap 滥用，均已进入测试计划和 OCAW。

Source files read: backend/internal/repository/claude_oauth_service.go; backend/internal/repository/openai_oauth_service.go; backend/internal/repository/gemini_oauth_client.go; backend/internal/service/openai_oauth_service.go; backend/internal/service/gemini_oauth.go; backend/internal/service/gemini_oauth_service.go; backend/internal/service/oauth_service.go; backend/internal/service/token_refresh_service.go; backend/internal/service/token_refresh_service_test.go; backend/internal/service/auth_oauth_email_flow.go; backend/internal/service/antigravity_oauth_service.go; backend/internal/service/antigravity_token_refresher.go; backend/internal/service/bedrock_signer.go; backend/internal/service/vertex_service_account.go; backend/internal/pkg/geminicli/oauth.go; backend/internal/handler/admin/account_handler.go; backend/internal/handler/admin/gemini_oauth_handler.go; backend/internal/handler/admin/account_codex_import.go; frontend/src/utils/oauthAffiliate.ts; frontend/src/components/account/OAuthAuthorizationFlow.vue; frontend/src/components/account/CreateAccountModal.vue; frontend/src/api/admin/accounts.ts; backend/migrations/122_pending_auth_completion_token_cleanup.sql; backend/migrations/135_allow_email_oauth_provider_types.sql
Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-15T17:39:58Z
