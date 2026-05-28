# DEFERRED — S1-015 cache-tier pricing (follow-up)

Source: codex `exec review --uncommitted` Round-2 (model gpt-5.5, reasoning xhigh, 2026-05-28) on the S1-015 fixed diff.
Disposition: S1-015 core lands now — no unresolved S0/S1. Round-1 found 2 issues (both fixed in this slice): [S1] cache bucket costs now gated by `CostForAttempt` (consistent with `actual_cost`); [S2] `UsageAccumulator.Empty()` now counts cache usage. Round-2 found the single S2 below.

## Follow-up finding

**[S2] Cache-only successful stream not settled** — `backend/internal/gateway/forwarder.go:405-407` + `backend/internal/billing/state.go` (`AttemptFromGatewayDraft`) + the stream settle gate.
A successful stream that reports ONLY cache creation/read usage with zero fresh input/output/delivered tokens now has its draft cache fields populated and is priceable, but `AttemptFromGatewayDraft` + the settle gate (chargeable OR delivered>0 OR ambiguous) still ignore cache buckets, so such a stream is aborted as no-billable-delivery and no usage_record/cost is written.

Severity rationale (codex P2 → HUAKAI S2): theoretical edge with no real-traffic impact — a real successful Anthropic stream always reports input_tokens>0 and delivers output (delivered>0 → chargeable), so the gate already catches every real billable stream. Cache-only-with-zero-input/output/delivered does not occur in production. Not a regression (pre-S1-015 all cache cost was 0). Fixing requires teaching the streaming chargeability state machine (`AttemptFromGatewayDraft`) + settle gate to treat nonzero cache buckets as billable delivery — a deliberate change to the stream state machine, out of scope for this slice.

Fix sketch: make `AttemptFromGatewayDraft` consider nonzero CacheCreation/CacheRead tokens as billable signal (so the attempt is chargeable / not no-billable-delivery), and add a discriminating test: a stream draft with only cache tokens + StreamEndGraceful should settle (usage_record written, cost = priced cache) rather than abort. Mutation: ignore cache buckets in the gate → aborted/no row → RED.

## Related test-hygiene debt (not a review finding)
`TestSettler_LeaseSweepAbortsExpiredClaims` sweeps GLOBAL expired-reserving claims; on the shared `huakai_dev` DB, orphan reserving claims + holds-without-balance-rows from other tests (no `t.Cleanup`) accumulate and can crowd the batch (limit 10) so the seeded claim isn't swept → flaky red. Mitigation today: clean before each integration_pg run (`DELETE orphan holds` + `abort stale reserving`). Durable fix (follow-up): scope the lease-sweep test to its own seeded claim or pre-clean stale state at setup.
