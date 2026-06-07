# 2026-06-07 AUTH-037 Email Send Per-IP Limit Codex Plan

| Owner directive | "模块A闭环 — 发码 per-IP 限流(AUTH-037)。Branch fix/a-sendlimit. HUAKAI-internal,clean-room。全加性。" |
| Scope | In: add `internal/emailsendlimit`, inject it into auth email-send paths, keep existing per-email cooldown. Out: schema, migrations, reference-source reads, git commit. |
| Success criteria | Same IP exceeds configured window quota and receives 429 with `Retry-After` before email sender call; another IP remains allowed; existing per-email cooldown remains unchanged; requested build/vet/test commands pass. |
| Time estimate | 45-75 minutes wall clock / one Codex work unit. |
| Blast radius | Auth register verification email and password reset email request paths; process-local memory limiter; gateway startup wiring. |
| Failure modes | Too strict defaults could block NAT users; mitigate with a generous default quota and env overrides. Wrong limiter key could miss IP rotation abuse; mitigate with a discriminating same-IP/different-IP unit test. Missing `Retry-After` would weaken client recovery; test package checks positive retry duration and handler writes header from the limiter result. |
| Decision points | No high-risk Owner sign-off expected: no `LICENSE`, secrets, payment, auth-core credential validation, billing ledger, quota enforcement, schema, migrations, deployment scripts, or destructive commands. |
| Pre-execution checklist | Read `loginthrottle` limiter shape; read `email` sender cooldown; locate auth send handlers; confirm client IP resolver wiring; write failing `emailsendlimit` unit test first; implement limiter; wire handler and startup deps; run requested checks. |

## Concrete Execution Order

1. Add `backend/internal/emailsendlimit/limiter_test.go` with injected-clock tests for same-IP exhaustion, positive retry-after, different-IP isolation, and window recovery.
2. Run `go test ./internal/emailsendlimit/...` from `backend` and confirm the package fails because implementation is missing.
3. Add `backend/internal/emailsendlimit/limiter.go` with a mutex-protected rolling-window limiter, default config, max-key fail-closed guard, old-key eviction, and second-rounded retry-after.
4. Re-run the new package test and keep it green.
5. Modify existing `backend/internal/gatewayhttp/auth_handler.go` only: add a small limiter interface to deps, check it before `SendVerification` / `SendPasswordReset`, and return 429 + `Retry-After` through a dedicated email-send throttle response helper.
6. Add gateway startup wiring in `cmd/gateway`: load an `emailsendlimit` limiter with generous defaults and env overrides, store it in `deps`, and pass it through `authHandlerDeps`.
7. Run the requested verification commands exactly:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && /usr/local/go/bin/go vet ./internal/emailsendlimit/...`
   - `cd backend && /usr/local/go/bin/go test -count=1 ./internal/emailsendlimit/...`

## Assumptions

- The existing `clientip.Resolver` is the authoritative trusted-proxy-aware source for limiter keys.
- Defaults should be wider than the per-email cooldown because this limiter protects IP-level abuse, not normal single-user resend behavior.
- Multi-instance centralized limiting remains out of scope for this small additive patch; this package documents process-local semantics.
