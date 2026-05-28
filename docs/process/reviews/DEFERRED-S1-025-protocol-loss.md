# DEFERRED — S1-025 protocol-loss evidence completeness (follow-up slice)

Source: codex `exec review --uncommitted` Round-2 (model gpt-5.5, reasoning xhigh, 2026-05-28) on the S1-025 fixed diff.
Disposition: S1-025 core (stop hardcoding `usage_records.protocol_loss = "[]"`; persist captured losses on the primary Settle / Abort / CommitCacheHit paths via real carriers) lands now — **no unresolved S0/S1**. The 4 findings below are evidence-**completeness** gaps in additional paths. None is a regression: every path listed persisted `[]` *before* S1-025 too, so this slice is a strict improvement. Classified **S2**, deferred to a follow-up slice per CLAUDE.md #8 ("S2/S3 记录 + 排进 follow-up 切片;不 block 当前 commit" + "review should not discover the spec drip-by-drip").

Severity-mapping rationale (codex P2 → HUAKAI S2): protocol_loss is an audit/observability evidence field, not a money figure (no balance/charge is wrong); and each path was already empty pre-slice, so there is no *new* data loss to elevate to S1.

## Follow-up findings

1. **Recovery DLQ replay drops ProtocolLoss** — `backend/internal/billing/billing.go:99` + `internal/settlementrecovery`.
   `settlementrecovery.Payload` mirrors the old `SettleRequest` shape and does not serialize/restore the new `ProtocolLoss`. On settle-failure replay, `ToSettleRequest()` passes nil → retried `usage_records.protocol_loss` = `[]`.
   Fix sketch: add `ProtocolLoss json.RawMessage` to the recovery Payload + round-trip it in `ToSettleRequest()`; discriminating integration test = enqueue payload with losses → replay → assert persisted JSON == input (mutation: drop the field → `[]`).

2. **Non-streaming snapshot taken too early / misses loss sources** — `backend/internal/gatewayhttp/chat_completions_billing.go:48`.
   `ex.protocolLoss` is snapshotted before `submitAuditLedgerEntry()` appends ledger/trust-chain warnings to `env.CapabilityGraph.ProtocolLoss`, before response-conversion losses (`CanonicalToClientResponse` return value, currently discarded), and request-translation losses (`RequestToCanonical` return value, discarded) are folded in. The PRIMARY source — capability/dispatch downgrades populated during dispatch — IS captured; the missed ones are secondary/warning-level.
   Fix sketch: fold request-side + response-side loss slices into `env.CapabilityGraph.ProtocolLoss`, snapshot after ledger submission; mutation test per loss source.

3. **Audit-ref-missing abort passes nil ProtocolLoss** — `backend/internal/gatewayhttp/chat_completions_billing.go:272` (`rejectMoneyPathAuditRef`).
   When audit-ref validation rejects a direct settle / cache-hit commit, the zero-cost abort record is the only persisted row for the claim, but the abort is called with nil losses. Narrow failure path (audit-ref missing AND losses present).
   Fix sketch: thread the cached/env losses into `rejectMoneyPathAuditRef` (verify whether `eventbus.RequestCompletionEvent` can carry them) and pass to Abort.

4. **Streaming per-event losses discarded** — `backend/internal/gatewayhttp/chat_completions_stream.go:564` + `StreamForwarder`.
   Only initial request-translation losses (`ex.protocolLoss`) reach settlement. Per-event losses emitted by `ProviderEventToCanonicalEvents` / `CanonicalEventToClientChunk` are discarded by `StreamForwarder` (pre-existing). Streams with unknown upstream deltas / dropped client chunks settle with only initial (often `[]`) losses.
   Fix sketch: accumulate the per-event loss slices in the forwarder and merge into the settle carrier. Note `gateway`/`gatewayhttp` are frozen packages (CLAUDE.md #13) — accumulation logic goes in a non-frozen package or existing-file edits only.
