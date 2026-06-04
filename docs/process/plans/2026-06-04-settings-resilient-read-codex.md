# 2026-06-04 settings-resilient-read

| Owner directive | "消除一个安全 fail-open —— 当 captcha / 2FA 等安全开关读平台设置遇到瞬时 DB/store 错误时...让 `platformsettings.Service.Get` 从常驻内存的 last-known-good 快照取值" |
| Scope | In: HUAKAI-owned `backend/internal/platformsettings` service/tests and `backend/cmd/gateway/wiring.go` boot prewarm. Out: external reference source, consumer fail-closed changes, schema, auth core, billing/quota, new runtime dependency, background goroutine, commit. |
| Success criteria | `Get` still returns programming errors for nil store and unknown keys; cache hit still works; cache miss store/normalize transient error returns LKG with nil error; empty LKG returns safe default with nil error; successful `Upsert` and `RefreshAll` seed LKG; gateway boot calls `RefreshAll` best-effort with warn-only logging; required build/vet/tests pass. |
| Time estimate | 45-75 minutes wall clock, one Codex implementation pass. |
| Blast radius | Platform settings reads used by captcha, 2FA, registration, promo, timeout/cooldown, response-header controls, fallback chains. A wrong fallback could stale settings longer than intended during store errors, but TTL cache remains the normal refresh path. |
| Failure modes | LKG not written after `Upsert`, causing stale/default during store outage; fallback accidentally masks programming errors; boot prewarm blocks startup; tests become non-discriminating; raw setting values get logged. Mitigation: TDD red tests, explicit nil/unknown key preservation, warn logs without values, final targeted and package gates. |
| Decision points | None pending. Owner already specified LKG fallback over consumer fail-closed, no background goroutine, no consumer edits, and no external reference source reads. |
| Reference projects in scope | None. Owner explicitly prohibited reading `/home/ubuntu/refs` or external reference source for this task; implementation is based only on PM spec and HUAKAI-owned code. |
| Package/file structure | Modify existing non-frozen package `backend/internal/platformsettings`; modify existing `backend/cmd/gateway/wiring.go`; do not create files in frozen `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. |
| Pre-execution checklist | Read `CLAUDE.md`; read `AGENTS.md`; inspect `platformsettings.Service`, `types`, store interfaces, existing tests, boot wiring, and consumers; claim coordination lock; write discriminating tests first; verify red; implement minimal code; verify green; run final gate. |

## Concrete Execution Order

1. Add tests in `backend/internal/platformsettings/service_test.go` for LKG fallback after successful read, default fallback with empty LKG, `Upsert` seeding LKG, and `RefreshAll` seeding multiple keys.
2. Run the focused `platformsettings` tests and confirm the new tests fail against current behavior.
3. Implement `Service.lastKnown sync.Map`, helper storage/loading, `Get` fallback behavior, `Upsert` LKG writes on both atomic and non-atomic success paths, and `RefreshAll(ctx) error`.
4. Add best-effort boot prewarm in `backend/cmd/gateway/wiring.go` immediately after constructing `platformSettingsService`; log only the error, not setting values.
5. Run focused `platformsettings` tests.
6. Run the Owner gate: `cd backend && (sqlc generate>/dev/null 2>&1||true) && go build ./... && go vet ./... && go test ./internal/platformsettings/... ./internal/captcha/... ./internal/twofahttp/... ./internal/gatewayhttp/... ./cmd/gateway/... 2>&1 | tail -20`.
7. Re-check `git diff --stat` and verify consumer files `internal/captcha/verifier.go`, `internal/gatewayhttp/auth_handler.go`, and `internal/twofahttp/handler.go` were not modified.
