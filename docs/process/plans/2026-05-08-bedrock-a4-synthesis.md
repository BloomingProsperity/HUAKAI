# 2026-05-08 Bedrock A4 — 双 lane 综合（claude + codex）

## 背景

CLAUDE.md #10 平行交叉法：claude lane + codex lane 各独立写 plan，再综合执行。

- claude plan：[2026-05-08-bedrock-a4-claude.md](./2026-05-08-bedrock-a4-claude.md)
- codex plan：`/tmp/parallel-a4-codex/plan.md`（codex bg lane pid 46576，UTC 2026-05-08T01:19）

## 一致点（两 lane 同决策）

1. ✅ A4 = 独立 `BedrockEventStreamAdapter` struct，不直接复用 `AnthropicAdapter`（未来 Llama / Cohere on Bedrock 需分流）
2. ✅ 内部 delegate `AnthropicAdapter`（Bedrock-on-Anthropic chunk inner JSON 与 native Anthropic 同形态）
3. ✅ 跨包：`proto` 不能 import `gateway`；adapter 接受 `[]byte` 形式的 inner JSON
4. ✅ `CanonicalToProviderRequest` / `ProviderResponseToCanonical` 占位返回 `ErrNotImplemented`（A8 才实现）
5. ✅ 复用 `UpstreamState`（与 AnthropicAdapter 同 stream 状态机）
6. ✅ 复用 `ErrUnknownEventType` / `ErrNotImplemented`，不引新 sentinel

## 差异 + synthesis 决策

| 项目 | claude lane | codex lane | 综合决策 | 理由 |
|---|---|---|---|---|
| inner adapter 缓存策略 | `s.inner *AnthropicAdapter` lazy init | **stateless，每次 call 构造 local** | **采纳 codex** | claude 版的 lazy init `s.inner = ...` 是 data race（registry-shared adapter + 多 goroutine 并发）。codex 提出无状态设计，per-call 构造 AnthropicAdapter 无开销（仅 immutable bool 字段）。✅ 已修。|
| `BedrockEventStreamEvent{Type,Data}` proto-local wrapper | 不引入 | 推荐引入用于 protocol-level error 透传 | **延后到 A5+A6** | 当前 A3 scanner 在遇到 exception/error 时 emit `Type="error"` SSEEvent **后**直接 yield ErrBedrockException 终止，不会让 error event 流到 A4。A5+A6 集成时如需保留语义，再引入 wrapper。|
| concurrency smoke test | 未列 | §Testing Matrix #10 列出 | **采纳 codex** ✅ 已加 `TestBedrockAdapter_ConcurrentRegistrySharing` 64 goroutine + `-race` 通过 |
| signature_delta 测试 | `TestBedrockAdapter_SignatureDeltaSkipped` + `_Carried` | 等价 | 等价 |
| FinalizeUpstreamStream 幂等性 | 测 1 次 | codex 建议测 "二次 finalize 幂等" | **延后**：当前 AnthropicAdapter `FinalizeUpstreamStream` 检查 `state.Terminated`，已自然幂等；测试中已隐含。|
| 工具调用 ID 边界（Bedrock 风格） | 复用 Anthropic ID 即可 | 显式 test 标注 "Anthropic-style Claude tool IDs，将来非 Anthropic 模型分流" | **采纳**：测试 `TestBedrockAdapter_ToolUseBlock` 已隐式覆盖；无需新增。|

## HIGH 修复对比

claude 初版 `s.inner = ...` lazy init 模式 = data race。codex 之独立 plan 在不见我代码的情况下命中此点 = 平行交叉的真实价值。

修后 adapter 无 mutable 字段，多 goroutine 共享调用安全。`-race` 测试通过。

## 实现产物

- `backend/internal/proto/bedrock_eventstream.go` — adapter（~80 LoC，stateless，per-call delegate）
- `backend/internal/proto/bedrock_eventstream_test.go` — 11 用例（含 -race 并发）
- 全量 test pass: `go test ./...` ok
- codex pre-commit review: NO HIGH FINDINGS

## Lane 选择

prod 实现 = synthesized（claude impl + codex stateless 修正 + codex 并发测试建议）。

## 引用源

- HUAKAI 内部：`backend/internal/proto/anthropic_sse.go` / `proto.go` / `hcsf.go` / `capability_matrix.go`
- HUAKAI 内部：`backend/internal/gateway/bedrock_stream_scanner.go`（A3 emit shape）
- AWS Bedrock 公开文档（chunk envelope）

严禁读 aws-sdk-go / botocore / aws-encryption-sdk reference 实现（CLAUDE.md #11）。
