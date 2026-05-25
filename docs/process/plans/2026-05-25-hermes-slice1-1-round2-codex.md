# 2026-05-25 Hermes Slice 1.1 Round 2 Codex Fix Plan

| Owner directive | "你是 codex 修复 lane Round 2,修 Hermes Slice 1.1 的 5 个 S1 findings。" |
| Scope | In: Hermes runner env optional startup, tenant/user runner context signing, profile owner guard, chat enabled guard, OpenAPI route docs. Out: schema, billing/quota/auth core, runner-side verification, compose/deploy. |
| Success criteria | `cd backend && go build ./...`; `cd backend && go vet ./...`; `cd backend && go test ./cmd/gateway/...`; `cd backend && go test ./internal/hermes/... ./internal/hermeshttp/...`; no git add/commit. |
| Time estimate | 1-2 hours wall clock; one Codex repair pass plus verification. |
| Blast radius | Low-to-medium: existing staged Hermes files and route docs only. Non-Hermes gateway startup must remain unaffected when Hermes env is absent. |
| Failure modes | Env optionality could hide malformed partial config; mitigate by treating both env vars missing as disabled, partial or invalid env as error. Tenant/user headers could be spoofed if not signed; mitigate by including them in HMAC input. Disabled guard could block history reads; mitigate by checking enabled only in POST chat. OpenAPI drift could fail route consistency; mitigate with existing consistency test. |
| Decision points | None for this lane: Owner supplied exact Round 2 repair decisions. High-risk files are not touched. |
| Pre-execution checklist | 1. Inspect existing Hermes wiring/handlers/client/settings. 2. Confirm target packages are not frozen packages for any new test files. 3. Add focused regression tests where practical. 4. Patch implementation. 5. Run requested verification commands. |

Concrete execution order:
1. Add tests for runner env optionality, HMAC tenant/user headers, profile owner mismatch, and chat disabled denial where local interfaces allow clean coverage.
2. Update `backend/internal/hermes/runner_client.go`.
3. Update `backend/cmd/gateway/wiring.go` and `backend/cmd/gateway/routes.go`.
4. Update `backend/internal/hermes/settings.go` and `backend/internal/hermeshttp/chat_handler.go`/conversation calls.
5. Add Hermes OpenAPI path entries and tag.
6. Run the requested build/vet/test commands and report evidence plus `git diff --stat`.
