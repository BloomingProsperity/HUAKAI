# 2026-05-24 Cursor vendor skeleton Codex plan
| Owner directive | "CONTEXT: HUAKAI 账号转 API 模块, Cursor vendor 实施骨架." |
| Scope | In: `backend/internal/provider/cursor/bootstrap.go`, `backend/internal/provider/cursor/refresher.go`, `backend/internal/provider/cursor/credential_store_adapter.go` if needed, and focused cursor package tests. Out: `backend/internal/credentialworker/scheduler.go`, `backend/internal/credentialworker/outcome.go`, DB schema, auth core, billing/quota, production secrets, LGPL/AGPL/GPL reference source. |
| Success criteria | Cursor OAuth bootstrap exposes PKCE config; cursor refresh adapter handles success and discriminates 401/invalid_grant, 429, 5xx outcomes; tests prove mutation sensitivity; requested build and cursor race test commands run or failures are reported honestly. |
| Time estimate | 90-150 minutes wall time in this Codex session. |
| Blast radius | Cursor provider package plus low-risk plan/test files. No frozen package file additions. No scheduler wiring in this task. |
| Failure modes | Cursor public endpoint evidence may be incomplete; mitigate by keeping endpoint/operator override explicit and reporting Owner confirmation need. Current S1 untracked `credentialworker/outcome.go` may affect build; treat as external workspace state and do not edit. Refresher API in prompt differs from current code; implement against current `credentialworker` interfaces and report the mismatch. |
| Decision points | OAuth endpoint/client values need Owner confirmation if CLIProxyAPI and public Cursor endpoint evidence diverge; scheduler injection remains a Claude merge decision; runtime template and live packet capture remain out of scope. |
| Pre-execution checklist | 1. Read `AGENTS.md` package/test/clean-room rules. 2. Confirm target package is not frozen. 3. Read current credential worker/store interfaces. 4. Read only allowed MIT/Apache reference regions with clean-room lane guard. 5. Write failing cursor tests before production code. 6. Run requested commands. |

## Concrete execution order

1. Keep the existing untracked S1 files untouched.
2. Read `~/refs/CLIProxyAPI` Cursor authentication regions as specifier-lane evidence only.
3. Add cursor tests for bootstrap config and refresh success/failure outcome discrimination.
4. Verify the tests fail for missing Cursor refresh/bootstrap implementation.
5. Implement cursor package files with local types and adapters that satisfy existing HUAKAI interfaces.
6. Run `go test ./internal/provider/cursor/... -count=1` for the red/green loop, then requested build and race commands.
7. Report new files, line counts, mutation self-check, residual Owner decisions, and clean-room provenance.
