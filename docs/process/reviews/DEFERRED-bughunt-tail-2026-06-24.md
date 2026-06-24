# 对抗猎 bug 尾部延后项(2026-06-24)

本会话对 #106-119 这批改动做了一轮对抗猎 bug(workflow w36w60yh4,6 面 finder + 3×独立证伪),
共 7 个存活确认。其中 **5 个已修并合**:

| 缺陷 | 严重度 | PR |
|------|--------|-----|
| 流式工具附加费恒 $0(finishDraft 漏拷工具计数) | S1 | #120 |
| R7 身份改写无协议族门控(非 Anthropic 注入致上游 400) | S2 | #121 |
| R7 session 仅按账号派生(同账号跨会话共用 upstream session) | S2 | #121 |
| 孤儿追扣 hold 非 held 时假报已扣并标 reconciled | S2 | #122 |
| Rust 代理拨号/握手无超时(挂死代理永久阻塞 DoS) | S2 | #123 |

下面 **2 项经裁定延后**,记录于此防丢失,各带精确 file:line + 修复 spec。

---

## 延后① 工具调用次数仅 Anthropic 适配器计数(其余 provider 服务端工具按次附加费恒 $0)

- **严重度**:S1(money,但属覆盖扩展而非回归)
- **现状证据**:服务端工具计数只存在于 Anthropic SSE 解析
  (`backend/internal/proto/anthropic/sse.go:198-211` 与 `:388-406`,识别 `server_tool_use` 块累加
  `WebSearchCalls`/`FileSearchCalls`)。`backend/internal/proto/openai/*` 与
  `backend/internal/proto/gemini/*` **无任何服务端工具计数**(grep 0 命中)。
- **影响**:OpenAI Responses 族(`/v1/responses`,真实接线于
  `chat_completions_handler.go` 的 Responses 处理)使用内建 `web_search`/`image_generation`,以及
  Gemini grounding/search,均不递增计数 → env/draft 两侧恒 0 → `ApplyToolCallSurcharge` 判 IsZero
  跳过 → 这些 provider 的服务端工具按次附加费**永不向租户计收**,我方承担上游成本。
- **为何延后(非现修)**:给这两族加计数,**必须先核实其真实 wire 格式**——OpenAI Responses
  输出项里 `web_search_call`/`image_generation_call` 的确切结构与计费口径、Gemini grounding 元数据
  的结构。billing 改动靠猜 wire 格式会**过扣或漏扣**(过扣比漏扣更糟,直接伤客户)。需一份真实
  OpenAI Responses(带 server tool)响应样本 + Gemini grounding 样本对齐后再实现。
- **缓释**:**主流量(Anthropic / Claude Code)已全覆盖**(#114 非流式 + #120 流式);OpenAI
  Responses/Gemini 服务端工具是小尾部收入,短期不计收的财务暴露有限。
- **修复 spec**:在 openai responses adapter 的 buffered/stream usage 解析处识别 server-tool 输出项
  并递增 `CanonicalUsage.WebSearchCalls`/`ImageGenerationCalls`(对齐 anthropic sse.go 的 `server_tool_use`
  桶逻辑);Gemini 同理识别 grounding/search 元数据。每加一族补判别性测试(喂含 server-tool 的响应,
  断言计数非零;变异删赋值即 RED)。**实现前先抓真实 wire 样本**。

## 延后② priority_weighted 仅接线 chat_completions,其余 6 协议忽略 selection_mode

- **严重度**:S3(功能未闭环,**失败方向安全**=退回均匀 Shuffle,不亏钱/不串号/不崩溃)
- **现状证据**:加权选号仅在 chat_completions dispatch
  (`backend/internal/gatewayhttp/chat_completions_dispatch.go` 的 `activeBindingSelectionMode()`)生效;
  embeddings/rerank/images/completions/audio 各自 attempt 构造的 `pool.SelectionRequest` 不带
  `SelectionMode`(`backend/internal/embeddingshttp/attempt.go`、`rerankhttp/attempt.go`、
  `imageshttp/attempt.go`、`completionshttp/attempt.go`、`audiohttp/attempt.go`),Gemini 原生
  `backend/internal/geminihttp/generate_content.go` 的 `routerPoolMetadataFromRegistry` 不透传
  `SelectionMode` → `policy().SelectionMode` 恒空 → 永走均匀 Shuffle。
- **影响**:运营者对某 binding 开 `priority_weighted` 并按 `static_weight` 做账号流量倾斜时,
  期望全协议生效,但 6/7 协议族静默无效(只有 chat_completions 真加权)。倾斜策略被绕过,
  流量打到本应少用的账号——但不造成数据正确性事故。
- **为何延后**:fails-safe + opt-in(默认 strict_priority 不受影响),非线上事故;且这是
  PR#118 引入的能力的"广度未铺满",非数据回退。
- **修复 spec**:把 `SelectionMode` 注入从 chat_completions 专用路径下沉为各协议共享——
  让 geminihttp 等 6 处的 `routerPoolMetadataFromRegistry` 也透传 `binding.SelectionMode`,并在各
  attempt 的 `SelectionRequest` 据命中 binding 填 `SelectionMode`(抽公共 helper 供全协议复用),
  或在 selector 内据 `req.PoolGroupID` 反查 `PoolMetadata[poolGroupID].SelectionMode` 兜底。
  补一条跨协议接线测试,变异删字段即 RED。

---

**裁定人**:Claude(本会话),依据 CLAUDE.md §0 安全网纪律。**这两项不是被忽略,是带 spec 排期**。
Owner 若要优先做工具计数全 provider,请先提供 OpenAI Responses(带 server tool)与 Gemini grounding
的真实响应样本以核 wire 格式。
