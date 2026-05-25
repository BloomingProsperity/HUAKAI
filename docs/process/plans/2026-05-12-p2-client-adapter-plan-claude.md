# P-2 ClientAdapter — Claude lane plan

- 日期：2026-05-12（UTC）
- 作者：Claude PM-Orchestrator（lane = specifier，独立未读 Codex lane）
- 范围：`backend/internal/proto/*_client*.go`（新）+ `backend/internal/proto/envelope.go` 扩展（必要时）+ fixtures/client/ 新分类
- 前置：P-1 D1-D5 已完成（INV-14..49 共 28 INV，291 TestINV 子case，35+2 fixture 全过）
- 平行 lane：`docs/process/plans/2026-05-12-p2-client-adapter-plan-codex.md`（codex 独立起草中，agent ID `bt8x0i1wk`）

## 1. 目标

P-2 把 client 入口的 4 个 hookpoint 全部落地，让 HCSF v0.4 envelope 不只是内存 IR，而是真实拉通 client → canonical → upstream → canonical → client 的端到端协议。

完成后 gateway 不再走 v0.3 / passthrough 兜底路径，所有 client 请求统一过 RequestToCanonical（INV 守门生效），所有响应通过 CanonicalToClientResponse 统一序列化（与 ClientProtocol 解耦）。

## 2. 当前状态分析

### 2.1 已有（UpstreamAdapter 方向 — P-1 阶段产出）

| 文件 | LoC | 4 个 hookpoint（canonical ↔ upstream） |
|---|---:|---|
| `backend/internal/proto/anthropic_sse.go` | 313 | `CanonicalToProviderRequest` / `ProviderResponseToCanonical` / `ProviderEventToCanonicalEvents` / `FinalizeUpstreamStream` |
| `backend/internal/proto/openai_sse.go` | 625 | 同上 4 个 |
| `backend/internal/proto/gemini_sse.go` | 500 | 同上 4 个 |
| `backend/internal/proto/bedrock_eventstream.go` | 114 | 同上 4 个（Bedrock binary stream） |

### 2.2 缺（ClientAdapter 方向 — P-2 要做）

| 协议 family | RequestToCanonical | CanonicalToClientResponse | CanonicalEventToClientChunk | FinalizeClientStream |
|---|---|---|---|---|
| `anthropic_messages` | ❌ | ❌ | ❌ | ❌ |
| `openai_chat` | ❌ | ❌ | ❌ | ❌ |
| `openai_responses` | ❌ | ❌ | ❌ | ❌ |
| `gemini` | （延后，P-2.1）| | | |
| `bedrock_anthropic` | （延后，P-2.1）| | | |

### 2.3 HCSF schema 现状（P-1 锁定）

- `HCSFEnvelope`：`RequestMeta` / `Messages` / `CapabilityGraph` / `BufferedResponse` / `StreamEvents` / `ProviderProjection` / `StreamPlan` / `Accounting` / `Policy` / `Extensions`
- 14 capability families + 5 edge types + tagged-union + INV-1..49 守门
- ClientAdapter 阶段 schema **不动**（已 schema-locked）

### 2.4 gateway 入口当前 path

需扫一遍 `backend/cmd/gateway` + `backend/internal/router` + `backend/internal/gatewayhttp`，确认目前 client request 是怎么进的（v0.3 path / passthrough fallback / 直通 upstream），P-2 把它们一并清掉。

## 3. P-2 切片（5 day per adapter × 3 = 15 working day，可平行）

### D1：`anthropic_messages` ClientAdapter — RequestToCanonical

**输入**：HTTP POST `/v1/messages` body（Anthropic Messages API v1 spec）  
**输出**：`*HCSFEnvelope`（含 `CapabilityGraph` 全 populated + `RequestMeta` + `Policy` 默认值）

字段映射表（关键）：

| Anthropic API 字段 | HCSF envelope 位置 |
|---|---|
| `model` | `RequestMeta.Model` + `RequestMeta.ClientProtocol="anthropic_messages"` |
| `system` (string 或 [{type:"text",...,cache_control}]) | `Messages[role=system]` + 如果有 cache_control 块则 emit `CapabilityCacheControl` node |
| `messages[].role` ("user"/"assistant") | `Messages[i].Role` |
| `messages[].content[].type="text"` | emit `CapabilityText` node + add to Messages[i].Content |
| `messages[].content[].type="image"` | emit `CapabilityImage` node + DataLocator |
| `messages[].content[].type="tool_use"` | emit `CapabilityToolUse` node（含 tool_call_id/Name/Input/Status="complete"） |
| `messages[].content[].type="tool_result"` | emit `CapabilityToolResult` node + `requires` edge → 对应 tool_use（INV-19）|
| `messages[].content[].cache_control` | emit `CapabilityCacheControl` node + BreakpointRefs 指向所在 block 的 node ID |
| `tools[]` | tool registry（每个 tool emit CapabilityToolUse? 不，工具声明不是 instance，可能存 Extensions 或 ToolRegistry 内存表）|
| `thinking.budget_tokens` | emit `CapabilityThinking` node + Redaction=public 默认 |
| `stream` (bool) | `StreamPlan.Mode = streaming / buffered` |
| `metadata.user_id` | `RequestMeta.Tenant`（待 spec 决定）|
| 顶层 unknown 字段 | `Extensions["vendor:anthropic_messages"]` (INV-12) |

字段不映射但要 emit ProtocolLoss 的：
- `top_k`、`stop_sequences` extreme 值、`metadata.user_id` PII

**测试矩阵**（D1 出产，~25 cases）：
- 5 positive（minimal text request / text+tool_use / text+image / cache_control / thinking）
- 12 negative（缺 role / 缺 model / tool_result 无 tool_use / cache_control 引用 invalid block / image base64 损坏 / tool_use_id 重复）
- 5 fixture-from-real-trace（捕真 Claude Pro 请求 sanitize 后做正向 fixture）
- 3 edge case（system prompt = ""、empty messages、empty content slice）

### D2：`anthropic_messages` ClientAdapter — Response/Event/Finalize（3 hookpoint）

**D2a：CanonicalToClientResponse**（buffered）：
- 输入 `HCSFEnvelope.BufferedResponse != nil`
- 输出 Anthropic Messages API response JSON
- 字段映射反向（同 D1 表）
- 注意：Anthropic content array 的顺序敏感，按 NodeSourceRef.BlockIndex 排序

**D2b：CanonicalEventToClientChunk**（streaming）：
- 输入 `HCSFEnvelope.StreamEvents[i]`
- 输出 Anthropic SSE chunk（`event: <type>\ndata: <json>\n\n`）
- 事件类型映射：`message_start` / `content_block_start` / `content_block_delta` / `content_block_stop` / `message_delta` / `message_stop` / `ping` / `error`
- 注意：tool_use input partial JSON 重组（partial_input 字段累积）

**D2c：FinalizeClientStream**：
- emit terminal event（如果上游缺，发 synthetic message_stop per `StreamPlan.SyntheticTerminalAllowed`）
- 调 PASR slot release + settle
- cache mutate（INV-26 BreakpointRefs 驱动 cache_creation/read 计费）
- log audit + ProtocolLoss 记录

**测试矩阵**（D2 出产，~30 cases）：
- 8 buffered positive（含 tool_use chain / thinking visible / cache hit）
- 8 buffered negative（malformed envelope / missing fields）
- 10 stream positive（含 partial tool_use input、cache_read mid-stream、replay 三态）
- 4 stream negative（terminal missing + synthetic / event order broken）

### D3：`openai_chat` ClientAdapter

类似 D1+D2 但目标协议 OpenAI Chat Completions：
- `messages` 数组（role + content / tool_calls）
- `function_call` deprecated → `tool_calls[]`
- `stream` SSE 形态：`data: {chunk}\n\ndata: [DONE]\n\n`
- 不支持 thinking → emit ProtocolLoss severity=warning code=downgrade_thinking
- cache 无原生支持 → cache_control 节点要么 ProtocolLoss UNSUPPORTED 要么用 `prompt_cache_key` extension（部分模型）

D3 工程量 ≈ D1+D2 = 1 week

### D4：`openai_responses` ClientAdapter

OpenAI Responses API v1（newer）：
- `input` 数组（包含 user / system / tool_call / tool_result）
- `output` 数组（assistant 输出按 type 拆 `output_text` / `output_text_delta` / `tool_call` / `reasoning_summary`）
- 原生 `reasoning_summary` → `CapabilityThinking.Blocks`
- 原生 `response_format.type="json_schema"` → `CapabilityStructuredOutput.Mode=json_schema` + `Schema`
- 原生 `previous_response_id` → `RequestMeta.SessionHash`
- 原生 `store` boolean → `Policy.DataRetention.RequestStore`（INV-30 关联）

D4 工程量 ≈ 1 week

### D5：integration / regression / mock upstream E2E harness

- 跨 adapter 端到端：client(anthropic_messages) → canonical → upstream(openai_chat or 反向) → canonical → client，全 link 跑
- mock upstream 在 `backend/internal/test/mockupstream/`（codex 测试方案 P0-3 建议）
- regression 库新增 ~10 个 issue-derived fixture（sub2api#1552 tool_args_lost / portkey#1579 cache_strip / litellm#27468 tool_args_lost / new-api#4678 cache_metadata 等）
- 实测 anthropic + openai + gemini + codex 真账号 smoke（限 4 vendor per memory）

D5 工程量 ≈ 1 week

## 4. INV 扩展

P-2 **不**新增 INV，仅消费 P-1 锁定的 INV-14..49。

P-2 落地时如发现某 INV 需要细化（如 `Extensions["vendor:..."]` 解析顺序），走 v0.4 → v0.4.1 patch minor bump（不算 P-2 范围；列 follow-up）。

## 5. 风险与权衡

### 5.1 vendor docs 模糊

Anthropic Messages / OpenAI Chat / OpenAI Responses 各家 spec 都有 undocumented quirks：
- Anthropic `cache_control` 嵌在 content block 的最后一个，且只对 ephemeral 缓存有效
- OpenAI `tool_calls[].arguments` partial streaming 时 chunk 形态不固定
- OpenAI Responses `previous_response_id` 链如何 invalidate

**对策**：
- 看 vendor 官方 SDK 源码（Anthropic Python SDK / OpenAI Python SDK 都是 MIT/Apache-2.0，可 vendor 参考）
- 维护 `docs/research/2026-05-12-vendor-quirks-anthropic.md` 等 quirks 表
- Smoke test 真账号 + capture trace + 反 fixture 化

### 5.2 mock upstream test harness 设计

Codex 测试方案 P0-3 说要"建立 mock upstream E2E harness"。设计要点：
- HTTP test server (`httptest`) 模拟每家 vendor endpoint
- Fixture-driven response（fixtures/mock_upstream/anthropic_chat_basic.txt 等）
- SSE chunk 注入 + 延迟 / 断流 / mid-stream fallback 注入
- 与 ClientAdapter D1-D4 共用 fixture set

### 5.3 real-upstream smoke 4 vendor 限定

Per memory `project_real_vendor_account_scope.md`：只 anthropic / openai / gemini / codex 真账号；其他 vendor 全 mock。

**对策**：
- D5 smoke test 走 `t.Skip()` 如果 env var `HUAKAI_REAL_VENDOR_SMOKE=1` 没设
- 每个真测带 envelope label `evidence_label="real_smoke"`，便于审计
- 真账号凭据从 `~/.huakai-smoke-creds.json` 读，**不**入 git

### 5.4 v0.3 兼容路径下沉

现有 anthropic_sse.go / openai_sse.go 含 v0.3 旧 ProtocolLossEntry 字段（Feature/Direction/Verdict/Note）。P-2 ClientAdapter 新代码默认填 v0.4（Severity/Reason/Code/Capability/NodeID），不写 v0.3 字段。等 P-3 阶段把现有 UpstreamAdapter 也下沉。

### 5.5 性能 / hot path

Hot path 仍走 `ValidateEnvelopeVersionGuard`（只检 INV-4）。`ValidateEnvelope` 完整守门只在 debug build / fixture test / P-2 启动检查。per-request 走 zero-cost。

## 6. 工作量估计

| 切片 | LoC delta | 测试 LoC | engineer-day |
|---|---:|---:|---:|
| D1 anthropic.RequestToCanonical | +500 | +600 | 5 |
| D2 anthropic 三 hookpoint | +800 | +800 | 5 |
| D3 openai_chat 四 hookpoint | +1200 | +1000 | 5 |
| D4 openai_responses 四 hookpoint | +1300 | +1100 | 5 |
| D5 mock harness + integration + smoke | +600 | +1500 | 5 |
| **总计** | **+4400 prod LoC** | **+5000 test LoC** | **25 engineer-day（约 5 周）** |

Codex per-commit review：每个 Dx 切片 1 commit，~25 个 codex review pass。

## 7. 验证标准（P-2 exit criteria）

1. `go build ./... && go vet ./... && go build -tags debug ./...` 0 error
2. `go test ./backend/internal/proto/...` 全绿
3. `go test -tags debug ./backend/internal/proto/...` 全绿
4. 新增 ~80 个 client-side TestINV / TestClientAdapter 子case 全过
5. mock upstream E2E 跑通 3 个 protocol family × buffered/streaming 6 个组合
6. real-vendor smoke：anthropic + openai + gemini + codex 各跑一遍 minimum text request，end-to-end 通
7. 老的 v0.3 fallback / passthrough fallback path 全部下沉（grep 找不到 `// TODO: passthrough fallback`）
8. Codex review：所有 D1-D5 commit 0 HIGH
9. docs/16 Phase 5 P-2 行勾 ✅

## 8. 与 Codex lane 的 cross-discuss 流程

1. 本 plan 与 codex plan 并行起草（codex agent ID `bt8x0i1wk`）
2. 双 plan 写完后做 synthesis：列 agree / conflict / gaps 三表
3. 共识点估计：4 hookpoint 框架、5 切片节奏、mock harness 必做
4. 分歧点可能：v0.3 兼容下沉时机、smoke test gate（env var vs 强制）、Extensions passthrough 字段命名约定
5. Owner 通过 synthesis 后开 D1

## 9. 需要 Owner / PM 决策点

1. **gateway 入口现状**：当前 `backend/cmd/gateway` 走 v0.3 还是 passthrough？是否允许 P-2 一上来就切 v0.4（破现有 client）？还是双轨过渡（v0.3 + v0.4 共存 N 周）？
2. **Extensions passthrough 命名**：`Extensions["vendor:anthropic_messages.metadata"]` 还是 `Extensions["vendor:anthropic_messages"]["metadata"]`？锁住 INV-12 表达。
3. **Real-vendor smoke 走 mock-only CI 还是允许真账号 nightly**？后者要 GitHub Actions secret 配置（codex 测试方案 P1 提到的 CI 决策）。
4. **gateway HTTP /v1/messages 接收时，是否要支持 multipart 或 form-encoded body**？规范 only JSON。
5. **OpenAI Responses `previous_response_id` 链**是否在 P-2 内实现完整 conversation tracking，还是 P-3 follow-up？

---

Claude lane plan 起草时间：2026-05-12T07:XX:00Z  
Claude PM-Orchestrator session: 0bd24191-601f-47be-adae-91fc05c2771f
