# 2026-04-30 外部架构提案 vs HUAKAI 当前状态 - Codex 独立评估

| 字段 | 值 |
| --- | --- |
| Status | Independent Codex evaluation |
| Owner directive | Owner 要求 Codex 独立比较外部第三方架构意见与 HUAKAI 当前状态 |
| Lane | evaluation |
| Prior lanes on this artifact | none |
| Clean-room note | 本任务只读 HUAKAI 内部 docs/code/sql，没有读取非 MIT 参考项目源代码 |
| Claude isolation | 未读取任何 `docs/plans/*-claude.md` 内容，也未读取 Claude 对本题的分析文件或聊天转述 |
| Observed regions | 17 个内部证据簇，尾部列出实际读取文件 |
| Inferences | 18 条，均基于已读 specs/code/sql/docs |
| Open questions | 9 个，集中在 refactor 顺序、模块边界和阶段优先级 |
| Test execution | 本评估未执行测试；只读取测试代码和 smoke/integration 测试约束 |

## 评估口径

- `Done` = HUAKAI 已有 Released spec，且 schema/code/test 至少有一条真实实现路径或强约束。
- `Partial` = HUAKAI 已有 Released spec 或 schema，但代码仍是 skeleton、smoke-only、单 provider、或只覆盖部分路径。
- `Missing` = 当前已读 docs/code/sql 中没有明确模块、spec、schema 或实现。
- `外建对` = 外部提案指出了 HUAKAI 当前确实需要补的东西。
- `HUAKAI 对` = HUAKAI 现有设计比外部提案更细、更安全，应该保留。
- `平行` = 两边方向一致，但命名、切分或优先级不同，不应机械替换。
- `证据引用` 使用相对路径；不使用上游代码、上游文件结构或上游实现细节。

> **2026-04-30 update after Slice 1 + Slice 4 landed**: row A05 (no `internal/router` package) is now SUPERSEDED — `backend/internal/router/{router.go,route_plan.go,default_router.go,router_test.go}` exist with `Router` interface + `RoutePlan` types + `DefaultRouter` minimum impl. Remaining gaps: real fallback chain (Slice 2 needs Registry); attempt_id schema column (Slice 3 migration). The matrix below was authored before Slice 1; treat A05 as "skeleton landed, fallback logic still gap".

## A. 现状自评（HUAKAI vs 外建提案）

| # | 外建概念 | HUAKAI 现状 | Done / Partial / Missing | 评级 | 证据 |
| --- | --- | --- | --- | --- | --- |
| A01 | "不是 fork，做新的 AI Gateway" | HUAKAI 的项目定位已经是 clean-room AI Gateway + Account Hub + Admin Ops Platform，强调 Sub2API 底座加综合产品宽度。 | Done | 平行 | `docs/01_PROJECT_BRIEF.md`, `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`, `docs/decisions/DR-000-clean-room-methodology.md` |
| A02 | 三层大链路 Client → Gateway → Auth → Router → Pool → Adapter → Upstream | HUAKAI 当前是 5 层：HTTP entry、Tx1 ClaimGate、Pool Selector、Auth+Proto+Forwarder、Tx2 Settler。外建链路更直观，但 HUAKAI 把 money path 前后事务拆得更硬。 | Done | HUAKAI 对 | `docs/02_HUAKAI_FUSION_ARCHITECTURE.md`, `docs/specs/observability-billing.md`, `backend/cmd/gateway/main.go` |
| A03 | Resource Pool 是所有上游资源库存系统 | HUAKAI 已有 `internal/pool`、`provider_accounts`、`pool_groups`、`channels`、`pool_slot_acquisitions`、sticky bindings，但包名仍是 pool，不是显式 `resourcepool`。 | Partial | 平行 | `docs/specs/pool-routing.md`, `backend/internal/pool/selector.go`, `backend/sql/migrations/0001_pool_routing.up.sql` |
| A04 | Resource Pool 管 Provider/Pool/Resource/Credential/Capability/Lease/HealthEvent | Provider/Pool/Account/Credential/Capability/Lease/Health 基础字段存在；HealthEvent 作为独立事件流不完整，health/rate/auth audit 分散。 | Partial | 外建对 | `backend/sql/migrations/0001_pool_routing.up.sql`, `0004_rate_limiting.up.sql`, `0006_upstream_credential_management.up.sql` |
| A05 | Router Engine 生成 RoutePlan | HUAKAI 现在没有 `internal/router` 和显式 `RoutePlan` 类型；选择逻辑在 `internal/pool.DefaultSelector` 和 gateway handler 中。 | Partial | 外建对 | `backend/internal/pool/selector.go`, `backend/internal/gatewayhttp/chat_completions_handler.go` |
| A06 | Router 支持 fallback、retry、load balance、conditional routing | Spec 覆盖 fallback/retry/selection/reason；代码当前只做 pool selection、wait plan、exclusion gate 的一部分，没有完整 retry orchestrator 和 conditional policy engine。 | Partial | 外建对 | `docs/specs/pool-routing.md`, `docs/specs/streaming-forwarder.md`, `backend/internal/pool/selector.go` |
| A07 | Router 不读明文凭证 | HUAKAI 方向正确：pool selector 通过 `AuthCredentialGate` 调 token provider，不直接持有 credential；但当前 DB schema 仍是 `credentials jsonb`，不是强制 encrypted bytea。 | Partial | 外建对 | `backend/internal/pool/auth_credential_gate.go`, `backend/internal/auth/antigravity_token_provider.go`, `backend/sql/migrations/0001_pool_routing.up.sql` |
| A08 | Billing + Observability Ledger 是 append-only 事实账本 | HUAKAI 在 spec/schema 上比外建更深：billing claims、billing_events、usage_records、reconciliation_events、adjustments、DLQ、outbox 都有设计。代码 Tx2 已写 usage + event + claim + slot release，但多维扣费/DLQ/reconciliation 仍未完成。 | Partial | HUAKAI 对 | `docs/specs/observability-billing.md`, `docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md`, `backend/internal/billing/settler.go`, `backend/sql/migrations/0002_observability_billing.up.sql` |
| A09 | request_id / attempt_id / lease_id 串联 | HUAKAI 有 claim id、logical_request_id、attempt_seq、acquisition_token、lease_expires_at；HTTP middleware 有 request id，但 usage/billing schema 未显式统一 `request_id` / `attempt_id` / `lease_id` 命名。 | Partial | 外建对 | `backend/internal/billing/billing.go`, `backend/sql/queries/billing_claims.sql`, `backend/sql/queries/pool_slot_acquisitions.sql`, `backend/cmd/gateway/main.go` |
| A10 | Claim-Gate Pattern B：先候选，attempt 时原子 claim | HUAKAI 已明确 Pattern B：Tx1 claim row 先 reserving，Pool acquire 后写回 provider_account_id/acquisition_token。代码有 DBClaimGate 和 DBSlotManager。 | Done | HUAKAI 对 | `docs/specs/pool-routing.md`, `docs/specs/observability-billing.md`, `backend/internal/pool/db_claim_gate.go`, `backend/internal/pool/db_slot_manager.go` |
| A11 | 9-Gate: Tenant Gate | Spec 已有 tenant gate，schema every primary table tenant-aware，代码 DB queries 基本带 tenant_id；DefaultGateChain 的 TenantGate 仍是 AllowAll unless injected。 | Partial | 平行 | `docs/decisions/DR-001-multi-tenancy.md`, `backend/internal/pool/gates.go`, `backend/sql/queries/pool_accounts.sql` |
| A12 | 9-Gate: Model Gate | Spec 有 model-support gate，schema 有 model_allow_list 和 model_routing_overrides；代码当前 DBAccountSource 不把 model_allow_list 纳入 AccountSnapshot 过滤，默认 gate 仍 AllowAll。 | Partial | 外建对 | `docs/specs/pool-routing.md`, `backend/sql/migrations/0001_pool_routing.up.sql`, `backend/internal/pool/db_account_source.go` |
| A13 | 9-Gate: Capability Gate | Spec 和 protocol capability matrix 已有；代码 capability gate 默认 AllowAll，protocol matrix 是内存/SQL基础但未接入 request path。 | Partial | 外建对 | `docs/specs/protocol-translation.md`, `backend/internal/proto/capability_matrix.go`, `backend/internal/pool/gates.go` |
| A14 | 9-Gate: Budget Gate | HUAKAI 的 Budget/Billing Gate 比外建更严，Tx1/Tx2 specs 有 5-effect 和 pricing version pin；代码 Tx1 只插 claim/predicted_cost，尚未做真实 5 维预扣。 | Partial | HUAKAI 对 | `docs/specs/observability-billing.md`, `backend/internal/billing/claim_gate.go`, `backend/internal/billing/settler.go` |
| A15 | 9-Gate: Policy Gate | HUAKAI 有 route/pool policy columns、protocol_policy_versions；缺独立 policy package 和声明式 reload。 | Partial | 外建对 | `backend/sql/migrations/0001_pool_routing.up.sql`, `0005_protocol_translation.up.sql`, `backend/internal/config/config.go` |
| A16 | 9-Gate: Provider Gate | Provider catalog/table 已存在；provider adapter registry 尚未真正落地。 | Partial | 外建对 | `backend/sql/migrations/0001_pool_routing.up.sql`, `backend/pkg/adapter/adapter.go`, `backend/internal/proto/proto.go` |
| A17 | 9-Gate: Health Gate | Spec/schema 有 health_state、rate_limit、overload、temp_unsched；代码 health gate 默认 AllowAll，DB eligibility query 只过滤 operational/degraded。 | Partial | 平行 | `docs/specs/rate-limiting.md`, `backend/sql/queries/pool_accounts.sql`, `backend/internal/pool/gates.go` |
| A18 | 9-Gate: Resource Gate | cap_concurrency/in_flight_count/pool_slot_acquisitions 已实现 DB-backed acquire；但 serialization retry 和 queue depth/wait queue 仍待补。 | Partial | HUAKAI 对 | `backend/internal/pool/db_slot_manager.go`, `backend/sql/queries/pool_accounts.sql`, `backend/sql/queries/pool_slot_acquisitions.sql` |
| A19 | 9-Gate: Claim Gate | Tx1 ClaimGate 和 Tx2 Settler 已有 PG 实现和 integration tests；预扣真实余额/订阅/API-key quota 仍不完整。 | Partial | HUAKAI 对 | `backend/internal/billing/claim_gate.go`, `backend/internal/billing/settler.go`, `backend/internal/billing/*_integration_test.go` |
| A20 | Model Registry 映射用户模型名到内部能力 | HUAKAI 有 protocol capability matrix、model_allow_list、model_routing_overrides，但没有独立 Model Registry 包和统一 internal model capability entity。 | Partial | 外建对 | `docs/specs/protocol-translation.md`, `backend/sql/migrations/0001_pool_routing.up.sql`, `0005_protocol_translation.up.sql` |
| A21 | Provider Adapter 转换请求格式并调用上游 | HUAKAI 有 HCSF、Anthropic SSE upstream adapter、adapter interfaces；Chat Phase C 仍用 mock upstream，ClientAdapter nil，真实 provider HTTP adapter 未接。 | Partial | 外建对 | `backend/internal/proto/anthropic_sse.go`, `backend/internal/gatewayhttp/mock_upstream.go`, `backend/internal/gatewayhttp/chat_completions_handler.go` |
| A22 | Usage Extractor 解析 tokens/cost/duration/status | StreamForwarder 已抽 usage accumulator、end_class、duration；cost 现在 Phase C hardcoded 0.01，不是真 pricing parser。 | Partial | 外建对 | `backend/internal/gateway/forwarder.go`, `backend/internal/gateway/forwarder_types.go`, `backend/internal/gatewayhttp/chat_completions_handler.go` |
| A23 | Ledger settle/refund | Settle/Abort 有实现；refund/adjustment/reconciliation schema 有，但服务逻辑缺。 | Partial | HUAKAI 对 | `backend/internal/billing/settler.go`, `backend/sql/migrations/0002_observability_billing.up.sql` |
| A24 | 失败释放资源、标记健康事件、下一条 fallback route | Abort/slot release 有；rate service skeleton 未接入 forwarder/gateway；retry/fallback orchestrator 未实现完整 attempt loop。 | Partial | 外建对 | `backend/internal/billing/settler.go`, `backend/internal/rate/rate.go`, `backend/internal/gatewayhttp/chat_completions_handler.go` |
| A25 | 目录结构 internal/{gateway,auth,registry,router,resourcepool,adapters,ledger,usage,policy,admin} | HUAKAI 当前有 `gateway/auth/pool/billing/obs/proto/rate/config/db/gatewayhttp`；缺 registry/router/resourcepool/adapters/ledger/usage/policy/admin 的显式边界。 | Partial | 外建对 | `backend/internal/*`, `backend/pkg/adapter/adapter.go` |
| A26 | MVP 阶段 1：干净核心 + Fake Provider | HUAKAI 已在 Phase C 做 real PG money path + mock upstream；不是纯 fake core。 | Partial | HUAKAI 对 | `backend/cmd/gateway/main.go`, `backend/internal/gatewayhttp/mock_upstream.go`, `backend/cmd/gateway/smoke_test.go` |
| A27 | MVP 阶段 2：真实 provider + usage parser + fallback/retry | HUAKAI usage parser 部分有；真实 provider、retry/fallback、pricing parser 未完成。 | Partial | 外建对 | `backend/internal/proto/anthropic_sse.go`, `backend/internal/rate/rate.go`, `backend/pkg/adapter/adapter.go` |
| A28 | MVP 阶段 3：接 sub2api 底座作为 AccountPoolAdapter | HUAKAI 因 clean-room 不能直接接非 MIT 源实现；可以做 AccountPoolAdapter 语义边界，但实现必须本地 clean-room。 | Missing | HUAKAI 对 | `docs/decisions/DR-000-clean-room-methodology.md`, `docs/10_RISK_REGISTER.md` |
| A29 | MVP 阶段 4：充值、订阅、套餐、admin dashboard、日志检索、成本报表 | HUAKAI parity matrix 已把 payment/admin/ops 作为必需；代码 admin routes mostly 501，obs repository skeleton。 | Partial | 外建对 | `docs/03_FEATURE_PARITY_MATRIX.md`, `backend/cmd/gateway/main.go`, `backend/internal/obs/obs.go` |
| A30 | MVP 阶段 5：声明式 route policy、缓存、A/B、成本优化、SLA、企业多租户 | HUAKAI spec/matrix 覆盖 declarative config/cache/SLO/SaaS，但多数是 Open/Mandatory Roadmap。 | Partial | 平行 | `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/decisions/DR-002-product-editions.md`, `docs/specs/api-contract.md` |
| A31 | New-API 适合作为运营壳 | HUAKAI 同意需要用户、key、额度、渠道、充值、日志、分组；但外建低估了 HUAKAI clean-room 和两版商业模型约束。 | Partial | 平行 | `docs/01_PROJECT_BRIEF.md`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/decisions/DR-002-product-editions.md` |
| A32 | LiteLLM 学 model standardization/provider adapter/fallback/budget/team key | HUAKAI 已把 provider breadth、protocol capability matrix、budget/tenant 作为核心方向；实际 adapter registry 和 team/key 权限仍缺。 | Partial | 外建对 | `docs/01_PROJECT_BRIEF.md`, `docs/specs/protocol-translation.md`, `backend/pkg/adapter/adapter.go` |
| A33 | Portkey 学 fallback/retry/load balance/conditional/cache/virtual key | HUAKAI matrix 覆盖 F-GW-004、F-CACHE、F-RBAC/F-KEY；代码还没实现完整 virtual key/user auth/key management。 | Partial | 外建对 | `docs/03_FEATURE_PARITY_MATRIX.md`, `backend/internal/auth/smoke_resolver.go`, `backend/cmd/gateway/main.go` |
| A34 | Helicone 学透明代理日志 request/response/usage/trace/cost/latency/prompt | HUAKAI F-OBS-001 已比透明日志更 money-grade；但 request/response cold store、trace export、operator query repo 未实现。 | Partial | HUAKAI 对 | `docs/specs/observability-billing.md`, `backend/internal/obs/obs.go`, `backend/sql/migrations/0002_observability_billing.up.sql` |
| A35 | Envoy AI Gateway 学声明式配置 | HUAKAI parity matrix 有 F-CONFIG-001/F-ARCH/F-DEPLOY；当前 runtime 仍 env + DB columns，没有 config-as-code reload。 | Missing | 外建对 | `docs/03_FEATURE_PARITY_MATRIX.md`, `backend/internal/config/config.go` |
| A36 | all-api-hub 只学账号资产管理/模型同步/导出配置 | HUAKAI 已以 Plugin/Mandatory Roadmap 处理 OPS/EXPORT/SYNC，并有明文凭证风险登记；方向一致。 | Partial | 平行 | `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/10_RISK_REGISTER.md`, `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` |
| A37 | 不复制凭证抓取/自动化账号操作/站点绕过逻辑 | HUAKAI clean-room 和安全风险规则支持这个边界；但未来插件必须明确禁止越界。 | Done | HUAKAI 对 | `docs/decisions/DR-000-clean-room-methodology.md`, `docs/10_RISK_REGISTER.md`, `docs/03_FEATURE_PARITY_MATRIX.md` |
| A38 | Adapter 不能绕过 Ledger | HUAKAI 当前 handler 是 Forwarder 后同步 Settler，无队列绕过；但未来 provider adapter registry 需要接口级强制。 | Partial | 外建对 | `backend/internal/gatewayhttp/chat_completions_handler.go`, `docs/specs/observability-billing.md` |
| A39 | Billing 只能事件结算 | HUAKAI schema 支持 append-only events/adjustments；代码 Tx2 仍直接更新 claim status 和写 event，合理但需要避免后续余额裸写绕过 ledger。 | Partial | 平行 | `backend/internal/billing/settler.go`, `backend/sql/migrations/0002_observability_billing.up.sql` |
| A40 | Credential 永远不进日志 | HUAKAI 有 sanitizer、audit redaction、token leakage tests/spec；DB at-rest encryption 未强制，schema comment 说 redacted at rest 但实际列是 jsonb。 | Partial | 外建对 | `docs/specs/upstream-credential-management.md`, `backend/internal/auth/sanitizer.go`, `backend/sql/migrations/0001_pool_routing.up.sql` |
| A41 | 每次 request 有 request_id | chi middleware 装了 request ID，但业务表没有完整传播 `request_id`；audit 表有 request_id column，usage_records 没有。 | Partial | 外建对 | `backend/cmd/gateway/main.go`, `backend/sql/migrations/0001_pool_routing.up.sql`, `0006_upstream_credential_management.up.sql` |
| A42 | 每次 upstream attempt 有 attempt_id | HUAKAI 使用 attempt_seq，缺独立 attempt table/attempt_id；对 fallback/retry 追踪不够。 | Partial | 外建对 | `backend/internal/billing/billing.go`, `backend/sql/migrations/0002_observability_billing.up.sql` |
| A43 | 每次资源占用有 lease_id | HUAKAI 有 pool_slot_acquisitions.id 和 acquisition_token，不叫 lease_id；语义上接近，但 lease 生命周期/heartbeat worker 未完成。 | Partial | 平行 | `backend/sql/migrations/0001_pool_routing.up.sql`, `backend/sql/queries/pool_slot_acquisitions.sql` |
| A44 | Admin → Auth/Router/Pool/Ledger | OpenAPI/API contract 有 admin endpoints；Go routes 大多 501，obs repository skeleton，无真实 admin package。 | Partial | 外建对 | `docs/specs/api-contract.md`, `backend/cmd/gateway/main.go`, `backend/internal/obs/obs.go` |

### A 小结

- 外建提案的最大价值不是重画架构图，而是提醒 HUAKAI 需要显式补 `router/registry/policy/adapters/admin` 等模块边界。
- HUAKAI 当前最强的地方是外建没有展开的：Tx1/Tx2 money path、strict clean-room、tenant-aware PostgreSQL、protocol_loss、stream end taxonomy、credential CAS/storm control。
- 代码状态不能按 spec 乐观估计：pool/billing/gateway 有真实 Phase C path，auth/rate/obs/admin/provider adapters 仍明显未完成。
- 因此当前应评为：架构方向 Done，money-path core Partial-to-strong，运营壳和 provider breadth Partial-to-missing。

## B. 外建说对了的（HUAKAI 应该采纳）

### B01. 建立显式 `Router Engine` 和 `RoutePlan`

- 优先级：HIGH
- 工作量：2-4 天，先做接口和数据结构，后接 retry/fallback loop。
- 影响范围：`backend/internal/gatewayhttp/`, `backend/internal/pool/`, 新 `backend/internal/router/`, `docs/specs/pool-routing.md`, `docs/specs/streaming-forwarder.md`, `docs/specs/api-contract.md`
- 采纳理由：当前 route/orchestrate 逻辑散在 handler 与 selector 中，Phase C 可以接受，但真实 fallback/retry/conditional routing 会把 handler 变成状态机。
- 具体动作：定义 `RoutePlan`，包含 candidates、attempt budget、failover reasons、selected pool_group、required capabilities、pricing policy version。
- 具体动作：`Router Engine` 只读 policy/capability/price snapshot，不读 credential secret。
- 具体动作：Pool 只执行 admission/lease，不决定 retry 策略。

### B02. 建立 `Model Registry`

- 优先级：HIGH
- 工作量：2-3 天，先 DB/Go 只读 registry，后接 admin UI。
- 影响范围：新 `backend/internal/registry/`, `backend/sql/migrations/0007_*`, `backend/sql/queries/*`, `backend/internal/proto/`, `backend/internal/pool/`
- 采纳理由：HUAKAI 现在有 model_allow_list、protocol matrix、requested_model，但缺一个统一回答 "这个用户模型名需要哪些能力、价格、上下文窗口、协议限制" 的地方。
- 具体动作：把 external model alias、internal model capability、provider model id、context window、pricing class、protocol requirements 放进 versioned registry。
- 具体动作：RoutePlan 引用 registry snapshot version，UsageRecord 记录 version，避免回放时模型含义漂移。

### B03. 将 `Resource Pool` 作为语义边界，而不是立刻大改包名

- 优先级：MED
- 工作量：1-2 天文档/接口，3-5 天代码整理。
- 影响范围：`backend/internal/pool/`, `backend/sql/migrations/0001_pool_routing.up.sql`, `docs/specs/pool-routing.md`
- 采纳理由：外建的 Resource Pool 定义比 "pool selector" 更完整，覆盖 inventory、credential state、lease、health。
- 不建议立刻做：把 `internal/pool` 全面改名 `resourcepool`。当前有真实 tests 和 integrations，改名收益低，冲突高。
- 建议做法：保留包名，新增 doc/API：`pool` 是 Resource Pool 的实现包；Router 不越界修改 pool state。

### B04. 固化 request_id / attempt_id / lease_id 三 ID 链

- 优先级：HIGH
- 工作量：1 天 spec/API，1-2 天 schema/code migration，测试另算。
- 影响范围：`backend/internal/gatewayhttp/`, `backend/internal/billing/`, `backend/internal/pool/`, `backend/sql/migrations/*`, `docs/specs/api-contract.md`, `docs/specs/observability-billing.md`
- 采纳理由：当前有 logical_request_id、claim_id、attempt_seq、acquisition_token，但 operator 调试链路不够直观。
- 具体动作：`request_id` 从 chi middleware 进入 claim、usage、billing_event、audit。
- 具体动作：新增 attempt identity，至少是 `(claim_id, attempt_seq)` 的稳定字符串；后续可独立 table。
- 具体动作：明确 `lease_id = pool_slot_acquisitions.id`，`lease_token = acquisition_token`，避免概念混乱。

### B05. Router/Pool/Adapter/Ledger 权限边界写成不变量

- 优先级：HIGH
- 工作量：0.5-1 天 docs，1-2 天接口断言和 tests。
- 影响范围：`docs/specs/pool-routing.md`, `docs/specs/observability-billing.md`, `backend/internal/router/`, `backend/internal/pool/`, `backend/internal/proto/`, `backend/internal/billing/`
- 采纳理由：外建列出的 "Router 不能读明文凭证、Adapter 不能绕过 Ledger、Credential 不进日志" 正好应变成 HUAKAI reviewer gate。
- 具体动作：新增 `docs/specs/_invariants/cross-module-boundaries.md`。
- 具体动作：代码层让 Adapter 只返回 `UsageRecordDraft`/stream result，不持有 Settler。
- 具体动作：handler/orchestrator 负责强制 Tx2，不让 provider adapter 自己决定是否记账。

### B06. Declarative route policy 作为 Phase E/F 后的模块，而不是现在抢跑

- 优先级：MED
- 工作量：3-7 天，取决于 YAML reload、UI wizard、DB versioning 范围。
- 影响范围：`backend/internal/config/`, 新 `backend/internal/policy/`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/specs/api-contract.md`
- 采纳理由：当前 DB columns 已承载很多 policy，但可读性、迁移、审计和跨部署复现不足。
- 执行顺序：先 RoutePlan，再 policy snapshot，再 declarative authoring。
- 风险：过早上 YAML 会把 money-path 未完成的问题藏在配置灵活性后面。

### B07. Provider adapter registry 要成为 L1/L2 重点

- 优先级：HIGH
- 工作量：第一批 1-2 周，按 provider/protocol 分 vertical slice。
- 影响范围：`backend/pkg/adapter/`, `backend/internal/proto/`, `backend/internal/gateway/`, `backend/internal/auth/`, `docs/01_PROJECT_BRIEF.md`
- 采纳理由：Owner 的 North Star 明确说 Sub2API 底座好，HUAKAI 差异化是 provider/API/model breadth。
- 当前差距：只有 Anthropic SSE upstream parsing 和 mock upstream；OpenAI Chat client output 还不是 Phase C scope。
- 具体动作：做 adapter registry map，注册 client adapters 和 upstream adapters；每个 adapter 必须声明 capability matrix cells。

### B08. Observability Ledger 的 query/replay/admin surface 要尽快补

- 优先级：HIGH
- 工作量：3-5 天先 usage/claims/audit query，DLQ/replay 另加 2-4 天。
- 影响范围：`backend/internal/obs/`, `backend/cmd/gateway/main.go`, `docs/specs/api-contract.md`, `backend/sql/queries/*`
- 采纳理由：外建强调 transparent logs 是对的；HUAKAI 现在写入强于查询，operator 看不到就等于半成品。
- 具体动作：实现 `obs.Repository`，先支持 usage_records、billing_events、claims、pool/rate/auth audit 的分页查询。
- 具体动作：DLQ replay 在 Phase 4.5 前至少要有 list + manual replay placeholder，不要只留 schema。

### B09. Admin Ops 不能长期停留在 501

- 优先级：HIGH
- 工作量：1-2 周最小运营面。
- 影响范围：`backend/cmd/gateway/main.go`, 新 `backend/internal/admin/`, `docs/specs/api-contract.md`, future frontend。
- 采纳理由：Owner Model 1 要商业化，必须能发 key、看 usage、看 billing、管理 provider accounts、处理失败。
- 当前证据：routes 已挂 14 个 admin endpoints，但大部分 `notImplemented`。
- 最小完成线：provider account list/detail、usage query、claim query、audit query、pool list、clear cooldown。

### B10. all-api-hub 概念只走插件/安全等价

- 优先级：LOW-to-MED
- 工作量：2-4 天写 plugin contract，具体插件后置。
- 影响范围：`docs/03_FEATURE_PARITY_MATRIX.md`, future `backend/internal/plugin/`, admin UI。
- 采纳理由：外建边界正确：只学账号资产管理/模型同步/导出配置概念，不学凭证抓取和绕站逻辑。
- 具体动作：把 export/config-sync/asset dashboard 都定义为 operator-only plugin，默认关，审计强制开。

### B11. Fallback/retry 需要 per-tenant budget

- 优先级：HIGH
- 工作量：2-4 天。
- 影响范围：新 `backend/internal/router/`, `backend/internal/rate/`, `backend/internal/billing/`, `docs/specs/streaming-forwarder.md`
- 采纳理由：外建强调 fallback/retry；HUAKAI 风险登记也指出 retry 可能放大成本和 DOS。
- 具体动作：RoutePlan 包含 max attempts、retryable end_class、per-tenant retry budget、per-request exclusion set。
- 具体动作：每个 attempt 都写 attempt reason，最终 usage/billing 只 settle 成功/终态 attempt。

### B12. Credential storage 必须从 "不进日志" 升到 "强制加密 at rest"

- 优先级：HIGH
- 工作量：需要 Owner 确认，schema/auth core 高风险；估计 3-6 天含 migration 和 tests。
- 影响范围：`backend/sql/migrations/0001_pool_routing.up.sql`, `backend/internal/auth/`, `backend/sql/queries/auth_credentials.sql`
- 采纳理由：外建 "Credential 永远不进日志" 只是底线；HUAKAI 风险登记已指出明文凭证不能继承。
- 当前差距：schema 使用 `credentials jsonb`，comment 说 redacted at rest，但并非加密字段。
- 决策点：是否在 0007 做 additive `credentials_encrypted bytea`，还是 Phase E 后统一迁移。

## C. HUAKAI 走得比外建深的（保留）

### C01. Clean-room 不是附属流程，是架构约束

- 保留理由：外建只说不 fork，但没有方法论。
- HUAKAI 已有 DR-000，明确 Option B default 和 Option C carve-out。
- HUAKAI 还登记 R-LIC-001/R-LIC-002，覆盖 AGPL/LGPL/GPL 风险和跨 session 污染风险。
- 设计依据：`docs/decisions/DR-000-clean-room-methodology.md`, `docs/10_RISK_REGISTER.md`
- 执行影响：任何 "接 sub2api 底座" 都只能接 clean-room 语义边界，不能接源代码或文件结构。

### C02. Strict Authenticity Gate 比外建 MVP 节奏更硬

- 保留理由：外建阶段规划偏产品工程，缺 "每个 L1/L2 必须 Released spec" 的硬门。
- HUAKAI DR-008 明确选择 Strict，慢但真实。
- 设计依据：`docs/decisions/DR-008-methodology-choice-strict-authenticity.md`
- 执行影响：不能为了做 Router/Registry 重构跳过 spec/review。

### C03. Tenant-aware from day 1

- 保留理由：外建提到 enterprise multi-tenant 在高级阶段，但 HUAKAI 已把 tenant_id 放进第一迁移。
- DR-001 决定 MVP 单默认 tenant，但 schema 从第一天多租户。
- 设计依据：`docs/decisions/DR-001-multi-tenancy.md`, `backend/sql/migrations/0001_pool_routing.up.sql`
- 执行影响：任何新 router/registry/policy table 必须带 `tenant_id`。

### C04. Personal Edition + SaaS Edition 两业务模型

- 保留理由：外建 "New-API 运营 SaaS 壳" 太单一；HUAKAI 有 Model 1 自用基座卖 API 和 Model 2 SaaS。
- DR-002 明确 Personal Edition 也可以商业化，payment optional but available。
- 设计依据：`docs/decisions/DR-002-product-editions.md`, `docs/01_PROJECT_BRIEF.md`
- 执行影响：admin/payment 不能被推到很晚才考虑，否则 Owner 不能跑 Model 1。

### C05. PostgreSQL 是 correctness 选择，不只是存储选择

- 保留理由：外建没有讨论 DB 隔离和事务正确性。
- HUAKAI DR-006 要求 PostgreSQL-only、sqlc、显式事务、row locks、no ORM hiding tx boundaries。
- 设计依据：`docs/decisions/DR-006-database.md`, `backend/sqlc.yaml`, `backend/internal/db/pgconn.go`
- 执行影响：RoutePlan/Registry/Policy 不能用临时内存 map 当 production source of truth。

### C06. Tx1/Tx2 money path 已比 "Ledger" 概念更细

- 保留理由：外建只说预扣/结算；HUAKAI 有 Tx1 Reserve、Tx2 Reconcile、idempotency fingerprint、orphan sweep、DLQ、append-only reconciliation。
- 设计依据：`docs/specs/observability-billing.md`, `docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md`
- 代码依据：`backend/internal/billing/claim_gate.go`, `backend/internal/billing/settler.go`
- 保留要求：任何 Router refactor 不得绕开 Tx1/Tx2。

### C07. Pattern B 已经不是建议，是 spec + code seam

- 保留理由：外建提出 Pattern B 正确，但 HUAKAI 已有更具体实现。
- DBClaimGate 以 `(id, tenant_id, status='reserving')` 写回 provider_account_id/acquisition_token。
- DBSlotManager 以 serializable tx increment in_flight + insert slot acquisition。
- 设计依据：`backend/internal/pool/db_claim_gate.go`, `backend/internal/pool/db_slot_manager.go`, `backend/sql/queries/billing_claims.sql`
- 保留要求：不要把 claim 和 slot acquire 合并成一个大 Router 事务。

### C08. Streaming forwarder failure taxonomy 更完整

- 保留理由：外建只说 latency/trace/usage；HUAKAI 定义了 stream end class、bounded scanner、bounded drain、client disconnect partial settlement。
- 设计依据：`docs/specs/streaming-forwarder.md`
- 代码依据：`backend/internal/gateway/forwarder.go`, `backend/internal/gateway/forwarder_types.go`, `backend/internal/gateway/event_scanner.go`
- 保留要求：真实 provider adapter 接入时必须保留 end_class，不要只返回 success/fail。

### C09. Protocol translation 有 capability matrix 和 protocol_loss

- 保留理由：外建的 provider adapter 概念偏接口转换，HUAKAI 已规定每个损失都要 operator-visible。
- 设计依据：`docs/specs/protocol-translation.md`
- 代码依据：`backend/internal/proto/capability_matrix.go`, `backend/sql/migrations/0005_protocol_translation.up.sql`
- 保留要求：Model Registry 要引用 capability matrix，不能用简单 model string map 替代。

### C10. Upstream credential management 比 "账号/订阅/OAuth" 更细

- 保留理由：HUAKAI 有 token cache、refresh skew、CAS、storm budget、sanitizer、mimicry legal gate。
- 设计依据：`docs/specs/upstream-credential-management.md`
- 代码依据：`backend/internal/auth/antigravity_token_provider.go`, `backend/internal/auth/storm_controller.go`, `backend/sql/migrations/0006_upstream_credential_management.up.sql`
- 保留要求：Resource Pool 不能吞掉 auth package；Pool 只能调用 TokenProvider seam。

### C11. Rate-limit/cooldown taxonomy 更深

- 保留理由：外建说 health/cooldown；HUAKAI F-RATE-001 定义 429/529/401/403/custom codes、5h/7d/RPM/TPM、overload、model-only cooldown、cascade clear。
- 设计依据：`docs/specs/rate-limiting.md`, `backend/sql/migrations/0004_rate_limiting.up.sql`
- 当前差距：`backend/internal/rate/rate.go` 是 skeleton。
- 保留要求：不要把 Health Gate 简化成 boolean healthy。

### C12. API contract 已锁外部 HTTP surface

- 保留理由：外建没有 OpenAPI/contract 层。
- HUAKAI API contract 已覆盖 3 client relay endpoints 和 14 admin endpoints，标准错误 envelope。
- 设计依据：`docs/specs/api-contract.md`
- 执行影响：新增 Router/Registry/Admin 时应更新 OpenAPI，而不是只改 Go handlers。

### C13. Phase C 已有真实 PG smoke path

- 保留理由：外建阶段 1 建议 fake provider；HUAKAI 已把真实 PostgreSQL Tx1/pool/forwarder/Tx2 跑通到 smoke test 级别。
- 代码依据：`backend/cmd/gateway/main.go`, `backend/internal/gatewayhttp/chat_completions_handler.go`, `backend/cmd/gateway/smoke_test.go`
- 限制：上游是 mock Anthropic SSE，auth 是 smoke bearer，client response 不是 OpenAI chat chunk。
- 保留要求：下一步是增量补真实 provider/adapter，不是推倒重来。

### C14. Schema 已包含未来能力钩子

- 保留理由：外建用模块名表达能力；HUAKAI 已在 migrations 中落了多处未来钩子。
- 例子：`usage_record_dlq`, `usage_record_reconciliation_events`, `billing_ledger_adjustments`, `protocol_capability_matrix`, `oauth_storm_budget`, `mimicry_policy`。
- 设计依据：`backend/sql/migrations/0002_observability_billing.up.sql`, `0005_protocol_translation.up.sql`, `0006_upstream_credential_management.up.sql`
- 保留要求：不要重复建平行表；新模块优先复用现有 schema。

### C15. Feature parity matrix 已覆盖外建 "借鉴边界"

- 保留理由：外建列了 New-API/LiteLLM/Portkey/Helicone/Envoy/all-api-hub 的学习点；HUAKAI 03 矩阵已逐项 disposition。
- 设计依据：`docs/03_FEATURE_PARITY_MATRIX.md`
- 执行影响：外建没有新增方向本身，主要新增的是模块边界和阶段重排建议。

## D. 外建说漏 / 说错的（不采纳的理由）

### D01. 不采纳 "三层架构替换 HUAKAI 5 层" 的理解

- 反对理由：外建图适合沟通，但不能替换 HUAKAI 的 Tx1/Tx2 5 层执行模型。
- HUAKAI 依据：`docs/02_HUAKAI_FUSION_ARCHITECTURE.md`, `docs/specs/observability-billing.md`
- 实操结论：可以引入 Router Engine，但必须放在 Tx1 Reserve 之后、Pool claim 之前，且 Tx2 仍是强制终点。

### D02. 不采纳 "sub2api 作为 AccountPoolAdapter" 的字面接入

- 反对理由：clean-room 和 LGPL/AGPL 风险要求行为等价，不允许直接接源码/结构。
- HUAKAI 依据：`docs/decisions/DR-000-clean-room-methodology.md`, `docs/10_RISK_REGISTER.md`
- 实操结论：可以定义 `AccountPoolAdapter` 接口，但实现必须是 HUAKAI 本地 clean-room code。

### D03. 外建低估了 Billing Ledger 的 failure modes

- 反对理由：只说 append-only 不够；money path 需要 DLQ、outbox lag、orphan sweep、pending reconciliation、audit event survives usage failure。
- HUAKAI 依据：`docs/specs/observability-billing.md`, `docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md`
- 实操结论：Ledger 不应被压缩成一个通用 logging module。

### D04. 外建的 "Helicone 透明代理日志" 不足以替代 HUAKAI observability

- 反对理由：HUAKAI 的核心不是只看 request/response，而是 usage/cost/billing_event/claim/slot/attempt 的一致性。
- HUAKAI 依据：`backend/sql/migrations/0002_observability_billing.up.sql`, `backend/internal/billing/settler.go`
- 实操结论：可以采纳 trace/cost/latency UI，但底层仍以 Tx2 ledger 为准。

### D05. 外建没有强调 PostgreSQL transactional correctness

- 反对理由：Router/Pool/Ledger 的边界如果没有 DB isolation 约束，会重现并发超卖和 double settlement。
- HUAKAI 依据：`docs/decisions/DR-006-database.md`
- 实操结论：任何 refactor 必须保持 sqlc + explicit transaction + tenant-scoped queries。

### D06. 外建没有处理 user-facing auth/key/payment 的实际商业闭环

- 反对理由：它说 New-API 运营壳成熟，但没有点出 HUAKAI 代码目前只有 smoke auth，admin/payment/key issuance 尚未实现。
- HUAKAI 依据：`docs/03_FEATURE_PARITY_MATRIX.md`, `backend/internal/auth/smoke_resolver.go`, `backend/cmd/gateway/main.go`
- 实操结论：不能只做 Router/Pool；Owner Model 1 需要 API key issuance、quota、payment/admin。

### D07. 外建的 "Adapter" 概念缺 protocol_loss 和 capability enforcement

- 反对理由：简单 adapter 很容易 silent drop tool/image/reasoning/cache semantics。
- HUAKAI 依据：`docs/specs/protocol-translation.md`, `backend/internal/proto/capability_matrix.go`
- 实操结论：Provider adapter 必须注册 capability matrix 和 loss behavior；Router 必须读取 capability requirements。

### D08. 外建把 Envoy 声明式配置提得太早

- 反对理由：HUAKAI 当前最大的真实缺口是 provider adapter、router attempt loop、admin query、auth/key/payment。Declarative config 在这些之前会增加抽象债。
- HUAKAI 依据：`docs/plans/2026-04-29-integration-sprint-plan.md`, `backend/cmd/gateway/main.go`
- 实操结论：Config-as-code 应排在 RoutePlan 和 policy snapshot 后。

### D09. 外建没有区分 spec 完成和 code 完成

- 反对理由：HUAKAI 很多能力是 Released spec/schema，但 implementation 只是 skeleton 或 smoke-only。
- HUAKAI 依据：`backend/internal/rate/rate.go`, `backend/internal/obs/obs.go`, `backend/pkg/adapter/adapter.go`
- 实操结论：评估必须用 Done/Partial/Missing，而不是 "spec 已有 = 已实现"。

### D10. 外建没有处理 clean-room reviewer-lane 输出质量

- 反对理由：HUAKAI 的真值约束要求 source-observed、inferred、open question 分类，外建缺这个治理层。
- HUAKAI 依据：AGENTS.md Owner directives, `docs/decisions/DR-008-methodology-choice-strict-authenticity.md`
- 实操结论：任何新 architecture spec 仍要按 HUAKAI released-spec 模板走，不应直接把外建意见当架构决议。

### D11. 外建的 MVP 阶段顺序需要调整

- 反对理由：阶段 4 才做充值/admin/logs 对 HUAKAI Model 1 商业化太晚；但阶段 5 的高级 policy/cache/SLA 也不能抢在 money path 和 provider adapter 前。
- HUAKAI 依据：`docs/decisions/DR-002-product-editions.md`, `docs/01_PROJECT_BRIEF.md`
- 实操结论：应采用 "Phase C/E 增量补核心 + 最小 admin/obs + provider breadth" 的顺序。

### D12. 外建没有点名 credentials at rest 的当前代码差距

- 反对理由：它说 Credential 不进日志，但 HUAKAI 当前 schema 的 `credentials jsonb` 仍需要加密迁移决策。
- HUAKAI 依据：`backend/sql/migrations/0001_pool_routing.up.sql`, `backend/internal/auth/sanitizer.go`, `docs/10_RISK_REGISTER.md`
- 实操结论：Owner 需要确认是否尽早上 `credentials_encrypted`。

## E. 真正需要 Owner 决策的开放问题

### E01. 要不要现在新建 `backend/internal/router/`？

- 类型：该不该新建模块
- 建议：该建，但只建 RoutePlan + attempt orchestrator，不碰 billing/pool 内部。
- Owner 决策点：是否允许 Phase E 前先做模块边界重构。
- 不做风险：真实 retry/fallback 会继续塞进 HTTP handler。
- 做的风险：若范围失控，会影响已能跑通的 Phase C money path。

### E02. 要不要把 `internal/pool` 改名为 `internal/resourcepool`？

- 类型：该不该 refactor
- 建议：暂不改名；用 docs/interface 明确 "pool package = Resource Pool implementation"。
- Owner 决策点：是否坚持命名与外建一致。
- 不做风险：新成员可能误以为 pool 只负责 selector。
- 做的风险：机械改名影响 tests、imports、review噪音大。

### E03. 要不要现在新建 `backend/internal/registry/`？

- 类型：该不该新建模块
- 建议：该建，优先级高于 declarative policy。
- Owner 决策点：model registry 是否进入下一 vertical slice。
- 不做风险：Router 无法稳定回答 context window、capability、price class、provider model id。
- 做的风险：需要新增 schema，属于高风险 schema migration，需 Owner 明确确认。

### E04. `backend/pkg/adapter` 是否迁到 `backend/internal/adapters`？

- 类型：该不该 refactor
- 建议：等第一批真实 adapter 开工时再定；现在先注册接口。
- Owner 决策点：adapter 是否作为 plugin/public package 暴露。
- 不做风险：adapter registry 位置模糊。
- 做的风险：若未来插件需要外部 package，放 internal 可能不合适。

### E05. 是否现在做 `request_id / attempt_id / lease_id` additive migration？

- 类型：该不该改顺序
- 建议：应该尽早做，但这是 schema change，需要 Owner 确认。
- Owner 决策点：是否允许 0007 添加 request_id/attempt_id/lease_id/trace fields。
- 不做风险：后续 observability/admin 调试链断裂。
- 做的风险：迁移会触碰 money-path tables，属于 AGENTS 高风险文件范围。

### E06. Credential at-rest encryption 是否进入下一批？

- 类型：该不该改顺序
- 建议：建议 HIGH，但必须 Owner 确认。
- Owner 决策点：`credentials jsonb` 是否保留短期可用，还是立即引入 encrypted bytea/envelope metadata。
- 不做风险：与 "Credential 永远不进日志" 不冲突，但与生产安全预期冲突。
- 做的风险：涉及真实凭证模型、迁移、key management，是高风险安全/credential scope。

### E07. Admin Ops 最小面是先做 API 还是 UI？

- 类型：该不该新建模块
- 建议：先 API + obs repository，再 UI。
- Owner 决策点：Gemini 是否并行做 ops dashboard mock，Codex/Claude 做 backend API。
- 不做风险：Owner 无法验证商业闭环。
- 做的风险：API 先行会暴露 auth/RBAC 未完成，需要 smoke/admin guard。

### E08. Declarative policy 放在哪个阶段？

- 类型：该不该改顺序
- 建议：不要早于 RoutePlan + ModelRegistry + first real provider adapter。
- Owner 决策点：是否将 F-CONFIG-001 从 L2 提前到当前 sprint。
- 不做风险：配置迁移复现能力晚。
- 做的风险：提前抽象会拖慢真实请求链路。

### E09. Payment plugin 是否要和 Admin Ops 同步进入 Model 1 最小商业线？

- 类型：该不该改顺序
- 建议：需要 Owner 定义 "commercializable minimum"。
- Owner 决策点：Personal Edition 是否必须在下一大阶段包含至少一个 payment provider plugin shell。
- 不做风险：只会转发和记账，但无法收钱。
- 做的风险：支付逻辑是高风险范围，AGENTS 要求 Owner 确认。

## F. 推荐执行路径

### 候选 A：增量补强，不全面推倒

- 内容：保留 5 层 Tx1/Pool/Forwarder/Tx2；新增 Router Engine、Model Registry、ID chain、Adapter Registry、Obs Repository。
- 优点：保护已跑通的 Phase C money path。
- 优点：外建正确部分能逐项吸收。
- 缺点：短期目录仍不完全符合外建三层美感。
- 适用条件：Owner 想尽快继续推进真实 provider 和商业闭环。

### 候选 B：全面 refactor 成外建目录结构

- 内容：重切 `internal/{gateway,auth,registry,router,resourcepool,adapters,ledger,usage,policy,admin}`。
- 优点：概念边界清晰。
- 缺点：会打断现有 integration tests 和 Phase C smoke path。
- 缺点：容易把 spec 完成和代码迁移混在一起。
- 适用条件：Owner 暂停功能推进，专门做架构整理。

### 候选 C：维持现状，只继续原 sprint plan

- 内容：按 2026-04-29 integration sprint plan 继续 Phase C/D/E，不引入新模块。
- 优点：最少扰动。
- 缺点：Router/Registry/ID chain 的债会越来越重。
- 缺点：真实 fallback/retry/provider breadth 会被 handler 和 selector 承担。
- 适用条件：只追求短期 smoke green，不追求下一阶段结构。

### 候选 D：再开一轮平行 Codex/Gemini/Claude 交叉计划

- 内容：围绕 Router+Registry+Adapter+Admin 最小商业线，各自独立写 plan，再合成。
- 优点：符合 2026-04-30 parallel plans 指令。
- 缺点：会延迟执行，且本评估已经足够指出主要方向。
- 适用条件：Owner 准备批准非平凡 schema/module work 前。

### Top 1 推荐

选择候选 A。

理由不超过 5 行：

- HUAKAI 的 Tx1/Tx2 和 clean-room/tenant/PostgreSQL 基础比外建更强，不能推倒。
- 外建最有价值的是补显式 Router/Registry/Policy/Adapter/Admin 边界，应增量吸收。
- 下一步优先做 `RoutePlan + ModelRegistry + ID chain + real adapter registry + obs queries`。
- 涉及 schema、credential encryption、payment 的部分必须单独 Owner 确认。

## 建议下一步任务切片

### Slice 1：RoutePlan skeleton

- 输出：`backend/internal/router`，只含 pure planning structs + attempt policy，不发请求。
- 测试：RoutePlan 不含 credential secret；每个 attempt 有 reason。
- 文档：更新 `docs/specs/pool-routing.md` implementer notes。
- 风险：低到中。

### Slice 2：Model Registry spec/schema plan

- 输出：先写 plan，不直接 migration。
- 包含：model alias、provider model id、capability requirements、context window、pricing class、registry version。
- 决策：Owner 是否批准 0007 migration。
- 风险：高，因为触碰 schema。

### Slice 3：Correlation ID migration proposal

- 输出：docs/spec delta，不先写 migration。
- 包含：request_id、attempt_id 或 attempt_uid、lease_id、trace_id 在 claim/usage/billing/audit 的传播规则。
- 决策：Owner 是否批准 additive migration。
- 风险：高，因为 money-path schema。

### Slice 4：Obs Repository minimum

- 输出：`internal/obs` SQL-backed usage/claim/audit query。
- 测试：tenant isolation、pagination、no credential fields。
- 风险：中。

### Slice 5：First real adapter vertical

- 输出：一个真实 provider upstream adapter + one client adapter，不能只用 mock upstream。
- 测试：protocol_loss、usage extraction、stream end taxonomy、Tx2 settle。
- 风险：中到高，取决于 provider credential。

## 功能缩水评估

- 外建提案没有要求缩水。
- 本评估建议不删除任何 HUAKAI parity feature。
- 对高风险能力的处理方式是：Safe Equivalent、Plugin、Feature Flag、Mandatory Roadmap、Owner confirmation。
- all-api-hub 相关能力保持产品结果，不继承凭证抓取/自动化绕站实现路径。
- Envoy declarative config 保留为 F-CONFIG/F-ARCH/F-DEPLOY 路线，不抢在 money path 和 provider breadth 前。

## Clean-room 风险评估

- 本评估未读取非 MIT reference source。
- 本评估没有引用上游代码、上游文件结构、函数名、字段名或算法顺序。
- 外部提案本身作为第三方意见，只用于与 HUAKAI 内部状态比较。
- 若后续要具体研究某参考项目如何做某机制，必须重新走 Clean-Room Lane Guard。
- "接 sub2api 底座" 只能解释为行为等价/接口边界，不能解释为代码接入。

## 安全风险评估

- 当前最大安全差距是 credential at-rest encryption 未在代码/schema 中强制落地。
- 当前 user-facing auth 仍是 smoke resolver，不能视为生产 auth。
- Admin routes 多数 501，未来开放前必须做 RBAC/audit。
- Router/Adapter 边界未固化前，存在未来 adapter 绕过 ledger 的设计风险。
- request/attempt/lease ID 未完整传播，事故排查和审计链路会弱。

## Owner 需要确认的事项汇总

- 是否批准新增 `backend/internal/router`。
- 是否批准新增 `backend/internal/registry` 的 plan，并准备后续 schema migration。
- 是否批准 0007 additive migration 设计 request_id/attempt_id/lease_id。
- 是否批准 credential encrypted-at-rest 进入下一批。
- 是否把 Admin Ops API 作为下一阶段高优先级。
- 是否把 payment plugin shell 纳入 Personal Edition commercial minimum。
- 是否暂缓全面目录重构，采用增量补强路径。
- 是否要求 Gemini 并行做 Admin Ops UI 计划。
- 是否要求 Claude/Codex 按 parallel plan 再合成一个执行计划。

## 结论

外部提案方向基本正确，但不是 HUAKAI 的替代架构。

它说对的部分集中在模块边界：Router Engine、Model Registry、Resource Pool 语义、Adapter Registry、ID chain、Declarative Policy、Admin/Ops。

HUAKAI 已经走得更深的部分集中在生产正确性：clean-room、tenant-aware PostgreSQL、Tx1/Tx2、Pattern B、streaming taxonomy、protocol_loss、credential CAS/storm control、rate-limit taxonomy。

因此最稳的路线是：保留 5 层 money path，增量补外建指出的模块边界，不做全面 refactor。

## Source files read

```text
docs/01_PROJECT_BRIEF.md
docs/02_HUAKAI_FUSION_ARCHITECTURE.md
docs/03_FEATURE_PARITY_MATRIX.md
docs/10_RISK_REGISTER.md
docs/decisions/DR-000-clean-room-methodology.md
docs/decisions/DR-001-multi-tenancy.md
docs/decisions/DR-002-product-editions.md
docs/decisions/DR-006-database.md
docs/decisions/DR-008-methodology-choice-strict-authenticity.md
docs/plans/2026-04-29-integration-sprint-plan.md
docs/specs/api-contract.md
docs/specs/observability-billing.md
docs/specs/pool-routing.md
docs/specs/protocol-translation.md
docs/specs/rate-limiting.md
docs/specs/streaming-forwarder.md
docs/specs/upstream-credential-management.md
docs/specs/_invariants/F-OBS-001-tx2-invariants-checklist.md
backend/cmd/gateway/main.go
backend/cmd/gateway/smoke_test.go
backend/internal/auth/antigravity_token_provider.go
backend/internal/auth/audit.go
backend/internal/auth/auth.go
backend/internal/auth/auth_test.go
backend/internal/auth/auth_helpers_test.go
backend/internal/auth/sanitizer.go
backend/internal/auth/smoke_resolver.go
backend/internal/auth/storm_controller.go
backend/internal/billing/billing.go
backend/internal/billing/claim_gate.go
backend/internal/billing/claim_gate_integration_test.go
backend/internal/billing/settler.go
backend/internal/billing/settler_integration_test.go
backend/internal/config/config.go
backend/internal/db/db.go
backend/internal/db/models.go
backend/internal/db/pgconn.go
backend/internal/db/querier.go
backend/internal/gateway/event_scanner.go
backend/internal/gateway/forwarder.go
backend/internal/gateway/forwarder_test.go
backend/internal/gateway/forwarder_types.go
backend/internal/gateway/gateway.go
backend/internal/gatewayhttp/chat_completions_handler.go
backend/internal/gatewayhttp/mock_upstream.go
backend/internal/obs/obs.go
backend/internal/pool/auth_credential_gate.go
backend/internal/pool/auth_credential_gate_integration_test.go
backend/internal/pool/db_account_source.go
backend/internal/pool/db_adapters_integration_test.go
backend/internal/pool/db_claim_gate.go
backend/internal/pool/db_repo.go
backend/internal/pool/db_slot_manager.go
backend/internal/pool/gates.go
backend/internal/pool/pool.go
backend/internal/pool/pool_helpers_test.go
backend/internal/pool/pool_test.go
backend/internal/pool/routing_reason.go
backend/internal/pool/selector.go
backend/internal/pool/slot.go
backend/internal/proto/anthropic_sse.go
backend/internal/proto/capability_matrix.go
backend/internal/proto/hcsf.go
backend/internal/proto/proto.go
backend/internal/proto/proto_test.go
backend/internal/proto/tool_call_id.go
backend/internal/rate/rate.go
backend/pkg/adapter/adapter.go
backend/sql/migrations/0001_pool_routing.up.sql
backend/sql/migrations/0002_observability_billing.up.sql
backend/sql/migrations/0003_streaming_forwarder.up.sql
backend/sql/migrations/0004_rate_limiting.up.sql
backend/sql/migrations/0005_protocol_translation.up.sql
backend/sql/migrations/0006_upstream_credential_management.up.sql
backend/sql/queries/auth_audit.sql
backend/sql/queries/auth_credentials.sql
backend/sql/queries/auth_storm.sql
backend/sql/queries/billing_claims.sql
backend/sql/queries/billing_settle.sql
backend/sql/queries/pool_accounts.sql
backend/sql/queries/pool_audit.sql
backend/sql/queries/pool_slot_acquisitions.sql
backend/sql/queries/pool_sticky_bindings.sql
backend/sql/queries/proto_capability.sql
backend/sql/queries/proto_policy.sql
```

Lane: evaluation

Agent: Codex GPT-5

UTC timestamp: 2026-04-30T05:36:49.6078012Z

中文总结：本次独立评估中，真实观察来自 HUAKAI 内部架构文档、Released specs、DR、当前 Go/SQL 代码和测试；合理推断主要是把外部提案的模块概念映射到 HUAKAI 已有包/表/阶段缺口；没有使用非 MIT 参考项目源代码，也没有读取 Claude 对本题的分析。结论是外建方向可采纳但不能替代 HUAKAI：HUAKAI 在 clean-room、Tx1/Tx2、PostgreSQL、Pattern B、protocol_loss、streaming taxonomy 上更深；真正开放问题有 9 个，优先让 Owner 确认 Router/Registry/ID chain/schema/credential/admin/payment 的执行顺序。
