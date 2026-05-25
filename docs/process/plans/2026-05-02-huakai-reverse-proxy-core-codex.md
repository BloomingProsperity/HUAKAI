# 2026-05-02 HUAKAI reverse proxy core refinement list

| Field | Value |
| --- | --- |
| Owner directive | "写 HUAKAI 的反向代理核心模块细化提升清单" |
| Scope | Reverse proxy core only: protocol adapters, WebSocket, streaming forwarder, upstream transport, TLS profile policy, error normalization, body/header firewalls, decompression, body cache and retry replay. |
| Out of scope | Payment provider implementation, auth core, billing ledger migration, quota enforcement migration, production deployment, runtime dependency changes. |
| Clean-room lane | This file is a behavior-level planning artifact from internal deep-dive summaries and released HUAKAI specs. It does not copy reference source, schemas, comments, UI, or file layout. |
| Naming discipline | Uses only codenames for references and vendors. No direct project or vendor names are used in the module plan. |
| Feature stance | No feature is dropped. Risk changes rollout, gating, or implementation method only. |

## Grounding and assumptions

- Observed input artifacts: 10 required internal planning/spec files were read for this plan.
- Inferences: module-level data structures and FSMs below are HUAKAI-local design proposals derived from the observed behavior requirements.
- Open questions: listed at the end. They require Owner or future specifier decisions before implementation.
- Priority meaning: `P0` blocks safe real-upstream reverse proxy, `P1` blocks commercial reliability, `P2` is post-MVP hardening, `P3` is later optimization.
- Type meaning: `passthrough` preserves upstream/client behavior, `创新` is HUAKAI-added stronger behavior, `架构卫生` is internal structure needed to keep later features safe.

## R1. 协议适配

**R1.1 Protocol pair and event mapping registry [P0] [类型: 架构卫生]**

基线-开源: Clean-Arch-Ref shows per-provider executors and dual HTTP/WS variants, but it bundles shape translation, credential injection, and error handling too tightly for HUAKAI's account spine. Retry-Policy-Ref validates a policy object model, but its gateway policy is not account-lease aware.

基线-官方: Vendor-X1, Vendor-X2, Vendor-X3, Vendor-X4, and Vendor-Meta each expose different request/stream envelopes. Official strategy requires explicit capability handling instead of silent lossy conversion.

HUAKAI 改动:
  算法: 1. Resolve `(client_protocol, upstream_protocol, event_type)` cell. 2. Load mapping version pinned on the route decision. 3. Reject `UNSUPPORTED` cells before account slot acquisition when possible. 4. For `LOSSY` cells, append `protocol_loss` and continue only if route policy allows safe equivalent. 5. Emit per-cell coverage counters.
  数据结构: `protocol_pair_matrix(client_protocol, upstream_protocol, event_type, verdict, mapper_version, loss_note_code, default_policy)`; index `(client_protocol, upstream_protocol, event_type)`.
  FSM 或边界: `unresolved -> supported_exact -> supported_lossy -> unsupported -> drift_detected`; every unknown runtime event must move the cell to `drift_detected` until reviewed.

信号: Operator sees pair/event heatmap, lossy conversion count, unknown-event count, and route policy version. Customer may receive `X-HUAKAI-Protocol-Loss` when enabled.

对应 F-* IDs: F-PROTO-002, F-GW-POLICY-001, new F-RPX-PROTO-PAIR-001.

Effort: 18h

**R1.2 Lifecycle and terminal stream events adapter [P0] [类型: passthrough]**

基线-开源: Commercial-Pool-Ref and released HUAKAI streaming spec require terminal-marker awareness. Clean-Arch-Ref confirms CLI-shaped clients need stable session continuity, but the first pass did not fully verify executor event lifetime behavior.

基线-官方: Vendor-X1/Vendor-X2 official stream surfaces use lifecycle events such as start, item/block open, stop, completed, failed, and terminal markers. Vendor-X3/Vendor-X4 may return buffered results that HUAKAI must wrap into synthetic stream lifecycle events.

HUAKAI 改动:
  算法: 1. Convert every upstream lifecycle event into HCSF lifecycle event. 2. Maintain exactly one active response root. 3. Permit nested block/item state only when the matrix cell says the pair supports it. 4. On upstream EOF without terminal, synthesize `terminal_missing` and mark usage source `inferred` or `ambiguous`. 5. Reject double terminal as idempotent no-op with audit.
  数据结构: `stream_event_cursor(request_id, attempt_no, root_state, active_item_id, active_block_id, terminal_seen, synthetic_terminal_reason)`; index `(request_id, attempt_no)`.
  FSM 或边界: `INIT -> RESPONSE_STARTED -> ITEM_OPEN -> BLOCK_OPEN -> TERMINAL_SEEN -> FINALIZED`; boundary: at most one terminal; EOF without terminal is not graceful.

信号: `stream_end_class`, missing-terminal counter, synthetic-terminal reason in request detail.

对应 F-* IDs: F-PROTO-002, F-GW-002, new F-RPX-PROTO-LIFECYCLE-001.

Effort: 16h

**R1.3 Text, reasoning, and usage delta event adapter [P0] [类型: passthrough]**

基线-开源: Obs-Ref records detailed token/cost/body mapping dimensions, while Billing-Engine-Ref shows provider-specific token category normalization. Their weakness for HUAKAI is that body/log analytics are not the same as account-aware settlement.

基线-官方: Vendor-X1/Vendor-X2 expose text and reasoning deltas differently; Vendor-X3/Vendor-X4 quota and token accounting may arrive only in final usage or vendor metadata. Official strategy requires preserving token categories that affect price and rate limits.

HUAKAI 改动:
  算法: 1. Normalize `text_delta`, `reasoning_delta`, `usage_delta`, and `cache_usage_delta` into monotonic accumulators. 2. Use last-non-zero-wins only for fields marked cumulative; use additive merge only for fields marked incremental. 3. Detect conflicts between stream deltas and terminal usage. 4. Freeze source confidence per field. 5. Hand final accumulator to billing session.
  数据结构: `usage_accumulator(request_id, attempt_no, token_class, source_kind, value, merge_mode, confidence, last_event_seq)`; unique `(request_id, attempt_no, token_class, source_kind)`.
  FSM 或边界: `empty -> partial -> reported -> conflict -> sealed`; boundary: sealed accumulator cannot be changed after Tx2, only reconciled by a correction event.

信号: Operator sees usage source per token class, conflict count, and pricing snapshot link. Customer sees stable final usage, not raw upstream category names.

对应 F-* IDs: F-GW-002, F-BILL-001, F-BILL-TOKEN-NORM-001, new F-RPX-USAGE-DELTA-001.

Effort: 20h

**R1.4 Tool, media, cache, and signature event adapter [P1] [类型: passthrough]**

基线-开源: Retry-Policy-Ref and released protocol spec confirm lossy conversion must be explicit. Obs-Ref demonstrates prompt/body mapping and cache dimensions, but its broad observability platform is larger than HUAKAI's first reverse proxy scope.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X4 expose tool, media, cache, and signature-like events with different semantics. Vendor-Meta routing may hide provider choice unless HUAKAI records the selected upstream path locally.

HUAKAI 改动:
  算法: 1. Map tool-call start/delta/stop using stable HUAKAI call IDs. 2. Map media chunks by reference pointer, never inline large binary into audit logs. 3. Preserve prompt-cache read/write counters when official usage exposes them. 4. Drop signature deltas by default unless route policy allows carry-forward. 5. Mark unsupported event subtypes as `protocol_loss`, not silent ignore.
  数据结构: `canonical_event_parts(request_id, event_seq, part_kind, canonical_id, upstream_ref_hash, bytes_count, loss_flag)`; index `(request_id, canonical_id)`.
  FSM 或边界: `part_open -> delta_seen -> part_closed`; boundary: malformed tool ID rejects the event pair, not the whole tenant.

信号: Protocol-loss dashboard by feature, tool-call ID mismatch counter, cache-token savings field.

对应 F-* IDs: F-PROTO-002, F-CACHE-001, F-BILL-003, new F-RPX-PROTO-PARTS-001.

Effort: 18h

**R1.5 Unknown, error, and empty-response event adapter [P0] [类型: 创新]**

基线-开源: Retry-Policy-Ref wraps local gateway exceptions distinctly and stops fallback on gateway-local errors. Legacy-Ref shows retry usefulness, but also negative examples around unsafe panic/body logging. Commercial-Pool-Ref requires account state to react to upstream failures.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 error events and empty responses differ, but official strategy requires stable status, request id, and retry semantics where provided.

HUAKAI 改动:
  算法: 1. For unknown event type, preserve raw type hash, skip body by default, add telemetry. 2. For upstream error event, call ErrorClassifier before protocol adapter finalization. 3. For empty success response, synthesize empty canonical message only when route policy allows. 4. For local adapter exception, emit `gateway_local_error` and block provider fallback. 5. Attach retry advice only after error normalization.
  数据结构: `protocol_anomalies(request_id, attempt_no, anomaly_class, upstream_type_hash, adapter_version, action, created_at)`; index `(anomaly_class, created_at)`.
  FSM 或边界: `observed -> classified -> action_selected -> audited`; boundary: local adapter exceptions never trigger cross-account fallback by default.

信号: Operator sees unknown event trend, adapter drift alerts, and explicit "fallback stopped because local gateway error" reason.

对应 F-* IDs: F-PROTO-002, F-UPSTREAM-FALLBACK-001, F-ACCAPI-ERR-CLASSIFY-001, new F-RPX-PROTO-ANOMALY-001.

Effort: 14h

## R2. WebSocket 全双工反代

**R2.1 WebSocket connection lifecycle bridge [P1] [类型: passthrough]**

基线-开源: Clean-Arch-Ref indicates HTTP and WebSocket executor variants and likely relay sessions, but the first pass flags WS lifecycle as needing deeper verification. Obs-Ref confirms attempt execution and wallet/billing links matter for long-lived calls.

基线-官方: Vendor-X1 has realtime WebSocket semantics; Vendor-Meta may route provider choices behind a vendor policy. Official strategy requires connection open, session update, bidirectional event flow, close code handling, and rate-limit/billing signals.

HUAKAI 改动:
  算法: 1. Authenticate client before upstream dial. 2. Reserve account slot and billing session before handshake. 3. Dial upstream with detached context and route-pinned transport pool. 4. Start two pumps: client-to-upstream and upstream-to-client. 5. Close both sides through a single coordinator that records close origin and code.
  数据结构: `ws_sessions(id, tenant_id, request_id, route_id, account_id, binding_id, state, client_close_code, upstream_close_code, opened_at, closed_at)`; indexes `(tenant_id, request_id)`, `(account_id, state)`.
  FSM 或边界: `RESERVED -> HANDSHAKING -> OPEN -> HALF_CLOSED_CLIENT | HALF_CLOSED_UPSTREAM -> CLOSING -> CLOSED`; boundary: handshake timeout default 10s, max session duration route-configured.

信号: Operator sees live WS sessions, close origin, duration, account slot pin, and route decision id.

对应 F-* IDs: F-RT-001, F-POOL-001, F-ACCAPI-ATTEMPT-001, new F-RPX-WS-LIFE-001.

Effort: 24h

**R2.2 WebSocket billing tap and usage accumulator [P1] [类型: 创新]**

基线-开源: Obs-Ref shows wallet escrow must finalize/cancel based on actual request outcome. Billing-Engine-Ref shows per-request billing sessions and idempotent settle/refund. Neither alone gives a HUAKAI WS tap integrated with account lease.

基线-官方: Vendor-X1 realtime usage may be event-driven; Vendor-X2/Vendor-X3/Vendor-X4 billing signals may arrive differently or not at every turn. Official strategy requires usage and cost to be explainable for long-lived sessions.

HUAKAI 改动:
  算法: 1. Register a tap in both pumps. 2. Classify each message as control, input, output, usage, error, or binary. 3. Accumulate per-turn usage where turn markers exist. 4. Use heartbeat snapshots for long sessions. 5. On close, settle by final usage; on ambiguous close, abort or partial-charge by route policy.
  数据结构: `ws_usage_turns(session_id, turn_no, input_tokens, output_tokens, audio_ms, bytes_in, bytes_out, usage_source, settled)`; unique `(session_id, turn_no)`.
  FSM 或边界: `turn_open -> usage_partial -> usage_reported -> turn_sealed`; boundary: no charge on zero accumulator plus abnormal close unless route policy explicitly allows inferred settlement.

信号: Customer sees one billing record per logical WS session or turn policy; Operator sees ambiguous usage, tap drops, and settlement reason.

对应 F-* IDs: F-RT-001, F-GW-002, F-BILL-SESSION-001, new F-RPX-WS-BILL-TAP-001.

Effort: 26h

**R2.3 WebSocket reconnect and resume coordinator [P2] [类型: 创新]**

基线-开源: Clean-Arch-Ref points to WS relay as a real product need, while Commercial-Pool-Ref emphasizes sticky routing and wait plans. The gap is safe resume across account cooldown or connection loss.

基线-官方: Vendor-X1 realtime clients expect reconnect behavior, but official guarantees vary by session state and event id. Vendor-Meta sticky routing shows provider order/fallback can be policy-driven.

HUAKAI 改动:
  算法: 1. Issue HMAC resume token bound to tenant, request id, route id, account alias, session state hash, and expiry. 2. On reconnect, validate token and attempt same account first. 3. If account is cooling down, expose context-loss risk before fallback. 4. Resume only from last acknowledged HUAKAI event seq. 5. Block replay of client messages not marked idempotent.
  数据结构: `ws_resume_tokens(token_hash, session_id, last_client_seq, last_upstream_seq, account_id, expires_at, consumed_at)`; index `(session_id, expires_at)`.
  FSM 或边界: `not_resumable -> token_issued -> resume_pending -> resumed | resume_denied | context_lost`; boundary: default expiry 60s; one token one resume.

信号: Customer receives structured reconnect denial or context-loss warning. Operator sees resume success rate and account-caused context loss.

对应 F-* IDs: F-RT-001, F-SESSION-001, F-ACCAPI-TRACE-001, new F-RPX-WS-RESUME-001.

Effort: 20h

**R2.4 Bidirectional message mirror with redaction [P1] [类型: 架构卫生]**

基线-开源: Obs-Ref stores request/response bodies with retention controls and exceptions for billing recovery, but HUAKAI must keep default prompt/body logging off. Retry-Policy-Ref exposes debug metadata, not raw bidirectional message bodies.

基线-官方: Vendor-X1 realtime and Vendor-X2/Vendor-X3 streaming interactions may include sensitive input, output, tool payloads, or binary frames. Official strategy requires preserving privacy boundaries and data policy choices.

HUAKAI 改动:
  算法: 1. Mirror only metadata by default: seq, direction, type, byte count, hash, timestamp. 2. Apply redaction classifier before optional body retention. 3. Store binary frame pointer only if retention policy permits. 4. Mark messages needed for billing recovery with a separate reason. 5. Enforce per-tenant retention TTL.
  数据结构: `ws_message_mirror(session_id, seq, direction, message_class, body_hash, bytes_count, retained_pointer, retention_reason, redaction_state)`; index `(session_id, seq)`.
  FSM 或边界: `metadata_only -> retained_pending -> retained -> redacted -> deleted`; boundary: default retained_pointer is NULL.

信号: Operator can inspect frame sequence and hashes without seeing secrets; retention exceptions are auditable.

对应 F-* IDs: F-RETENTION-001, F-OBS-QUERY-001, F-LOG-SAFE-001, new F-RPX-WS-MIRROR-001.

Effort: 16h

## R3. 流式 forwarder

**R3.1 Adaptive scanner buffer manager [P0] [类型: 创新]**

基线-开源: Released HUAKAI streaming spec already sets bounded scanner buffer and oversize terminal failure. Billing-Engine-Ref and Legacy-Ref show decompressed body risks; Retry-Policy-Ref normalizes response headers after forwarding. Weakness: static limits are either too tight for legitimate events or too loose for abuse.

基线-官方: Vendor-X1/Vendor-X2 stream chunks can vary by tool/media/reasoning event size. Vendor-X3/Vendor-X4 may return larger JSON objects. Official strategy requires not truncating protocol events.

HUAKAI 改动:
  算法: 1. Start with route default buffer. 2. Increase buffer only within tenant and route caps when previous chunks show legitimate growth. 3. Refuse single event above hard cap. 4. Record largest event per provider/model. 5. Feed anomaly rate into route tuning UI.
  数据结构: `stream_buffer_observations(route_id, provider_code, model_code, p50_bytes, p99_bytes, max_seen_bytes, overflow_count, window_start)`; index `(route_id, provider_code, model_code)`.
  FSM 或边界: `normal -> elevated -> hard_cap_hit -> blocked`; boundary: default 1 MiB, global max 64 MiB unless Owner approves higher.

信号: Operator sees recommended buffer cap, overflow counts, and abusive oversized event fingerprints.

对应 F-* IDs: F-GW-002, F-REQ-BODY-001, new F-RPX-STREAM-BUFFER-001.

Effort: 12h

**R3.2 Client disconnect drain controller [P0] [类型: 创新]**

基线-开源: Released HUAKAI spec requires bounded drain after client disconnect. Commercial-Pool-Ref confirms account slot release and usage settlement must not be best-effort. Obs-Ref confirms billing recovery can require body/usage exception handling.

基线-官方: Vendor-X1/Vendor-X2 streams can continue after downstream loss. Official strategy requires the upstream request not be confused with client cancellation unless HUAKAI intentionally aborts.

HUAKAI 改动:
  算法: 1. Detect downstream write failure. 2. Switch to no-write drain loop. 3. Continue parsing only usage and terminal events. 4. Stop on first exhausted budget among seconds, bytes, or estimated cost. 5. Finalize Tx2 with `drain_outcome`.
  数据结构: `stream_drain_records(request_id, attempt_no, started_at, ended_at, bytes_drained, estimated_cost, stop_reason, terminal_seen)`; unique `(request_id, attempt_no)`.
  FSM 或边界: `emitting -> client_gone -> draining -> drain_budget_hit | terminal_seen -> finalizing`; boundary: default 30s, 1 MiB, and local money cap per route.

信号: Customer sees disconnect locally; Operator sees drain stop reason and whether partial charge was applied.

对应 F-* IDs: F-GW-002, F-BILL-SESSION-001, F-ACCAPI-ATTEMPT-001, new F-RPX-STREAM-DRAIN-001.

Effort: 14h

**R3.3 SSE frame normalizer [P0] [类型: passthrough]**

基线-开源: Released F-GW-002 depends on per-event flush and SSE terminal classification. Retry-Policy-Ref strips unsafe transfer/content headers. Weakness across references: SSE normalization is often embedded in handler code and becomes hard to test per provider pair.

基线-官方: Vendor-X1/Vendor-X2 official SSE events may include comments, retry hints, event names, data-only frames, or multi-line data fields. Official strategy requires preserving event order and terminal semantics.

HUAKAI 改动:
  算法: 1. Parse raw bytes into SSE frame fields. 2. Normalize multi-line `data` before JSON parsing. 3. Preserve comment/retry metadata only if client protocol accepts it. 4. Emit canonical event sequence numbers. 5. Flush each client frame immediately.
  数据结构: `sse_frame_log(request_id, seq, event_name_hash, data_bytes, normalized_ok, terminal_candidate, flush_at)` sampled by route policy; index `(request_id, seq)`.
  FSM 或边界: `await_line -> frame_open -> data_collect -> frame_complete -> emitted`; boundary: frame max follows R3.1 event cap.

信号: Operator sees malformed SSE rate and first-token flush latency.

对应 F-* IDs: F-GW-002, F-PROTO-002, new F-RPX-SSE-NORMALIZE-001.

Effort: 12h

**R3.4 JSON-stream and binary stream forwarder [P1] [类型: passthrough]**

基线-开源: Retry-Policy-Ref builds bodies differently for multipart, raw streams, proxy audio, JSON, and no-body methods. Obs-Ref shows binary and asset pointers belong in retention policy, not raw logs.

基线-官方: Vendor-X1 realtime/audio surfaces and Vendor-X3/Vendor-X4 binary or JSON stream variants may not be SSE. Official strategy requires preserving byte order and not corrupting content encodings.

HUAKAI 改动:
  算法: 1. Detect stream mode from response headers and selected adapter. 2. For newline JSON, parse complete JSON object boundaries without assuming SSE. 3. For binary, mirror metadata and pass bytes without JSON parsing. 4. For multipart, enforce part-size caps. 5. Disable protocol translation unless adapter declares the mode.
  数据结构: `stream_mode_decisions(request_id, mode, content_type, adapter_id, parser_id, binary_passthrough, part_cap_bytes)`; index `(mode, adapter_id)`.
  FSM 或边界: `detecting -> json_stream | binary_passthrough | multipart -> finalizing`; boundary: binary mode never enters JSONPath rewrite.

信号: Operator sees stream mode distribution and parser mismatch alerts.

对应 F-* IDs: F-GW-002, F-RETENTION-001, F-REQ-BODY-002, new F-RPX-STREAM-MODE-001.

Effort: 18h

## R4. 上游 transport pool

**R4.1 Transport capacity caps and leasing [P0] [类型: 架构卫生]**

基线-开源: Commercial-Pool-Ref and released F-POOL-001 require concurrency slots, wait plans, and account slot release. Retry-Policy-Ref has timeout/retry budgets, but not HUAKAI's account-lease coupling.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 rate and connection behavior can penalize bursty shared accounts. Official strategy requires operators to respect rate/capacity boundaries per account and project/workspace.

HUAKAI 改动:
  算法: 1. Compute transport key from tenant, account, upstream host, proxy, TLS profile, and isolation mode. 2. Acquire total cap, per-host cap, and idle/active stream cap in deterministic order. 3. Attach acquisition token to request attempt. 4. Release idempotently in Tx2 or orphan sweep. 5. Expose wait plan when cap is exhausted.
  数据结构: `transport_pools(pool_key, tenant_id, account_id, host_hash, proxy_id, tls_profile_id, max_idle, max_per_host, max_total, active_count, idle_count, waiting_count)`; index `(tenant_id, account_id, host_hash)`.
  FSM 或边界: `idle -> leased -> stream_pinned -> releasing -> idle | quarantined`; boundary: active streams are not evicted by idle cleanup.

信号: Operator sees active/idle/waiting counts per account and cap-exhaustion reason.

对应 F-* IDs: F-POOL-001, F-GW-004, F-ACCAPI-ATTEMPT-001, new F-RPX-TRANSPORT-CAP-001.

Effort: 22h

**R4.2 Dial-time DNS and upstream host safety [P0] [类型: 创新]**

基线-开源: Commercial-Pool-Ref and Retry-Policy-Ref both show SSRF-safe endpoint validation and dial-time DNS rebinding protection. Weakness: these controls often live in monitor/custom-host paths only, while HUAKAI needs them on every configurable upstream path.

基线-官方: Vendor-X3/Vendor-X4 official deployments may involve regional endpoints or private service routing, while Vendor-X1/Vendor-X2 public endpoints remain HTTPS-origin constrained. Official strategy requires not turning custom upstream config into SSRF.

HUAKAI 改动:
  算法: 1. Validate configured origin at save time. 2. Re-resolve DNS at dial time. 3. Reject private, loopback, metadata, link-local, special-use, encoded, credential-bearing, or homograph hosts unless explicitly marked as trusted private deployment. 4. Validate final connected IP. 5. Redact rejected IP in customer-facing errors.
  数据结构: `upstream_endpoint_checks(endpoint_id, host_hash, last_dns_hash, safety_class, last_checked_at, rejection_reason)`; index `(safety_class, last_checked_at)`.
  FSM 或边界: `unchecked -> config_valid -> dial_validating -> allowed | rejected | private_allowed`; boundary: private_allowed requires Owner-approved deployment profile.

信号: Operator sees endpoint safety state and rebinding rejection count; customer sees sanitized 502/403-style error.

对应 F-* IDs: F-REQ-CUSTOM-HOST-001, F-SEC-001, F-GW-004, new F-RPX-DIAL-SAFE-001.

Effort: 18h

**R4.3 Pool isolation modes [P1] [类型: 创新]**

基线-开源: Clean-Arch-Ref exposes per-entry proxy bypass and high-concurrency mode. Commercial-Pool-Ref emphasizes account scheduling compatibility. Weakness: neither should force HUAKAI into one global connection reuse policy.

基线-官方: Vendor-X1/Vendor-X2 account surfaces can be sensitive to shared transport fingerprints. Vendor-X3/Vendor-X4 project/service-account boundaries may require stronger isolation. Vendor-Meta provider routing may need separate transport policy from direct upstream.

HUAKAI 改动:
  算法: 1. Let route/account select isolation mode: `shared_host`, `per_account`, `per_credential_version`, `per_proxy`, `single_use`. 2. Include mode in transport key. 3. Block downgrade while streams active. 4. Rotate idle pools when credential version changes. 5. Emit policy diff audit.
  数据结构: `transport_isolation_policies(scope_kind, scope_id, mode, allow_downgrade, rotate_on_credential_version, updated_by)`; unique `(scope_kind, scope_id)`.
  FSM 或边界: `shared -> isolated_pending -> isolated_active -> rotate_pending -> retired`; boundary: no pooling across tenant ids under any mode.

信号: Operator sees why a request used a fresh connection, reused pool, or bypassed proxy.

对应 F-* IDs: F-POOL-001, F-ACCAPI-LEASE-001, F-RESP-META-001, new F-RPX-TRANSPORT-ISOLATION-001.

Effort: 16h

## R5. TLS 指纹 / impersonation

**R5.1 TLS profile registry and policy gate [P1] [类型: 架构卫生]**

基线-开源: Commercial-Pool-Ref flags TLS/browser-fingerprint logic as not fully audited; Clean-Arch-Ref surfaces per-provider executors and proxy controls. This is evidence of product need, not permission to mimic distinctive implementations.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 require TLS and ALPN compatibility. Official strategy does not require unsafe impersonation by default. Any provider-sensitive profile must be operator-gated.

HUAKAI 改动:
  算法: 1. Define named TLS profiles as local config records. 2. Separate compatibility profiles from impersonation profiles. 3. Validate cipher suites, curves, ALPN list, extension order policy, and minimum TLS version. 4. Require legal/ToS risk acknowledgement for impersonation profile activation. 5. Bind profile id into transport key and request attempt.
  数据结构: `tls_profiles(id, name, class, min_version, cipher_suites, curves, alpn, extension_order_policy, grease_mode, enabled, risk_ack_id)`; index `(class, enabled)`.
  FSM 或边界: `draft -> compatibility_enabled -> impersonation_pending_ack -> impersonation_enabled -> disabled`; boundary: default is compatibility profile only.

信号: Operator sees TLS profile used per account and risk-gated status; customer never sees raw TLS details.

对应 F-* IDs: F-AUTH-005, F-POOL-001, new F-RPX-TLS-PROFILE-001.

Effort: 20h

**R5.2 Fingerprint drift and GREASE controller [P2] [类型: 创新]**

基线-开源: No required input provides a full audited safe implementation of TLS impersonation. Therefore HUAKAI should implement a smaller controller that verifies its own configured profile behavior and records drift, not copy any upstream profile.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 official endpoints may change TLS acceptance. GREASE behavior must remain standards-compatible and not create brittle account-level fingerprints.

HUAKAI 改动:
  算法: 1. Compute observed handshake fingerprint hash for each profile/host. 2. Compare against configured expected compatibility class. 3. Rotate GREASE values only when profile class permits. 4. Freeze profile on repeated auth/rate anomalies. 5. Emit drift review task instead of auto-escalating impersonation.
  数据结构: `tls_fingerprint_observations(profile_id, host_hash, ja_like_hash, alpn_selected, grease_seen, anomaly_count, observed_at)`; index `(profile_id, host_hash, observed_at DESC)`.
  FSM 或边界: `stable -> drift_suspected -> frozen -> reviewed -> stable | disabled`; boundary: drift does not change profile automatically.

信号: Operator sees profile drift, freeze reason, and impacted accounts.

对应 F-* IDs: F-AUTH-005, F-ACC-AUTODISABLE-001, new F-RPX-TLS-DRIFT-001.

Effort: 14h

## R6. 错误归一化

**R6.1 HUAKAI 12-class standard error taxonomy [P0] [类型: 架构卫生]**

基线-开源: Commercial-Pool-Ref, Billing-Engine-Ref, Obs-Ref, Retry-Policy-Ref, and Legacy-Ref all show different failure categories: auth, quota, rate limit, timeout, provider error, local gateway failure, and billing recovery. Weakness: substring/status-only matching is not stable enough.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4/Vendor-Meta error shapes and retry headers differ. Official strategy requires honoring retry-after/reset signals where present and not conflating local gateway failure with upstream failure.

HUAKAI 改动:
  算法: 1. Parse status, headers, and safe body fields. 2. Map to one of 12 classes. 3. Attach retryability and transition advice. 4. Preserve original status in attempt row. 5. Return sanitized HUAKAI envelope to client.
  数据结构: `error_taxonomy(class_code, http_default, retryable_default, account_transition_default, customer_visibility, operator_severity)`.
  FSM 或边界: Classes: `local_validate`, `local_timeout`, `local_transport`, `upstream_auth`, `upstream_quota`, `upstream_rate_limit`, `upstream_overload`, `upstream_invalid_request`, `upstream_policy_denied`, `upstream_5xx`, `upstream_empty_or_malformed`, `unknown`.

信号: Customer gets stable error code. Operator sees class, raw status, retry-after, transition advice, and parser version.

对应 F-* IDs: F-ACCAPI-ERR-CLASSIFY-001, F-RATE-001, F-UPSTREAM-RETRY-002, new F-RPX-ERR-TAXONOMY-001.

Effort: 18h

**R6.2 Vendor-shape parser and retry policy matrix [P0] [类型: passthrough]**

基线-开源: Retry-Policy-Ref demonstrates bounded retry budgets and retry-after parsing. Commercial-Pool-Ref shows scheduler/account refresh events. Legacy-Ref confirms retry on selected status classes but also shows why specific-channel/no-retry rules matter.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4/Vendor-Meta expose different status/body/header reset signals. Official strategy requires respecting vendor-specific throttling and auth semantics.

HUAKAI 改动:
  算法: 1. Resolve parser by vendor code and protocol family. 2. Parse status, body shape hash, retry-after/reset headers, and safe error code. 3. Score confidence: exact, header-only, body-only, fallback. 4. Apply route retry matrix. 5. Cap retry delay by max elapsed budget and account cooldown policy.
  数据结构: `vendor_error_parsers(vendor_code, protocol_family, parser_version, status_set, body_shape_hash, header_names_hash, output_class)`; `retry_policy_matrix(route_id, class_code, max_attempts, max_elapsed_ms, honor_retry_after, fallback_allowed)`.
  FSM 或边界: `parse_exact -> parse_partial -> fallback_class -> retry_decided -> cooldown_written`; boundary: provider retry-after above route cap becomes skip reason, not ignored sleep.

信号: Operator sees parser confidence and retry skip reason per attempt.

对应 F-* IDs: F-UPSTREAM-RETRY-002, F-UPSTREAM-FALLBACK-001, F-RATE-001, new F-RPX-ERR-PARSER-001.

Effort: 22h

**R6.3 Account state transition emitter [P0] [类型: 创新]**

基线-开源: Commercial-Pool-Ref and Legacy-Ref both require auto-disable/cooldown/health events; account audit says HUAKAI currently has fragmented health, credential, quota, enabled, and expiry axes.

基线-官方: Vendor-X1/Vendor-X2 spend/rate-limit surfaces, Vendor-X3 quota modes, Vendor-X4 IAM errors, and Vendor-Meta credit/provider fallback all imply different recovery actions.

HUAKAI 改动:
  算法: 1. Take normalized error class plus parser confidence. 2. Compute transition with precedence: disabled > expired > needs_manual_recovery > needs_refresh > quota_exhausted > cooling_down > degraded > normal. 3. Write transition outbox in same attempt transaction. 4. Scheduler consumes outbox and refreshes snapshot. 5. Manual override can suppress auto-clear.
  数据结构: `account_state_events(id, tenant_id, account_id, request_id, class_code, from_state, to_state, confidence, retry_after_ms, outbox_status)`; indexes `(account_id, created_at DESC)`, `(outbox_status, created_at)`.
  FSM 或边界: `normal -> cooling_down | needs_refresh | quota_exhausted | degraded | needs_manual_recovery | disabled`; boundary: low-confidence parser cannot disable, only degrade/cooldown with review flag.

信号: Operator sees state reason, originating request, auto-clear time, and whether scheduler consumed it.

对应 F-* IDs: F-ACCAPI-STATE-001, F-ACC-SCHED-005, F-ACC-AUTODISABLE-001, new F-RPX-ERR-STATE-001.

Effort: 18h

## R7. 请求 body 改写

**R7.1 JSONPath mutation compiler [P1] [类型: 创新]**

基线-开源: Retry-Policy-Ref supports override params and hooks; Billing-Engine-Ref has route affinity and override templates. Declarative-Ref in the codename map confirms body mutation is a relevant gateway capability. Weakness: arbitrary mutators can become unreviewable.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 request bodies have different allowed fields. Official strategy requires rejecting unsupported fields rather than silently changing semantics.

HUAKAI 改动:
  算法: 1. Compile route mutation rules into ordered ops: `set`, `remove`, `copy`, `conditional_set`, `deny_if_present`. 2. Validate JSONPath against allowed schema for client/upstream pair. 3. Apply ops to canonical request, not raw provider body. 4. Re-run protocol capability matrix after mutation. 5. Emit mutation summary.
  数据结构: `body_rewrite_rules(route_id, pair_id, priority, op, json_path, value_expr, condition_expr, strict, enabled)`; index `(route_id, pair_id, priority)`.
  FSM 或边界: `compiled -> applicable -> mutated -> validated -> emitted | rejected`; boundary: strict mode fails closed on missing path, type mismatch, or multi-match ambiguity.

信号: Operator sees mutation diff summary and reject reason; customer sees structured invalid-request code.

对应 F-* IDs: F-GW-POLICY-001, F-PROTO-002, F-ROUTE-OVERRIDE-001, new F-RPX-BODY-REWRITE-001.

Effort: 18h

**R7.2 Body rewrite audit and strict-mode safety [P1] [类型: 架构卫生]**

基线-开源: Obs-Ref warns that body retention must be explicit and default-off. Retry-Policy-Ref hook/mutator logs can be useful, but HUAKAI should avoid logging prompts or credentials by default.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 data policy expectations require clear handling of bodies, especially when requests are transformed for compatibility.

HUAKAI 改动:
  算法: 1. Store only rule ids, field paths, op names, before/after type hashes, and redaction state. 2. Never store raw values unless retention policy permits. 3. Mark mutation as billing-relevant if it changes model, max tokens, cache, tool, or response format fields. 4. Require operator reason for enabling non-strict rules. 5. Attach audit id to usage record.
  数据结构: `body_rewrite_audit(request_id, rule_id, op, path_hash, before_type, after_type, billing_relevant, redaction_state, decision)`; index `(request_id)`, `(rule_id, created_at DESC)`.
  FSM 或边界: `audit_planned -> emitted -> retained_metadata -> purged`; boundary: raw value retention default false.

信号: Operator can prove what changed without exposing user prompt content.

对应 F-* IDs: F-RETENTION-001, F-OBS-QUERY-001, F-LOG-SAFE-001, new F-RPX-BODY-AUDIT-001.

Effort: 10h

## R8. Header/Cookie 防火墙

**R8.1 Bidirectional header and cookie allowlist [P0] [类型: 架构卫生]**

基线-开源: Retry-Policy-Ref forwards only allowed headers and strips internal headers. Clean-Arch-Ref defaults passthrough response headers off. Legacy-Ref and Commercial-Pool-Ref show auth/account state risk if upstream details leak.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 use authorization, project, organization, cookie, request-id, and rate-limit headers differently. Official strategy requires preserving necessary request metadata while not leaking credentials or account identity.

HUAKAI 改动:
  算法: 1. Split policy by direction: client-to-upstream, upstream-to-client, internal-to-log. 2. Apply denylist first for credentials/cookies/internal/billing/admin headers. 3. Apply allowlist by route and provider. 4. Recompute content-length after body changes; strip unsafe transfer/content encoding. 5. Tag every forwarded sensitive-adjacent header.
  数据结构: `header_firewall_rules(scope_kind, scope_id, direction, header_name_norm, action, sensitivity_class, reason, enabled)`; index `(scope_kind, scope_id, direction)`.
  FSM 或边界: `received -> denied | allowed | redacted -> forwarded | logged_metadata`; boundary: `Authorization`, `Cookie`, credential-like, and HUAKAI internal headers default deny unless injector explicitly owns them.

信号: Operator sees "currently exposing N headers" diagnostic and per-request stripped/forwarded counts.

对应 F-* IDs: F-SEC-005, F-ACCAPI-CRED-INJECT-001, F-RESP-META-001, new F-RPX-HEADER-FW-001.

Effort: 16h

**R8.2 Sensitive information detector and diagnostic replay [P0] [类型: 创新]**

基线-开源: Commercial-Pool-Ref requires leakage-safe logging, Obs-Ref emphasizes redaction state, and Retry-Policy-Ref response metadata helps support work. Weakness: static header lists miss new vendor/private headers.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 may introduce new request ids, project ids, rate-limit headers, or auth-adjacent cookies. Official strategy requires diagnostics that help operators without exposing secrets.

HUAKAI 改动:
  算法: 1. Run detector over header names and sampled safe values. 2. Classify as credential, cookie, account-id, rate-limit, tracing, content, or unknown-sensitive. 3. Block credential/cookie by default. 4. For diagnostics, replay policy decision from stored metadata, not raw request. 5. Emit drift task when unknown-sensitive appears repeatedly.
  数据结构: `header_decision_audit(request_id, direction, header_name_hash, action, sensitivity_class, rule_id, detector_version)`; `header_drift_tasks(class, sample_hash, count, status)`.
  FSM 或边界: `classified -> allowed | blocked | redacted | drift_review`; boundary: no raw secret values in audit.

信号: Operator can debug missing/blocked headers and sees drift review queue.

对应 F-* IDs: F-SEC-005, F-LOG-SAFE-001, F-OBS-QUERY-001, new F-RPX-HEADER-DIAG-001.

Effort: 12h

## R9. 解压保护

**R9.1 Guarded gzip/br/plain request decompressor [P0] [类型: 创新]**

基线-开源: Billing-Engine-Ref explicitly guards gzip/br/plain bodies with post-decompression max bytes and reusable storage. Legacy-Ref confirms gzip support is useful but is a negative example when decompressed-size cap is missing.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 accept different payload sizes and encodings depending on endpoint. Official strategy requires the gateway to protect itself before upstream dispatch.

HUAKAI 改动:
  算法: 1. Detect `Content-Encoding`. 2. Wrap gzip/br/plain readers with post-decompression byte cap. 3. Track compressed and decompressed byte counters. 4. Abort when max bytes or max ratio trips. 5. Return stable 413-style error and do not log body.
  数据结构: `body_decode_observations(request_id, encoding, compressed_bytes, decompressed_bytes, ratio, stop_reason, storage_tier)`; index `(encoding, stop_reason, created_at)`.
  FSM 或边界: `plain | gzip_wrapped | br_wrapped -> reading -> limit_hit | complete -> cleanup`; boundary: reject unsupported encoding; default decompressed max from route/tenant cap.

信号: Customer receives stable too-large error. Operator sees zip-bomb ratio and encoding distribution.

对应 F-* IDs: F-REQ-BODY-001, F-LOG-SAFE-001, new F-RPX-DECOMP-GUARD-001.

Effort: 14h

**R9.2 Streaming decompression and cleanup ownership [P0] [类型: 架构卫生]**

基线-开源: Billing-Engine-Ref uses a body storage abstraction and cleanup middleware. Obs-Ref distinguishes in-memory vs remote body buffering. Weakness: cleanup often depends on handler success path unless ownership is explicit.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 request and media payloads may be large. Official strategy requires the gateway to close readers, remove temp files, and avoid retrying unreplayable streams.

HUAKAI 改动:
  算法: 1. Assign body owner at ingress. 2. Stream decompressed bytes into memory or disk tier while enforcing cap. 3. Expose seekable body only if storage tier sealed successfully. 4. Register cleanup defer and orphan cleanup task. 5. Mark body as non-replayable if stream ended before seal.
  数据结构: `request_body_storage(request_id, owner, tier, sealed, bytes_count, temp_path_hash, expires_at, cleanup_state)`; indexes `(cleanup_state, expires_at)`.
  FSM 或边界: `owned -> streaming -> sealed -> released -> cleaned`; boundary: unsealed body cannot be used for retry replay.

信号: Operator sees cleanup backlog and unreplayable-body retry skips.

对应 F-* IDs: F-REQ-BODY-002, F-UPSTREAM-RETRY-002, F-USAGE-CLEAN-001, new F-RPX-BODY-STORE-001.

Effort: 16h

## R10. 响应 body cache / retry 重放

**R10.1 Response/request replay store memory tier [P0] [类型: 架构卫生]**

基线-开源: Billing-Engine-Ref supports reusable body storage and disk spill; Retry-Policy-Ref retries with request reconstruction; released F-GW-002 blocks unsafe mid-stream failover by default.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 official APIs may charge per attempt. Official strategy requires retries to avoid duplicating non-idempotent side effects when a body cannot be replayed exactly.

HUAKAI 改动:
  算法: 1. For eligible non-stream or pre-content-failure requests, seal request body into memory tier under size cap. 2. Store normalized headers after firewall policy. 3. Hash body and header replay shape. 4. On retry, rebuild request from sealed snapshot. 5. Refuse replay if body mutated after seal.
  数据结构: `replay_snapshots(request_id, attempt_no, tier, body_hash, header_shape_hash, bytes_count, sealed_at, replayable, deny_reason)`; index `(request_id, attempt_no)`.
  FSM 或边界: `buffering -> sealed -> replayed -> retired`; boundary: memory tier default cap small enough to prevent tenant memory abuse.

信号: Operator sees retry blocked because body not replayable vs retry skipped by policy.

对应 F-* IDs: F-UPSTREAM-RETRY-002, F-GW-004, F-REQ-BODY-002, new F-RPX-REPLAY-MEM-001.

Effort: 16h

**R10.2 Disk tier, TTL, and cleanup [P1] [类型: 创新]**

基线-开源: Billing-Engine-Ref has disk spill and old-file cleanup. Obs-Ref shows body retention must have TTL and redaction semantics. Weakness: disk replay cache can become hidden prompt retention if not tied to privacy policy.

基线-官方: Vendor-X1/Vendor-X2/Vendor-X3/Vendor-X4 data policies and customer privacy expectations require short-lived replay buffers separate from observability body retention.

HUAKAI 改动:
  算法: 1. Spill sealed snapshot to disk only above memory threshold and below disk cap. 2. Encrypt or permission-restrict temp files according to deployment mode. 3. Set short TTL for retry replay separate from body-retention TTL. 4. Cleanup on request finish and by periodic sweeper. 5. Emit alert on orphaned disk bytes.
  数据结构: `replay_disk_objects(object_id, tenant_id, request_id, path_hash, bytes_count, expires_at, cleanup_state, privacy_class)`; indexes `(cleanup_state, expires_at)`, `(tenant_id, expires_at)`.
  FSM 或边界: `created -> sealed -> eligible -> expired -> deleted`; boundary: replay cache TTL default minutes, not months.

信号: Operator sees replay disk usage, orphan cleanup count, and privacy class.

对应 F-* IDs: F-REQ-BODY-002, F-RETENTION-001, F-USAGE-CLEAN-001, new F-RPX-REPLAY-DISK-001.

Effort: 14h

**R10.3 Response body cache and idempotent retry replay policy [P1] [类型: 创新]**

基线-开源: Retry-Policy-Ref exposes cache status and avoids caching streaming requests. Obs-Ref has cache metrics and saved token/latency dimensions. The gap is a money-safe distinction between response cache and retry replay cache.

基线-官方: Vendor-X1/Vendor-X2 prompt/cache billing and Vendor-Meta provider fallback policies can affect charges and provider choice. Official strategy requires cache hits, retries, and fallbacks to be explainable and auditable.

HUAKAI 改动:
  算法: 1. Separate `retry_replay` snapshots from `response_cache` entries. 2. Require route cache policy, tenant scope, model, protocol pair, body hash, header shape, and billing policy in cache key. 3. For retry, replay only before first content event unless idempotent-stream replay header/policy exists. 4. For response cache, settle according to explicit cache charge policy. 5. Invalidate by route/model/tenant and TTL.
  数据结构: `response_cache_entries(cache_key, tenant_id, route_id, model_code, protocol_pair, body_hash, status, usage_snapshot, billing_policy, expires_at)`; index `(tenant_id, route_id, expires_at)`.
  FSM 或边界: `miss -> storing -> hit -> refresh -> expired -> purged`; boundary: streaming response cache default disabled.

信号: Customer sees cache status only when policy allows. Operator sees cache hit/miss/refresh, replay vs cache distinction, and charge policy.

对应 F-* IDs: F-CACHE-001, F-CACHE-002, F-BILL-003, F-UPSTREAM-RETRY-002, new F-RPX-RESP-CACHE-001.

Effort: 22h

## Priority Rollup

| Priority | Modules | Why |
| --- | --- | --- |
| P0 | R1.1, R1.2, R1.3, R1.5, R3.1, R3.2, R3.3, R4.1, R4.2, R6.1, R6.2, R6.3, R8.1, R8.2, R9.1, R9.2, R10.1 | Minimum safe real-upstream reverse proxy. These prevent silent protocol loss, unsafe retries, unbounded streams/bodies, SSRF, credential/header leakage, and opaque account state transitions. |
| P1 | R1.4, R2.1, R2.2, R2.4, R3.4, R4.3, R5.1, R7.1, R7.2, R10.2, R10.3 | Commercial reliability and operator usability. These make WS, mutation, transport isolation, response cache, and TLS compatibility manageable without turning them into hidden billing/security risks. |
| P2 | R2.3, R5.2 | Advanced recovery and fingerprint drift hardening. Needed before broad WS/realtime and provider-sensitive account pooling, but not before basic safe HTTP/SSE proxy. |
| P3 | None in this pass | The requested reverse proxy core still has enough P0/P1/P2 work that no module should be demoted to cosmetic backlog. |

## Open Questions

1. TLS impersonation policy: should HUAKAI allow any impersonation-class profile before a legal/ToS review, or keep R5 to compatibility-only for L1/L2?
2. WebSocket billing granularity: should billing settle per WS session, per turn, or by time window for Vendor-X1 realtime-style sessions?
3. Response cache charge policy: should cache hits be free, discounted, or charged by a local cache price? This must align with F-BILL-001/F-BILL-003 before R10.3 implementation.
4. Private upstream endpoints: should Personal Edition allow trusted private network upstreams, or should private routing be SaaS/deployment-profile only?
5. Body mutation authority: should customer-supplied per-request mutation ever be allowed, or should R7 be admin-route-policy only until abuse controls mature?
6. Protocol pair scope for first implementation: minimum viable set should likely be Vendor-X1-compatible client to Vendor-X1/Vendor-X2 upstream plus Vendor-X2-compatible client to Vendor-X1 upstream, but Owner should confirm provider breadth priority.
7. Disk replay storage: should replay disk tier require encryption-at-rest in Personal Edition, or rely on OS permissions until SaaS Edition?
8. Operator diagnostics exposure: which debug headers are safe for end customers by default, and which should be admin-log only?

## Owner summary

本清单把 HUAKAI 反向代理核心拆成 30 个可落地子模块，覆盖协议适配、WebSocket、流式 forwarder、transport pool、TLS profile、错误归一化、body/header 防火墙、解压保护、响应缓存和 retry replay。功能没有缩水，所有风险项都转成默认关闭、策略门控、审计或后续 Owner 决策。Clean-room 风险控制为只使用内部行为摘要和 HUAKAI released specs，不复制参考源码、结构、注释或 UI。需要 Owner 确认的重点是 TLS impersonation 是否允许、WS 计费粒度、response cache 计费策略、私网 upstream 允许范围、body mutation 权限和首批协议 pair 范围。
