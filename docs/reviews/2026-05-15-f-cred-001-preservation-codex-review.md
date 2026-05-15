# 2026-05-15 F-CRED-001 acquisition preservation review

Lane: REVIEWER  
Agent: Codex / GPT-5  
sub2api HEAD reviewed: `dbc8ae658cfc1c012160752582925e45115e2f3a`  
Verdict: **BLOCK** for F-CRED-001 implementation until the red flags below are added to the synthesized plan or explicitly mapped to Safe Equivalent / Mandatory Roadmap. F-AUTH-005 commit `6262551` is a valid 15-mode credential-storage foundation, but it does **not** cover sub2api acquisition behavior or all mode-specific refresh semantics.

Metadata: Observed regions: 34 cited source regions. Inferences: 6. Open questions: 4.

## scope (files reviewed)

HUAKAI plans reviewed:

- `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md`
- `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md`
- `docs/plans/2026-05-15-f-auth-005-credential-mgmt-claude.md`
- `docs/plans/2026-05-15-f-auth-005-credential-mgmt-codex.md`
- `docs/reviews/2026-05-15-round2-cross-discuss-synthesis.md`

HUAKAI F-AUTH-005 implementation reviewed at commit `6262551`:

- `backend/sql/migrations/0016_account_credentials.up.sql`
- `backend/internal/credentialstore/*`
- `backend/internal/credentialworker/*`
- `backend/internal/gatewayhttp/admin_credentials_handler.go`
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`
- `backend/internal/provider/postgres_vault.go`
- `backend/cmd/gateway/main.go`

sub2api acquisition / credential source reviewed:

- Requested service files for OpenAI, Gemini, Antigravity, token refresh, Gemini token refresh, auth email flow.
- Requested repository files for Claude, OpenAI, Gemini, and refresh-token cache.
- Requested migrations `122` and `135`.
- The requested path `backend/internal/service/claude_oauth_service.go` does not exist in this checkout. The service-equivalent Claude/Anthropic behavior is in `backend/internal/service/oauth_service.go`; the HTTP client side is in `backend/internal/repository/claude_oauth_service.go`.
- Adjacent refresh files were also read where needed to avoid false conclusions: `backend/internal/service/oauth_refresh_api.go`, `backend/internal/service/antigravity_token_refresher.go`, and `backend/internal/service/refresh_token_cache.go`.

Validation attempted:

- `go test ./internal/credentialstore ./internal/credentialworker ./internal/gatewayhttp` from `backend/` failed before test execution because the Go build cache / temp work dir hit `disk quota exceeded`. This is an environment limit, not a passing or failing assertion result.

## sub2api feature inventory (categorized)

| Category | sub2api observed capability | Evidence | Preservation state |
| --- | --- | --- | --- |
| OAuth session core | Browser OAuth start stores state/PKCE/proxy/redirect/session and returns an authorization URL for OpenAI, Gemini, and Antigravity. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:101`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:32` | PRESERVED-WITH-UPGRADE by Codex F-CRED plan: one provider-neutral acquisition session and finalizer into F-AUTH-005. |
| OAuth callback core | Callback reloads flow session, verifies state, exchanges code, deletes session, and maps returned credential material. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:131`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:445`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:98` | PRESERVED-WITH-UPGRADE if F-CRED keeps hashed state, idempotent finalize, and encrypted sink from `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:234`. |
| Claude/Anthropic acquisition | Browser flow supports different scopes; cookie/session bootstrap can obtain authorization material and fill org/account metadata. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:50`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174` | PARTIAL. F-CRED plans mention Claude browser/cookie bootstrap, but not the team-org selection and long-lived setup-token behavior. |
| OpenAI ChatGPT metadata | After acquisition / refresh, account email, plan metadata, subscription expiry, and privacy/training preference are fetched or updated best-effort. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:255`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:276` | MISSING from implementable F-CRED/F-AUTH acceptance criteria. |
| OpenAI refresh edge behavior | Refresh supports explicit client identity; account-level refresh can fall back to existing access material when refresh material is absent; credential build preserves prior refresh material if a new one is absent. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:209`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:286`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331` | PARTIAL. Generic F-AUTH refresh exists, but mode-specific OpenAI edge cases are not encoded. |
| Gemini OAuth client/tier model | Gemini distinguishes built-in vs custom OAuth client paths, validates OAuth type, normalizes legacy tier labels, and derives Google One tier from Drive quota data. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:83`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:211`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:363` | PARTIAL. Plans mention project/tier, but not legacy tier canonicalization, client mismatch fallback, or Drive-tier refresh cache. |
| Gemini project discovery | Code Assist / Google One flows discover project metadata, fall back through project listing / registration paths, and may attempt onboarding before failing. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:531`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:922` | PARTIAL. Codex F-CRED plan cites this region, but the exact fallback/onboarding behavior is not acceptance-tested. |
| Gemini refresh semantics | Refresh retries transient failures with bounded backoff, stops on permanent token errors, retries with an alternate client type on specific client mismatch, and caches Google One tier for 24 hours. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:675`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:733` | MISSING from F-AUTH-005 implementation; must be planned before claiming Gemini parity. |
| Antigravity acquisition | Antigravity has a separate OAuth path, fetches account email, discovers project/plan with retry, applies privacy preference, and supports refresh-token-only validation. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:98`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:214`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:337` | PARTIAL in F-CRED plan, MISSING in F-AUTH implementation. Current F-AUTH adapter routes it through generic Gemini refresh. |
| Antigravity refresh | Refresh classifies permanent vs transient failures, preserves old project metadata if new lookup fails, and can fill project metadata without token refresh. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:170`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:277`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:442` | MISSING in F-AUTH-005 mode adapter. |
| Common refresh safety | Refresh path uses local/distributed locking, rereads before write, merges returned credential material, and recovers some race cases by rereading. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:32`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:102`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:195` | PARTIAL. F-AUTH has encrypted CAS writeback, but not all race/recovery semantics are visible in commit `6262551`. |
| User auth refresh-token cache | Separate user-session refresh-token cache stores hashed refresh tokens, supports per-token, per-family, and per-user invalidation. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/refresh_token_cache.go:13`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/refresh_token_cache.go:13` | OUT OF SCOPE for F-CRED-001 / F-AUTH-005, but must be explicitly mapped to HUAKAI auth/session features, not silently dropped. |
| OAuth email/local account flow | Third-party email flow can require verification/invitation, create local user, roll back token-generation failure, update signup source, and record login. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:30`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:101`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:255`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:286` | OUT OF SCOPE for upstream-provider credential acquisition, but must map to HUAKAI user-auth roadmap if not already covered. |
| Flow cleanup migration lesson | One migration strips token-shaped values from pending auth completion state; another broadens user auth provider type constraints. | `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/122_pending_auth_completion_token_cleanup.sql:1`; `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/135_allow_email_oauth_provider_types.sql:1` | PRESERVED-WITH-UPGRADE if F-CRED table forbids raw provider tokens after completion and HUAKAI keeps explicit 15-mode enum. |

## PRESERVED features (with HUAKAI delta)

1. **15-mode credential lattice is structurally preserved.** F-AUTH-005 commit `6262551` has local mode constants covering the 15 Owner-survey modes at `backend/internal/credentialstore/types.go:18`, validates those combinations in `backend/internal/credentialstore/types.go:238`, and enforces the same closed vendor/mode set in `backend/sql/migrations/0016_account_credentials.up.sql:49`.

2. **Encrypted credential sink is preserved and stronger than sub2api storage shape.** HUAKAI uses `account_credentials` with encrypted payload, key id, nonce, AAD hash, fingerprints, refresh timestamps, and state machine fields at `backend/sql/migrations/0016_account_credentials.up.sql:9`; AES-GCM seal/open is implemented at `backend/internal/credentialstore/crypto.go:120` and `backend/internal/credentialstore/crypto.go:154`; create/rotate/refresh writebacks go through the store at `backend/internal/credentialstore/postgres_store.go:120`, `backend/internal/credentialstore/postgres_store.go:180`, and `backend/internal/credentialstore/postgres_store.go:424`.

3. **Browser OAuth flow shape is preserved as a planned provider-neutral acquisition service.** Codex F-CRED plan maps browser OAuth start/callback/session evidence into one acquisition-session layer and an encrypted finalizer (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:234` and `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:248`).

4. **Manual credential entry paths are preserved.** API key, refresh token paste, Bedrock manual credential, Vertex/service-account upload, and AI Studio API key are all represented in Codex F-CRED 15-row acquisition table (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:148`) and F-AUTH handler validation (`backend/internal/credentialstore/types.go:240`).

5. **Refresh ownership boundary is preserved.** Codex F-CRED plan correctly keeps ongoing refresh in F-AUTH-005 and makes acquisition initialize credential material only (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:237`). F-AUTH commit wires the credential-aware scheduler to scan `account_credentials` at `backend/internal/credentialworker/mode_refresh.go:171` and mount the credential refresher in `backend/cmd/gateway/main.go:329`.

6. **CLI import is preserved as a safer equivalent.** Codex F-CRED plan preserves Codex/Claude CLI import through uploaded or pasted content and explicitly avoids default server-side workstation file reads (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:216`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:238`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:251`). This is the safer plan than Claude's initial local auto-detect idea (`docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:23` and `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:28`).

7. **Pending-flow token cleanup lesson is preserved at plan level.** Codex F-CRED plan says acquisition session data must store only redacted metadata after completion, mapped from the sub2api cleanup migration evidence (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:242`).

## PRESERVED-WITH-UPGRADE features

1. **Scattered provider flows -> one acquisition wizard and registry.** sub2api has provider-specific acquisition services; HUAKAI plans one "获取凭据" wizard and deterministic `(vendor, auth_mode, flow_kind)` adapter selection (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:211`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:248`).

2. **Plain acquisition result -> encrypted finalizer + audit chain.** HUAKAI finalizer writes to F-AUTH-005 encrypted `account_credentials`, and F-CRED plan adds acquisition audit without token-shaped payloads (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:222`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:250`).

3. **Local CLI file convenience -> safe upload/paste by default.** Claude plan's local auto-detect is useful but unsafe as a default (`docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:23`, `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:24`, `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:25`). Codex plan's OCAW makes this an explicit Owner decision and recommends upload/paste only (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:339`).

4. **Cloud manual paths -> manual-first plus controlled bootstrap.** Codex plan preserves manual Bedrock / Vertex / Azure paths and gates deeper STS / managed identity / service-account import behind Owner OCAW (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:162`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:165`, `docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:338`).

5. **Refresh worker after save -> optional first-validity check.** Claude plan proposes first refresh / validity verification inside acquisition callback before returning success (`docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:46`). This is a useful upgrade, but it must be reconciled with Codex's boundary that acquisition initializes and F-AUTH refresh owns ongoing rotation.

## MISSING features (RED FLAG - must add to HUAKAI plan before impl)

**RF-1 HIGH: OpenAI post-acquisition metadata and privacy handling is not preserved as a testable F-CRED requirement.** sub2api fetches account/plan/email metadata and attempts privacy/training preference update after token acquisition or refresh (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:255`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:276`). HUAKAI plans list `chatgpt_oauth`, but do not require storing plan/subscription/email metadata or recording privacy outcome. Add an F-CRED acceptance test and F-AUTH metadata field policy.

**RF-2 HIGH: Gemini tier canonicalization, Drive-derived subscription tier, and 24h tier refresh cache are only high-level in plans.** sub2api normalizes legacy/current tier labels, derives Google One tier from Drive quota, and refreshes cached tier metadata conditionally (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:211`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:363`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:733`). Codex plan cites project/tier broadly, but AT-CRED does not assert canonical labels, stale-cache refresh, or UI/rate-limit behavior.

**RF-3 HIGH: Gemini Code Assist / Google One project discovery fallback is not decomposed into acceptance criteria.** sub2api has multiple fallback/onboarding branches before deciding project metadata is unavailable (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:531`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:922`). F-CRED `AT-CRED-001-009` only says project metadata is stored redacted; it does not require the fallback matrix (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:283`).

**RF-4 HIGH: Antigravity must not be treated as generic Gemini.** sub2api has a dedicated Antigravity acquisition/refresh path with project/plan retry, privacy handling, refresh-token-only validation, and project-fill helper (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:98`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:214`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:442`). F-AUTH commit registers Antigravity through generic Gemini refresh (`backend/internal/credentialworker/mode_refresh.go:66` through `backend/internal/credentialworker/mode_refresh.go:68`). Add a dedicated mode adapter or explicitly mark Safe Equivalent with matching metadata behavior.

**RF-5 MED: Claude/Anthropic cookie bootstrap details are under-specified.** sub2api prefers a team org when available and handles setup-token style exchange differently (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174`). F-CRED plans mention cookie/session bootstrap but do not preserve team-org preference, setup-token expiry semantics, or tests for missing/ambiguous org metadata.

**RF-6 MED: OpenAI refresh edge cases are absent from F-AUTH adapter coverage.** sub2api can preserve old refresh material, use explicit client identity, and refresh under an account-state condition even if expiry metadata is missing (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:209`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresher.go:92`). F-AUTH current OpenAI modes route to a generic OAuth refresh adapter (`backend/internal/credentialworker/mode_refresh.go:60`, `backend/internal/credentialworker/mode_refresh.go:63`).

**RF-7 MED: Common refresh race protection is weaker than sub2api unless F-AUTH adds reread/recovery semantics.** sub2api's common path locks, rereads, merges credentials, and has invalid-grant race recovery (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:32`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:102`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:195`). F-AUTH has encrypted CAS writes, but scheduler storm control is still account-scoped (`backend/internal/credentialworker/scheduler.go:176`) and mode refresh does not show the same recovery branch.

**RF-8 MED: User auth refresh-token cache and OAuth email local-account flow are not F-CRED, but still need project-level preservation mapping.** These are credential-related files requested by Owner, and sub2api implements hashed user refresh-token invalidation plus email/invitation/rollback logic (`sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/refresh_token_cache.go:13`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:101`, `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:286`). Mark them explicitly as F-AUTH/session roadmap or implemented-equivalent elsewhere; otherwise they become silent-drop risk.

**RF-9 LOW/MED: Claude plan and Codex plan conflict on local file auto-detect.** Claude plan proposes reading local CLI/cloud auth files for convenience (`docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:23` through `docs/plans/2026-05-15-f-cred-001-acquisition-claude.md:32`); Codex plan makes upload/paste default and pushes local-agent access to OCAW (`docs/plans/2026-05-15-f-cred-001-acquisition-codex.md:339`). Use Codex's safe boundary unless Owner explicitly approves a local-agent connector.

## F-AUTH-005 6262551 implementation review (mode-by-mode coverage)

Overall: commit `6262551` covers the **existence** of all 15 modes in schema, registry, admin routes, encrypted storage, and refresh adapter registry. It does not cover all sub2api acquisition behavior, and several modes are only placeholder/static/mock-equivalent refresh paths.

Evidence for broad coverage:

- 15 local auth-mode constants: `backend/internal/credentialstore/types.go:18`.
- 15 credential validation handlers: `backend/internal/credentialstore/types.go:238`.
- DB closed set for 3 vendors x 5 modes: `backend/sql/migrations/0016_account_credentials.up.sql:49`.
- Mode refresh registry has 15 entries: `backend/internal/credentialworker/mode_refresh.go:49`.
- Admin credential CRUD routes exist: `backend/internal/gatewayhttp/admin_credentials_handler.go:49`.
- Provider vault prefers credential v2 and falls back to legacy JSON: `backend/internal/provider/postgres_vault.go:43`.
- Tests verify handler/adapter count, but not mode-specific behavior: `backend/internal/credentialstore/types_test.go:8`, `backend/internal/credentialworker/mode_refresh_test.go:15`.

| Mode | F-AUTH-005 coverage | Reviewer verdict |
| --- | --- | --- |
| `anthropic/api_key` | Required API-key field, encrypted storage, static/no refresh. | COVERED. |
| `anthropic/claude_ai_oauth` | Generic Anthropic OAuth refresh adapter. | COVERED-WEAK: no team-org preference, setup-token behavior, or acquisition bootstrap. |
| `anthropic/claude_code` | Session/token payload shape and generic Anthropic refresh. | COVERED-WEAK: no CLI import/acquisition and no explicit legal/policy gate in this commit. |
| `anthropic/bedrock` | AWS key/secret/region payload and no refresh. | COVERED-WEAK: manual SigV4 storage only; no STS bootstrap/rotation. |
| `anthropic/vertex_anthropic` | Metadata/mock token endpoint style refresh. | COVERED-WEAK: no service-account JWT/token exchange semantics. |
| `openai/api_key` | Required API-key field, encrypted storage, static/no refresh. | COVERED. |
| `openai/chatgpt_oauth` | Generic OpenAI refresh adapter. | COVERED-WEAK: missing ChatGPT plan/email/privacy metadata. |
| `openai/codex_cli_oauth` | Generic Codex/OpenAI refresh adapter. | COVERED-WEAK: no acquisition/import in commit; F-CRED must provide it. |
| `openai/azure` | API/access/mock token payload; mock token endpoint adapter. | COVERED-WEAK: `backend/internal/credentialworker/mode_refresh.go:231` is mock endpoint only, not real Azure client credential / managed identity. |
| `openai/refresh_token` | Generic OpenAI refresh token path. | COVERED-WEAK: no endpoint allowlist/client-id preservation/race fallback tests. |
| `gemini/aistudio_api_key` | Required API-key field, encrypted storage, static/no refresh. | COVERED. |
| `gemini/vertex_sa` | Metadata/mock token endpoint style refresh. | COVERED-WEAK: `backend/internal/credentialworker/mode_refresh.go:260` does not implement full service-account exchange. |
| `gemini/code_assist` | Generic Gemini refresh adapter. | COVERED-WEAK: missing project discovery, tier defaulting, client mismatch fallback. |
| `gemini/google_one` | Generic Gemini refresh adapter. | COVERED-WEAK: missing Drive quota tier and 24h metadata refresh cache. |
| `gemini/antigravity` | Registered through generic Gemini refresh adapter. | MISSING-SPECIFIC: sub2api behavior is Antigravity-specific, so generic Gemini handling is weakened. |

Testing gap:

- Current tests count mode names and cover a few representative material mappings, mock Azure token exchange, and metadata endpoint requests (`backend/internal/credentialstore/types_test.go:8`, `backend/internal/credentialworker/mode_refresh_test.go:28`, `backend/internal/credentialworker/mode_refresh_test.go:55`). They do not assert 15 mode-specific refresh/acquisition semantics or the sub2api edge cases listed above.

## ship recommendation (BLOCK / APPROVE_WITH_CHANGES / APPROVE)

**F-CRED-001 recommendation: BLOCK.** Do not implement from the current plans until RF-1 through RF-8 are either added to the synthesized F-CRED/F-AUTH plan with acceptance tests or explicitly mapped as Safe Equivalent / Mandatory Roadmap. This is a Feature Preservation Rule blocker because several sub2api acquisition features are currently easy to silently drop.

**F-AUTH-005 commit `6262551` recommendation: APPROVE_WITH_CHANGES as storage foundation only.** It can stand as the 15-mode encrypted credential-management base, but it must not be described as full sub2api acquisition/refresh parity. Before shipping a parity claim, add mode-specific adapters/tests for OpenAI ChatGPT metadata/privacy, Gemini project/tier/client fallback, Antigravity dedicated behavior, Claude org/setup bootstrap details, and refresh race recovery.

Decision points for Owner:

1. Decide whether OpenAI privacy/training update is a default behavior, opt-in tenant setting, or forbidden/Manual First feature.
2. Decide whether local CLI/cloud auto-detect is allowed only via explicit local-agent connector, rather than server-side file reads.
3. Decide whether user auth refresh-token cache and OAuth email flow belong to a separate F-AUTH roadmap item or are already covered elsewhere.
4. Decide cloud bootstrap depth for Azure/Bedrock/Vertex before writing real SDK/token-exchange code.

## sources read (sub2api citations)

Primary cited sub2api files:

- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:45`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:131`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:209`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:255`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:276`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:286`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/openai_oauth_service.go:331`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/openai_oauth_service.go:74`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:83`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:101`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:211`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:363`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:445`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:531`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:675`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:733`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_oauth_service.go:922`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/gemini_oauth_client.go:28`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:32`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:98`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:170`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:214`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:277`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:337`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/antigravity_oauth_service.go:442`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:50`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_service.go:161`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:35`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/claude_oauth_service.go:174`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/token_refresher.go:92`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/gemini_token_refresher.go:17`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:32`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:102`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/oauth_refresh_api.go:195`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/refresh_token_cache.go:13`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/repository/refresh_token_cache.go:13`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:30`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:101`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:255`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/internal/service/auth_oauth_email_flow.go:286`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/122_pending_auth_completion_token_cleanup.sql:1`
- `sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a:backend/migrations/135_allow_email_oauth_provider_types.sql:1`

Source files read: backend/internal/service/openai_oauth_service.go; backend/internal/service/gemini_oauth_service.go; backend/internal/service/antigravity_oauth_service.go; backend/internal/service/oauth_service.go; backend/internal/service/token_refresher.go; backend/internal/service/gemini_token_refresher.go; backend/internal/service/oauth_refresh_api.go; backend/internal/service/antigravity_token_refresher.go; backend/internal/service/refresh_token_cache.go; backend/internal/repository/claude_oauth_service.go; backend/internal/repository/openai_oauth_service.go; backend/internal/repository/gemini_oauth_client.go; backend/internal/repository/refresh_token_cache.go; backend/internal/service/auth_oauth_email_flow.go; backend/migrations/122_pending_auth_completion_token_cleanup.sql; backend/migrations/135_allow_email_oauth_provider_types.sql

Lane: reviewer  
Agent: Codex / GPT-5  
UTC timestamp: 2026-05-15T18:10:00Z

Owner 中文摘要：本次 reviewer lane 真实观察到 sub2api 的 OAuth 获取、CLI/手动获取、Gemini tier/project、Antigravity 专用逻辑、OpenAI 元数据/隐私、Claude cookie bootstrap、refresh race 保护、用户 refresh-token cache 与 email OAuth 本地账号流程；合理推断是 HUAKAI F-AUTH-005 已经具备 15 mode 加密存储骨架，但还不能声称完整 acquisition/refresh parity；open question 4 个，最高优先级是把 RF-1 至 RF-8 写入 synthesized plan 或 Mandatory Roadmap。结论是 F-CRED-001 当前应 BLOCK，F-AUTH-005 仅可作为 storage foundation APPROVE_WITH_CHANGES；没有建议功能缩水，clean-room 风险通过行为摘要和 file:line citation 控制，安全风险集中在本地文件读取、隐私设置、token 泄漏、refresh race 和 cloud bootstrap 权限边界。
