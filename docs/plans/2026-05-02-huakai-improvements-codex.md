# 2026-05-02 HUAKAI Improvements - Codex Independent Plan

| Owner directive | "读 ... 四份材料 ... 不要读 C:\\Users\\h\\.claude\\plans\\velvet-meandering-dusk.md ... 列 8-12 个 HUAKAI 具体功能提升点 ... 写到 docs/plans/2026-05-02-huakai-improvements-codex.md。不要复制 sub2api/Anthropic 的源码字段名。写完 git add + commit 作为 Codex 作者。" |
| --- | --- |
| Scope | 只做独立规划文档；不改后端、数据库 schema、OpenAPI、主 parity matrix。输入为 Owner 指定 4 份内部证据、同批 reference_delta 摘要、项目规则，以及官方供应商公开文档。 |
| Explicit non-read | 未读取 `C:\\Users\\h\\.claude\\plans\\velvet-meandering-dusk.md`。 |
| Success criteria | 产出 8-12 个提升点；每个包含开源基线、官方基线、算法/数据结构/状态机、操作员/客户可见信号、类型、优先级。 |
| Blast radius | 文档-only；最大风险是把 roadmap 优先级写偏或把 reference 行为误当官方要求。 |
| Failure modes + mitigation | 1. 过度 gateway 化：用 Account-to-API 主线校正。2. 过度创意化：每项必须绑定开源/官方基线。3. clean-room 风险：不读取新上游源码，不复制上游源码字段名或算法公式。 |
| Decision points | 是否把 P0/P1 项写入 `docs/03_FEATURE_PARITY_MATRIX.md`、是否开启 schema 计划、是否把 CLIProxyAPI 作为 Account-to-API 主参考进入下一轮 specifier。 |
| Pre-execution checklist | 已读项目规则、Owner 指定 4 份材料、reference_delta 8 仓摘要、CLIProxyAPI deep dive；官方策略在 2026-05-03 用官方站点核对。 |

## Evidence Baseline

### Internal Evidence Read

- `docs/reference_delta/2026-05-02/account-to-api-mainline-audit.md`: Account-to-API 主线缺口，尤其 local key -> binding -> account/pool -> credential lease -> adapter -> usage/billing/state。
- `docs/reference_delta/2026-05-02/huakai-creative-strengthening.md`: 10 条 0-reference 创新候选，明确区分 architectural hygiene 与真正 product moat。
- `reference_deep_dive/2026-05-02/sub2api/core-ops-deep-dive.md`: Sub2API 在 scheduler、payment lifecycle、channel monitor、usage write backpressure 上的核心运营精度。
- `reference_deep_dive/2026-05-02/cliproxy-api/account-to-api-deep-dive.md`: CLIProxyAPI 的 per-provider executor、credential store、routing enum、session-affinity extraction、retry knobs、remote-management/TUI/operator config。
- 同批 8 仓 delta 摘要：Sub2API、one-api、New API、LiteLLM、Portkey Gateway、Helicone、Envoy ai-gateway、All API Hub。CLIProxyAPI 作为第 9 个 Account-to-API 参考。

### Official Vendor Sources Checked 2026-05-03

- OpenAI Platform: Projects/Admin/API keys/rate limits/usage-cost/prompt caching/Responses API.
  - https://platform.openai.com/docs/api-reference/projects
  - https://platform.openai.com/docs/api-reference/admin-api-keys/listget
  - https://platform.openai.com/docs/api-reference/project-api-keys/list
  - https://platform.openai.com/docs/guides/rate-limits
  - https://platform.openai.com/docs/api-reference/usage/costs
  - https://platform.openai.com/docs/guides/prompt-caching
  - https://platform.openai.com/docs/guides/responses-vs-chat-completions
- Anthropic Console/API: rate limits, workspaces, usage/cost, prompt caching.
  - https://docs.anthropic.com/en/api/rate-limits
  - https://support.anthropic.com/en/articles/9796807-creating-and-managing-workspaces
  - https://docs.anthropic.com/en/api/usage-cost-api
  - https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching
- Google Vertex AI: project quotas, Dynamic Shared Quota / Standard PayGo, Provisioned Throughput, context caching.
  - https://cloud.google.com/vertex-ai/docs/quotas
  - https://docs.cloud.google.com/vertex-ai/generative-ai/docs/dynamic-shared-quota
  - https://cloud.google.com/vertex-ai/generative-ai/docs/resources/provisioned-throughput
  - https://cloud.google.com/vertex-ai/generative-ai/docs/context-cache/context-cache-overview
- AWS Bedrock: IAM, cross-region inference profiles, quotas, prompt caching, token burndown.
  - https://docs.aws.amazon.com/bedrock/latest/userguide/security-iam.html
  - https://docs.aws.amazon.com/bedrock/latest/userguide/cross-region-inference.html
  - https://docs.aws.amazon.com/bedrock/latest/userguide/global-cross-region-inference.html
  - https://docs.aws.amazon.com/bedrock/latest/userguide/prompt-caching.html
  - https://docs.aws.amazon.com/bedrock/latest/userguide/quotas-token-burndown.html
- OpenRouter: provider routing, usage limits/key info, BYOK, prompt caching/sticky routing.
  - https://openrouter.ai/docs/features/provider-routing
  - https://openrouter.ai/docs/api-reference/limits/
  - https://openrouter.ai/docs/use-cases
  - https://openrouter.ai/docs/features/prompt-caching

## 1. P0 Account-to-API Spine Ledger

**基线-开源**

- Sub2API 的 scheduler/service + account repository 证明它已经有 schedulability filters、score inputs、sticky routing、wait plan、outbox refresh；不足是它仍然把 local customer key 到 upstream account/pool 的产品合同藏在 group/pool 调度之后。
- CLIProxyAPI 的 config + per-provider executor 证明 `local API key -> bound credential -> provider executor` 是可成立的产品形态；不足是偏 1-to-1/配置驱动，不提供 HUAKAI 需要的多 binding、fallback order、credential lease、attempt ledger。
- Helicone 有 request explorer，LiteLLM/Portkey 有 retry/fallback 元数据；不足是没有把 key、binding、credential version、attempt、usage/billing/state 合并成一个 Account-to-API 可审计链。

**基线-官方**

- OpenAI/Anthropic/OpenRouter 都把 API key、project/workspace、usage/cost 或 key usage 作为治理对象；Bedrock/Vertex 把 project/account/region/profile 作为配额和路由对象。官方策略要求 HUAKAI 不能只记录 "provider_account_id"，必须记录请求当时绑定到哪个供应商治理对象和哪种 credential version。

**算法/数据结构/状态机**

- 新增 `BindingContract` 概念：`local_key_id -> target_kind(pool/account/default) -> target_id -> priority -> fallback_policy -> client_profile -> policy_version`。L1 允许 1 primary + N fallback，禁止 null target 的隐式 default。
- 新增轻量 lease：每个 request/attempt 写入 `(binding_contract_id, account_id, credential_kind, credential_version, lease_deadline)`；credential refresh 只 bump version，不覆盖 in-flight forensic record。
- 新增 append-only `AttemptLedger`：`request_id, attempt_no, chosen_account, chosen_model, chosen_region/profile, credential_version, started/ended, normalized_error, retry_after, state_transition, emitted_bytes, usage_vector_delta`。
- 状态机：`ResolveKey -> ResolveBinding -> FreezeCapability -> AcquireLease -> ExecuteAttempt -> ClassifyResult -> SettleUsage -> ReleaseLease -> AccountStateTransition`。任何 retry/fallback 都回到 `AcquireLease`，但不得覆盖旧 attempt。

**操作员/客户可见信号**

- Admin trace 一屏展示：local key prefix、binding、pool/account、credential version、provider endpoint/profile、attempt chain、usage/billing、状态变化。
- 客户侧仅暴露 request id、final status、是否 fallback、是否可能存在 context loss；不暴露 upstream credential/account secrets。

**类型分类**: 架构卫生
**优先级**: P0

## 2. P0 Vendor Governance Capacity Graph

**基线-开源**

- LiteLLM schema 摘要有 team/user/key/model budgets；Helicone 有 request/cents rate limit；Sub2API 有 user/group balance/RPM/capacity；不足是这些都是 gateway 内部 quota，没有建模供应商官方 project/workspace/account/region/profile 层级。
- one-api token/user quota 和 New API pricing expression 证明基础账务可做；不足是无法解释 "被 OpenAI project TPM、Anthropic workspace spend、Vertex project DSQ、Bedrock inference profile、OpenRouter key limit 中哪一层卡住"。

**基线-官方**

- OpenAI rate limits 是 organization/project/model 维度，Admin API 能管理 projects/API keys/rate limits。
- Anthropic 组织级 limits 与 Workspace limits 会同时参与请求判定，API key 绑定 Workspace。
- Vertex quotas 一般按 GCP project + region 生效，Standard PayGo/DSQ 与组织级 rolling spend/throughput 相关。
- Bedrock 配额受 AWS account/Region/model/inference profile 影响。
- OpenRouter 明确多 API key 不会绕过全局容量限制，key 还含 credit limit/remaining/BYOK usage。

**算法/数据结构/状态机**

- 建 `CapacityGraph`：节点 = `Tenant, VendorOrg, VendorProject/Workspace, Region/Profile, ProviderAccount, Pool, LocalKey, ModelClass`；边 = "counts_against / owns / routes_to / bills_to"。
- 每个节点维护多轴 token bucket：`request_per_min, input_token_per_min, output_token_per_min, token_per_day, dollars_month, image/audio/batch_queue, region_capacity`。请求进入时求 path 上所有 bucket 的 min-residual，返回 limiting node。
- Reconcile loop 从 OpenAI Usage/Costs、Anthropic Usage/Cost、OpenRouter key/credits、Cloud Monitoring/Bedrock CloudWatch 或 operator-entered snapshots 回写 graph；本地 usage 与官方 usage 差异进入 discrepancy queue。

**操作员/客户可见信号**

- 429/402/over-budget 错误显示 "limiting ancestor": 例如 `anthropic workspace`, `openai project`, `bedrock global profile`, `openrouter key balance`。
- Admin capacity page 显示每个供应商治理节点的 current burn、reset clock、official source timestamp、是否为估算。

**类型分类**: passthrough
**优先级**: P0

## 3. P0 Versioned Capability Snapshot Registry

**基线-开源**

- Envoy ai-gateway CRD 有 route/backend/model/header/body mutation 与 quota policy；LiteLLM 有广 provider packages；Portkey 有 provider routing/filtering；CLIProxyAPI 有 one-file-per-provider executor；不足是这些没有把 "单个 upstream account 此刻支持什么能力" 版本化到 request forensic 里。
- Account-to-API audit 已指出 HUAKAI 当前能力是 live row mutable columns，admin 中途改模型/额度会让同一 account 两个请求看到不同 truth。

**基线-官方**

- OpenAI 正推荐 Responses API 作为新项目默认，带 stateful conversation、built-in tools、MCP/custom tools、streaming semantic events。
- Anthropic/Vertex/Bedrock/OpenRouter 都有 provider-specific cache tokens、prompt caching TTL、region/data policy、tool/parameter support 差异。
- OpenRouter provider routing 的 `require_parameters`, data policy, ZDR, provider order/only/ignore 说明 capability mismatch 不能静默吞掉。

**算法/数据结构/状态机**

- 每次 account/provider/model 配置变化产生 `CapabilitySnapshot(version)`：`endpoint_family, request_shape, stream_shape, modalities, tool_support, cache_support, cache_token_fields, max_context, region_set, data_policy, pricing_vector, rate_axes, unsupported_param_policy`。
- Router 执行 `CapabilityFilter`: hard constraints 先过滤（protocol, region, data policy, required params），soft constraints 打分（cost, latency, cache warmth, SLA confidence）。
- Request 冻结 `capability_version` 到 AttemptLedger 和 UsageVector；如果后续发现 provider 忽略参数或 downgrade，写 `capability_loss_event`，按 policy fail-closed 或 warn。

**操作员/客户可见信号**

- Admin model/account matrix 展示 "可路由 / 可缓存 / 支持工具 / ZDR / region / streaming / price version"。
- 客户请求如果被降级，返回可配置 warning；strict mode 直接 4xx，避免 silent semantic loss。

**类型分类**: 架构卫生
**优先级**: P0

## 4. P1 Cache-Aware Sticky Routing + Session Migration

**基线-开源**

- Sub2API deep dive 证明 sticky routing 和 wait plan 是核心运营能力；CLIProxyAPI config 直接列出多种 session-affinity extraction 来源；OpenRouter 文档级行为证明 provider sticky routing 能为 prompt cache 命中服务。
- 不足：开源参考通常把 sticky 当 "继续用同账号/同 provider"，没有把 cache TTL、cache token economics、conversation migration 变成状态机。

**基线-官方**

- OpenAI prompt caching 依赖 exact prefix / prompt_cache_key，cache hit 会在 usage 中显示 cached tokens。
- Anthropic cache_control 有 5m/1h TTL 与 cache read/write usage fields。
- Vertex context caching 有 implicit/explicit 两类，cache resource 属于 project/location。
- Bedrock prompt caching 有 checkpoint、TTL、cache read/write token fields，并可与 cross-region inference 同用。
- OpenRouter 明确为了缓存使用 provider sticky routing，且按 account/model/conversation granularity。

**算法/数据结构/状态机**

- `SessionFingerprint = hash(client_identity_source, first_system_or_developer_prefix, first_user_prefix, normalized_tools, tenant_salt)`。
- `CacheLease` 记录 `provider_endpoint/account, cache_namespace, vendor_cache_id/prefix_hash, ttl_deadline, cost_delta, hit_rate_window`。
- 状态机：`Cold -> Warming -> StickyServing -> AtRisk(account unhealthy/cache expiring) -> RebuildOnTarget -> Migrated | Degraded | Broken`。
- Failover 前计算 `MigrationPlan`: `none`, `prefix_replay`, `cache_recreate`, `previous_response_reset`, `client_context_required`。已经向客户输出 partial stream 后，禁止透明重放，除非协议声明 continuation。

**操作员/客户可见信号**

- Usage row 展示 cache read/write、saved cost、sticky provider/account、migration action。
- 客户可看到 `context_migrated` 或 `context_may_be_lost`，避免长会话突然变笨但无解释。

**类型分类**: 创新
**优先级**: P1

## 5. P0 Typed Multi-Attempt Executor With Cost/Latency/Semantic Budgets

**基线-开源**

- Sub2API scheduler deep dive 有 filters、score inputs、sticky、wait plan、outbox。
- Portkey retry/fallback 参考有 status-code targeting、Retry-After、config inheritance/merge。
- LiteLLM deep rows有 typed fallback branches、bounded fallback depth、streaming fallback usage reconciliation。
- CLIProxyAPI config 有 request retry、max retry credentials、max retry interval、disable cooling、quota-exceeded fallback toggles。
- 不足：没有一个参考同时把 retry/fallback、model substitution、region/profile routing、cost budget、semantic loss、partial stream guard 合成一个 attempt DAG。

**基线-官方**

- OpenAI/Anthropic 都通过 response headers 或 retry-after 给出 rate-limit 反馈。
- Vertex DSQ/Standard PayGo 明确 429 可能是 shared pool contention，建议重试与 traffic smoothing。
- Bedrock cross-region inference profiles 自动跨 Region 增吞吐；global/geographic profile 有不同 data-residency/cost/throughput tradeoff。
- OpenRouter provider routing 有 order/allow_fallbacks/sort/max_price/latency-throughput threshold。

**算法/数据结构/状态机**

- Planner 生成 `AttemptDAG`，node = `(account, provider_endpoint, model_variant, region/profile, capability_version)`，edge reason = `retry_after_elapsed, account_cooldown, model_substitution, region_spillover, provider_order, cache_rebuild`。
- 每个 DAG 带预算：`max_attempts, max_distinct_accounts, max_wall_ms, max_incremental_cost, max_semantic_loss_score, max_region_policy_risk`。
- Execution state: `Plan -> Attempt -> Classify -> ContinueSame | Backoff | SwitchAccount | SubstituteModel | SwitchRegion/Profile | FailFinal`。若 `emitted_bytes > 0`，只允许 provider-declared resumable path，否则记录 partial failure 并停止透明 fallback。

**操作员/客户可见信号**

- Admin trace 显示每条 edge 为什么走、为什么没走。
- 客户错误体给出 sanitized reason：例如 `all candidates exhausted`, `fallback blocked by max_price`, `semantic downgrade rejected`, `retry-after not elapsed`。

**类型分类**: passthrough
**优先级**: P0

## 6. P1 Credential Lifecycle Auto-Recovery + Pluggable Secret Custody

**基线-开源**

- Sub2API 证明 upstream account refresh/recover/test/bulk ops 是商业运营面，不是后台细节。
- CLIProxyAPI 证明 file/Postgres/Git/object credential storage 与 bounded auth-refresh workers 是可行形态；不足是它偏 personal config，不是多租户审计型 custody。
- All API Hub 有 account auth auto-detect、duplicate scan、telemetry；不足是 browser-local secret custody 是 HUAKAI 应拒绝的 anti-pattern。

**基线-官方**

- OpenAI API keys/service/admin keys 是高权限 secret，官方强调只在 server/KMS 等安全位置加载；project API key/API key owner/last_used 是治理信号。
- Anthropic API keys 绑定 Workspace，Workspace archive 会影响 keys。
- Google/AWS 以 service account/IAM 身份为主，权限、region、project/account 边界是安全边界。
- OpenRouter BYOK 把 provider keys 加密保存并可设 fallback，但使用自带 key会改变成本/限额归属。

**算法/数据结构/状态机**

- Credential state machine: `Valid -> RefreshDue -> Refreshing -> GraceServing -> NeedsHuman -> Disabled -> Recovered`。每个转移有 actor、reason、secret-redacted audit。
- Refresh scheduler 用 three-scope storm guard：`account lock`, `provider endpoint rate gate`, `tenant/global worker budget`。Credential version 用 CAS 更新，旧 version 的 active leases 可到 deadline 后强制失效。
- Secret custody plugin: L1 Postgres + KMS/envelope encryption；L3/L4 支持 file/git/object store，但必须加密、签名、device/tenant scoped，禁止浏览器明文长期保存。
- Recovery link 是一次性签名 action token，绑定 tenant/account/credential kind/reason/expiry；human recovery 成功后 bump version 并触发 capability snapshot refresh。

**操作员/客户可见信号**

- Account list 直接显示 `needs_refresh`, `needs_manual_recovery`, `refresh_storm_suppressed`, `active_leases_on_old_version`。
- 一键恢复动作打开 OAuth/browser handoff 或 provider-specific instructions；所有 secret 永不回显。

**类型分类**: 创新
**优先级**: P1

## 7. P1 Asset Valuation + Predictive Capacity Planner + SLA Oracle

**基线-开源**

- Sub2API 有 concurrency/capacity/admin usage；Helicone 有 cost/latency/token rollups 和 wallet/credits；LiteLLM 有 budgets；All API Hub 有 external telemetry profiles。
- 不足：9 个参考都把 upstream accounts 当 routing/cost 输入，没有把账号库存当可估值资产、可补货库存、可销售 SLA capacity。

**基线-官方**

- OpenAI/Anthropic usage tiers 与 spend limits 决定可用额度；Vertex Standard PayGo 通过 rolling spend 给 baseline throughput，Provisioned Throughput 用保留容量换 predictability；Bedrock 有 on-demand/cross-region/provisioned-like throughput tradeoffs 与 token burndown；OpenRouter 有 credits/BYOK/provider price/routing。

**算法/数据结构/状态机**

- `AccountAssetSnapshot`: `remaining_quota_vector, reset_clock, subscription_days_left, model_price_vector, official_limit_vector, reliability_score, policy_risk_score`。
- `AssetValue = sum(remaining_capacity_i * resale_margin_i) + subscription_time_value - risk_discount - stranded_capacity_penalty`。不用于财务记账，只用于运营估值和补货建议。
- Forecast 用 7/30 day usage windows + quantile smoothing：按 `(binding, model_class, client_profile, weekday/hour)` 预测 burn。SLA oracle 返回 `can_sustain`, `confidence`, `limiting_node`, `needed_inventory_delta`, `recommended_purchase/profile`。

**操作员/客户可见信号**

- Dashboard 显示 "account portfolio value", "burn/day", "exhaust date", "top draining key", "add one account/profile changes SLA by X"。
- 销售/客户页可给出可解释 SLA quote：例如 "50 RPM for this key is accepted at 82% confidence; limiter is Anthropic workspace OTPM"。

**类型分类**: 创新
**优先级**: P1

## 8. P2 Official Policy / Data Residency / ToS Change Guard

**基线-开源**

- OpenRouter provider routing 有 data policy/ZDR/provider ToS exposure；All API Hub 有 telemetry/check-in/provider automation，但需要法律和产品审查；CLIProxyAPI/All API Hub 的自动 panel/download/check-in 类能力也暴露供应链/ToS 风险。
- 不足：没有参考把 upstream official policy drift 作为 HUAKAI 运营状态机的一部分。

**基线-官方**

- OpenRouter 允许按 data collection/ZDR 过滤 provider，并提醒不能违反第三方 provider ToS。
- OpenAI prompt caching/store/ZDR 行为会影响数据保留；Responses API stateful store 默认行为也需要客户选择。
- Vertex context caches 属于 project/location，支持 VPC-SC；Bedrock geographic/global cross-region inference 有明确 data residency tradeoff 与 IAM/SCP 要求。
- Anthropic Workspace limits/default workspace/null usage dimensions 会影响治理归属。

**算法/数据结构/状态机**

- `PolicyConstraint` 附着在 tenant/key/binding/request：`zdr_required, data_residency_region, cache_retention_allowed, store_allowed, third_party_training_denied, official_tos_profile, allowed_region_profile`。
- `ProviderPolicySnapshot` 从官方 docs/changelog/manual review 进入：`observed_version, effective_date, changed_clauses, affected_features, confidence`。
- 状态机：`Observed -> NeedsReview -> Approved -> Active`, 或 `Observed -> BlocksRoute -> ExceptionUntil(expiry) -> ReviewAgain`。路由器在 capability filter 前做 policy filter。

**操作员/客户可见信号**

- Compliance posture page：哪些 accounts/bindings 受某供应商政策变化影响。
- 路由拒绝原因可读：`EU-only policy rejects Bedrock Global profile`; `ZDR required rejects provider endpoint that may retain data`; `extended cache disabled by tenant policy`。

**类型分类**: 创新
**优先级**: P2

## 9. P0 UsageVector Pricing Engine With Cache / Service-Tier / Burndown Dimensions

**基线-开源**

- New API 证明 pricing expression、cache-token billing、disk/hybrid cache ops 是生产 pricing 的 baseline；one-api 的 reservation/refund 不是 money-grade idempotent claim；Sub2API payment lifecycle deep dive 证明 order/webhook/refund/recovery 必须状态机化；Helicone 有 cost/credits/rate units。
- 不足：开源参考没有统一多供应商 official usage dimensions，尤其 cache write/read、service tier、reasoning/thinking、Bedrock burndown、BYOK fee 与 fallback split attribution。

**基线-官方**

- OpenAI Usage/Costs API 支持 project/user/api_key/model/batch/service_tier grouping，并有 cached token fields。
- Anthropic usage/rate limits 区分 input/cache creation/cache read/output，ITPM/OTPM 的计算也不是简单 total tokens。
- Vertex context cache response 有 cached token count；Bedrock TokenUsage 有 cache read/write/cache details，并对部分模型 output tokens 有 quota burndown multiplier；OpenRouter key/credits 区分 normal usage 与 BYOK usage。

**算法/数据结构/状态机**

- `UsageVector`: `input_text, input_cached, cache_write_5m, cache_write_1h, output_text, reasoning, image, audio_seconds, tool_runtime, batch_queued, service_tier, byok_usage, bedrock_burndown_weight, region/profile`。
- Pricing expression engine 只允许 whitelisted variables/operators；每个 price rule 有 `version, effective_from, source, test_vectors, rollback_to`。Compile cache key = `(rule_hash, variable_schema_version)`。
- Billing state: `AuthorizeBudget -> ReserveCeiling -> AccrueAttemptDeltas -> FinalizeSucceededAttempt -> ReconcileOfficialCost -> AdjustOrDispute`。Idempotency key = request fingerprint + binding + first attempt id；fallback split uses explicit policy (`succeeded_on`, `dollar_weighted`, `operator_absorbs_failed_attempts`).

**操作员/客户可见信号**

- Invoice line 可解释：base input、cache write、cache read、output、reasoning、fallback failed cost、BYOK fee、official discrepancy。
- Admin reconciliation page 对比 HUAKAI billed amount vs official usage/cost export，差异进入 review queue。

**类型分类**: passthrough
**优先级**: P0

## 10. P1 Request Trace Explorer + Conversational Ops Debug Agent

**基线-开源**

- Helicone request explorer/request detail/session/metrics 是最强 post-request baseline；Sub2API admin dashboards 覆盖 concurrency/alerts/request errors/upstream errors/cleanup；All API Hub 有 selected-row execution、retry failed、preview before write 等 operator ergonomics。
- 不足：参考项目多是 raw explorer 或 dashboard，没有把 Account-to-API trace 图交给一个受权限约束的 debug agent 做解释和下一步操作。

**基线-官方**

- OpenAI response headers 包含 request id 与 rate-limit headers，Usage/Costs 可按 project/key/model 追踪。
- Anthropic Usage/Cost API 与 workspace rate headers 支持对照。
- Bedrock CloudTrail 可显示 cross-region inference 的 actual inferenceRegion，CloudWatch 有 token/cache metrics。
- OpenRouter key/credits endpoint 能解释 credit limit/remaining/BYOK usage。

**算法/数据结构/状态机**

- `RequestTraceGraph` 节点：`LocalKey, BindingContract, CapabilitySnapshot, Attempt, VendorRequestId, VendorLimitHeader, UsageVector, BillingClaim, AccountStateTransition, AuditEvent`；边表示 `resolved_to, attempted_on, limited_by, billed_by, changed_state`。
- Debug agent 只读 sanitized graph，不读 raw prompts by default。Allowed actions 是 typed intents：`open_recovery_flow`, `disable_binding`, `raise_local_limit`, `rerun_health_probe`, `create_policy_exception`，每个 action 必须 RBAC + confirm + audit。
- Retention policy 将 raw body、structured metadata、billing facts 分层保留；客户 trace-lite 永远不含 upstream credential/account internals。

**操作员/客户可见信号**

- Operator 问 "why did request R fail?"，系统回答 attempt chain、limiting node、state transition、建议动作，并能打开对应 recovery UI。
- Customer portal 看到 sanitized reason、retry suggestion、billing impact，而不是 500/429 黑盒。

**类型分类**: 创新
**优先级**: P1

## Priority Rollup

| Priority | Items | Why |
| --- | --- | --- |
| P0 | 1, 2, 3, 5, 9 | 这些是 Slice 5 real upstream / money-grade routing / official quota-billing 对齐的地基；缺了会把 HUAKAI 推回 generic gateway。 |
| P1 | 4, 6, 7, 10 | 这些直接形成 Personal Edition 和 Account-to-API 产品差异；需要 spine/usage 基础后尽快进入 L2。 |
| P2 | 8 | 官方 policy/ToS 变化是高价值但需要产品/法律节奏，建议作为 Phase 8+ 或 SaaS hardening。 |

## No Feature Shrinkage / Clean-Room Notes

- 没有删除任何 reference feature；风险项都转为 passthrough、创新或架构卫生提升。
- 未读取新 reference source；只读内部 evidence/deep-dive 文档和官方 vendor docs。
- 未复制 Sub2API/Anthropic 源码字段名、函数名、算法公式；本文的数据结构为 HUAKAI 自定义抽象。
- 需要 Owner 确认：是否把 P0 五项提升为 `docs/03_FEATURE_PARITY_MATRIX.md` 新 F-* rows；是否批准 P1 创新进入下一轮计划；是否对 policy/ToS guard 启动法律/产品审查。
