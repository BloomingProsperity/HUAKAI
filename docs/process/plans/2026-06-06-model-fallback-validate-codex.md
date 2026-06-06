# 2026-06-06 model fallback validate

| Owner directive | "TASK: Add write-time TYPED validation for model_fallback_chains setting ... Reach CLOSURE. No shortcuts." |
| Scope | In: HUAKAI-native validation and tests for `model_fallback_chains` in `backend/internal/platformsettings`, plus required docs plan. Out: `/home/ubuntu/refs`, migrations, new endpoints, OpenAPI route changes, frozen-package new files, git commit. |
| Success criteria | `KeyModelFallbackChains` no longer uses generic object-only validation; invalid fallback configs fail at admin save through existing service validation; discriminating unit tests cover unknown buckets, non-string-array chains, self/cycle, max-depth bounds, and a valid config. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass with test-first red/green checks. |
| Blast radius | Platform setting writes and reads that normalize stored settings. Existing valid empty value and well-formed configs should remain accepted; malformed DB values can now be rejected during normalization instead of silently tolerated. |
| Failure modes | Import cycle if `platformsettings` imports `modelfallback` because runtime resolver already imports `platformsettings`; mitigation: inline a local typed parse struct in `platformsettings`. Overly broad validation could reject intentionally empty default; mitigation: keep empty string accepted. Incomplete cycle detection could leave silent runtime disablement; mitigation: test direct self-reference and two-node bucket cycle. |
| Decision points | No Owner sign-off needed unless validation touches high-risk files, changes schema, adds endpoint, adds runtime dependency, or requires edits in frozen packages. |
| Pre-execution checklist | 1. Read `docs/RULES.md`, `backend/internal/modelfallback/resolver.go`, `backend/internal/platformsettings/types.go`, `backend/internal/controlhttp/platformsettings_handler.go`. 2. Confirm import-cycle risk. 3. Add failing tests in existing `platformsettings` package. 4. Implement only `platformsettings` validation. 5. Run targeted package tests, requested package tests, `go build ./...`, and `go vet ./...` with `GOCACHE=/tmp/go-build`. 6. Stage `backend/` and `docs/` only if verification reaches expected state; do not commit. |

## Concrete execution order

1. Add `TestValidateModelFallbackChains_*` tests under `backend/internal/platformsettings`, with comments documenting the exact mutations they catch.
2. Run the targeted new tests and confirm they fail because current validation accepts malformed object-shaped configs.
3. Add a `validateModelFallbackChainsValue` path for `KeyModelFallbackChains`, keeping `KeyBudgetLimits` on `validateJSONObjectValue`.
4. Implement local typed JSON validation in `platformsettings`:
   - accept empty string as unset;
   - reject unknown top-level buckets;
   - parse only `enabled`, `max_depth`, `general`, `context_window`, and `content_policy`;
   - enforce `max_depth` either absent or explicitly within `1..10`;
   - enforce every chain map key and target model is non-empty after trimming;
   - enforce every chain value is a non-empty JSON string array;
   - reject direct self-reference and two-node bucket cycles.
5. Run targeted tests until green, then run:
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go test ./internal/platformsettings ./internal/modelfallback ./internal/controlhttp`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go build ./...`
   - `cd backend && GOCACHE=/tmp/go-build /usr/local/go/bin/go vet ./...`
6. Run `git status --short`, then stage `backend/ docs/` without committing.
