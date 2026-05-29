# S1-029 — streaming provisional cost fix (LANDED) + reconcile worker (DEFERRED)

Source: codex `exec review --uncommitted` Round-1..Round-4 (model gpt-5.5, reasoning xhigh, 2026-05-28/29) on the S1-029 diff.
Owner decision (2026-05-29): land ONLY the streaming cost fix now; defer the settlementreconcile worker to a dedicated migration-backed slice (see docs/process/plans/2026-05-28-s1029-p1-provisional-overcharge-claude.md for the decision record).

## LANDED in this commit — streaming provisional cost fix (no overcharge)
A stream that delivered content but received NO upstream usage now keeps `ActualCost = 0` with
`pending_reconciliation = true` and `usage_source = inferred` (unless the stream is `ambiguous`,
which is preserved). It does NOT price the delivered count as tokens.

Why (codex Round-2 [S1], 2 independent drafts agreed): on the missing-usage path
`draft.DeliveredTokenCount` is the delivered CONTENT-FRAME count (`canonicalDeliveredChunks`: 1 per
text_delta/tool_input_delta/reasoning_delta), NOT a token count. Finely-fragmented streams (OpenAI
sub-token deltas, tool-argument JSON in many fragments) make frames > tokens → pricing frames as
tokens would OVERCHARGE the user. Output-only $0 provisional = conservative/safe (never overcharges).

Guards (each RED→GREEN proven, #14):
- `reportedUsageMissing(usage)` (single source of truth, shared with `actualCompletionCost`) gates the
  inferred-marking to the genuine no-token-signal case. A pricing-CONFIG failure (rate table missing
  but real tokens present) stays `usage_source = reported` — must NOT become inferred (test T2), else a
  future reconcile worker could zero-finalize a real charge.
- `usage_source != ambiguous` (codex Round-4 [S1]): ambiguous streams (unknown termination, etc.) stay
  ambiguous for genuine reconciliation — never downgraded to a finalizable $0 provisional (test T3).

Tests: T1 (no-usage → $0/inferred/pending, mutation = chunk-count provisional → 0.1 overcharge → RED),
T2 (config failure → $0/reported/pending), T3 (ambiguous preserved). All in chat_completions_pricing_test.go.

## DEFERRED — settlementreconcile finalize-after-grace worker (own slice)
The worker (internal/settlementreconcile, billing_settle.sql SelectPendingReconciliationForFinalize +
FinalizePendingReconciliation, cmd/gateway wiring) is implemented locally but NOT landed. Across four
review rounds it kept misclassifying rows because it identifies "the no-authoritative-usage provisional"
by PROXY signals that are not unique to it:

| signal | also matched by | round |
|---|---|---|
| `usage_source = inferred` | S2-163 cross-check `reported` flags | R1 (excluded via inferred filter) |
| `usage_source = inferred` | `UpstreamEOFNoTerminal` PARTIAL-usage rows (cost > 0) | R3 (tried `actual_cost = 0`) |
| forced `inferred` | `AmbiguousUsage` rows (fixed in the landed cost-fix via the ambiguous guard) | R4-P1 |
| `actual_cost = 0` | zero-rate / free-model PARTIAL-EOF rows (real usage, $0 rate) | R4-P2 (UNRESOLVED) |

Root cause: `usage_records.tokens_output` carries the delivered-FRAME count on missing-usage
(`outputTokensForAttempt` = `max(TokensOutput, DeliveredTokenCount)`; the `max` only ever upgrades to the
frame count in the missing-usage case), so `tokens_output` cannot be used to distinguish "no usage" from
"real partial usage" either. With both `inferred` and `actual_cost=0` overloaded, the worker cannot
unambiguously identify its target rows in SQL.

Required for the deferred slice (both Owner-gated):
1. **Dedicated provisional marker** — add a distinct `usage_source` value (e.g. `provisional_no_usage`)
   set ONLY by the missing-usage streaming branch, and finalize ONLY that. Requires a **migration**
   (the `usage_source` CHECK constraint, migration 0002:157) — Wave B deliberately added none; prod
   application stays gated.
2. **Root frames/tokens disambiguation** — `outputTokensForAttempt` / `AttemptFromGatewayDraft` must not
   record the delivered-frame count as `tokens_output` on missing-usage (it should be 0). Shared
   settle-path billing-ledger code with encoding tests (state_test.go:77-83,
   billing_state_integration_test.go:29) → High-risk per the Risk-Based Confirmation Rule.

Until the worker lands, missing-usage streams accrue as `pending_reconciliation = true` with no
auto-finalizer (a growing but benign pending backlog; cost is correct at $0). The worker’s zero-delta
`FinalizePendingReconciliation` marker (append-only, into `usage_record_reconciliation_events`) and its
60s/grace-5m/batch FOR UPDATE SKIP LOCKED design are sound and carry over to the dedicated slice; only
the row-identification predicate needs the marker above.

## Other deferred follow-ups
- **[S2] Admin/observability pending-views over-count finalized rows.** Once the worker lands, finalize
  is append-only (the `pending_reconciliation` column stays true), so pending-only reads
  (observability.sql:33,52) must exclude rows with a `finalize_after_grace` marker (or derive an
  outstanding view). NOT a regression — pre-worker nothing cleared the flag. Belongs with the worker slice.
- **[S2] Integration-test data persists under append-only triggers.** Money-path integration tests can't
  DELETE seeded rows (migration 0039); use a unique per-run tenant + finalize markers, or an ephemeral
  per-run schema/DB. Subsumes the lease-sweep test-isolation debt in DEFERRED-S1-015.

## Cross-reference
S2-163 cross-check pending flags carry `usage_source='reported'` and are never auto-finalized — the
worker (when it lands) targets the no-usage provisional only. See DEFERRED-S2-163-tokencheck-wire.md.
