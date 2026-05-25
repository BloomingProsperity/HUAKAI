# 2026-05-19 Wave 2-A Chat Completions Handler Refactor
| Field | Content |
| --- | --- |
| Owner directive | "你是 HUAKAI codex executor lane, 任务 = Wave 2-A 拆 chat_completions_handler.go 大文件." |
| Scope | In: `backend/internal/gatewayhttp/chat_completions_handler.go` split within the same Go package, related `chat_completions_handler_*_test.go` test file organization, local build/test verification. Out: reference reverse-proxy source, frontend, Rust, `vendor/boring`, proto, pool, audit, billing implementation, `backend/cmd/gateway/main.go`, new runtime dependencies, public API changes. |
| Success criteria | Handler responsibilities are split into 4-6 focused files, each target file is no more than 300 lines, existing behavior is preserved, tests are only moved/renamed where needed, `cd backend && GOCACHE=/tmp/go-cache go build ./...` passes, and `cd backend && GOCACHE=/tmp/go-cache go test ./internal/gatewayhttp/... -race -count=1 -timeout 240s` passes. |
| Time estimate | 60-90 minutes wall clock; one Codex implementation pass plus verification. |
| Blast radius | Medium: request routing, auth, validation, provider dispatch, streaming, cache, billing settle, and audit receipt are on the same handler hot path. The refactor should be mechanical and same-package only, so compile-time checks should catch most symbol movement issues. |
| Failure modes | Missing import after split: mitigate with `gofmt`/`go test`. Behavior drift in handler branches: keep code blocks intact and move helpers by primary use. Race test timeout: run focused package first and report exact failure if environment-bound. Forbidden scope touch: verify `git diff --name-only` before final. |
| Decision points | Stop for Owner only if the refactor requires changing high-risk implementations in billing/audit/pool/proto/main or adding dependencies. No such change is expected. |
| Pre-execution checklist | Read `AGENTS.md` and `docs/RULES.md`; read the full target handler; read existing chat completions tests; record original line count; keep all moved code in `gatewayhttp` same package; avoid reference source; run build/test; report final file line counts. |
| Concrete execution order | 1. Split entry/deps/request structs and route constructors into main handler file. 2. Move body/protocol/model validation helpers to validation file. 3. Move routing, pool selection, credential resolution, non-stream dispatch, and channel-health helpers to dispatch file. 4. Move settle/audit/hash/model-chain helpers to billing file. 5. Move SSE and L2 cache helpers to stream file. 6. Move JSON/upstream error helpers to error file. 7. Reorganize tests by behavior without changing assertions. 8. Run gofmt, build, race tests, and line-count audit. |

Assumptions:

- Owner's current message is the start signal for execution.
- This is an internal same-package refactor, not a feature or schema change.
- Test logic should remain semantically unchanged; only file placement and imports may change.

Risks:

- The existing handler contains tightly interleaved non-stream and stream branches, so helper extraction may be safer than over-aggressive control-flow changes.
- `chat_completions_handler_headers.go` already owns model header helpers; it can remain as a related support file outside the six requested split files unless consolidation is necessary for line limits.
