# 客户端 wire-shape 证据表 (HUAKAI Upgrade #6 U6-D-1)

> 用途：HUAKAI 路由层在 identity-aware mode (U6-D-6 feature flag) 启用前，
> 必须有真实证据证明每个 identity 期望的 client wire format。
>
> **本表只记录黑盒行为证据，绝不复制客户端源代码或推断私有协议格式。**
> 即使 Cody (Apache-2.0) 等客户端开源，本研究也只用其公开 docs / OCAW
> 抓包结果，不读其源码（codex synthesis 严守 clean-room 边界）。

## 证据等级

| 等级 | 含义 |
|---|---|
| `verified` | OCAW 实测复现 + 链接 |
| `official_docs` | 官方文档 / changelog 明确声明 |
| `community_reports` | 公开 issue / discussion 描述（弱信号）|
| `inferred` | 行为推断，未实测（不应作 production 决策依据）|
| `open_question` | 未确认，identity-aware 模式下应 fail-closed |

## 严格度分类

| 分类 | 含义 | passthrough policy |
|---|---|---|
| `strict-openai-chat` | 拒绝 / 忽略非 OpenAI Chat stream shape | strict (allowlist 已知字段) |
| `strict-anthropic-messages` | 拒绝 / 忽略非 Anthropic Messages | strict |
| `tolerant-openai-chat` | 要求 OpenAI Chat envelope 但容忍未知字段 | tolerant (extras passthrough) |
| `tolerant-anthropic-messages` | 要求 Anthropic Messages envelope 但容忍未知字段 | tolerant |
| `ambiguous` | OCAW 未跑或证据冲突 | identity-aware 模式 fail-closed |

## 客户端证据表 (template + 当前已知)

### Cursor

- **Identity**: `IdentityCursor`
- **Default API base**: `https://api.openai.com/v1` 或 OpenRouter; 用户可改 base
- **Wire shape expectation**: `strict-openai-chat` (推测) — 待 OCAW
- **Evidence level**: `community_reports` + `inferred`
- **OCAW status**: `pending`（U6-D-1 创建本表时尚未跑 OCAW）
- **Sources**:
  - Cursor 官方设置页明示 "OpenAI API key" 字段 + Custom OpenAI base URL ——
    暗示其 wire 假设 OpenAI Chat shape
  - 公开 issue cursor.directory / forum 报告 "switching to Claude API base
    breaks streaming" → 推测 strict OpenAI shape
- **Open questions**:
  - 是否容忍 `system_fingerprint` 等 OpenAI 后加的 unknown 字段？
  - HUAKAI 把 Anthropic upstream 翻译为 OpenAI Chat output 时,
    `usage.prompt_tokens_details` 等 nested 子结构 Cursor 是否解析？
- **HUAKAI policy 默认 (until OCAW)**: identity-aware 模式不启用 cursor 路径;
  feature flag 默认 off

### Claude Code (anthropic-cli)

- **Identity**: `IdentityClaudeCode`
- **Default API base**: `https://api.anthropic.com/v1`
- **Wire shape expectation**: `strict-anthropic-messages` (高 confidence)
- **Evidence level**: `official_docs` (Anthropic 官方 SDK)
- **OCAW status**: `pending`
- **Sources**:
  - Anthropic 官方 SDK 文档明示 Messages API streaming 形态
    (`event: message_start`, `event: content_block_delta` 等)
  - claude-code npm tarball 公开 (binary) — 不读其源, 但可观察 outbound
    traffic via OCAW
- **Open questions**:
  - `cache_creation_input_tokens` / `cache_read_input_tokens` (Anthropic
    新加字段) 是否 Claude Code 客户端解析使用？(影响 U7 passthrough policy)
  - `event: error` 帧 Claude Code 如何处理？
- **HUAKAI policy 默认**: identity-aware 模式 fail-closed; OCAW 后可启用

### Cody (Sourcegraph)

- **Identity**: `IdentityCody`
- **Default API base**: 多协议 — OpenAI Chat OR Anthropic Messages OR Azure
- **Wire shape expectation**: 看 model family 决定 (cody supports both)
- **Evidence level**: `official_docs` (sourcegraph cody 设置文档)
- **OCAW status**: `pending`
- **Sources**:
  - Cody 官方文档 admin/configuration 描述 "providers" 多种, 包括 anthropic /
    openai / azure-openai / generic
  - **拒绝读 cody 源码** (虽 Apache-2.0 license 允许) — 保 clean-room 边界
- **Open questions**:
  - identity=cody 时 HUAKAI 应根据 model family 二次决定 client shape, 不能
    单凭 identity
  - Cody 使用 LLM API 时是否 inject 自定义 metadata header (`X-Cody-*`)？
    如有, U6-A 已捕获 (X-Cody-* prefix detection)
- **HUAKAI policy 默认**: 路径优先 (path/route 决定 client shape), identity
  仅 informational

### Generic Chat UI (LobeChat / OpenWebUI / Jan / 其它)

- **Identity**: `IdentityChatUI`
- **Default API base**: 用户可配
- **Wire shape expectation**: 大多 OpenAI Chat 兼容 (大多是 OpenAI SDK fork)
- **Evidence level**: `inferred` from common UI patterns
- **OCAW status**: `pending`
- **Sources**:
  - LobeChat / OpenWebUI / Jan 的 GitHub readme 都明示支持 "OpenAI compatible"
- **Open questions**:
  - 各 chat UI 实际实施宽容度差异大；U6-D 不应假设统一
- **HUAKAI policy 默认**: 路径优先；ChatUI identity 仅观测 metric

### Curl / Script (curl-like)

- **Identity**: `IdentityCurlScript`
- **Default API base**: 任意 (用户构造)
- **Wire shape expectation**: 视脚本而定，无统一形态
- **Evidence level**: `verified` (人工 = 任意)
- **HUAKAI policy 默认**: 路径优先；abuse detection 标 metric

### Unknown

- **Identity**: `IdentityUnknown`
- **HUAKAI policy 默认**: 路径优先 (path/route default), 无 identity 影响

## OCAW 实测计划 (U6-D-2 启用 identity-aware 路由前必跑)

按 codex synthesis §3.OCAW plan 步骤:

1. 本地搭一个 mock gateway endpoint 输出 5 类响应变体:
   - OpenAI Chat SSE
   - Anthropic Messages SSE
   - OpenAI Chat + 未知 extra 字段 (system_fingerprint, prompt_filter_results 等)
   - Anthropic Messages + 未知 extra 字段 (cache_creation_input_tokens 等)
   - tool-call 流 / usage chunks / truncation / protocol-level error frames
2. 把每个客户端通过其文档中的 custom base 指向 mock harness（不动真实凭据）
3. 用合成 prompt + 假凭据；不抓真用户 prompt 内容
4. 记录可观测 artifacts:
   - request 路径 / method
   - 白名单 headers（含 secret 红黑名单）
   - 顶层 JSON 字段名 (不记 prompt 值)
   - SSE event names + 顶层 keys
   - 客户端最终行为: accepted / rejected / 渲染错误 / 重试 / 降级
5. 每条记录附 timestamp + 客户端版本 + OS

OCAW 跑完后回填本表 evidence_level + strictness。

## 引用源约束

- 所有 entry 加新数据时**必须**加 timestamp + URL（feedback_no_training_memory 规则）
- **不读** Cursor / Claude Code / Cody 源码（即使 Cody 是 Apache 2.0）
- 公开 docs / changelog / GitHub issue 链接均可作 evidence_level=`official_docs` / `community_reports`

## 状态

- 本表 U6-D-1 创建 2026-05-08 — 所有 entry 当前 `pending` OCAW
- U6-D-2 (ClientShapeDecision contract) 可独立实施，使用本表 default policy
- U6-D-6 (forwarder 接入 feature flag) production 启用前 **必须** OCAW 跑过 ≥3 客户端

Lane: claude (synthesis-driven, codex strictness 边界)
Time: 2026-05-08T<UTC>
