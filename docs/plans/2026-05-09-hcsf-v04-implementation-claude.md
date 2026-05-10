# HCSF v0.4 Phased Delivery Plan — Claude (sonnet) Lane

**日期**: 2026-05-09
**Lane**: claude (sonnet via general-purpose subagent, parallel-draft per CLAUDE.md #10)
**Codex 平行 lane**: `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md`（写作时未读）
**前置决策（Owner 已批，不重复评估）**:
- D-HCSF: v0.4 合成方案 = OpenAI-compatible storefront + Anthropic-native side-entry + capability graph IR (11+ capabilities) + per-vendor native passthrough
- D-PMF: L3（AI agent 框架后端）+ L4（中国中转）
- D-METRIC: monthly annualized inference spend（OpenRouter $100M+ 锚点）
- D-PACE: 按 axis 完整度，10-15 周（项目不急 per `feedback_pace_not_urgent.md`）

**前置上下文文件**:
- `docs/plans/2026-05-09-hcsf-canonical-synthesis.md`
- `docs/research/2026-05-09-axis3-huakai-current-state.md`
- `docs/research/2026-05-09-axis3-protocol-translation-{litellm,portkey,envoy}.md`
- `docs/research/2026-05-09-issue-mining-cross-repo.md`
- `docs/research/2026-05-09-market-research-claude.md`

---

## TL;DR

1. **HCSF v0.4 = capability graph IR + 双入口 + native passthrough**：内部 IR 以 Anthropic-rich 为主干（cache_control / tool_use / thinking / signature 不丢），OpenAI 入口只是 IR 的一个 client view；vendor 之外有显式 `/v1/native/<vendor>/<endpoint>` 兜底超出 IR 表达力的能力（Computer Use / Live WSS / Server-side tools / MCP server）。
2. **Schema 设计 13 capability**：text / tool_use / thinking / cache_control / structured_output / file_attachment / image / audio / video / live_session / batch / mcp_server / data_retention（外加 computer_use 作 capability flag 不入 IR）。
3. **8 阶段, 12-15 周**：P-0 HCSF struct 实化 → P-1 capability graph IR → P-2 ClientAdapter Anthropic+OpenAI → P-3 Phase B Canonical→Provider 翻译 → P-4 native passthrough 端点 → P-5 capability matrix property test + cross-vendor invariant test → P-6 真账号 smoke (4 vendor, Owner 本机) → P-7 metric / spend 仪表 + L3+L4 PMF gate → 上线。
4. **Vendor 优先级**：完整 IR full-fidelity = Anthropic + OpenAI Chat + OpenAI Responses + Gemini + Bedrock-on-Anthropic（已有）；roadmap = Bedrock-on-Llama/Cohere/Mistral + xAI/Cursor/Copilot/Kiro/Windsurf/Antigravity（5 vendor session 反转 OCAW-gated）。
5. **三维 delta（架构 + 算法 + 生态）每 capability 都要列**，不能只画 ✓/✗：HUAKAI fusion = LiteLLM + Portkey + envoy-ai-gateway 各取所长 + mid-stream fallback continuation prompt + 跨账号 cache locality blend + per-vendor metric slicing 这三条 nobody-else 算法/生态升级。
6. **失败模式禁止 silent drop**：跨 vendor capability gap → 显式 `X-HUAKAI-Capability-Loss` header + ProtocolLossEntry 累计；streaming 中段 → mid-stream fallback continuation prompt 合成（三 lane upstream 都没做）；OpenAI lossy 字段 → 进 audit log + per-vendor 切片报表。
7. **L3+L4 PMF connection**：每阶段假想 monthly inference spend 数字 + capability supported % 作 gate，达不到不进下一阶段（pace not urgent，但 quality 必须真）。

---

## 1. HCSF v0.4 Schema 设计

### 1.1 设计原则

| 原则 | 含义 | 凭据 |
|---|---|---|
| Anthropic-rich 主干 | 顶层 IR 以 Anthropic Messages 为参考形态，因为它能无损表达 cache_control / tool_use signature / thinking 内容块；OpenAI/Gemini 是 view，不是主干 | issue-mining `Portkey#1579/#1589 / new-api#4678` 数据点显示 OpenAI canonical 会 strip cache_control |
| OpenAI 入口仍为 client storefront | 客户用 `/v1/chat/completions` 进来，进 IR 后内部 enrich，出去时若客户期望 OpenAI 输出，也能降阶；但 IR 内部不损失 | market research L3 (Cline/Cursor/Claude Code) 数据点 — OpenAI Chat 是事实标准入口 |
| Anthropic-native 侧门入口 | 客户用 `/v1/messages` 进来，IR 直接 1:1，不强制翻成 OpenAI form | issue-mining `sub2api#1331 / #1413 / #1451` 中文中转客户用 claude-code/codex 直连 |
| Per-capability schema | 每个 capability 是独立 sub-schema，可独立加 vendor binding；新 capability 不污染其他 capability | envoy-ai-gateway 的 per-endpoint canonical 启发（见 `axis3-protocol-translation-envoy.md` §3.1） |
| Native passthrough 兜底 | 任何 capability 对当前 vendor matrix 表达力不够时（Live WSS / Computer Use / Anthropic web_search hosted tool），客户走 `/v1/native/<vendor>/<endpoint>` 直连，HCSF 不强行翻 | LiteLLM 的 ANTHROPIC_HOSTED_TOOLS bypass pattern + envoy-ai-gateway 的 vendor extension 路径启发 |

### 1.2 顶层结构（去除 `proto.HCSF struct{}` 空壳，平铺 + 命名空间）

```
HCSF v0.4 IR
├── Request
│   ├── Model (resolved: protocol_family + vendor_model_name)
│   ├── Messages []CanonicalMessage   // role + []ContentBlock
│   ├── Tools []CanonicalTool         // 含 hosted_tool flag
│   ├── ToolChoice CanonicalToolChoice  // typed union, 不再 any
│   ├── MaxTokens, Temperature, TopP, TopK, StopSequences
│   ├── ReasoningConfig *CanonicalReasoning  // budget_tokens 原值, 不 bucket 化
│   ├── CacheConfig *CanonicalCacheConfig    // 显式 cache_control 表达
│   ├── ResponseFormat *CanonicalResponseFormat  // structured_output schema
│   ├── Stream bool, ParallelToolCalls *bool
│   ├── Metadata map[string]string  // user_id / session_id 等; 不带 vendor 私有 key
│   ├── VendorPassthrough map[string]json.RawMessage  // U7-A 已有
│   └── Capabilities []CapabilityHint  // 客户端声明意图（"我会用 thinking"）
├── Response
│   └── (mirror, 含 Usage 三分法)
└── StreamEvent (per chunk)
    └── (Anthropic 6 事件名 + OpenAI/Gemini 翻译目标)
```

**与现状的差异**：
- `proto.HCSF struct{}` 删除（per Q3 现状审计 — 顶层 wrapper 设计意图与实物形态不一致），代之以 `proto/canonical.go` 平铺 root-level types
- `CanonicalRequest.ToolChoice any` 改 typed union（解决 LiteLLM-style 各 vendor 形态不同的混乱）
- 新增 `Capabilities []CapabilityHint` — 让客户端声明意图，路由时可路给真支持该 capability 的 vendor

### 1.3 13 个 Capability 详细 schema

#### Cap-1 `text`

- **IR schema**: `ContentBlock{Type: "text", Text: string}`
- **Vendor 损耗矩阵**:
  | Vendor | Lossless | 备注 |
  |---|---|---|
  | Anthropic | ✓ | 原生 |
  | OpenAI Chat | ✓ | message.content string |
  | OpenAI Responses | ✓ | input/output_text |
  | Gemini | ✓ | parts[].text |
  | Bedrock-on-Anthropic | ✓ | delegate Anthropic |
- **Native passthrough**: 不需要

#### Cap-2 `tool_use`

- **IR schema**: `ContentBlock{Type: "tool_use", CallID, Name, Input json.RawMessage, Signature *[]byte}`
  - `Signature` 字段保留 Anthropic thought signature；OpenAI 无该字段，per LiteLLM 模式 stash 到 `function.provider_specific_fields["thought_signature"]`，反向能恢复（而非 LiteLLM 的 silent drop）
- **Vendor 损耗矩阵**:
  | Vendor | Lossless | 损耗 | 备注 |
  |---|---|---|---|
  | Anthropic | ✓ | — | tool_use block 原生 |
  | OpenAI Chat | partial | 64-char name truncation | per LiteLLM SHA-suffix algorithm（HUAKAI 加 collision detection + 失败时切 SHA-12，per `axis3-protocol-translation-litellm.md` §"借鉴 trap 4"）|
  | OpenAI Responses | partial | 同上 + `tools[]` 路径不同 | |
  | Gemini | partial | functionCall 一次性 args（无 partial_json delta） | streaming 时 Gemini 走 buffered path emit content_block_start + 完整 args + stop |
  | Bedrock-on-Anthropic | ✓ | — | 同 Anthropic |
  | Bedrock-on-Llama/Cohere | unsupported | tool_use 在 Llama/Cohere model 上不一致 | roadmap |
- **算法升级 vs LiteLLM**: LiteLLM 的 `function.arguments` JSON encode 一次、客户端再 decode 一次造成 args 字段稳定性问题（issue-mining `LiteLLM#27468`）。HUAKAI 在 IR 内部 `Input` 始终是 `json.RawMessage`（已 typed），`CanonicalToProviderRequest` 时一次 encode；client view 层再次 encode 时若是 OpenAI 路径才 stringify。
- **Native passthrough**: 不需要（基本能 lossless）

#### Cap-3 `thinking` (extended thinking / reasoning)

- **IR schema**:
  ```
  ContentBlock{Type: "thinking", ReasoningText string, Signature []byte}
  + Request.ReasoningConfig{
      BudgetTokens *int    // 原值, 不 bucket
      Effort *string       // OpenAI o-series style: minimal/low/medium/high
      // 至少一个非 nil; 互斥则取 BudgetTokens 优先
    }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | Lossless | 损耗 | 备注 |
  |---|---|---|---|
  | Anthropic | ✓ | — | thinking block + signature_delta carry-forward |
  | OpenAI Chat o-series | partial | budget_tokens 不暴露, 只 effort | HUAKAI 算法升级：保 budget_tokens 原值 IR 内部，vendor adapter 层 derive effort，**不在 canonical 层 bucket** (per LiteLLM trap §3 — bucketize 是 lossy pessimisation) |
  | OpenAI Responses | ✓ | — | reasoning items |
  | Gemini | partial | thought parts (`parts[].thought = true`)，无 signature | |
  | Bedrock-on-Anthropic | ✓ | — | delegate |
- **算法升级 (vs LiteLLM #4)**: budget_tokens 原值保留 + adapter 层映射，避免 4500→`low`→ default budget 的数值漂移
- **Native passthrough**: 不需要

#### Cap-4 `cache_control`

- **IR schema**:
  ```
  ContentBlock 上加可选字段 CacheControl *CacheControlMarker
  CacheControlMarker{
    Scope: "ephemeral" | "persistent"
    TTL: "5m" | "1h"   // Anthropic prompt-caching 标准
    Hint: string       // 客户端语义标签, e.g. "system_prompt", 用于 PASR cache locality 评分
  }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | Lossless | 损耗 |
  |---|---|---|
  | Anthropic | ✓ | 原生 |
  | OpenAI Chat | ✗ | OpenAI 隐式 prefix cache，无显式 marker；HUAKAI 在 PASR 路由层用 Hint 做 locality scoring，但 vendor 端无法标 |
  | OpenAI Responses | ✗ | 同上 |
  | Gemini | partial | cachedContent (separate API)；当前不映射到 inline marker |
  | Bedrock-on-Anthropic | ✓ | 同 Anthropic（已有 auto cache_control 注入路径） |
- **生态升级 (vs Portkey #1579/#1589 / new-api #4678)**: HUAKAI **必须**保 cache_control marker 不被剥离；同时**必须** sanitize gateway 自注入的动态 metadata header（issue `new-api#4678` 揭示的 cch=xxx 破坏 prefix cache 问题）— canonical 层不允许 admin 注入随请求变化的 header / system 后缀
- **算法升级 (生态)**: Hint 字段连接 PASR cache locality scoring (A2)，vendor-sliced metric 体现"per vendor cache hit rate"
- **Native passthrough**: 不需要

#### Cap-5 `structured_output`

- **IR schema**:
  ```
  Request.ResponseFormat{
    Type: "json_schema" | "json_object" | "text"
    Schema *json.RawMessage      // JSON Schema draft 2020-12
    Strict *bool                 // OpenAI strict mode flag
    Name *string
  }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | Lossless | 损耗 | 备注 |
  |---|---|---|---|
  | OpenAI Chat | ✓ | — | response_format |
  | OpenAI Responses | ✓ | — | text.format |
  | Anthropic | partial | 无 strict 字段，只能 system prompt 引导 | 写入 system prompt 时 sanitize（不破坏 cache）|
  | Gemini | partial | responseSchema + responseMimeType；strict 模式 disabled | |
  | Vertex Anthropic | partial | issue-mining `Portkey#1570` — Vertex 拒 output_config.format | HUAKAI fall back: 不送 schema 让 vendor passthrough 而不是失败 |
- **失败模式**: schema 无法翻译时 → 进 ProtocolLossEntry + capability_loss header，不 silent drop
- **Native passthrough**: 不需要

#### Cap-6 `file_attachment` / `image` / `audio` / `video`（multi-modal capability 群）

- **IR schema**:
  ```
  ContentBlock{
    Type: "image" | "audio" | "video" | "file"
    MediaType string             // MIME type, 显式
    Source: SourceBase64{Data []byte} | SourceURL{URL string} | SourceVendorRef{VendorID string}
    AdditionalProperties map[string]string  // for vendor-specific metadata
  }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | image base64 | image URL | audio | video | file PDF |
  |---|---|---|---|---|---|
  | Anthropic | ✓ | ✓ | ✗ | ✗ | ✓ (document block) |
  | OpenAI Chat | ✓ | ✓ | ✓ (audio chat input) | ✗ | ✓ (file_data) |
  | OpenAI Responses | ✓ | ✓ | ✓ | ✗ (Sora 走 image gen 端点) | ✓ |
  | Gemini | ✓ | ✗ (data URI only per envoy axis-3 §4.2) | ✓ | ✓ (generative-ai 主战场) | ✓ |
- **架构升级 (vs envoy-ai-gateway §4.2)**: HUAKAI 必须显式声明"是否下载远程 URL"策略 — envoy 只接 data URI，HUAKAI 默认行为：若 client 提供 URL 且 vendor 不接 URL，则 **gateway 侧 fetch + base64**（capability adapter 层 normalize），但有 size cap (10 MB) + latency budget
- **Native passthrough**: video / live → `/v1/native/gemini/live`（WSS）

#### Cap-7 `live_session` (Gemini Live WSS / OpenAI Realtime WSS)

- **IR schema**: 不进 HCSF main IR，直接走 native passthrough
- **Vendor**:
  | Vendor | passthrough endpoint |
  |---|---|
  | Gemini Live | `/v1/native/gemini/live`（WSS upgrade）|
  | OpenAI Realtime | `/v1/native/openai/realtime`（WSS upgrade）|
- **架构升级 (vs LiteLLM/Portkey)**: LiteLLM 不支持 Live；Portkey 有独立 `realtimeHandler.ts` (per `axis3-protocol-translation-portkey.md` §5)。HUAKAI 不重写状态机，只做透明 WSS proxy + auth substitution + per-msg metric counter

#### Cap-8 `batch` (OpenAI Batch / Anthropic Message Batches)

- **IR schema**:
  ```
  BatchSubmitRequest{
    Requests []HCSF.Request   // 每条仍是 HCSF Request
    CompletionWindow string   // "24h"
    Metadata map[string]string
  }
  BatchStatus{ID, Status, ResultsRef *string, FailureReason *string}
  ```
- **Vendor 损耗矩阵**:
  | Vendor | Submit | Status poll | Cancel | Partial output |
  |---|---|---|---|---|
  | OpenAI Batch | ✓ | ✓ | ✓ | ✗ (per Portkey #1156-1158) |
  | Anthropic Message Batches | ✓ | ✓ | ✓ | ✗ |
  | Gemini Batch | ✓ | ✓ | ✓ | partial |
- **架构升级 (生态)**: HUAKAI 把 batch 接成 axis-1 (account/quota) 内的 long-running job — 不在 axis-3 主流水线，但 IR 共用
- **Native passthrough**: 部分 vendor 用（OpenAI Batch 现有 SDK 习惯）

#### Cap-9 `mcp_server` (Model Context Protocol bridging)

- **IR schema**:
  ```
  Request.MCPServers []MCPServerRef{
    URL string
    Transport "stdio" | "sse" | "http"
    AuthRef *string  // 引用 BackendSecurityPolicy
  }
  ```
- **Vendor 损耗矩阵**: MCP 是 Anthropic-led 协议，OpenAI/Gemini 还在 catch-up；HUAKAI 当前作 Plugin 后置
- **生态升级 vs LiteLLM #7934 (closed) / Portkey #926 (open)**: 两家都还在 issue 阶段；HUAKAI 不在 P-0..P-7 主线，列入 P-8 roadmap
- **Native passthrough**: 不适用

#### Cap-10 `data_retention` / `audit_metadata`

- **IR schema**:
  ```
  Request.DataRetention *DataRetentionPolicy{
    DisableTraining *bool       // OpenAI store=false
    AuditTags []string
    PII Scrubbing config
  }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | DisableTraining | Audit |
  |---|---|---|
  | OpenAI Chat | ✓ (`store: false`, `safety_identifier`) | partial |
  | Anthropic | ✓ (no train opt-out via header) | ✓ |
  | Gemini | ✓ | ✓ |
- **生态升级 (vs envoy-ai-gateway)**: envoy-ai-gateway 用 BackendSecurityPolicy + DynamicMetadata；HUAKAI in-process 直接走 audit log handler 链 + DLQ
- **Native passthrough**: 不需要

#### Cap-11 `computer_use`

- **IR schema**: 不进 HCSF main IR (Anthropic 独有 schema, computer_20241022 等), 走 native passthrough only
- **Vendor**: Anthropic only (现状)
- **Native passthrough**: `/v1/native/anthropic/messages` with computer-use enabled headers

#### Cap-12 `web_search` / `code_execution` / `file_search` (hosted tools)

- **IR schema**: `Tools[]` 中 `HostedTool` 子类型
  ```
  CanonicalTool{
    Type: "function" | "hosted_web_search" | "hosted_code_exec" | "hosted_file_search" | "hosted_computer"
    Spec *FunctionSpec | *HostedToolSpec
  }
  ```
- **Vendor 损耗矩阵**:
  | Vendor | web_search | code_execution | file_search | computer |
  |---|---|---|---|---|
  | Anthropic | ✓ (hosted) | ✓ | ✓ | ✓ |
  | OpenAI Responses | ✓ (web_search_preview) | ✓ (code_interpreter) | ✓ (file_search) | ✗ |
  | Gemini | ✓ (groundingMetadata) | ✓ | ✗ | ✗ |
- **算法升级 (vs LiteLLM bypass)**: LiteLLM 把 ANTHROPIC_HOSTED_TOOLS 直接透传不翻；HUAKAI 在 IR 加 HostedTool 显式声明 + per-vendor mapping，跨 vendor 路由时若目标无该 tool → ProtocolLossEntry + 触发 capability mismatch 拒绝 (而不是默默丢)
- **Native passthrough**: hosted_computer → `/v1/native/anthropic/messages`

#### Cap-13 `vendor_extension`

- **IR schema**: VendorPassthrough map[string]json.RawMessage（已有 U7-A）
- **Vendor 损耗矩阵**: 永远 lossless (by design — 未知字段直接抓)
- **算法升级 (vs Portkey strictOpenAiCompliance binary flag)**: Portkey 的 `strictOpenAiCompliance` 是布尔总开关，HUAKAI 改为 per-field allow/deny + per-vendor 切片 audit
- **Native passthrough**: 不需要

### 1.4 Capability Matrix 总结表

| ID | Capability | 主 IR | Anthropic | OpenAI Chat | OpenAI Resp | Gemini | Bedrock-on-Anthropic | Native passthrough? |
|---|---|---|---|---|---|---|---|---|
| 1 | text | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | no |
| 2 | tool_use | ✓ | ✓ | partial (name trunc) | partial | partial (no delta) | ✓ | no |
| 3 | thinking | ✓ | ✓ | partial (effort) | ✓ | partial | ✓ | no |
| 4 | cache_control | ✓ | ✓ | ✗ (隐式) | ✗ (隐式) | partial | ✓ | no |
| 5 | structured_output | ✓ | partial | ✓ | ✓ | partial | partial | no |
| 6 | image | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | no |
| 6 | audio | ✓ | ✗ | ✓ | ✓ | ✓ | ✗ | partial (audio gen 端点) |
| 6 | video | partial IR | ✗ | ✗ | ✗ | ✓ | ✗ | yes (Veo / Sora) |
| 6 | file | ✓ | ✓ (PDF) | ✓ | ✓ | ✓ | ✓ | no |
| 7 | live_session | ✗ (passthrough only) | ✗ | ✗ | partial (realtime) | ✓ | ✗ | yes (WSS) |
| 8 | batch | ✓ | ✓ | ✓ | partial | ✓ | partial | partial |
| 9 | mcp_server | partial | ✓ (native MCP) | ✗ | ✗ | ✗ | ✓ | partial |
| 10 | data_retention | ✓ | ✓ | ✓ | ✓ | partial | ✓ | no |
| 11 | computer_use | ✗ (passthrough only) | ✓ | ✗ | ✗ | ✗ | ✓ | yes |
| 12 | hosted_web_search | partial IR | ✓ | ✗ | ✓ (preview) | ✓ (grounding) | ✓ | partial |
| 12 | hosted_code_exec | partial IR | ✓ | ✗ | ✓ (interpreter) | ✓ | ✓ | partial |
| 13 | vendor_extension | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | n/a |

---

## 2. 阶段分解 (12-15 周)

### P-0 HCSF struct 实化 + 命名空间清理（1 周）

- **Goal**: 删除 `proto.HCSF struct{}` 空壳，平铺 root types 到 `proto/canonical.go`，统一 `CanonicalRequest/Response/StreamEvent` 命名；新增 `Capabilities []CapabilityHint` + 统一 `CanonicalToolChoice` typed union；保 backward-compat 通过 alias
- **输入**: 现状 audit `docs/research/2026-05-09-axis3-huakai-current-state.md` Q3
- **输出**: `proto/canonical.go` (~200 LoC) + 现有 4 vendor adapter 改 import + alias 兼容层
- **工作量**: 1 周（含测试）
- **Owner 授权点**: 开始时（删除 HCSF wrapper 是 medium-risk 修改类型签名）
- **失败模式**: typed union 设计不当 → 后续 capability 加入时反复重塑；mitigations: P-0 末尾 mock-implement 13 capability 各 schema test，验 schema stability
- **三维 delta**: 架构（IR 平铺 + 命名空间）

### P-1 Capability Graph IR Schema（2 周）

- **Goal**: 落地 13 capability 的 sub-schema + capability matrix runtime；CapabilityMatrix 不再"DefaultMatrix() 用规则填"，改为基于声明 + property test 验证；HostedTool 子类型；ReasoningConfig（不 bucket）；CacheControlMarker
- **输入**: §1.3 schema 设计
- **输出**:
  - `proto/capability/text.go` `tool_use.go` `thinking.go` `cache.go` `structured_output.go` `multimodal.go` `hosted_tool.go` 等独立 sub-schema 文件 (每个 ~80-150 LoC)
  - `proto/capability_matrix.go` 重写 (~250 LoC) 含 `Get(client, upstream, capability) Verdict`
  - schema unit tests
- **工作量**: 2 周
- **Owner 授权点**: 进入 P-1 前（schema 定义大）
- **失败模式**: schema 设计偏 Anthropic 太厉害导致 OpenAI 主路径过度复杂 → mitigations: 每 capability 必须设计两条 client view（Anthropic-form + OpenAI-form），P-1 末尾 dual-form 翻译测验
- **三维 delta**: 架构（capability graph 抽象）+ 算法（cache_control hint → PASR locality scoring 接入点）+ 生态（capability matrix 暴露给 admin UI 让 operator 可见 vendor 真支持度）

### P-2 ClientAdapter 落地 (Anthropic + OpenAI Chat + OpenAI Responses)（2-3 周）

- **Goal**: 实现 3 个 ClientAdapter（之前全仓 0 个）：把 Canonical event → 客户端期望的 SSE chunk 形态；含 streaming wrapper（borrow LiteLLM `AnthropicStreamWrapper` mechanism, 不抄源码）
- **输入**: 现状 Q5 缺口 1（ClientAdapter 整体不存在）
- **输出**:
  - `proto/clientadapter_anthropic.go` (~250 LoC) — Canonical → Anthropic SSE chunks
  - `proto/clientadapter_openai_chat.go` (~300 LoC) — Canonical → OpenAI Chat SSE
  - `proto/clientadapter_openai_responses.go` (~350 LoC) — Canonical → OpenAI Responses SSE（含 response.output_item.added 等专属事件）
  - `gateway/forwarder.go` 去除 nil-fallback raw passthrough，强制选 ClientAdapter
  - tests: 12 fixture pair（client × upstream），每 pair 6 个 capability scenario
- **工作量**: 2-3 周
- **Owner 授权点**: 进入 P-2 前（这是 axis-3 的 raison d'être，开始落地真翻译）
- **失败模式**:
  - SSE 状态机 bug（content_block 序号错乱、tool_calls.index 累计错）→ mitigations: property test
  - 跨 vendor `tool_use.input` partial_json 重组等价性 → mitigations: F-PROTO-002 spec AT-PROTO-002-12 round-trip bijection test 全 matrix 跑
  - mid-stream 中段 client 协议突变（罕见）→ ignore，不支持
- **三维 delta**:
  - 架构（ClientAdapter 接口实化 + per-vendor SSE state machine）
  - 算法（borrow LiteLLM cross-chunk state pattern + 加 cross-attempt state continuity — LiteLLM 是 per-request scope，HUAKAI lift 到 attempt-lease 维度，跨 attempt 共享 block-index 计数器，让 mid-stream fallback 续约成为可能）
  - 生态（per-vendor SSE chunk-shape stability metric）

### P-3 Phase B Canonical → Provider Request 翻译（1-2 周）

- **Goal**: 实现 4 个 vendor adapter 的 `CanonicalToProviderRequest`（之前全部 ErrNotImplemented）
- **输入**: §1 IR schema + 现状 Q2 缺口 2 (Phase B 空)
- **输出**:
  - `proto/anthropic_sse.go` 加 `CanonicalToProviderRequest` (~150 LoC delta)
  - `proto/openai_sse.go` 同上（含 64-char tool name truncation algo + 反向恢复 mapping）
  - `proto/gemini_sse.go` 同上
  - `proto/bedrock_eventstream.go` 同上（覆 Llama/Cohere/Mistral 时返回 ErrCapabilityUnsupported, 不 ErrNotImplemented）
  - tool_choice / response_format / structured_output schema mapping
- **工作量**: 1-2 周
- **Owner 授权点**: P-3 入口
- **失败模式**: tool name SHA-truncation 撞名 → HUAKAI 加 collision detection，撞名时切 SHA-12（per `axis3-protocol-translation-litellm.md` §"借鉴 trap 5"）
- **三维 delta**: 架构（Phase B 完整化）+ 算法（tool name collision detection — vs LiteLLM 仅 SHA-8 一档）

### P-4 Native Passthrough 端点（1-2 周）

- **Goal**: 实现 `/v1/native/<vendor>/<endpoint>` 透明代理路径，覆盖 IR 不能表达的能力（Computer Use / Live WSS / Anthropic web_search hosted tool / OpenAI Realtime）
- **输入**: §1.1 设计原则 5
- **输出**:
  - `gatewayhttp/native/router.go` (~150 LoC) — URL match + auth substitution + capability tag
  - `/v1/native/anthropic/messages` (含 computer_use beta header passthrough)
  - `/v1/native/openai/responses` (含 server-side tools)
  - `/v1/native/gemini/live` (WSS upgrade)
  - `/v1/native/openai/realtime` (WSS upgrade)
  - native 路径 metric: per-vendor count + duration（不进 capability matrix）
- **工作量**: 1-2 周
- **Owner 授权点**: P-4 入口（WSS upgrade 是 production-critical 路径）
- **失败模式**:
  - WSS proxy 中段断 → upstream/downstream session 不对称（HUAKAI in-process 比 envoy ext_proc 简单，但仍要做 close-half-duplex）
  - native 路径绕过 capability matrix → admin UI 必须明确标"native passthrough = full vendor 风险，包括他们改 schema"
- **三维 delta**: 架构（passthrough 路径独立于主翻译流水线 — 这是 LiteLLM/Portkey 都没做的；envoy 通过 `requestHandlers` escape hatch 接近，但仍是 ext_proc 逻辑内）

### P-5 Capability Matrix Property Test + Cross-Vendor Invariant Test（2 周）

- **Goal**: 满足 F-PROTO-002 spec AT-PROTO-002-15（capability matrix matches reality: every cell asserted via property test running each (client × upstream) pair through multi-feature canonical request）
- **输入**: §1.4 capability matrix 总结表
- **输出**:
  - `proto/capability_matrix_property_test.go` (~400 LoC) — 13 capability × 5 client view × 5 upstream = 325 cell scenarios（多数 cell 是 N/A skip，估计 ~80 cell 实际跑）
  - tool_use round-trip bijection test (AT-PROTO-002-12) — OpenAI partial_json → canonical → Anthropic input_json_delta → OpenAI partial_json 等价 final JSON
  - cache_control marker preservation test — 每个 vendor 的 cache_control 是否被剥离 / 重写 / 保留
  - thinking budget_tokens 不漂移 test — 4500 budget 不被 bucket
  - tool name collision detection test — 注入 collision 验 SHA-12 fallback
- **工作量**: 2 周
- **Owner 授权点**: P-5 入口
- **失败模式**: property test 覆盖完成但 cell 实际行为与 matrix 表不符 → admin UI 必须显示"已验证 cell" vs "声明但未测 cell"，不允许混淆
- **三维 delta**: 生态（property test 推 capability matrix 从"声明"到"实测"，per-vendor 切片显示）

### P-6 真账号 Smoke Test（4 vendor, Owner 本机）（2 周）

- **Goal**: anthropic / openai / gemini / openai_codex 4 vendor 真上游 smoke per `project_real_vendor_account_scope.md`；其他 vendor 全 mock
- **输入**: P-5 输出（property test 通过）
- **输出**:
  - `tests/smoke/real_anthropic_test.go`
  - `tests/smoke/real_openai_chat_test.go`
  - `tests/smoke/real_openai_responses_test.go`
  - `tests/smoke/real_gemini_test.go`
  - `tests/smoke/real_openai_codex_session_test.go`
  - 每个含 6 capability 子测试 (text / tool_use / thinking / cache_control / structured_output / image)
  - **Owner 本机执行报告** 模板 (latency / cache hit rate / tool name collision rate / mid-stream-failure rate baseline)
- **工作量**: 2 周（含 Owner 本机 iteration 时间）
- **Owner 授权点**: P-6 入口（涉及真凭据 + 真扣费，必须 Owner 同意）
- **失败模式**:
  - Owner 凭据异常 → 测试卡住 / 误报；mitigations: 每个 smoke test 必须有 quick auth-only sanity check (低 token 单次)，再跑 capability scenarios
  - 真上游临时不稳 → 区分 HUAKAI bug vs upstream incident；mitigations: 每个 capability scenario 重试 3 次，3 次都失败才报 HUAKAI 失败
- **三维 delta**: 生态（real-vendor metric per capability per slice — 比 LiteLLM/Portkey/envoy 的 mock-only test 都强）

### P-7 Metric / Spend 仪表 + L3+L4 PMF Gate（1-2 周）

- **Goal**: 实装 D-METRIC（monthly annualized inference spend）+ L3+L4 capability supported % gate；per-vendor monthly 假想 spend 数字达不到时 admin UI 显示 GATE 红色
- **输入**: P-6 完成 + Owner 决策
- **输出**:
  - `metric/spend.go` (~150 LoC) — daily inference spend rollup → monthly annualized
  - `metric/capability_pmf.go` (~100 LoC) — capability supported % per L3/L4 客户层
  - admin UI dashboard tile `Monthly Annualized Inference Spend` + `Capability PMF Gate (L3) / (L4)`
  - per-阶段 gate 配置: 进 P-X 前 inference spend ≥ X / capability_supported ≥ Y%
- **工作量**: 1-2 周
- **Owner 授权点**: P-7 入口（dashboard 是 production-critical operator 入口）
- **失败模式**: PMF gate 数字假象 — Owner 自测的 4 vendor smoke 是 baseline 不是 PMF；mitigations: 仪表显示来源 (smoke-baseline / mock-projection / real-customer)，不混淆
- **三维 delta**: 生态（spend metric + PMF gate 是 OpenRouter $100M anchor 的 HUAKAI 对应）

### P-8 (Roadmap, 不在 12-15 周窗口)

- non-Anthropic Bedrock 子模型 (Llama/Cohere/Mistral)
- 5 vendor session 反转（Cursor/Copilot/Kiro/Windsurf/Antigravity）— OCAW-gated
- xAI Grok native (现是 OpenAI compat 假设)
- mid-stream fallback continuation prompt synthesis (核心差异化 — 提到下一个 sprint)
- mcp_server bridging (cap-9)
- batch (cap-8) full coverage

### 阶段汇总表

| Phase | Goal | 时长 | 累计 | Gate |
|---|---|---|---|---|
| P-0 | HCSF struct 实化 | 1w | 1w | schema stability mock test |
| P-1 | Capability graph IR | 2w | 3w | 13 capability schema unit test |
| P-2 | ClientAdapter 3 个 | 2-3w | 5-6w | 12 fixture pair × 6 capability cross-test |
| P-3 | Phase B Canonical→Provider | 1-2w | 6-8w | 4 vendor request building 真请求形态对（vs vendor SDK 期望）|
| P-4 | Native passthrough | 1-2w | 7-10w | WSS upgrade 不丢半双工; computer_use header 透 |
| P-5 | Capability matrix property test | 2w | 9-12w | F-PROTO-002 AT-PROTO-002-15 通过 |
| P-6 | 真账号 smoke (4 vendor) | 2w | 11-14w | 4 vendor × 6 capability smoke 通过率 ≥ 95% |
| P-7 | Spend metric + PMF gate | 1-2w | 12-16w | dashboard 上线 + Gate 配置 |

**总时长**: 12-15 周（中位数 13 周）— 与 §3.5 of canonical-synthesis 估算 10-15 周对齐，并落实到 8 阶段 Gate

---

## 3. Vendor Adapter 优先级（基于 D-PMF L3+L4）

### Tier-A 必须完整 IR 支持（P-0..P-6 主线）

| Vendor | Adapter 状态 | 优先级 | 备注 |
|---|---|---|---|
| **Anthropic Messages (API key + Bedrock-on-Anthropic)** | Provider→Canonical ✓; Phase B 待 P-3; ClientAdapter 待 P-2 | A0 | rich IR 主干；L4 中文 claude-code 主入口 |
| **OpenAI Chat Completions** | Provider→Canonical ✓; Phase B 待 P-3; ClientAdapter 待 P-2 | A0 | 事实标准入口；L3 主战场 |
| **OpenAI Responses API** | Provider→Canonical 部分（复用 OpenAIAdapter 假设, 待 P-3 加专属事件）; ClientAdapter 待 P-2 | A1 | issue-mining `Portkey#1583 / sub2api#594 / new-api#1216/#1812` — 高频需求 |
| **Gemini API (text + thinking + functionCall + cachedContent + image)** | Provider→Canonical ✓; Phase B 待 P-3; ClientAdapter 待 P-2; cache observation 缺位 P-1 修 | A1 | L3 + multi-modal |
| **Bedrock-on-Anthropic** | 已完整（A1-A4 落地）| A0 (existing) | 已完整, P-3 仅补 CanonicalToProviderRequest |

### Tier-B Roadmap (P-8+)

- **OpenAI Codex (ChatGPT Plus session)** — scaffold 在，OCAW 待 Owner 本机采集；中文 L4 真凭证测试限定 4 vendor 之一（included in 真账号 smoke）
- **Bedrock-on-Llama / -Cohere / -Mistral** — Owner 没 AWS 凭据 (per `project_no_aws_credentials.md`)，不进 P-6 真测；schema 准备在 P-1 留 capability `unsupported` 标记
- **xAI Grok native (vs current OpenAI-compat 假设)** — 当前复用 OpenAIAdapter，待 fixture 验证；保留为 P-8
- **Cursor / Copilot / Kiro / Windsurf / Antigravity (5 session 反转)** — per `feedback_skip_vendor_implementation_test.md` 用 sub2api / cursor-api 等成熟项目逻辑作行为参考；不进 P-6 真测
- **xAI / Mistral / DeepSeek / Together / Perplexity / Fireworks / GroqCloud (OpenAI-compat 兼容路径)** — 当前 PassthroughAdapter 完整，IR 路径直接复用 OpenAI Chat ClientAdapter，不需要单独 adapter

### Tier-C 不在 12-15 周做

- MCP server bridging (cap-9)
- Live WSS / Realtime (cap-7) — P-4 做 native passthrough 但不进主 IR
- Computer Use (cap-11) — P-4 native passthrough only
- Batch (cap-8) — P-4 部分 native passthrough; full coverage roadmap

---

## 4. 测试策略

### 4.1 Unit Tests (per capability)

- 13 capability × 每 capability 6-12 case = ~120 unit test
- mock vendor SSE / response shape, 验 IR 翻译 round-trip 等价性
- 每个 capability 必须含: 正常 case, lossy degrade case (不抛错; 进 ProtocolLossEntry), unsupported case (抛 ErrCapabilityUnsupported)

### 4.2 Property Tests (canonical round-trip)

- F-PROTO-002 AT-PROTO-002-12 (tool-call ID round-trip bijection): 5 vendor pair × 100 random tool-name + tool-args input
- F-PROTO-002 AT-PROTO-002-15 (capability matrix matches reality): per-cell property — 一对 (client view, upstream) × multi-feature canonical → 验真翻译路径产生 vendor 期望形态
- partial_json 拼接等价测试: OpenAI tool_calls.arguments delta + Anthropic input_json_delta 拼出 final JSON 等价（issue-mining `LiteLLM#27468` 修复点）

### 4.3 Capability Matrix Integration Tests

- 每个 P 进入下一阶段前，capability matrix gate: 实测 cell 通过率 ≥ 80%
- mid-stream 断流测试: 注入 abort 后验 ProtocolLossEntry 准确性
- gateway 注入 metadata sanitizer 测试: 注入 cch=xxx 不被 propagate (per `new-api#4678` 修复点)

### 4.4 真账号 Smoke (Owner 本机)

- 4 vendor × 6 capability scenario（text / tool_use / thinking / cache_control / structured_output / image）
- 每 scenario quick auth check + 低 token 实跑
- Owner 本机跑 daily（自测）+ weekly（Owner 校对 baseline）
- per-vendor metric: latency p50/p95, cache_hit rate, tool name collision rate, mid-stream fallback success rate

### 4.5 Cross-Repo Issue Regression Tests

精选 issue-mining 中的高 reaction issue 复现作 regression 防护:
- `Portkey#1579 / #1589` — cache_control.scope 跨平台保留
- `LiteLLM#27468` — OpenAI→Anthropic args lost
- `sub2api#2245 / #1552` — mid-stream 无 terminal event
- `new-api#4678` — gateway 注入 metadata 破坏 prefix cache
- `new-api#4697` — Anthropic 流缺 sentinel (content_block_stop / message_delta / message_stop)
- `LiteLLM#23777` — multi-OAuth ChatGPT 单进程

每条 issue → 对应 1 个 regression test fixture，CI 每 commit 跑

---

## 5. 失败模式 + 检测

### 5.1 跨 vendor capability 不存在 — 不许 silent drop

| 失败模式 | 检测 | 处理 |
|---|---|---|
| ClientAdapter 跨 vendor 路由后, capability matrix 显示 cell unsupported | property test + admin UI 显示 unsupported | client view 拒接请求 + `X-HUAKAI-Capability-Loss: <cap-id>` header + ProtocolLossEntry 累计 |
| LiteLLM-style if/elif 没 else fallthrough | code review + lint rule (no fallthrough drop) | mandatory `default:` branch 进 ProtocolLossEntry |
| Anthropic-hosted tool 路由到 OpenAI vendor | capability matrix `hosted_*` 列检查 | 显式拒绝 + 提示客户端用 native passthrough endpoint |

### 5.2 streaming 中断 fallback — LiteLLM `MidStreamFallbackError` 模式 (HUAKAI 升级)

| 失败模式 | 检测 | 处理 |
|---|---|---|
| upstream stream ended without terminal event (issue `sub2api#1552`) | StreamForwarder Phase D drain + last-sentinel watchdog | 强制补 sentinel (content_block_stop + message_delta + message_stop) + ProtocolLossEntry |
| stream 中段 5xx / connection drop (issue `Portkey#1047`) | SSE body inspect (不仅 HTTP code) | trigger mid-stream fallback continuation prompt synthesis (HUAKAI 算法升级 — 跨 attempt 续约, 含已 stream 出去的 partial assistant content) |
| stream 中段时 client_gone (issue `new-api#4168`) | upstream stream still active 但 downstream socket gone | 4-状态计费表区分: `client_gone` / `upstream_timeout` / `output_token_zero` / `upstream_5xx` |
| holding_chunk 永不释放 (LiteLLM trap §2) | timeout watchdog | first-chunk-or-200ms whichever-first 释放 |

### 5.3 Anthropic-rich → OpenAI lossy 字段审计

| 字段 | OpenAI lossy? | 检测 + 处理 |
|---|---|---|
| `cache_control.scope` | ✓ (隐式 prefix cache 无 marker) | ProtocolLossEntry; per-vendor cache hit metric 切片显示 |
| `thinking.signature` | ✓ | LiteLLM-style stash 到 `provider_specific_fields["thought_signature"]` 反向能恢复 |
| `tool_use.signature` | ✓ | 同上 |
| `cache_creation_input_tokens` | ✓ | Usage `_cache_creation_input_tokens` PrivateAttr (per LiteLLM `types/utils.py:1527-1533`) |
| `web_search hosted tool` | ✓ | unsupported 拒绝 + 提示 native passthrough |
| `computer_use` | ✓ | unsupported 拒绝 + 提示 native passthrough |

### 5.4 真凭证错误

| 错误 | 检测 | 处理 |
|---|---|---|
| 401 Unauthorized | smoke test quick auth | Owner 本机指引 `OWNER_AUTH_FAIL` 流程 |
| 403 Forbidden (region block / Anthropic 实名 ID 风控) | response body inspect | 进 audit log + 标 vendor 状态 unhealthy |
| 429 Rate Limit | header `Retry-After` parse | per-vendor cooldown 配置 (sub2api #641 借鉴 — 不强制 PST 午夜, 看 tier) |
| invalid_request_error + body sniff | per LiteLLM `exception_mapping_utils.py:399-494` substring before status-code 优先级 | normalized error class — HUAKAI 拆 vendor 注册表（不像 LiteLLM 2.5K LoC 单文件）|
| 5xx upstream | 同上 + retry policy | retry handler + circuit breaker |

### 5.5 安全/合规失败模式

| 失败模式 | 处理 |
|---|---|
| gateway 自注入动态 header (issue `new-api#4678`) | system prompt 路径 sanitizer; 不允许带 timestamp / req_id 等动态 token |
| PyPI-style 供应链投毒 (issue `LiteLLM#24512` 1113 reactions) | release 走 SLSA + 签名; 主二进制不动态加载 plugin |
| SSRF via provider URL fragment (issue `Portkey#1596`) | URL allowlist + path sanitization |

---

## 6. PMF + Metric 连接

### 6.1 每阶段 monthly annualized inference spend 假想数字

| Phase | 进入条件 (假想 spend) | Capability supported % | L3 PMF gate | L4 PMF gate |
|---|---|---|---|---|
| P-0 | $0 (start) | 25-30% (现状) | n/a | n/a |
| P-1 | $1K/mo equivalent (mock + smoke baseline) | ≥ 35% | not gated yet | not gated yet |
| P-2 | $5K/mo | ≥ 50% | partial (Anthropic + OpenAI) | partial |
| P-3 | $10K/mo | ≥ 70% (text/tool_use/thinking 全 vendor) | gating ON | gating ON |
| P-4 | $30K/mo | ≥ 80% (含 Live + Computer Use native passthrough) | gating ON | gating ON |
| P-5 | $100K/mo | ≥ 90% (capability matrix property test 全 cell pass) | gate green | gate green |
| P-6 | $300K/mo (含 Owner 本机 4 vendor 真测) | ≥ 95% | gate green | gate green |
| P-7 | $1M/mo annualized = HUAKAI MVP target | ≥ 95% | dashboard live | dashboard live |

### 6.2 L3+L4 服务度 = capability supported % per slice

- L3 (AI agent 框架后端) 关键 capability: text + tool_use + thinking + structured_output + cache_control + parallel_tool_calls — 6/13
- L4 (中国中转) 关键 capability: text + tool_use + thinking + cache_control + image + (codex session 反转 + claude-code 反转) — 6/13 主路径 + 2 vendor session
- 每阶段必须显示 per-slice supported %, 不混淆

### 6.3 Gate 进入下一阶段

- 当前 phase 的 acceptance test 通过 ≥ 95%
- capability supported % ≥ table 6.1 列值
- 上一 phase 没有遗留 HIGH-severity bug
- Owner 显式批准（per S-001/S-002 start gate）

---

## 7. Fusion-upgrade 三维 delta 表（per capability + vendor adapter）

### 7.1 Per-Capability Delta（13 capability）

| Capability | LiteLLM ref | Portkey ref | envoy-ai-gateway ref | HUAKAI delta | 维度 |
|---|---|---|---|---|---|
| text | `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1873-1876` | `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:427-488` | `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:100-117` | per-capability sub-schema 文件而非单一 ContentBlock 大 union; capability matrix 真测 cell | 架构 |
| tool_use | `litellm/llms/anthropic/.../adapters/transformation.py:584-617` (name SHA-8) | `src/providers/anthropic/chatComplete.ts:599` (tool_use 块 filter out 折叠到 tool_calls) | `internal/translator/openai_awsbedrock.go:298-419` (in-translator tool_use → ToolUseBlock) | name collision detection + SHA-12 fallback; signature 字段保留不靠 LiteLLM provider_specific_fields 隐式 | 算法 |
| thinking | `litellm/.../adapters/transformation.py:668-707` (budget_tokens bucket 2K/5K/10K) | `src/providers/anthropic-base/messages.ts:3-67` (thinking field as Anthropic native, OpenAI canonical 不映射) | `internal/translator/anthropic_helper.go:637-654` (mapReasoningEffortToOutputConfigEffort) | budget_tokens 原值保留, vendor adapter 内 derive effort, 不在 IR 层 bucket | 算法 |
| cache_control | `litellm/types/utils.py:1527-1533` (private attrs); `:1615-1635` (folding) | `src/providers/anthropic-base/messages.ts` (Anthropic-only); `src/providers/anthropic/chatComplete.ts:617-626` (lossy in OpenAI canonical) | `internal/translator/openai_awsbedrock.go:251-254, 285-290` (CachePoint inline marker per content block) | cache_control marker preserve + sanitize gateway-injected dynamic header (per new-api#4678) + Hint 字段对接 PASR cache locality scoring | 架构 + 算法 + 生态 |
| structured_output | `litellm/llms/.../transformation.py` (response_format 透传) | `src/providers/openai/chatComplete.ts:124-132` (verbosity / prompt_cache_key) | `internal/translator/openai_awsbedrock.go` (output_config.format) | Vertex Anthropic Extra inputs not permitted (Portkey#1570) → fallback 不送 schema 而非失败 | 算法 |
| image (multi-modal) | (not specifically called out) | `src/providers/anthropic/chatComplete.ts:198-237` (data URI parse + media_type) | `openai_awsbedrock.go:256-291` (data URI only, no remote URL fetch) | gateway-side fetch + base64 with size cap (HUAKAI 不限于 data URI) + per-vendor URL/base64 偏好检测 | 架构 + 算法 |
| audio | (limited) | `requestBody.ts:461-465` (audio + modalities) | `endpointspec.go:127` (/v1/audio/speech canonical = OpenAI SpeechRequest) | per-vendor audio gen passthrough (audio gen 端点独立) | 架构 |
| video | (none) | (limited - createSpeech only) | (limited) | Gemini Live native passthrough only (HUAKAI 不强行翻 video → 其他 vendor) | 架构 (passthrough boundary) |
| live_session | (none) | `realtimeHandler.ts` (92 LoC) + `realtimeLlmEventParser.ts` | (none) | 透明 WSS proxy + auth substitution + per-msg metric (HUAKAI 不重写 session 状态机, 比 Portkey 90 LoC realtime parser 简单) | 架构 + 生态 |
| batch | `litellm/llms/...batch` (有支持) | `requestBody.ts` createBatch endpoint + Bedrock S3 requestHandlers | `endpointspec.go` (batch 不在主 endpoint list) | batch job 接 axis-1 account/quota long-running queue (HUAKAI fusion: batch + quota + audit 在一处) | 生态 |
| mcp_server | (LiteLLM#7934 closed) | (Portkey#926 open) | (none) | P-8 roadmap; capability matrix 标 unsupported now | (roadmap) |
| data_retention | (limited) | `src/providers/types.ts` (config validation) | BackendSecurityPolicy independent CRD | data_retention as canonical capability + audit log handler chain + DLQ + priority lanes | 生态 |
| computer_use | (none / Anthropic-hosted bypass at `transformation.py:811-816`) | (none) | (none) | native passthrough + capability flag (HUAKAI 不强行翻; LiteLLM 透传 dict 但假设 destination = Anthropic-native) | 架构 |
| hosted tools (web_search/code_exec) | bypass at `:811-816` + `:996-1029` (set web_search_options) | (limited) | (limited) | HostedTool typed in IR + capability matrix 显式声明 cross-vendor mapping; 跨 vendor 路由时不匹配 → reject + 提示 native passthrough | 算法 |
| vendor_extension (passthrough) | (limited / private attrs only) | `strictOpenAiCompliance` 二进制开关 | per-endpoint extras | per-field allow/deny + per-vendor 切片 audit (HUAKAI 不是二进制 strict 开关) | 算法 + 生态 |

### 7.2 Per-Vendor Adapter Delta（5 Tier-A）

| Vendor adapter | LiteLLM ref | Portkey ref | envoy-ai-gateway ref | HUAKAI delta | 维度 |
|---|---|---|---|---|---|
| Anthropic Messages | `litellm/llms/anthropic/.../adapters/transformation.py` (1500+ LoC) | `src/providers/anthropic/chatComplete.ts` (600+ LoC) | `internal/translator/anthropic_anthropic.go` (323 LoC passthrough) | rich IR 主干 (cache_control + signature + thinking 全保) + 跨 attempt streaming state continuity | 架构 + 算法 |
| OpenAI Chat | `litellm/llms/openai/...` (multi-file) | `src/providers/openai/chatComplete.ts` | `internal/translator/openai_openai.go` (passthrough 315 LoC) | 64-char tool name SHA-8 → SHA-12 collision fallback (vs LiteLLM 仅 SHA-8) + cache_control 隐式 metric 切片 | 算法 + 生态 |
| OpenAI Responses | (limited) | `src/handlers/.../responsesHandler.ts` (per Portkey#1583 buggy) | `endpointspec.go:128-145` (/v1/responses → translator switch) | response.output_item.added 等专属事件全实现 (vs HUAKAI 现状复用 OpenAIAdapter) | 架构 |
| Gemini API | `litellm/llms/gemini/chat/transformation.py:18-22` (extends Vertex Gemini) | `src/providers/google/chatComplete.ts` (892 LoC) | `internal/translator/openai_gcpvertexai.go` | cachedContentTokenCount → cachemetrics.Observe (HUAKAI 现状缺位 per Q3 audit) + image data-URI vs URL 双路径 | 架构 + 生态 |
| Bedrock-on-Anthropic | (limited) | `src/providers/bedrock/index.ts` (`getConfig` model prefix dispatch) | `internal/translator/openai_awsbedrock.go` (1075 LoC) | A1-A4 atomic 已落; P-3 仅补 CanonicalToProviderRequest;  AWS SigV4 自实现 (vs envoy 用 SDK) + auto cache_control 注入 | 算法 |

### 7.3 HUAKAI 整体 fusion 三维 delta（vs upstream all 3）

- **架构升级**:
  1. capability graph IR (per-capability sub-schema) — LiteLLM 单 OpenAI canonical 太窄, Portkey bi-canonical 还是分裂, envoy per-endpoint canonical 思路 closest 但仍以 endpoint 为粒度
  2. 双入口 storefront + side-entry — LiteLLM 单入口, envoy per-endpoint 接近但客户视角混乱
  3. 显式 native passthrough endpoint — LiteLLM 通过 ANTHROPIC_HOSTED_TOOLS bypass 隐式做, Portkey 无, envoy `requestHandlers` 接近但仍在 ext_proc 内
  4. cross-attempt streaming state continuity (mid-stream fallback continuation prompt synthesis 的基础) — 三家都不做
- **算法升级**:
  5. tool name SHA collision detection + SHA-12 fallback (vs LiteLLM 仅 SHA-8)
  6. budget_tokens 原值保留 + vendor adapter 内 derive effort (vs LiteLLM bucket 2K/5K/10K)
  7. mid-stream fallback continuation prompt synthesis (跨 attempt 续约, 含已 stream 出去的 partial assistant content) — 三家都没做
  8. cache locality + headroom score blend (PASR-A2 已落) — 升级到 cache_control Hint 接入
- **生态升级**:
  9. capability matrix property test 推 cell 从"声明"到"实测" — 三家都用 hand-typed 表
  10. per-vendor metric slicing (cache hit / tool name collision / mid-stream fallback rate) — LiteLLM 整体折叠, Portkey 部分, envoy 通过 DynamicMetadata 但聚合视角弱
  11. spend metric + L3/L4 PMF gate — 三家无对应概念
  12. gateway-injected metadata sanitizer (per `new-api#4678`) — 三家都没明确
  13. 4-状态计费区分 (client_gone / upstream_timeout / output_token_zero / upstream_5xx) — `new-api#4168` 仍 open

---

## 8. Decision Points (DECISION-POINT 标记)

### DECISION-POINT D-1: P-0 是否删除 `proto.HCSF struct{}` 空壳？

- **背景**: 现状 Q3 audit 揭示设计意图（OpenAI Responses 风格）与实物形态（Anthropic 风格 Canonical*）不一致；空壳让 ClientAdapter / Phase B 实现无 anchor
- **选项**:
  - (a) 删除 + 平铺命名空间到 `proto/canonical.go`（推荐）
  - (b) 保留 + 实化为 envelope wrapper, 把 Canonical* sub-types 包进去
- **我的推荐**: (a)，因为现有 4 vendor adapter 都已直接用 Canonical* 类型，wrapper 反而是历史包袱
- **Owner 待决**: 接受 (a) 即开始 P-0

### DECISION-POINT D-2: tool name truncation 算法 — SHA-8 还是 SHA-12 with collision detection？

- **背景**: LiteLLM 用 SHA-8 (1/2^32 撞概率)；HUAKAI 在大客户高 tool 数场景 (>10K tools) 可能撞名
- **选项**:
  - (a) SHA-8 + collision detection + 撞名时切 SHA-12 fallback（HUAKAI 升级）
  - (b) 直接 SHA-12（更稳但 4 字符吃 truncated name 长度）
  - (c) SHA-8 + collision-pretty-fail（不静默 fall through）
- **我的推荐**: (a) — 默认行为兼容 LiteLLM 心智，撞名时升级
- **Owner 待决**: 接受 (a) 即写入 P-3 schema

### DECISION-POINT D-3: native passthrough 路径鉴权策略

- **背景**: `/v1/native/<vendor>/<endpoint>` 直连 vendor，绕过部分 capability matrix 验证
- **选项**:
  - (a) 默认 enabled, admin UI 显示风险标志
  - (b) 默认 disabled, opt-in per tenant
  - (c) 默认 enabled, 但每个 native 调用记录 capability_loss = "native passthrough (full vendor risk)"
- **我的推荐**: (c) — 默认可用, 但 audit visible
- **Owner 待决**: 接受 (c) 即写入 P-4

### DECISION-POINT D-4: cache_control 是否做 fusion delta 的"跨账号 cache locality 复制意图"？

- **背景**: `project_pasr_real_diff_matrix.md` 揭示 LiteLLM 已有 cache locality routing；HUAKAI 真 delta 是 score blend + miss demote + 跨账号复制意图
- **选项**:
  - (a) P-1 schema 加 `CacheControl.ReplicationIntent` 字段，admin UI 控制；P-2 ClientAdapter 在路由层用
  - (b) P-1 仅 marker 保留，跨账号复制留 P-8 roadmap
- **我的推荐**: (a) — 因为 HUAKAI 的 PASR 已有 segment-table-with-bitmap 设计, 复制意图加成本低
- **Owner 待决**: 接受 (a) 即写入 P-1 schema

### DECISION-POINT D-5: 真账号 smoke 范围

- **背景**: per `project_real_vendor_account_scope.md` 限定 4 vendor (anthropic / openai / gemini / openai_codex)；其他 mock
- **选项**:
  - (a) 严格 4 vendor (推荐, 与 Owner directive 对齐)
  - (b) 加 1 vendor (e.g. xAI Grok), Owner 自决
- **我的推荐**: (a)
- **Owner 待决**: 接受 (a) 即写入 P-6

### DECISION-POINT D-6: P-7 Spend dashboard 数字来源标记

- **背景**: smoke baseline 数字 ≠ 真客户 PMF 数字
- **选项**:
  - (a) 三档来源标记 (smoke-baseline / mock-projection / real-customer)
  - (b) 仅显示一个数字, 不区分来源
- **我的推荐**: (a) — 透明诚实
- **Owner 待决**: 接受 (a) 即写入 P-7

### DECISION-POINT D-7: 中段 fallback 范围

- **背景**: HUAKAI 真差异化 algorithm — mid-stream fallback continuation prompt synthesis
- **选项**:
  - (a) P-2 (ClientAdapter) 内已留接口, P-8 实装 algorithm
  - (b) P-2 直接做完整 mid-stream fallback (开发量增加 1-2 周)
- **我的推荐**: (a) — 接口先留, 实装放 P-8（与 PASR-A4/A5 配合）
- **Owner 待决**: 接受 (a) 即写入 P-2

### DECISION-POINT D-8: capability matrix property test 的 cell 数

- **背景**: 13 capability × 5 client × 5 upstream = 325 cell;  N/A skip 后约 80 cell
- **选项**:
  - (a) 严格 80 cell 全测 (P-5 工作量 2 周)
  - (b) 选 50 高频 cell (工作量 1 周)
- **我的推荐**: (a) — capability 真测 cell 是 HUAKAI 跑赢三家上游的关键证据
- **Owner 待决**: 接受 (a) 即写入 P-5

---

## 9. 风险与盲点

### 9.1 Schema 设计偏向风险

- **风险**: Anthropic-rich 主干可能让 OpenAI 客户路径过于"被翻译"——OpenAI 客户进 IR 后再出 OpenAI 形态可能漂
- **mitigations**: P-1 末尾加 dual-form 翻译测验（OpenAI in → IR → OpenAI out 形态等价）；P-2 OpenAI ClientAdapter 必须 byte-level fixture 测试

### 9.2 工作量估算偏乐观

- §3.5 of synthesis 估 10-15 周，本 plan 落 12-15 周
- **风险**: P-1 capability schema 设计常拉锯（13 capability + cross-vendor 损耗矩阵，可能需要多次反复）
- **mitigations**: P-1 入口 Owner 显式批 schema 设计文档 v0.4 后再开始 implementation；schema review 以 capability-by-capability 进行不一次过

### 9.3 真账号 smoke 凭据 / 网络依赖

- **风险**: Owner 本机 4 vendor smoke 涉及真凭据 + 真扣费 + 网络稳定性；无法 CI 化
- **mitigations**: smoke test 设 quick auth check 阶段, Owner 每周校 baseline；mock E2E 是最深的 sandbox 测试 (per `project_no_aws_credentials.md`)

### 9.4 mid-stream fallback 接口先留, 实装放 P-8 的风险

- **风险**: P-2 ClientAdapter 接口若没设计好, P-8 实装时反复
- **mitigations**: P-1 schema 必须含 cross-attempt streaming state 字段 (block index lift, last-sentinel watch)；ClientAdapter 接口含 `OnAttemptSwitch()` hook

### 9.5 capability matrix property test 80 cell 工作量

- **风险**: P-5 2 周可能不够 80 cell 全 case
- **mitigations**: 80 cell 中 ~50 是 lossy degrade (写 1 个 ProtocolLossEntry assertion + skip)，~30 是 lossless equivalence (要写 byte-level 期望); 真实 effort ≈ 30 个深 case + 50 个 shallow case

### 9.6 fusion delta 表的"三家都没做"是否准确

- **风险**: 我说"mid-stream fallback continuation prompt synthesis 三家都没做"基于 source-read 报告，但可能有遗漏
- **mitigations**: 在落地前做一次 grep 确认 LiteLLM `streaming_iterator.py:212-216` (downgrade Exception 为 StopAsyncIteration) + Portkey `streamHandler.ts:343/377` (console.error only) + envoy `processor_impl.go` (per axis-3 envoy §5.4 — 流式中段不可回退) — 已确认；但需要 Codex lane 复核

### 9.7 与 Codex 平行 lane 的预期分歧

- 本 lane 起 8 阶段 12-15 周；Codex lane 可能起 6 阶段或 10 阶段、可能不同优先级
- **mitigations**: synthesis pass 时按 lane 分歧逐项辩论；Owner 看双 lane 独立产物再裁定

### 9.8 删除 `proto.HCSF struct{}` 的 backward compat

- **风险**: 全仓 grep `proto.HCSF` 至少 5+ 处引用
- **mitigations**: P-0 末尾 alias 兼容, sunset 在 P-2 删除 alias

### 9.9 PMF gate 数字假象

- **风险**: 12 个月 $1M annualized 是假想数字, 没 真客户验证就上 dashboard 会误导
- **mitigations**: §6 已说三档来源标记 (smoke / mock / real-customer)；P-7 dashboard 显示来源不混淆

### 9.10 capability matrix 与 admin UI 联动

- **风险**: capability matrix 在 backend 实测, 但 admin UI 需要操作员能看到 per-vendor cell 状态
- **mitigations**: P-7 dashboard 含 capability matrix tile (实测 cell vs 声明 cell 双视图)

---

## 10. Source Citations

### HUAKAI 内部 (CLAUDE.md #12 exempt)

- `docs/plans/2026-05-09-hcsf-canonical-synthesis.md` (3 lane synthesis)
- `docs/research/2026-05-09-axis3-huakai-current-state.md` (Q1-Q7 现状盘点)
- `docs/research/2026-05-09-axis3-protocol-translation-litellm.md` (Q1-Q7 LiteLLM mechanism)
- `docs/research/2026-05-09-axis3-protocol-translation-portkey.md` (Q1-Q6 Portkey mechanism)
- `docs/research/2026-05-09-axis3-protocol-translation-envoy.md` (Q1-Q7 envoy-ai-gateway mechanism)
- `docs/research/2026-05-09-issue-mining-cross-repo.md` (4 repo issue mining)
- `docs/research/2026-05-09-market-research-claude.md` (Q1-Q6 market)
- `docs/02_HUAKAI_FUSION_ARCHITECTURE.md` §5 复杂度轴
- `docs/03_FEATURE_PARITY_MATRIX.md` F-PROTO-001 / F-PROTO-002
- `docs/specs/protocol-translation.md` Phase A-E
- `docs/specs/streaming-forwarder.md` F-GW-002
- `backend/internal/proto/{anthropic_sse.go,openai_sse.go,gemini_sse.go,bedrock_eventstream.go,hcsf.go,proto.go,passthrough.go,field_matrix.go,capability_matrix.go,tool_call_id.go}`
- `backend/internal/gateway/{forwarder.go,protocol_selector.go,stream_scanner.go,event_scanner.go,bedrock_stream_scanner.go,forwarder_types.go}`

### 上游 reference 引用 (per CLAUDE.md #11/#12)

- `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1873-1876` (ModelResponse extends OpenAI ChatCompletion)
- `BerriAI/litellm@b5d3a5fc:litellm/types/utils.py:1527-1533, 1615-1635, 1637-1647, 1668-1671` (Usage cache token folding)
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:21-67` (tool name SHA-8 truncation algorithm)
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:584-617` (tool_use → tool_calls)
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:668-707` (thinking budget bucket — HUAKAI 不学)
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/transformation.py:811-816` (ANTHROPIC_HOSTED_TOOLS bypass)
- `BerriAI/litellm@b5d3a5fc:litellm/llms/anthropic/experimental_pass_through/adapters/streaming_iterator.py:31-44, 78-216, 212-216` (AnthropicStreamWrapper, exception swallow trap)
- `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/exception_mapping_utils.py:236-2536, 343-356, 399-494, 539-640` (exception_type 2.5K LoC; substring before status-code)
- `BerriAI/litellm@b5d3a5fc:litellm/litellm_core_utils/streaming_handler.py:1229-1235` (received_finish_reason swallow trap — HUAKAI 不学)
- `Portkey-AI/gateway@351692fd:src/providers/types.ts:19-41, 47-83, 85-120, 131-143` (ProviderConfig + ProviderAPIConfig + endpoint strings + requestHandlers)
- `Portkey-AI/gateway@351692fd:src/providers/types.ts:266-274` (canonical ErrorResponse)
- `Portkey-AI/gateway@351692fd:src/types/requestBody.ts:248-270, 427-488` (canonical Params + multi-modal content type)
- `Portkey-AI/gateway@351692fd:src/providers/anthropic-base/messages.ts:3-67` (Anthropic-shape canonical)
- `Portkey-AI/gateway@351692fd:src/providers/anthropic/chatComplete.ts:198-237, 597-602, 599, 617-626, 651-728` (image data URI parsing, content_blocks filter, tool_use 折叠, SSE encoding hand-rolled)
- `Portkey-AI/gateway@351692fd:src/handlers/streamHandler.ts:149-202, 300-411, 414-476` (transformFunction stream state)
- `Portkey-AI/gateway@351692fd:src/handlers/handlerUtils.ts:476-834, 663-779` (tryTargetsRecursively, strategy modes)
- `Portkey-AI/gateway@351692fd:src/services/conditionalRouter.ts:15-156` (Mongo-style operators)
- `Portkey-AI/gateway@351692fd:src/handlers/retryHandler.ts:65-220` (retry strategy)
- `envoyproxy/ai-gateway@4d3eae8b:internal/translator/translator.go:41-117` (Translator generic interface, 8 endpoint canonical)
- `envoyproxy/ai-gateway@4d3eae8b:internal/extproc/processor_impl.go:92, 99-101, 297-299, 307-398, 425-434, 551-572` (originalRequestBodyRaw, forceBodyMutation, ProcessingMode_STREAMED, gzip stream)
- `envoyproxy/ai-gateway@4d3eae8b:internal/translator/openai_awsbedrock.go:202-213, 251-254, 285-290, 298-419, 798-957` (vendor嗅探 trap, CachePoint inline marker, tool_use → ToolUseBlock, ReasoningContent vendor extension)
- `envoyproxy/ai-gateway@4d3eae8b:internal/translator/anthropic_openai.go:80-86, 154-184, 172` (anthropicToOpenAIV1ChatCompletionTranslator, non-nil empty body trick)
- `envoyproxy/ai-gateway@4d3eae8b:internal/endpointspec/endpointspec.go:128-145, 339-353, 374-381, 580-588` (per-endpoint canonical, schema dispatch)
- `envoyproxy/ai-gateway@4d3eae8b:api/v1alpha1/shared_types.go:15-44, 138-152` (VersionedAPISchema 三元组, Cost type 7 enum)

### Issue references (per `2026-05-09-issue-mining-cross-repo.md`)

- `BerriAI/litellm#23777` (multi-OAuth ChatGPT)
- `BerriAI/litellm#27468` (OpenAI→Anthropic args lost)
- `BerriAI/litellm#11364` (Anthropic cached_token cost 错算)
- `BerriAI/litellm#19921` (v1.81 perf 回归)
- `BerriAI/litellm#20246` (VLLM streaming reasoning_content lost)
- `BerriAI/litellm#24512 / #24518` (PyPI 投毒 1113 reactions)
- `Wei-Shaw/sub2api#1331 / #1413 / #1143 / #1451 / #1552 / #2245` (协议自动转换提案 + cc 反向 + telemetry / fingerprint / mid-stream)
- `Wei-Shaw/sub2api#641 / #2293` (Gemini cooldown / cache_read 倍率)
- `QuantumNous/new-api#4678` (gateway 注入 cch=xxx 破坏 prefix cache)
- `QuantumNous/new-api#4697` (qwen3 流缺 sentinel)
- `QuantumNous/new-api#4168` (stream 失败仍按 prompt 扣费)
- `QuantumNous/new-api#1730` (渠道级 rate-limit 11👍 open 8m)
- `Portkey-AI/gateway#1579 / #1589` (cache_control.scope 跨平台保留)
- `Portkey-AI/gateway#1047` (Bedrock 200 + stream-error fallback 6👍 open 12m)
- `Portkey-AI/gateway#1583` (Tool calls + Responses API 跨 vendor)
- `Portkey-AI/gateway#1156-1158` (Batch API: cancel/pause/partial)

### Memory entries 引用

- `feedback_pace_not_urgent.md` (D-PACE 项目不急)
- `project_real_vendor_account_scope.md` (4 vendor 真账号限定)
- `project_no_aws_credentials.md` (Owner 没 AWS 凭据)
- `feedback_owner_local_verification.md` (真上游 smoke Owner 本机)
- `feedback_skip_vendor_implementation_test.md` (vendor session 反转用成熟项目逻辑)
- `project_pasr_real_diff_matrix.md` (PASR cache locality + miss demote)
- `feedback_chinese_comments.md` (中文注释)
- `feedback_no_single_track_max_parallel.md` (最大并行)

---

## Tail block

**Source files read** (HUAKAI internal — exempt per CLAUDE.md #12; upstream cites done by previous specifier lanes, this lane's role = synthesizer):
```
docs/plans/2026-05-09-hcsf-canonical-synthesis.md (213 lines)
docs/research/2026-05-09-axis3-huakai-current-state.md (394 lines)
docs/research/2026-05-09-axis3-protocol-translation-litellm.md (252 lines)
docs/research/2026-05-09-axis3-protocol-translation-portkey.md (204 lines)
docs/research/2026-05-09-axis3-protocol-translation-envoy.md (332 lines)
docs/research/2026-05-09-issue-mining-cross-repo.md (458 lines)
docs/research/2026-05-09-market-research-claude.md (462 lines)
```
0 upstream source files re-read in this lane — synthesizer relies on prior specifier lane citations (per CLAUDE.md #11 lane guard, this lane = synthesizer不能重复 specifier role)。

**Lane**: synthesizer (consume 7 specifier+research artifacts; do not re-read upstream source)
**Agent**: Claude Code Opus 4.7 (1M context) — `general-purpose` subagent (id `ab2475426a4fd6dd9`)
**UTC timestamp**: 2026-05-09T17:55Z
**Codex parallel lane (do not read here)**: `docs/plans/2026-05-09-hcsf-v04-implementation-codex.md` — pending; synthesis pass after both lanes complete.
