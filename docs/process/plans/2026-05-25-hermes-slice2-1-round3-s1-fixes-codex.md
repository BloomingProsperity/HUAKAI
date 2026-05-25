# 2026-05-25 Hermes Slice 2.1 Round 3 S1 Fixes

| Owner directive | "你是 Hermes Slice 2.1 Round 3 S1 fix executor. Round 2 codex review 暴露 2 个 Round 1 漏检的 S1." |
| Scope | Fix only `backend/internal/hermes` runner client auth selection and `backend/cmd/gateway/routes.go` internal runner bootstrap/refresh identity checks. Tests may update existing Hermes and cmd/gateway test files. No frozen packages, no `credentialacq`, no `git add`, no commit. |
| Success criteria | Runner client defaults to HMAC during transition when HMAC credentials exist, uses JWT only when `HUAKAI_HERMES_CLIENT_AUTH_MODE=jwt`, falls back to JWT when only JWT credentials exist, and emits discriminating headers in tests. Bootstrap and refresh reject missing/zero `X-Hermes-Tenant` or `X-Hermes-User` before signing/refreshing JWTs, while complete signed requests return 200 and record audit. Requested Go and Python verification commands pass or blockers are reported with evidence. |
| Time estimate | 60-90 minutes including RED tests, implementation, race tests, and Python unittest discovery. |
| Blast radius | Existing files in `backend/internal/hermes` and `backend/cmd/gateway`; `backend/internal/{gatewayhttp,gateway,proto}` remain untouched. Handler changes affect internal runner auth only. |
| Failure modes | Auth mode fallback could keep preferring JWT and break transition HMAC runners; tests compare HMAC-vs-Bearer headers with dual credentials. Audit identity validation could read body JSON instead of signed headers and preserve the gap; tests omit/zero signed headers while body contains valid IDs. Refresh test could pass without exercising refresh; use a real signed old JWT and require audit insert on the success path. |
| Decision points | None expected. Stop if the fix requires schema, auth core, billing ledger, quota enforcement, real secrets, new runtime dependencies, frozen packages, or destructive actions. |
| Pre-execution checklist | Read `CLAUDE.md` #8/#14, `AGENTS.md`, existing staged Hermes code, `runner_client.go`, `runner_client_test.go`, `cmd/gateway/routes.go`, and `cmd/gateway/hermes_internal_test.go`. Capture pre-existing staged/unstaged status. |

## Concrete Execution Order

1. Add RED tests in `backend/internal/hermes/runner_client_test.go` for:
   - dual credentials + default/HMAC client mode sends HMAC headers and no Bearer token;
   - dual credentials + `HUAKAI_HERMES_CLIENT_AUTH_MODE=jwt` sends Bearer and no HMAC signature;
   - only HMAC credentials + no mode sends HMAC;
   - only JWT credentials + no mode sends Bearer.
2. Add RED tests in `backend/cmd/gateway/hermes_internal_test.go` for:
   - bootstrap signed with missing/zero tenant header returns 401 and does not record audit;
   - bootstrap signed with tenant/user headers returns 200 and inserts one audit row;
   - refresh signed with missing/zero tenant header returns 401 and does not refresh/audit;
   - refresh signed with tenant/user headers returns 200 and inserts one audit row.
3. Run targeted Go tests to confirm RED failures are caused by the two known S1 defects.
4. Implement minimal production changes:
   - add `HUAKAI_HERMES_CLIENT_AUTH_MODE` parsing to Hermes runner client config;
   - make HMAC the transition default whenever shared secret is present unless client mode is explicitly `jwt`;
   - reject invalid client mode values;
   - add a helper in `routes.go` to parse positive signed identity from `X-Hermes-Tenant`/`X-Hermes-User`;
   - require that identity before `IssueBootstrapJWT` and before `RefreshJWT`, and audit with header identity.
5. Run targeted tests, then requested verification:
   - `cd backend && export GOCACHE=/tmp/huakai-gocache && go build ./... && go vet ./... && go test ./internal/hermes/... ./cmd/gateway/... -count=1 -race`
   - `python3 -m unittest discover -s backend/deploy/hermes-runner -p 'test_*.py'`
6. Report before/after, verification evidence, mutation self-check, security/clean-room risk, and `git diff --stat`. Do not stage or commit.
