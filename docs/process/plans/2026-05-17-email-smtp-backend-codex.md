# 2026-05-17 Email SMTP Backend Codex Plan

| Field | Value |
| --- | --- |
| Owner directive | "任务 = Email SMTP backend 实施 (sub2api 设计 + HUAKAI dev-mode + release gate)" |
| Scope | In: tenant-scoped email settings migration, Go `internal/email` infrastructure, admin email settings/test endpoints, auth HTTP dev-mode token exposure, gateway wiring, OpenAPI sync, focused tests. Out: frontend, Rust core gateway, billing, quota, provider account rename, pool dispatcher, mimicry, spec-wave rewrites, non-Sub2API reference source. |
| Success criteria | SMTP sender is no longer noop by default; unconfigured backend fails explicitly; production release mode fails fast unless required tenant email settings and verification toggle are present; admin can view/update/test settings with password masked/encrypted; dev-mode responses can return raw one-time tokens with warning logs; tests cover unconfigured, tenant isolation, encryption, header sanitization, release gate, and dev token behavior. |
| Time estimate | 1 focused implementation pass plus test/fix loop; expected wall time 1.5-3h depending on existing gatewayhttp compile state. |
| Blast radius | Backend auth registration/reset delivery path, admin HTTP route surface, migration set, OpenAPI contract. No billing/quota/auth-core schema beyond additive email settings table. |
| Failure modes | SMTP networking is hard to test deterministically, so sender gets an injectable transport seam and tests validate message construction/sanitization without external SMTP. Migration could diverge from sqlc patterns, so email store uses local pgx interfaces and focused SQL. Admin endpoints could leak secrets, so password is omitted/masked on reads and unchanged when omitted on update. Release gate could block dev boots, so it only runs when `HUAKAI_RELEASE_MODE=production`. |
| Decision points | No high-risk Owner decision expected because work is additive. If a required change touches auth core, billing, quota, DB destructive migration, or runtime dependency addition, stop for Owner confirmation. |
| Pre-execution checklist | Read `docs/RULES.md`; read `docs/specs/user-authentication.md`; read only the two Owner-authorized Sub2API source files for behavior, paraphrased; inspect current auth handler, gateway wiring, credentialstore cipher, admin handler conventions, migration naming, and OpenAPI anchors; preserve existing dirty worktree changes. |
| Concrete execution order | Add migration 0025 up/down; add `backend/internal/email` types, settings store, sender, factory and tests; add gatewayhttp admin email settings handler and tests; wire main deps and release gate; add dev-mode response behavior in auth handler; update OpenAPI; run focused then broad backend tests; report clean-room identifiers and source tail. |

## Clean-Room Guard Notes

The Owner task explicitly authorizes this Codex session to read the two Sub2API service files and then implement HUAKAI code. This is a narrow exception to the normal lane separation rule. Mitigations:

- Treat upstream as behavior evidence only.
- Do not copy upstream struct/interface/error/function names into HUAKAI code.
- Do not copy comments, HTML templates, tests, or implementation ordering.
- Keep HUAKAI schema tenant-scoped and independently named.
- Do not read other reference-project source.

Source files read for planning: `docs/RULES.md`; `docs/specs/user-authentication.md`; `/home/codex/refs/sub2api/backend/internal/service/email_service.go`; `/home/codex/refs/sub2api/backend/internal/service/setting_service.go`; `backend/cmd/gateway/main.go`; `backend/internal/gatewayhttp/auth_handler.go`; `backend/internal/userauth/service.go`; `backend/internal/userauth/types.go`; `backend/internal/credentialstore/crypto.go`; `backend/internal/gatewayhttp/admin_pool_accounts_handler.go`; `backend/internal/gatewayhttp/admin_credentials_handler.go`; `backend/sql/migrations/0001_pool_routing.up.sql`; `backend/sql/migrations/0020_user_authentication.up.sql`; `docs/openapi/openapi.yaml`.

Lane: specifier (limited source read) + implementer (HUAKAI code)
Agent: Codex GPT-5
UTC timestamp: 2026-05-17T00:00:00Z
