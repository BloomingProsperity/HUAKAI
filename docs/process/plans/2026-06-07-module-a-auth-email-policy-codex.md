# 2026-06-07 module-a auth email policy
| Owner directive | "TASK: 模块A闭环 — 注册/登录/邮箱策略子开关(6 项小功能,融合 new-api 同位能力)。Branch fix/a-regpolicy. HUAKAI-internal,clean-room(禁读任何参考项目源码,只读 HUAKAI 自身 + 本 spec)。全部加性 / 新包 / 默认不改变现有行为..." |
| Scope | In: platformsettings add six opt-in keys and allowlist empty-enable guard; new `internal/authpolicy`; new `internal/emailpolicy`; additive `userauth.Service` injection and registration/login checks; unit tests only. Out: reference project source, migrations/schema, frozen packages, money/quota/session-token/auth-core rewrites, commits. |
| Success criteria | Requested build/vet/test command passes; new tests cover password register toggle, password login toggle with equal work, email domain allowlist, empty allowlist setting guard, alias restriction, and reserved local-part policy; nil/default policy paths preserve current behavior. |
| Time estimate | 60-90 minutes wall clock / one Codex session. |
| Blast radius | Authentication registration/login service behavior and global platform setting validation. Defaults remain pass-through, so existing deployments should behave as before unless an operator opts into a new policy. |
| Failure modes | Too-early login toggle return could create a timing oracle; mitigate by calling existing `equalizeLoginWork` before returning disabled-login error. Empty allowlist could lock out all registrations when enabled; mitigate by rejecting `email_domain_allowlist_enabled=true` while the stored/default allowlist is blank. Email parsing could drift from existing normalization; mitigate by applying checks after `NormalizeEmail` and using pure helpers. |
| Decision points | None expected. Stop for Owner confirmation only if schema/auth-core/money/quota/frozen-package changes become necessary. |
| Pre-execution checklist | Confirm branch/worktree; read `platformsettings` set/get validation path; read `userauth.Register`, `Authenticate`, `NormalizeEmail`, and timing-equalization tests; write failing tests before production code; keep clean-room by not opening reference repositories. |

## Concrete Execution Order

1. Add RED tests:
   - `backend/internal/userauth/service_test.go`: `TestPasswordRegisterToggle`, `TestPasswordLoginToggle`, and registration-level email policy tests.
   - `backend/internal/emailpolicy/policy_test.go`: pure email policy domain/alias/reserved tests.
   - `backend/internal/platformsettings/service_test.go`: empty allowlist enable guard.
   - `backend/internal/authpolicy/policy_test.go`: platformsettings-backed password toggle helpers.
2. Run targeted tests and confirm expected compile/failure from missing symbols.
3. Implement platformsettings keys in `types.go`:
   - password register/login defaults true.
   - email domain allowlist enabled default false, list default empty.
   - email alias restriction default false, reserved localparts default empty.
   - make `ValidateValue` accept empty CSV values for the list keys.
   - make `Service.Upsert` reject enabling an empty domain allowlist by reading the current allowlist value.
4. Implement `internal/authpolicy` with a narrow injected settings reader interface.
5. Implement `internal/emailpolicy` with pure, case-normalizing CSV checks and sentinel errors.
6. Wire `userauth.Service` additively:
   - optional `RegistrationGate` interface with password registration/login methods.
   - optional `EmailPolicy` interface for domain/alias/reserved checks.
   - nil means allow.
   - registration checks run after registration-mode disabled and after email normalization.
   - password-login disabled returns a typed error only after equal-work argon2 dummy work.
7. Run targeted tests, then the requested full build/vet/test commands.

## Clean-Room Notes

This plan uses only HUAKAI files and the Owner-provided task spec. No reference-project source, source-derived tests, distinctive identifiers, schemas, or comments are in scope.
