# 2026-06-04 notifications-slice2-codex

| Owner directive | "通知系统从 slice-1(只 admin worker-stats)补成真投递——per-user 通知设置(email/webhook/bark/gotify)+ 余额不足自动提醒 + 限频防刷。默认关(用户没配就不发)。" |
| --- | --- |
| Scope | In: HUAKAI-only implementation under `backend/internal/notify`, user/admin notification settings API, migration `0089`, cmd/gateway wiring, non-blocking low-balance settle hook, discriminating tests. Out: reading `/home/ubuntu/refs`, copying reference code, git commits, new runtime dependencies, frozen-package new files, auth core rewrites, billing ledger/schema changes beyond the authorized settings table. |
| Success criteria | Default `notify_type=none` sends nothing; email/webhook/bark/gotify adapters exist; webhook signs with HMAC; webhook/bark/gotify fail closed on SSRF-unsafe URLs; email rejects CRLF header injection; post-settle low-balance trigger is best-effort and non-blocking; per-user per-event rate limit suppresses repeat sends in-window; requested gate passes or blockers are documented. |
| Time estimate | 2-4 hours wall clock, one Codex work unit. |
| Blast radius | New table and new internal package are isolated. Existing hot path impact is a settler decorator call after successful settlement; default `none` keeps behavior inert for users without settings. Route changes add user/admin notification settings endpoints without changing existing worker-stats route. |
| Failure modes | SSRF guard too weak: mitigate with preflight URL validation and guarded HTTP client. Notification failure blocks settlement: mitigate with settler decorator that discards notification errors and runs async by default. Secret/email leakage in logs or responses: no notify logging, response masks secrets. Rate limiter ineffective: discriminating test calls same event twice inside window. Migration mismatch: direct SQL store and build/test gate. |
| Decision points | Owner already authorized per-user settings storage and settlement hook. Stop before adding dependencies, modifying `LICENSE`, editing real secrets, changing auth core, changing quota enforcement, changing billing ledger semantics, destructive shell commands, or production deployment. |
| Pre-execution checklist | 1. Read `CLAUDE.md` and `AGENTS.md`. 2. Confirm no `/home/ubuntu/refs` access. 3. Check coordination state and claim touched files. 4. Verify migration number `0089` is available. 5. Read HUAKAI email, settlement, SSRF, settings, and route wiring code. 6. Write discriminating tests before implementation. 7. Implement inside non-frozen package and only edit existing frozen-package files if unavoidable. 8. Run requested gate. |

## Concrete execution order

1. Add acceptance-style unit tests for notification delivery, SSRF fail-closed behavior, email header injection rejection, low-balance settler non-blocking behavior, and default-none no-op behavior.
2. Add migration `0089_user_notification_settings` with `notify_type`, delivery target fields, secret-token fields, and decimal low-balance threshold.
3. Implement `internal/notify` settings validation/store, delivery adapters, HMAC signing, SSRF checks, rate limiter, low-balance event, and `billing.Settler` decorator.
4. Implement `internal/notify/notifyhttp` user/admin settings handlers with existing session/admin identity patterns and secret masking.
5. Wire cmd/gateway dependencies and routes while preserving existing admin worker-stats route.
6. Run targeted tests, then the Owner gate command.

## Clean-room note

This work is based on the Owner/PM specification and HUAKAI-owned code only. `/home/ubuntu/refs` is explicitly out of scope and must not be read.
