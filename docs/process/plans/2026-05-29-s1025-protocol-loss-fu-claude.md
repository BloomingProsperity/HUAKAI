# S1-025 follow-up — 4×P2 protocol-loss completeness — Claude plan

Written independently (CLAUDE.md #10), without seeing codex's parallel draft.
Source spec: docs/process/reviews/DEFERRED-S1-025-protocol-loss.md (4 S2 completeness gaps).
Severity: all S2 (audit/observability evidence field, not a money figure; every path emitted `[]` pre-S1-025 so strict improvement). Lands on fix/hermes.

## Scope (4 items)

### Item 1 — Recovery DLQ replay drops ProtocolLoss  [NOT frozen]
`settlementrecovery.settleRequestPersisted` (payload.go:58-80) mirrors `billing.SettleRequest` but omits `ProtocolLoss`. So a settle-failure → DLQ → replay path reconstructs SettleRequest with nil ProtocolLoss → `usage_records.protocol_loss = "[]"` on the retried row.
**Fix**: add `ProtocolLoss json.RawMessage \`json:"protocol_loss,omitempty"\`` to `settleRequestPersisted`; set it in `FromCompletionEvent` (`req.ProtocolLoss`) and restore in `ToSettleRequest`.
**Test** (payload_test.go, discriminating): build a Payload via FromCompletionEvent from an event whose SettleRequest.ProtocolLoss = a non-empty JSON array (e.g. `[{"severity":"warning","code":"x"}]`), Encode→Decode→ToSettleRequest, assert the resulting SettleRequest.ProtocolLoss bytes EQUAL the input. MUTATION: delete the ToSettleRequest assignment (or the struct field) → round-trip yields nil → RED. (Mirror the existing field-round-trip test pattern in payload_test.go.)

### Item 2 — Non-streaming snapshot too early / misses secondary loss sources  [FROZEN gatewayhttp, existing-file edit]
`chat_completions_billing.go:51` snapshots `ex.protocolLoss = protocolLossJSONFromEnv(bufferedEnv)` BEFORE (a) `submitAuditLedgerEntry` (line 52) appends ledger/trust warnings to `env.CapabilityGraph.ProtocolLoss` (see line 539), and (b) `CanonicalToClientResponse` (line 62) whose loss return value is discarded (`_`).
**Fix**: KEEP the early line-51 snapshot (the abort paths at 54/64/73 must still carry the primary dispatch-time losses). After a SUCCESSFUL `submitAuditLedgerEntry` and after `CanonicalToClientResponse`, fold the response-conversion loss slice (capture the currently-discarded return at line 62) into `env.CapabilityGraph.ProtocolLoss`, then RE-SNAPSHOT `ex.protocolLoss = protocolLossJSONFromEnv(bufferedEnv)` immediately before building the settle request (before line 79). Net: abort rows keep primary losses; the success settle row (line 139) additionally carries ledger-warning + response-conversion losses.
- Do NOT change abort-path semantics. Do NOT fail the request if loss marshaling fails (protocolLossJSONFromEntries already returns nil on marshal error).
- Request-translation losses: in the non-streaming path request→canonical happens inside dispatchBufferedEnvelope; if its losses are already in env.CapabilityGraph by line 42, the line-51 snapshot already has them — confirm, do not double-add.
**Test** (chat_completions_pricing_test.go or a billing test, discriminating): seed a bufferedEnv whose CapabilityGraph.ProtocolLoss is empty at snapshot time but to which submitAuditLedgerEntry/response-conversion add an entry; assert the SETTLE request's ProtocolLoss contains that entry. MUTATION: revert to the early-only snapshot → settle ProtocolLoss misses the late entry → RED. (Must use a discriminating fixture where early≠late.)

### Item 3 — Audit-ref-missing abort passes nil ProtocolLoss  [FROZEN gatewayhttp, existing-file edit]
`rejectMoneyPathAuditRef` (line 269) calls `d.Settler.Abort(..., nil)` at line 275. The event already carries losses at `event.SettleRequest.ProtocolLoss` (set in nonStreamingSettleRequest:139 / stream:577).
**Fix**: pass `event.SettleRequest.ProtocolLoss` instead of `nil` at line 275.
**Test** (existing reject test, e.g. post_delivery_recovery_test.go or a billing handler test): use a recording Settler mock; call the reject path with an event whose SettleRequest.ProtocolLoss is a non-empty array; assert the captured Abort call received those bytes. MUTATION: keep `nil` → captured losses empty → RED. NOTE: this changes no signature (Abort already takes the losses arg), so the only test-call-site break is the assertion itself.

### Item 4 — Streaming per-event losses discarded  [FROZEN gateway+gatewayhttp, existing-file edits]
`forwarder.go` discards per-event loss slices: `ProviderEventToCanonicalEvents` (lines 279, 368) and `CanonicalEventToClientChunk` (line 390) each return a `[]proto.ProtocolLossEntry` currently assigned to `_`.
**Fix** (existing-file edits only — frozen):
1. In `forwarder.go`, accumulate the per-event loss entries into a slice local to the forward loop, and surface them on the produced `UsageRecordDraft` via a NEW field on `UsageRecordDraft` (forwarder_types.go, existing-file edit) e.g. `StreamProtocolLoss []proto.ProtocolLossEntry` (or json.RawMessage). Append from all three sites.
2. In `chat_completions_stream.go` (around the settle build at 577), MERGE the draft's accumulated stream losses with the initial `ex.protocolLoss` (request-translation) into the SettleRequest.ProtocolLoss (concatenate entry slices, marshal once). Keep order: request-translation first, then per-event in emission order.
**Test** (forwarder_test.go, discriminating): drive a forward with a stub adapter whose ProviderEventToCanonicalEvents / CanonicalEventToClientChunk return non-empty loss entries on some events; assert the resulting draft's accumulated stream losses contain them in order. MUTATION: keep the `_` discards → draft losses empty → RED. Plus a stream-level test that the merged SettleRequest.ProtocolLoss = request-translation ∪ per-event.

## Success criteria
- All 4 paths persist captured protocol_loss (no silent `[]` where losses exist).
- 4 discriminating tests, each with a stated mutation that goes RED.
- No frozen-package new files. No migration (protocol_loss column exists, migration 0043). No money/charge change. No abort-path behavior change.
- build + vet + unit + relevant integration_pg green.

## Blast radius
settlementrecovery/payload.go(+payload_test.go); gatewayhttp/chat_completions_billing.go (items 2,3) + its test; gateway/forwarder.go + forwarder_types.go (item 4) + forwarder_test.go; gatewayhttp/chat_completions_stream.go (item 4 merge). Generated sqlc: none (no query change). Tests that may need call-site touch-ups: post_delivery_recovery_test.go (item 3 assertion).

## What could go wrong / decision points
- **D1 (item 2)**: double-counting request-translation losses if dispatchBufferedEnvelope already folds them into env. Mitigation: snapshot is idempotent per-entry only if we don't append duplicates; confirm where request losses land before adding.
- **D2 (item 4)**: UsageRecordDraft is in the FROZEN gateway package — adding a FIELD to an existing struct in an existing file is allowed (#13 forbids new FILES, not new fields). Confirm no new file.
- **D3 (item 4 merge)**: marshal cost — merge entry slices then marshal once (avoid concatenating JSON arrays as bytes).
- **D4**: ordering/dedupe of loss entries — keep insertion order; do not dedupe (each entry is a distinct observation; the audit wants all).

## Decision
Lean to implement all 4 as above. Compare with codex parallel draft; on agreement, dispatch codex to implement; #8 review ≤2 rounds; land fix/hermes. No Owner stop unless D1/D2 surface a real conflict.
