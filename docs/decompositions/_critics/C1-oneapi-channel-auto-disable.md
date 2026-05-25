# Critic Review of one-api Channel auto-disable on permanent-error pattern

| Field | Value |
| --- | --- |
| Critic | Codex critic-lane |
| Date | 2026-04-29 |
| Source files read | MIT public source: https://github.com/songquanpeng/one-api, https://github.com/songquanpeng/one-api/blob/main/README.md, https://github.com/songquanpeng/one-api/blob/main/README.en.md, https://github.com/songquanpeng/one-api/blob/main/common/config/config.go, https://github.com/songquanpeng/one-api/blob/main/monitor/manage.go, https://github.com/songquanpeng/one-api/blob/main/monitor/channel.go, https://github.com/songquanpeng/one-api/blob/main/monitor/metric.go, https://github.com/songquanpeng/one-api/blob/main/controller/relay.go, https://github.com/songquanpeng/one-api/blob/main/controller/channel-test.go, https://github.com/songquanpeng/one-api/blob/main/model/channel.go; cross-checked through pkg.go.dev, GitExtract snapshot, and public mirror snippets where GitHub raw was unavailable. |
| Companion specifier output | docs/decompositions/one-api/channel-auto-disable-source-verified.md |

## A. Coverage gaps (specifier likely missed these)
- C-001: Permanent-error auto-disable is not a standalone always-on behavior. Source evidence shows a separate operator option defaults off, while metric-based disable is controlled by env flags. HUAKAI must model two gates: immediate permanent-error disable and rolling success-rate disable.
- C-002: The permanent-error classifier is narrow. It treats unauthorized status and a small OpenAI-shaped error taxonomy as disabling signals, but provider-specific quota, billing, organization-blocked, policy, region, and malformed-auth responses can fall through into generic failure metrics instead of immediate disable.
- C-003: Disable is launched from the relay error path asynchronously while retry selection continues. Other requests, and possibly the same retry flow via stale cache, can still select the channel before channel status propagation completes.
- C-004: A user/admin-specified channel path is not retried, but the same failure path can still mark the shared channel auto-disabled. In HUAKAI this is a tenant-scoped blast-radius problem: one tenant's deliberate channel pinning must not globally punish all tenants.
- C-005: Success-rate accounting can be skewed by retry. The first failed channel emits a failure event, but a later successful retry does not clearly credit the fallback channel as success in the same relay loop. The metric may over-count failed attempts and under-count successful recovery.
- C-006: Metric windows are in-process and volatile. The rolling success/failure store is a memory map, so restarts erase evidence and multi-node deployments split channel health by process.
- C-007: Metric event transport is bounded but emitted from per-request goroutines. If the success/failure queues fill, request goroutines can accumulate behind channel sends; this turns health tracking into a latent availability risk.
- C-008: Auto-disable writes channel status but the decomposition must also cover cache invalidation, ability/model availability, and operator UI state. Source evidence shows status update is separate from channel cache sync and model ability logic.
- C-009: Scheduled channel tests and live relay failures share disable/enable behavior but not identical inputs. Manual/periodic tests can enable previously disabled channels after one clean test; this needs a recovery-policy spec, not only a disable spec.
- C-010: Notification is best-effort operational side effect. Disabling does not depend on successful email/message delivery, so HUAKAI must require durable audit events separately from alert delivery.

## B. Flattering errors (looks simple, isn't)
- F-001: "Disable on permanent error" looks like a boolean rule, but production behavior is a decision chain: upstream error normalization, HTTP status, provider error code, retry eligibility, channel pinning, cache freshness, and async status mutation.
- F-002: "Success rate threshold" looks deterministic, but a small default sample window makes early traffic noisy. Low-volume channels can be disabled after a handful of transient failures, especially when retry recovery is counted asymmetrically.
- F-003: "Multi-machine deployment" is advertised, but health memory is per process. In a cluster, node A can disable from its local observations while node B keeps routing from stale cache until sync.
- F-004: "Auto recovery" through channel tests is not a full remediation workflow. A single test pass can be a false positive for provider account health, tenant quota, model-specific failures, regional blocks, or organization restrictions.
- F-005: "Permanent" error categories are provider-contract dependent. A 401 is often bad key, but it can also be caused by transient proxy/header rewriting or provider-side incident; HUAKAI needs quarantine and confirmation states, not immediate irreversible trust in one response.

## C. Upstream's own drift
- D-001: README.env documents success-rate metric disable, but the immediate permanent-error disable gate is not documented with the same clarity. Operators can enable metrics and still not get the expected immediate permanent-error disable behavior.
- D-002: README.en multi-machine deployment emphasizes shared database and sync, while source-level health metric state remains local memory. The docs imply cluster operation, but channel health evidence is not cluster-consistent.
- D-003: The release note language says automatic disable by error rate and points to metric env vars, while the implementation also has non-metric permanent-error disable and response-time disable paths. The public description compresses three distinct mechanisms into one operator mental model.
- D-004: README.en still says primary SQL should be MySQL, while current project descriptions and config snippets mention PostgreSQL elsewhere. For HUAKAI DR-006, cite behavior, not upstream database guidance.
- D-005: The source contains a race-condition note around relay error mutation after retry. Even if outside the disable function, it sits in the same error-handling path and undermines confidence in treating relay error state as cleanly serialized.

## D. Things HUAKAI should NOT copy
- N-001: Do not copy in-memory rolling health windows. HUAKAI should store channel health observations in PostgreSQL with tenant_id, channel_id, provider, model, status class, request_id, and decay window.
- N-002: Do not copy global channel disable without tenant blast-radius controls. HUAKAI needs tenant-scoped disable, global provider quarantine, and edition-aware policy per DR-001 and DR-002.
- N-003: Do not copy a small hard-coded provider error allowlist as the policy boundary. HUAKAI should define provider-normalized permanent, transient, quota, auth, billing, safety, policy, timeout, and transport categories.
- N-004: Do not copy async fire-and-forget status mutation without an idempotent state transition record. HUAKAI should use compare-and-set transitions, reason codes, actor/source, and recovery deadlines.
- N-005: Do not copy alert delivery as the only operator evidence. HUAKAI needs durable audit logs, Ops UI incident rows, notification retry state, and manual override history.
- N-006: Do not copy immediate re-enable after a generic successful health test. HUAKAI should require model-specific probes, tenant/account-specific probes where relevant, cooldown, and optionally Owner/operator approval for shared channels.
- N-007: Do not copy cache/eventual-sync assumptions for routing after disable. HUAKAI routing must fail closed against disabled/quarantined channel state, preferably via transactionally visible state or short-lived cache with invalidation.
- N-008: Do not copy direct channel pin behavior without guardrails. Tenant/admin pinning should be isolated and must not disable shared channels unless the failure is validated outside the pinned request context.

## E. Smells found
- S-001: Single point of failure / volatility: rolling metric history is one in-memory map per process with no persistence or cross-node merge.
- S-002: Hidden global state: channel health state, root notification target, and channel status mutation are global to the instance rather than tenant-scoped.
- S-003: Inconsistent error taxonomy: immediate disable uses a narrow OpenAI-shaped set, success-rate disable treats all non-immediate failures alike, and response-time disable is another separate rule.
- S-004: Magic constants without complete operator override: default metric queue size and success threshold are env-controlled, but fallback response-time sentinel and bounded event queue behavior are not an operator-grade policy model.
- S-005: Fail-open path: if automatic disable is off, permanent-looking errors become metric failures or logs; routing can continue through a known-bad channel.
- S-006: Tenant data leakage potential: notifications and logs include channel identifiers and failure reasons in a root-global channel model; HUAKAI must prevent cross-tenant exposure of provider/account failure details.
- S-007: Race/concurrency smell: relay error handling, retry, async disable, cache refresh, and metric emission are not a single ordered state machine.
- S-008: Operational recovery smell: disable and enable are symmetric-looking status writes, but disable reasons, cooldown, and validation evidence are not durable first-class state.

## F. Synthesis recommendations
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: separate immediate permanent-error disable, rolling success-rate disable, and response-time disable; specify cache/routing consistency after disable; specify retry accounting semantics so fallback success does not corrupt health metrics.
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: require normalized provider error taxonomy with permanent/transient/quota/auth/billing/policy/transport categories; require durable audit and idempotent state transitions; require recovery workflow with cooldown, model-specific probes, and manual override.
- Top-3 things specifier MUST address before this decomp can be cited by implementer-lane: define tenant blast radius for shared, tenant-owned, and pinned channels; define cluster behavior under DR-001 and DR-006; define alert failure behavior separately from state mutation.
- Top-3 HUAKAI-specific divergences this decomp must call out: PostgreSQL-backed channel health evidence and transition log, not process memory; tenant-scoped/channel-scope policy with edition-specific defaults; fail-closed routing against disabled/quarantined state with explicit stale-cache handling.
- Top-3 HUAKAI-specific divergences this decomp must call out: provider-normalized error taxonomy and configurable policy table, not source-level string matching; quarantine-before-disable for ambiguous 401/5xx/proxy failures; durable Ops UI incident lifecycle instead of email-only operator notification.
- Top-3 HUAKAI-specific divergences this decomp must call out: retry-aware health accounting, bounded event backpressure strategy, and safe recovery gates that avoid one successful test re-enabling a broadly broken provider account.

## Owner Chinese summary (1 paragraph)
本次 critic-lane 独立阅读 one-api 的 README、配置、relay、monitor、metric、channel test 和 channel status 相关源码证据后，认为 specifier 最容易低估的是：永久错误禁用并不是一个简单开关，而是与成功率统计、重试链、缓存同步、定时测试恢复、通知、全局渠道状态交织在一起；最高优先级补强是把“立即永久错误禁用 / 成功率禁用 / 响应时间禁用”拆开建模，并要求 PostgreSQL 持久化健康证据、租户级 blast-radius 控制、fail-closed 路由和可审计恢复流程；该问题如果不补，会阻塞 implementer-lane 直接引用该 decomp 进入下一 slice，因为 HUAKAI 的 DR-001 多租户、DR-002 双版本、DR-006 PostgreSQL 都不能接受上游这种进程内、全局、异步、弱恢复的健康状态设计。
