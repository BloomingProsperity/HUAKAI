# 2026-04-29 Codex Reviewer-Lane Audit: Slice 4 (F-GW-002)

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane (read-only sandbox) |
| Audit date | 2026-04-29 |
| Initial verdict | **REJECT** — 4 HIGH-severity findings, 4 MED |
| Maintainer response | Owner directive "规则是规则，但功能不能丢" — verify findings as impl bugs vs test gaps; fix impl bugs, strengthen relevant tests, accept rest as backlog |
| Post-fix commit | `c5ce2dc` |
| Impl bugs surfaced by this audit | (1) drain cost cap short-circuit returned CostExhausted on first event when cap > 0 with no accumulation; (2) UsageAccumulator never froze on terminal frame, allowing post-`message_stop` ghost frames to overwrite |

---

# Phase 4 v0.1 slice 4 (F-GW-002) Test Coverage Audit

## Coverage Matrix
| AT-ID | Status | Notes |
|---|---|---|
| AT-GW-002-01 | COVERED-WEAK | Spec requires per-event write and explicit flush, plus first-token latency tracking at `docs/specs/streaming-forwarder.md:62` and `docs/specs/streaming-forwarder.md:63`; test `TestAT_GW_002_01_FirstEventFlushObservable` only asserts total `Forward` duration `<1s`, non-empty body, and `FirstTokenLatencyMillis < 0` at `backend/internal/gateway/forwarder_test.go:96`, `backend/internal/gateway/forwarder_test.go:102`, `backend/internal/gateway/forwarder_test.go:105`, `backend/internal/gateway/forwarder_test.go:108`. It does not observe `Flush()` timing or first-event visibility while upstream remains open. |
| AT-GW-002-02 | COVERED-WEAK | Spec requires upstream to canonical to client protocol translation preserving usage at `docs/specs/streaming-forwarder.md:58` and `docs/specs/streaming-forwarder.md:181`; test asserts usage `100/250` and graceful end at `backend/internal/gateway/forwarder_test.go:122`, `backend/internal/gateway/forwarder_test.go:126`, `backend/internal/gateway/forwarder_test.go:129`, but `newForwarder` only sets `UpstreamAdapter` and leaves `ClientAdapter` nil at `backend/internal/gateway/forwarder_test.go:70`, so the canonical to chat leg is not actually covered. |
| AT-GW-002-03 | SKIPPED | Spec requires pre-stream failover on configured status list at `docs/specs/streaming-forwarder.md:182`; test skips as above-forwarder Phase 4.5 orchestration at `backend/internal/gateway/forwarder_test.go:322`. Skip reason is plausible for this narrow forwarder slice, but Released-spec coverage is absent. |
| AT-GW-002-04 | SKIPPED | Spec requires sanitized non-failover 4xx response at `docs/specs/streaming-forwarder.md:183`; test skips as chat-completions handler work at `backend/internal/gateway/forwarder_test.go:325`. Plausible layer boundary, not covered here. |
| AT-GW-002-05 | SKIPPED | Spec requires buffered missing `message_start` to return 502 at `docs/specs/streaming-forwarder.md:184`; test skips because current file is streaming-only at `backend/internal/gateway/forwarder_test.go:328`. Plausible layer boundary, not covered here. |
| AT-GW-002-06 | COVERED-WEAK | Spec requires scanner oversize typed terminal failure and no silent truncation at `docs/specs/streaming-forwarder.md:51` and `docs/specs/streaming-forwarder.md:185`; test asserts `ErrScannerOverflow` and `response_event_too_large` at `backend/internal/gateway/forwarder_test.go:140`, `backend/internal/gateway/forwarder_test.go:141`, `backend/internal/gateway/forwarder_test.go:144`, but does not assert client output was not partially/truncatedly emitted. |
| AT-GW-002-07 | COVERED-WEAK | Spec requires client disconnect exits with accumulated usage and drain outcome at `docs/specs/streaming-forwarder.md:136`, `docs/specs/streaming-forwarder.md:137`, and `docs/specs/streaming-forwarder.md:186`; test injects usage `50/75` at `backend/internal/gateway/forwarder_test.go:154` but only asserts `client_disconnect` and non-`not_drained` at `backend/internal/gateway/forwarder_test.go:160`, `backend/internal/gateway/forwarder_test.go:164`. It never asserts accumulated usage survived. |
| AT-GW-002-08 | COVERED | Spec requires last-non-zero-wins per usage field at `docs/specs/streaming-forwarder.md:187`; test gives `10/20`, then `0/30`, then `0/0`, and asserts input remains `10` while output becomes `30` at `backend/internal/gateway/forwarder_test.go:173`, `backend/internal/gateway/forwarder_test.go:174`, `backend/internal/gateway/forwarder_test.go:183`, `backend/internal/gateway/forwarder_test.go:186`. |
| AT-GW-002-09 | COVERED-WEAK | Spec requires disconnect drain to read upstream events, extract usage, not write downstream, and exit on any budget at `docs/specs/streaming-forwarder.md:91`, `docs/specs/streaming-forwarder.md:92`, `docs/specs/streaming-forwarder.md:93`, `docs/specs/streaming-forwarder.md:190`; test appends tail events at `backend/internal/gateway/forwarder_test.go:201` but only accepts any budget enum at `backend/internal/gateway/forwarder_test.go:215`. It does not prove events were consumed, usage was extracted, or downstream was not written during drain. |
| AT-GW-002-10 | COVERED-WEAK | Spec requires drain stop on `drain_max_estimated_cost`, not just time, at `docs/specs/streaming-forwarder.md:91` and `docs/specs/streaming-forwarder.md:191`; test sets nonzero `MaxEstimatedCost` and asserts `DrainBudgetCostExhausted` at `backend/internal/gateway/forwarder_test.go:232`, `backend/internal/gateway/forwarder_test.go:238`, but does not verify cost accumulation or that byte/time budgets remained unexhausted after consuming a meaningful event set. |
| AT-GW-002-11 | COVERED-WEAK | Spec requires eight-axis timeout independence, specifically total-stream before inter-event when both apply, at `docs/specs/streaming-forwarder.md:41`, `docs/specs/streaming-forwarder.md:77`, `docs/specs/streaming-forwarder.md:78`, `docs/specs/streaming-forwarder.md:79`, and `docs/specs/streaming-forwarder.md:192`; test instead disables total timeout and asserts first-token timeout at `backend/internal/gateway/forwarder_test.go:243`, `backend/internal/gateway/forwarder_test.go:249`, `backend/internal/gateway/forwarder_test.go:250`, `backend/internal/gateway/forwarder_test.go:255`. This does not cover the stated AT. |
| AT-GW-002-12 | COVERED-WEAK | Spec requires `RESPONSE_EVENT_TOO_LARGE`, no charge, and alert at `docs/specs/streaming-forwarder.md:139`, `docs/specs/streaming-forwarder.md:141`, `docs/specs/streaming-forwarder.md:142`, and `docs/specs/streaming-forwarder.md:193`; test asserts end class, usage source not reported, and zero tokens at `backend/internal/gateway/forwarder_test.go:268`, `backend/internal/gateway/forwarder_test.go:271`, `backend/internal/gateway/forwarder_test.go:274`, but has no operator alert/metric assertion. |
| AT-GW-002-13 | SKIPPED | Spec requires mid-stream failover blocked by default after first content at `docs/specs/streaming-forwarder.md:194`; test skips as Phase 4.5 orchestration at `backend/internal/gateway/forwarder_test.go:331`. Plausible layer boundary, not covered here. |
| AT-GW-002-14 | SKIPPED | Spec requires mid-stream failover only with `Idempotent-Stream-Replay: true` at `docs/specs/streaming-forwarder.md:195`; test skips pending handler work at `backend/internal/gateway/forwarder_test.go:334`. Plausible layer boundary, not covered here. |
| AT-GW-002-15 | COVERED-WEAK | Spec requires multi-source usage conflict logged and terminal frame wins at `docs/specs/streaming-forwarder.md:196`; test only sends two `message_delta` usage frames and asserts final values `333/444` at `backend/internal/gateway/forwarder_test.go:283`, `backend/internal/gateway/forwarder_test.go:286`, `backend/internal/gateway/forwarder_test.go:294`. This overlaps AT-08 last-non-zero behavior and does not prove terminal-frame priority over another source or conflict logging. |
| AT-GW-002-16 | SKIPPED | Spec requires Tx2 crash/orphan sweep finalization within budget at `docs/specs/streaming-forwarder.md:105` and `docs/specs/streaming-forwarder.md:197`; test skips as F-OBS-001 slice 5 work at `backend/internal/gateway/forwarder_test.go:337`. Valid cross-feature deferral, not covered in this slice. |
| AT-GW-002-17 | SKIPPED | Spec requires 100 concurrent streams across 5 tenants with no cross-tenant data at `docs/specs/streaming-forwarder.md:198`; test skips full HTTP stack at `backend/internal/gateway/forwarder_test.go:340`. The skip is understandable, but the test suite has no concurrency or tenant-isolation coverage. |
| AT-GW-002-18 | COVERED-WEAK | Spec requires zero accumulator plus `UNKNOWN_TERMINATION` to abort claim/no-charge under `AMBIGUOUS_USAGE` at `docs/specs/streaming-forwarder.md:148`, `docs/specs/streaming-forwarder.md:149`, `docs/specs/streaming-forwarder.md:150`, and `docs/specs/streaming-forwarder.md:199`; test uses empty EOF and only asserts not graceful plus zero tokens at `backend/internal/gateway/forwarder_test.go:308`, `backend/internal/gateway/forwarder_test.go:310`, `backend/internal/gateway/forwarder_test.go:313`. It does not assert `UNKNOWN_TERMINATION`, `AMBIGUOUS_USAGE`, `ErrAmbiguousUsage`, or claim abort. |
| AT-GW-002-19 | SKIPPED | Spec requires EOF without terminal to produce inferred usage with `confidence_score` at `docs/specs/streaming-forwarder.md:115`, `docs/specs/streaming-forwarder.md:171`, and `docs/specs/streaming-forwarder.md:200`; test skips due missing tokenizer at `backend/internal/gateway/forwarder_test.go:343`. Skip is only partly valid: tokenizer confidence may be deferred, but `pending_reconciliation` is already a draft field and should be asserted for EOF-no-terminal. |

## Assertion Strength Findings
- F-001: `TestAT_GW_002_11_FirstTokenTimeoutFires` tests first-token timeout with total timeout disabled, while the AT requires total-stream beating inter-event when both apply. Severity: HIGH.
- F-002: `TestAT_GW_002_09_BoundedDrainBudgetExhausts` does not prove drain consumes upstream events, extracts usage, or suppresses downstream writes during drain. Severity: HIGH.
- F-003: `TestAT_GW_002_15_MultiSourceConflictTerminalFrameWins` only proves later nonzero usage overwrites earlier usage, not terminal-frame priority or conflict logging. Severity: HIGH.
- F-004: `TestAT_GW_002_18_AmbiguousUsageNoCharge` does not assert the required `UNKNOWN_TERMINATION`/`AMBIGUOUS_USAGE` abort path. Severity: HIGH.
- F-005: `TestAT_GW_002_19_TokenizerFallbackInferredUsage` skip overreaches; pending reconciliation is settable without tokenizer. Severity: MED.
- F-006: `TestAT_GW_002_07_ClientDisconnectExitsWithUsage` injects `50/75` usage but never asserts the returned draft preserved it. Severity: MED.
- F-007: `TestAT_GW_002_01_FirstEventFlushObservable` does not observe `Flush()` or first-event delivery before stream completion. Severity: MED.
- F-008: `TestAT_GW_002_02_ProtocolTranslationPreservesUsage` does not exercise canonical-to-chat client translation because `ClientAdapter` is nil. Severity: MED.
- F-009: `TestAT_GW_002_12_OversizeTerminalNoCharge` has no alert/metric assertion. Severity: LOW.

## Stub Fidelity Findings
- No production SQL stubs are present in `backend/internal/gateway/forwarder_test.go`, so tenant/status/deleted filters are not applicable in this file.
- The upstream F-PROTO-002 touchpoint is real enough for upstream parsing: `newForwarder` uses `proto.AnthropicAdapter` at `backend/internal/gateway/forwarder_test.go:70`, and the adapter contract is `ProviderEventToCanonicalEvents` at `backend/internal/proto/proto.go:27`. The client protocol leg is not real because no `ClientAdapter` is installed.

## Cross-Feature Gaps
- F-PROTO-002 boundary is only partially exercised: upstream Anthropic to canonical is covered, but canonical to client chat output is not.
- F-OBS-001 Tx2, orphan sweep, billing claim abort, audit row, metrics, and alerts are deferred or absent, so AT-12, AT-16, and AT-18 cannot be considered fully Released-spec covered.
- No `Gate` chain issue observed in this file; no gates are instantiated.
- Tenant isolation under load is entirely skipped; no substitute unit-level concurrency test exists.

## Recommended Additional Tests (priority order)
1. Add AT-GW-002-11 test with steady events where `TotalStreamTimeout < InterEventTimeout`, asserting `total_stream_timeout`, not first-token timeout.
2. Add AT-GW-002-09 test with post-disconnect usage-bearing events, a recording writer, and assertions that drain updates partial usage and does not write downstream.
3. Add AT-GW-002-15 test with distinct usage sources plus conflict log evidence, asserting terminal-frame priority is not just last-non-zero overwrite.
4. Add AT-GW-002-18 test that forces `UNKNOWN_TERMINATION` with zero accumulator and asserts `AMBIGUOUS_USAGE`/abort handoff semantics.
5. Replace or narrow AT-GW-002-19 skip with EOF-no-terminal assertions for `pending_reconciliation=true`; keep tokenizer confidence as a deferred subcase if needed.
6. Strengthen AT-GW-002-01 with a blocking upstream/recording flusher that observes first flush before stream completion.
7. Strengthen AT-GW-002-02 by installing a real or contract-valid client adapter and asserting the emitted chat chunk shape plus preserved usage.
8. Strengthen AT-GW-002-07 by asserting accumulated usage values after disconnect.

## Final Verdict
- Phase 4 v0.1 slice 4: REJECT
- Coverage % rough: 1 / 19 AT-IDs effectively covered; 10 additional tests exist but are weak; 8 are skipped.
- Blocks next slice? YES, unless Owner explicitly accepts slice 5 as parallel F-OBS-001 work while these high-severity test gaps remain open.

- HIGH-severity findings: AT-09 drain consumption not proven; AT-11 tests the wrong timeout invariant; AT-15 terminal-frame/conflict behavior not proven; AT-18 ambiguous-usage abort path not proven.
- Recommendation: REJECT
- Blocks slice 5? YES for Released-spec status; NO only if slice 5 is treated as parallel work to add the missing Tx2/observability coverage.

Owner 总结：这组测试目前更像 forwarder 骨架冒烟测试，而不是 Released spec 的完整验收覆盖；19 个 AT 中只有 AT-08 可以算强覆盖，最高优先级必须补 AT-11 的 timeout 轴独立性、AT-09 的 drain 实际消费与不下写、AT-15 的 terminal-frame + conflict log、AT-18 的 ambiguous abort 路径。当前不建议把 Phase 4 v0.1 slice 4 标为 Released-spec 覆盖通过；若继续 slice 5，只能作为并行补 F-OBS-001/Tx2 能力推进，不能视为本 slice 已解除阻塞。
