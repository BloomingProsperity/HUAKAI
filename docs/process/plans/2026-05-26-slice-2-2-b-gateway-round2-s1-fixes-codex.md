# 2026-05-26 Slice 2.2.b Gateway Round 2 S1 fixes
| Owner directive | "你是 Slice 2.2.b Gateway Round 2 S1 fix executor. 3 S1 + 1 S2." |
| Scope | Fix gateway-side Hermes chat internal token format, runner conversation-id retargeting, and audit failure transaction handling. Add discriminating tests for each S1. Add one deferred S2 scanner-buffer ticket. If production savepoint access requires a helper, create it in `backend/internal/db/hermes` only; that package is not frozen. Do not modify `backend/deploy/hermes-runner/`, frozen packages, schema, auth core, quota, billing ledger, secrets, or deployment scripts. Do not `git add` or commit. |
| Success criteria | `SignInternalToken` and `VerifyInternalToken` use the runner pipe-format contract; bridge persistence ignores runner-supplied conversation ids; audit insert failure rolls back only the audit sub-operation, commits message persistence, and enqueues audit DLQ outside the transaction; S1 tests fail under the described mutations and pass after fixes; requested build/vet/test command completes or any blocker is reported with evidence. |
| Time estimate | 45-75 minutes wall clock; one Codex session. |
| Blast radius | `backend/internal/hermeschat` request preparation, SSE stream persistence, audit recording, and tests; optional non-frozen `backend/internal/db/hermes` helper for transaction control; one docs review ticket. No runner or frozen package writes. |
| Failure modes | Token canonicalization mismatch with runner: use exactly `tenant_id|user_id|request_id|exp_unix_int|hex_hmac`. Retarget test not discriminating: assert persisted message/touch/audit use prepared id while forwarded SSE still contains runner id. Audit tx stub not mirroring PostgreSQL abort behavior: make test store simulate transaction abort after audit failure unless savepoint rollback is called. Savepoint SQL unsupported by existing store interface: use optional savepoint-capable interface so existing hermes store implementations keep compiling. |
| Decision points | None expected; Owner already selected ignore-runner-conversation-id and savepoint path. Escalate before schema, runner, frozen package, or dependency changes. |
| Pre-execution checklist | Read CLAUDE.md #8/#14, AGENTS.md, staged diff, and relevant Hermes chat code; write failing tests first; run focused tests to observe RED; implement minimal gateway-side fixes; run focused tests GREEN; write deferred scanner buffer ticket; run full requested verification. |

## Concrete execution order

1. Inspect `backend/internal/hermeschat/internal_token.go`, `bridge.go`, and current tests.
2. Add token-format tests that verify the runner pipe-format spec and reject legacy `hki_v1` tokens.
3. Add bridge retargeting test where runner sends a different `event: conversation` id and persistence remains on `prepared.ConversationID`.
4. Add audit failure test support that simulates PostgreSQL transaction abort unless the audit savepoint is rolled back, then assert message commit plus DLQ.
5. Run focused Hermes chat tests and confirm the new tests fail for the current implementation.
6. Change token signing/verification to pipe-format with hex HMAC over the four-field canonical string.
7. Change bridge stream handling to ignore runner conversation ids for persistence.
8. Add savepoint/rollback/release hooks around audit insert via an optional store capability and keep DLQ outside the transaction; if needed, expose `Exec` on `dbhermes.Queries` from a new non-frozen helper file.
9. Add `docs/process/reviews/DEFERRED-hermes-bridge-scanner-buffer.md`.
10. Run focused tests and then the requested build, vet, and race test command.
