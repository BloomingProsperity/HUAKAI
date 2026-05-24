# 2026-05-24 Vendor Refreshers Wiring - Codex independent plan

| Owner directive | "把 5 个 vendor refresher 接入 credentialworker.Scheduler.vendorRefreshers map (cursor / windsurf / openai_codex / kiro / gemini)。anthropic + copilot 已在 S2 切片接通。" |
| --- | --- |
| Scope | In: HUAKAI-internal wiring only for `credentialworker.Scheduler` vendor refreshers; optional env-backed config for Cursor, Windsurf, OpenAI Codex, Kiro, and Gemini operator OAuth settings; focused mutation-discriminating tests for scheduler routing and empty-config skip behavior. Out: vendor refresher implementation changes, audit/schema/migration changes, auth core, billing ledger, quota enforcement, production secrets, git staging/commit/push, and reference-project source reading. |
| Success criteria | `cmd/gateway` startup adds `WithVendorRefresher` options for configured `cursor`, `windsurf`, `openai_codex`, `kiro`, and `gemini`; empty operator config does not inject the vendor refresher and does not crash startup; scheduler routes a cursor refresh row to the cursor-specific refresher; an unconfigured cursor row uses the default refresher; requested build/tests pass or failures are reported honestly; at most two Codex review rounds are run if the CLI supports the requested mode. |
| Time estimate | 60-90 minutes wall clock, mostly RED/GREEN tests, build/race tests, and review command runtime. |
| Blast radius | Gateway boot wiring and credential refresh dispatch only. No database schema, migrations, audit ledger format, billing/quota/auth core, or vendor refresh algorithm changes. Mis-wiring would affect background token refresh for the five vendors while leaving request routing untouched. |
| Failure modes | Wrong vendor key sends a refresh request to fallback or another vendor; mitigation: cursor-specific scheduler spy test asserts exact account ID and default spy remains unused. Empty config could still inject a zero-value adapter and fail every refresh attempt closed instead of falling back; mitigation: config/wiring test asserts no configured refresher is returned when TokenURL is blank, plus scheduler fallback test covers the absence path. Kiro requires `ClientSecret` while the initial field list omits it; mitigation: Owner decision point below before implementation. Existing dirty Gemini changes may alter compile/test results; mitigation: do not overwrite those files and report if they affect verification. |
| Decision points | D1: Kiro's current `RefreshAdapter` requires `ClientSecret`; I recommend extending `VendorOAuth` with optional `ClientSecret` and env vars for vendors that need confidential clients (`HUAKAI_KIRO_OAUTH_CLIENT_SECRET`, optional `HUAKAI_GEMINI_OAUTH_CLIENT_SECRET`) while preserving the requested TokenURL/ClientID/Scope/AuthURL fields. D2: choose env names as `HUAKAI_<VENDOR>_OAUTH_{AUTH_URL,TOKEN_URL,CLIENT_ID,CLIENT_SECRET,SCOPE}` with `<VENDOR>` = `CURSOR`, `WINDSURF`, `OPENAI_CODEX`, `KIRO`, `GEMINI`. D3: if `codex exec review` requires staging, Owner said do not `git add`; use closest uncommitted read-only review without staging and report any CLI mismatch. |
| Pre-execution checklist | Confirm this is Codex's independent plan and do not read any sibling Claude plan for this descriptor; confirm worktree dirtiness and protect existing Gemini edits; write failing tests before production code; confirm no new files are added under frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`; keep edits to existing files unless a non-frozen test file is clearly needed; run requested build/tests; run per-commit review gate without staging/commit/push. |

## Design

Use a small gateway wiring helper rather than putting provider imports inside `credentialworker`. `credentialworker` should keep only dispatch behavior; `cmd/gateway` already owns production construction and already imports Anthropic/Copilot provider packages.

Add optional config fields in `backend/internal/config.Config`:

- `type VendorOAuth struct { AuthURL, TokenURL, ClientID, ClientSecret, Scope string }`
- `CursorOAuth VendorOAuth`
- `WindsurfOAuth VendorOAuth`
- `OpenAICodexOAuth VendorOAuth`
- `KiroOAuth VendorOAuth`
- `GeminiOAuth VendorOAuth`

Load env vars without making them required. `HUAKAI_DATABASE_URL` remains the only existing required setting in this slice. Empty vendor operator config means the refresher is absent from the Scheduler vendor map; the scheduler then follows the existing default `Refresher` fallback.

In `backend/cmd/gateway/wiring.go`, add a helper that builds vendor-specific `credentialworker.Option` values only when the relevant operator config is minimally complete:

- Cursor/Windsurf: require `TokenURL` and `ClientID`; include `Scope` when present.
- OpenAI Codex: require `AuthURL`, `TokenURL`, `ClientID`, and `Scope` because its bootstrap validation treats `AuthURL` as the device authorization endpoint and requires scope.
- Gemini: require `AuthURL`, `TokenURL`, `ClientID`, and `Scope`; pass optional `ClientSecret`.
- Kiro: require `TokenURL`, `ClientID`, and `ClientSecret`; `AuthURL`/`Scope` are preserved in config but not needed by the current refresh adapter.

Append the helper output to the existing scheduler options, alongside the always-wired Anthropic and Copilot refreshers.

## File Scope And Structure Check

- Modify existing `backend/internal/config/config.go`; non-frozen package, no new file.
- Modify existing `backend/internal/config/config_test.go`; non-frozen package, no new file.
- Modify existing `backend/cmd/gateway/wiring.go`; command package, not one of the frozen packages. Startup wiring exception applies.
- Add or modify tests under existing non-frozen packages:
  - Prefer modifying `backend/internal/credentialworker/scheduler_test.go` for the cursor route/fallback checks.
  - Prefer adding/modifying a `backend/cmd/gateway/*_test.go` wiring test only if existing test files do not already cover gateway wiring helpers; `cmd/gateway` is not frozen.
- Do not modify `backend/internal/provider/{cursor,windsurf,openai_codex,kiro,gemini}/refresher.go` except if compilation proves the existing public constructors require a tiny compatibility call-site adjustment. Current plan expects no provider refresher implementation changes.

## Concrete Execution Order

1. RED: add a scheduler test in `backend/internal/credentialworker/scheduler_test.go` showing `WithVendorRefresher("cursor", cursorRef)` routes a `VendorName="cursor"` row to `cursorRef` and does not call default. Mutation self-check: changing the registration key to another vendor or breaking `refresherForAccount` vendor lookup must make it fail.

2. RED: add a config load test in `backend/internal/config/config_test.go` proving vendor env vars populate `VendorOAuth` fields exactly and absent vendor env vars leave them empty without changing required `HUAKAI_DATABASE_URL` behavior.

3. RED: add a gateway wiring helper test showing `CursorOAuth.TokenURL == ""` produces no cursor vendor option/configured entry. Pair it with the scheduler fallback test for a cursor row with no cursor option, so the protected risk is visible: empty operator config must not install a broken zero-value adapter.

4. GREEN: extend `internal/config` with `VendorOAuth`, the five config fields, and env loading helpers using trimmed env values.

5. GREEN: add gateway wiring helper(s) in `cmd/gateway/wiring.go`:
   - Convert `runtimeconfig.VendorOAuth` to provider refresh adapters.
   - Build store adapters using existing provider `NewCredentialStoreAdapter(credentialStore)` helpers.
   - Return only configured vendor refreshers.
   - Append those options into `credentialworker.NewScheduler(...)`.

6. Run focused tests after each GREEN step:
   - `cd backend && GOCACHE=/tmp/go-build go test ./internal/credentialworker/... ./internal/config/... -count=1`
   - targeted `go test ./cmd/gateway -run 'Test.*Vendor.*Refresh|Test.*OAuth' -count=1` if a gateway test is added.

7. Mutation self-checks before final claim:
   - Temporarily damage cursor registration key or vendor lookup and verify the cursor routing test fails; revert immediately.
   - Temporarily force the empty-config helper to return a cursor refresher with blank config and verify the empty-config test fails; revert immediately.

8. Final verification:
   - `cd backend && GOCACHE=/tmp/go-build go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build go test ./internal/credentialworker/... ./internal/config/... -count=1 -race`
   - Run any added `cmd/gateway` targeted tests.
   - Run `codex exec review --uncommitted --full-auto --sandbox read-only` from repo root if available. Run round 2 only for unresolved S0/S1 or material behavior/test changes. Do not `git add`, commit, or push.

## Clean-Room Boundary

Lane: implementer. This plan uses only HUAKAI-internal code and the Owner-provided contract. No reference-project source is read, quoted, copied, or translated. No upstream function names, struct fields, comments, schemas, UI source, or distinctive file structures are introduced.

Source files read: `AGENTS.md`; `CLAUDE.md`; `docs/RULES.md`; `backend/internal/credentialworker/scheduler.go`; `backend/internal/credentialworker/options.go`; `backend/internal/credentialworker/types.go`; `backend/internal/credentialworker/scheduler_test.go`; `backend/cmd/gateway/config.go`; `backend/cmd/gateway/wiring.go`; `backend/internal/config/config.go`; `backend/internal/config/config_test.go`; `backend/internal/provider/cursor/bootstrap.go`; `backend/internal/provider/cursor/refresher.go`; `backend/internal/provider/windsurf/bootstrap.go`; `backend/internal/provider/windsurf/refresher.go`; `backend/internal/provider/openai_codex/bootstrap.go`; `backend/internal/provider/openai_codex/refresher.go`; `backend/internal/provider/kiro/bootstrap.go`; `backend/internal/provider/kiro/refresher.go`; `backend/internal/provider/gemini/bootstrap.go`; `backend/internal/provider/gemini/refresher.go`.
Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-24T00:00:00Z

## Cross-Discussion Status

This is Codex's independent plan. I did not read a Claude sibling plan for this descriptor. Per the project parallel-draft rule, execution should wait for Owner approval or an approved synthesized plan.
