# 2026-05-16 F-AUTH-007 + F-SESSION-001 Implementation

| Field | Value |
| --- | --- |
| Owner directive | "你是 HUAKAI 项目 codex executor lane, 任务 = F-AUTH-007 + F-SESSION-001 真代码实施 (按 commit 084e9c9 spec)." |
| Scope | Add PostgreSQL migrations 0020/0021, implement `backend/internal/userauth`, implement `backend/internal/usersession`, add `gatewayhttp` auth/session handlers, update OpenAPI, and run requested Go tests. |
| Out of scope | Reference-project source reads; F-AUTH-005/F-CRED-001 runtime paths; billing, quota, `LICENSE`, Rust `core_gateway`, production secrets, and destructive migrations beyond reversible new-table DDL. |
| Success criteria | Tenant-scoped users, invite codes, email/password flows, neutral social-login bridge, session families, refresh-token rotation, revocation, anomaly checks, HTTP endpoints, OpenAPI paths, and target tests compile/pass or report concrete blockers. |
| Time estimate | 1 Codex implementation session for a vertical skeleton; production hardening remains follow-up Phase 6 work. |
| Blast radius | Medium: new auth/session code and migrations are additive, but route wiring touches gateway startup and OpenAPI. Failure could break compile or expose incomplete handler behavior. |
| Failure modes | Schema references missing local tables; accidental overlap with upstream credential auth; raw token storage/logging; weak tenant checks; racey refresh rotation; OpenAPI drift; adding dependencies unnecessarily. |
| Mitigations | Use only HUAKAI specs/current code; keep `tenant_id` required in every auth/session record and request; store token hashes only; use transactions and advisory locks for invite/refresh races; avoid new dependencies; keep routes behind explicit deps. |
| Decision points | Owner may later choose email delivery provider, production OAuth provider clients, default registration policy, session lifetimes, external cache, and anomaly thresholds. |
| Pre-execution checklist | Read 084e9c9 specs; inspect current migrations, db interface, gateway route wiring, handler helper conventions, and Go module dependencies; confirm no reference-project source reads. |

## Concrete Execution Order

1. Add reversible `0020_user_authentication` and `0021_session_management` migrations.
2. Implement `userauth` data types, password hashing, token hashing, invite redemption with advisory lock, email verification/reset flows, neutral OAuth bridge, and PostgreSQL store.
3. Implement `usersession` data types, PostgreSQL store with short in-memory fallback cache, refresh rotation, revocation, and IP/UA drift classification.
4. Add HTTP handlers under `/v1/auth/*` and `/v1/sessions/*` with dependency structs so tests can use in-memory fakes.
5. Wire production routes only when gateway has PostgreSQL dependencies available.
6. Add OpenAPI path definitions for the eight requested endpoints.
7. Add focused tests and run `go test ./internal/userauth ./internal/usersession ./internal/gatewayhttp -race -count=1 -run 'Auth|Session'`.
8. Report `git status`, diff stat, and test result.

Source files read: docs/specs/user-authentication.md@084e9c9; docs/specs/session-management.md@084e9c9; docs/process/plans/2026-05-16-user-auth-session-spec-codex.md; backend/go.mod; backend/sql/migrations/0001_pool_routing.up.sql; backend/sql/migrations/0007_l0_inbound_auth.up.sql; backend/sql/migrations/0010_admin_auth.up.sql; backend/sql/migrations/0019_credential_acquisition_flow_sessions.up.sql; backend/sql/migrations/0019_credential_acquisition_flow_sessions.down.sql; backend/internal/credentialacq/refresh_lock.go; backend/internal/credentialacq/session_store.go; backend/internal/gatewayhttp/admin_credential_acquisition_handler.go; backend/internal/gatewayhttp/admin_pool_accounts_handler.go; backend/internal/gatewayhttp/chat_completions_handler.go; backend/internal/gatewayhttp/audit_verify_handler.go; backend/cmd/gateway/main.go; docs/openapi/openapi.yaml
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-16T08:02:21Z
