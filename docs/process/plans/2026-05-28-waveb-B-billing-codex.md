# 2026-05-28 waveb B — Billing fix plan (S1-015 + S1-029)

| Owner directive | `TASK: INDEPENDENT plan (PLAN ONLY, no code) for HUAKAI fixes S1-015 + S1-029.` |
| Scope | S1-015 与 S1-029 的设计与落地计划；仅编辑现有文件（除 `internal/settlementreconcile` 新增非冻结包）；不做代码实现与构建测试 |
| Success criteria | 1) 5m/1h 缓存写入与缓存读计费正确落账，2) 流式无上游 usage 仍有临时计费并可后续 reconcile，3) 有可执行且可否决的判别式测试与灰度回滚策略，4) 与 chunk-A S1-025 的 settle 行为冲突点已对齐 |
| Time estimate | 2 工作日（含跨任务协调与文档更新） |
| Blast radius | 计费金额、usage_records、账务重试、reconciliation worker 生命周期 |
| Failure modes | 错误 multiplier 导致误收/漏收、账务重放重复、`settler.go` 并发路径冲突、冻结包新增文件误碰、PendingReconciliation 永久堆积 |
| Decision points | ① S1-015 倍率方案 A/B/C 采用哪一版；② 是否新增 pricing 版本表列（0061）；③ 统一 `usage_source='inferred'` 场景条件；④ 重试 worker 的幂等/重试边界 |

## 1) 前置规则核对（plan execution envelope）
- 已满足 AGENTS 的 non-trivial 工作写计划要求：计划文件在 `docs/process/plans/`。
- 冻结包限制：`backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 仅改已存在文件，不新增。
- S1-029 与 S1-015 按依赖顺序执行（S1-015 先于 S1-029）。
- 不运行 build/test；不做 git add/commit/push。

## 2) 风险分级
- S1-015 风险：
  - S0/S1 风险：定价乘数错误/分桶错误会产生金额性偏差。
  - S2 风险：迁移新增列遗漏或回滚路径不齐。
- S1-029 风险：
  - S0/S1 风险：临时计费不足导致收入损失，或 worker 重放错误导致双重收/退。
  - S2 风险：pending 记录长期堆积与告警策略缺失。

## 3) S1-015 plan（cache-tier 5m/1h + cost breakdown）

### 3.1 触发缺陷（with 文件定位）
- `backend/internal/proto/hcsf.go:117-120`：协议层已声明 `CacheCreationInputTokens5m/1h`。
- `backend/internal/proto/anthropic/sse.go:357-362`：解析已做 TTL 拆分。
- `backend/internal/billing/settler.go:154-155, 158-163`：将 `CacheCreation5mTokens/1h` 硬编码为 0，且各类 cost 字段统一 `decimal.Zero`，导致分桶未落账。
- `backend/internal/billing/settler.go:354-358, 503-508`：Abort/CommitCacheHit 亦未填充缓存相关 cost。
- `backend/internal/gateway/forwarder_types.go`（`UsageRecordDraft`）与 `backend/internal/gateway/stream.go`（attempt/draft）需承接 5m/1h。
- `backend/internal/gatewayhttp/chat_completions_pricing.go:226`：当前仅对 `cache_creation` 一桶定价。
- `backend/internal/gatewayhttp/chat_completions_billing.go:562`：非流路径 draft 仍只见旧 `CacheCreationTokens` 聚合字段。
- 迁移/表结构：`backend/sql/migrations/0002_observability_billing.up.sql:133-136, 160,183-185` 与 `backend/sql/migrations/0002_observability_billing.up.sql:271-291`；`backend/internal/billing/rate_table_source.go` 已有 `PricingVersionReader`。

### 3.2 设计目标
1. 将 5m/1h 两种写入缓存 token 在 `UsageRecordDraft` → `settler` 全链路透传。
2. 在计费计算侧拆分“缓存创建（5m/1h）+ 缓存读取”为独立桶；输出对应 `CacheCreationCost`/`CacheReadCost`。
3. 保持与现有模型兼容：已存在 `cache_creation_tokens` 列继续可回填，`cache_creation_5m_tokens` / `cache_creation_1h_tokens`/`cache_read_tokens` 入账。
4. 不新增 frozen 包文件；必要时新增迁移为 `0061`（见 3.5）。

### 3.3 处理方案（无代码，仅设计）
- `backend/internal/gateway/forwarder_types.go`
  - Extend `UsageRecordDraft`（已存在类型）字段以携带 `cache_creation_input_tokens_5m`, `cache_creation_input_tokens_1h`, `cache_read_tokens`（命名沿现有风格）。
- `backend/internal/gateway/forwarder_types.go` 及 `backend/internal/gateway/forwarder.go`
  - non-stream 与 stream draft 合并逻辑保留总量兼容，同时补齐两个 TTL 字段；如果上游仍只有总量则由 parse 阶段回退规则填充到 5m 或 1h（与当前 `anthropic/sse.go` 行为一致）。
- `backend/internal/gateway/forwarder.go` 与 `backend/internal/gateway/stream.go`
  - 在 `UsageDraft` 生成/合并路径统一写入两段缓存 token 与 `usage_source` 元数据，便于后续追踪。
- `backend/internal/billing/settler.go`
  - 在 `InsertUsageRecordParams` 填充 `CacheCreation5mTokens`、`CacheCreation1hTokens` 和 `CacheReadTokens`（替代硬编码 0）。
  - 成本字段改为来自 pricing 结果（不是 `decimal.Zero`）。
  - 保持 Abort/CommitCacheHit 的 cost 回填一致；若未找到 usage，则写入 0 或可恢复逻辑。
- `backend/internal/gatewayhttp/chat_completions_pricing.go`
  - 扩展 completion token usage bucket 映射：`cache_creation_5m` 与 `cache_creation_1h` 可独立 bucket 名。
  - 新增“缓存读” bucket 使用更低倍率。
  - 价格解析沿既有 `addTokenBucket` 风格实现，失败回退/限幅沿现有策略。

### 3.4 参考对照与乘数候选（Owner 决策输入）
- 参考项目：
  - New-API 文件路径 `relay/channel/claude/relay-claude.go:593-614`（commit `20d3e73`）保留了 5m/1h 分片并穿透到账单层。
  - LiteLLM 文件路径 `litellm/litellm_core_utils/llm_cost_calc/utils.py:170-290`（commit `79b4578671`）展示 cache creation 输入价策略中存在 base 与 `above_1hr` 两档。
  - 对应维度：5m/1h 写入拆分、缓存读取按低倍数、是否按 provider model map 动态调整。

- 倍率候选：
  - A. Canonical（Anthropic 官方）
    - 5m 写入：`1.25x`
    - 1h 写入：`2.0x`
    - 缓存读：`0.1x`
  - B. LiteLLM 风格（按模型映射/按文件配置）
    - 引入每模型覆盖规则；若缺省回退到 A
  - C. 延后计价（先入 token，再延后成本）
    - 先写 token 成本为 0，后续在 reconcile/计费表版本里统一计算

- 推荐初始方案：A（按 Vendor 合约）上线，兼容开关后评估逐模型覆盖。

### 3.5 迁移与版本管理
- 若现有定价版本表可容纳两档倍率且不需新增列：优先用版本参数表达两个 cache 写入倍率与 cache read。
- 若需要新增列映射以支持 per-tier 可持久化元信息：单独声明 `migration 0061`（不在本任务内执行）
  - 目的：将倍率参数纳入 `billing_pricing_versions`。
  - 备注：本次计划优先不新增列，除非发现 schema 无法表达或查询/回填路径复杂化。

### 3.6 依赖与并发冲突管理（与 chunk-A S1-025）
- S1-015 与 S1-025 同触及 `backend/internal/billing/settler.go:152-168`。
- 先决协调：
  1) S1-025 的 draft/settle 分支变更抽象不变更本节字段语义。
  2) S1-015 仅补齐 cost & split，不更改结算控制流。
  3) 合并时以“冲突人工裁剪（同字段先级）”方式固定顺序。

### 3.7 S1-015 判别式测试（不执行）
- 测试 S1-015-01：总 cache token 固定，分别是 100% 落 5m 与 100% 落 1h；断言费用比例应符合 1h/5m 倍率比，不应相等（若把 5m/1h 合并一桶则变红）。
- 测试 S1-015-02：同输入中混合 5m+1h；断言 `usage_records.cache_creation_5m_tokens` 与 `cache_creation_1h_tokens` 分别落库，`cache_read_tokens` 按读取流量计入。
- 测试 S1-015-03（变异）：把 5m/1h 合并为单一写入倍率；断言期望 1h/5m 差异被吞掉（失败）。

## 4) S1-029 plan（streaming no-usage provisional + reconcile worker）

### 4.1 触发缺陷
- `backend/internal/gatewayhttp/chat_completions_stream.go:530-533`：无 usage 时置 `PendingReconciliation=true` 且 `ActualCost=0`。
- 送达计数与草稿：`backend/internal/gateway/forwarder_types.go:177-185`、`backend/internal/gateway/forwarder.go:176-180,401,408-410`、`backend/internal/gateway/stream.go:226,251-253`。
- 持久化状态：`backend/sql/migrations/0002_observability_billing.up.sql:160,183-185`。
- `backend/internal/db/billing/observability.sql.go:421-470,509+` 已支持 `PendingReconciliationOnly` 筛选。
- 现有 `backend/internal/settlementrecovery/handler.go` 不消费 pending true-up。

### 4.2 设计目标
1. 当上游未返回 usage，但流已交付 token 时，按输出速率先做 provisional charge（可回算）。
2. 标记 `PendingReconciliation=true` 与 `usage_source='inferred'`。
3. 新增后台 worker 从 pending 集合中逐批拉取，按 authoritative usage/后续 reconcile 做增量纠偏。
4. 重放对账动作采用现有 append-only `reconciliation_appended` 事件与 `RefundInTx` 的借贷方向。

### 4.3 处理方案（无代码，仅设计）
- `backend/internal/gateway/stream.go` 与 `backend/internal/gateway/http` 流式结束点：
  - 当 `usage missing` + `DeliveredTokenCount > 0`，将 provisional cost 设为 `DeliveredTokenCount * output_unit_cost`。
  - 成本来源使用 S1-015 完成后的 output pricing bucket（不允许使用硬编码比率）。
  - 同时保留 `pending_reconciliation=true` 与 `usage_source='inferred'`；原字段 `actualCost` 不得再固定为 0。
- `backend/internal/gateway/forwarder.go`
  - 确保 Draft 在流式完成路径保留 `DeliveredTokenCount` 与 `UsageSource=Inferred`（已有迹象），并与 reconciliation payload 一致。
- 新建 package `backend/internal/settlementreconcile`（非 frozen，允许新包）
  - Worker 查询 `PendingReconciliationOnly` 条件。
  - 对每条 pending 记录做 authoritative usage 二次拉取/对账（支持缺省或延迟到账）。
  - 以 `Reconciliation` append-only 事件更新账务（含 `reconciliation_appended`），采用 S1-013 的 `RefundInTx` 成本回抽路径处理差额（正负）与幂等。
  - 重试与租约策略沿现有 `ReplayJanitor/LeaseSweeper`，避免重复冲账。
- `backend/cmd/gateway/wiring.go:374-379`
  - 按 `NewReplayJanitor` / `NewLeaseSweeper` 模式新增 reconcile worker 生命周期注入。
- `backend/cmd/gateway/lifecycle.go:26-37`
  - 新增 stop channel 注入与优雅退出。

### 4.4 S1-029 判别式测试（不执行）
- S1-029-01：模拟流式返回 500 token、上游无 usage；断言 usage_records 实际记录非零 provisional，`pending_reconciliation=true`，`usage_source='inferred'`。
- S1-029-02：触发 worker 二次 reconcile 后，权威 usage=600 token；断言：产生 adjust event（金额收敛到差额），usage balance 发生对应变化。
- S1-029-03（变异）：无 provisional 时应红，worker 跳过 pending 处理应红（pending 长期滞留）。

## 5) S1-015 -> S1-029 排序与交付顺序
- 第 1 阶段：先完成 S1-015（缓存拆分/成本分桶），确认 output 和 cache 定价路径定义稳定。
- 第 2 阶段：基于 S1-015 的 output multiplier 与 bucket 名称，完成 S1-029 的 provisional 公式。
- 第 3 阶段：补齐 pending true-up worker，并在 wiring/lifecycle 中接入。
- 第 4 阶段：整理观测指标与告警（pending 累积量、reconcile 成功/失败率），交由 Owner 决定是否纳入本 slice。

## 6) 开发依赖（最小改动边界）
- 必改文件（现有）
  - `backend/internal/proto/anthropic/sse.go`（确认分桶语义不回退）
  - `backend/internal/gateway/forwarder_types.go`
  - `backend/internal/gateway/forwarder.go`
  - `backend/internal/gateway/stream.go`
  - `backend/internal/gatewayhttp/chat_completions_billing.go`
  - `backend/internal/gatewayhttp/chat_completions_pricing.go`
  - `backend/internal/billing/settler.go`
  - `backend/internal/db/billing/observability.sql.go`（查询/查询参数边界）
- 新建包（允许）
  - `backend/internal/settlementreconcile/*`（worker 与任务调度）
- 连接点
  - `backend/cmd/gateway/wiring.go`
  - `backend/cmd/gateway/lifecycle.go`
  - `backend/internal/settlementrecovery/handler.go`（避免职责重叠，新增入口仅消费 pending）

## 7) 风险与确认项（供 Owner）
- 需要 Owner 决定的点：
  - 计价模型优先级：A/B/C。
  - 是否立刻提交 `migration 0061`。
  - reconcile worker 的扫描频率/重试上限/告警阈值。
- 已知功能不缩水：不移除任何 feature；S1-015 与 S1-029 均保留并增强现有行为。
- 低风险并发：只加成本差异与 true-up，不改 auth/billing ledger schema 核心主逻辑。

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: litellm / new-api

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY:
  - file:line citations are ALLOWED in prose as `<repo>@<sha>:<file>:<line>`
  - the cited identifier itself should be paraphrased in surrounding prose
  - Source files read: AGENTS.md; CLAUDE.md; backend internal files above; `litellm@79b4578671:litellm/litellm_core_utils/llm_cost_calc/utils.py:170-290`; `new-api@20d3e73:relay/channel/claude/relay-claude.go:593-614`
  - Lane: specifier
  - Agent: codex
  - UTC timestamp: 2026-05-28T00:00:00Z

=== END CLEAN-ROOM LANE GUARD ===
