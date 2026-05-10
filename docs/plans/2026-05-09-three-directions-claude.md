# 三方向差异化评估 — Claude 独立草案

**日期**: 2026-05-09
**作者**: Claude (sonnet/opus 主线)
**对应 Codex 草案**: docs/plans/2026-05-09-three-directions-codex.md（写作时未见 Codex 草案）
**触发**: Owner 提出"PASR 不是差异化，是地基；真正的差异化是从'路由账号'升级到'编排账号'"，列出三方向：Account Cache Fabric、Multi-Account Request Decomposition、Predictive Session Migration。

## TL;DR

视角对——升维到"编排"是有意义的。但三方向中：
- 方向 1 物理前提缺失（vendor cache scope 未验证）
- 方向 2 层级错位 + scope 灾难（应做独立产品而非 gateway 内）
- 方向 3 经济模型不成立（5 分钟 TTL 内预测精度不足）

真正空白且可付费的差异化在 **Account Lifecycle Operations + 反 detection 强伪装层**，已在 roadmap 中。

## 整体框架评估

"路由 → 编排"的升维有真实价值。sub2api / one-api / LiteLLM / Portkey 全在"选账号 + 转发"框架内换算法，PASR 也确实没跳出这个框。但跳出框的方式不止编排——把"账号本身"当作可运维的资源（lifecycle / quota aggregation / detection resistance）也是跳出框，且更接近 HUAKAI 既有 roadmap。

## 方向 1: Account Cache Fabric

### 核心断言
"在空闲账号上发预热请求让其也持有热 prefix 的 cache。"

### 物理前提存疑

Anthropic prompt cache 的 scope 据我所知是 **per-organization**，不是 per-account；OpenAI 的自动 prompt caching 文档上是 per-API-key 但实测有 routing affinity 维度。

后果：
- 同一 org 多账号 → cache 本就共享，不需要预热（Cache Fabric 无价值）
- 不同 org 多账号（HUAKAI 多账号池化的常见场景）→ 跨 org 无法共享 cache，预热请求 100% 浪费

**这是该方向能否成立的硬性前提，未经实验验证不能写入 roadmap。**

### 验证路径

派遣最小验证脚本：取两个不同 org 的 Anthropic API key，账号 A 发 cache_creation 请求建 prefix P 的 cache，账号 B 发同 prefix，看 response 中 `cache_read_input_tokens` 是否 > 0。一天可出结果。

OpenAI / Gemini 同理但语义不同（OpenAI 看 `prompt_tokens_details.cached_tokens`，Gemini 看 explicit cache reference）。

### 已知 PASR 限制叠加

`HasCacheBitmap` 更新条件硬编码 `cache_creation_input_tokens > 0`（Anthropic 字段）。即使 Cache Fabric 物理可行，当前实现也只在 Anthropic 路径上工作。要做成多 vendor 必须先抽象出 vendor-agnostic 的 cache signal interface。

### 结论
**Block on 物理验证**。在两条 Anthropic 跨 org 实验出结果之前，此方向暂不进 roadmap。

## 方向 2: Multi-Account Request Decomposition

### 核心断言
"把一个用户请求分解成多个子请求，并行发给不同账号，再合并。"

### 层级错位

请求语义分解是 agent framework 的层级（LangGraph / AutoGen / Anthropic Agent SDK），不是 gateway。Gateway 的契约是 1 个 inbound request ↔ 1 个或多个 upstream calls 但响应语义保持单一；分解破坏这个契约。

### 具体问题

1. **Idempotency 模型崩塌**：`billing_ledger_claims` 表设计 1 logical_request_id ↔ 1 claim，分解需 1:N。Tx1/Tx2 协议要重写。`uq_claims_idempotency` 唯一约束语义改变。
2. **响应合并不可解**：用户请求 SSE 流，3 个子请求各有独立 `message_start / content_block_delta / message_stop` 序列，合并时 token id 排序、tool_use 跨子请求依赖、stop_reason 仲裁都没有定义良好的合并语义。
3. **错误半失败**：3 子请求 2 成功 1 503。重试单子 → session state 不一致（前两个 token 已计费）；整体重试 → 已成功 token 双计费。
4. **Scope 爆炸**：HUAKAI 同时是 gateway + agent runtime + 编排器 + billing platform。对照 [project_sub2api_scaling_bottleneck.md] 中"功能堆叠 → 延迟随客户数线性涨"风险。

### 应做但不在 gateway

如果商业上有需求，应做独立产品 "HUAKAI Agents"（在 gateway 之上层），通过 HUAKAI gateway 的 inbound API 进出。这样 gateway 仍保持 1:1 契约，分解逻辑在 agent 层。

### 结论
**层级错位**。不进 gateway scope；如要做请单独立项。

## 方向 3: Predictive Session Migration

### 核心断言
"在账号 A 还活着时预测它将变慢，把会话上下文预热到账号 B。"

### 经济模型不成立

三个数字：
- Anthropic prompt cache TTL = 5 min（标准）/ 1 hour（extended，需付费 flag）
- 预热成本 = 1 次完整 prefix input-token 计费的真请求
- 预测窗口必须 < TTL，否则预热完即过期

5 分钟窗口内预测"账号 A 5 分钟后会限流" — Anthropic / OpenAI 限流是 server-side decision，外部观测到的是结果非因。EWMA 给"使用率高 → 可能限流"是粗信号，假阳性率高。

净收益条件：
```
预测精度 × cache_hit_价值 > 1 × 预热_token_成本
```

5 min TTL + 嘈杂信号下不相信此不等式成立。

### 退化到反应式

去掉"预测"层 → 账号 A 真 503 才迁移到 B → 等价于 sub2api 已有的 failover，无差异化。

### 结论
**经济模型不成立**。不进 roadmap，除非有数据证明特定场景下预测精度 > 70%。

## 真正空白的差异化空间（roadmap 内已有）

不是上面三方向，而是：

### 1. Account Lifecycle Operations
- 自动注册 / 续费 / 封禁检测 / 自动恢复
- pool 健康度仪表盘 + 自动隔离
- 已在 account hub roadmap 内

### 2. 用户视角"无限 Pro"
- N Pro 账号聚合成 1 用户配额，底层多账号轮转用户无感
- quota aggregation + 透明限流
- 是 HUAKAI 区别于 LiteLLM 的核心（LiteLLM 不做 stickiness + 配额聚合）

### 3. 反 detection 强伪装层（R5/R7/R8）
- Memory `feedback_stability_means_stronger.md` 明确 Owner 立场："稳定 = 比 sub2api 强（含强伪装）"
- 现有 sub2api / cursor-api / kiro-api 都不做完整 R5/R7/R8 强伪装
- 比"请求分解"更接近真实付费意愿

### 4. 多 vendor 真互通 + Cache Locality
- 把 PASR `HasCacheBitmap` 抽象为 vendor-agnostic cache signal interface
- 让 OpenAI `cached_tokens` + Gemini explicit cache 也喂入 segment table
- 这是 LiteLLM 没做、且当前 HUAKAI 也没做（PASR 只支持 Anthropic）的真正缺口

## 派 codex 的事项

CLAUDE.md #10 要求材料决策必须 Codex 平行起草。本草案在 Codex 草案完成前完成，互不见面。Owner 在 Codex 草案到位后做 agree/conflict/gaps 综合。

附加请 codex 重点验证：
1. Anthropic / OpenAI / Gemini 的 prompt cache scope 文档证据
2. 当前 PASR 在 OpenAI / Gemini 路径上的实际更新逻辑（grep 证据）
3. 方向 2 在 `billing_ledger_claims` 表结构下的迁移代价估算
4. 反 detection 强伪装层的具体技术含量（R5/R7/R8 文档）

## 风险与盲点（自评）

- **我没实测 cache scope** — 方向 1 的否决基于我对 Anthropic 文档的记忆，可能过期。Codex 应交叉查文档。
- **我没读 R5/R7/R8 详细规格** — "强伪装"的实际技术深度我只看到 memory 引用，没看 spec 文档
- **方向 3 的预测精度阈值（70%）是我估算** — 没有数据支撑，可能过严

## 决策建议（待 Owner + Codex 综合）

1. **方向 1 暂不进 roadmap，先派物理验证脚本**（一天工作量，Owner 本机跑）
2. **方向 2 退出 gateway scope**（如要做单独立项，不在本项目）
3. **方向 3 不进 roadmap**（除非有数据反驳经济模型）
4. **资源转投**：把"差异化"投入用于 Account Lifecycle + 反 detection R5/R7/R8 + PASR 多 vendor 抽象
