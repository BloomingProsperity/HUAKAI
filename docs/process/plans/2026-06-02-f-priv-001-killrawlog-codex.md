# F-PRIV-001 Kill Raw Gateway Logs Implementation Plan

| Owner directive | "Kill ALL raw-payload logging on the gateway hot path so system logs NEVER contain user prompt / completion / tool-io / upstream raw body / secrets." |
| Scope | In: gateway/clienterr/eventbus/accounting hot-path system logs, adjacent gatewayhttp raw-error system logs found by grep, discriminating no-leak tests, mutation checks, build/test/review/commit/push. Out: database schema/auth/billing ledger/quota enforcement behavior changes. |
| Success criteria | System logs from `clienterr.LogInternal`, stream protocol-error SSE, eventbus handler/DLQ failures, and post-delivery settle recovery enqueue failure contain request/safe class metadata only, never raw prompt/token/upstream body. Client-visible canonical errors stay unchanged. Required build/tests pass and Codex review has no unresolved S0/S1. |
| Time estimate | 2-4 wall-clock hours; one Codex implementation session. |
| Blast radius | Logging behavior in hot-path error/settlement paths. No response contract, settlement state, schema, auth, quota, or billing ledger mutation intended. |
| Failure modes | Over-redaction hides safe correlation fields: keep request_id/event_id/handler_id/classes. Under-redaction leaks raw err text: route through `internal/privacy` error class and payload sanitization. Frozen-package violation: edit existing files only in `backend/internal/gatewayhttp` and `backend/internal/gateway`. Weak tests: inject sentinels that current raw logging leaks, then run mutation red checks. |
| Decision points | No Owner sign-off needed unless a high-risk file (`LICENSE`, secrets, schema, auth core, billing ledger, quota enforcement, deployment) becomes necessary. Current design avoids all high-risk files. |
| Pre-execution checklist | Read `CLAUDE.md`, `docs/RULES.md`, privacy logger/redactor, all grep-identified slog sites; claim edit locks; write failing tests before production edits; avoid new files in frozen packages; run mutation and verification before commit; stage and run Codex review before commit. |

## File Scope

- Modify existing non-frozen package files:
  - `backend/internal/privacy/logger.go`: add package-level privacy system logging through `slog.Default()` and reuse it from `Logger.LogSystem`.
  - `backend/internal/privacy/redactor.go`: add a small `ErrorClassFor` helper backed by `DefaultRedactor().SanitizeError`.
  - `backend/internal/clienterr/log.go`: replace raw `slog.Any("err", err)` with a privacy system event.
  - `backend/internal/eventbus/bus.go`: replace raw handler/DLQ error attrs with privacy system events.
  - `backend/internal/redact/payload_summary.go`: expose bounded structured safe summary fields so stream logs do not rely on raw error text and do not trip the privacy string cap.
- Modify existing frozen package files only:
  - `backend/internal/gateway/forwarder.go`: replace the stream protocol-error `clienterr.LogInternal(...fmt.Errorf(...evt.Data...))` pattern with a privacy event carrying only safe payload summary metadata.
  - `backend/internal/gatewayhttp/chat_completions_billing.go`: replace direct raw settle/enqueue error logging and audit-ref validation string logging with privacy events.
  - `backend/internal/gatewayhttp/audit_verify_handler.go`: replace raw audit verification error logging found by the broad gatewayhttp grep with privacy events.
  - `backend/internal/gatewayhttp/admin_billing_settings_handler.go`: replace raw admin billing audit-write error logging found by the broad gatewayhttp grep with a privacy event.
- Modify existing tests only in frozen packages:
  - `backend/internal/clienterr/catalog_test.go`
  - `backend/internal/eventbus/bus_test.go`
  - `backend/internal/gateway/forwarder_clientadapter_test.go`
  - `backend/internal/gatewayhttp/post_delivery_recovery_test.go`

## Execution Order

1. Write failing tests:
   - `clienterr.LogInternal` logs request/class metadata but not `RAWPROMPT_SECRET_MARKER` or fake `sk-...`.
   - `StreamForwarder.handleEventWithAdapter` logs safe payload summary/class metadata but not raw sentinel/token.
   - `eventbus.Bus` handler and DLQ-persist failure logs omit raw handler/enqueue errors.
   - `settleCompletionWithRecovery` enqueue-failure alert omits raw settle/enqueue errors.
2. Run targeted tests and verify they fail for the current raw-log behavior.
3. Implement minimal privacy logging helpers and replace only the identified unsafe log sites.
4. Run targeted tests and verify green.
5. Mutation checks:
   - Temporarily reintroduce raw error logging in `clienterr.LogInternal`; run the clienterr no-leak test and verify red; restore.
   - Temporarily reintroduce raw stream `evt.Data` logging; run the stream no-leak test and verify red; restore.
6. Run required verification:
   - `go build ./...`
   - `go test ./internal/gatewayhttp/... ./internal/gateway/... ./internal/clienterr/... ./internal/privacy/...`
   - `go test ./internal/eventbus/...` because this task modifies eventbus accounting logs.
   - `-race` if feasible after normal tests pass.
7. Stage intended diff and run `timeout 600 codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh < /dev/null`; normalize findings and fix unresolved S0/S1.
8. Commit with root cause and `Rules touched: ...`, then push `HEAD:work/f-priv-killrawlog`.

## Assumptions And Risks

- `audit_verify_handler.go` and `admin_billing_settings_handler.go` are outside the chat completion hot path, but were updated because the requested broad gatewayhttp grep found direct raw-error system logs.
- Direct client response behavior must not change; all edits are log-only.
- Risk class is SECURITY/PRIVACY and should be parked for Owner awareness after push.
