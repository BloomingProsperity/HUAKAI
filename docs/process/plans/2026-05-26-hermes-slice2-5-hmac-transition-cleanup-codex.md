# 2026-05-26 Hermes Slice 2.5 HMAC Transition Cleanup Codex Plan

| Owner directive | "你是 Slice 2.5 HMAC transition cleanup executor... HMAC fallback 路径按 synthesis plan §A 共识 '1 release transition + Slice 2.5 cleanup' 应清理." |
| Scope | In: remove Hermes runner HMAC fallback from Go runner client, Python runner middleware, runner entrypoint, gateway bootstrap/refresh wiring, focused tests, and Slice 2.5 deferred-ticket state. Out: schema/sqlc, frozen package new files, non-MIT reference source reads, hermeschat/hermeshttp/credentialacq, hash-lock implementation, git add/commit. |
| Success criteria | `RunnerClient` is JWT-only and fail-closed on missing `HUAKAI_HERMES_JWT_PRIVATE_KEY_PATH` or `HUAKAI_HERMES_JWT_KID`; runner middleware accepts only Bearer JWT; entrypoint validates only JWT key material; gateway internal runner bootstrap/refresh no longer verifies legacy HMAC; HMAC-only tests are removed or rewritten; requested Go/Python/shell verification commands produce usable evidence. |
| Time estimate | 60-120 minutes wall clock in this Codex session, mostly code/test update plus verification. |
| Blast radius | Hermes runner authentication between gateway and runner. A mistake can break runner chat/bootstrap/refresh calls, leave an HMAC bypass alive, or make deployments fail startup. |
| Failure modes | Incomplete symbol removal leaves dead HMAC route or compile errors; mitigation: `rg` for HMAC/auth-mode symbols after edits. Weak test passes despite fallback still alive; mitigation: add discriminating tests that set legacy HMAC env/config and assert JWT-only behavior. Entrypoint rejects valid public-key directory deployments; mitigation: preserve existing JWT public-key-dir and path+kid validation. Deferred docs over-closed; mitigation: only close the HMAC cleanup ticket, leave hash-lock and unresolved S2 tickets documented. |
| Decision points | No new Owner decision expected. Stop if implementation requires schema/sqlc, auth/billing/quota core, adding runtime dependencies, changing frozen-package file structure, or touching real secrets. |
| Pre-execution checklist | 1. Read `CLAUDE.md` #8/#13/#14, `AGENTS.md`, and synthesis §A. 2. Confirm worktree status. 3. Search current HMAC/JWT symbols. 4. Write this plan before implementation. 5. Add failing/discriminating tests before production changes where practical. 6. Edit only scoped files. 7. Run requested verification commands. |

## File Scope

- Modify `backend/internal/hermes/runner_client.go`: remove HMAC config/env/auth-mode/signature/verifier code; require JWT private key and kid.
- Modify `backend/internal/hermes/types.go`: remove `SharedSecret` and `ClientAuthMode` from `RunnerConfig` if only used by removed HMAC transition path.
- Modify `backend/internal/hermes/runner_client_test.go`: replace HMAC/dual-mode tests with JWT-only fail-closed and legacy-env ignored/rejected coverage.
- Delete `backend/internal/hermes/runner_canonical_hmac_test.go`: HMAC-only canonical/verifier tests no longer describe released behavior.
- Modify `backend/deploy/hermes-runner/main.py`: remove HMAC middleware helpers and auth mode switch; verify JWT for all non-health requests.
- Modify `backend/deploy/hermes-runner/test_main_auth.py`: remove HMAC mode tests; keep/add JWT-only entrypoint and middleware tests.
- Modify `backend/deploy/hermes-runner/entrypoint.sh`: remove auth-mode/HMAC branch; require JWT public key material.
- Modify `backend/deploy/hermes-runner/Dockerfile` only if HMAC transition comments/env remain.
- Modify `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, `backend/cmd/gateway/hermes_internal_test.go`: remove `hermesRunnerSharedSecret` and route HMAC verifier usage while preserving JWT bootstrap/refresh behavior.
- Modify docs/process deferred tickets only where directly tied to Slice 2.5 HMAC cleanup or SSE disconnect status. Keep hash-lock deferred ticket and do not change `requirements.txt`.

No new files will be added under frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`. This plan adds only this docs/process plan file and may delete the scoped HMAC-only test file per Owner instruction.

## Execution Order

1. Inspect exact HMAC references in scoped files and note before-state counts.
2. TDD red step: add or update focused Go/Python tests so legacy HMAC env/config does not authenticate or select HMAC, and missing JWT config fails closed.
3. Run focused tests to confirm the new tests fail against the current fallback implementation.
4. Remove Go runner-client HMAC config, signer, verifier, auth-mode resolver, and related struct fields.
5. Remove Python runner HMAC middleware and entrypoint auth-mode/HMAC validation.
6. Remove gateway wiring/routes HMAC verifier branches and update gateway tests to assert bootstrap/refresh JWT behavior without HMAC proof.
7. Remove HMAC-only tests and update docs/tickets for HMAC cleanup closure; leave hash-lock and unresolved S2 items deferred if not safely fixable.
8. Run `rg` for removed symbols and fix remaining scoped references.
9. Run requested verification:
   - `cd backend && export GOCACHE=/tmp/huakai-gocache && go build ./... && go vet ./... && go test ./internal/hermes/... ./cmd/gateway/... -count=1 -race`
   - `cd backend/deploy/hermes-runner && python3 -m unittest discover -p 'test_*.py'`
   - `sh -n backend/deploy/hermes-runner/entrypoint.sh`
10. Report before/after, `git diff --stat`, and verification evidence. Do not `git add` or commit.

## Test Quality Self-Check

- Go fail-closed test guards the defect "legacy HMAC-only config still constructs an authenticated client"; mutation: if `NewRunnerClient` accepts `SharedSecret` without JWT key/kid, test must fail.
- Go request test guards the defect "client still emits HMAC signature when legacy env/config is present"; mutation: if `authenticate` calls HMAC signing or suppresses Bearer JWT, test must fail.
- Python middleware test guards the defect "setting HMAC env or auth mode bypasses JWT"; mutation: if middleware branches to `_valid_signature`, HMAC-only request must return non-401 and fail the test.
- Entrypoint test guards the defect "container can start with only HMAC secret"; mutation: if HMAC branch remains, subprocess exits 0 or misses the JWT error and test fails.
