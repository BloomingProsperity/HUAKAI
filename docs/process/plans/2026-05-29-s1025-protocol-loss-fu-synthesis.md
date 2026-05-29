# S1-025 follow-up — synthesis of Claude + Codex parallel plans (CLAUDE.md #10)

Two independent plans drafted: `*-claude.md` and `*-codex.md`. Result: **AGREE on all material decisions; no conflict.** Codex's plan is a strict superset (better-researched on items 2 & 4). Adopt codex's plan as the implementation base with ONE Claude naming refinement (item 4).

## Agreement
- **Item 1** (recovery DLQ replay): add `ProtocolLoss json.RawMessage` to `settleRequestPersisted`; round-trip in FromCompletionEvent/ToSettleRequest. Identical in both. (codex adds a 2nd guard test in post_delivery_recovery_test.go — adopt.)
- **Item 3** (audit-ref-missing abort): pass `event.SettleRequest.ProtocolLoss` instead of `nil` at chat_completions_billing.go:275. Identical. Event already carries it (verified: settler reads req.ProtocolLoss; eventbus event carries SettleRequest). codex adds: recordingSettler.Abort test mock must capture the loss arg — adopt.
- **Design decisions agreed**: item 3 reuse the existing event field (no parallel field); item 4 accumulate per-event losses in `UsageAccumulator`, surface on the `UsageRecordDraft`, merge in `streamingCompletionEvent`; item 2 snapshot after each loss-appending source.

## Where codex was more complete (adopt codex)
- **Item 2 — request-translation losses**: codex located the actual drop site — `chat_completions_dispatch.go:338` `canonicalReq, _, err := RequestToCanonical(...)` discards request losses on the NON-streaming buffered path (the streaming path at stream.go:343 already captures them). My plan left this as "confirm where". VERIFIED: dispatch.go:338 drops them. Fix there: capture `requestLosses`, set `ex.protocolLoss = protocolLossJSONFromEntries(requestLosses)` before the invalid-request abort branches (340/347), and append them into `canonicalReq.CapabilityGraph.ProtocolLoss`. Plus the two other sources both plans share (ledger warnings after submitAuditLedgerEntry; client-response losses at billing.go:62). Upstream-response losses are ALREADY captured (upstream_dispatcher_hcsf.go:131/150) — do not double-add.
- **Item 4 — accumulation sites**: codex found more than my 3 (forwarder.go 279/368/390). Full set: handleEventWithAdapter (279), drainWithAdapter (368), clientChunks (386/390, change signature to return losses), and the hop-chain caller `forwarder_hop_chain.go:138`. VERIFIED forwarder_hop_chain.go:138 calls clientChunks → must update when signature changes. Accumulate all into `UsageAccumulator`, copy to draft in finishDraft (~forwarder.go:424).

## Claude refinement (item 4 field name) — #10-exempt coding choice, decided + recorded
codex names the new `UsageRecordDraft` carrier field `ProtocolLoss`. But S1-025 just REMOVED a dead `UsageRecordDraft.ProtocolLoss` field (the one the settler wrongly read pre-S1-025). Re-introducing the same name risks resurrecting that settler-disconnect bug.
**DECISION**: name the carrier `StreamProtocolLoss` on BOTH `UsageAccumulator` and `UsageRecordDraft`, with a doc comment: "settler/SettleRequest must NEVER read this; it is merged into SettleRequest.ProtocolLoss by streamingCompletionEvent only." VERIFIED settler reads only `req.ProtocolLoss` (settler.go:172/519/873), not any draft field — so the merge-in-stream path is the single writer into the persisted loss. This keeps the S1-025 dead-field cleanup intact.

## Net implementation spec (what codex will build)
1. settlementrecovery/payload.go: +ProtocolLoss field + round-trip (+payload_test.go round-trip with sentinel; +post_delivery_recovery_test.go guard).
2. chat_completions_dispatch.go: capture request losses at 338, set ex.protocolLoss, append to canonicalReq.CapabilityGraph.
3. chat_completions_billing.go: snapshot ex.protocolLoss after submitAuditLedgerEntry (before err-abort) and after CanonicalToClientResponse (capture its discarded losses, append to env, re-snapshot before settle). 3 source-specific discriminating tests (request `d5_metadata_field_pending`; ledger `audit_signer_deferred`; response `stop_reason_unknown`).
4. chat_completions_billing.go:275: rejectMoneyPathAuditRef passes event.SettleRequest.ProtocolLoss (+recordingSettler captures arg; table test both wrappers).
5. forwarder_types.go: +StreamProtocolLoss []proto.ProtocolLossEntry on UsageAccumulator + UsageRecordDraft (+comment). forwarder.go + forwarder_hop_chain.go: accumulate per-event provider+client losses, copy to draft in finishDraft. clientChunks returns losses. stream.go:577: merge ex.protocolLoss (request) ∪ draft.StreamProtocolLoss (per-event) into SettleRequest.ProtocolLoss via a marshal-once helper. Forwarder accumulation test + stream merge test (2 sentinels).

## Guards
All S2 (audit evidence, no money figure). No migration (protocol_loss col exists, mig 0043). No charge/quota/abort-decision change. Frozen packages gateway/gatewayhttp/proto = existing-file edits only, no new files (adding struct fields to existing files is allowed). #14: every test asserts an exact sentinel code with a named RED mutation.

Execution order (codex): item1 → item3 → item2 → item4 (low→high risk). Then #8 review ≤2 rounds, mutation proofs, land fix/hermes. No Owner stop (autonomy granted; no conflict surfaced).
