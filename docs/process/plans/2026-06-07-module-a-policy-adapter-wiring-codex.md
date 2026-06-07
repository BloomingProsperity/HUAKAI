# 2026-06-07 module-a policy adapter wiring
| Owner directive | "TASK: 模块A闭环 — 把已落地的注册/登录/邮箱策略开关「接活」到生产(让 AUTH-004/005/007/008/009/010 真正可用)。Branch fix/a-regwire. HUAKAI-internal,clean-room。全加性,默认行为不变(强而不破)。无 schema。" |
| Scope | In: HUAKAI-internal adapter package for auth/email policy settings; additive `cmd/gateway` wiring in `buildUserServices`; unit tests for setting reads and fail-open behavior; requested build/vet/test commands. Out: reference source, schema/migrations, runtime dependencies, frozen-package new files, commits. |
| Success criteria | Adapter returns password registration/login settings from `platformsettings.Service`; email policy adapter returns allowlist/alias/reserved settings; settings read errors fail open to current defaults; `buildUserServices` injects non-nil `RegistrationGate` and `EmailPolicy`; requested checks pass or failures are reported honestly. |
| Time estimate | 45-75 minutes wall clock / one Codex session. |
| Blast radius | Registration and login construction path plus a new small internal adapter package. Defaults are pass-through (`password_* = true`, email policies disabled/empty), so deployments without changed settings keep existing behavior. |
| Failure modes | A store/read failure could lock out users if fail-open defaults are wrong; mitigate with explicit unit tests. Adapter could accidentally ignore platformsettings values; mitigate with discriminating tests that set false/enabled/list values. Wiring could remain dormant; mitigate with a constructor-level test or an equivalent compile/runtime assertion. |
| Decision points | Stop for Owner confirmation only if schema, auth-core rewrite, quota/billing, secrets, runtime dependency, destructive migration, or frozen-package new file changes become necessary. |
| Pre-execution checklist | Confirm isolated worktree and branch; read `authpolicy.Settings`, `userauth.RegistrationGate`, `userauth.EmailPolicy`, `platformsettings.Service.Get`, and `cmd/gateway` construction examples; write RED adapter tests before production adapter code; keep all work HUAKAI-internal and clean-room. |

## Concrete Execution Order

1. Add RED tests in a new non-frozen package test file for platformsettings-backed auth/email adapters.
2. Run the targeted new test and confirm it fails because the adapter package does not exist yet.
3. Implement the adapter package with narrow constructors and explicit fail-open defaults.
4. Re-run the adapter tests and keep them green.
5. Edit `cmd/gateway/lifecycle.go` additively to construct `platformsettings.NewService(platformsettings.NewPostgresStore(pgPool), nil)` and inject `authpolicy.New(...)` plus the email policy adapter into `userauth.Service`.
6. Add or update a focused `cmd/gateway` test if feasible without a live database; otherwise rely on adapter tests plus compile/build for wiring verification and report the reason.
7. Run the Owner-requested build, vet, and test commands.

## Clean-Room Notes

This work reads and modifies only HUAKAI-internal code. No reference-project source, source-derived implementation details, schema copying, or upstream identifiers are in scope.
