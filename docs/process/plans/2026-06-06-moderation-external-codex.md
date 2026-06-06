# 2026-06-06 Moderation External Codex Plan

| Owner directive | "TASK: Add EXTERNAL moderation provider + per-category thresholds + image moderation to HUAKAI (branch fix/moderation-external). Verified absent ... Reach CLOSURE ... Default-OFF ... Do NOT read /home/ubuntu/refs ... Do NOT git commit." |
| Scope | In: `internal/moderation` external provider, screener integration, config fields, platformsettings KV keys/validation, discriminating unit tests. Out: `/home/ubuntu/refs`, migrations, commits, email notification, auth/billing/quota core changes, production secrets, socket/PG integration execution. |
| Success criteria | Default-off behavior remains pass-through; external check runs after keyword check when enabled; category scores block on `>=` threshold; flagged blocks only when no thresholds are configured; multi-key freeze skips unhealthy keys; external errors fail open with audit/metric; image data URL caps reject before HTTP; SSRF-protected client blocks loopback/private base URLs; requested build/vet/package tests run or any sandbox blockers are recorded. |
| Time estimate | 2-4 hours wall clock in one Codex session. |
| Blast radius | Hot path screener behavior changes only when moderation runtime and external config are both enabled. Platformsettings key list changes affect settings list/default/validation tests. No migration or frozen package new file is planned. |
| Failure modes | Non-discriminating tests; accidental external calls while disabled; SSRF bypass through injected custom transport; fail-closed regression on external outage; raw body/key logging; auto-ban ignoring external block; platformsettings accepting malformed JSON. Mitigation: test-first with call counters, use `auth.NewSSRFProtectedOAuthClient(nil)` by default, log only metadata/error types, route all block decisions through existing audit/ban helpers, validate JSON shapes and bounds. |
| Decision points | Owner/PM must approve any later strict fail-closed flag, email notification, DB schema persistence, admin UI/API expansion, or production rollout. This slice proceeds with fail-open external errors and platformsettings KV defaults because the Owner task explicitly requested closure under those boundaries. |
| Pre-execution checklist | Read `AGENTS.md` and `docs/RULES.md`; inspect `internal/moderation` screener/types/audit/ban tests; inspect `platformsettings` key validation patterns; locate HUAKAI SSRF-protected HTTP client; confirm no new files under frozen packages `gatewayhttp`, `gateway`, or `proto`; write tests before implementation. |

## Current Understanding

`internal/moderation/screener.go` currently loads tenant config, returns pass when disabled, then runs hash and keyword checks before logging a clean pass. `types.go` has `ModerationConfig`, `ScreenRequest`, `ScreenResult`, and existing decisions for pass, keyword, hash, backend, and fee-charged outcomes. `audit_log.go` stores metadata-only moderation events, and `ban_counter.go` currently records only keyword/hash blocks. HUAKAI already exposes `auth.NewSSRFProtectedOAuthClient`, which installs a dial-time SSRF guard and rejects redirects. `platformsettings/types.go` owns allow-listed global KV keys plus validation/defaults, so external moderation defaults should be added there without a migration.

## Execution Order

1. Add failing `internal/moderation` screener tests for external block, disabled no-call, fail-open audit, and auto-ban wiring.
2. Add failing `internal/moderation` provider tests for multi-key freeze, image byte cap, and SSRF loopback rejection.
3. Add failing `internal/platformsettings` tests for default-off external moderation keys and validation of URL, JSON string-array API keys, JSON threshold map, timeout bounds, and image-enabled bool.
4. Implement minimal type additions in `internal/moderation/types.go`: external decision, image input metadata, external config fields, and provider interface.
5. Implement `internal/moderation/external_provider.go` with OpenAI-compatible request/response structs, default SSRF client, key pool freeze windows, timeout/retry clamps, image cap validation, and threshold evaluation.
6. Integrate `checkExternal` into `storeScreener.Screen` after keyword checks, with block audit/ban and error fail-open audit/metric.
7. Update `ban_counter.go` to include external blocks.
8. Update `platformsettings/types.go` key allow-list, defaults, and validators.
9. Run `gofmt`, targeted package tests, then requested `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./... && go vet ./...` and package unit tests for `internal/moderation` and `internal/platformsettings`.
10. Inspect `git status`/diff for scope, frozen package compliance, no commits, and no `/home/ubuntu/refs` reads.

## Clean-Room Notes

This is HUAKAI-native implementation from PM-provided reference semantics only. Codex will not read `/home/ubuntu/refs`, reference source, or upstream file structures. The new provider file is scoped to HUAKAI's existing `internal/moderation` package and reuses HUAKAI's existing SSRF client rather than copying an upstream implementation.

## Execution Note

During implementation, Codex verified existing moderation migrations and found `moderation_log.decision` and `moderation_violation_events.decision` CHECK constraints currently omit `block_external`. The code can return `DecisionBlockExternal` and unit fakes can audit/auto-ban it, but DB-backed audit and violation-event inserts need an Owner-approved schema follow-up before PostgreSQL integration can be fully gate-ready. Codex did not change migrations in this pass because AGENTS marks database schema changes as high risk and the task said to prefer no migration.
