# 2026-05-28 Wave B (S1-025 / S2-163 / S1-015 / S1-029) — Claude independent draft (#10)

> Parallel-draft per CLAUDE.md #10. Written from my own code reading + an Explore map, WITHOUT seeing the
> codex draft (`2026-05-28-waveb-billing-evidence-codex.md`, dispatched separately). Synthesis + Owner
> surface (esp. the S1-015 multiplier decision) happen after both exist.
> All four findings converge on `backend/internal/billing/settler.go` usageParams construction → land SERIALLY.

## Cross-cutting: serial landing order

Lines `settler.go:141-172` (the `InsertUsageRecordParams` build in `Settle`, mirrored in `Abort` ~289-366 and
`CommitCacheHit` ~431-517) are the shared bottleneck. Order:
1. **S1-025** — add `ProtocolLoss` to `UsageRecordDraft` + `SettleRequest`, thread real losses into the 3 settle paths.
2. **S1-015** — populate cache 5m/1h tokens + tier-aware costs in the same usageParams region.
3. **S1-029** — provisional charge (stream path) + new `internal/settlementreconcile` worker (isolated).
4. **S2-163** — wire tokencheck verdict onto the usage record (new column + new wire file).
Each lands as its own commit with per-commit codex review (#8); `go build ./...` + integration_pg after each.

---

## S1-025 — persist protocol-loss evidence

**Current:** adapters/forwarder accumulate `[]proto.ProtocolLossEntry` into `env.CapabilityGraph.ProtocolLoss`
(`gateway/upstream_dispatcher_hcsf.go:150-154`, `gatewayhttp/chat_completions_billing.go:511-520`,
`proto/anthropic/sse.go:368-379`). At settlement the evidence is **discarded**: `settler.go:172` hardcodes
`ProtocolLoss: []byte("[]")` (same in Abort:366, CommitCacheHit:517). `usage_records.protocol_loss` (jsonb,
migration 0002:165) therefore always stores `[]`.

**Fix:**
- Add `ProtocolLoss []proto.ProtocolLossEntry` (or pre-marshaled `json.RawMessage`) to
  `gateway.UsageRecordDraft` (`forwarder_types.go:79-95`) and `billing.SettleRequest` (`billing.go:81-108`).
- At the gateway settle call sites (`chat_completions_billing.go` / `_stream.go`), extract
  `env.CapabilityGraph.ProtocolLoss` into the draft/SettleRequest before calling Settle/Abort/CommitCacheHit.
- In `settler.go`, marshal `req.Draft.ProtocolLoss` (fallback `[]`) into `InsertUsageRecordParams.ProtocolLoss`
  in all three paths instead of the hardcoded `[]byte("[]")`.
- Keep `jsonOrEmptyArray` semantics (never NULL).
**Frozen-pkg note:** edits to existing files in gateway/gatewayhttp/proto are OK (bug-fix); no new files there.
**Discriminating test:** drive a request whose adapter emits ≥1 loss entry → assert `usage_records.protocol_loss`
contains it (and audit billing_event if threaded). *Mutation:* keep `[]byte("[]")` → empty array → red.

## S2-163 — wire tokencheck cross-check verdict

**Current:** `tokencheck/crosscheck.go:8` `CrossCheck(reported, estimated)` → `Discrepancy`/`Verdict`
(`types.go:8-13`: OK/Warn5/Fail20/Unknown, 5%/20% thresholds), plus `cache_verify.go`. **Invoked from no
non-test code**; the settle path never cross-checks reported vs computed tokens.

**Fix:**
- New file `internal/tokencheck/crosscheck_wire.go` (tokencheck is **non-frozen**) exposing
  `EvaluateDraft(reported, estimated TokenCounts) Verdict` (+ ratio) that the settle path calls.
- Compute `estimated` from the same token inputs used for pricing (S1-015) and `reported` from upstream usage;
  record the verdict. **Storage decision (D-WB1):** reuse existing columns — map Warn5/Fail20 onto
  `usage_source` (`'ambiguous'`) + set `confidence_score` from the discrepancy ratio + `pending_reconciliation=true`
  on Fail20 — OR add a dedicated `usage_records.token_check_verdict` column (migration 0061). Recommend **reuse
  existing columns** first (no migration); add a column only if Owner wants a first-class verdict field.
- Wire `EvaluateDraft` into `settler.go` usageParams build (after token counts known).
**Discriminating test:** reported=1000, estimated=500 (>20%) → assert verdict Fail20 recorded (confidence low /
usage_source ambiguous / pending). *Mutation:* don't wire EvaluateDraft → verdict OK/absent → red.

## S1-015 — cache-tier (5m/1h) + cost breakdown  ← OWNER DECISION (multipliers)

**Current:** the 5m/1h split arrives (`proto/hcsf.go:117-120` `CacheCreationInputTokens5m/1h`;
`proto/anthropic/sse.go:357-361`) but is **flattened**: `settler.go:154-155` hardcodes
`CacheCreation5mTokens:0, CacheCreation1hTokens:0`; `:158-162` hardcodes every `*Cost` to `decimal.Zero`
(same in Abort/CommitCacheHit). Columns `usage_records.cache_creation_5m_tokens/1h_tokens` (0002:135-136)
exist but always get 0. A rate-table cost path exists in `gatewayhttp/chat_completions_pricing.go:226`
(`addTokenBucket(... usage.CacheCreationTokens, v.CacheCreation, v.HasCacheCreation ...)`) but treats
cache-creation as ONE bucket; `billing_pricing_versions` table (0002:271-291) + `rate_table_source.go`
(`PricingVersionReader`) are the pricing scaffolding.

**Fix:**
- Thread `CanonicalUsage.CacheCreationInputTokens5m/1h` into `UsageRecordDraft` → `settler.go` so
  `CacheCreation5mTokens/1hTokens` carry the real split (stop zeroing).
- Extend the pricing bucket logic (`chat_completions_pricing.go`) to price 5m and 1h cache-creation at
  DIFFERENT multipliers (+ cache-read cheap), populate `CacheCreationCost`/`CacheReadCost`/`InputCost`/
  `OutputCost` instead of `decimal.Zero`, and thread the breakdown into usageParams.
- Add the per-tier rates to the pricing version vector (extend `billing_pricing_versions` row shape; if a
  migration is needed for new rate columns that is migration 0061 — confirm in plan).

**OWNER DECISION (D-WB2): cache-tier multipliers.** Need the rate scheme. 参考项目对照 (ACTIVE refs;
one-api retired):

| Scheme | litellm `@79b4578671` | new-api `@20d3e73` | Anthropic public (vendor contract) |
|---|---|---|---|
| cache write 5m | `litellm_core_utils/llm_cost_calc/utils.py:177-196` `cache_creation_input_token_cost` (per-model $/tok) | `relay/channel/claude/relay-claude.go:593-614` preserves 5m/1h split into billing (per-model ratio) | **1.25 × base input** |
| cache write 1h | `utils.py:198-200` `cache_creation_input_token_cost_above_1hr` (distinct higher key) | same split, 1h ratio | **2.0 × base input** |
| cache read | `utils.py:181` `cache_read_input_token_cost` | cache-read ratio | **0.1 × base input** |
| long-context tiers | `utils.py:274-287` `_above_{threshold}_tokens` keys (per-token-threshold) | — | (Anthropic ≥200k tier on some models) |

- litellm = the precision model: distinct per-model $/token keys for 5m vs 1h-vs read + long-context thresholds.
- new-api = preserves the 5m/1h split through to billing (doesn't flatten), applies ratios.
- HUAKAI delta (架构+算法): per-tier rate vector in the versioned `billing_pricing_versions` (Merkle-snapshotted,
  replayable) vs litellm's static per-model map / new-api's runtime ratio — same tiers, replayable pricing.

**Options for Owner:**
- **A (recommended): adopt Anthropic canonical multipliers** (5m=1.25×, 1h=2.0×, read=0.1× of base input) as the
  default rate vector, long-context tier deferred. Simplest, matches the dominant upstream; vendor-contract values are clean-room-exempt.
- **B: litellm-style per-model $/token map** (more precise across providers, heavier config; needs a rate-table seed per model).
- **C: minimal — carry the 5m/1h token split to usage_records now, defer all cost computation** (records the tiers for audit; $ stays placeholder until the Phase-E pricing engine). Lowest risk, but doesn't fix revenue.

**Discriminating test:** same total cache-creation tokens split 100%@5m vs 100%@1h → assert cost differs by the
1h/5m multiplier ratio. *Mutation:* flatten to one rate → equal cost → red.

## S1-029 — streaming no-usage provisional charge + reconcile worker

**Current:** when a stream finishes with NO upstream usage but delivered>0, `stream.go:528-532` sets
`PendingReconciliation=true` + `actualCost=Zero` → **charges zero** (revenue loss). `DeliveredTokenCount` is
tracked (`forwarder_types.go:177-185`, `settler.go:168`). `pending_reconciliation` column + index exist
(0002:160,183-185); a `PendingReconciliationOnly` query filter exists (observability.sql.go) but **no worker
consumes it** (settlementrecovery is post-delivery retry, not pending-reconciliation true-up).

**Fix:**
- When usage is absent on a delivered stream, compute a **provisional** charge from `DeliveredTokenCount` × the
  output rate (from S1-015 pricing), set `pending_reconciliation=true`, `usage_source='inferred'`,
  `confidence_score` from S2-163. (Edits stream path + settler; depends on S1-015 pricing being wired.)
- New non-frozen package `internal/settlementreconcile`: a worker that periodically selects
  pending-reconciliation usage records (the existing `PendingReconciliationOnly` query) and, when authoritative
  usage later arrives, trues up via the existing append-only `reconciliation_appended` billing_event +
  the S1-013 balance credit/debit (`RefundInTx`/a capture-delta). Wire it in `cmd/gateway/wiring.go:374-379` +
  `lifecycle.go:26-37` following the `ReplayJanitor`/`LeaseSweeper` lifecycle pattern (ticker + stop channel).
**Discriminating test:** stream delivers 500 tokens, upstream reports no usage → assert non-zero provisional
charge + `pending_reconciliation=true`; then worker trues up against authoritative 600 tokens → assert
adjustment event + balance delta. *Mutation:* skip provisional → zero charge → red; skip worker → stuck pending → red.

## Decision points for Owner
- **D-WB2 (S1-015 multipliers)** — A (Anthropic 1.25×/2×/0.1×, recommended) vs B (litellm per-model map) vs C (carry tokens only, defer cost). **Gating.**
- **D-WB1 (S2-163 storage)** — reuse existing columns (recommended, no migration) vs add `token_check_verdict` column.
- Migration: only needed if D-WB2=B/A-with-new-rate-columns or D-WB1=column → migration 0061. Confirm at impl.

## Risk / sequencing
High-risk (money path + pricing). Plan surfaced for Owner before impl; per-commit codex review; integration_pg
tests against huakai_dev; NOT pushed / migration NOT applied to prod until Owner Docker regression OK.
