# 2026-06-01 S1-019 Release Mode Gate

| Owner directive | "Fix audit finding S1-019 to high quality and push it for review." |
| Scope | In: HUAKAI gateway startup release-mode validation, existing gateway release-mode tests, smoke harness env, backend Dockerfile env documentation. Out: auth core, billing/quota ledgers, database schema, reference-project behavior mining. |
| Success criteria | Missing or unrecognized `HUAKAI_RELEASE_MODE` fails at gateway startup validation; explicit `dev`, `development`, `test`, and `production` remain recognized; trust-chain production gates still key off explicit `production`; a discriminating Go test fails if missing mode is allowed again; `go build ./...` and `go test ./cmd/gateway/...` pass. |
| Time estimate | 30-45 minutes wall time; one focused Codex work unit. |
| Blast radius | Gateway process startup only. Existing helper-level dev tests may need explicit dev mode in fixtures. No runtime dependency, schema, auth, billing, or quota mutation. |
| Failure modes | Local/dev workflows may omit release mode and fail startup; mitigate by allowing explicit `dev`/`development`/`test` and documenting `HUAKAI_RELEASE_MODE` as required. Tests could become non-discriminating; mitigate by asserting empty mode errors while explicit dev succeeds. |
| Decision points | None for this worker: Owner delegated autonomous S1-019 fix. No reference-project comparison needed because this is HUAKAI-internal audit behavior only, with no reference behavior claim. |
| Pre-execution checklist | 1. Read `CLAUDE.md`, `AGENTS.md`, and coordination rules. 2. Load S1-019 row from audit files. 3. Confirm current code still permits omitted release mode. 4. Claim target files via `.coordination/`. 5. Add failing test before production code. 6. Implement minimal existing-file patch. 7. Run focused tests, full build, self-review, commit, push. |

## Target Files

| File | Status | Frozen package check |
| --- | --- | --- |
| `backend/cmd/gateway/config.go` | Existing file | Not in frozen package. |
| `backend/cmd/gateway/release_mode_test.go` | Existing file | Not in frozen package. |
| `backend/cmd/gateway/smoke_test.go` | Existing file | Not in frozen package. |
| `backend/Dockerfile` | Existing file | Not in frozen package. |
| `docs/process/plans/2026-06-01-s1-019-release-mode-codex.md` | New plan artifact | Docs only, not a frozen package. |

## Execution Order

1. Update the release-mode test so empty `HUAKAI_RELEASE_MODE` is rejected and explicit dev/test modes remain accepted.
2. Run the focused test and confirm it fails against current code.
3. Change `validateReleaseMode` to require an explicit recognized value.
4. Make the smoke subprocess launch with explicit dev mode.
5. Update Dockerfile comments to list `HUAKAI_RELEASE_MODE` as required.
6. Run focused tests and `go build ./...`.
7. Stage, run Codex self-review best effort, fix S0/S1 if any, commit, and push `work/s1-019`.
