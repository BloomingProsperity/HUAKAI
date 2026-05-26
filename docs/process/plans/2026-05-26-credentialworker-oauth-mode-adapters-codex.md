# 2026-05-26 credentialworker OAuth mode adapters Codex plan

| Owner directive | "你是 Slice 2.6 credentialworker adapter registry 补 3 OAuth mode executor." |
| Scope | In: inspect credentialworker mode registry, add discriminating tests, register `gemini/oauth`, `antigravity/oauth`, and `windsurf/oauth` refresh executors. Out: upstream source reads, schema/auth/billing/quota/gateway/hermes changes, adapter API refactor, new runtime dependencies, git add/commit. |
| Success criteria | `DefaultModeAdapterRegistry()` covers every `credentialstore.DefaultHandlerRegistry()` mode; explicit lookups for the three OAuth mode keys succeed; a scheduler-style refresh of `windsurf/oauth` no longer records `adapter_missing`; targeted and required Go build/vet/test commands complete or any failure is reported with evidence. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass with TDD red/green verification. |
| Blast radius | Credential refresh scheduling for mode-keyed credentials. Incorrect behavior can leave refreshable credentials stuck in `adapter_missing` or accidentally attempt unsupported network refresh. |
| Failure modes | Weak tests that still pass when a registration is removed: use exact missing-key assertions and an `AccountCredentialRefresher` store that records `adapter_missing`. Unsupported Windsurf auto-refresh: implement manual-token safe equivalent that validates stored token shape and maps to `ErrNoRefreshRequired`, preserving saved payload until operator re-entry. OAuth refresh request regression: reuse existing Gemini/Antigravity adapters instead of adding new token exchange logic. |
| Decision points | No Owner sign-off needed unless implementation requires new dependencies, schema change, auth/billing/quota changes, or reading non-MIT upstream source. |
| Pre-execution checklist | 1. Read `CLAUDE.md` #8/#11/#14 and `AGENTS.md`. 2. Read `refresh_adapter.go`, `mode_refresh.go`, `scheduler.go`, current adapters, and `credentialstore/types.go`. 3. Confirm registry key shape and registration mechanism. 4. Run targeted failing tests before production edits. 5. Implement minimal registrations and Windsurf safe-equivalent adapter. 6. Run required verification. |
| Target packages | `backend/internal/credentialworker` existing files/tests: not frozen, under package budget. `backend/internal/credentialworker/adapters` new `windsurf.go`: not frozen, under package budget. |

Concrete execution order:

1. Run `cd backend && export GOCACHE=/tmp/huakai-gocache && go test ./internal/credentialworker -run 'TestDefaultModeAdapterRegistryCoversCredentialStoreModes|TestDefaultModeAdapterRegistryRoutesSlice26OAuthModes|TestModeRefreshWorkerFindsWindsurfOAuthAdapter' -count=1` and capture current red evidence.
2. Add tests in `backend/internal/credentialworker/mode_refresh_test.go`:
   - exact lookup assertions for `gemini/oauth`, `antigravity/oauth`, `windsurf/oauth`;
   - a scheduler/refresher regression using `windsurf/oauth` that records failure if the adapter is absent.
3. Add `backend/internal/credentialworker/adapters/windsurf.go` with a small manual-token adapter that validates an existing token field and reports the no-auto-refresh safe equivalent; map that sentinel to `ErrNoRefreshRequired` in the credentialworker mode adapter.
4. Update `DefaultModeAdapterRegistry()` in `backend/internal/credentialworker/mode_refresh.go` with explicit registrations for the three mode keys:
   - `gemini/oauth` -> existing `GeminiRefresh`;
   - `antigravity/oauth` -> existing `AntigravityRefresh`;
   - `windsurf/oauth` -> new manual-token safe-equivalent adapter.
5. Run targeted tests, then required `go build`, `go vet`, and race tests.
