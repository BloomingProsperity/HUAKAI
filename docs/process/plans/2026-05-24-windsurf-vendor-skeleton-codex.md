# 2026-05-24 Windsurf Vendor Skeleton
| Owner directive | "HUAKAI 账号转 API 模块, Windsurf vendor 实施骨架, 与 cursor (commit 648ceb5) 类似." |
| Scope | In: `backend/internal/provider/windsurf/bootstrap.go`, `backend/internal/provider/windsurf/refresher.go`, `backend/internal/provider/windsurf/credential_store_adapter.go`, focused Windsurf package tests, and `backend/internal/credentialacq/vendor_exchangers.go` registration. Out: `backend/internal/credentialworker/scheduler.go`, DB schema, auth core, billing/quota, production secrets, LGPL/AGPL/GPL reference source. |
| Success criteria | Windsurf OAuth bootstrap is operator-config fail-closed; Windsurf refresh adapter implements provider refresh and maps 401/invalid_grant, 429, and 5xx through `credentialworker.ClassifyRefreshError`; session-to-credentialstore adapter exists; Windsurf fake PKCE exchanger is registered; mutation-discriminating tests cover bootstrap and refresh; requested race test command is run or failure is reported honestly. |
| Time estimate | 60-90 minutes wall clock, one Codex session. |
| Blast radius | Low/medium. New files are isolated to non-frozen `backend/internal/provider/windsurf`; one registry line changes OAuth callback lookup. No scheduler injection, schema, billing, quota, auth-core, deployment, or secret changes. |
| Failure modes | Tests could accidentally pass without discriminating endpoint defaults or error classes; mitigation: use fixtures where guessed defaults, missing body parsing, or flattened status handling make tests fail. Registry registration could conflict; mitigation: follow existing cursor key pattern. Refresher could write wrong provider credentials; mitigation: explicit vendor/auth-mode guard and store transaction test. |
| Decision points | Owner confirmation is still needed before any real Windsurf OAuth endpoint, client ID, scopes, or production scheduler injection. Stop if implementation requires schema/auth-core/billing/quota changes. |
| Pre-execution checklist | 1. Read local clean-room, package, and test-quality rules. 2. Confirm `backend/internal/provider/windsurf` is not frozen and stays under package budget. 3. Reuse HUAKAI cursor skeleton patterns, not non-MIT source. 4. Write failing Windsurf tests before implementation. 5. Do not touch `scheduler.go`. 6. Run `cd backend && GOCACHE=/tmp/go-build go test ./internal/provider/windsurf/... -count=1 -race`. |

## File Structure Check

- Create `backend/internal/provider/windsurf/bootstrap.go`: package `windsurf`, non-frozen, OAuth config helpers only.
- Create `backend/internal/provider/windsurf/refresher.go`: package `windsurf`, non-frozen, refresh adapter and classification only.
- Create `backend/internal/provider/windsurf/credential_store_adapter.go`: package `windsurf`, non-frozen, bridge to `credentialstore.Store`.
- Create `backend/internal/provider/windsurf/bootstrap_test.go`: package test coverage for fail-closed PKCE config.
- Create `backend/internal/provider/windsurf/refresher_test.go`: package test coverage for refresh success, classification, and lock-path failure recording.
- Modify `backend/internal/credentialacq/vendor_exchangers.go`: add `windsurf/oauth` registration beside session-vendor exchange skeletons.

`backend/internal/provider/windsurf` currently has 1 non-test Go source file. This plan adds 3 non-test Go files, keeping the package below the 20-file and 5000-LoC budget.

## Execution Order

1. Add Windsurf bootstrap test proving defaults contain no auth URL, token URL, client ID, or scopes, and validation fails with `ErrWindsurfOAuthConfigRequired`.
2. Add Windsurf authorize URL test proving operator config flows through PKCE S256.
3. Run Windsurf package tests and confirm the new tests fail because bootstrap symbols do not exist.
4. Implement `bootstrap.go` with operator-only defaults and validation.
5. Add refresh adapter tests for success merge and HTTP failure classes.
6. Run Windsurf package tests and confirm refresh tests fail because refresher symbols do not exist.
7. Implement `refresher.go` and `credential_store_adapter.go` using local HUAKAI cursor skeleton behavior.
8. Add/verify exchanger registration for `windsurf/oauth`.
9. Run requested race test command.

## Clean-Room Guard

No LGPL/AGPL/GPL reference project source will be read. Any claim about real Windsurf OAuth endpoints requires an official source or Owner capture; absent that, the implementation remains operator-config fail-closed.
