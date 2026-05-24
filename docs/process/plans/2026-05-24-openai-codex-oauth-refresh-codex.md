# 2026-05-24 openai_codex OAuth refresh Codex plan

| Owner directive | "HUAKAI 账号转 API, OpenAI Codex CLI vendor (provider code: openai_codex)... 任务范围 (mirror cursor + windsurf 模式, 但带真 OAuth flow)..." |
| Scope | In: plan and later implementation for `backend/internal/provider/openai_codex/` bootstrap, refresher, credential-store adapter, focused registration in `backend/internal/credentialacq/vendor_exchangers.go`, and mutation-quality tests. Out: `scheduler.go`, DB schema, auth core, quota/billing, production deployment, git add/commit/push. |
| Success criteria | New provider package builds; focused package tests pass under `-race`; refresh endpoint, client ID, and scope are accepted only from operator config; credential payload cannot override token endpoint; 401/429/403-risk classifications produce audit outcomes `auth_expired`, `rate_limit_exceeded`, and `risk_control_triggered`; device-code exchanger alias exists if absent. |
| Time estimate | 60-90 minutes wall clock for implementation + tests after execution approval; 10-15 minutes for review/verification. |
| Blast radius | Medium. Touches credential acquisition and refresh behavior for OpenAI Codex CLI credentials, but stays in a new provider package and one existing registry file. Failure could break Codex credential acquisition/refresh or silently weaken audit classification. |
| Failure modes | Endpoint spoofing through credential payload: tests inject an evil credential endpoint and assert fail-closed/no call. Non-discriminating tests: HTTP status fixtures assert exact saved failure class and `auth.RefreshAuditOutcomeFromError`. Clean-room leakage: use OAuth behavior evidence and constants only, no copied implementation structure. Package budget violation: add files only under new `backend/internal/provider/openai_codex`, not frozen packages. Existing registry mismatch: if local credential naming uses `openai/codex_cli_oauth`, add alias without removing the existing registration. |
| Decision points | Owner/Claude synthesis gate: AGENTS requires parallel plans for non-trivial work. Codex can execute after Owner confirms this Codex plan is the execution plan or provides the synthesized plan. If implementation needs schema/auth/billing/quota/scheduler edits, stop for Owner confirmation. If no source-backed device-code endpoint is found, keep bootstrap operator-config-only fail-closed instead of inventing defaults. |
| Pre-execution checklist | Read `docs/RULES.md`; read skills; inspect Cursor/Windsurf/Copilot patterns; verify reference licenses; collect OAuth evidence from `~/refs/openai-codex` and `~/refs/CLIProxyAPI`; run TDD red tests first; implement minimal code; run requested build/test; run at most two uncommitted review rounds if CLI permits without staging; do not git add/commit/push. |

## Evidence Snapshot

- `~/refs/openai-codex` is Apache-2.0 licensed at local `LICENSE`; HEAD observed as `6a225e400520`.
- `~/refs/CLIProxyAPI` local export has MIT `LICENSE`; it is not a git checkout in this workspace, so citations can only be local file:line unless Owner provides the git checkout path.
- OpenAI Codex CLI default issuer is `https://auth.openai.com`: `/home/codex/refs/openai-codex/codex-rs/login/src/server.rs:54`.
- OpenAI Codex CLI derives browser authorization and token endpoints from that issuer: `/home/codex/refs/openai-codex/codex-rs/login/src/server.rs:518` and `/home/codex/refs/openai-codex/codex-rs/login/src/server.rs:729`.
- OpenAI Codex CLI device-code start/poll endpoints are issuer-derived account API paths: `/home/codex/refs/openai-codex/codex-rs/login/src/device_code_auth.rs:162-166` and `/home/codex/refs/openai-codex/codex-rs/login/src/device_code_auth.rs:179-196`.
- OpenAI Codex CLI client ID is observed at `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:921`.
- OpenAI Codex CLI browser-login scope is observed at `/home/codex/refs/openai-codex/codex-rs/login/src/server.rs:491-499`; refresh in current upstream uses client ID and refresh token but no scope at `/home/codex/refs/openai-codex/codex-rs/login/src/auth/manager.rs:806-824`.
- CLIProxyAPI confirms the same auth/token endpoints and client ID at `/home/codex/refs/CLIProxyAPI/internal/auth/codex/openai_auth.go:24-26`, with authorization scope at lines 68-78 and refresh scope at lines 191-198.

## Intended File Changes

- Create `backend/internal/provider/openai_codex/bootstrap.go`: `DefaultOAuthConfig`, `OAuthConfig`, `ValidateOAuthConfig`, and device-code endpoint helpers. Default must be operator-config fail-closed unless the value is explicitly backed by accepted evidence and Owner accepts embedding it.
- Create `backend/internal/provider/openai_codex/refresher.go`: provider-aware refresher following Cursor/Windsurf store transaction shape, but classification must call `auth.ClassifyRefreshError` or `credentialworker.ClassifyRefreshError` and wrap returned errors with `auth.WithRefreshAuditOutcome`.
- Create `backend/internal/provider/openai_codex/credential_store_adapter.go`: bridge `credentialstore.Store` into the new refresher without duplicating store logic.
- Create focused tests under `backend/internal/provider/openai_codex/`: bootstrap validation tests, refresh mutation tests, and store adapter nil-store guard tests.
- Modify `backend/internal/credentialacq/vendor_exchangers.go`: add `openai_codex/device-code` and/or `openai_codex/device_code` alias only if absent; preserve existing `openai/codex_cli_oauth` registration.

## Concrete Execution Order

1. Write red tests for bootstrap fail-closed behavior and operator-config merge.
2. Run the focused bootstrap test and confirm it fails for missing package/types.
3. Implement `bootstrap.go` with no invented endpoint defaults.
4. Run the bootstrap test to green.
5. Write red refresh tests for credential endpoint SSRF defense and 401/429/403-risk audit outcomes.
6. Run the focused refresh tests and confirm they fail for missing refresher.
7. Implement `refresher.go` and `credential_store_adapter.go` with narrow copies of local HUAKAI patterns, not reference code.
8. Run focused refresh tests to green.
9. Write/adjust red registration test for `openai_codex/device-code` lookup if no existing coverage catches it.
10. Add the registry alias and run `go test ./internal/credentialacq/... -count=1` if the modified package needs it.
11. Run requested checks:
    - `cd backend && GOCACHE=/tmp/go-build go build ./internal/provider/openai_codex/...`
    - `cd backend && GOCACHE=/tmp/go-build go test ./internal/provider/openai_codex/... -count=1 -race`
12. Run `codex exec review --uncommitted --full-auto --sandbox read-only` if available without staging; normalize S0/S1 and fix within the two-round cap. If the CLI requires staging, report that Owner's "do not git add" blocked the canonical review command.

## Clean-Room Controls

- Source reads are limited to Apache-2.0 OpenAI Codex and MIT CLIProxyAPI OAuth/device-code evidence.
- No LGPL/AGPL/GPL reference projects are read for this task.
- Implementation uses HUAKAI Cursor/Windsurf/Copilot local patterns and Go standard library behavior, not upstream reference structure.
- Endpoint/client/scope values are treated as protocol/operator configuration inputs, with source citations recorded in the report.

## Pre-Execution Self-Check

- No frozen package file additions: yes, new files target `backend/internal/provider/openai_codex`.
- No high-risk files: yes, no `LICENSE`, secrets, DB schema, billing/quota/auth core, deployment, or scheduler edits planned.
- TDD required: yes, red tests first for bootstrap, refresh, and registry alias.
- Owner gate remaining: yes, execution waits for Owner/synthesized-plan approval under AGENTS.
