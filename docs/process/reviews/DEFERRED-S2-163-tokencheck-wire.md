# DEFERRED — S2-163 token cross-check wiring (follow-up)

Source: codex `exec review --uncommitted` Round-1 + Round-2 (model gpt-5.5, reasoning xhigh, 2026-05-28) on the S2-163 diff.
Disposition: S2-163 core lands now — no unresolved S0/S1. The dead `tokencheck.CrossCheck` is wired into `nonStreamingUsageDraft`, comparing reported output tokens vs a heuristic estimate of the buffered response content, and recording the verdict in existing columns (confidence_score + pending_reconciliation), usage_source unchanged, no cost change, no migration.

Fixed in-slice (review-driven, all RED→GREEN proven):
- Round-1 [S2]: short responses false-flagged (percentage-only). → added absolute-token floor `crossCheckMinAbsTokenDelta=50` (above the heuristic estimator's noise band).
- Round-2 [S2]: estimator omitted `block.Thinking`. → estimator now counts Thinking text (fixes Anthropic visible-thinking false positives).
- Round-2 [S2]: L2 cache-hit drafts (zero-cost replay) could be flagged while `CommitCacheHit` writes pending=false (inconsistent). → cross-check downgrade now gated on `actualCost.Total.IsPositive()`, so zero-cost cache-hit drafts are never flagged.

## Deferred follow-up

**[S2] Hidden reasoning tokens (e.g. OpenAI o1/o3) can still false-flag.** `proto.CanonicalUsage` carries a single `OutputTokens` with NO separate reasoning-token field, so for a response with a visible answer PLUS hidden reasoning tokens (reasoning counted in OutputTokens but never returned as content), the content-based heuristic estimate is below reported → can exceed the 20% / 50-token thresholds → confidence=0.5 + pending_reconciliation=true even though the response is legitimate.

Severity rationale: S2 (audit/observability only — no charge is wrong; confidence_score/pending_reconciliation are annotations). Not a regression. **Self-healing**: the S1-029 reconcile worker finalizes (clears) aged pending_reconciliation records after a grace period, so any transient false flag is automatically cleared and does not accumulate. The cost on these rows is already correct (the cross-check never alters cost).

Durable fix (separate slice — needs a proto change): add a reasoning-token field to `proto.CanonicalUsage` (populate from vendor usage, e.g. OpenAI `completion_tokens_details.reasoning_tokens` / Anthropic thinking accounting), and in the cross-check compare the estimate against `OutputTokens - reasoningTokens` (visible output only). Until then the abs-floor + Thinking estimation cover the common Anthropic case; the OpenAI hidden-reasoning residual is accepted as transient noise the reconcile worker absorbs.

Also deferred: **streaming cross-check** — the stream forwarder does not retain full response content blocks, so output-token cross-check is only wired on the non-streaming buffered path. Streaming cross-check would need accumulated content (larger change).
