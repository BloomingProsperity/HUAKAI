# 2026-05-27 TRUST-A Green Codex Plan

| Owner directive | "TRUST-A Green 阶段实现 (TDD 完成 Red→Green)" |
| Scope | In: make the existing TRUST-A backend and frontend red tests pass by adding audit result metadata, trust status/header helpers, chat completions header wiring, usage API trust fields, and dashboard usage rendering. Out: TRUST-B signing/pubkey/verify endpoint, schema migrations, new runtime dependencies, commits, reference-project source reading. |
| Success criteria | Requested commands pass: `go test ./internal/auditledger/ ./internal/trust/ ./internal/gatewayhttp/ -count=1 -timeout 90s`, `npm run test:trust`, `npm run type-check`, and `go build ./...`. Existing ledger tests stay unchanged. |
| Time estimate | 90-150 minutes wall clock; one Codex implementation pass with focused verification loops. |
| Blast radius | Medium: changes response headers, audit ledger result JSON surface, admin usage API response shape, and frontend usage table rendering. No database schema, auth, billing ledger write path, quota enforcement, deployment, secrets, or license files are in scope. |
| Failure modes | Header writes can happen after `WriteHeader`; mitigate by wiring trust headers at existing pre-response header points. Ledger validation can break backward compatibility; mitigate by allowing persisted upstream metadata to be empty while forbidding it for disabled/deferred. Usage API fields can mismatch frontend row types; mitigate with type-check and helper tests. Dirty worktree can contain prior edits; mitigate by inspecting before modifying and not reverting unrelated changes. |
| Decision points | No Owner decision expected unless implementation requires a schema migration, new dependency, frozen-package new file, auth/billing/quota core change, destructive action, or TRUST-B scope expansion. |
| Pre-execution checklist | 1. Read relevant rules and confirm Owner start signal. 2. Do not read non-MIT reference source. 3. Confirm new files avoid frozen packages: `backend/internal/trust/status.go` is allowed; `frontend/lib/usage-trust.ts` is allowed; no new files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. 4. Run existing red tests before implementation where feasible. 5. Preserve unrelated dirty worktree changes. 6. Run required verification commands before reporting completion. |

## Concrete Execution Order

- [ ] Inspect existing TRUST tests and current dirty files to understand the actual red surface.
- [ ] Run the focused backend and frontend trust tests to confirm the current failures.
- [ ] Update `backend/internal/auditledger/result.go` so persisted results derive optional upstream provider/model/request metadata and deferred/disabled results reject metadata.
- [ ] Implement or complete `backend/internal/trust/status.go` in the non-frozen `trust` package.
- [ ] Wire `trust.WriteResponseHeaders` into existing `backend/internal/gatewayhttp` success paths without deleting existing dispatch-only headers.
- [ ] Find the real usage list endpoint and add provider/upstream model/request ID/trust status fields from ledger entries without schema changes.
- [ ] Implement or complete `frontend/lib/usage-trust.ts` and connect the real dashboard usage panel rendering.
- [ ] Run the requested commands; fix any failures within scope.
- [ ] Report changed files, key verification lines, mutation-check reasoning, gaps, and Chinese Owner summary.
