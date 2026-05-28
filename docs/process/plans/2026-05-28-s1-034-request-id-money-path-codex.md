# 2026-05-28 S1-034 Request ID Money Path Fix - Codex Plan

| Owner directive | "BUG (S1-034): The canonical request id used for billing/audit is taken from the client-controllable X-Request-Id header ... FIX ... money/audit MUST use a SERVER-generated request id; client X-Request-Id only as metadata; a duplicate must NOT drop a delivered charge ... Do NOT git add, commit, or push." |
| Scope | In: existing `backend/internal/gatewayhttp` validation, non-streaming audit/settlement handling, and existing gatewayhttp tests. In: verify `backend/internal/auditledger` duplicate lookup contract by reading code. Out: schema changes, auth/quota core changes, new runtime dependencies, new files in frozen packages, commits/pushes. |
| Success criteria | Two inbound requests with the same `X-Request-Id` produce different canonical money/audit `RequestID` values while preserving `ClientRequestID`. A duplicate audit ledger append in the non-streaming delivered path still settles a non-zero cost and returns success rather than aborting/500. Completion event metadata carries `client_request_id` when present. Response trust/request headers surface the server-generated canonical request id. Required `go test` and `go build` commands are run from `backend/`. |
| Time estimate | 45-90 minutes wall clock; one Codex session. |
| Blast radius | Money path, audit ledger references, trust headers, idempotency/replay metadata, non-streaming completion settlement. Incorrect handling can lose revenue, return wrong audit refs, or break client correlation headers. |
| Failure modes | Duplicate lookup could return missing/cross-tenant entry; mitigation: fail closed unless the existing ledger entry matches the current tenant. Metadata change could omit route id; mitigation: preserve `route_id` and add `client_request_id` only when non-empty. Tests could be non-discriminating; mitigation: include mutation comments and assert IDs, metadata, status, settle count, abort count, and non-zero/equal cost. Frozen package rule could be violated; mitigation: edit only existing files. |
| Decision points | None needing new Owner sign-off. Owner already chose server-generated audit id, client id as metadata, and duplicate replay tolerance. If implementing the full handler duplicate test proves impractical with the existing harness, use the closest discriminating variant and report the limitation as requested. |
| Pre-execution checklist | 1. Read `AGENTS.md`, `CLAUDE.md`, and `docs/RULES.md`. 2. Confirm this worktree is isolated. 3. Read the cited gatewayhttp/auditledger code paths. 4. Add failing tests first. 5. Run targeted tests to see the expected red. 6. Patch existing files only. 7. Run required verification from `backend/`. 8. Report full test output and changed files; do not stage, commit, or push. |

## File Scope

- Modify existing frozen-package implementation file: `backend/internal/gatewayhttp/chat_completions_validate.go`.
- Modify existing frozen-package implementation file: `backend/internal/gatewayhttp/chat_completions_billing.go`.
- Modify existing frozen-package implementation file: `backend/internal/gatewayhttp/chat_completions_handler.go` if needed to carry `ClientRequestID` through `chatExecution`.
- Modify existing frozen-package implementation file: `backend/internal/gatewayhttp/chat_completions_dispatch.go` if needed to initialize `chatExecution.ClientRequestID`.
- Modify existing test file: `backend/internal/gatewayhttp/chat_completions_validate_test.go`.
- Modify existing test file: `backend/internal/gatewayhttp/chat_completions_billing_test.go`.
- No new files under `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.

## Reference-Project Comparison On Decisions

No open product/architecture decision is being surfaced in this plan: the Owner has already selected the behavior. The task reads and changes HUAKAI-internal code only, so the source-must-read reference-project trigger does not apply.

## Execution Order

1. Add Test A in `chat_completions_validate_test.go`:
   - Build two requests with identical inbound `X-Request-Id: dup-1`.
   - Run through `middleware.RequestID` so the current buggy path adopts the client header.
   - Assert canonical `RequestID` values are non-empty and different.
   - Assert `ClientRequestID == "dup-1"` on both.
   - Mutation comment: reverting canonical id to `middleware.GetReqID(ctx)` makes both canonical IDs equal `dup-1`.
2. Add Test B in `chat_completions_billing_test.go`:
   - Use an audit ledger stub whose second `Append` returns `auditledger.ErrDuplicateRequestID` and whose `GetByRequestID` returns the first persisted entry for the second request id.
   - Drive two non-streaming completions through the existing handler harness.
   - Assert the second response is not HTTP 500, no abort is recorded, a second settlement occurs, and the second settled cost equals the first non-zero cost.
   - Mutation comment: keeping the current Abort+500 branch makes the second request fail and records no positive settlement.
3. Run targeted tests and confirm they fail for the intended reasons before production edits.
4. Implement:
   - Generate canonical `RequestID` with `uuid.NewString()` in validation.
   - Preserve `r.Header.Get("X-Request-Id")` in `ClientRequestID`.
   - Carry `ClientRequestID` into `chatExecution` and completion event metadata as `client_request_id`.
   - On duplicate audit append, call `GetByRequestID`, require tenant match, return `auditledger.PersistedLedgerResult(existing)`, and update envelope accounting fields from the existing entry.
5. Re-run targeted tests, then required package tests/build from `backend/`.
