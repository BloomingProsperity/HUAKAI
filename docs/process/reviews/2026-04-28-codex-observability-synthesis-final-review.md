# Codex Final Reviewer-Lane Report - F-OBS-001 Observability + Atomic Billing Synthesis

| Field | Value |
| --- | --- |
| Reviewer | Codex final reviewer-lane |
| Review date | 2026-04-28 |
| Artifact reviewed | `docs/decompositions/_cross-cutting/observability-synthesis.md` |
| Gate | CL-001..CL-011 strict path review for F-OBS-001 / atomic billing synthesis |
| Verdict | APPROVE-WITH-FIXES |
| Local Sub2API source | `.omc/reference-src/sub2api` at `b0a2252ed19c3720e6adafde6083e64fbac2efa9` |
| Local Helicone source | `.omc/reference-src/helicone` at `548832f8e763a33732ead27d8b2dcaeccc665a39` |

## Review Protocol Notes

- Pre-commitment prediction 1: the synthesis would correctly fix the earlier "Sub2API has no atomic billing" error, but would overstate the guarantee by looking only at `Apply`.
- Actual: confirmed. `Apply` is atomic, but the production handler submits the whole `RecordUsage` task through a lossy bounded worker pool. That outer queue is not accounted for.
- Pre-commitment prediction 2: at least one TODO would already be resolvable from local source.
- Actual: confirmed. The scheduler outbox consumer and typed errors are directly present in source; the synthesis leaves them open and also makes a false "no lag observability" claim.
- Pre-commitment prediction 3: license rows would need checking because Helicone license evidence moved from high-level README rows to source clone review.
- Actual: confirmed. The synthesis cites `E-LIC-009`, but the evidence ledger has Helicone at `E-LIC-007`.
- Pre-commitment prediction 4: HUAKAI-DESIGN labels would mostly be present but one design choice would conflict with domain docs.
- Actual: confirmed. `pending_reconciliation` currently says to update the Usage Record, contradicting the domain model's immutable Usage Record rule.
- Pre-commitment prediction 5: release-facing text would still contain upstream identifiers and schema names inherited from source-verification inputs.
- Actual: confirmed. Several Sub2API method names, table/column names, and error identifiers are still in implementer-relevant sections.
- Review mode: escalated to ADVERSARIAL after the worker-pool billing gap plus the outbox-lag false negative. I expanded source checks beyond the synthesis's stated files into handlers, worker pool, config, scheduler snapshot service, domain model, and the license ledger.
- Self-audit result: no low-confidence finding remains in the scored MAJOR section. Items that could be process choices rather than flaws are listed as minor or missing work.
- Realist check result: I kept the verdict at APPROVE-WITH-FIXES, not REJECT, because no implementation has shipped from this synthesis and the corrections are bounded text/spec fixes. I did not downgrade the worker-pool finding below MAJOR because it affects billing loss risk if copied into design assumptions.

## §1 - CL-001..011 Verdict Matrix

| Check | Verdict | One-line justification |
| --- | --- | --- |
| CL-001 | FAIL | Release-facing prose contains upstream method/function/config identifiers including `Apply`, `writeUsageLogBestEffort`, `applyUsageBillingEffects`, `deferredService.ScheduleLastUsedUpdate`, `cmd.BalanceCost`, and `BeginTx(ctx, nil)`. |
| CL-002 | FAIL | Release-facing prose carries upstream schema/table/column names such as `request_id`, `api_key_id`, `usage_logs`, `scheduler_outbox`, `usage_5h`, and `usage_cleanup_repo.go`; several proposed HUAKAI table names are also not yet anchored in domain docs. |
| CL-003 | PASS | No upstream UI component names, CSS class names, or dashboard layout identifiers from reference dashboards were found in the synthesis. |
| CL-004 | PASS | No upstream docs sentence longer than the allowed short technical phrases was found in the synthesis itself. |
| CL-005 | PARTIAL | The HUAKAI Tx1/Tx2 model is independent design, but the source-derived Apply path is summarized in implementation-shaped terms and misses the outer lossy worker-pool path. |
| CL-006 | FAIL | `observability-synthesis.md:10` cites Helicone as `E-LIC-009`; the ledger has Helicone at `E-LIC-007` and no `E-LIC-009` row was found. |
| CL-007 | PASS | `observability-synthesis.md:7` declares `Lane mode | Option C`, appropriate because Usage Record plus billing settlement is in the Option C carve-out. |
| CL-008 | PASS | `observability-synthesis.md:6` includes `F-OBS-001`, and `docs/03_FEATURE_PARITY_MATRIX.md:48` contains the F-OBS-001 row. The F-BILL-001 correction is also anchored at `docs/03_FEATURE_PARITY_MATRIX.md:42`. |
| CL-009 | FAIL | `observability-synthesis.md:230-235` leaves four open TODOs and explicitly says they block Released spec. |
| CL-010 | PASS | No external source URL appears in Normal Path, Failure Path, audit/log evidence, or test direction sections; the file uses local doc links and local source paths. |
| CL-011 | FAIL | Synthesis files may inherit citations from input passes, but spot-checks found a missing load-bearing source path, a false inherited outbox-lag claim, and multiple source claims without direct inherited file:line evidence. |

Detailed CL notes:

- CL-001 / CL-002 are release blockers, not necessarily proof of license contamination in the decomposition lane.
- The file itself admits it must move "cleaned of source identifiers" before becoming `docs/specs/observability-billing.md`; that cleanup is not done yet.
- CL-006 is a mechanical but hard failure. A source row that does not exist is not a verified license tier.
- CL-009 is not optional. Open TODOs are a hold signal by the checklist and by the synthesis's own line 235.
- CL-011 fails because the inherited citations are incomplete for the actual production billing path: `RecordUsage` is submitted through `UsageRecordWorkerPool`, which can drop tasks before `Apply` ever runs.

## §2 - Spot-Check Log

Spot-check method:

- I selected source claims across Sub2API atomic billing, handler submission, usage-log best-effort path, scheduler outbox, config, and Helicone ingestion.
- I used `rg -n` against local clones and cross-checked relevant repo docs.
- Verdict meanings:
- PASS: cited source supports the claim.
- FAIL: cited source exists but contradicts or materially narrows the claim.
- MISSING: the synthesis does not cite a necessary source path for the claim.

### Spot-check 01 - Sub2API `Apply` opens one transaction and commits effects together

- Synthesis claim: `Apply` is a single-transaction atomic billing primitive.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/usage_billing_repo.go:22` defines `Apply`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/usage_billing_repo.go:35` calls `r.db.BeginTx(ctx, nil)`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/usage_billing_repo.go:45` calls `claimUsageBillingKey`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/usage_billing_repo.go:54` calls `applyUsageBillingEffects`.
- Grep evidence: `.omc/reference-src/sub2api/backend/internal/repository/usage_billing_repo.go:58` commits the transaction.
- Verdict: PASS.
- Note: this proves the primitive, not the end-to-end production durability of billing.

### Spot-check 02 - Idempotent claim and archive conflict detection

- Synthesis claim: claim gate uses conflict handling with archive check and fingerprint conflict detection.
- Grep evidence: `usage_billing_repo.go:68` inserts into `usage_billing_dedup`.
- Grep evidence: `usage_billing_repo.go:77` re-reads `usage_billing_dedup` on conflict.
- Grep evidence: `usage_billing_repo.go:83` returns `ErrUsageBillingRequestConflict`.
- Grep evidence: `usage_billing_repo.go:93` reads `usage_billing_dedup_archive`.
- Grep evidence: `usage_billing_repo.go:98` returns `ErrUsageBillingRequestConflict` for archive mismatch.
- Verdict: PASS.
- Clean-room note: these source table names must not survive in implementer-facing Released prose.

### Spot-check 03 - Five billing effects and scheduler outbox enqueue

- Synthesis claim: `Apply` applies subscription, balance, API key quota, API key windows, Account quota, and scheduler outbox in one transaction.
- Grep evidence: `usage_billing_repo.go:108` defines `applyUsageBillingEffects`.
- Grep evidence: `usage_billing_repo.go:115-116` deducts balance when `BalanceCost > 0`.
- Grep evidence: `usage_billing_repo.go:331` enqueues `SchedulerOutboxEventAccountChanged` using the same `tx`.
- Grep evidence: `usage_billing_repo.go:327-329` detects total/daily/weekly threshold crossings before enqueue.
- Verdict: PASS.
- Note: source supports the effects, but not the synthesis's later "no outbox lag observability" claim.

### Spot-check 04 - Usage Record write is detached from `Apply`

- Synthesis claim: Sub2API production Usage Record write is best-effort/detached from the `Apply` transaction.
- Grep evidence: `gateway_service.go:8023` calls `applyUsageBilling`.
- Grep evidence: `gateway_service.go:8038` calls `writeUsageLogBestEffort` after the billing call.
- Grep evidence: `gateway_service.go:7812` defines `writeUsageLogBestEffort`.
- Grep evidence: `gateway_service.go:7816` uses `detachedBillingContext`.
- Grep evidence: `usage_log_repo.go:267` supports tx-in-context, but production caller does not pass `Apply`'s tx.
- Verdict: PASS.
- Note: source supports "Usage Record detached", not "billing task always runs".

### Spot-check 05 - Production handlers submit billing through a worker pool before `Apply`

- Synthesis claim by omission: the production billing path is adequately represented by `Apply`.
- Grep evidence: `gateway_handler_chat_completions.go:256-257` wraps `RecordUsage` in `submitUsageRecordTask`.
- Grep evidence: `gateway_handler.go:487-488` does the same for another path.
- Grep evidence: `openai_gateway_handler.go:398-399` does the same for OpenAI path.
- Grep evidence: `gateway_handler.go:1785-1787` calls `h.usageRecordWorkerPool.Submit(task)` and ignores the returned submit mode.
- Verdict: FAIL / MISSING.
- Why it matters: `Apply` may never run if the outer task is dropped. This is not in the synthesis's Sub2API picture.

### Spot-check 06 - Usage worker pool can drop the whole billing task

- Synthesis claim: Sub2API has "atomic, money-grade" billing, while only Usage Record write is detached/droppable.
- Grep evidence: `usage_record_worker_pool.go:43-45` defines `enqueued`, `dropped`, and `sync_fallback` submit modes.
- Grep evidence: `usage_record_worker_pool.go:145` defines `Submit`.
- Grep evidence: `usage_record_worker_pool.go:168-183` uses overflow policy and returns `UsageRecordSubmitModeDropped`.
- Grep evidence: `usage_record_worker_pool.go:21` defaults overflow policy to `sample`.
- Grep evidence: `config.go:1714-1715` defaults `gateway.usage_record.overflow_policy` to sample and sample percent to 10.
- Verdict: FAIL.
- Required synthesis correction: Sub2API has an atomic `Apply` primitive, but its handler-level billing submission is lossy under worker-pool overflow unless sync fallback executes.

### Spot-check 07 - Scheduler outbox consumer exists

- Synthesis TODO: `Find and read scheduler-outbox consumer for at-least-once delivery + idempotent invalidation`.
- Grep evidence: `scheduler_snapshot_service.go:201-209` runs an outbox worker and repeatedly calls `pollOutbox`.
- Grep evidence: `scheduler_snapshot_service.go:232-285` reads watermark, lists events, handles each event, then writes watermark.
- Grep evidence: `scheduler_snapshot_service.go:288-301` dispatches event types to account/group/full rebuild handlers.
- Verdict: PASS as source existence, FAIL as open TODO.
- Required action: close TODO-3 or convert any remaining at-least-once/idempotency uncertainty into a precise open question.

### Spot-check 08 - Sub2API does have outbox lag warning/rebuild behavior

- Synthesis claim: `Outbox consumer lag observability` is absent from Sub2API and is HUAKAI-DESIGN only.
- Grep evidence: `config.go:919-929` defines outbox poll, lag warning, lag rebuild, lag failure count, and backlog rebuild config fields.
- Grep evidence: `scheduler_snapshot_service.go:586` defines `checkOutboxLag`.
- Grep evidence: `scheduler_snapshot_service.go:592-593` logs an outbox lag warning when age exceeds threshold.
- Grep evidence: `scheduler_snapshot_service.go:596-608` triggers rebuild after repeated lag.
- Grep evidence: `scheduler_snapshot_service.go:617-628` triggers backlog rebuild when outbox backlog exceeds threshold.
- Verdict: FAIL.
- Required synthesis correction: HUAKAI may still add metrics and operator alerts, but Sub2API is not a blank slate for outbox lag observability.

### Spot-check 09 - `BeginTx(ctx, nil)` uses default isolation

- Synthesis TODO: verify whether `BeginTx(ctx, nil)` means default isolation.
- Grep evidence: `usage_billing_repo.go:35` calls `r.db.BeginTx(ctx, nil)`.
- Grep evidence: no `sql.TxOptions` with serializable isolation appears in `usage_billing_repo.go`.
- Verdict: PASS as verified open source fact.
- Required action: close TODO-4 by saying Sub2API uses driver/database default isolation; HUAKAI must explicitly choose isolation or locking semantics instead of inheriting a serializable claim.

### Spot-check 10 - Typed errors exist

- Input TODO: confirm typed errors are defined and propagated.
- Grep evidence: `service/usage_billing.go:13` defines `ErrUsageBillingRequestConflict`.
- Grep evidence: `repository/usage_billing_repo.go:173` returns `ErrSubscriptionNotFound`.
- Grep evidence: `repository/usage_billing_repo.go:186` returns `ErrUserNotFound`.
- Grep evidence: `repository/usage_billing_repo.go:212` and `:240` return `ErrAPIKeyNotFound`.
- Grep evidence: `repository/usage_billing_repo.go:310` returns `ErrAccountNotFound`.
- Verdict: PASS as source fact, MISSING in synthesis.
- Required action: if error taxonomy is retained in tests, cite these lines or paraphrase without upstream error identifiers.

### Spot-check 11 - Helicone schedules logging outside caller-visible response path

- Synthesis claim: Helicone decouples operator analytics ingestion from caller response path.
- Grep evidence: `ProxyForwarder.ts:437-453` schedules `log(...)` via asynchronous continuation before returning response.
- Grep evidence: `ProxyForwarder.ts:487` defines the log operation.
- Grep evidence: `ProxyForwarder.ts:522-543` starts logging in parallel with response processing and sends through `HeliconeProducer`.
- Verdict: PASS.
- Note: GPL source is behavior-only evidence; no implementation structure should be copied.

### Spot-check 12 - Helicone queue producer and retry/fallback paths exist

- Synthesis claim: Helicone supports queue-backed ingestion and internal fallback.
- Grep evidence: `HeliconeProducer.ts:37-55` chooses a producer and sends to it when configured.
- Grep evidence: `HeliconeProducer.ts:60-67` falls back to internal HTTP logging when no producer is configured.
- Grep evidence: `KafkaProducerImpl.ts:42-49` attempts produce and logs failed attempts.
- Verdict: PASS.

### Spot-check 13 - Helicone DLQ behavior exists in ingestion manager

- Synthesis claim: Helicone provides a dead-letter path.
- Grep evidence: `LogManager.ts:174-181` decides `pushToDLQ` and sends failed per-message items to `request-response-logs-prod-dlq`.
- Grep evidence: `LogManager.ts:309-316` does the same for failed batch logging.
- Grep evidence: `LogManager.ts:188-202` and `:323-329` record DLQ send outcomes.
- Verdict: PASS.
- Note: synthesis should not name Helicone topics in Released prose.

### Spot-check 14 - Helicone hot/cold split and polling dashboards

- Synthesis claim: Helicone splits analytic metadata from body storage and uses polling dashboards.
- Grep evidence: `LoggingHandler.ts:178-190` chooses body storage location and maps S3 records.
- Grep evidence: `LoggingHandler.ts:293-316` writes log store, S3, and ClickHouse paths in durable logging.
- Grep evidence: `LoggingHandler.ts:319-348` handles S3 upload path.
- Grep evidence: `VersionedRequestStore.ts:25` inserts request/response records into ClickHouse-backed analytic store.
- Grep evidence: `useJawnMetrics.ts:41`, `:63`, `:85`, `:110`, and later hooks use `refetchInterval: isLive ? 5_000 : undefined`.
- Verdict: PASS.

### Spot-check 15 - Helicone terminal stream reason is explicit

- Synthesis claim: Helicone captures stream terminal reason explicitly.
- Grep evidence: `ReadableInterceptor.ts:6` defines reason values `cancel`, `done`, and `timeout`.
- Grep evidence: `ReadableInterceptor.ts:38-54` records terminal reason.
- Grep evidence: `ReadableInterceptor.ts:94-143` records cancellation and timeout outcomes.
- Grep evidence: `ProxyRequestHandler.ts:28-40` maps end reason into observability status semantics.
- Verdict: PASS.

## §3 - Findings

### Critical Findings

No CRITICAL finding survives realist check.

The worker-pool billing gap has financial-risk severity if implemented incorrectly, but it is a pre-release synthesis defect and HUAKAI's proposed Tx1/Tx2 design already points in the safer direction. I rate it MAJOR, not CRITICAL, because the required correction is bounded and no implementation has shipped from this synthesis.

### Major Findings

1. The core "Sub2API HAS atomic billing" framing omits the lossy outer worker-pool path.
   - Evidence: `observability-synthesis.md:13` says the correction is that Sub2API has atomic billing and HUAKAI only promotes Usage Record into the transaction.
   - Evidence: `observability-synthesis.md:19-26` presents the Sub2API picture as `Apply` plus detached Usage Record only.
   - Evidence: `gateway_handler.go:1785-1787` submits the whole `RecordUsage` task to `UsageRecordWorkerPool.Submit` and ignores the return value.
   - Evidence: `usage_record_worker_pool.go:168-183` can return `UsageRecordSubmitModeDropped` on overflow.
   - Evidence: `usage_record_worker_pool.go:21` and `config.go:1714-1715` default overflow behavior to sampled sync fallback, not guaranteed sync.
   - Confidence: HIGH.
   - Why this matters: if the final spec treats Sub2API as end-to-end money-grade, HUAKAI can miss the real production failure mode: the entire post-response billing task can be dropped before `Apply` runs.
   - Fix: reframe Sub2API as "atomic `Apply` primitive behind a lossy async submission boundary." Add a HUAKAI invariant that billing settlement must be triggered by a durable claim/reservation path, never by a lossy analytics worker queue.

2. Outbox lag observability is falsely classified as absent from Sub2API.
   - Evidence: `observability-synthesis.md:33` lists "Outbox consumer lag observability" as a Sub2API gap.
   - Evidence: `observability-synthesis.md:77`, `:158`, and `:176` label outbox lag metric/alert as HUAKAI-DESIGN.
   - Evidence: `config.go:919-929` defines outbox lag warning/rebuild/backlog settings.
   - Evidence: `scheduler_snapshot_service.go:586-628` checks lag, logs warnings, triggers lag rebuild, and triggers backlog rebuild.
   - Confidence: HIGH.
   - Why this matters: this is a false source delta. HUAKAI can still improve from logs/rebuild to metrics/alerts, but the source truth is not "Sub2API has none."
   - Fix: rewrite as "Sub2API has scheduler outbox polling, lag warning, and rebuild safeguards; HUAKAI adds operator-grade metric, alert routing, SLA threshold, and dashboard surfacing."

3. CL-006 fails because the Helicone license row is wrong.
   - Evidence: `observability-synthesis.md:10` cites Helicone as `E-LIC-009`.
   - Evidence: `docs/07_REFERENCE_EVIDENCE_LEDGER.md:21` defines Helicone as `E-LIC-007`, GPL-3.0-or-later.
   - Evidence: `rg -n "E-LIC-009" docs/07_REFERENCE_EVIDENCE_LEDGER.md` returned no matching row.
   - Confidence: HIGH.
   - Why this matters: CL-006 requires every source to point to a verified license tier. A non-existent row is a hard release gate failure.
   - Fix: replace `E-LIC-009` with `E-LIC-007` and use the full commit hash from the Helicone input file.

4. Open TODOs explicitly block Released status, and several are stale.
   - Evidence: `observability-synthesis.md:230-235` lists TODO-1..TODO-4 and says they block Released spec.
   - Evidence: TODO-3 is stale because `scheduler_snapshot_service.go:201-285` is the scheduler outbox consumer path.
   - Evidence: TODO-4 is resolvable because `usage_billing_repo.go:35` uses `BeginTx(ctx, nil)` and no serializable options are present in the file.
   - Evidence: typed error TODOs from the Sub2API input are resolvable via `service/usage_billing.go:13` and `usage_billing_repo.go:173`, `:186`, `:212`, `:240`, `:310`.
   - Confidence: HIGH.
   - Why this matters: CL-009 says open questions are a hold signal. A file with explicit release-blocking TODOs cannot move to `docs/specs/observability-billing.md` as Released.
   - Fix: close TODOs with verified source text or move truly unresolved items into a non-Released follow-up list outside the implementer spec.

5. The pending reconciliation design contradicts the domain model's immutable Usage Record rule.
   - Evidence: `observability-synthesis.md:136` says a reconciliation worker can "update Usage Record + write delta to billing event log."
   - Evidence: `docs/19_DOMAIN_MODEL.md:79` says "Usage Record is immutable" and corrections happen through paired adjustment rows in the Billing Ledger, never by mutating an existing Usage Record.
   - Confidence: HIGH.
   - Why this matters: implementers can build mutable Usage Records and break auditability. This is not just wording; it changes the accounting model.
   - Fix: replace "update Usage Record" with "append a reconciliation event and paired Billing Ledger adjustment linked to the original immutable Usage Record."

6. CL-001 / CL-002 source identifier exposure is too broad for Released spec.
   - Evidence: `observability-synthesis.md:20`, `:26`, `:30`, `:66`, `:100`, `:206`, and `:233` carry upstream method/function/error/config identifiers.
   - Evidence: `observability-synthesis.md:21`, `:54`, `:78`, `:91`, `:111-148`, `:163`, `:210-211`, and `:225` carry schema-like names or table-like names.
   - Confidence: HIGH.
   - Why this matters: reviewers can cite source identifiers, but implementer-facing specs should describe HUAKAI behavior and domain entities, not upstream implementation fingerprints.
   - Fix: move source identifiers to a reviewer-only evidence appendix or remove them during the move to `docs/specs/observability-billing.md`.

7. Several reference behavior claims lack inherited file:line citations in the synthesis.
   - Evidence: `observability-synthesis.md:41-55` summarizes Sub2API/Helicone convergence but does not carry direct file:line citations.
   - Evidence: `observability-synthesis.md:55` says Sub2API has "tenant scaffolding but doesn't fully exercise" without source evidence.
   - Evidence: `observability-synthesis.md:182-184` marks lock-order and cache-source-of-truth as KEEP from prior cycle-1 synthesis without source citation in this file.
   - Confidence: HIGH.
   - Why this matters: CL-011 permits synthesis files to inherit citations only if behavior claims remain source-traceable. Prior prose decomposition is not a substitute for source truth.
   - Fix: either add direct source evidence references for each retained reference claim or relabel the item HUAKAI-DESIGN / Open Question.

8. The test plan misses the newly discovered worker-pool drop path.
   - Evidence: `observability-synthesis.md:203-214` lists Sub2API-inheritable tests, but none covers `UsageRecordWorkerPool.Submit` returning dropped before `Apply`.
   - Evidence: `usage_record_worker_pool.go:181-183` drops on queue full when overflow policy does not sync.
   - Confidence: HIGH.
   - Why this matters: this is the failure mode that separates "atomic `Apply`" from "money-grade end-to-end billing." Without an acceptance test, the final spec can repeat the same blind spot.
   - Fix: add an HUAKAI acceptance test: "billing settlement submission queue overflow cannot drop billing; system must either reserve before upstream, run sync durable settlement, or fail closed with audit event."

### Minor Findings

1. The test heading says `AT-OBS-001..017`, but the list runs through `AT-OBS-019`.
   - Evidence: `observability-synthesis.md:201` vs `observability-synthesis.md:225-226`.
   - Fix: change heading to `AT-OBS-001..019`.

2. The "default 60s" lag threshold is a HUAKAI policy choice, not a source-derived default.
   - Evidence: `observability-synthesis.md:77` and `:158` set 60s; Sub2API source defaults warning/rebuild thresholds elsewhere.
   - Fix: label it explicitly as HUAKAI operator default and do not imply source inheritance.

3. `Helicone-style` is imprecise for HUAKAI DLQ persistence.
   - Evidence: `observability-synthesis.md:160-165` mandates a PostgreSQL DLQ table.
   - Fix: say "Helicone-inspired behavior; HUAKAI storage choice" because the specific DLQ persistence store is local design.

4. `Status | Action Plan` is correct for a synthesis but not for the destination Released spec.
   - Evidence: `observability-synthesis.md:5` and `:12`.
   - Fix: destination spec must get a Released/Reviewed header after fixes are applied by the specifier/implementer process.

## What's Missing

- No source-traceable statement that the outer `UsageRecordWorkerPool` is lossy and can drop the entire billing task.
- No release-safe replacement for the source-shaped `Apply` / `writeUsageLogBestEffort` wording.
- No correction for Sub2API's real scheduler outbox lag warning/rebuild behavior.
- No acceptance test for billing task queue overflow or worker-pool stop.
- No acceptance test proving Usage Record reconciliation is append-only and does not mutate immutable records.
- No explicit boundary between billing-grade Usage Record and analytics/body-retention records.
- No source citation for "Sub2API tenant scaffolding but doesn't fully exercise."
- No source citation for "lock order is fixed alphabetical by entity-id pair"; this reads like HUAKAI design, not a source KEEP.
- No direct mapping from the proposed hot/cold table names to `docs/18_GLOSSARY.md` and `docs/19_DOMAIN_MODEL.md`.
- No decision on whether HUAKAI Tx2 uses default database isolation, explicit row locks, serializable isolation, or idempotent retry after serialization failures.
- No operator workflow acceptance test for DLQ replay, retry success rate, suppress/escalate action, and audit actor.
- No security test direction for token-leakage-safe logging, despite `H9` being listed as a HUAKAI improvement.

## Ambiguity Risks

- `Sub2API HAS atomic billing` can mean "the repository method is atomic" or "the production request path cannot lose billing."
- Risk if wrong interpretation chosen: implementers may put settlement behind a lossy worker and still claim parity.

- `Outbox consumer lag observability` can mean "Prometheus-style metric plus alert" or "any lag warning/rebuild behavior."
- Risk if wrong interpretation chosen: HUAKAI falsely claims a design improvement instead of a better operator-grade equivalent.

- `update Usage Record + write delta` can mean mutating the original record or appending a new correction record.
- Risk if wrong interpretation chosen: audit immutability is broken.

- `usage_record_dlq table` can mean a required logical capability or a premature physical schema name.
- Risk if wrong interpretation chosen: implementers add schema before schema review, or clean-room reviewers treat it as a copied table-like fingerprint.

## Multi-Perspective Notes

- Executor perspective: an executor following only this synthesis would not know that `RecordUsage` can be dropped before `Apply`. That is a direct implementation trap.
- Stakeholder perspective: the proposed HUAKAI architecture is directionally stronger than both references, but the current source truth is not clean enough to become Released.
- Skeptic perspective: the document still trusts the Sub2API input pass too much. The scheduler outbox and worker-pool source checks show the input pass did not cover the full production path.
- Security perspective: token-leakage-safe logging is listed as a design improvement, but no acceptance test or redaction boundary is specified.
- Ops perspective: outbox lag, DLQ depth, replay success, and worker-pool drop counts must be operator-visible. The spec currently mentions some of these but does not connect them to testable alerts.
- New-hire perspective: the file reads like a synthesis memo. A new implementer would have to infer which source identifiers are evidence-only and which HUAKAI names are allowed.

## §4 - FINAL VERDICT

Verdict: APPROVE-WITH-FIXES.

Meaning:

- Do not move `observability-synthesis.md` to `docs/specs/observability-billing.md` Status=Released as-is.
- The corrected high-level direction is valid: HUAKAI should use Tx1 reservation, Tx2 durable settlement, immutable accounting records, analytics decoupling, DLQ/replay, hot/cold retention, and tenant isolation.
- The artifact is not release-clean because CL-001, CL-002, CL-006, CL-009, and CL-011 fail.
- The remaining fixes are bounded enough that I am not issuing REJECT.
- If the worker-pool drop gap or Usage Record immutability conflict is not fixed, the verdict downgrades to REJECT.

### Required Fixes Before Released

1. Reframe the critical correction at `observability-synthesis.md:13` and section 1 at `:19-34`.
   - Recommended replacement:
   - `Sub2API has an atomic UsageBillingRepository.Apply primitive: once Apply runs, claim + billing effects + outbox commit atomically. Sub2API does NOT guarantee end-to-end durable billing submission, because production handlers submit the whole RecordUsage task through a bounded worker pool that can drop tasks under overflow. HUAKAI's improvement is durable pre-call reservation plus non-lossy Tx2 settlement, and promoting billing-grade Usage Record / audit event into Tx2.`

2. Add the worker-pool gap after `observability-synthesis.md:34`.
   - Recommended addition:
   - `Additional verified gap: Sub2API's handler-level usage/billing task is submitted through a bounded worker pool. When overflow policy drops rather than sync-fallbacks, Apply is never invoked. HUAKAI must not place billing settlement behind a lossy analytics queue. Evidence: gateway_handler.go:1785-1787; usage_record_worker_pool.go:168-183; config.go:1714-1715.`

3. Correct the Helicone source row at `observability-synthesis.md:10`.
   - Recommended replacement:
   - `Helicone ([E-LIC-007](../../07_REFERENCE_EVIDENCE_LEDGER.md), GPL-3.0-or-later, commit 548832f8e763a33732ead27d8b2dcaeccc665a39 - behavior-only by clean-room policy)`

4. Rewrite outbox lag claims at `observability-synthesis.md:33`, `:77`, `:158`, and `:176`.
   - Recommended replacement:
   - `Sub2API has scheduler outbox polling plus lag warning/rebuild/backlog safeguards. HUAKAI improves this into an operator-grade metric, alert, dashboard surface, and tested SLA threshold. Evidence: scheduler_snapshot_service.go:586-628; config.go:919-929.`

5. Resolve TODOs at `observability-synthesis.md:230-235`.
   - Recommended replacement:
   - `Closed before release: usage_log_repo full path verified for tx-in-context, best-effort batching, LRU dedup, and queue-full drop; billing_cache_service verified as async cache update queue; scheduler outbox consumer verified in SchedulerSnapshotService; Apply verified to use default DB isolation via BeginTx(ctx, nil). Remaining isolation choice is HUAKAI-DESIGN and must be specified in Tx1/Tx2 invariants.`

6. Fix the Usage Record immutability conflict at `observability-synthesis.md:136`.
   - Recommended replacement:
   - `When inferred or partial usage later receives an authoritative report, append a reconciliation event and paired Billing Ledger adjustment linked to the original immutable Usage Record. Do not mutate the original Usage Record or the committed claim.`

7. Scrub release-facing source identifiers and upstream schema names across `observability-synthesis.md:20-33`, `:54-66`, `:90-104`, `:138-165`, and `:205-214`.
   - Recommended method:
   - Use HUAKAI glossary terms in the main spec: Billing Ledger claim, Usage Record, Provider Account quota, Account cache invalidation, settlement transaction, analytics DLQ.
   - Keep source method/table/error names only in a non-implementer evidence appendix, or remove them from the Released spec entirely.

8. Add CL-011 source evidence or relabel unverified source claims at `observability-synthesis.md:41-55` and `:182-184`.
   - Recommended replacement pattern:
   - `Reference-backed behavior: <claim> (source: <input file section + local source file:line>).`
   - `HUAKAI design: <claim> (not in source).`
   - `Open question: <claim> (blocks release until resolved).`

9. Add required acceptance tests under `observability-synthesis.md:201-226`.
   - Recommended additions:
   - `AT-OBS-020 / Billing submission cannot be dropped: settlement queue overflow must sync-fallback, reserve-before-upstream, or fail closed; no successful upstream response may bypass durable settlement/audit.`
   - `AT-OBS-021 / Reconciliation is append-only: late authoritative usage appends adjustment rows and does not mutate original Usage Record.`
   - `AT-OBS-022 / Outbox lag alert: source-backed lag warning/rebuild exists; HUAKAI metric and operator alert fire at configured threshold.`

10. Fix the test range heading at `observability-synthesis.md:201`.
    - Recommended replacement:
    - `## 10. Test Scenarios (AT-OBS-001..022)`

### Realist Check

- Finding 1 stays MAJOR: the realistic worst case is financial under-billing if the wrong pattern is implemented. Mitigated by the fact that this is still pre-release and HUAKAI already proposes Tx1/Tx2.
- Finding 2 stays MAJOR: false source attribution contaminates design justification, but the fix is textual and preserves the HUAKAI metric/alert improvement.
- Finding 3 stays MAJOR: wrong license row is mechanical, but CL-006 is a hard release gate.
- Finding 4 stays MAJOR: open TODOs block release, but several are already source-resolved.
- Finding 5 stays MAJOR: immutable accounting conflicts cause significant rework if implemented incorrectly.
- Finding 6 stays MAJOR: clean-room exposure is broad but can be fixed by scrubbing and evidence appendix discipline.
- No finding was downgraded solely because it is easy to fix; release gates are about preventing bad specs, not about edit difficulty.

### Upgrade Conditions

- Upgrade to APPROVE-FOR-RELEASED only after all 10 required fixes are applied.
- Rerun at least 8 citation spot-checks after the worker-pool and outbox-lag corrections, because those fixes introduce new source claims.
- Do not carry any open TODO into `docs/specs/observability-billing.md`.
- Do not keep upstream method/table/error names in implementer-facing Released sections.
- If the specifier keeps mutable Usage Record reconciliation, final verdict becomes REJECT because it contradicts `docs/19_DOMAIN_MODEL.md`.

## Appendix A - Assumptions, Pre-Mortem, and Dependency Audit

### Key Assumptions Extracted

| Assumption | Rating | Evidence / concern |
| --- | --- | --- |
| The artifact is intended to become `docs/specs/observability-billing.md`. | VERIFIED | `observability-synthesis.md:12`. |
| F-OBS-001 exists in the parity matrix. | VERIFIED | `docs/03_FEATURE_PARITY_MATRIX.md:48`. |
| F-BILL-001 correction is in scope. | VERIFIED | `observability-synthesis.md:6`, `:13`; parity row at `docs/03_FEATURE_PARITY_MATRIX.md:42`. |
| Sub2API `Apply` is atomic once invoked. | VERIFIED | `usage_billing_repo.go:22-58`. |
| Sub2API production billing is end-to-end durable. | FRAGILE / FALSE | `RecordUsage` is submitted to a lossy worker pool before `Apply`. |
| Sub2API has no outbox lag observability. | FRAGILE / FALSE | `scheduler_snapshot_service.go:586-628` and `config.go:919-929`. |
| Helicone source license row is `E-LIC-009`. | FALSE | Ledger row is `E-LIC-007`. |
| Usage Records may be updated during reconciliation. | FALSE | Domain model says Usage Records are immutable. |
| Open TODOs do not block release. | FALSE | `observability-synthesis.md:235` says they do block Released spec. |

### Pre-Mortem

Assume the synthesis was released exactly as written and failed. Specific failure scenarios:

1. Implementer puts settlement behind a bounded async worker and believes `Apply` atomicity is enough. Under overflow, billing tasks drop after successful upstream responses.
   - Covered by synthesis? No.
   - Finding: Major Finding 1.

2. Spec claims HUAKAI invented outbox lag observability; later reviewer finds Sub2API already has lag warning/rebuild and blocks clean-room/source-truth approval.
   - Covered by synthesis? No.
   - Finding: Major Finding 2.

3. Release gate fails because Helicone license source points to a non-existent `E-LIC-009`.
   - Covered by synthesis? No.
   - Finding: Major Finding 3.

4. Implementer mutates Usage Records during reconciliation, breaking append-only audit semantics.
   - Covered by synthesis? No.
   - Finding: Major Finding 5.

5. Clean-room reviewer rejects Released spec because source method and schema identifiers remain in implementer-facing text.
   - Covered by synthesis? Only by saying the future move will be cleaned.
   - Finding: Major Finding 6.

6. Implementer starts work while TODOs remain open and later discovers default isolation / outbox consumer behavior was assumed incorrectly.
   - Covered by synthesis? It lists TODOs but still says they do not block synthesis.
   - Finding: Major Finding 4.

### Dependency Audit

| Dependency | Status | Notes |
| --- | --- | --- |
| Local Sub2API clone exists and matches pinned commit. | PASS | `git rev-parse HEAD` returned `b0a2252ed19c3720e6adafde6083e64fbac2efa9`. |
| Local Helicone clone exists and matches input commit. | PASS | `git rev-parse HEAD` returned `548832f8e763a33732ead27d8b2dcaeccc665a39`. |
| Sub2API license row exists. | PASS | `E-LIC-001` at `docs/07_REFERENCE_EVIDENCE_LEDGER.md:15`. |
| Helicone license row exists. | PASS in ledger, FAIL in synthesis | Ledger row is `E-LIC-007`; synthesis says `E-LIC-009`. |
| F-OBS-001 parity row exists. | PASS | `docs/03_FEATURE_PARITY_MATRIX.md:48`. |
| F-BILL-001 parity row exists. | PASS | `docs/03_FEATURE_PARITY_MATRIX.md:42`. |
| Domain model supports mutable Usage Record reconciliation. | FAIL | `docs/19_DOMAIN_MODEL.md:79` says Usage Record is immutable. |
| Open TODOs closed before release. | FAIL | TODO-1..TODO-4 remain. |
| Release-facing source identifiers scrubbed. | FAIL | Multiple source method/table/error names remain. |

### Self-Audit

- Major Finding 1 confidence: HIGH. Could author refute with context? No; source shows `Submit` can return dropped and caller ignores result.
- Major Finding 2 confidence: HIGH. Could author refute with context? Only by defining "observability" as Prometheus metric only. The current wording is broader and false.
- Major Finding 3 confidence: HIGH. Could author refute with context? No; the cited row does not exist in the ledger.
- Major Finding 4 confidence: HIGH. Could author refute with context? No; TODOs are in the file and line 235 says they block Released spec.
- Major Finding 5 confidence: HIGH. Could author refute with context? No; domain model directly contradicts the update wording.
- Major Finding 6 confidence: HIGH. Could author refute with process context? Partly, because synthesis files can carry evidence. Kept as release blocker, not proof of contamination.
- Major Finding 7 confidence: HIGH. Could author refute with context? Only by adding citations; as written they are missing.
- Major Finding 8 confidence: HIGH. Could author refute with context? No; no test covers worker-pool drop.

## §5 - Owner-Facing Chinese Summary

最终结论：`observability-synthesis.md` 只能 `APPROVE-WITH-FIXES`，不能直接移动为 `docs/specs/observability-billing.md` Released。最关键的问题是 Sub2API 的 `Apply` 本身是原子事务，但生产路径先把整个 `RecordUsage` 任务丢进有界 worker pool，队列溢出时可能 drop；所以当前“Sub2API has atomic billing”的表述不完整，必须改成“atomic primitive, lossy submission boundary”。另外，Helicone license row 写错为不存在的 `E-LIC-009`，Open TODO 明确阻塞 Released，Sub2API 的 outbox lag 行为也被误判为不存在。没有发现需要删功能的地方，但必须修正 source truth、clean-room identifier 暴露、Usage Record 不可变性冲突和测试缺口后，才能进入 Released。
