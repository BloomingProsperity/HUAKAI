# 2026-05-25 Hermes Slice 2.1 Round 2 S1 Fixes

| Owner directive | "你是 Hermes Slice 2.1 Round 2 fix executor. Round 1 codex review 给出 3 个 S1 finding, 必须全部修复." |
| Scope | Fix only Hermes JWT refresh/verifier behavior and `backend/deploy/hermes-runner/Dockerfile`; add discriminating tests in `backend/internal/hermes` and `backend/deploy/hermes-runner`. No `credentialacq`, no frozen packages, no git add/commit. |
| Success criteria | Dockerfile copies `jwt_verify.py`; custom issuer/audience are accepted consistently by Go refresh and Python verifier; default issuer token is rejected when custom env/config is active; refresh is allowed only when remaining lifetime is <= `JWTRefreshLead`; required Go and Python commands pass or blockers are reported. |
| Time estimate | 45-60 minutes including RED test runs and full requested verification. |
| Blast radius | `backend/internal/hermes/{jwt.go,runner_bootstrap.go,bootstrap_test.go}`; `backend/deploy/hermes-runner/{Dockerfile,jwt_verify.py,test_jwt_verify.py}`. `backend/internal/hermes` is not frozen; runner deploy files are in the allowed slice scope. |
| Failure modes | Non-discriminating tests could miss the Round 1 defects; mitigate with RED runs before implementation and mutation reasoning for each test. Env drift could remain if only direct `verify_token` is fixed; mitigate by testing `PublicKeyCache.verify()`. Refresh lead could be off by one; define policy as reject when `exp > now + JWTRefreshLead`, allow equality and nearer expiry. |
| Decision points | None expected. Stop if a fix requires credentials, schema/auth/billing/quota changes, `LICENSE`, frozen packages, or runtime dependency changes. |
| Pre-execution checklist | Read `CLAUDE.md` #8/#14, `AGENTS.md` review/test/package rules, current Hermes JWT code, runner Dockerfile, and existing Go/Python tests. |

## Execution Order

1. Add RED tests:
   - Go refresh with custom issuer/audience passes near expiry and rejects an otherwise valid default-issuer token under custom config.
   - Go refresh rejects a token with more than `JWTRefreshLead` remaining and accepts one with one minute remaining.
   - Python `PublicKeyCache.verify()` reads custom issuer/audience env and rejects default issuer/audience under custom env.
   - Runner Dockerfile test fails if `jwt_verify.py` is omitted from `COPY`.
2. Run targeted Go/Python tests to confirm expected RED failures.
3. Implement minimal code:
   - Add issuer/audience-aware Go claim validation and use it from `BootstrapIssuer.RefreshJWT`.
   - Enforce refresh lead window in `RefreshJWT`.
   - Read issuer/audience env in Python verifier when explicit arguments are absent.
   - Add `jwt_verify.py` to the Dockerfile copy set.
4. Run targeted tests, then full requested verification:
   - `cd backend && GOCACHE=/tmp/huakai-gocache go build ./... && go vet ./... && go test ./internal/hermes/... ./cmd/gateway/... -count=1 -race`
   - `python3 -m unittest discover -s backend/deploy/hermes-runner -p 'test_*.py'`
