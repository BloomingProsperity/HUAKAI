# 2026-05-28 WaveB-A Evidence Plan (Codex)

## Owner directive
`TASK: INDEPENDENT plan (PLAN ONLY) for HUAKAI fixes S1-025 + S2-163.`

## Scope
- S1-025: end-to-end persistence of protocol-loss JSON into `usage_records.protocol_loss` in all 3 Tx2 settle writes (Settle / Abort / CommitCacheHit).
- S2-163: wire tokencheck cross-check into settlement (`Settle` path) and persist verdict outcome to existing observability columns.

### Files in scope
- `backend/internal/gateway/upstream_dispatcher_hcsf.go` (protocol-loss source)
- `backend/internal/gatewayhttp/chat_completions_billing.go` (non-stream + async settle req assembly)
- `backend/internal/gateway/forwarder_types.go` (`UsageRecordDraft`)
- `backend/internal/billing/billing.go` (`SettleRequest`)
- `backend/internal/gatewayhttp/chat_completions_stream.go` (streaming settle req assembly)
- `backend/internal/gatewayhttp/chat_completions_handler_headers.go` (cache-hit and audit-ack settle reqs)
- `backend/internal/billing/settler.go` (`usageParams` in Settle / Abort / CommitCacheHit; conflict note with S1-015 on same region)
- `backend/internal/tokencheck/{crosscheck.go, crosscheck_wire.go(new), types.go, cache_verify.go}`
- `backend/internal/db/billing/billing_settle.sql.go` (field already present)
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/internal/billing/settler_integration_test.go`
- `backend/internal/tokencheck/crosscheck_test.go` and `estimator_test.go`

## Success criteria
1. `protocol_loss` in `usage_records` contains non-empty protocol-loss JSON when adapter/HCSF emits loss entries.
2. `usage_records.protocol_loss` is no longer hardcoded to `[]` in Settle / Abort / CommitCacheHit inserts.
3. `tokencheck` cross-check produces `Warn5` / `Fail20` verdicts and maps to persisted settlement metadata via existing columns.
4. No new files are added in frozen packages (`backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`).
5. Regression tests are discriminating with explicit mutation checks.

## Success metrics
- For S1-025: `protocol_loss` row assertion changes from fail to pass when input contains protocol-loss payload, and mutation (“replace with `[]`”) turns red.
- For S2-163: discrepancy 1000 vs 500 in mapped path emits `Fail20` metadata and survives settlement insert; mutation (“skip EvaluateDraft wiring”) keeps old metadata and test must fail.

## Time estimate
- Planning done; implementation estimate: 1 day total split into two small commits.

## Blast radius
- Money path Tx2 writes in `settler.go` and reconciliation visibility in `usage_records`.
- Schema-write semantics (no schema migration required for chosen mapping).
- JSON payload size and serialization impact for protocol-loss payloads on settlement inserts.

## Failure modes + mitigations
- Failure: `protocol_loss` not wired in one settlement path -> mitigated by explicit 3-path checklist in one commit.
- Failure: tokencheck wiring computes estimated/reported from same field -> mitigated by explicit source contract and unit tests for divergent values.
- Failure: frozen-package rule violation -> mitigated by reusing existing files in frozen packages and placing new file in `backend/internal/tokencheck/`.

## Decision points
1. S2 mapping strategy (required decision):
   - **推荐:** reuse existing columns `usage_source`, `confidence_score`, `pending_reconciliation` (no migration `0061`).
   - 理由: column set already present in migration `0002` with compatible enums and semantics for ambiguity reconciliation.
   - 风险: 弱差异可读性 (无法独立存储 ok/warn/fail)。缓解: 将 `confidence_score` 持久化为 drift 绝对比例并保留告警阈值上下文。

## Execution order
1. `S1-025` first (modifies settlement payload structs and Tx2 param surfaces).
2. `S2-163` second (add tokencheck wire + mapping using settled payload fields now stabilized by S1).
3. Resolve S1-015 conflict by sequencing merge/rebase with its chunk touching `backend/internal/billing/settler.go:152-162` before/after, avoiding overlap on same hunk.

---

## Finding #1: S1-025 protocol-loss persistence

### Scope
- Root cause: `ProtocolLoss` is accumulated in gateway adapters (`...upstream_dispatcher_hcsf.go:150-154`, `...chat_completions_billing.go:511-520`, `...anthropic/sse.go:368-379`) but hardcoded away in settlement inserts (`settler.go` Settle/Abort/CommitCacheHit: `ProtocolLoss: []byte("[]")`).

### Fix design
1. Add a protocol-loss carrier to gateway draft and settlement request.
2. Update `backend/internal/gateway/forwarder_types.go` in `UsageRecordDraft` with a JSON-friendly field for protocol-loss payload (non-breaking, additive).
3. Update `backend/internal/billing/billing.go` `SettleRequest` with an additive protocol-loss field (same bytes shape or normalized JSON bytes).
4. Populate carry-forward in gateway settle constructors (non-test files):
   - `backend/internal/gatewayhttp/chat_completions_billing.go:119-137` (`nonStreamingSettleRequest`)
   - `backend/internal/gateway/http/chat_completions_stream.go:527-567` (`streamingCompletionEvent`, pass through draft protocol-loss)
   - `backend/internal/gatewayhttp/chat_completions_handler_headers.go:213-225` (cache-hit `cacheHitReq`)
   - `backend/internal/gatewayhttp/chat_completions_handler_headers.go:275-291` (cache-hit normal `settleReq`)
   - Source is already available in `env.CapabilityGraph.ProtocolLoss` in non-stream and cache-hit paths where `bufferedEnv` / `cachedEnv` exists.
5. In `backend/internal/billing/settler.go` replace all three hardcoded defaults with request-carried data:
   - `Settle` usageParams currently at `...:172`.
   - `Abort` usageParams currently at `...:366`.
   - `CommitCacheHit` usageParams currently at `...:517`.
   - Use `jsonOrEmptyArray(...)` so absent payload still defaults to `[]` cleanly.

### Packages and freeze check
- Modified packages:
  - `gatewayhttp` (existing files only, no new files)
  - `gateway`
  - `billing`
  - `internal/db`
- New file is not required in frozen packages.

### Discriminating test
- `backend/internal/billing/settler_integration_test.go`
  - New/updated test case: seed claim for Settle path, add 1+ protocol-loss entries to `SettleRequest.Draft.ProtocolLoss`, call `Settle`.
  - Assert: `SELECT protocol_loss` contains that entry; `protocol_loss != '[]'` and includes expected loss code.
  - Mutation: patch step changed to keep hardcoded `[]` should make assertion red (test must fail).

---

## Finding #2: S2-163 tokencheck dead code wiring

### Scope
- `backend/internal/tokencheck/crosscheck.go`, `types.go`, `crosscheck_test.go` provide cross-check primitives but settlement path does not call them.
- Need wire into `Settle` workflow before writing `usage_records` and persist outcome via existing columns.

### Fix design
1. Add `backend/internal/tokencheck/crosscheck_wire.go` (non-frozen package `tokencheck`).
   - Export `EvaluateDraft(reported, estimated int) (Verdict, float64)`.
   - Internally delegate to `CrossCheck` and return verdict + ratio.
2. In `backend/internal/billing/settler.go` within `Settle`:
   - Define clear reported/estimated values from settlement-local fields.
   - Call `tokencheck.EvaluateDraft` and capture verdict/ratio.
   - On `Warn5`/`Fail20`, set:
     - `usageSource = gateway.UsageSourceAmbiguous`.
     - `confidenceScore = ratio` (mapped through `numericFromFloat`).
     - `pendingReconciliation = true` (at least for `Fail20`, or both warn/fail if policy requires immediate attention).
   - Keep existing OK path unchanged to avoid false positives.
3. Do not add new migration column.
   - Persist verdict semantics via:
     - `usage_source` enum (`ambiguous`) for mismatch class,
     - `confidence_score` for quantitative drift,
     - `pending_reconciliation` as workflow flag.
4. Optional enhancement if stricter traceability is required later:
   - Encode drift tags as `ProtocolLoss` warning entries (same mechanism used by cache verify) and persist through S1 pipeline.

### Mapping precedence rule
- `Warn5`: mark `ambiguous + confidence + pending_reconciliation` per data quality policy.
- `Fail20`: same mapping and keep same stronger reconciliation state; if policy prefers stronger action, treat as stricter pending path.

### Discriminating tests
- `backend/internal/tokencheck/crosscheck_wire_test.go` (new): add explicit `EvaluateDraft(1000, 500)` case asserting `Fail20` and ratio `2.0`.
  - Mutation: return OK unconditionally should fail.
- `backend/internal/billing/settler_integration_test.go`
  - New/updated case that sets reported/estimated inputs to force `Fail20` in the settle path (via chosen local estimate input path).
  - Assert persisted row has `usage_source='ambiguous'`, `confidence_score` set to expected ratio, and `pending_reconciliation=true`.
  - Mutation: remove tokencheck hook in Settle path; test fails due unchanged `usage_source`/score.

### Packages and freeze check
- New file in `backend/internal/tokencheck` (non-frozen, allowed).
- No new files in `backend/internal/gateway`, `gatewayhttp`, or `proto`.

### Implementation caution
- Confirm local estimated token input source before coding: if current draft fields cannot express a stable estimated value distinct from upstream-reported value, define that source explicitly in one follow-up design step and keep deterministic behavior (e.g., estimator output or estimator-disabled path).
