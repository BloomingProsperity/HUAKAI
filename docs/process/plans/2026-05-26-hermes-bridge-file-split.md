# 2026-05-26 Hermes Bridge File Split

| Owner directive | "你是 Slice 2.4 hygiene executor: 拆分 backend/internal/hermeschat/bridge.go (629 LoC 混 5 责任) into focused files per DEFERRED-hermes-bridge-file-split.md." |
| Scope | In: pure file split inside non-frozen package `backend/internal/hermeschat`; keep behavior unchanged; delete `docs/process/reviews/DEFERRED-hermes-bridge-file-split.md` after split. Out: sqlc, SQL, migrations, Hermes service layer, hermes-runner Python, frozen packages `backend/internal/{gatewayhttp,gateway,proto}`, non-MIT upstream source. |
| Success criteria | `bridge.go` contains only Bridge construction/top-level streaming/public helpers and is <=200 LoC; focused files exist for request, SSE, persistence, and audit responsibilities; no behavior changes except visibility/import adjustments required by the split; requested build/vet/race test command exits 0; before/after LoC evidence is recorded. |
| Time estimate | 30-60 minutes wall clock; one Codex execution pass plus verification. |
| Blast radius | Compile failures from missing imports or moved private helpers; accidental behavior drift in request preparation, SSE handling, persistence, or audit fallback. Mitigation: move code verbatim, run `gofmt`, run requested checks. |
| Failure modes | Import drift after moving functions; cyclic or missing symbols; `bridge.go` remains over size budget; deferred ticket deletion missed; existing tests reveal hidden dependency on file-local ordering. Mitigation: inspect function boundaries, keep package-private visibility unless tests require otherwise, verify LoC and full requested command. |
| Decision points | None expected. Stop for Owner confirmation only if execution would require high-risk files, SQL/schema, auth/billing/quota core, frozen package changes, new runtime dependency, or behavior change. |
| Pre-execution checklist | Read `CLAUDE.md` #13/#14; read `AGENTS.md` Package & File Structure Discipline and Test Quality Discipline; read `docs/process/reviews/DEFERRED-hermes-bridge-file-split.md`; capture current `bridge.go` LoC; confirm target package is not frozen; confirm worktree has no pre-existing diff. |

## Target Files

- `backend/internal/hermeschat/bridge.go`: keep `txRunner`, `auditDLQ`, `warningLogger`, options, `Bridge`, `NewBridge`, `Stream`, response/header/write helpers. Target package `hermeschat`, not frozen.
- `backend/internal/hermeschat/bridge_request.go`: move `Request`, `PreparedRequest`, request decoding/validation, conversation selection/creation, internal token injection. Target package `hermeschat`, not frozen.
- `backend/internal/hermeschat/bridge_sse.go`: move `streamState`, SSE event parsing, event filtering, token/done payload parsing. Target package `hermeschat`, not frozen.
- `backend/internal/hermeschat/bridge_persist.go`: move assistant message persistence and conversation touch logic, including Slice 2.3 Round 3 race guard behavior. Target package `hermeschat`, not frozen.
- `backend/internal/hermeschat/bridge_audit.go`: move audit savepoint, warning, and DLQ fallback helpers. Target package `hermeschat`, not frozen.
- `docs/process/reviews/DEFERRED-hermes-bridge-file-split.md`: delete after ticket closure.

## Execution Order

1. Use function boundaries from `bridge.go` to move request helpers into `bridge_request.go`.
2. Move SSE parsing/state helpers into `bridge_sse.go`.
3. Move persistence helper into `bridge_persist.go`.
4. Move audit helpers into `bridge_audit.go`.
5. Trim imports in each file, keep existing names and visibility unless required by package compilation.
6. Run `gofmt` on the touched Go files.
7. Delete the deferred review ticket.
8. Record after LoC for each file.
9. Run `cd backend && export GOCACHE=/tmp/huakai-gocache && go build ./... && go vet ./... && go test ./internal/hermeschat/... ./internal/hermeshttp/... -count=1 -race`.

## Notes

- No non-MIT reference source is needed or read for this hygiene split.
- No new tests are planned because behavior must not change; existing discriminating tests remain the verification surface.
