# 2026-05-19 Codex Review v5 P1 Cleanup — Codex 计划

| Owner directive | "修 codex review v5 反馈的 3 个 P1 (pre-existing branch debt, 不是新引入)" |
| Scope | In: `backend/internal/transport/policy.go`, `backend/internal/provider/registrydefault/default.go`, `backend/internal/gatewayhttp/chat_completions_stream.go` 及对应测试。另按 parallel-draft 规则写本计划 artifact。Out: 不读参考反代源码；不动 Rust / frontend / proto / billing / pool / admin / schema / migration；不引新依赖；不 push。 |
| Success criteria | P1#1 `platform=gemini` 可通过 transport policy 的 standard / diagnostics 校验，mimicry 仍拒绝。P1#2 `openai_responses` 默认 adapter endpoint 指向 `https://api.openai.com/v1/responses`。P1#3 stream 跨协议请求在 dispatch 前转成上游协议 body，响应仍由现有 `StreamForwarder` 做上游 SSE → HCSF → client SSE 翻译；同协议 stream 保留 raw passthrough。每个 P1 独立 commit，按用户指定 build/test/review 命令验证。 |
| Time estimate | 90-120 分钟，主要风险在 gateway stream 测试夹具和 race 测试耗时。 |
| Blast radius | 三处均在 gateway 出站链路：错误可能影响 Gemini standard API-key 路径、OpenAI Responses endpoint、跨协议 stream body。通过单模块 commit、局部测试和 `go build ./...` 限制风险。 |
| Failure modes | (1) Gemini provider 加入矩阵后误放 mimicry：测试显式断言 `gemini + mimicry_gemini_advanced` 拒绝。(2) Responses endpoint test 只查注册不查类型：测试会取出 adapter 并断言 concrete type 与 Endpoint。(3) 误用 non-streaming `DispatchHCSF` 缓冲 SSE：源码确认 `DispatchHCSF` 读取完整响应，因此 stream fix 不调用它。(4) HCSF marshal 默认 `stream:false`：stream 路径会在本文件内用 JSON parser 将上游 body 的 `stream` 设为 true。 |
| Decision points | 若跨协议 stream 需要新增 SSE translator 或改 `proto/` / `gateway/forwarder` / provider adapter，则停止并 surface Owner；当前源码显示 `StreamForwarder` 已有上游 SSE 到客户端 SSE 的 translator，所以无需停。若 codex review 报 HIGH，先修复后再 commit。 |
| Pre-execution checklist | 1. 已读 Claude 计划。2. 已确认 `openai.PassthroughAdapter.Endpoint` 是 public 字段。3. 已确认 `DispatchHCSF` 是 buffered non-streaming，不用于 SSE。4. 已确认 `StreamForwarder` 已通过 `ProtocolAdapters` + `ClientAdapter` 做流式响应翻译。5. 开始每 commit 前检查 worktree，避免混入无关改动。 |

## Concrete Execution Order

1. Transport commit：加 `ProviderGemini = "gemini"`，矩阵只允许 standard / diagnostics；补 policy 单测；跑 build、transport race test、codex review；提交。
2. Provider commit：把 `ProtocolOpenAIResponses` 注册为带 `/v1/responses` endpoint 的 `openai.PassthroughAdapter`；补 registrydefault 单测；跑 build、provider race test、codex review；提交。
3. Gateway commit：在 stream dispatch 前判断 `ex.clientProtocol != proto.ClientProtocol(ex.resolved.ProtocolFamily)`；跨协议时通过 client adapter 生成 HCSF，再 marshal 为上游协议 body 并强制 `stream:true`，然后仍调用 raw `Dispatcher.Dispatch` + 现有 `StreamForwarder`；补测试确认 raw client body 不直送；跑 build、gateway race test、codex review；提交。
4. 总验证：跑用户给定总命令并输出实际结果。

## Cross-Discuss Notes

- 与 Claude 计划一致：P1#1 和 P1#2 实施方案相同；P1#3 的目标相同，都是避免跨协议 stream raw body 直送上游。
- 与 Claude 计划分歧：Claude 计划提到 `DispatchHCSF` / 类似 HCSF dispatch 入口；Codex 从源码确认 `DispatchHCSF` 是 non-streaming buffered 路径，`DispatchHCSF` 会读取完整 response body，不适合 SSE。因此本轮不调用 `DispatchHCSF`，只复用请求侧 HCSF canonicalization / marshal，并复用已有 `StreamForwarder` 的上游 SSE 翻译。

## Source Files Read

- `docs/process/plans/2026-05-19-codex-review-v5-p1-cleanup-claude.md`
- `backend/internal/transport/policy.go`
- `backend/internal/transport/policy_test.go`
- `backend/internal/provider/registrydefault/default.go`
- `backend/internal/provider/registrydefault/default_test.go`
- `backend/internal/provider/openai/passthrough.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`
- `backend/internal/gatewayhttp/chat_completions_handler_clientadapter_test.go`
- `backend/internal/gatewayhttp/chat_completions_billing.go`
- `backend/internal/gateway/hcsf_graph_marshal.go`
- `backend/internal/gateway/upstream_dispatcher.go`
- `backend/internal/gateway/upstream_dispatcher_hcsf.go`
- `backend/internal/gateway/forwarder.go`
- `backend/internal/gateway/protocol_selector.go`
- `backend/internal/proto/proto.go`

Lane: implementer
Agent: Codex GPT-5
UTC timestamp: 2026-05-19T11:21:59Z
