# Critic Review of litellm Cooldown handler + retry policy hierarchy

| Field | Value |
| --- | --- |
| Critic | Codex critic-lane |
| Date | 2026-04-29 |
| Source files read | https://github.com/BerriAI/litellm/blob/main/litellm/router.py ; https://github.com/BerriAI/litellm/blob/main/litellm/router_utils/cooldown_handlers.py ; https://github.com/BerriAI/litellm/blob/main/litellm/router_utils/cooldown_cache.py ; https://github.com/BerriAI/litellm/blob/main/litellm/router_utils/get_retry_from_policy.py ; https://github.com/BerriAI/litellm/blob/main/litellm/router_utils/fallback_event_handlers.py ; https://github.com/BerriAI/litellm/blob/main/litellm/types/router.py ; https://docs.litellm.ai/docs/routing ; https://docs.litellm.ai/docs/proxy/reliability ; https://github.com/BerriAI/litellm/issues/6011 ; https://github.com/BerriAI/litellm/issues/7669 ; https://github.com/BerriAI/litellm/issues/12503 |
| Companion specifier output | docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md |

## A. Coverage gaps (specifier likely missed these)
- C-001: Cooldown state is not just "deployment failed N times"; it is a mixed decision tree combining deployment availability, status-code classification, configured failure budget, per-error allowed-failure policy, per-minute counters, and single-deployment protection. Evidence: routing docs describe cooldowns as failure count per minute and a short table of triggers, while the source path for the cooldown handler includes separate branches for status classes, single-deployment traffic thresholds, and policy overrides.
- C-002: Multi-process behavior is split-brain unless the cache backend is treated as production-critical. Evidence: router initialization builds a dual local/distributed cache when a distributed backend is configured, but failure counters for allowed failures are also maintained through local router state. HUAKAI must test two gateway replicas receiving alternating failures against the same Channel, because one replica may not see the other's pre-cooldown failure count.
- C-003: Retry delay semantics are endpoint-path sensitive. Evidence: official routing docs promise exponential backoff for rate-limit errors and a minimum retry wait setting; public upstream issues report paths where retry delay was ignored or immediate retry occurred, especially under usage-based routing and proxy retry configuration.
- C-004: The retry policy hierarchy is wider than global policy versus model-group policy. Evidence: Router construction accepts gateway-level retry count, request-level retry count, global retry policy, model-group retry policy, provider response headers, and fallback recursion limits. A spec that only models "policy override order" will miss runtime behavior when fallback changes the model group mid-request.
- C-005: Failure accounting and cooldown insertion happen through callbacks and exception metadata, so missing deployment identity can silently suppress cooldown. Evidence: router failure callbacks recover deployment identity from request metadata/model info before invoking cooldown handling; if a provider path or streaming path loses that metadata, HUAKAI would keep routing to a failing Channel.
- C-006: Cooldown recovery is TTL expiry, not an active health-state reconciliation loop. Evidence: official docs say deployments are reintroduced after cooldown and describe reset behavior, while source evidence points to cached cooldown entries expiring and being omitted from later active-cooldown reads. HUAKAI needs a separate Health model if operators must know "recovered by probe" versus "cooldown expired without proof."
- C-007: Status-code taxonomy creates reliability blind spots. Evidence: upstream issue #12503 reports the same provider-side temporary outage being classified as non-retryable in one path and retryable in another. HUAKAI needs provider-error normalization before cooldown/retry decisions, not after.
- C-008: "Fallback to specific deployment ID skips cooldown check" is operationally dangerous. Evidence: official fallback docs explicitly describe a specific deployment fallback path that bypasses cooldown checking. HUAKAI must not allow an operator-specified Channel bypass to ignore tenant/channel health state without an explicit emergency override audit event.

## B. Flattering errors (looks simple, isn't)
- F-001: "Set allowed failures and cooldown time" looks like two knobs, but production behavior depends on traffic volume, model group cardinality, error type, distributed cache health, and whether the request path is sync, async, streaming, embedding, or routed by a specialized strategy.
- F-002: "Retry after rate limit" looks like normal backoff, but it depends on whether the provider error carries parseable headers, whether the error was normalized into a retryable class, whether the call entered Router retry or SDK retry, and whether fallback is configured to short-circuit retry.
- F-003: "Per-model-group retry policy" sounds deterministic, but fallback mutates the effective model group. HUAKAI must define whether retry budget is consumed per original Gateway Request, per attempted Channel, or per fallback target.
- F-004: "Cooldown removes bad deployments" hides a noisy-neighbor risk. A provider credential shared across tenants can poison a deployment for all tenants if health state is not scoped by tenant/account/key ownership.
- F-005: "Automatic recovery" hides an ops gap. TTL expiry may put traffic back on a still-broken Channel. HUAKAI needs operator-visible recovery mode: expired, probe-passed, manual-restored, or forced-enabled.

## C. Upstream's own drift
- D-001: Docs say generic errors retry immediately and rate-limit errors use exponential backoff; public bug reports show configured delay/retry-after not respected in some paths. Evidence: routing docs retry section versus upstream issues #6011 and #7669.
- D-002: Docs say cooldown is "failures in a minute before cooled down"; source behavior includes immediate/status-driven cooldown and high-failure-rate thresholds that do not reduce to a simple allowed-fails counter.
- D-003: Docs describe deployments recovering and failure counters resetting once healthy; source evidence supports TTL-based disappearance from the active cooldown list more clearly than active health confirmation.
- D-004: Docs say fallbacks cover all remaining errors, including rate limits; issue #12503 shows provider temporary errors can be mapped into a non-fallback class and fail the request instead.
- D-005: Docs state specific deployment fallback skips cooldown checks as a convenience; that contradicts the reliability story that cooled deployments are temporarily removed from the available pool.

## D. Things HUAKAI should NOT copy
- N-001: Do not copy local in-memory failure counting as an authoritative pre-cooldown gate. HUAKAI's DR-001 multi-tenant gateway needs PostgreSQL/Redis-scoped health state with tenant_id, account_id, channel_id, and deployment_id dimensions.
- N-002: Do not copy cooldown bypass for specific deployment fallback as default behavior. HUAKAI can support an audited Manual First / emergency override, but normal routing must fail closed against unhealthy Channels.
- N-003: Do not copy mixed SDK/proxy retry semantics. HUAKAI should have one Gateway Request retry budget, one Channel attempt budget, and explicit precedence for request override, tenant policy, route policy, and platform default.
- N-004: Do not copy status-code-only retryability. HUAKAI needs provider-normalized error reasons: quota exhausted, auth invalid, transient upstream, context too large, safety blocked, tenant budget blocked, and gateway overload.
- N-005: Do not copy TTL expiry as recovery proof. HUAKAI should store cooldown cause, first_seen, last_seen, expires_at, last_probe_at, recovery_source, and operator_actor where applicable.
- N-006: Do not copy global callback mutation as the health pipeline. HUAKAI should emit structured Gateway Attempt outcomes into a durable event path, then derive cooldown state from those events.
- N-007: Do not copy one default cooldown constant for every provider and failure mode. HUAKAI should make retry/cooldown windows policy-driven by tenant, provider, Channel class, error class, and edition capability under DR-002.

## E. Smells found
- S-001: Single point of failure: without a distributed cache, cooldown state is local to one gateway process. In a scaled HUAKAI deployment this would route traffic inconsistently across replicas.
- S-002: Hidden global state: Router setup registers callbacks into process-level callback lists. Multiple Router instances or tests can contaminate each other's health behavior unless cleanup is perfect.
- S-003: Inconsistent error taxonomy: retry/cooldown decisions depend on mapped exception class and status code; public issues show temporary provider failures can land in different classes.
- S-004: Magic constants without complete operator policy: default cooldown and failure thresholds are short and mostly environment/config driven, but not clearly tenant/channel scoped.
- S-005: Fail-open path: specific deployment fallback can skip cooldown checks. That is a reliability bypass and a tenant isolation risk if exposed to users or admins without guardrails.
- S-006: Tenant data leakage potential: health/cooldown keyed only by deployment identity can leak one tenant's provider/key failure into another tenant's routing if deployments share public model names or provider endpoints.
- S-007: Recovery ambiguity: TTL expiry makes a Channel eligible again without proving the failure cause disappeared.

## F. Synthesis recommendations
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: define the full retry policy hierarchy including request override, route policy, tenant policy, model-group policy, default retry count, provider retry-after headers, fallback depth, and SDK/proxy boundaries; document cooldown eligibility as a decision table covering status taxonomy, allowed-fail policy, failure percentage, single-deployment protection, missing deployment identity, disabled cooldowns, and specific-deployment fallback; include concurrency scenarios for two gateway replicas, streaming failure after partial output, fallback mutation of model group, Redis outage, and provider error misclassification.
- Top-3 HUAKAI-specific divergences this decomp must call out: HUAKAI cooldown state must be tenant/account/channel scoped and durable enough for DR-001 operations; HUAKAI must replace status-code-only taxonomy with provider-normalized Gateway Error classes; HUAKAI must make cooldown bypass and manual recovery explicit audited operations, not implicit fallback behavior.

## Owner Chinese summary (1 paragraph)
本次 critic-lane 独立阅读 litellm 的 Router、cooldown handler/cache、retry policy、fallback handler、router types、官方 routing/reliability 文档和相关公开 issue 后，结论是 specifier 必须重点补足“多副本并发下的冷却状态一致性、错误分类漂移、retry_after 实际生效路径、fallback 改变模型组后的重试预算、以及 TTL 到期不等于健康恢复”这些高风险点；最高优先级补测是两台 HUAKAI Gateway 交替打同一 Channel 失败、provider 429/401/400 临时故障分类、Redis 不可用、streaming 中途失败、specific Channel fallback 绕过 cooldown 的场景；该 decomp 若不补这些内容，会误导 implementer-lane 复制上游短期便利设计，因此阻塞下一 slice 直接引用，但不阻塞继续做 safe-equivalent 设计。
