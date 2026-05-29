# 2026-05-29 S1025 Protocol-Loss FU Codex Plan

Owner directive: "independently DRAFT AN IMPLEMENTATION PLAN (not code) for a HUAKAI bug-fix slice. Money/audit path." This plan covers all 4 deferred S2 protocol-loss evidence completeness findings from `docs/process/reviews/DEFERRED-S1-025-protocol-loss.md:10`.

Scope: implementation and tests only for protocol-loss evidence carriers. No migration: `billing.SettleRequest.ProtocolLoss` already exists at `backend/internal/billing/billing.go:99`, and Settle / Abort / CommitCacheHit already persist it through `jsonOrEmptyArray` at `backend/internal/billing/settler.go:172`, `backend/internal/billing/settler.go:366`, and `backend/internal/billing/settler.go:519`. No money amount, chargeability, quota, or abort behavior changes.

## Item 1 - Recovery DLQ Replay Preserves ProtocolLoss

### files

- `backend/internal/settlementrecovery/payload.go:56` / `settleRequestPersisted`
- `backend/internal/settlementrecovery/payload.go:97` / `FromCompletionEvent`
- `backend/internal/settlementrecovery/payload.go:153` / `Payload.ToSettleRequest`
- Tests in existing files:
  - `backend/internal/settlementrecovery/payload_test.go:18` / `fixtureCompletionEvent`
  - `backend/internal/settlementrecovery/payload_test.go:63` / `TestPayload_RoundTrip_SettleRequestFieldsByteIdentical`
  - `backend/internal/gatewayhttp/post_delivery_recovery_test.go:64` / `newPostDeliveryFixtureEvent`
  - `backend/internal/gatewayhttp/post_delivery_recovery_test.go:112` / `TestSettleCompletionWithRecovery_PostDeliveryFailureEnqueues`

### change

- Add `ProtocolLoss json.RawMessage` to `settleRequestPersisted`, aligned with `billing.SettleRequest.ProtocolLoss` at `backend/internal/billing/billing.go:99`.
- In `FromCompletionEvent`, copy `req.ProtocolLoss` into the persisted payload. The current copier covers `Stream` then `Draft` at `backend/internal/settlementrecovery/payload.go:123`, but has no protocol-loss field.
- In `ToSettleRequest`, restore `ProtocolLoss` into the replayed `billing.SettleRequest`. The current reconstruction jumps from `Stream` to `Draft` at `backend/internal/settlementrecovery/payload.go:170`.
- Do not persist or restore `OutboxEmitter`; the existing nil replay behavior at `backend/internal/settlementrecovery/payload.go:175` remains unchanged.

### frozen-check

- `backend/internal/settlementrecovery` is not a frozen package.
- `backend/internal/gatewayhttp` is frozen, but this plan edits an existing test file only and adds no file there. Compliant with the frozen package rule.
- No `.go` file is changed during plan drafting.

### discriminating test + mutation

- In `payload_test.go`, set `fixtureCompletionEvent(t).SettleRequest.ProtocolLoss` to:

```json
[{"severity":"warning","code":"dlq_protocol_loss_roundtrip","reason":"settlement recovery must replay audit evidence"}]
```

- Extend `TestPayload_RoundTrip_SettleRequestFieldsByteIdentical` to assert JSON semantic equality between `got.ProtocolLoss` and `original.ProtocolLoss`.
- Mutation that turns it red: remove the `ProtocolLoss` assignment from either `FromCompletionEvent` or `ToSettleRequest`; `got.ProtocolLoss` becomes empty / nil while the fixture contains the sentinel JSON.
- In `post_delivery_recovery_test.go`, set `newPostDeliveryFixtureEvent().SettleRequest.ProtocolLoss` to the same sentinel and assert the decoded DLQ payload's `ToSettleRequest().ProtocolLoss` equals it after `settleCompletionWithRecovery` enqueues at `backend/internal/gatewayhttp/chat_completions_billing.go:207`.
- Mutation that turns it red: remove the field from `settleRequestPersisted`; the DLQ payload still enqueues, but replay sees nil and the assertion fails.

### risk

- Main risk is field drift: `settleRequestPersisted` manually mirrors `billing.SettleRequest`, as documented at `backend/internal/settlementrecovery/payload.go:56`. Keep the new field near `Stream` / `Draft` so future audits can compare order against `backend/internal/billing/billing.go:81`.
- Existing `TestSettler_SettleWritesProtocolLossEvidence` already proves `Settler.Settle` persists a non-empty `req.ProtocolLoss` to `usage_records.protocol_loss` at `backend/internal/billing/settler_integration_test.go:348`, so this item should focus on the replay carrier, not duplicate DB coverage.

## Item 2 - Non-Streaming Snapshot After All Loss Sources

### files

- `backend/internal/gatewayhttp/chat_completions_dispatch.go:337` / `chatExecution.dispatchCanonicalBuffered`
- `backend/internal/gatewayhttp/chat_completions_billing.go:40` / `chatExecution.executeNonStreamingAttempt`
- `backend/internal/gatewayhttp/chat_completions_billing.go:146` / protocol-loss helper area
- Tests in existing files:
  - `backend/internal/gatewayhttp/chat_completions_billing_test.go:244` area for direct non-stream settle assertions
  - `backend/internal/gatewayhttp/chat_completions_handler_headers_test.go:191` / `TestChatCompletionResponseFailOpenWhenSignerNilInProduction`
  - `backend/internal/gatewayhttp/chat_completions_handler_clientadapter_test.go:19` mock dispatcher area, if a reusable preserving dispatcher helper is needed

### change

- Request-translation losses:
  - Change `canonicalReq, _, err := ex.clientAdapter.RequestToCanonical(...)` at `backend/internal/gatewayhttp/chat_completions_dispatch.go:338` to capture `requestLosses`.
  - Set `ex.protocolLoss = protocolLossJSONFromEntries(requestLosses)` immediately after the call, before the invalid-request abort branches at `backend/internal/gatewayhttp/chat_completions_dispatch.go:340` and `backend/internal/gatewayhttp/chat_completions_dispatch.go:347`.
  - After `canonicalReq != nil`, append `requestLosses` to `canonicalReq.CapabilityGraph.ProtocolLoss` before dispatch. Current adapters return losses separately, for example OpenAI Chat request losses at `backend/internal/proto/openai_chat_request.go:82` and return `env, losses` at `backend/internal/proto/openai_chat_request.go:223`; they do not put those losses into the envelope themselves.
- Ledger / trust-chain warnings:
  - Keep or replace the early snapshot at `backend/internal/gatewayhttp/chat_completions_billing.go:51`, but take a mandatory snapshot immediately after `submitAuditLedgerEntry` returns at `backend/internal/gatewayhttp/chat_completions_billing.go:52` and before the `err != nil` abort branch at `backend/internal/gatewayhttp/chat_completions_billing.go:53`.
  - Evidence: `submitAuditLedgerEntry` appends warnings into `env.CapabilityGraph.ProtocolLoss` through `appendTrustChainWarning` at `backend/internal/gatewayhttp/chat_completions_billing.go:442`, `backend/internal/gatewayhttp/chat_completions_billing.go:449`, `backend/internal/gatewayhttp/chat_completions_billing.go:466`, `backend/internal/gatewayhttp/chat_completions_billing.go:481`, `backend/internal/gatewayhttp/chat_completions_billing.go:505`, `backend/internal/gatewayhttp/chat_completions_billing.go:508`, and `backend/internal/gatewayhttp/chat_completions_billing.go:514`. The append helper writes the actual `ProtocolLossEntry` at `backend/internal/gatewayhttp/chat_completions_billing.go:535`.
- Client response-conversion losses:
  - Change `clientBody, _, err := ex.clientAdapter.CanonicalToClientResponse(...)` at `backend/internal/gatewayhttp/chat_completions_billing.go:62` to capture `clientLosses`.
  - Append `clientLosses` to `bufferedEnv.CapabilityGraph.ProtocolLoss`, then update `ex.protocolLoss = protocolLossJSONFromEnv(bufferedEnv)` before the `canonical_response_error` abort branch at `backend/internal/gatewayhttp/chat_completions_billing.go:63`.
  - Leave `nonStreamingSettleRequest` unchanged except that it will read the final `ex.protocolLoss` at `backend/internal/gatewayhttp/chat_completions_billing.go:139`.
- Upstream response adapter losses are already captured by the HCSF dispatcher: `ProviderResponseToCanonical` returns `respLosses` at `backend/internal/gateway/upstream_dispatcher_hcsf.go:131`, and those are appended at `backend/internal/gateway/upstream_dispatcher_hcsf.go:150`.

### frozen-check

- `backend/internal/gatewayhttp` is frozen. All implementation and test changes are existing-file edits only. No new files.
- `backend/internal/proto` is frozen; this plan reads it for evidence only and does not require editing it.

### discriminating test + mutation

- Request-side loss test:
  - Fixture: non-stream OpenAI Chat body with `metadata`, e.g. `{"model":"gpt-4o","stream":false,"metadata":{"trace":"x"},"messages":[{"role":"user","content":"hi"}]}`. `OpenAIChatClient.RequestToCanonical` emits code `d5_metadata_field_pending` for metadata at `backend/internal/proto/openai_chat_request.go:99`.
  - Use a preserving test dispatcher that returns a buffered response while copying `requestEnvelope.CapabilityGraph.ProtocolLoss` into the response envelope. Assert `settler.calls[0].ProtocolLoss` contains code `d5_metadata_field_pending`.
  - Mutation that turns it red: revert the capture in `dispatchCanonicalBuffered` back to `_`, or forget to append `requestLosses` into the request envelope; the preserving dispatcher receives no request-side loss, and settlement lacks the code.
- Ledger/trust-chain loss test:
  - Extend `TestChatCompletionResponseFailOpenWhenSignerNilInProduction` at `backend/internal/gatewayhttp/chat_completions_handler_headers_test.go:191` with a `recordingSettler`.
  - Keep the existing fixture where `d.Signer = nil` and `AuditLedgerDLQ` is set; this path appends `audit_signer_deferred` at `backend/internal/gatewayhttp/chat_completions_billing.go:481`.
  - Assert `settler.calls[0].ProtocolLoss` contains `audit_signer_deferred`, not just that `dispatcher.returned.CapabilityGraph.ProtocolLoss` contains it.
  - Mutation that turns it red: leave the only snapshot at `backend/internal/gatewayhttp/chat_completions_billing.go:51`; the envelope gains `audit_signer_deferred` later, but `SettleRequest.ProtocolLoss` remains stale.
- Client response-conversion loss test:
  - Fixture: custom dispatcher returns a `proto.CanonicalResponse` with `StopReason: proto.CanonicalStopUnknown`. `OpenAIChatClient.CanonicalToClientResponse` maps that to protocol-loss code `stop_reason_unknown` at `backend/internal/proto/openai_chat_response.go:72`.
  - Assert the non-stream settle request contains `stop_reason_unknown`.
  - Mutation that turns it red: keep discarding the second return value from `CanonicalToClientResponse`; response body still succeeds, but `ProtocolLoss` lacks `stop_reason_unknown`.

### risk

- Double-counting: current request adapters return protocol-loss entries separately and do not append them to the envelope. Append once in `dispatchCanonicalBuffered`; do not also add adapter-level changes. If a future adapter both returns and embeds the same loss, that future adapter should be fixed or a deliberate dedupe helper should be added with tests. Do not dedupe now because identical-looking losses may represent separate request fields.
- Abort behavior: this item only changes the evidence payload passed to existing abort calls. It must not change whether the abort path fires or the abort reason.

## Item 3 - Audit-Ref-Missing Abort Uses Event ProtocolLoss

### files

- `backend/internal/gatewayhttp/chat_completions_billing.go:269` / `rejectMoneyPathAuditRef`
- Tests in existing files:
  - `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:21` / `recordingSettler` and `recordedAbort`
  - `backend/internal/gatewayhttp/chat_completions_billing_test.go:244` area for direct settle rejection tests
  - `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:246` area for cache-hit commit rejection tests

### change

- In `rejectMoneyPathAuditRef`, replace the nil protocol-loss argument at `backend/internal/gatewayhttp/chat_completions_billing.go:275` with `event.SettleRequest.ProtocolLoss`.
- No new eventbus field is needed. `eventbus.RequestCompletionEvent` already carries `SettleRequest billing.SettleRequest` at `backend/internal/eventbus/types.go:75`, and `billing.SettleRequest` already carries `ProtocolLoss` at `backend/internal/billing/billing.go:99`.
- Direct non-stream events already carry the settle request at `backend/internal/gatewayhttp/chat_completions_billing.go:95`; cache-hit audit events carry `cacheHitReq` at `backend/internal/gatewayhttp/chat_completions_handler_headers.go:239`, and `cacheHitReq.ProtocolLoss` is populated from `cachedEnv` at `backend/internal/gatewayhttp/chat_completions_handler_headers.go:220`.

### frozen-check

- `backend/internal/gatewayhttp` is frozen. All edits are existing-file edits only. No new files.

### discriminating test + mutation

- First update `recordedAbort` to store `protocolLoss json.RawMessage`, and update `recordingSettler.Abort` at `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:40` to copy the argument.
- Add a focused table test in `chat_completions_billing_test.go`:
  - Fixture: `eventbus.RequestCompletionEvent{TenantID: 7, ClaimID: 9, RequestID: "req-audit-ref-loss", SettleRequest: billing.SettleRequest{ProtocolLoss: json.RawMessage(`[{"severity":"warning","code":"audit_ref_abort_sentinel","reason":"audit-ref reject must preserve losses"}]`)}}`
  - `ChatHandlerDeps{Settler: &recordingSettler{}}`
  - Call both wrappers: `rejectMoneyPathDirectSettle(ctx, d, event, eventbus.ErrAuditRefMissing)` and `rejectMoneyPathCacheHitCommit(ctx, d, event, eventbus.ErrAuditRefMissing)` in separate subtests.
  - Assert exactly one abort per subtest and that `settler.aborts[0].protocolLoss` semantically equals the sentinel JSON.
- Mutation that turns it red: keep passing nil at `backend/internal/gatewayhttp/chat_completions_billing.go:275`; the abort reason assertion still passes, but the protocol-loss assertion fails.

### risk

- This does not change audit-ref validation or rejection. It only improves the zero-cost abort record's evidence payload.
- The event-level answer is clear: `RequestCompletionEvent` already carries the needed losses through `SettleRequest.ProtocolLoss`; threading a parallel field would create drift risk.

## Item 4 - Streaming Per-Event Losses Reach Settle

### files

- `backend/internal/gateway/forwarder_types.go:78` / `UsageRecordDraft`
- `backend/internal/gateway/forwarder_types.go:140` / `UsageAccumulator`
- `backend/internal/gateway/forwarder.go:251` / `StreamForwarder.handleEventWithAdapter`
- `backend/internal/gateway/forwarder.go:319` / `StreamForwarder.finalizeClientStream` remains unchanged unless tests prove final client-only chunks need a carrier; current interface returns no losses at `backend/internal/proto/proto.go:28`.
- `backend/internal/gateway/forwarder.go:340` / `StreamForwarder.drainWithAdapter`
- `backend/internal/gateway/forwarder.go:386` / `StreamForwarder.clientChunks`
- `backend/internal/gateway/forwarder.go:394` / `StreamForwarder.finishDraft`
- `backend/internal/gateway/forwarder_hop_chain.go:115` / `StreamForwarder.emitFinalUpstreamEvents`
- `backend/internal/gatewayhttp/chat_completions_stream.go:526` / `chatExecution.streamingCompletionEvent`
- `backend/internal/gatewayhttp/chat_completions_billing.go:153` helper area for a merge helper
- Tests in existing files:
  - `backend/internal/gateway/forwarder_test.go` or `backend/internal/gateway/forwarder_clientadapter_test.go`
  - `backend/internal/gatewayhttp/chat_completions_pricing_test.go:63` area for `streamingCompletionEvent` unit tests

### change

- Accumulation location:
  - Add `ProtocolLoss []proto.ProtocolLossEntry` to `UsageAccumulator` so the hot forwarding loop can accumulate losses alongside usage signals.
  - Add `ProtocolLoss []proto.ProtocolLossEntry` to `UsageRecordDraft` as the carrier returned by `StreamForwarder.Forward`. This is the carrier surface into the HTTP settle path.
- Provider event losses:
  - In `handleEventWithAdapter`, change `canonicalEvents, _, err := adapter.ProviderEventToCanonicalEvents(...)` at `backend/internal/gateway/forwarder.go:279` to capture `eventLosses`, append them to `acc.ProtocolLoss`, then process errors as today.
  - In `drainWithAdapter`, change `canonicalEvents, _, err := adapter.ProviderEventToCanonicalEvents(...)` at `backend/internal/gateway/forwarder.go:368` to capture drain-stage losses and append them to `acc.ProtocolLoss` when parsing succeeds.
- Client event losses:
  - Change `clientChunks` at `backend/internal/gateway/forwarder.go:386` to return `([][]byte, []proto.ProtocolLossEntry, error)` by preserving the second return value from `CanonicalEventToClientChunk` at `backend/internal/gateway/forwarder.go:390`.
  - Update callers at `backend/internal/gateway/forwarder.go:295` and `backend/internal/gateway/forwarder_hop_chain.go:138` to append client losses to the same accumulator before handling errors.
- Draft output:
  - In `finishDraft`, copy `acc.ProtocolLoss` into `d.ProtocolLoss` before returning the draft at `backend/internal/gateway/forwarder.go:424`.
- Settle merge:
  - In `streamingCompletionEvent`, replace `ProtocolLoss: ex.protocolLoss` at `backend/internal/gatewayhttp/chat_completions_stream.go:577` with a merged JSON value combining request-translation losses already stored in `ex.protocolLoss` at `backend/internal/gatewayhttp/chat_completions_stream.go:344` and per-event losses from `draft.ProtocolLoss`.
  - Add a helper in the existing `chat_completions_billing.go` protocol-loss helper area to decode base `json.RawMessage` into `[]proto.ProtocolLossEntry`, append typed entries, and marshal through `protocolLossJSONFromEntries`.

### frozen-check

- `backend/internal/gateway` and `backend/internal/gatewayhttp` are frozen. This plan edits existing files only and adds no new files.
- Adding a field to an existing struct in an existing file is allowed by the task constraints.

### discriminating test + mutation

- Forwarder accumulation test:
  - Fixture: a test upstream adapter whose `ProviderEventToCanonicalEvents` returns one canonical text delta and loss code `provider_event_loss_sentinel`, and a test client adapter whose `CanonicalEventToClientChunk` returns one SSE chunk plus loss code `client_event_loss_sentinel`.
  - Run `StreamForwarder.Forward` with one non-terminal event and one terminal event. Assert returned `draft.ProtocolLoss` contains both codes.
  - Mutation that turns it red: discard provider event losses at `backend/internal/gateway/forwarder.go:279` or discard client event losses at `backend/internal/gateway/forwarder.go:390`; the stream still writes chunks and usage, but one sentinel code is absent.
- Stream settle merge test:
  - In `chat_completions_pricing_test.go`, create a `chatExecution` with `ex.protocolLoss = json.RawMessage(`[{"severity":"info","code":"request_translation_loss_sentinel","reason":"request translation"}]`)`.
  - Pass a `gateway.UsageRecordDraft{ProtocolLoss: []proto.ProtocolLossEntry{{Severity: proto.ProtocolLossWarning, Code: "stream_event_loss_sentinel", Reason: "provider/client stream event"}}}` to `streamingCompletionEvent`.
  - Assert `event.SettleRequest.ProtocolLoss` semantically contains both `request_translation_loss_sentinel` and `stream_event_loss_sentinel`.
  - Mutation that turns it red: leave `ProtocolLoss: ex.protocolLoss` at `backend/internal/gatewayhttp/chat_completions_stream.go:577`; the request sentinel remains but the stream-event sentinel is missing.

### risk

- Do not put accumulation in a new non-frozen package for this slice; doing so would add abstraction without reducing risk. The forwarder already owns per-event adapter calls, and existing-file edits satisfy the frozen constraint.
- Do not change stream settle / abort decision logic at `backend/internal/gatewayhttp/chat_completions_stream.go:249`. If a stream goes down the existing abort branch at `backend/internal/gatewayhttp/chat_completions_stream.go:263`, keep abort behavior unchanged per Owner constraint.
- Potential double-counting is controlled by source separation: `ex.protocolLoss` is request-translation evidence, while `draft.ProtocolLoss` is provider/client event evidence accumulated by the forwarder.

## Open design decisions

- Item 2 snapshot decision: update `ex.protocolLoss` after each source that can append losses and before the next abort branch. Specifically: after request translation in `dispatchCanonicalBuffered`, after `submitAuditLedgerEntry` returns, and after `CanonicalToClientResponse` returns. This covers abort paths and success settle because the abort calls at `backend/internal/gatewayhttp/chat_completions_billing.go:54`, `backend/internal/gatewayhttp/chat_completions_billing.go:64`, and `backend/internal/gatewayhttp/chat_completions_billing.go:73` all read `ex.protocolLoss`, and successful settle reads it through `nonStreamingSettleRequest` at `backend/internal/gatewayhttp/chat_completions_billing.go:139`.
- Item 2 missed vs already captured:
  - Missed now: request-translation losses because `RequestToCanonical` return losses are discarded at `backend/internal/gatewayhttp/chat_completions_dispatch.go:338`.
  - Missed now: ledger/trust-chain warnings because `ex.protocolLoss` is snapshotted before `submitAuditLedgerEntry` mutates the envelope at `backend/internal/gatewayhttp/chat_completions_billing.go:51`.
  - Missed now: client response-conversion losses because `CanonicalToClientResponse` return losses are discarded at `backend/internal/gatewayhttp/chat_completions_billing.go:62`.
  - Already captured: upstream response adapter losses in HCSF dispatch at `backend/internal/gateway/upstream_dispatcher_hcsf.go:131` and `backend/internal/gateway/upstream_dispatcher_hcsf.go:153`, as long as the caller snapshots after dispatch.
- Item 3 eventbus decision: `eventbus.RequestCompletionEvent` already carries losses through `event.SettleRequest.ProtocolLoss`; no parallel field should be added.
- Item 4 carrier decision: use `gateway.UsageRecordDraft.ProtocolLoss` as the carrier from `StreamForwarder.Forward` to `streamingCompletionEvent`, with accumulation in `UsageAccumulator`.
- Owner sign-off needed: none for the plan as written. Implementation must stop for Owner confirmation if someone proposes a migration, charge calculation change, abort-path behavioral change, new runtime dependency, or new file in a frozen package.

## Sequencing

Success criteria:

- Recovery DLQ payload round-trips non-empty `ProtocolLoss`.
- Non-streaming success settle and non-streaming abort paths include request, ledger/trust-chain, and client-response protocol losses.
- Audit-ref-missing abort reuses `event.SettleRequest.ProtocolLoss`.
- Streaming settle merges request-translation losses with per-event provider/client conversion losses.
- No `.sql` changes, no migration, no charge/quota/billing amount changes, no new files under frozen packages.

Time estimate:

- Implementation: 60-90 minutes.
- Tests and fixes: 60-120 minutes, depending on existing gatewayhttp test helper friction.

Blast radius:

- Audit evidence only: `usage_records.protocol_loss` and recovery DLQ payload shape.
- Packages touched during implementation: `settlementrecovery`, `gatewayhttp`, `gateway`, tests in those packages. No schema, auth, quota, or billing ledger amount logic.

Failure modes and mitigations:

- Missing one source: covered by separate discriminating sentinels for request, ledger, client-response, provider-event, and client-event losses.
- Frozen package violation: use only existing files under `backend/internal/gatewayhttp` and `backend/internal/gateway`; do not create new test files there.
- Non-discriminating tests: each test asserts an exact sentinel code and names the mutation that removes it.
- JSON equality flake: compare decoded arrays or code membership, not raw byte formatting.

Pre-execution checklist:

1. Confirm no implementation starts until the synthesized Claude/Codex plan is approved.
2. Confirm target edits are existing files for `backend/internal/gatewayhttp` and `backend/internal/gateway`.
3. Confirm no migration is generated and migration 0043 remains untouched.
4. Confirm no charge, quota, balance, claim status, or abort decision logic is changed.
5. Add tests first, verify they fail for the stated mutation mentally or by temporary local mutation if allowed in the implementation phase.

Execution order:

1. Item 1 first: low-risk carrier field in non-frozen `settlementrecovery`, plus replay payload tests.
2. Item 3 second: one-line abort evidence threading in `rejectMoneyPathAuditRef`, plus focused wrapper tests.
3. Item 2 third: non-streaming snapshot order and loss folding, with three source-specific tests.
4. Item 4 fourth: streaming forwarder carrier and stream settle merge, with forwarder and gatewayhttp unit tests.
5. Run targeted tests:
   - `PATH=/usr/local/go/bin:$PATH go test ./internal/settlementrecovery ./internal/gatewayhttp ./internal/gateway` from `backend/`.
   - If implementation touches helper behavior used by observability DLQ, also run `PATH=/usr/local/go/bin:$PATH go test ./internal/observability` from `backend/`.
6. Run `gofmt` on touched `.go` files during implementation.
7. Stage only intended changes and run required Codex per-commit review before any commit, per project rule. Do not commit during this plan-only task.
