# 2026-06-03 audit001-cost-disputes Codex Plan

| Owner directive | "实现+验证。先读 CLAUDE.md + AGENTS.md。设计依据:读 /tmp/verify-audit001.log... 任务:用户成本争议 API... 纯记录 + 状态流转,绝不触发退款、绝不改账本... migration 编号用 0084" |
| Scope | In: `cost_disputes` schema/query, `internal/audit` dispute store, new `internal/disputehttp` handlers, route wiring in `cmd/gateway/routes.go`, focused discriminating tests. Out: refund linkage, billing ledger mutation, quota mutation, payment dispute handling, admin dashboard UI, commit. |
| Success criteria | User can create one dispute for their own receipt, duplicate `(tenant_id,user_id,request_id)` is rejected, user list is scoped to authenticated tenant/user, operator can resolve/reject status with note, and verification gate passes. |
| Time estimate | 90-150 minutes wall clock; one Codex implementation pass plus TDD/mutation verification. |
| Blast radius | Adds one table and read/write APIs. Main risk is cross-user receipt/dispute leakage or accidentally turning a dispute into a money-path operation. Mitigation: owner-bound receipt lookup, unique DB constraint, no calls into refund/billing/quota writers, route tests. |
| Failure modes | Wrong migration number; user-created dispute for another user's receipt; duplicate disputes accepted; admin resolve does not update status; handler trusts caller-supplied tenant/user; generated sqlc drift; new files in frozen packages; tests that do not fail under mutation. Mitigation: verify migration tail first, use `GetReceiptForUser`, unique constraint test, explicit status assertions, auth-derived scope only, `sqlc generate`, non-frozen packages, mutation RED evidence. |
| Decision points | No Owner decision required inside this slice unless implementation would need refund, billing ledger, quota, auth core, secrets, or destructive migration changes. Those are high-risk and explicitly out of scope. |
| Pre-execution checklist | Read `CLAUDE.md`, `AGENTS.md`, `docs/RULES.md`; read `/tmp/verify-audit001.log`; verify `backend/sql/migrations` tail shows `0083_usage_records_cost_snapshot`; confirm `internal/audit` is non-frozen; inspect receipt owner-bound storage and routes; claim `.coordination` locks; write RED tests; implement; run mutation checks and gate. |

## Reference Projects In Scope

Default mirrors listed per AGENTS.md rule: CLIProxyAPI, sub2api, new-api. This implementation lane does not derive code, schema layout, identifiers, or algorithms from those projects; it implements the already approved HUAKAI/Sonnet residual design. A narrow pre-implementation shape scan was only used to avoid expanding this slice into payment/refund workflows. No reference-source code is copied.

## Package And File Structure

- Create `backend/sql/migrations/0084_cost_disputes.up.sql` and `.down.sql`: schema only, not a frozen package.
- Create `backend/sql/queries/cost_disputes.sql`: sqlc CRUD/list queries for `internal/db/audit`.
- Modify `backend/sqlc.yaml`: add `cost_disputes.sql` to the existing audit sqlc package.
- Generate/update `backend/internal/db/audit/*`: generated sqlc code, non-frozen DB package.
- Create `backend/internal/audit/dispute_store.go`: non-frozen package; only dispute storage/state logic.
- Create `backend/internal/audit/dispute_store_test.go`: store tests including unique duplicate rejection and resolve update.
- Create `backend/internal/disputehttp/handler.go`: new non-frozen HTTP package; session/admin auth scoped handlers.
- Create `backend/internal/disputehttp/handler_test.go`: discriminating cross-user/list/resolve tests.
- Modify `backend/cmd/gateway/routes.go`: existing non-frozen route wiring only.
- Optionally modify `backend/cmd/gateway/openapi_consistency_test.go` if route allowlist tests require the new endpoint.

Frozen packages check: no new files in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## API Shape

- `POST /v1/receipts/{request_id}/disputes`: session-auth user creates a dispute for their own receipt. Request body: `{"reason":"..."}`. Response includes the dispute record. Duplicate request returns conflict and does not create a second row.
- `GET /v1/me/disputes`: session-auth user lists only their own disputes.
- `POST /v1/admin/disputes/{id}/resolve`: admin auth resolves a dispute. Request body: `{"tenant_id":7,"status":"resolved|rejected|reviewing","operator_note":"..."}`. Response includes the updated dispute.

## Concrete Execution Order

1. Write `internal/disputehttp` tests first:
   - user A cannot create a dispute for user B receipt because the handler calls owner-bound receipt lookup.
   - user A list excludes user B dispute even if the store contains it.
   - duplicate create returns HTTP 409.
   - admin resolve changes status and note.
2. Write `internal/audit` store tests first:
   - duplicate `(tenant_id,user_id,request_id)` maps to a duplicate error.
   - list is scoped by tenant and user.
   - resolve updates only tenant/id and sets `resolved_at` for terminal statuses.
3. Run focused tests and confirm RED because package/store/schema/query do not exist.
4. Add migration `0084_cost_disputes` with status check `open|reviewing|resolved|rejected`, unique `(tenant_id,user_id,request_id)`, and no refund/billing columns.
5. Add sqlc queries and run `sqlc generate`.
6. Implement `internal/audit` store using generated queries and pgx duplicate detection.
7. Implement `internal/disputehttp` handlers using session identity/admin identity and no caller-supplied tenant/user for user APIs.
8. Wire routes in `cmd/gateway/routes.go`.
9. Run focused GREEN tests.
10. Mutation checks:
    - Temporarily remove user filtering in the list or fake store filter; cross-user test must RED.
    - Temporarily make duplicate create succeed in the store/fake; duplicate test must RED.
    - Temporarily make resolve keep old status; resolve test must RED.
    - Restore each mutation immediately.
11. Run required gate: `cd backend && (sqlc generate >/dev/null 2>&1 || true) && go build ./... && go vet ./... && go test ./internal/audit/... ./internal/disputehttp/... ./cmd/gateway/... 2>&1 | tail -18`.
12. Do not commit; PM submits.

## Assumptions And Risks

- Latest Owner instruction supersedes older F-AUDIT-001 draft route names.
- `request_id` may contain slashes in existing receipt routes, so route wiring should support both `{request_id}` and `{request_id_host}/{request_id_tail}` for dispute creation if the existing receipt route does.
- This slice is medium risk because it adds schema and APIs, but it is safe to land as pure record/state flow. Refund linkage stays a future high-risk slice.
- Clean-room risk is low because implementation is HUAKAI-owned and based on internal spec/Sonnet design; no reference implementation is translated.

