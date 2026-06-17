# DEFERRED（S3）：严格 OpenAI 兼容上游的 tool-call id 多轮 round-trip 前缀未闭合

- **来源**：fix/proto-toolcall-id 切片的对抗审查（2026-06-17）
- **严重度**：S3（真实但窄；pre-existing 架构限制，非本切片回归）
- **状态**：deferred —— 不阻塞本切片 commit（commit 门禁为"无未结 S0/S1"，本切片零 S0/S1）

## 事实链（源码坐实）

`fix/proto-toolcall-id` 修复了响应侧 tool-call id 被丢成空串（S1-1 OpenAI / S2-6 Gemini / S2 anthropic streaming 兄弟）。修复把翻译失败的 id 合成为 `call_<sanitized>`。但**回程（tool_result 回传上游）从不剥离该前缀**：

1. Mistral 等走 OpenAI 适配/marshal：`protocol_selector.go:128`（mistral_chat→openai.Adapter）、`upstream_dispatcher_hcsf.go:282-285`（归一为 openai_chat）、`hcsf_graph_marshal.go:29`。
2. 上游无前缀 id（如 Mistral `abc123def`）→ 合成 `call_abc123def` 发给客户端（`openai_chat_response.go:112`）。
3. 客户端回传**不做 canonical 化**：`openai_chat_request.go:161/174` 直接 `CallID = m.ToolCallID`。
4. 回程 marshal 原样吐：`hcsf_graph_marshal.go:224/265/271`，**不调用** `FromCanonicalCallID`。
5. **`FromCanonicalCallID` 在生产代码零调用**（grep 全仓仅定义 + 测试）。
6. 结果：第二轮上游收到 `call_abc123def`（14 字符带下划线）而非原始 `abc123def`（9 字符）。

## 为何是 S3 而非 S2

- **前缀原样回程是 pre-existing 架构**：marshal 层从不 strip，任何正常 `call_xxx` id 本来就连前缀回程；本修复仅把 malformed 分支从"返回空串"改为"返回 `call_<sanitized>`"，**未新引入也未回归**该行为。
- **worst-case 严格优于修复前**：修复前该分支 `b.CallID==""` → `openai_chat_response.go:107` 硬报错、整个第一轮响应失败；修复后第一轮成功，仅严格上游第二轮可能被拒。对单轮请求与 Anthropic 客户端（id 对 Claude 不透明、前缀无害）是净改进。
- **依赖未验证的上游契约假设**：「Mistral 422 拒 14 字符下划线 tool_call_id」是外部契约推断，本仓未编码任何 Mistral tool_call_id 格式/长度约束，无法在源码内证实。

## 真正闭合的备选（未实施，待 Owner 排期）

1. 在 OpenAI 兼容上游**回程剥离合成前缀**（把 `call_<sanitized>` 还原回上游原始形态）。需要一处能区分"合成 id" vs "真 OpenAI call_ id"的标记。
2. 用 **`OriginalToolCallID`** 还原：该字段目前仅 `anthropic_messages_request.go:185` set、`hcsf_graph_marshal.go:460-461` 用于 Gemini 工具名查找，**无 OpenAI 回程还原路径**——需新增。
3. 若量化确认主流 OpenAI 兼容上游（Mistral/Qwen/GLM）对 tool_result 的 `tool_call_id` 做严格回显校验，再决定优先级。

## 影响面

- 仅影响 **OpenAI/Gemini 兼容上游 + 工具多轮（tool_result 回程）+ 上游严格校验 tool_call_id 回显** 三者同时成立的窄场景。
- 单轮工具调用、Anthropic 上游、宽松上游均不受影响。
