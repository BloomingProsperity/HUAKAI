# money-path worker slice — 计划（Claude，独立草案）

**作者**: Claude PM  **日期**: 2026-05-29  **基线**: fix/hermes-phase-1-e33d940 @ 4c0a3fb
**性质**: HIGH-risk money-path + schema 迁移 → **全程 Owner 门控**；本文件仅计划，未经放行不落代码、不建迁移、不迁库。
**来源 spec**: docs/process/reviews/DEFERRED-S1-029-provisional-reconcile.md、
docs/process/plans/2026-05-29-s2163-s1029-shared-fu-claude.md（sub-part 3）
**配套**: docs/process/plans/2026-05-29-money-path-worker-codex.md（codex 独立草案，#10 交叉讨论）

## 1. 现状（已取证，file:line 锚定 fix/hermes@4c0a3fb）
- **可计费唯一条件 = StreamStatePartial**；`CostForAttempt` 对非可计费 attempt 返回 Zero
  （`backend/internal/billing/state.go:191-196`）；在 settle 内 `actualCost = CostForAttempt(actualCost, attempt)`
  （`backend/internal/billing/settler.go:131-132`）。
- **S1-029 缺 usage → 零成本 + pending** 两处来源：① forwarder 在 UpstreamEOFNoTerminal 置
  `PendingReconciliation=true`（`backend/internal/gateway/forwarder.go:176-177`）；② 流式结算在
  `reportedUsageMissing` 时清 actualCost 为零 + 置 pending（`backend/internal/gatewayhttp/chat_completions_stream.go:529-543`）。
- **正向 provisional 可行点**：清零处 `draft.EstimatedOutputTokens` 已可达，价表经 `ex.d.RateTables`
  可用，`completionRateVector.price()` / `tokenBucketMicros(tokens, rate)` 能按 token 数算成本
  （`backend/internal/gatewayhttp/chat_completions_pricing.go:253-315,339-350`）。
- **frames/tokens 歧义（R4-P2 根因）**：写入 usage_records 的 tokens_output 来自
  `outputTokensForAttempt(draft, attempt) = max(draft.TokensOutput, attempt.DeliveredTokenCount)`
  （`backend/internal/billing/settler.go:151,937-948`）；`DeliveredTokenCount` 是 canonical 事件**帧数**
  （`canonicalDeliveredChunks`），非 token。缺 usage 时 TokensOutput=0 → tokens_output 列拿到**帧数**：
  ①污染"无真实输出 token 信号"的判别（reconcile worker 无法把零费率真实-usage 行与无-usage provisional 行分开）；
  ②若任何路径按 tokens_output 计价则潜在超收。**actualCost 当前另算**（不直接来自 tokens_output），故这是
  记录/识别正确性问题，超收是潜在面而非已证实的当前流式超收。
- **usage_source = 5 值**（reported/normalized/inferred/partial/ambiguous）
  （`backend/internal/gateway/forwarder_types.go:39-47`），持久化到 billing_events 与 usage_records，
  带 CHECK 约束（`backend/sql/migrations/0002_observability_billing.up.sql:103,156-158`）。加新值需迁约束。
- **最新迁移 0060**；目录 `backend/sql/migrations/`，约定 `NNNN_desc.up.sql` / `.down.sql` → 下一个 **0061**。
- **尚无 settlementreconcile worker**（`backend/internal/obs/obs.go:73-75` 仅 TODO 占位）；现有
  `settlementrecovery`（`backend/internal/settlementrecovery/handler.go`）是 settle 重试,非 reconcile;
  `observability` 的 DualRunReconciler 是测试/双跑比对,非 DB-backed 结算 reconcile。
- **append-only 退款通道已存在**：`RefundInTx` 追加 reconciliation_appended 负事件、用 audit_request_id 幂等
  （`backend/internal/billing/settler.go:536-702,618-620`）；reconcile 不改原零成本行,而是追加 delta。
- **idempotency**：Settle 走 Serializable Tx2 + claim FOR UPDATE + 状态机校验
  （`backend/internal/billing/settler.go:78-100,233-246`）。

## 2. 切片分解（4 个可独立评审/落地的 commit；C2/C3/C4 为门控金钱/迁移步）
**C1 — frames/tokens 消歧（基础，无迁移、无加费，先落）**
- 改 `outputTokensForAttempt`：缺真实 usage（reportedUsageMissing / TokensOutput==0）时**不**用帧数
  `DeliveredTokenCount` 充当 tokens_output；tokens_output 只反映真实输出 token（无则 0）。帧数仍保留在既有
  `delivered_token_count` 列(独立)。→ tokens_output==0 重新成为可靠的"无真实输出信号",解 R4-P2 行识别根因。
- 包归属：`backend/internal/billing`（**非 frozen**，改既有 settler.go）。可能触及读取 draft 字段（gateway frozen，
  仅读不改）。**无迁移、无加费**（仅可能下调缺-usage 行被错记的 tokens_output；成本仍由 actualCost 决定）。
- 风险点：须确认无任何计价/配额路径直接读 tokens_output 来算钱（取证后若有，则该路径同改或回归测试覆盖）。
- 判别测试：缺-usage draft（TokensOutput=0、DeliveredTokenCount=帧数 N>0）→ 断言写入行 tokens_output==0、
  delivered_token_count==N；mutation（恢复 max(...) 用帧数）→ tokens_output==N → RED。正常 graceful（TokensOutput>0）
  → tokens_output 不变（self-proving 双路）。

**C2 — 专用 provisional usage_source 值 + 迁移 0061（门控·迁移）**
- 新增枚举值（**待定，决策点 D1**，建议 `provisional`），仅由"缺 usage"分支设置；CHECK 约束迁移
  0061 扩 billing_events + usage_records 两表。down 迁移收敛回旧约束（需先确认无行用新值,否则 down 不安全→
  文档标注 down 仅在无 provisional 行时可用）。
- 包归属：迁移文件在 `backend/sql/migrations/`（非 frozen）；枚举常量在 `backend/internal/gateway/forwarder_types.go`
  （**frozen**，加既有 type 的常量，属"改既有文件",允许）；set-site 在 forwarder.go / chat_completions_stream.go
  （frozen，改既有）。
- 风险点：CHECK 约束迁移是 schema 变更（HIGH-risk）;旧行不受影响（默认仍 reported）;**prod 迁移单独门控**。
- 判别测试：缺-usage 流 → 行 usage_source==新值；非缺-usage（reported/inferred/ambiguous）不被改写;
  mutation（set-site 用旧 inferred）→ RED。迁移 up/down round-trip 在本地 dev 库验证（已授权 local-only）。

**C3 — accurate positive provisional 成本（= sub-part 3，门控·money-path）**
- 在 S1-029 清零点改为：缺 usage 时用 `draft.EstimatedOutputTokens × 输出费率` 算**正向** provisional 成本
  （不再 $0），随 C2 的 provisional 标记 + pending_reconciliation 落账。仅输出 token 维度估算（input/cache 已知则照实）。
- 包归属：`backend/internal/gatewayhttp`（**frozen**，改既有 chat_completions_stream.go / pricing.go）。
- 风险点（money）：估算可能高于/低于真实 → 但**有 C4 reconcile 兜底翻正**;须保证不双计（estimate 成本仅在
  缺 usage 分支,有 usage 分支不变）;EstimatedOutputTokens==0 → 退化为 $0（安全,不无中生有计费）。
- 判别测试：缺-usage 流 reported=0、EstimatedOutputTokens=N>0、有正费率 → actualCost>0 且 == price(N);
  mutation（仍清零）→ actualCost==0 → RED;EstimatedOutputTokens=0 → actualCost==0（安全退化）。
  对比有-usage 流成本不变（self-proving）。

**C4 — settlementreconcile worker（门控·最大·决策点 D2）**
- 新建 `backend/internal/settlementreconcile` 包（**非 frozen,新包合规** #13）：周期/触发扫描
  `usage_records WHERE usage_source=provisional AND pending_reconciliation=true`,取权威 usage（**D2：来源**——
  ①上游可 retrieve-by-id 的 provider 重取；②宽限期后把 estimate 定为终值 finalize_after_grace；③两者结合）,
  经 `RefundInTx` append-only 写 delta + 翻 pending=false / 标 finalized。幂等(audit_request_id)、Serializable。
- 配套（DEFERRED 已点）：admin/observability pending 视图须排除 finalize_after_grace 标记行避免过计
  （observability.sql + 查询，非 frozen）。
- 风险点：reconcile 改既有账（退/补）→ HIGH;必须幂等 + 不重复 reconcile + 不动已 committed-final 行。

## 3. Owner 决策点（#15 参考对照）
**D1 — provisional 专用 usage_source 值命名/形态**（建议新增 `provisional`，仅缺-usage 分支设）
- 参考对照（参考项目对照）：
  - `llmgateway@1146e11:packages/db/src/schema.ts` 用**布尔标记列**(estimatedCost, default false)而非枚举值标记估算账，
    缺 token 时按内容长度算（`apps/gateway/src/lib/costs.ts`）。→ 替代形态：布尔列 vs 我们的枚举值。
  - `litellm@79b4578671:litellm/proxy/schema.prisma` SpendLogs 用可选 `status` 字段 + 缺 token 隐式标记,
    无专门 provisional 枚举。→ 也无专用枚举,靠 status/缺字段。
  - 综合：两参考都倾向**布尔/状态标记列**而非扩 CHECK 枚举。HUAKAI 已有 pending_reconciliation 布尔 → D1 可
    选 (a) 复用布尔 + 不扩枚举（轻,但 R4-P2 行识别仍靠 tokens_output==0,弱）或 (b) 扩枚举（强识别,需迁移）。
    **倾向 (b)**：枚举值让 reconcile 查询精确、与现有 usage_source 体系一致;成本是一次 CHECK 迁移。
**D2 — reconcile 权威 usage 来源**（①provider 重取 / ②宽限期 finalize estimate / ③结合）
- 参考对照：
  - `litellm@79b4578671:litellm/proxy/hooks/parallel_request_limiter_v3.py` 用 **post-call reconciliation 退还
    超额预留**——即"先预留→实际到达后再平"模型,权威值来自实际调用结果(到达即平)。
  - `llmgateway@1146e11:apps/gateway/src/lib/costs.ts` 缺 token 时直接按内容**估算定值**(isEstimated=true),
    未见后续向上游重取——即②宽限/估值即终值倾向。
  - 综合：litellm=到达即平(近①)、llmgateway=估值即终值(近②)。HUAKAI 缺-usage 多因客户端断连,上游可能
    永不补 usage → **倾向②为主(宽限期后 finalize estimate)+ ①仅对支持 retrieve-by-id 的 provider 机会性补全**。

## 4. 成功标准 / 纪律
- 每 commit：`go build ./...` + 相关包 test 全绿;判别测试 mutation RED→GREEN;#8 codex review 无未结 S0/S1。
- 分步落地：C1 先（基础、低风险）→ C2（迁移,门控）→ C3（加费,门控）→ C4（worker,门控）。每步独立 commit + ff fix/hermes + push;
  **prod 迁移始终单独门控**（local dev 库已授权验证）。
- frozen 包仅改既有文件/加既有 type 常量;新包 settlementreconcile 合规。
- commit-naming-v2;trailer `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`。

## 5. blast radius / what could go wrong
- **money 正确性**：C3 引入正向估算计费 → 估算偏差直接影响账单;缓解=C4 reconcile 翻正 + estimate=0 安全退化 + 仅缺-usage 分支。
- **schema**：C2 CHECK 迁移影响两表;down 迁移在有 provisional 行时不安全(文档化 + 先验证无行)。
- **双路径**：S1-029 在 gatewayhttp 清零 vs settler outputTokensForAttempt 两条路必须一致,C1/C3 须同时核对两处不打架。
- **reconcile 幂等**：C4 重复跑/并发须不重复退补(复用 RefundInTx 的 audit_request_id 幂等)。
- **观测过计**：pending 视图须排除已 finalize 行。

## 6. 时间估计（落地，非本轮）
C1 ~0.5d、C2 ~0.5d（含本地迁移验证）、C3 ~0.5d、C4 ~1–1.5d（worker + 视图 + 测试）。分步、每步评审。

## 7. 本轮请求 Owner 裁定
1) 确认分步顺序 C1→C2→C3→C4 是否照此推进，或先只做 C1（基础消歧、最低风险）观察；
2) D1（枚举值 vs 复用布尔）；3) D2（reconcile 权威来源策略）；4) 何时放行各门控步的本地实现（prod 迁移仍另行单批）。

## 8. #10 交叉讨论综合（vs codex 独立草案 2026-05-29-money-path-worker-codex.md）
两份草案**独立撰写、互不可见**。综合如下：

### AGREE（双方收敛，已校验）
- **C1 frames/tokens 消歧**：`outputTokensForAttempt` 不再回退用 delivered 帧数当 tokens_output；优先 reported、其次
  estimate、否则 0。
- **用既有 usage_source 枚举 + 加新值（CHECK 迁移 0061），不加可变状态列**。codex 补强一条我漏掉的关键事实：
  **usage_records 在 0039 迁移有 append-only 触发器**（`backend/sql/migrations/0039_money_path_append_only_triggers.up.sql:22-23`）
  → 根本不能加"可变 reconciled 列"，只能 append-only reconcile 事件。这把 D1 从"枚举 vs 布尔"推向**枚举值确定**。
- **正向 provisional = EstimatedOutputTokens × 费率；estimate=0 → 维持 $0 安全退化**；reconcile 只 append delta 不重复全额计费。
- **迁移须先于发新值的代码上线**；prod 迁移单独门控。

### CONFLICT / 差异（需 Owner 定）
- **范围**：我的草案含 C4 reconcile worker（本切片内，门控）；codex **显式排除 worker**，本切片只做 C1+C2+C3。
  codex 更窄且更稳——理由：worker 有隐藏前置（见下 GAP：reconcile 事件表缺幂等键）+ D2 权威来源未决。
  **我接受 codex 的收窄**：本切片 = C1+C2+C3，C4 worker 另起门控切片（含其自己的幂等键迁移 + D2）。
- **标记串**：codex 定 `usage_source='estimated'`；我原议 `provisional`。`estimated` 更贴近语义，倾向采纳（→ Owner D1）。
- **provisional 是否含 input 成本**：codex 指出断连时 input 也可能缺，提议同时估 input（estimateInputTokens），并标注
  output-only "更稳但明知少计"。我原草案只说"input 已知则照实"——**codex 此处更周全**，升为 Owner 决策点 D3。

### codex 在我草案里抓到的关键 GAP（已并入合并方案）
- **【最重要】chargeability**：仅 StreamStatePartial 可计费，`CostForAttempt` 会把非可计费 attempt 的成本**重新清零**
  （state.go:191-196）。我的 C3 只在清零点算正成本，却没改 `AttemptFromGatewayDraft` 的状态分类 → **正成本会被
  CostForAttempt 再清零、白算**。codex 正确指出须让"缺 usage 但有 estimate"成为可计费信号（改 state.go），
  且**不**把 estimate 灌进 DeliveredTokenCount。→ 合并方案 C3 必含 state.go 改动 + 对应判别测试。
- **迁移精度**：`billing_events.usage_source` 是无 CHECK 的 text（0002:97,103），只有 `usage_records` 有 CHECK（0002:156）。
  → 迁移 0061 **只动 usage_records**；我草案"两表都改"过宽，纠正。
- **迁移模式**：codex 用 `ADD CONSTRAINT ... NOT VALID` + `VALIDATE CONSTRAINT` 避免长锁，更 production-careful，采纳。
- **DLQ 掩盖**（#14 测试质量）：`insertUsageRecordOrDLQ` 可能把 usage 插入失败入 DLQ 而非 fail settle（settler.go:750）
  → 集成测试须断言**行确实存在**而非仅 settle 成功。采纳进测试计划。

### GAP（两份都需后续解的，归入 C4 worker 切片）
- **reconcile 幂等键**：codex 发现 reconciliation 事件表无唯一幂等键（0002:225）；RefundInTx 靠 audit_request_id 查重
  （settler.go:618-620）是代码层 lookup-before-insert，并发下可能不足 → C4 worker 切片须先加唯一约束迁移。
- **D2 reconcile 权威来源**（provider 重取 / 宽限期 finalize estimate / 结合）——仅我草案提出，归入 C4 切片决策。

### 合并后本切片范围（建议）= **C1 + C2 + C3（含 state.go chargeability）**，C4 worker 另起门控切片。

## 9. sub2api + CLIProxyAPI 对照（Owner 指令 2026-05-29「做功能都要看 sub2 和 cliproxy」）
补读 Owner 点名的两家 Go-域参考（clean-room specifier lane）：

| 关注点 | sub2api@91da8159 | CLIProxyAPI@21fad9d | 对 HUAKAI 含义 |
|---|---|---|---|
| 缺 usage 估算成本 | 否：记零/记已收集 token，不估（`backend/internal/service/gateway_service.go:5398`、`backend/ent/schema/usage_log.go:68-79`） | 否：EnsurePublished 显式记零 token（`internal/runtime/executor/helps/usage_helpers.go:131-138`） | 两家都**保守记零**；C3 正向估算超出二者 |
| 帧数当 token | 否：只用解析 token 字段（`backend/internal/service/gateway_service.go:5492-5540`） | 否：只用上游 token 字段（`internal/runtime/executor/helps/usage_helpers.go:322-350`） | **强印证 C1**——HUAKAI 帧数回退是异类 |
| reconcile 不完整 usage | 仅手动清理(删除)子系统、非自动 re-settle（`backend/internal/service/usage_cleanup.go:10-76`） | 无：publish 即终态（`sdk/cliproxy/usage/manager.go:183-199`） | C4 reconcile 是 HUAKAI 超出二者的能力 |
| usage 状态标记 | 无：有 ClientDisconnect bool 但不落库不计费（`backend/internal/service/gateway_service.go:490-511`） | 无：仅 Failed bool（`sdk/cliproxy/usage/manager.go:14-49`） | C2 的 `estimated` 标记是 HUAKAI 新增 |

**new-api 对照（Owner 指令 2026-05-29「再加一个 new-api」）**——new-api@20d3e73 四件全做：
- 缺 usage：tokenizer 对累积文本数 completion token（`relay/channel/openai/relay_responses.go:133-147`、
  `service/token_counter.go:394-406`）。
- 帧数 vs token：累积全文 tokenizer 计数、非帧数（`relay/channel/openai/relay_responses.go:133-140`）。
- reconcile：两阶段——估算先记，后续 RecalculateTaskQuota/轮询取真实值做 delta 结算 SettleBilling
  （`service/billing_session.go:41-78`、`service/task_billing.go:247-301`、`service/billing.go:34-78`）。
- 状态标记：stream_status(end_reason: client_gone 等) + UsageSource/UsageSemantic 字段（缺即隐含估算）
  （`service/log_info_generate.go:92-117`、`relay/common/stream_status.go:10-95`）。

**Fusion-upgrade 框定（#12，含 new-api）**：
- C1（帧数→不用帧数当 token）= 对齐 sub2api + CLIProxyAPI + new-api **三家**既有做法（三家都不拿帧数当 token）→
  **修 HUAKAI 异类回归**，非新增。
- C3+C2+C4（估算正向 provisional + estimated 标记 + reconcile）= **同族于 new-api@20d3e73 + litellm@79b4578671 +
  llmgateway@1146e11**（均"估算+标记+reconcile"），**异于** sub2api/CLIProxyAPI（保守记零）。
  **HUAKAI delta**：估算用 O(1) 启发式增量（tokencheck.EstimateStreamDelta，**零内容滞留**）而非 new-api 的
  tokenizer-over-累积全文（更准但需保留全文 + tokenizer 依赖）——HUAKAI 选 hot-path 内存安全的近似，reconcile 兜底；
  标记用 usage_source 枚举列 + pending_reconciliation（new-api 放 'Other' JSON + UsageSource 字段）。
  维度：**算法升级**（O(1) 估算定价）+ **生态升级**（usage_source=estimated 精确识别 + append-only reconcile delta）。
- **决策（D4，已知情确认 2026-05-29）**：Owner 在看过 sub2/cliproxy（保守记零）后，仍选 **(a) 保留正向估算**；
  new-api 作为 Owner 点名的同族参考进一步印证该方向。C3 成立。

### D4 — C3 方向（知情重确认，#15）
- **(a) 保留 C3 正向估算**（fusion-upgrade，对齐 litellm/llmgateway）：缺 usage 时按 input+估 output 计正向成本 +
  estimated 标记 + 未来 reconcile 翻正。参考：`litellm@79b4578671:litellm/proxy/hooks/parallel_request_limiter_v3.py`
  预留-reconcile；`llmgateway@1146e11:apps/gateway/src/lib/costs.ts` 缺 token 按内容估。
- **(b) 转保守**（对齐 sub2api+CLIProxyAPI）：缺 usage 仍记零（不估），只做 C1（消歧）+ C2 的标记（区分缺-usage 行
  以便日后人工/批处理），不引入正向估算计费。参考：`sub2api@91da8159:backend/internal/service/gateway_service.go:5398`
  记零；`CLIProxyAPI@21fad9d:internal/runtime/executor/helps/usage_helpers.go:131-138` 记零。
- Claude 倾向：**(a)**——HUAKAI 定位是"融合怪+升级"，正向估算+reconcile 是真实计费精度提升(断连流不再 0 收/不再
  frames 超收)，且有 C4 兜底翻正；但这是 money-path，且偏离 Owner 点名的两家，故交 Owner 知情定夺。
