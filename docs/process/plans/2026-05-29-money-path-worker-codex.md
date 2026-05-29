<!-- codex 独立草案，#10 交叉讨论。由 codex exec (gpt-5.5, xhigh) 生成，Claude 未参与撰写。原始探索 trace 略，仅留计划正文。 -->

# Money-Path Worker Implementation Plan

## Scope

Implement one narrow backend slice: when a stream delivers content but upstream reports no usage, compute a positive provisional cost from `EstimatedOutputTokens`, mark the usage row as estimated/provisional, and stop treating delivered frames as output tokens.

Out of scope: full reconciliation worker implementation. Current reconciliation handler is no-op (`backend/internal/observability/reconciliation_handler.go:207`), while reconciliation event storage already exists (`backend/sql/migrations/0002_observability_billing.up.sql:225`).

## Current Grounding

- `UsageRecordDraft` already carries `EstimatedOutputTokens` and `EstimatedReasoningTokens` (`backend/internal/gateway/forwarder_types.go:100`, `backend/internal/gateway/forwarder_types.go:107`).
- The forwarder accumulates visible output estimates per stream event (`backend/internal/gateway/forwarder.go:300`) and copies them into the draft at finish (`backend/internal/gateway/forwarder.go:447`).
- `streamingCompletionEvent` currently turns missing reported usage into `PendingReconciliation=true`, zero cost, and inferred usage source when content was delivered (`backend/internal/gatewayhttp/chat_completions_stream.go:528`, `backend/internal/gatewayhttp/chat_completions_stream.go:531`, `backend/internal/gatewayhttp/chat_completions_stream.go:540`).
- `actualCompletionCost` currently rejects fully missing reported usage (`backend/internal/gatewayhttp/chat_completions_pricing.go:68`).
- `AttemptFromGatewayDraft` currently derives delivered count from `DeliveredTokenCount`, then lets reported `TokensOutput` win if higher (`backend/internal/billing/state.go:110`), and `CostForAttempt` only preserves cost for chargeable attempts (`backend/internal/billing/state.go:191`).
- `outputTokensForAttempt` currently falls back from `draft.TokensOutput` to `attempt.DeliveredTokenCount`, which can turn frames/chunks into `tokens_output` (`backend/internal/billing/settler.go:937`).
- `usage_records.usage_source` currently allows only `reported`, `normalized`, `inferred`, `partial`, `ambiguous` (`backend/sql/migrations/0002_observability_billing.up.sql:156`).
- `billing_events.usage_source` is text without the same CHECK constraint (`backend/sql/migrations/0002_observability_billing.up.sql:97`, `backend/sql/migrations/0002_observability_billing.up.sql:103`).
- `usage_records` is append-only at DB trigger level (`backend/sql/migrations/0039_money_path_append_only_triggers.up.sql:22`).

## Success Criteria

- Missing-usage stream with `EstimatedOutputTokens > 0` and non-ambiguous end class settles with `actual_cost > 0`, `usage_source='estimated'`, `pending_reconciliation=true`, and `tokens_output = EstimatedOutputTokens`.
- Missing-usage stream with delivered frames but no estimate remains zero-cost and pending, so frames are never billed as tokens.
- Ambiguous usage remains `usage_source='ambiguous'` and zero-cost.
- Reported usage remains reported and billed from reported tokens.
- Reconcile scanner can find provisional rows with `pending_reconciliation=true AND usage_source='estimated'`.

## Exact Files And Packages

| Path | Package | Frozen? | Planned change |
|---|---:|---:|---|
| `backend/internal/gateway/forwarder_types.go` | `internal/gateway` | Yes, edit existing only | Add `UsageSourceEstimated = "estimated"` beside existing source constants (`forwarder_types.go:38`). |
| `backend/internal/gatewayhttp/chat_completions_pricing.go` | `internal/gatewayhttp` | Yes, edit existing only | Add helper to build provisional usage from `EstimatedOutputTokens` plus estimated input tokens. Existing pricing path already prices token buckets (`chat_completions_pricing.go:253`). |
| `backend/internal/gatewayhttp/chat_completions_stream.go` | `internal/gatewayhttp` | Yes, edit existing only | In missing-usage error branch, compute positive provisional cost when estimate is present; mark source estimated. |
| `backend/internal/billing/state.go` | `internal/billing` | No | Treat estimated-output drafts as chargeable without overwriting frame delivery count. |
| `backend/internal/billing/settler.go` | `internal/billing` | No | Make `outputTokensForAttempt` prefer reported tokens, then estimated tokens, and never fall back to delivered frames for `tokens_output`. |
| `backend/sql/migrations/0061_usage_source_estimated.{up,down}.sql` | schema | No | Expand `usage_records.usage_source` CHECK and add a partial reconcile index. Latest current migration is `0060_user_balance_holds.up.sql` (`backend/sql/migrations/0060_user_balance_holds.up.sql:1`). |
| Existing tests only | multiple | respect frozen packages | Update/add tests in existing `_test.go` files; no new files under frozen packages. |

## Schema Migration

Use the existing `usage_records.usage_source` column, not a new mutable status column, because original usage rows are immutable/append-only (`backend/sql/migrations/0039_money_path_append_only_triggers.up.sql:23`).

Up migration:

```sql
BEGIN;

ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_usage_source_check;
ALTER TABLE usage_records DROP CONSTRAINT IF EXISTS usage_records_usage_source_chk;

ALTER TABLE usage_records
  ADD CONSTRAINT usage_records_usage_source_chk CHECK (
    usage_source IN ('reported', 'normalized', 'inferred', 'partial', 'ambiguous', 'estimated')
  ) NOT VALID;

ALTER TABLE usage_records VALIDATE CONSTRAINT usage_records_usage_source_chk;

CREATE INDEX IF NOT EXISTS idx_usage_records_pending_estimated_reconciliation
  ON usage_records (tenant_id, settled_at, id)
  WHERE pending_reconciliation = true AND usage_source = 'estimated';

COMMIT;
```

Down migration: drop the partial index, refuse rollback if any `usage_source='estimated'` rows exist, then restore the old CHECK set. Default remains `reported`; old code remains compatible after migration, but new code must not deploy before this migration or estimated inserts can fail the old CHECK.

## Implementation Steps

1. Add `gateway.UsageSourceEstimated`.
2. Add `estimatedStreamingUsageForCost(draft, requestBody)` in `chat_completions_pricing.go`:
   - require `draft.EstimatedOutputTokens > 0`;
   - use `OutputTokens = draft.EstimatedOutputTokens`;
   - use `InputTokens = draft.TokensInput` if present, otherwise `estimateInputTokens(ex.body)`;
   - preserve cache buckets if present.
3. In `streamingCompletionEvent`, when `actualCompletionCost(usageFromDraft(draft))` fails because `reportedUsageMissing(usage)` is true:
   - if not ambiguous and estimate exists, call `completionCost` on provisional usage;
   - only mark `UsageSourceEstimated` if provisional cost is positive;
   - keep `PendingReconciliation=true`;
   - leave `TokensOutput` unchanged so reported-vs-estimated remains distinguishable.
4. In `AttemptFromGatewayDraft`, use estimated output only as a chargeability signal when reported output and delivered count are both zero; do not inflate `DeliveredTokenCount`.
5. In `outputTokensForAttempt`, return:
   - reported `draft.TokensOutput` when positive;
   - estimated `draft.EstimatedOutputTokens` only when `UsageSourceEstimated`;
   - otherwise zero;
   - keep existing int32 clamp behavior.
6. Add migration and run sqlc only if migration parsing requires generated metadata changes. The `InsertUsageRecord` query already writes `usage_source` and `pending_reconciliation` (`backend/sql/queries/billing_settle.sql:32`, `backend/sql/queries/billing_settle.sql:40`), so no query shape change is expected.

## Blast Radius

- Money path: `actual_cost`, claim capture, user balance capture, usage records, billing events.
- Stream-only behavior: non-stream cost calculation remains unchanged.
- Reconcile visibility: new marker changes operator scan predicates from broad `pending_reconciliation=true` to estimated provisional rows.
- Deployment ordering: migration must land before code that emits `usage_source='estimated'`.

## What Could Go Wrong

- Overbilling: using delivered frames instead of `EstimatedOutputTokens` would overcharge chunk-heavy streams.
- Underbilling: output-only provisional pricing would miss input cost; this plan includes estimated input unless Owner chooses output-only.
- Double billing: later reconcile must append only the delta between authoritative and provisional cost, not charge the full final cost again.
- Idempotency: current reconciliation event table has no unique idempotency key (`backend/sql/migrations/0002_observability_billing.up.sql:225`); the later worker must define one before writing deltas.
- DLQ masking: `insertUsageRecordOrDLQ` can enqueue usage insert failures instead of failing the settle (`backend/internal/billing/settler.go:750`), so integration tests must assert the row exists, not only that settle succeeded.
- Rollback: down migration cannot safely rewrite estimated rows because `usage_records` is append-only.

## Discriminating Test Plan

| Test | File | Mutation that turns it RED |
|---|---|---|
| Missing usage + `EstimatedOutputTokens=8`, delivered frames `40`, output rate `2500µ` bills exactly `0.02000000`, not `0` or `0.10000000`. | `backend/internal/gatewayhttp/chat_completions_pricing_test.go` | Remove provisional branch => zero; use `DeliveredTokenCount` => `0.10000000`. |
| Missing usage with delivered frames but `EstimatedOutputTokens=0` remains zero-cost inferred pending. | same | Fall back to frames for cost => positive cost. |
| Ambiguous usage with estimate stays ambiguous and zero-cost. | same | Drop ambiguous guard => estimated positive cost. |
| Pricing config failure with real reported tokens stays reported, pending, zero-cost. | same | Mark every pricing error as estimated/inferred => wrong source. |
| Estimated source makes attempt chargeable even when no frame count exists, but preserves `DeliveredTokenCount=0`. | `backend/internal/billing/state_test.go` | Ignore estimated source in state => failed/zero; copy estimate into delivered count => delivered assertion fails. |
| `outputTokensForAttempt` returns estimated tokens over delivered frames. | `backend/internal/billing/settler_test.go` | Current max fallback returns frames. |
| `outputTokensForAttempt` returns reported tokens over estimated tokens. | same | Estimated overrides reported. |
| Settler integration writes `usage_source='estimated'`, `pending_reconciliation=true`, `tokens_output=8`, and positive `actual_cost`. | `backend/internal/billing/settler_integration_test.go` | Missing migration CHECK, missing source marker, or frame fallback causes row absence/wrong columns. |

## Owner Decision Points

1. Approve schema migration `0061` touching money-path table constraints.
2. Confirm marker string: this plan uses `usage_source='estimated'`.
3. Confirm provisional pricing includes estimated input cost plus estimated output cost. Output-only is safer against input overestimate but knowingly underbills.
4. Confirm no mutable `reconciled` column on `usage_records`; final reconciliation should be append-only via reconciliation events.

## Commit And Landing Sequence

1. Owner approves the schema decision.
2. Commit 1: migration + `UsageSourceEstimated` constant only. Run migration up/down/up on a clean DB, `cd backend && go test ./internal/gateway ./internal/billing ./internal/gatewayhttp`. Stage, then run `codex exec review --uncommitted --full-auto --sandbox read-only`; fix any S0/S1 before commit.
3. Commit 2: pricing/state/settler implementation + tests. Run targeted tests, then `cd backend && go test -count=1 ./internal/gatewayhttp ./internal/billing`. Stage and run the same Codex review gate; no unresolved S0/S1 may land.
4. Final verification before PR: `cd backend && go test -race -count=1 -timeout 5m ./...`, plus migration up/down/up if local DB is available.
