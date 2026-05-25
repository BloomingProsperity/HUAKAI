# Helicone Observability Source-Verified Cross-Reference for F-OBS-001
Status: Draft source-verified decomposition  
Author: Codex  
Date: 2026-04-28  
Reference: Helicone at commit `548832f8e763a33732ead27d8b2dcaeccc665a39`  
License: GPL-3.0; behavioral-only source verification; no implementation reuse  
Lane: Specifier-lane source verification  
Scope: Observability ingestion, Usage Record persistence, tenant isolation, operator freshness, and reconciliation behavior
Clean-room boundary:
- This document is a behavior decomposition only.
- It intentionally does not reproduce Helicone source code, SQL, schema definitions, field names, request/response shapes, implementation identifiers beyond source file paths, or distinctive comments.
- File and line citations are included only to satisfy CL-011 traceability.
- Implementer-lane agents must use this document as the source of product behavior and must not read the GPL-3.0 source paths cited below.
## 1. Helicone Observability Ingestion Architecture
### 1.1 Reviewed Source Areas
The Helicone clone contains multiple observability-relevant subsystems:
- Gateway/proxy request forwarding and response interception under `worker/src/lib/...`.
- Queue producers under `worker/src/lib/clients/producers/...` and `valhalla/jawn/src/lib/clients/...`.
- Batch consumers under `valhalla/jawn/src/workers/...` and `valhalla/jawn/src/lib/clients/...`.
- Ingestion handlers under `valhalla/jawn/src/lib/handlers/...`.
- Durable analytic persistence under `valhalla/jawn/src/lib/stores/request/...`.
- Operator dashboard polling hooks under `web/components/...` and `web/services/hooks/...`.
Source traces:
- `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`
- `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:117-200`
- `worker/src/lib/dbLogger/DBLoggable.ts:804-1032`
- `worker/src/lib/clients/producers/HeliconeProducer.ts:6-80`
- `valhalla/jawn/src/managers/LogManager.ts:71-229`
- `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-389`
- `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-35`
- `web/components/templates/dashboard/dashboardPage.tsx:151-186`
- `web/services/hooks/useJawnMetrics.ts:21-377`
### 1.2 End-to-End Data Flow
Helicone's observability path is not a direct inline database write from the gateway request handler. The observed flow is:
1. The proxy forwards the upstream request and returns the provider response to the caller.
2. The proxy wraps streaming response bodies so that response bytes and stream termination reason can be observed while the client receives data.
3. The proxy schedules observability logging outside the client response path.
4. The proxy-side logger builds a compact queue message from request metadata, response metadata, captured body material when retained, timing, usage, and cost signals.
5. The queue producer sends the message to a configured queue backend, or falls back to an internal HTTP logging path when no queue producer is configured.
6. The ingestion service consumes queue messages, authenticates tenant context, enriches request and response bodies, computes usage and cost when possible, and batches durable writes.
7. Durable observability state is split between an analytic store for request/usage metrics and an object-storage tier for larger request/response bodies.
8. Operator dashboards query the analytic path and use short-interval polling when live mode is enabled.
Evidence:
- The proxy returns the client response while scheduling logging through an asynchronous runtime continuation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`.
- Streaming responses are wrapped with an interceptor before returning to the caller: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:117-124`.
- The observable response used for logging waits for the intercepted body and terminal stream reason: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:187-200`.
- The proxy-side logger builds and sends an ingestion message after extracting request/response, timing, usage, and cost material: `worker/src/lib/dbLogger/DBLoggable.ts:804-1032`.
- Producer selection supports queue-backed ingestion and an internal fallback path: `worker/src/lib/clients/producers/HeliconeProducer.ts:6-80`.
- The ingestion manager uses a handler chain for authentication, body processing, usage/cost processing, durable logging, and downstream integrations: `valhalla/jawn/src/managers/LogManager.ts:71-118`.
- The durable logging handler batches processed records and writes analytic records plus body-storage records: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-389`.
### 1.3 Gateway to Queue Boundary
The proxy does not require observability persistence to complete before sending the upstream response to the client. Logging is attached to an asynchronous continuation after the response has been prepared.
This is a deliberate latency isolation design:
- Client response latency is protected from the analytic database and object store.
- Observability durability depends on the asynchronous continuation and queue/write path.
- The gateway may still compute cost and wallet/rate-limit side effects in the same asynchronous continuation after response processing.
Evidence:
- Normal response handling schedules logging after response preparation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-453`.
- Generated error responses also schedule logging asynchronously: `worker/src/lib/HeliconeProxyRequest/ErrorForwarder.ts:105-153`.
- The proxy-side log operation sends to the producer rather than directly committing a local transaction with the client response: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:487-543`.
- Response post-processing and cost finalization happen in the same asynchronous continuation, not in the caller-visible response path: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:545-741`.
### 1.4 Queue and Consumer Behavior
Helicone supports queue-first ingestion with retry and dead-letter behavior, but the behavior differs by queue backend:
- Proxy-side queue producers attempt delivery more than once before surfacing producer failure.
- Jawn-side queue production can process inline when no external queue is configured.
- One queue consumer deletes messages only after processing success.
- The Kafka-style consumer resolves offsets after the processing attempt, including error returns; separate dead-letter logic in the ingestion manager is therefore important.
- The ingestion manager sends failed messages or failed batches to a dead-letter path when queueing is enabled.
Evidence:
- Worker queue producer selection and fallback: `worker/src/lib/clients/producers/HeliconeProducer.ts:6-80`.
- Worker queue send retry behavior: `worker/src/lib/clients/producers/KafkaProducerImpl.ts:29-58`; `worker/src/lib/clients/producers/SQSProducer.ts:39-66`.
- Jawn queue producer inline fallback: `valhalla/jawn/src/lib/clients/HeliconeQueueProducer.ts:31-69`.
- Jawn SQS-style batch producer returns failure when batch send fails: `valhalla/jawn/src/lib/producers/SQSProducer.ts:26-73`.
- SQS-style consumer deletes only after successful processing: `valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:50-109`.
- Kafka-style consumer processes mini-batches and resolves offsets after the attempt: `valhalla/jawn/src/lib/clients/kafkaConsumers/KafkaConsumer.ts:65-193`.
- Per-message and batch failure dead-letter handling: `valhalla/jawn/src/managers/LogManager.ts:143-205`; `valhalla/jawn/src/managers/LogManager.ts:271-334`.
### 1.5 Persistent Store to Operator Dashboard
The operator experience is near-real-time polling, not a push stream.
Observed behavior:
- Dashboard pages store a live-mode flag and pass it into metrics hooks.
- Metrics hooks use short polling intervals when live mode is active.
- Request-list pages also use live mode; the time range can be adjusted so new requests remain visible.
- Request-table and request-count hooks poll at short intervals when live mode is active.
Evidence:
- Dashboard live flag and hook wiring: `web/components/templates/dashboard/dashboardPage.tsx:151-186`.
- Dashboard live control and manual refresh affordance: `web/components/templates/dashboard/dashboardPage.tsx:420-427`; `web/components/shared/LivePill.tsx:14-50`.
- Dashboard metrics hooks poll in live mode: `web/components/templates/dashboard/useDashboardPage.tsx:95-122`; `web/services/hooks/useJawnMetrics.ts:21-377`.
- Request page live flag and time-range behavior: `web/components/templates/requests/RequestsPage.tsx:152-208`.
- Request-list hook passes live mode through to query hooks: `web/components/templates/requests/useRequestsPageV2.tsx:113-120`.
- Request-table and count hooks poll in live mode: `web/services/hooks/requests.tsx:97-123`; `web/services/hooks/requests.tsx:213-233`.
## 2. Usage Record Persistence Semantics
### 2.1 Where the Usage Record Is Persisted
Helicone's Usage Record equivalent is persisted primarily in the analytic request/response store, with large body content optionally stored in an object-storage tier. The persistence model is multi-tier:
- Analytic metadata, timing, usage, cost, status, tenant identity, and searchable request attributes go to the analytic store.
- Larger or raw request/response bodies can be retained in object storage.
- A legacy relational write path exists for selected onboarding or prompt-related side effects, but the observed request/usage analytic record is the analytic-store row.
Evidence:
- The ingestion handler builds analytic request/response records from processed request, response, tenant, usage, and cost data: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:476-587`.
- The analytic request/response store performs the insert: `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-35`.
- The durable logging handler prepares object-storage records and analytic records in the same ingestion pass: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-272`.
- The storage handler writes multiple durable tiers in parallel and reports failure if any required tier fails: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:282-317`.
- Body-retention behavior chooses inline, empty, or object-storage-backed material depending on size and storage decision: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:641-678`.
### 2.2 Single Write or Multi-Tier
Helicone is multi-tier rather than single-write.
The record visible to operators is anchored in the analytic store, while request/response bodies may be stored separately. The ingestion layer treats the durable write as a batch that can include analytic records, object-storage records, and selected relational side effects.
HUAKAI interpretation:
- For F-OBS-001, "Usage Record persistence" should distinguish the immutable accounting record from body retention.
- A single logical Usage Record can have attachments or body references, but the accounting row should not depend on optional body storage to be valid.
- Helicone's split is useful for performance and storage cost, but HUAKAI should explicitly define the consistency contract between the Usage Record and body attachments.
Evidence:
- Multi-record batch assembly: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-272`.
- Parallel durable writes across tiers: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:282-317`.
- Analytic insert path: `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-35`.
- Object-storage keying is tenant- and request-scoped: `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353`.
### 2.3 Atomicity with Upstream Call Response
The Usage Record write is not atomic with the upstream response.
The gateway can return the provider response before observability persistence has completed. Logging runs in an asynchronous continuation, and the durable write normally occurs after queue production and ingestion processing.
This means:
- The client can receive a successful response even if observability persistence later fails.
- Queue delivery and dead-letter handling become the durability boundary, not the HTTP response boundary.
- Cost computation and wallet/rate-limit finalization can be coupled to response post-processing, but they are still outside the immediate client response path.
Evidence:
- Response returned with asynchronous logging continuation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`.
- Logging scheduled after response preparation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-453`.
- Log operation constructs and sends the ingestion message asynchronously: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:487-543`.
- Post-response cost and wallet/rate-limit side effects occur in the continuation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:545-741`.
### 2.4 Ordering
The observed ordering is:
1. Forward upstream request.
2. Return response stream wrapper to caller.
3. Capture response body and terminal stream reason as the client consumes the body.
4. Build loggable request/response data after the intercepted stream resolves, cancels, or times out.
5. Send a queue or fallback logging message.
6. Ingest, enrich, compute usage/cost, and persist durable analytic state.
For non-streaming responses, the gateway can inspect the full raw response body before finalizing log content. For streaming responses, completion, cancellation, or timeout determines what body material and status semantics are available.
Evidence:
- Response interceptor wrapping: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:117-124`.
- Loggable response waits on intercepted body completion: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:187-200`.
- Interceptor appends chunks as they pass through: `worker/src/lib/util/ReadableInterceptor.ts:56-63`.
- Interceptor records cancellation and timeout outcomes: `worker/src/lib/util/ReadableInterceptor.ts:94-143`.
- Terminal stream reason is mapped to observability status semantics: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40`.
### 2.5 Durability
Helicone has stronger durability than a pure fire-and-forget database write because it can use a queue and dead-letter path. It is still not fully durable at the moment the client receives the response.
Durability stages:
- Before queue delivery: vulnerable to runtime crash or continuation loss.
- After queue delivery: durable if the queue backend accepts the message.
- During ingestion: retry/dead-letter behavior depends on the consumer path.
- After analytic and body-tier commit: durable for operator query paths.
Evidence:
- Asynchronous continuation boundary: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-453`.
- Queue producer retry behavior: `worker/src/lib/clients/producers/KafkaProducerImpl.ts:29-58`; `worker/src/lib/clients/producers/SQSProducer.ts:39-66`.
- SQS-style consumer deletes after successful processing: `valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:50-109`.
- Kafka-style consumer offset resolution occurs after the processing attempt and makes dead-letter behavior important: `valhalla/jawn/src/lib/clients/kafkaConsumers/KafkaConsumer.ts:65-193`.
- Dead-letter handling exists for message and batch failures: `valhalla/jawn/src/managers/LogManager.ts:143-205`; `valhalla/jawn/src/managers/LogManager.ts:271-334`.
### 2.6 Idempotency and Deduplication Observations
Helicone has a request-identity-centered model in the observed storage and read paths, but this pass did not identify a complete exactly-once contract for duplicate queue delivery, replay, or partial multi-tier commit.
HUAKAI should not infer exactly-once semantics from Helicone. The safer design is:
- Use a stable Usage Record identity.
- Enforce idempotent insert/update semantics around that identity.
- Make queue replay safe.
- Make body attachment writes retryable without double-billing.
- Make accounting immutable except through explicit reconciliation events.
Evidence boundary:
- Request/response analytic records carry request identity through storage and read/update paths: `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-35`; `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:54-110`.
- Queue consumers and dead-letter paths imply retry/replay behavior must be tolerated: `valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:50-109`; `valhalla/jawn/src/managers/LogManager.ts:143-205`.
- No complete exactly-once contract was identified in the reviewed ingestion files.
## 3. Out-of-Band Reconciliation Strategy
### 3.1 Async Cost Adjustments
The reviewed Helicone path computes or finalizes usage/cost after the provider response is available, but still inside the asynchronous gateway logging and post-processing path.
The observed pattern is not "write a minimal Usage Record now and later reconcile from provider billing exports." It is closer to:
- Capture provider response and stream output.
- Extract usage/cost signals in the worker where possible.
- Re-parse or normalize usage/cost in ingestion where possible.
- Persist the resulting analytic usage/cost record.
- Perform wallet or rate-limit finalization after response post-processing.
Evidence:
- Worker extracts usage/model/cost signals from response material when possible: `worker/src/lib/dbLogger/DBLoggable.ts:804-836`.
- Worker sends fallback usage/model material to ingestion: `worker/src/lib/dbLogger/DBLoggable.ts:931-1032`.
- Ingestion populates usage and cost from processed response or worker-provided fallback: `valhalla/jawn/src/lib/handlers/ResponseBodyHandler.ts:117-170`.
- Ingestion has provider/body-aware parsing and cost breakdown behavior: `valhalla/jawn/src/lib/handlers/ResponseBodyHandler.ts:172-220`.
- Wallet/rate-limit finalization occurs after response processing: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:545-741`.
- Wallet finalization path includes a spend-sync hook, but the reviewed implementation does not show an active external reconciliation loop: `worker/src/lib/managers/WalletManager.ts:117-153`; `worker/src/lib/managers/WalletManager.ts:274-279`.
### 3.2 Streaming Late-Arriving Usage
Helicone's streaming pattern is to wait for the intercepted stream to finish, cancel, or time out, then log what was captured.
This is a late-in-the-request reconciliation pattern, not an external out-of-band provider reconciliation pattern:
- Final usage can arrive near stream end and be included when the stream completes normally.
- On cancellation, the captured stream is treated as incomplete and adjusted before parsing.
- On timeout, partial body material and a timeout terminal reason are retained.
- If final usage arrives only after the logging continuation has completed, no general late-provider update mechanism was identified in this pass.
Evidence:
- Stream terminal reason is captured by the interceptor: `worker/src/lib/util/ReadableInterceptor.ts:37-54`.
- Stream chunks are accumulated during client delivery: `worker/src/lib/util/ReadableInterceptor.ts:56-63`.
- Cancel and timeout outcomes are explicitly represented: `worker/src/lib/util/ReadableInterceptor.ts:94-143`.
- Cancelled stream parsing drops incomplete trailing material before processing: `worker/src/lib/dbLogger/DBLoggable.ts:317-320`; `valhalla/jawn/src/lib/handlers/ResponseBodyHandler.ts:342-350`.
- Terminal reason affects observability status semantics: `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40`.
### 3.3 Backfill vs Reconciliation
Helicone contains a historical backfill-style consumer path that can seek by timestamp and filter a subset of events, but this should not be treated as a productized provider-usage reconciliation system.
Behavioral distinction:
- Backfill can reprocess selected queued/logged events for operational repair or migration.
- Reconciliation would require a durable pending state, a later source-of-truth usage signal, deterministic conflict rules, and an audit trail.
- This pass found the former pattern, not a complete latter pattern.
Evidence:
- Backfill consumer entry point: `valhalla/jawn/src/workers/kafkaConsumer.ts:24-33`.
- Kafka consumer has timestamp-seek and filtering options: `valhalla/jawn/src/lib/clients/kafkaConsumers/KafkaConsumer.ts:20-42`; `valhalla/jawn/src/lib/clients/kafkaConsumers/KafkaConsumer.ts:82-101`; `valhalla/jawn/src/lib/clients/kafkaConsumers/KafkaConsumer.ts:147-159`.
- HUAKAI prior synthesis requires an explicit pending reconciliation flag for inferred usage: `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`.
### 3.4 Handling Missing Usage or Cost
Helicone has guard behavior when successful requests do not yield computable cost in certain non-streaming conditions, while streaming is treated differently because usage can be incomplete or delayed until stream completion.
HUAKAI implication:
- Do not silently accept "unknown cost" as final.
- Persist source quality and reconciliation state.
- Distinguish reported, normalized, inferred, and partial usage.
- Make late reconciliation a first-class lifecycle, not an implicit best-effort repair.
Evidence:
- Non-streaming successful requests with missing cost can trigger a protective disallow-list behavior: `worker/src/lib/managers/WalletManager.ts:193-221`.
- Streaming path is handled through stream capture and terminal reason rather than the same non-streaming guard: `worker/src/lib/util/ReadableInterceptor.ts:37-143`; `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40`.
- HUAKAI prior streaming decomposition already calls for usage source labels and later reconciliation for partial usage: `docs/decompositions/sub2api/streaming-forwarder.md:57-68`.
## 4. Tenant Isolation in the Observability Layer
### 4.1 Ingestion-Time Tenant Context
Helicone authenticates observability messages and resolves tenant context before durable logging. The handler chain carries that context into later body processing, cost computation, and analytic record construction.
Evidence:
- Authentication handler resolves tenant context before later handlers run: `valhalla/jawn/src/lib/handlers/AuthenticationHandler.ts:12-80`.
- The ingestion manager orders authentication before request/response processing and durable logging: `valhalla/jawn/src/managers/LogManager.ts:71-118`.
- Analytic record construction includes tenant context from the handler state: `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:500-555`.
### 4.2 Query-Time Tenant Filtering
Operator-facing read paths are tenant-scoped. The web handler wrapper loads authenticated user and tenant context before calling API logic, and request query builders apply tenant-aware filtering.
Evidence:
- Web API wrapper injects authenticated tenant context into handlers: `web/lib/api/handlerWrappers.ts:85-130`.
- Request-list query path builds filters with tenant context before querying: `valhalla/jawn/src/lib/stores/request/request.ts:54-78`.
- Analytic request-list query path also builds tenant-aware filters before querying the analytic store: `valhalla/jawn/src/lib/stores/request/request.ts:151-169`.
- The analytic query itself constrains results to the tenant context: `valhalla/jawn/src/lib/stores/request/request.ts:205-229`.
### 4.3 Object-Storage Tenant Partitioning
Object-storage keys for request/response body material are tenant-scoped. This does not replace authorization checks, but it reduces accidental cross-tenant mixing at the storage-key level.
Evidence:
- Request/response body storage keys include tenant and request scope: `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353`.
### 4.4 Controlled Analytic Query Context
Helicone has a tenant-aware analytic query wrapper that supplies tenant context through database settings and rejects user-supplied attempts to override the tenant placeholder. This is important because operator analytics often allow flexible filters or custom queries.
Evidence:
- Tenant context is supplied by the server-side wrapper and user-provided tenant override markers are rejected: `valhalla/jawn/src/lib/db/ClickhouseWrapper.ts:115-170`.
HUAKAI implications:
- Tenant identity must be injected by trusted server code, not accepted from operator query text.
- Observability APIs should apply tenant constraints in both query builders and lower-level analytic wrappers.
- Object-storage keys should be tenant-scoped, but authorization must still be enforced before signed access.
- Internal backfill, replay, and DLQ tooling must carry tenant identity and audit actor identity.
## 5. Sub2API vs Helicone Comparison Matrix
| Dimension | Sub2API observed / current HUAKAI baseline | Helicone observed behavior | Better HUAKAI decision | Evidence |
|---|---|---|---|---|
| Write mode | Assigned baseline: detached-context best-effort async write after billing settlement; prior HUAKAI synthesis flags this as still needing second-source verification. | Async gateway logging into queue/fallback path, then ingestion service persists analytic records. | Use async observability ingestion for latency, but make billing-grade Usage Record insertion part of HUAKAI Tx2. | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`; `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:241-243`; `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-543`; `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:282-317` |
| Atomicity with upstream response | Baseline says Usage Record write is detached and best-effort. | Not atomic with upstream response; logging is scheduled after response preparation. | Do not make operator analytics block response, but do make money/quota Usage Record atomic with billing settlement. | `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`; `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105` |
| Primary durable record | HUAKAI target is a Usage Record inside Tx2, linked to billing ledger. | Operator-visible usage is anchored in analytic request/response persistence. | Split accounting Usage Record from observability analytics, then link them by stable request identity. | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`; `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:476-587`; `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-35` |
| Hot/cold or multi-tier storage | Sub2API decomposition focuses on streaming usage; body tiering is not the highlighted pattern. | Multi-tier: analytic request/usage store plus object-storage body tier. | Adopt multi-tier observability storage, but keep the billing Usage Record independent of body retention. | `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-317`; `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353` |
| Queue durability | Baseline best-effort detached write risks loss if process fails before write. | Queue-backed ingestion with retries and DLQ paths; still vulnerable before queue accept. | Put a durable ingress event or Tx2 Usage Record before any non-recoverable accounting state is considered complete. | `worker/src/lib/clients/producers/KafkaProducerImpl.ts:29-58`; `worker/src/lib/clients/producers/SQSProducer.ts:39-66`; `valhalla/jawn/src/managers/LogManager.ts:143-205`; `valhalla/jawn/src/managers/LogManager.ts:271-334` |
| Real-time operator view | Not established by Sub2API baseline. | Live dashboard and request pages use short polling, not push streaming. | Use polling first for F-OBS-001; add push later only if operator latency requirements justify it. | `web/services/hooks/useJawnMetrics.ts:21-377`; `web/services/hooks/requests.tsx:97-123`; `web/services/hooks/requests.tsx:213-233` |
| Streaming final usage | Sub2API prior decomposition tracks streaming usage but lacks a formal multi-source reconciliation rule. | Stream body is intercepted until done, cancel, or timeout; final usage is parsed from captured material when available. | Keep streaming accumulator, add explicit source precedence and terminal-state rules. | `docs/decompositions/sub2api/streaming-forwarder.md:57-68`; `worker/src/lib/util/ReadableInterceptor.ts:37-143`; `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40` |
| Late provider usage | HUAKAI prior synthesis calls for pending reconciliation when usage is inferred. | No complete provider-late reconciliation lifecycle identified; backfill exists but is not the same thing. | Implement a first-class reconciliation state and job; do not rely on replay/backfill as the product contract. | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:43`; `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`; `valhalla/jawn/src/workers/kafkaConsumer.ts:24-33`; `worker/src/lib/managers/WalletManager.ts:274-279` |
| Crash before queue/write | Best-effort detached write can lose the Usage Record. | Async continuation can lose observability if runtime fails before queue delivery. | Billing-grade Usage Record must be created or reserved in a durable transaction; async analytics may be replayed. | `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-543`; `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105` |
| Crash after queue accept | Baseline unclear. | Queue-backed path can retry or dead-letter, depending on backend and consumer behavior. | Require queue ack only after durable commit; define replay and DLQ operator workflow. | `valhalla/jawn/src/lib/clients/sqsConsumers/sqsConsumers.ts:50-109`; `valhalla/jawn/src/managers/LogManager.ts:271-334` |
| Duplicate delivery / replay | HUAKAI synthesis requires stable request identity and claim idempotency. | Request identity is central, but no complete exactly-once contract was identified in this pass. | Enforce idempotency at Usage Record, billing ledger, and attachment layers. | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:35-36`; `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-110` |
| Tenant isolation | HUAKAI must isolate by tenant across ledger, quota, ops, and observability. | Authentication resolves tenant context; writes, reads, object storage, and analytic query wrapper are tenant-scoped. | Adopt defense-in-depth tenant scoping at auth, query-builder, storage-key, and analytic-wrapper layers. | `valhalla/jawn/src/lib/handlers/AuthenticationHandler.ts:12-80`; `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:500-555`; `web/lib/api/handlerWrappers.ts:85-130`; `valhalla/jawn/src/lib/db/ClickhouseWrapper.ts:115-170` |
| Body retention and privacy | Not central in the Sub2API baseline. | Body material may be omitted, stored inline, or stored in object storage. | Make body retention policy explicit per tenant/project and keep Usage Record valid without body content. | `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:641-678`; `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353` |
| Cost computation | HUAKAI target computes actual cost from final usage in Tx2. | Worker and ingestion can both derive usage/cost; wallet/rate-limit finalization runs after response. | Preserve multiple cost signal sources, but final accounting must have one authoritative settlement path. | `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`; `worker/src/lib/dbLogger/DBLoggable.ts:804-836`; `valhalla/jawn/src/lib/handlers/ResponseBodyHandler.ts:117-220`; `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:545-741` |
| Missing cost handling | Sub2API prior notes incomplete usage risk and recommends source labels. | Certain successful non-streaming missing-cost cases trigger protective behavior; streaming is handled separately. | Record missing/inferred/partial usage explicitly, block unsafe repeated free usage, and reconcile later. | `docs/decompositions/sub2api/streaming-forwarder.md:53-68`; `worker/src/lib/managers/WalletManager.ts:193-221` |
| External integrations | Not part of the baseline Usage Record path. | Core durable logging is awaited; several downstream integrations are best-effort. | Keep external exports best-effort and never let them define billing truth. | `valhalla/jawn/src/managers/LogManager.ts:220-229` |
## 6. KEEP / IMPROVE / AVOID for HUAKAI Usage Record Design
### 6.1 KEEP
KEEP: Decouple operator analytics ingestion from the caller-visible response path.
- Helicone protects response latency by scheduling observability logging asynchronously.
- HUAKAI should keep this for dashboards, traces, and large body persistence.
- It must not be the only path for billing-grade Usage Record durability.
Evidence:
- `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`
- `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-543`
KEEP: Use a durable queue plus dead-letter path between gateway and analytic ingestion.
- A queue gives better recoverability than direct fire-and-forget database writes.
- Dead-letter handling is necessary for malformed messages, transient store failures, and replay.
Evidence:
- `worker/src/lib/clients/producers/KafkaProducerImpl.ts:29-58`
- `worker/src/lib/clients/producers/SQSProducer.ts:39-66`
- `valhalla/jawn/src/managers/LogManager.ts:143-205`
- `valhalla/jawn/src/managers/LogManager.ts:271-334`
KEEP: Split analytic metadata from large body retention.
- Usage, cost, latency, status, route, and tenant dimensions should remain queryable even when request/response bodies are omitted or moved to object storage.
- This keeps dashboards fast and lowers storage pressure.
Evidence:
- `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:159-317`
- `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:641-678`
KEEP: Capture stream terminal reason.
- Done, cancel, and timeout are materially different for billing, support, and operator triage.
- HUAKAI should preserve terminal reason and partial-usage semantics.
Evidence:
- `worker/src/lib/util/ReadableInterceptor.ts:37-143`
- `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40`
KEEP: Use polling for first-version live dashboards.
- Helicone's "live" operator mode is short-interval query refetching.
- Polling is simpler to secure, simpler to scale initially, and compatible with analytic stores.
Evidence:
- `web/services/hooks/useJawnMetrics.ts:21-377`
- `web/services/hooks/requests.tsx:97-123`
- `web/services/hooks/requests.tsx:213-233`
KEEP: Apply tenant context at multiple layers.
- Tenant context appears in ingestion, durable record construction, query filtering, object-storage keying, and controlled analytic query context.
- HUAKAI should treat observability data as tenant-sensitive production data.
Evidence:
- `valhalla/jawn/src/lib/handlers/AuthenticationHandler.ts:12-80`
- `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:500-555`
- `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353`
- `valhalla/jawn/src/lib/db/ClickhouseWrapper.ts:115-170`
### 6.2 IMPROVE
IMPROVE: Make billing-grade Usage Record insertion atomic with HUAKAI's reconcile transaction.
- Helicone's observability write is not atomic with the client response.
- That is acceptable for dashboards, but not enough for quota, balance, or billing correctness.
- HUAKAI's Tx2 should insert the Usage Record, billing ledger entry, and final claim state together.
Evidence:
- Helicone async response boundary: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`
- HUAKAI target Tx2: `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`
IMPROVE: Add explicit usage-source and reconciliation lifecycle.
- Helicone can parse usage at multiple points, but this pass did not identify a complete durable late-reconciliation state machine.
- HUAKAI should encode reported, normalized, inferred, partial, and reconciled states.
- Inferred or partial records should carry pending reconciliation until a later authoritative source confirms or corrects them.
Evidence:
- Multiple Helicone usage extraction points: `worker/src/lib/dbLogger/DBLoggable.ts:804-836`; `valhalla/jawn/src/lib/handlers/ResponseBodyHandler.ts:117-220`
- HUAKAI prior requirement: `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:43`; `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`
IMPROVE: Define source precedence for streaming usage.
- Sub2API prior decomposition explicitly notes no multi-source usage reconciliation rule.
- Helicone waits for captured stream completion but does not supply HUAKAI's full precedence policy.
- HUAKAI should define deterministic precedence across provider-reported terminal usage, normalized event accumulation, tokenizer inference, and partial drain.
Evidence:
- Sub2API gap: `docs/decompositions/sub2api/streaming-forwarder.md:57-68`
- Helicone stream capture: `worker/src/lib/util/ReadableInterceptor.ts:37-143`
IMPROVE: Add a crash-resistant pre-log marker or durable accounting reservation.
- Helicone can lose analytics if the runtime fails before queue delivery.
- HUAKAI should not depend on an asynchronous continuation for the first durable accounting fact.
- The claim gate and Tx2 design already point in the safer direction.
Evidence:
- Helicone async continuation: `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:437-543`
- HUAKAI claim gate: `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-108`
IMPROVE: Require replay-safe idempotency across queue, analytic store, Usage Record, billing ledger, and body attachments.
- Helicone has request identity throughout the path, but this pass did not identify a complete exactly-once product contract.
- HUAKAI should define duplicate handling explicitly before production launch.
Evidence:
- Helicone identity-centered storage/read paths: `valhalla/jawn/src/lib/stores/request/VersionedRequestStore.ts:22-110`
- HUAKAI stable identity concern: `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:35-36`
IMPROVE: Add operator DLQ replay and reconciliation UI requirements to F-OPS/F-OBS.
- Queue and DLQ are only useful operationally if operators can inspect, retry, suppress, or escalate failed observability events.
- F-OBS-001 should reference operator workflows for failed usage persistence.
Evidence:
- DLQ behavior: `valhalla/jawn/src/managers/LogManager.ts:143-205`; `valhalla/jawn/src/managers/LogManager.ts:271-334`
### 6.3 AVOID
AVOID: Treating best-effort analytics as billing truth.
- Helicone's design is strong for observability latency and operator analytics.
- HUAKAI should not copy the same atomicity boundary for quota, balance, billing, or ledger correctness.
Evidence:
- `worker/src/lib/HeliconeProxyRequest/ProxyForwarder.ts:363-460`
- `docs/decompositions/_cross-cutting/quota-billing-claim-gate-synthesis.md:95-105`
AVOID: A no-op or undefined reconciliation hook.
- A named spend-sync or backfill path is not the same as a complete reconciliation lifecycle.
- HUAKAI must define triggering, source-of-truth, conflict resolution, audit events, and operator visibility.
Evidence:
- `worker/src/lib/managers/WalletManager.ts:117-153`
- `worker/src/lib/managers/WalletManager.ts:274-279`
- `valhalla/jawn/src/workers/kafkaConsumer.ts:24-33`
AVOID: Letting streaming timeout/cancel collapse into generic success or generic failure.
- Stream terminal reason directly affects billing confidence and support triage.
- HUAKAI should never flatten done, cancel, timeout, provider error, and client disconnect into one status.
Evidence:
- `worker/src/lib/util/ReadableInterceptor.ts:94-143`
- `worker/src/lib/HeliconeProxyRequest/ProxyRequestHandler.ts:26-40`
AVOID: Allowing operator query text to determine tenant scope.
- Tenant context must be supplied by trusted server state.
- User-controlled query or filter material must not override tenant scope.
Evidence:
- `web/lib/api/handlerWrappers.ts:85-130`
- `valhalla/jawn/src/lib/db/ClickhouseWrapper.ts:115-170`
AVOID: Making request/response body retention mandatory for Usage Record validity.
- Bodies may be large, sensitive, omitted, or moved to colder storage.
- Usage, cost, and billing records must remain valid without body payloads.
Evidence:
- `valhalla/jawn/src/lib/handlers/LoggingHandler.ts:641-678`
- `valhalla/jawn/src/lib/shared/db/s3Client.ts:348-353`
## 7. License-Discipline Check
Result: No GPL implementation material is introduced by this decomposition.
Controls applied:
- No Helicone source code is copied.
- No SQL text is copied.
- No upstream schema definitions are copied.
- No upstream request/response API shapes are copied.
- No distinctive comments are copied.
- No UI source, styling, or component implementation is copied.
- No upstream tests or fixtures are copied.
- File and line references are used only as audit citations for specifier-lane traceability.
- All recommendations are expressed in HUAKAI vocabulary: Usage Record, Tx2, billing ledger, claim gate, tenant isolation, pending reconciliation, operator dashboard, DLQ replay.
Clean-room implementation rule:
- Implementer-lane agents may use the KEEP / IMPROVE / AVOID guidance above.
- Implementer-lane agents must not open the GPL-3.0 cited source files.
- If a future implementation needs a concrete algorithm, it must be designed from HUAKAI requirements and MIT-compatible materials, not from Helicone source structure.
Feature-preservation check:
- No feature is dropped because of license risk.
- Risky reference behavior is converted into HUAKAI-safe equivalents:
  - Async analytics ingestion remains allowed.
  - Billing-grade Usage Record persistence is strengthened into Tx2.
  - Stream terminal handling becomes a HUAKAI status taxonomy.
  - Late usage becomes explicit pending reconciliation.
  - Queue failure becomes DLQ replay and operator workflow.
Security and privacy check:
- Observability data is tenant-sensitive.
- Body retention must be policy-controlled and optional.
- Tenant scope must be injected by trusted server context.
- Analytics query flexibility must not bypass tenant isolation.
- DLQ and replay tooling must be audited because failed records can contain usage, routing, and tenant-sensitive metadata.
## 8. Chinese Summary for Owner
本次 Helicone 二次源核验显示：Helicone 的强项是低延迟观测链路，而不是把 Usage Record 当作计费原子事实来写入。它把网关响应和观测入库解耦，通过异步 continuation、队列、Jawn ingest、ClickHouse 风格分析存储和对象存储分层来服务运营查询；Dashboard 的“实时”主要是短轮询，不是推送流。这个设计适合 F-OBS-001 的运营可观测性部分，尤其是队列、DLQ、冷热分层、流式终止原因、租户隔离和轮询 dashboard。
HUAKAI 不应照搬 Helicone 的原子性边界。F-OBS-001 的 Usage Record 如果参与余额、quota、账单和审计，必须继续采用 HUAKAI 的 Tx2：最终用量、Usage Record、Billing Ledger、claim commit、Audit Event 一起落库。Helicone 没有在本次核验中体现完整的外部迟到用量 reconciliation 生命周期；HUAKAI 应显式加入 `pending_reconciliation`、用量来源、流式 partial 状态、DLQ replay 和运营修复工作流。未发现本文件引入 GPL 代码、字段、SQL、API shape 或实现结构；它只保留行为摘要和 file:line 追溯证据。
