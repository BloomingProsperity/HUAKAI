# 2026-06-03 Quota Hot Path Enforce Codex Plan

| Owner directive | "Implement + verify only... WIRE quota.Service into the hot path as per-tenant/policy admission control, BEHIND a default-OFF enforcement flag..." |
| Scope | In: config flag, quota hot-path reservation, quota settler decorator, gateway wiring, discriminating unit tests. Out: schema changes, DB integration behavior, new routes, commits, external reference-source mining. |
| Success criteria | `HUAKAI_QUOTA_ENFORCE` defaults false; OFF wires plain settler and no quota reserver; ON constructs `quota.NewService(quota.NewPostgresStore(pgPool))`, wraps billing settler, and reserves quota after non-replay billing claim reserve. Deny returns 429 with `insufficient_quota` body and aborts billing claim as `quota_denied`. Decorator finalizes quota after successful billing settle and releases after successful billing abort. Required build/vet/test commands run. |
| Time estimate | 1-2 hours wall clock; one Codex implementation pass plus verification. |
| Blast radius | Money admission path for chat completions, cache-hit lifecycle, settlement recovery wiring, gateway startup config. OFF default must leave runtime behavior unchanged. |
| Failure modes | Quota deny after billing reserve leaves claim reserving: abort immediately and test reason. Decorator leaks reservations on abort/settle/cache-hit: unit tests assert calls. OFF accidentally constructs/wires quota: wiring test asserts plain settler and nil quota resolver. Invalid release reason: map billing abort reasons to quota-supported release reasons while preserving original billing reason for inner abort. |
| Decision points | No Owner decision needed for default-off flag and new non-frozen package. PM deep-review + Owner-park required before landing because this is HOT-PATH + MONEY ADMISSION. If `billing.Settler` were unwrappable, stop; observed it is exported and already decorator-wrapped in `internal/audit`. |
| Pre-execution checklist | Read `AGENTS.md`, root `CLAUDE.md` (task's `backend/CLAUDE.md` path is absent), `docs/RULES.md §2`, coordination README, billing/quota interfaces, config loader, gateway dispatch, and existing error path. Claim files through `.coordination/`. Write failing tests before production changes. |

## File Scope

- Add `backend/internal/quotaenforce/settler.go` and `backend/internal/quotaenforce/settler_test.go`; target package is new and not frozen.
- Modify `backend/internal/config/config.go` and `backend/internal/config/config_test.go`; package is not frozen.
- Modify `backend/cmd/gateway/wiring.go` and `backend/cmd/gateway/wiring_test.go`; package is not frozen.
- Modify existing frozen-package files only: `backend/internal/gatewayhttp/chat_completions_handler.go`, `backend/internal/gatewayhttp/chat_completions_dispatch.go`, `backend/internal/gatewayhttp/chat_completions_error.go`, `backend/internal/gatewayhttp/chat_completions_dispatch_test.go`. No new file under frozen packages.

## Reference Projects In Scope

Rule-required default mirrors: CLIProxyAPI, sub2api, new-api.

This execution does not read external reference-project source and makes no reference-project behavior claim. The task is HUAKAI-internal wiring from PM-verified facts and existing local code. Clean-room risk is controlled by staying inside local HUAKAI code and not copying external source, names, schemas, comments, or algorithms.

## Concrete Execution Order

1. Add failing `internal/quotaenforce` tests for settle, abort, cache-hit pass-through/finalization, and plain pass-through when quota finalizer is nil.
2. Add failing config tests proving `HUAKAI_QUOTA_ENFORCE` defaults false, true parses true, invalid values fail with env name.
3. Add failing gateway/wiring tests proving OFF does not wire quota and ON wraps with quota.
4. Add failing gateway hot-path tests proving quota deny aborts billing claim with `quota_denied` and returns 429 `insufficient_quota`, and OFF/nil reserver never calls quota.
5. Implement minimal config field and env parse.
6. Implement `quotaenforce` interfaces, scope builder, and settler decorator.
7. Wire `quota.NewService(quota.NewPostgresStore(pgPool))` only when flag is true; wrap the existing settler only when flag is true.
8. Add `QuotaReserver` dependency to `ChatHandlerDeps` and call quota reserve after `ex.reserveRes = reserveRes` on the non-replay path.
9. Add 429 helper sharing the existing `insufficient_quota` body shape while preserving balance 402 behavior.
10. Run targeted tests, mutation checks, then required build/vet/test gate.
