# Feature-Tree Audit: routing-loadbalance

**Domain summary:** HUAKAI 具备成熟的多层路由骨架（优先级→PASR/HRW→粘性→健康门控→退避），在 adaptive scheduling 和 health FSM 上超出同类；但在正向延迟路由、主动 RPM/TPM 预算、跨模型 fallback 链、地理路由、权重实际生效等方面存在明显商业缺口，影响付费客户 SLA。

审计日期: 2026-06-02 | 代码分支: fix/hermes-phase-1-e33d940 | 范围: `backend/` + `exploratory/rust-core-gateway/`

---

## Feature Coverage Table

| # | Feature | Status | Evidence (file:line or grep term) | Gap Note |
|---|---------|--------|----------------------------------|----------|
| **A — 基础负载均衡策略** |
| A-1 | 优先级严格路由 (strict_priority) | **PRESENT** | `backend/internal/pool/router/default_selector.go:225-229` rankFresh 按 Priority 升序排; `backend/sql/migrations/0008_model_registry.up.sql:149` selection_mode 'strict_priority' | |
| A-2 | 加权随机选择 (weighted round-robin) | **PARTIAL** | `backend/sql/migrations/0008_model_registry.up.sql:165` weight 列存储; `backend/internal/registry/registry.go:73` RPMLimit/TPMLimit/weight 字段读出 | weight 在 schema 和 registry 中存在，但 rankFresh (`default_selector.go:223-244`) 排序键仅为 Priority→LoadRate→LastUsedAt，**未实现概率加权抽样**；weight 值读取后未传入 selector |
| A-3 | 最小负载 (least in-flight / LoadRate) | **PRESENT** | `backend/internal/pool/router/default_selector.go:230` LoadRate 次级排序键 | |
| A-4 | LRU 近似轮询 (tie-break round-robin) | **PRESENT** | `backend/internal/pool/router/default_selector.go:233` LastUsedAt 第三排序键 | 非严格 RR，仅 LRU 近似；Priority/LoadRate 不同时失效 |
| A-5 | 同优先级随机洗牌 | **PRESENT** | `backend/internal/pool/router/default_selector.go:236-241` topK 等位账号 rand.Shuffle | |
| **B — 健康感知路由** |
| B-1 | P99 延迟追踪 + 降级阈值 | **PRESENT** | `backend/internal/channelhealth/window.go:66-70` percentile99(); `backend/internal/channelhealth/service.go:492-510` latencyDecision(); 默认阈值 30 s | |
| B-2 | 错误率信号检测 | **PRESENT** | `backend/internal/channelhealth/signal_classifier.go` SignalClass.ErrorRate; `backend/internal/channelhealth/types.go:39-157` 枚举 + policy | |
| B-3 | 限速 (429) 信号检测 | **PRESENT** | `backend/internal/gatewayhttp/chat_completions_error.go:123-135` recordChannelHealthSignal() → RateLimit class; Retry-After 解析 | |
| B-4 | 每账号 cooldown / 退避 | **PRESENT** | `backend/internal/channelhealth/types.go:119-149` LatencyCooldown 5 min; CooldownUntil 时间戳; `backend/sql/migrations/0004_rate_limiting.up.sql:77` rate_limit_reason | |
| B-5 | 软恢复 Ramping (无强制 disable) | **PRESENT** | `backend/internal/channelhealth/service.go:418-510` Degraded→CoolingDown→Ramping FSM；不做硬关闭 | |
| B-6 | 连续健康分 + 衰减 | **PRESENT** | `backend/internal/gateway/health_fsm.go` score 0.0-1.0 半衰 10 min; `backend/sql/migrations/0022_channel_health_state.up.sql:20` score numeric | |
| B-7 | 熔断器 (circuit breaker) | **PRESENT** | `backend/internal/circuitbreaker/breaker.go:1-302` 三态 Closed/Open/HalfOpen; half-open probes; 可配 OpenCooldown | |
| B-8 | Admin 强制启/停单个 channel | **PRESENT** | `backend/internal/gatewayhttp/channel_health_admin_handler.go:50,130` POST /{id}/channel-health/force-active; `backend/internal/channelhealth/service.go:240,247` ForceActive/ForcePause | |
| **C — Provider / Channel 选择** |
| C-1 | 模型→Pool 绑定 (优先级 + 权重) | **PRESENT** | `backend/sql/migrations/0008_model_registry.up.sql:149-210` model_pool_bindings; `backend/internal/registry/registry.go:28-90` ResolveModel | weight 存储 PRESENT；**实际选择时未使用** → 见 A-2 |
| C-2 | 模型别名 / 归一化 | **PRESENT** | `backend/sql/migrations/0008_model_registry.up.sql:95-122` model_aliases 表; `backend/internal/registry/errors.go:21` ErrUnknownModel; `backend/cmd/gateway/smoke_test.go:234` seed alias | |
| C-3 | 模型通配符模式匹配 | **PRESENT** | `backend/internal/routeadmin/types.go:18-51` model_pattern_match ('*'/ suffix 通配); `backend/internal/subscriptionenforce/routes_repo_postgres.go:27-62` 应用层过滤 | |
| C-4 | 每模型绑定 RPM/TPM 上限 (配置) | **PRESENT** | `backend/internal/db/registry/registry.sql.go:171-204` rpm_limit, tpm_limit 字段; `backend/internal/registry/registry.go:75-76` RPMLimit/TPMLimit 结构体 | 值存储 PRESENT；**门控仅为响应式**（见 C-5） |
| C-5 | 主动 RPM/TPM 预算追踪 (proactive) | **MISSING** | grep `rpm_limit` in `pool/` → 0 hits; `gates.go:210-229` modelRateLimitGate 仅检查 RateLimitResetAt 时间戳（429 后写入），无滑动窗口计数器 | **高商业价值缺口**：无法在打满限额前绕开；每次 429 = 已失败请求 + 延迟 |
| C-6 | 协议族路由 (OpenAI/Anthropic/Gemini/…) | **PRESENT** | `backend/internal/gateway/upstream_dispatcher_hcsf.go:210` 多 case 分支; `backend/internal/pool/router/gates.go:194-203` protocolFamilyGate | |
| C-7 | 上下文窗口感知路由 | **PARTIAL** | `backend/internal/registry/registry.go:49` ContextWindow 字段; `backend/internal/router/route_plan.go:20` 传入 RoutePlan; `backend/internal/registry/registry.go:78` FallbackClass='context_window' 枚举 | 无主动"请求 token 数 > 模型窗口 → 跳过/fallback"逻辑；ContextWindow 传播但不参与 gate 决策 |
| C-8 | 能力标记路由 (vision / tools / streaming) | **PARTIAL** | `backend/internal/pool/router/gates.go:51-63` CapabilityGate 接口; `backend/internal/pool/router/types.go:38` CapabilityFlags 字段; `backend/internal/db/billing/pool_accounts.sql.go:707` 注释 "production gate AllowAll 全过" | 接口已定义、框架已接线，但生产实现为 AllowAll；能力匹配未实际执行 |
| **D — 会话粘性 / 亲和性** |
| D-1 | Prompt-hash 粘性 (缓存亲和) | **PRESENT** | `backend/internal/cache_routing/prompt_hash.go:1-37` SHA-256 前缀哈希; `backend/internal/pool/router/default_selector.go:123-131` upsert sticky_bindings | |
| D-2 | 粘性绑定持久化 + TTL | **PRESENT** | `backend/sql/migrations/0001_pool_routing.up.sql:199-215` sticky_bindings 表 expires_at; `backend/internal/db/billing/pool_sticky_bindings.sql.go:14-76` CRUD | |
| D-3 | 粘性绑定失效 / 清理 | **PRESENT** | `backend/sql/migrations/0001_pool_routing.up.sql:318` audit 'sticky_binding_invalidated'; DeleteExpiredStickyBindings | |
| D-4 | 多轮对话续流路由 (continuation) | **PARTIAL** | `backend/internal/pool/router/types.go:30-80` ContinuationKey 字段存在; proto MidStreamFallbackContinuation 枚举定义 | 中流续转 P-0 验证器强拒 (`envelope_validate.go:499`)，ContinuationKey 传播但续流选择逻辑待 P-8 落地 |
| **E — 自适应 / 高级路由** |
| E-1 | PASR/HRW 段路由 | **PRESENT** | `backend/internal/pool/router/pasr.go:1-150` PASRSelector K=3; `backend/internal/pool/router/hrw_ring.go` SHA-256 HRW; `backend/internal/pool/router/prefix_segment.go` SegmentTable | |
| E-2 | 缓存感知降级 (cache-miss demote) | **PRESENT** | `backend/internal/pool/router/feedback.go:54-100` MissCount N=2 demote; `backend/internal/pool/router/prefix_segment.go:94-126` HasCache 位清除 | |
| E-3 | 金丝雀流量分割 (hash-bucket %) | **PRESENT** | `backend/internal/pool/dispatcher/dispatcher.go` fnv64a % 100 < CanaryPercent; `backend/internal/pool/dispatcher/metrics.go` canary_pasr_used 等 | 分割比例当前由 ENV 配置，无运行时 admin API 调整 |
| E-4 | Shadow 模式 (异步 PASR 对比) | **PRESENT** | `backend/internal/pool/dispatcher/dispatcher.go:54-70` shadowQueueCap=1024, 500 ms timeout; panic recover | |
| E-5 | 地理 / 区域路由 | **MISSING** | grep `geo\|region.*route\|geographic\|nearest` in backend/ → 0 routing-related hits | 全球部署场景下无区域亲和；延迟可能显著高于优化后 |
| E-6 | 延迟优化正向路由 (prefer fastest) | **MISSING** | P99 延迟用于降级门控（负向），但 rankFresh 不以测量延迟作正向偏好；无 EWMA 延迟排名 | 仅防止"坏的"，不主动选"快的" |
| E-7 | 成本感知路由 (prefer cheapest) | **MISSING** | `backend/internal/billing/settler.go` 成本追踪 PRESENT，但 SelectionRequest / rankFresh 无成本权重字段 | 多定价区间 provider 时无法引导到低成本通道 |
| **F — Failover & Retry** |
| F-1 | 可重试错误自动 failover | **PRESENT** | `backend/internal/router/default_router.go:40-98` RetryableEndClasses: upstream_error_5xx / upstream_rate_limit / first_token_timeout / inter_event_timeout | |
| F-2 | 重试排除已失败 provider | **PRESENT** | `backend/internal/pool/router/types.go:30-80` ExcludedAccounts []int64; 每 Attempt 注入上一次 accountID | |
| F-3 | 跨模型 fallback 链 | **MISSING** | `backend/internal/registry/registry.go:78` FallbackClass 枚举有 'context_window'/'safety'/'quota' 占位，但无模型链执行逻辑 (grep `fallback.*chain\|model.*fallback.*next` → 0 hits) | 模型不可用时无自动降级到等价模型 |
| F-4 | 中流 failover (streaming 断续恢复) | **PARTIAL** | `backend/sql/migrations/0003_streaming_forwarder.up.sql:41` mid_stream_failover_default 列; proto MidStreamFallbackContinuation 枚举 | P-0 验证器强拒非 none 值 (`envelope_validate.go:499`); 路线图 P-8 |
| F-5 | 最大重试次数控制 | **PRESENT** | RoutePlan.Attempts 列表由 PoolCandidates 数量上界控制; RetryableEndClasses 枚举穷举终止条件 | |
| **G — 用户 / 租户路由** |
| G-1 | 用户组路由 (订阅层级门控) | **PRESENT** | `backend/internal/subscriptionenforce/gate.go:15-100` GroupPolicyGate; `backend/sql/migrations/0001_pool_routing.up.sql:265-318` routes 表 | |
| G-2 | 租户隔离 | **PRESENT** | 所有查询携带 tenant_id; pool/sticky/routes/billing 全部按 tenant 分区 | |
| G-3 | 每组自定义路由规则 | **PRESENT** | `backend/internal/routeadmin/types.go` Route CRUD; `backend/internal/adminhttp/channel_catalog_handler.go` admin API | |
| G-4 | Quota 策略感知路由 | **PRESENT** | `backend/sql/migrations/0070_quota_subsystem.up.sql:18-60` quota_policies; `backend/internal/quota/service.go` Reserve/Release | |
| **H — 流量管理** |
| H-1 | 并发槽上限 (slot manager) | **PRESENT** | `backend/internal/pool/dispatcher/slot_manager.go` DBSlotManager; in_flight_count cap per account | |
| H-2 | 等待计划 (all providers busy) | **PRESENT** | `backend/internal/pool/router/default_selector.go` fallback wait plan 分支 | |
| H-3 | 响应 drain 预算 (bytes/s/cost) | **PRESENT** | `backend/internal/gateway/forwarder_types.go:49-75` DrainOutcome; DrainBudgets; `backend/sql/migrations/0003_streaming_forwarder.up.sql:56` drain_max_estimated_cost_usd | |
| H-4 | Admin 可配流量分割比例 | **PARTIAL** | PASR canary % 存在但由 ENV 驱动 (`backend/internal/config/pool_selector.go`); 无运行时 API 动态调整百分比 | 变更流量分割需要重启；对灰度发布不友好 |
| H-5 | Provider 优雅排水 (graceful drain) | **MISSING** | grep `drain.*channel\|weight.*zero\|graceful.*remove` → 无 channel-level 排水逻辑 | 维护 provider 时无法平滑迁移流量 |
| **I — 可观测性 & Admin** |
| I-1 | 每次调度路由原因日志 | **PRESENT** | `backend/internal/pool/router/routing_reason.go` RoutingReason struct; selection_layer, affinity_key_class, capability_outcome 字段 | |
| I-2 | Canary / Shadow 指标 | **PRESENT** | `backend/internal/pool/dispatcher/metrics.go:37-210` canary_pasr_used, shadow_drop_full, pre_mutation_fallback 等 | |
| I-3 | Channel 健康状态审计日志 | **PRESENT** | `backend/internal/channelhealth/store_postgres_audit_required_test.go`; EventManualOverride, EventDisabled 枚举 | |
| I-4 | 路由策略 Admin CRUD | **PRESENT** | `backend/internal/routeadmin/`; `backend/internal/adminhttp/channel_catalog_handler.go` | |
| I-5 | 实时路由指标 (Prometheus) | **PARTIAL** | dispatcher/metrics.go 指标定义存在，但未找到 `prometheus.Register` 完整挂载路径；需验证是否接入 `/metrics` 端点 | |

---

## Top Missing / Partial, Ranked by Commercial Value

| Rank | Feature | Status | Commercial Impact |
|------|---------|--------|-------------------|
| 1 | **C-5 主动 RPM/TPM 预算追踪** | MISSING | **S1** — 无预算窗口计数器，provider 限额前无法预防性绕开；每次 429 = 用户看到错误或延迟。sub2api `backend/model/option.go` 有 QPM 令牌桶；new-api channel_limit 表含每分钟 rpm 计数器 |
| 2 | **F-3 跨模型 fallback 链** | MISSING | **S1** — 主模型不可用时无自动降级（gpt-4o 不可用 → gpt-4-turbo → claude-3.5-sonnet），可用性直接暴露给用户。sub2api 有 model_mapping + fallback 列表；new-api channel 有 models 字段多对多备选 |
| 3 | **A-2 权重实际生效** | PARTIAL | **S1** — weight 字段已存储但选择器忽略它；operator 设置的权重无效，等同于优先级路由 |
| 4 | **C-8 能力标记路由实际执行** | PARTIAL | **S1** — vision/tools 请求路由到不支持的 provider 会产生上游 400；当前 AllowAll 门控不保护 |
| 5 | **C-7 上下文窗口感知路由** | PARTIAL | **S2** — 超长 prompt 发到窗口不足的 provider 直接 400；需要"请求 token 数 > window → fallback"逻辑；FallbackClass='context_window' 枚举已预留但未执行 |
| 6 | **E-6 延迟优化正向路由** | MISSING | **S2** — 健康 FSM 只做负向门控（剔除坏的），不主动偏好最快的；p50 延迟高于理论下界。LiteLLM 有 `lowest_latency` routing strategy；new-api `channel_latency` 字段支持 EWMA 排名 |
| 7 | **E-7 成本感知路由** | MISSING | **S2** — 多定价区间 provider（不同价格/速率）时无法自动引流到低成本通道；降低 COGS 的核心杠杆。sub2api 支持 channel 权重 + cost 因子混合排名 |
| 8 | **H-4 Admin 可动态调整流量分割%** | PARTIAL | **S2** — 金丝雀比例需重启才能改；灰度发布操作体验差 |
| 9 | **F-4 中流 failover** | PARTIAL | **S2** — 路线图 P-8；streaming 场景下 provider 断连仍须客户端重发整个请求 |
| 10 | **E-5 地理 / 区域路由** | MISSING | **S3** — 全球化部署后跨区延迟无法优化；operator 无法将亚洲流量固定到亚洲 provider |
| 11 | **H-5 Provider 优雅排水** | MISSING | **S3** — 维护或下线 provider 时无 weight-to-zero 平滑过渡；流量切换是硬切 |
| 12 | **I-5 Prometheus 指标完整挂载** | PARTIAL | **S3** — 指标定义存在但挂载路径未验证；监控告警缺口 |
| 13 | **D-4 多轮续流路由** | PARTIAL | **S3** — ContinuationKey 框架已就位，待 P-8 续流策略落地 |

---

## 补充说明

- **PASR/HRW 超越同类**：sub2api 和 new-api 均用简单权重轮询；HUAKAI 的 HRW+SegmentTable+miss demote 是架构升级 (架构升级 + 算法升级)。
- **健康 FSM 超越同类**：new-api 仅 enable/disable 硬状态；HUAKAI 的 P99+ErrorRate+RateLimit 多信号 FSM + 软恢复 Ramping 是生态升级。
- **最高优先级修复**：C-5（主动 RPM/TPM）和 F-3（跨模型 fallback）直接影响客户可感知可用性，建议列入 Slice 优先级最高。
