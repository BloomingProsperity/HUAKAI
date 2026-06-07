# 2026-06-07 AUTH-182 validate invitation code endpoint

| Owner directive | `模块A闭环 — 独立 validate-invite 端点(AUTH-182,注册前只读校验邀请码,不消耗)` |
| Scope | Add a HUAKAI-internal, clean-room, read-only pre-registration invite validation path. In scope: `internal/userauth` read-only status method, new `internal/invitevalidatehttp` package, additive route wiring, rate-limit path registration, OpenAPI contract, focused unit/integration tests. Out of scope: schema/migration, frozen-package new files, reference-source reading, git commit. |
| Success criteria | `POST /v1/auth/validate-invitation-code` returns `{valid,reason}` without consuming invite usage; `InviteCodeStatus` performs only `SELECT`; route is public, JSON-only, no email/user/creator leakage, gated by `platformsettings.KeyInvitationRequired`, and OpenAPI consistency remains green. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation session. |
| Blast radius | Public auth route surface, auth strict rate-limit path table, userauth store query, OpenAPI path list. No database schema, no auth core mutation, no billing/quota/payment impact. |
| Failure modes | Accidentally using `RedeemInvite` would consume `used_count`; mitigate with TDD integration_pg test checking two validations and `used_count=0`. Missing rate-limit path would leave validation unprotected; mitigate with `cmd/gateway` rate-limit test. Missing OpenAPI path would break `TestOpenAPI_ImplementationConsistency`; update spec. Leaking metadata would violate anti-enumeration; response only contains coarse status reason. |
| Decision points | None expected. Stop for Owner only if implementation requires schema, auth core, billing/quota/payment, destructive operations, or adding a runtime dependency. |
| Pre-execution checklist | 1. Read current `RedeemInvite`/`redeemAuthInviteWithDB` behavior and invite schema. 2. Confirm platform setting default/toggle access. 3. Confirm existing auth route and rate-limit wiring. 4. Write failing tests before production code. 5. Implement minimal read-only status method and handler package. 6. Wire route additively outside frozen packages. 7. Update OpenAPI. 8. Run requested build/vet/test commands; leave `integration_pg` for PM unless local DB is available. |

## Clean-Room Note

No reference project source will be read. The behavior target is taken from the Owner prompt only: public `POST /validate-invitation-code`-style validation, per-IP limited, returning `{valid,...}`, and not consuming invite usage.

## Concrete Execution Order

1. Add/extend `internal/userauth` tests for read-only invite status and no `UPDATE`/advisory lock path.
2. Add `InviteCodeStatus` enum and `PostgresStore.InviteCodeStatus(ctx, tenantID, rawCode)` with a single `SELECT` by tenant and hashed code.
3. Create `internal/invitevalidatehttp` handler and tests for request validation, platform `invitation_required` gate, status mapping, and metadata-safe JSON response.
4. Add `cmd/gateway/routes_invitevalidate.go` helper and call it from the existing `/v1/auth` route block.
5. Add the new path to the register auth-strict rate-limit class and test shared register bucket behavior.
6. Add OpenAPI path and request/response schemas.
7. Run requested verification commands from `backend/`.
