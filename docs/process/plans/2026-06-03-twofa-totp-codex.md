# 2026-06-03 twofa-totp-codex

| Owner directive | "实现+验证。先读 CLAUDE.md + AGENTS.md。目标(Owner 批准:2FA/TOTP 两步验证,管理员可开关默认关)...严禁读任何外部参考源码...只按下面 PM 已提炼的方案 + HUAKAI 现有模式,用 HUAKAI 自己的风格从零实现。" |
| Scope | In: backend TOTP 2FA data model, sqlc, `internal/twofa`, `internal/twofahttp`, login challenge insertion, platform setting key, OpenAPI, focused tests. Out: external reference source reads, frontend UI, commits, new runtime dependencies, frozen package new files. |
| Success criteria | `go build ./...`, `go vet ./...`, and targeted tests pass; OpenAPI route consistency stays green; enabled 2FA users cannot receive sessions after password-only login; backup codes are one-time; lockout works; default false setting leaves non-2FA logins unchanged. |
| Time estimate | 4-7 hours wall clock in one Codex session, mostly schema/sqlc/wiring/test iteration. |
| Blast radius | High: auth hot path and additive DB schema. Incorrect ordering could bypass second factor, lock users out, or leak TOTP/backup secrets. |
| Failure modes | Session issued before challenge: guard with handler test asserting no session and no session family on password-only enabled login. Secret leakage: tests scan response/log sinks for TOTP secret and backup code sentinels. Schema/sqlc drift: run `sqlc generate` and build. Lockout bypass: service test drives wrong-code attempts to locked error. Backup code replay: service test consumes same code twice. |
| Decision points | Owner already approved auth/schema work and selected TOTP. No further Owner decision unless existing schema/wiring cannot support encrypted secret storage, sqlc generation fails irrecoverably, or implementation would require touching `LICENSE`, payment, quota, billing ledger, production secrets, deployment scripts, or destructive migration behavior. |
| Pre-execution checklist | Read `CLAUDE.md` and `AGENTS.md`; confirm worktree branch; do not open `/home/ubuntu/refs`; locate password-login session issuance; locate CAPTCHA/platform_settings double gate; locate credential key provider; confirm frozen package rule; claim files via `.coordination`; write RED tests before production code. |

## Clean-Room Boundary

No external reference source will be opened for this work. The normal source-must-read/triple-mirror rule conflicts with the Owner's explicit higher-priority clean-room instruction for this task, so the implementation uses only the PM-provided behavior summary, RFC6238 algorithm knowledge, Go standard library primitives, and HUAKAI-local code patterns read from this repository.

## Target Files And Package Discipline

- Create `backend/sql/migrations/0086_two_factor_auth.up.sql` and `.down.sql`: additive schema only, no destructive data migration.
- Create `backend/sql/queries/twofa.sql` and add a `sqlc.yaml` entry generating `backend/internal/db/twofa`: new package, not frozen.
- Create `backend/internal/twofa`: domain service, RFC6238 helpers, encrypted secret envelope, backup-code generation/hash/verification, lockout state, PG store adapter, memory store for tests. New package, not frozen.
- Create `backend/internal/twofahttp`: session-authenticated user self-service handlers for setup/enable/status/disable/regenerate. New package, not frozen.
- Modify `backend/internal/gatewayhttp/auth_handler.go`: existing frozen-package file only, adding the post-password/pre-session challenge branch and verify endpoint wiring surface.
- Modify `backend/cmd/gateway/{wiring.go,routes.go}` and possibly `backend/cmd/gateway/openapi_consistency_test.go` if route test fixtures need non-nil deps. Existing files only.
- Modify `backend/internal/platformsettings/types.go`: add `two_factor_enabled` allow-listed boolean defaulting to false.
- Modify `docs/openapi/openapi.yaml`: add mounted 2FA paths and schemas.

Frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto` receive no new files.

## Execution Order

1. Add RED domain tests in `internal/twofa` for RFC6238 vectors/basic verification, setup secret encryption non-plaintext, enable-after-code, backup-code one-time use, and failed-attempt lockout.
2. Add RED HTTP/login tests covering default-off password login unchanged, enabled-user password login returns challenge without session, wrong challenge code rejects, backup code succeeds once, and secret/backup sentinels do not appear in logs/events.
3. Add migration/query/sqlc config, run `sqlc generate`, and review generated `internal/db/twofa`.
4. Implement `internal/twofa` service/store/encryption/TOTP using only standard library plus existing `credentialstore.KeyProvider`.
5. Implement `internal/twofahttp` self-service handlers and mount them under session middleware.
6. Integrate password login with a pending 2FA challenge. Pending challenge is short-lived, stored hashed, and is the only path to session issuance for enabled users.
7. Add platform setting key `two_factor_enabled` with default `false`; login checks both platform setting and per-user enabled state, so default-off has zero behavior change.
8. Update OpenAPI for all mounted paths.
9. Run required verification: `cd backend && (sqlc generate >/dev/null 2>&1 || true) && go build ./... && go vet ./... && go test ./internal/twofa/... ./internal/twofahttp/... ./internal/panelauthhttp/... ./cmd/gateway/... 2>&1 | tail -18`.

## Risk Notes

- Schema/auth core is high risk but explicitly Owner-approved for this task. Implementation is additive and defaults off.
- No third-party code, schemas, comments, or identifiers are copied. No new runtime dependency or `go get`.
- Secret material is generated by `crypto/rand`, encrypted before persistence, backup codes are SHA-256 hashes only, and raw codes are returned only at setup/regeneration.
- Production recovery/admin reset is not in this slice unless already supported by disabling the platform gate; missing per-user admin recovery should be reported as follow-up rather than silently weakening login enforcement.
