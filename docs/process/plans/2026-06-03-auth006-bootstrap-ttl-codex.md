# 2026-06-03 auth006 bootstrap TTL

| Owner directive | "实现 GAP-1(CRITICAL): TTL selector 未接线... createOrStartCredentialAcqSession 用 selector 按 LongLivedRequested 算 ExpiresAt" |
| Scope | In: F-AUTH-006 GAP-1 only. Add a pure TTL selector in `backend/internal/credentialacq`, wire existing `backend/internal/gatewayhttp/admin_credential_acquisition_handler.go`, add TTL-only env/config overrides, add discriminating tests. Out: GAP-2 expiry sweeper, GAP-3 atomic create+start, schema migrations, secrets, production deploy, long-lived allow-gate enablement. |
| Success criteria | Long-lived OAuth acquisition start stores `ExpiresAt` near `now+7d`; short OAuth acquisition start stores `ExpiresAt` near `now+10m`; selector tests prove true/false return distinct expected durations; returning to zero `ExpiresAt` fallback would make long-lived handler test fail. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation session. |
| Blast radius | Credential acquisition session start only. A wrong value can widen or shrink OAuth bootstrap windows; it must not log secrets or alter credential finalization. |
| Failure modes | Non-discriminating test: avoid by asserting exact duration windows and checking long/short differ. Frozen package violation: do not add gatewayhttp files. Security expansion: do not bypass `AllowLongLivedSetupToken`; only apply TTL after that gate. Config confusion: parse optional TTL env as positive seconds and keep zero as selector default. |
| Decision points | Owner should confirm whether 7d is the desired long bootstrap default and whether production `AllowLongLivedSetupToken` wiring should be added in a follow-up. |
| Pre-execution checklist | 1. Read root `AGENTS.md` and `CLAUDE.md`; `backend/CLAUDE.md` was requested but is absent. 2. Read Sonnet plan `/home/ubuntu/.claude/plans/huakai-plan-f-auth-006-misty-brooks.md`. 3. Check `.coordination/` and claim target files. 4. Re-read `session_store.go`, `types.go`, handler, and existing tests. 5. Write failing tests before production code. 6. Implement smallest selector + handler wire. 7. Run targeted red/green and requested gate commands. |

## Reference Projects In Scope

No external reference source is read in this implementation lane. This is a HUAKAI-internal bug fix against existing `credentialacq`/`gatewayhttp` behavior and the Owner-supplied Sonnet plan; the work does not make new claims about CLIProxyAPI, sub2api, or new-api behavior.

## Concrete Execution Order

- [x] Add selector tests in `backend/internal/credentialacq/bootstrap_test.go`.
- [x] Add handler start TTL tests in existing `backend/internal/gatewayhttp/admin_credential_acquisition_handler_test.go`.
- [x] Run targeted tests and confirm they fail before implementation.
- [x] Add `backend/internal/credentialacq/bootstrap.go` with default short/long TTL selection and configurable-duration helper.
- [x] Wire `start.ExpiresAt` in existing `createOrStartCredentialAcqSession`.
- [x] Add TTL-only config/env overrides and pass them to admin credential acquisition deps.
- [x] Run targeted tests, then requested build/vet/test gate.
- [x] Report GAP-2/GAP-3 deferred and route/env gate observation.
