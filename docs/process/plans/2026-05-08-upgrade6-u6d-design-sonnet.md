# U6-D atomic — identity → adapter mapping 策略（设计 / sonnet lane）

- Lane: sonnet（plan only，不写代码）
- Date: 2026-05-08 UTC
- Scope: HUAKAI Upgrade #6 第四原子；前三原子 U6-A/B/C（`internal/clientid` 包 +
  middleware + per-client metrics）已 commit / 计划。
- Sister lane: 待 codex parallel-draft（CLAUDE.md #10）。

## 1. 决策点 — identity 是否覆盖 ProtocolFamily？

**结论：identity 不覆盖 `registry.Resolved.ProtocolFamily`，仅 influence 客户端
出站形态（ClientAdapter 选择）。**

理由：
- ProtocolFamily 是 **upstream wire format** 的 ID（19 family，参
  `protocol_selector.go:81` BuildDefaultProtocolAdapterRegistry），由 model alias
  + 账号绑定确定，不该被请求侧 spoof。Cursor 客户端用 OAuth 后调 `claude-3-7-sonnet`
  仍然必须由 anthropic_messages 上游处理 —— 否则 upstream call 无效。
- ClientAdapter 选择目前是隐式的（forwarder 直接走 OpenAI 形态返回；尚未实现
  `proto.ClientAdapter` 接口的注册表）。U6-D 的真正职责是给 ClientAdapter 选择
  注入 identity 信号。
- 安全：UA spoof 攻击若能改 ProtocolFamily 即可绕过 quota / 计费 —— 风险过高。

## 2. 边界场景

| 场景 | 客户端期望 | 上游 | U6-D 行为 |
|---|---|---|---|
| identity=cursor + model=claude-3-7-sonnet | OpenAI Chat shape (`data: {"choices":[…]}`) | anthropic_messages | 走"OpenAI ClientAdapter on Anthropic upstream"翻译路径（HCSF 中间态完整 round-trip）|
| identity=claude_code + model=gpt-4o | Anthropic Messages shape (`event: message_start…`) | openai_chat | 走"Anthropic ClientAdapter on OpenAI upstream"翻译 |
| identity=cursor + model=gpt-4o | OpenAI Chat shape | openai_chat | passthrough（同形态，最便宜） |
| identity=unknown | 沿用 path-based 默认（`/v1/chat/completions`→OpenAI；`/v1/messages`→Anthropic）| 任意 | 不基于 identity 改路径，按 path 判 |
| identity confidence < 0.7 | 同 unknown | 任意 | 降级 path-based，记 metric |

`fail` 不作默认 —— Feature Preservation Rule 要求保留功能。能力矩阵
（`capability_matrix.go`）已支持 client × upstream 翻译；缺失能力时返回
`ProtocolLossEntry` 而非 503。

## 3. clean-room 信号来源

**仅用公开行为，不读 Cursor / Claude Code / Cody 闭源代码。**

- Cursor：公开 changelog + UI 截图反映 SSE 块按 `choices[0].delta` 形态消费；
  HUAKAI 必须给 OpenAI Chat Completions 兼容 shape。证据需来自 OCAW 抓包或
  公开 issue tracker，记入 `docs/research/`。
- Claude Code：anthropic-cli 是闭源但 npm tarball 公开；其网络层只调
  `https://api.anthropic.com/v1/messages` 严格 Anthropic shape。
- Cody：sourcegraph/cody（Apache 2.0）公开 repo——可读，但 U6-D 仅用其行为
  描述，不复制结构。Cody 同时支持 OpenAI Chat 与 Anthropic Messages（按 model
  family 分），所以 identity=cody 时形态需结合 model family 二次决定。
- ChatUI：典型 OpenAI Chat 兼容（多数 self-host UI 直接 fork OpenAI SDK）。

输出标记：所有客户端形态结论 **必须** 注 "tolerant" 或 "strict"，并附 OCAW /
公开 issue 链接 timestamp（`feedback_no_training_memory` rule）。U6-D-2 atomic
的工作之一就是建立这张证据表。

## 4. 与 U7 passthrough field matrix 联动

**identity-aware passthrough = U7 第二阶段；U6-D 只暴露钩子，不引入 identity 维度
到 FieldMatrix key。**

- 当前 FieldMatrix key 是 `(ClientProtocol, UpstreamProtocol, fieldName)`。新增
  identity 会让 cardinality 19 × 6 × N 变 19 × 6 × N × 6，过早。
- U6-D 提供 `clientid.IdentityFromContext(ctx)` 已存在；后续 ClientAdapter 在
  `CanonicalEventToClientChunk` 内部可按 identity 调整 envelope merge 策略
  （如 Cursor 看到陌生字段会崩，则在 `MergeExtrasInto` 前 prune；Claude Code
  典型 tolerant 全透传）。
- 单一接入点：在 ClientAdapter 实现里调 `identityPassthroughPolicy(id) → {strict,
  tolerant, allowlist}`，policy 引用 FieldMatrix verdict 但不改其 key shape。

## 5. 实施 atomic 拆分

| Atomic | 内容 | LoC 上限 | 依赖 |
|---|---|---|---|
| U6-D-1 | `proto.ClientAdapterRegistry` 接口 + StaticImpl + `BuildDefaultClientAdapterRegistry`（OpenAI / Anthropic / Gemini 三家骨架，复用现有 typed struct） | ~150 | U6-A/B |
| U6-D-2 | clean-room 证据表 `docs/research/2026-05-08-client-shape-evidence.md`（Cursor/CC/Cody/ChatUI 形态 + 来源 + timestamp）| 0 LoC | 独立 |
| U6-D-3 | `gateway.SelectClientAdapter(ctx, path)` helper：先查 identity（≥0.7 conf 才信），否则 path-based fallback | <100 | U6-D-1 + U6-D-2 |
| U6-D-4 | forwarder 接入：`forwarder.go` 在响应序列化点用 SelectClientAdapter 替换当前隐式 OpenAI 输出（feature flag `HUAKAI_IDENTITY_AWARE_CLIENT_SHAPE`，默认 off） | <120 | U6-D-3 |
| U6-D-5 | `identityPassthroughPolicy(Identity) → Policy`（strict/tolerant/allowlist），ClientAdapter 内部消费；FieldMatrix 不改 schema | <80 | U6-D-4 + U7-E commit |
| U6-D-6 | acceptance tests：cursor↔anthropic、claude_code↔openai、unknown→path-based 三主轴；mismatch 不丢失 SSE | tests | U6-D-4 |

每 atomic 独立 commit + 独立 codex parallel-draft（feedback_parallel_for_code）。

## 6. Decision points 给 Owner

1. **D1**：U6-D-1 ClientAdapterRegistry 是否合并到 ProtocolAdapterRegistry 同
   一文件，还是新建 `client_adapter_registry.go`？sonnet 推荐独立文件（关注点
   分离）。
2. **D2**：feature flag 默认 off / canary on？sonnet 推荐 **off**，先收 OCAW 实证
   再切（execution_boundary_c：实测留 Owner 本机）。
3. **D3**：identity confidence 阈值（建议 0.7）是否 configurable？sonnet 建议
   **配置项**，默认 0.7，运维可调。
4. **D4**：identity=cursor + model=claude 翻译失败（如 tool_calls 跨形态丢失）时
   的语义——返回 OpenAI shape 但 `finish_reason="content_filter"`（lossy 标记），
   还是 502？sonnet 推荐 **lossy + ProtocolLossEntry 入审计**，不 502。
5. **D5**：U6-D 是否阻塞 U7-F（FieldMatrix 二阶段）？sonnet 推荐 **不阻塞**——
   U6-D-5 仅消费 FieldMatrix verdict，FieldMatrix schema 升级独立推进。

—— end U6-D design (sonnet lane). 待 codex parallel-draft 后入 synthesis。
