# 2026-04-30 Five-Axis Mechanism Question Matrix - Codex

| Field | Value |
| --- | --- |
| Status | Independent Codex pre-specifier scoping |
| Lane | pre-specifier scoping |
| Prior lanes on this artifact | none |
| Reference source read | none |
| Claude isolation | Did not read `docs/decompositions/_mechanism_questions/2026-04-30-five-axes-claude.md` |
| Projects covered | sub2api / one-api / new-api / portkey / helicone / litellm / envoy-ai-gateway |
| Exclusion | all-api-hub excluded per RB-4 except as "what not to do" background; no question rows |

## 1. 上下文状态 / Conversation Context State

### sub2api

Q-CTX-S2A-01. Does a conversation/session affinity binding persist beyond a single request process, with an explicit TTL and refresh-on-hit contract? Answer type: yes/no + TTL unit + refresh condition enum.

-> HUAKAI impact: informs `sticky_bindings`, `SelectionRequest.SessionHash`, and F-SESSION-001/F-POOL-001 sticky lifecycle tests.

### one-api

Q-CTX-OAI-01. Does channel selection carry any client conversation/session affinity, or is every channel choice stateless per request? Answer type: enum {none, API-key scoped, user scoped, session scoped, other}.

-> HUAKAI impact: decides whether one-api contributes to `backend/internal/pool` sticky behavior or only to non-sticky routing baselines.

### new-api

Q-CTX-NAI-01. Is the pricing/cache/reasoning policy version pinned in a per-request context before upstream dispatch? Answer type: yes/no + list of pinned policy classes.

-> HUAKAI impact: validates `billing_ledger_claims.billing_policy_version`, `protocol_policy_versions`, and Tx1/TX2 replay correctness in F-OBS-001.

### portkey

Q-CTX-PK-01. Is streaming transformer state guaranteed to be per-request and discarded on terminal event, upstream error, and client disconnect? Answer type: yes/no + terminal cleanup event enum.

-> HUAKAI impact: informs `backend/internal/proto` stream-state objects and `backend/internal/gateway` cleanup tests for no cross-request leakage.

### helicone

Q-CTX-HLC-01. When routing configuration changes during traffic, are in-flight requests pinned to the selected config version? Answer type: yes/no + version source enum {none, in-memory generation, durable row, external control plane}.

-> HUAKAI impact: informs `routes.routing_policy_version`, `routing_reason.scoring_policy_version`, and config reload semantics.

### litellm

Q-CTX-LM-01. Does retry/fallback state travel as a bounded per-request attempt context rather than global mutable state? Answer type: yes/no + fields contract {attempt_count, excluded_targets, last_error_class, budget_remaining}.

-> HUAKAI impact: informs `backend/internal/router.RoutePlan`, attempt exclusion, and F-GW-004 retry budget schema.

### envoy-ai-gateway

Q-CTX-EAG-01. Is model/body-derived routing metadata protected from client header spoofing before route matching? Answer type: yes/no + trust-boundary contract.

-> HUAKAI impact: informs `backend/internal/router`, F-SEC-005 header firewall, and API-key-derived tenant/model context rules.

## 2. 渠道调度 / Channel And Account Scheduling

### sub2api

Q-SCH-S2A-01. Does account scheduling have separate wait budgets for sticky reuse versus fresh/fallback selection? Answer type: yes/no + max-waiting/max-duration numeric pair per path.

-> HUAKAI impact: informs `pool_groups.sticky_wait_*`, `fallback_wait_*`, and `backend/internal/pool/db_slot_manager.go` wait-plan behavior.

### one-api

Q-SCH-OAI-01. In multi-replica deployments, is scheduled channel probing cluster-coordinated so only one runner mutates a channel at a time? Answer type: yes/no + coordination primitive enum.

-> HUAKAI impact: informs F-CH-002 health-probe worker design, PostgreSQL advisory lock use, and `rate_limit_audit_events`/pool audit ordering.

### new-api

Q-SCH-NAI-01. Are channel/account health scheduling decisions isolated from user balance/quota scheduling decisions? Answer type: yes/no + state-owner enum {channel, user, token, account, mixed}.

-> HUAKAI impact: informs separation between `provider_accounts.health_state`, quota status, `billing_ledger_claims`, and operator recovery UI.

### portkey

Q-SCH-PK-01. Does retry scheduling maintain a per-request failed-target exclusion set and a separate cross-request cooldown state? Answer type: yes/no for each + cooldown state scope enum.

-> HUAKAI impact: informs `routing_reason.per_request_exclusion_summary`, F-RATE-001 cooldown state, and F-GW-004 retry orchestration.

### helicone

Q-SCH-HLC-01. Are load-balancing signals shared across gateway replicas or process-local only? Answer type: enum {process-local, shared-cache, durable-store, external-control-plane}.

-> HUAKAI impact: informs whether F-ROUTE-001 metrics must be stored in PostgreSQL/Redis-like shared state before routing can use them.

### litellm

Q-SCH-LM-01. Is cooldown state shared across replicas by default, optional, or never shared? Answer type: enum {default-shared, optional-shared, process-local-only}.

-> HUAKAI impact: informs `provider_accounts.health_state_until`, F-RATE-001 distributed cooldown tests, and Personal Edition defaults.

### envoy-ai-gateway

Q-SCH-EAG-01. Does endpoint picking define a deterministic precedence between priority, weight, health, queue depth, and fallback policy? Answer type: ordered contract list or "not specified".

-> HUAKAI impact: informs SaaS Phase 10 `F-ROUTE-002` projection from HUAKAI Pool policy into Kubernetes/control-plane resources.

## 3. 协议转换 / Protocol Translation

### sub2api

Q-PROTO-S2A-01. During streaming conversion, is an open message/tool item state explicitly closed before final client emission? Answer type: yes/no + closure trigger enum.

-> HUAKAI impact: informs HCSF event grammar in `backend/internal/proto/hcsf.go` and AT-PROTO streaming closure tests.

### one-api

Q-PROTO-OAI-01. Is provider protocol handling adapter-specific, or mostly OpenAI-compatible pass-through with thin provider differences? Answer type: enum {adapter-per-provider, pass-through-majority, mixed}.

-> HUAKAI impact: informs adapter registry scope in `backend/pkg/adapter` and the minimum provider set for Phase 4.

### new-api

Q-PROTO-NAI-01. When reasoning effort is encoded in more than one request location, is precedence deterministic? Answer type: yes/no + ordered precedence contract.

-> HUAKAI impact: informs request normalization in `backend/internal/proto` and API contract validation for reasoning controls.

### portkey

Q-PROTO-PK-01. For each supported stream family, is there an explicit terminal-event contract that prevents early false termination? Answer type: yes/no + per-family terminal enum.

-> HUAKAI impact: informs `backend/internal/gateway` end-class taxonomy and provider-specific SSE tests.

### helicone

Q-PROTO-HLC-01. Does the conversion layer emit machine-readable loss/unsupported capability metadata to the caller or logs? Answer type: yes/no + destination enum {response header, response body, log, metrics, none}.

-> HUAKAI impact: informs `protocol_capability_matrix`, `usage_records.protocol_loss`, and operator-visible protocol warnings.

### litellm

Q-PROTO-LM-01. Do provider adapters expose unsupported/lossy capability verdicts before dispatch, or do they rely on provider error responses after dispatch? Answer type: enum {preflight, post-error, mixed, none}.

-> HUAKAI impact: informs F-PROTO-002 safe-equivalent gating and `backend/internal/proto/capability_matrix.go`.

### envoy-ai-gateway

Q-PROTO-EAG-01. Are header/body mutation rules allowed to change the model or cost metadata after route matching? Answer type: yes/no + mutation phase enum {pre-match, post-match-pre-cost, post-cost, not allowed}.

-> HUAKAI impact: informs F-PROTO-002/F-SEC-005 boundary rules and prevents billing keys from being derived from mutable request data.

## 4. 计费补偿 / Billing Compensation

### sub2api

Q-BILL-S2A-01. Are post-settlement corrections represented as append-only adjustment records rather than mutating the original settled claim? Answer type: yes/no + adjustment type enum.

-> HUAKAI impact: informs `billing_ledger_adjustments`, reconciliation events, and dispute/refund operator workflows.

### one-api

Q-BILL-OAI-01. Does duplicate-billing prevention bind all retries of one logical request to one idempotency key? Answer type: yes/no + idempotency key scope enum.

-> HUAKAI impact: informs `billing_ledger_claims.idempotency_key`, `attempt_seq`, and F-OBS-001 retry/abort tests.

### new-api

Q-BILL-NAI-01. Are cache creation tokens split into distinct 5-minute and 1-hour billing buckets at persistence time? Answer type: yes/no + bucket enum.

-> HUAKAI impact: informs `usage_records` token columns, pricing formulas, and cache-billing acceptance tests.

### portkey

Q-BILL-PK-01. If a hook or cache layer modifies the response body, is usage/cost calculated from the upstream original, the modified client response, or a separate metered record? Answer type: enum {upstream_original, client_modified, separate_metered_record, not billed}.

-> HUAKAI impact: informs `usage_source`, cache-hit streaming behavior, and plugin/hook boundaries in `backend/internal/gateway`.

### helicone

Q-BILL-HLC-01. Are cost limits enforced by pre-call reservation, post-call accounting, or shadow-only observation? Answer type: enum {pre-call-reserve, post-call-counter, shadow-only, none}.

-> HUAKAI impact: informs F-OBS-001 ClaimGate, `billing_pricing_versions`, and cost-aware routing safety.

### litellm

Q-BILL-LM-01. When a partial streaming attempt fails and fallback succeeds, is billing based on the failed attempt, the fallback attempt, both attempts, or a merged usage vector? Answer type: enum {failed-only, fallback-only, both, merged, no-charge}.

-> HUAKAI impact: informs Tx2 `attempt_seq`, partial usage settlement, and F-GW-002 replay-safe retry rules.

### envoy-ai-gateway

Q-BILL-EAG-01. Does quota shadow mode emit auditable cost/quota metadata without enforcing rejection? Answer type: yes/no + metadata destination enum.

-> HUAKAI impact: informs rate/quota dry-run mode, `billing_events`, and SaaS operator rollout controls.

## 5. 异步任务 / Async Background Workers

### sub2api

Q-ASYNC-S2A-01. Do health probes, rollups, and cache invalidations use durable watermarks/idempotency keys? Answer type: yes/no per worker class + watermark key contract.

-> HUAKAI impact: informs `scheduler_outbox`, `DeleteExpiredStickyBindings`, orphan sweep, and F-OBS-001 worker recovery tests.

### one-api

Q-ASYNC-OAI-01. Are channel-test logs and low-balance notifications durably retried after async write failure? Answer type: yes/no + retry persistence enum {none, in-memory, database, external-queue}.

-> HUAKAI impact: informs notification outbox, audit retention, and operator-visible health-probe history.

### new-api

Q-ASYNC-NAI-01. When billing expression or snapshot versions change, are historical records migrated, read-as-is, or recalculated lazily? Answer type: enum {migrated, read-as-is, lazy-recalculate, mixed}.

-> HUAKAI impact: informs `billing_pricing_versions`, `usage_record_reconciliation_events`, and versioned replay semantics.

### portkey

Q-ASYNC-PK-01. Are request/response hook pipelines awaited on the hot path with timeouts, or dispatched to background queues? Answer type: enum {hot-path-awaited, hot-path-timeboxed, background-queue, mixed}.

-> HUAKAI impact: informs plugin hook contract, forwarder latency budgets, and whether hook failure can affect billing settlement.

### helicone

Q-ASYNC-HLC-01. Are request logs/analytics writes buffered with replay/DLQ semantics, or best-effort fire-and-forget? Answer type: enum {synchronous, buffered-retry, durable-DLQ, fire-and-forget}.

-> HUAKAI impact: informs `usage_record_dlq`, `backend/internal/obs`, and analytics-vs-money-ledger separation.

### litellm

Q-ASYNC-LM-01. Are cooldown/logging callbacks awaited before retry decision completion? Answer type: yes/no + callback timeout numeric.

-> HUAKAI impact: informs F-RATE-001 audit emission path and prevents observability callbacks from extending user-visible retry latency.

### envoy-ai-gateway

Q-ASYNC-EAG-01. Does the control-plane reconciler expose a bounded staleness contract from config change to data-plane effect? Answer type: yes/no + max staleness metric enum {resource_version, generation, timestamp, none}.

-> HUAKAI impact: informs SaaS config projection workers, `protocol_policy_versions.effective_from`, and admin status conditions.

## Clean-Room Tail

Source files read: none

Lane: pre-specifier scoping

Agent: GPT-5 Codex (codex session)

UTC timestamp: 2026-04-30T07:00:34Z
