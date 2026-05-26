# 2026-05-26 Slice 2.5 Round 2 Internal HMAC S0/S1 Fix Codex Plan

| Owner directive | "你是 Slice 2.5 Round 2 S0 + S1 fix executor. Round 1 review 发现 cleanup 过激删了 internal route HMAC 验证, 留下 SSRF/credential forgery 漏洞." |
| Scope | In: restore required HMAC proof for `/internal/runner/bootstrap`, `/internal/runner/refresh`, and `/internal/keys`; keep chat path JWT-only; rename gateway internal shared-secret wiring to `HUAKAI_HERMES_INTERNAL_SHARED_SECRET`; fix dev compose JWT public-key material and internal shared-secret env; add discriminating Go tests and dev key generation docs/script. Out: hermes-runner Python chat path, frozen package new files, schema/sqlc, auth/billing/quota core, production secrets, git add/commit. |
| Success criteria | Missing HMAC and wrong HMAC return 401 on the three internal credential/key routes; valid HMAC reaches handler behavior and returns non-401/expected success where fixtures allow; tenant/user identity is sourced from authenticated signed headers; dev compose no longer uses removed chat `HUAKAI_HERMES_SHARED_SECRET` and has JWT public-key path, KID, internal shared secret, and documented dev key generation. |
| Time estimate | 60-120 minutes wall clock in this Codex session. |
| Blast radius | Gateway internal credential issuance and key refresh paths plus local dev boot configuration. A mistake can either reopen unauthenticated credential issuance or break dev/local bootstrap. |
| Failure modes | Tests pass without proving the deleted verifier defect; mitigation: red-run tests before production changes and use missing/wrong/valid HMAC fixtures. Env rename breaks existing config; mitigation: wire only internal route secret under the new env and document it in dev compose. Dev public key fixture accidentally includes private material; mitigation: commit public fixture only and add `.gitignore` for generated private keys. |
| Decision points | No new Owner decision expected. Stop before changing `LICENSE`, real secrets, database schema, deployment scripts beyond non-sensitive dev config/script, auth core outside the named gateway/hermes surfaces, billing/quota paths, or adding runtime dependencies. |
| Pre-execution checklist | 1. Read `CLAUDE.md` #8/#14 and `AGENTS.md`. 2. Confirm dirty worktree and avoid reverting user changes. 3. Inspect current route/wiring/hermes HMAC/JWT symbols. 4. Add failing tests before production edits. 5. Edit only scoped files. 6. Run requested verification and report evidence. |

## File Scope

- Modify existing `backend/cmd/gateway/routes.go`, `backend/cmd/gateway/wiring.go`, and gateway tests for internal-route HMAC enforcement.
- Modify existing `backend/internal/hermes/runner_client.go` and `backend/internal/hermes/types.go` only if needed to preserve/rename shared-secret semantics for internal routes.
- Modify `docker-compose.dev.yml` and docs for dev configuration.
- Add dev-only key material under a non-frozen deploy/dev path and a helper script under `scripts/dev/`; do not add files under frozen `backend/internal/{gatewayhttp,gateway,proto}`.

## Execution Order

1. Capture before-state with focused `rg` and file reads.
2. Add discriminating tests: missing HMAC -> 401, wrong HMAC -> 401, valid HMAC -> reaches route behavior for bootstrap, refresh, and keys.
3. Run focused gateway tests to verify the new tests fail against the current unauthenticated internal-route state.
4. Restore `hermes.VerifyRunnerHMACRequest` on the three internal routes and wire `HUAKAI_HERMES_INTERNAL_SHARED_SECRET` as mandatory for deployment.
5. Update dev compose JWT public-key env, KID, internal shared-secret env, public key fixture, private-key `.gitignore`, generator script, and README/docs note.
6. Run focused tests, then full requested Go verification and shell/grep compose sanity checks.

## Test Quality Self-Check

- Regression guarded: unauthenticated network caller can obtain or refresh runner credentials by spoofing `X-Hermes-Tenant` / `X-Hermes-User`.
- Mutation check: deleting the restored HMAC verifier must turn missing/wrong-HMAC tests red because they would become non-401.
- Fixture quality: missing, wrong, and valid signatures are all exercised against the same route set, so success is tied to HMAC proof rather than status-code coincidence.

