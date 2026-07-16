# 无运行时消费的 schema 保留清单

> 状态：2026-07-14 核验。本文只做事实标注，**不是删除计划**。本次工作不修改 schema、迁移、SQL 生成码或存量数据。

## 1. 判定口径

本文的“无运行时消费”指：列值或表中记录目前不会进入 HUAKAI 的请求路由、选号、gate、流式控制、计费、配额、认证、moderation 判定或 affiliate 等生产决策。以下命中本身不构成运行时消费：

- 迁移、schema 镜像、OpenAPI 与历史设计文档；
- sqlc 查询定义、生成的行类型和生成方法，但没有非生成业务代码调用；
- admin CRUD 的存取、兼容序列化或只读诊断投影；
- 固定写入零值，却没有后续业务读取与决策；
- 与数据库列同名、但配置来源完全独立的内存字段或环境变量。

若将来启用任何一项，必须补齐表中列出的完整链路并增加判别性测试；不得仅把 admin 控件重新露出。所有项目当前处置均为 **Mandatory Roadmap（保留 schema，运行时未启用）**，没有功能删除。

## 2. `routes` 的 26 个无消费列

运行时当前只读取路由身份、匹配条件、目标池、匹配优先级与启用态。`routeadmin` 的管理投影同样只覆盖这些核心字段。下列 26 列没有被任何 `routes` 查询投影到生产策略。`gateway.TimeoutConfig` 中存在独立的超时/排空字段，但其值来自 env/平台设置，不来自 `routes`。

| 表/列名 | 建于哪个迁移 | 现状（无运行时消费） | 将来若启用需要补的链路 |
| --- | --- | --- | --- |
| `routes.sticky_wait_max_override` | `backend/sql/migrations/0001_pool_routing.up.sql` | 仅有列定义；route admin 与订阅分组 gate 均不读取 | admin API/UI → route 策略加载器 → sticky 等待策略 → 快照/审计 → 超时与回退测试 |
| `routes.fallback_wait_max_override` | `0001_pool_routing.up.sql` | 仅有列定义；不进入 selector 的 fallback wait | admin API/UI → route 策略加载器 → selector 等待计划 → 边界/恢复测试 |
| `routes.capability_policy_override` | `0001_pool_routing.up.sql` | 仅有列定义；能力 gate 不读取 | admin API/UI → 枚举校验 → route 策略加载器 → capability gate → 决策审计 |
| `routes.top_k_override` | `0001_pool_routing.up.sql` | 仅有列定义；selector 的 `TopKDefault` 不从 route 取得 | admin API/UI → route 策略加载器 → selector top-K → 并发与分布测试 |
| `routes.weight_priority` | `0001_pool_routing.up.sql` | 仅有列定义；评分器不加载 route 权重 | admin API/UI → 版本化评分配置 → route loader → scorer → 快照/可解释理由 |
| `routes.weight_load_rate` | `0001_pool_routing.up.sql` | 同上 | 同上，并补负载维度的判别性排序测试 |
| `routes.weight_last_used` | `0001_pool_routing.up.sql` | 同上 | 同上，并补最近使用时间维度测试 |
| `routes.weight_recent_error_rate` | `0001_pool_routing.up.sql` | 同上 | 同上，并接健康窗口聚合与错误率来源 |
| `routes.weight_recent_latency` | `0001_pool_routing.up.sql` | 同上 | 同上，并接延迟窗口聚合与陈旧数据策略 |
| `routes.weight_quota_headroom` | `0001_pool_routing.up.sql` | 同上 | 同上，并接真实配额余量来源与失败关闭策略 |
| `routes.weight_fairness_debt` | `0001_pool_routing.up.sql` | 同上 | 同上，并定义公平债务的生成、衰减和并发一致性 |
| `routes.weight_snapshot_freshness` | `0001_pool_routing.up.sql` | 同上 | 同上，并定义快照新鲜度、陈旧阈值与降级行为 |
| `routes.connect_timeout_ms` | `backend/sql/migrations/0003_streaming_forwarder.up.sql` | 仅有列定义；连接超时不从 route 加载 | route loader → 每 attempt transport 配置 → 上限校验 → 超时分类/审计测试 |
| `routes.tls_handshake_timeout_ms` | `0003_streaming_forwarder.up.sql` | 仅有列定义 | route loader → TLS transport → 上限校验 → 握手超时测试 |
| `routes.request_write_timeout_ms` | `0003_streaming_forwarder.up.sql` | 仅有列定义 | route loader → 请求写入阶段 → 超时分类 → 重试安全测试 |
| `routes.response_header_timeout_ms` | `0003_streaming_forwarder.up.sql` | 仅有列定义 | route loader → header 等待阶段 → 超时分类 → failover 测试 |
| `routes.first_token_timeout_ms` | `0003_streaming_forwarder.up.sql` | 同名运行时能力存在，但由 env/平台设置提供，不读本列 | 明确配置优先级 → route loader → forwarder → 指标/审计 → 首 token 超时测试 |
| `routes.inter_event_timeout_ms` | `0003_streaming_forwarder.up.sql` | 同名运行时能力存在，但不读本列 | 明确配置优先级 → route loader → stream scanner → 中途静默测试 |
| `routes.total_stream_timeout_ms` | `0003_streaming_forwarder.up.sql` | 运行时总时限来自平台设置/env，不读本列 | 明确配置优先级 → route loader → stream 生命周期 → 结算/abort 协作测试 |
| `routes.downstream_write_timeout_ms` | `0003_streaming_forwarder.up.sql` | 仅有列定义 | route loader → downstream writer → 客户端慢读/断连测试 → 审计 |
| `routes.scanner_buffer_max_bytes` | `0003_streaming_forwarder.up.sql` | scanner 有独立限制，但不读本列 | route loader → scanner 构造 → 全局安全上限 → 大事件拒绝与内存压力测试 |
| `routes.drain_max_seconds` | `0003_streaming_forwarder.up.sql` | `TimeoutConfig` 有同名 JSON 字段，但由 env 构造，不读本列 | 配置优先级 → route loader → disconnect drain → 时间预算与结算测试 |
| `routes.drain_max_bytes` | `0003_streaming_forwarder.up.sql` | drain 能力未从 route 取得该预算 | route loader → drain budget → 字节计量 → 断连恢复/结算测试 |
| `routes.drain_max_estimated_cost_usd` | `0003_streaming_forwarder.up.sql` | 不进入当前 drain 成本预算 | route loader → decimal 金额校验 → drain 成本估算 → money 路径对账测试 |
| `routes.mid_stream_failover_default` | `0003_streaming_forwarder.up.sql` | 不进入流中 failover 决策 | admin 风险提示 → route loader → replay-safe gate → 客户端交付边界与重复计费测试 |
| `routes.tokenizer_fallback_enabled` | `0003_streaming_forwarder.up.sql` | 不进入 usage 缺失时的估算决策 | route loader → tokenizer 能力选择 → provisional usage → reconciliation 与信任状态测试 |

计数证明：`0001` 的 4 个策略 override + 8 个评分权重 = 12；`0003` 的 8 个超时 + 1 个 scanner cap + 3 个 drain budget + 1 个流中 failover + 1 个 tokenizer 开关 = 14；合计 **26**。

## 3. 整表无消费

| 表/列名 | 建于哪个迁移 | 现状（无运行时消费） | 将来若启用需要补的链路 |
| --- | --- | --- | --- |
| `mimicry_policy`（整表） | `backend/sql/migrations/0006_upstream_credential_management.up.sql` | 全仓无查询定义或业务读取；现有请求变换不以本表为配置源 | tenant+pool 作用域 Store/缓存 → legal review 强制门 → 请求变换策略 → 无秘密审计 → admin API/UI → 缓存失效与关闭测试 |
| `oidc_provider_configs`（整表） | `backend/sql/migrations/0081_multi_provider_oauth.up.sql` | OIDC 流程存在，但 provider resolver 使用平台设置/env；本表没有 Store、查询或调用 | 租户域 admin CRUD → secret envelope 加解密 → issuer/discovery SSRF 校验 → enabled+slug resolver → 缓存失效 → 回调/轮换/停用测试；先确定与平台设置的唯一真相源 |
| `protocol_capability_matrix`（整表） | `backend/sql/migrations/0005_protocol_translation.up.sql` | SQL 与 sqlc 生成方法存在，但无非生成业务调用；运行时使用 `proto.DefaultMatrix()` 内存矩阵 | tenant 策略服务 → DB loader/cache → 版本校验 → `CapabilityMatrix` 注入 → admin API/UI → 缓存失效与协议损失判别测试 |
| `protocol_policy_versions`（整表） | `backend/sql/migrations/0005_protocol_translation.up.sql` | SQL 与 sqlc 生成方法存在，但无业务调用；usage/协议转换不读取活动版本 | 版本发布事务 → 活动版本 loader/cache → 每请求 snapshot/provenance → usage/audit 持久化 → 回放与过期版本测试 |

## 4. `provider_accounts.cap_quota_*` 列族

这些列出现在旧的 sqlc 账号查询/生成行类型中，但生产选号使用 `ListEligibleAccountsByPoolGroup`，其投影和 `AccountSnapshot` 均没有这三个字段；没有 quota gate 或结算路径读取它们。

| 表/列名 | 建于哪个迁移 | 现状（无运行时消费） | 将来若启用需要补的链路 |
| --- | --- | --- | --- |
| `provider_accounts.cap_quota_total` | `backend/sql/migrations/0001_pool_routing.up.sql` | 旧查询投影存在；生产 snapshot/gate/结算不读 | admin API/UI → decimal 校验 → 原子累计/预占/结算 → AccountSnapshot/quota gate → exhaustion 状态与并发测试 |
| `provider_accounts.cap_quota_daily` | `0001_pool_routing.up.sql` | 同上 | 上述链路 + 日窗权威时区、窗口重置和并发跨窗测试 |
| `provider_accounts.cap_quota_weekly` | `0001_pool_routing.up.sql` | 同上 | 上述链路 + 周窗边界、重算/恢复和对账测试 |

## 5. moderation 违规费相关列

moderation 的筛查、审计和自动禁用能力已运行，但违规费没有接入。业务 Store 对费用列固定写 `0`，domain/HTTP 响应主动不暴露费用与 billing 关联，且没有路径产生 `fee_charged` 决策或调用 billing settler。

| 表/列名 | 建于哪个迁移 | 现状（无运行时消费） | 将来若启用需要补的链路 |
| --- | --- | --- | --- |
| `moderation_config.violation_fee_usd` | `backend/sql/migrations/0082_content_moderation.up.sql` | `UpsertConfig` 固定写零；配置 domain/HTTP 不读不写该值 | Owner money gate → admin 配置与权限/审计 → decimal 校验 → 版本化定价快照 → 判别性收费开关测试 |
| `moderation_log.violation_fee_usd` | `0082_content_moderation.up.sql` | 普通 moderation 日志固定写零；列表 domain/HTTP 丢弃该列 | 平台策略错误分类 → 幂等结算 → 实际费用写入 → 用户/运营可解释记录 → 退款/对账测试 |
| `moderation_log.billing_event_id` | `0082_content_moderation.up.sql` | 插入参数始终为空；domain/HTTP 丢弃该列 | billing 事件持久化后关联 → 同租户/幂等校验 → reconciliation/DLQ → 审计跳转与孤儿恢复测试 |

## 6. affiliate 等级进度

| 表/列名 | 建于哪个迁移 | 现状（无运行时消费） | 将来若启用需要补的链路 |
| --- | --- | --- | --- |
| `tier_progress`（整表） | `backend/sql/migrations/0034_community_invitation_referral.up.sql` | 仅迁移及迁移静态测试命中；推荐资格、奖励、查询与前端均不读写 | 资格/奖励事件 → tenant-aware 幂等聚合或可重建投影 → 并发升档 → 修正审计 → 用户/admin API/UI → 全量重算与回滚测试 |

## 7. 核验摘要

- `routes`：运行时 SQL 只投影核心匹配列；26 个候选列的 snake_case 搜索在业务/查询目录中只有独立的 `gateway.TimeoutConfig.drain_max_seconds` JSON 名命中，未发现 `routes` loader。
- `mimicry_policy`、`oidc_provider_configs`、`tier_progress`：除迁移、schema/文档与迁移静态测试外，无表名查询命中。
- 两张 protocol 表：查询文件与 sqlc 生成码存在；对生成方法名做非生成 Go 搜索为零业务调用。
- `cap_quota_*`：仅迁移、旧账号查询和 sqlc 生成字段命中；生产 `DBAccountSource` 不投影，selector/gate/settler 搜索无命中。
- `violation_fee_usd`：非生成业务代码仅两处固定 `decimal.Zero` 写入；`billing_event_id` 在 moderation 业务代码无读取/关联。

本文不声称这些能力永远不需要。它只记录 2026-07-14 的可复核现状，防止“schema 已存在”被误当成“功能已生效”。
