# 2026-05-18 F-COMM-001 Phase 1 Codex Plan

| Owner directive | "任务 = F-COMM-001 impl Phase 1: 4 数据表 + invitation code 生成 + 1 endpoint (POST /v1/invitations, 估时 2-3 天)." |
| Scope | In: Go backend migration, invitation package, authenticated create endpoint, focused tests, OpenAPI entry. Out: frontend, Rust, vendor/boring, control plane, redeem/register flow, reward issuance, billing/auth core edits. |
| Success criteria | Four community tables migrate cleanly; invitation code generation uses crypto randomness and Crockford base32; tenant monthly quota blocks creation after 100 rows; `POST /v1/invitations` requires session auth and returns the requested response contract; requested build/test commands pass or any blocker is reported truthfully. |
| Time estimate | 2-3 calendar days in the original task; one Codex implementation pass for this execution lane. |
| Blast radius | Backend migration order, gateway HTTP routing, session-auth surface, OpenAPI contract, invitation package tests. Existing billing, quota, auth registration, Rust gateway, and frontend remain untouched. |
| Failure modes | Duplicate migration number: use the next available migration number because `0032` and `0033` already exist. Clean-room leakage from reference projects: read only behavior evidence, avoid upstream identifiers/schema/function names, and cite read regions. Auth context mismatch: follow existing `backend/internal/auth/session_middleware.go` and gateway handler patterns. Race/flaky randomness tests: assert format/uniqueness over bounded samples, not deterministic codes. Database availability: keep package tests unit-level where possible and run requested Go tests; report infra blockers. |
| Decision points | Owner has already authorized the schema work by providing the four-table migration body. No new runtime dependency will be added. Any need to edit auth core, billing ledger, quota enforcement outside this feature package, production secrets, or destructive migrations would require Owner confirmation before acting. |
| Pre-execution checklist | Read `docs/RULES.md`; read `docs/specs/community-invitation-referral.md`; inspect session middleware and gateway routing; confirm migration sequence; read only the allowed reference source region under clean-room constraints; implement using stdlib and existing dependencies; add focused tests; update OpenAPI; run requested commands. |

## Concrete Execution Order

1. Inspect current backend auth, routing, migrations, test helpers, and OpenAPI layout.
2. Read the allowed reference source file only as behavior evidence and record repo SHA plus line anchors without copying names, schemas, or code.
3. Add community invitation/referral migration using the next free migration number.
4. Add `backend/internal/community/invitation` with types, code generation, quota, pgx-backed storage, and service orchestration.
5. Add `POST /v1/invitations` handler and wire it into `cmd/gateway/main.go`.
6. Add package and handler tests mapped to the requested AT-COMM cases.
7. Update `docs/openapi/openapi.yaml`.
8. Run requested `go build` and `go test` commands, then summarize changed files, verification, clean-room status, and residual risks.
