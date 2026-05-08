# 2026-05-08 Bedrock A5+A6 合并 atomic — gateway 集成 (claude lane plan)

## Owner directive
"继续" + "多 Agent 同步进行"——Owner 选 A 路径，按 plan 推 A5+A6 合并。

## Scope

**In scope**:
- A5: `BuildDefaultProtocolAdapterRegistry` 注册 `"bedrock_invoke" → proto.NewBedrockEventStreamAdapter()`
- A5: `BuildDefaultStreamScannerRegistry` 注册 `"bedrock_invoke" → &BedrockEventStreamScanner{}`
- A5: 移除/更新两处 "bedrock_invoke 故意不在此处注册" 注释（已被 A2-A4 解锁）
- A6: 翻转两处显式 assert "bedrock_invoke 未注册" 的 regression test
- A6: 新增 e2e 测试 — 合成 AWS Binary EventStream 流 → 走 forwarder pipeline →
  验证 canonical event 输出
- 更新原 atomic 提示注释（A2/A3/A4 已合）

**Out of scope**:
- A8（OpenAI request → Bedrock-Anthropic native body 翻译）
- A7（脱机 e2e smoke 走真 HTTP layer）
- 改 SQL migrations（CHECK 约束已含 `bedrock_invoke`，无需动）
- 引新依赖
- 改 `forwarder.go` 主流程（设计已通用，registry 查找即可）

## Success criteria

1. `go test ./internal/gateway/... ./internal/proto/...` 全过
2. 全量 `go test ./...` 全过（无回归）
3. e2e 测试：构造 binary EventStream（chunk message_start + content_block_delta + message_stop）→ `StreamForwarder.Forward` → 收到 3 个 canonical event 顺序正确
4. `go test -race ./internal/gateway/...` 全过

## Time estimate

30-45 分钟 wall clock（小改动 + 已有 A2-A4 基础）。

## Blast radius

- 改动仅 5 个文件；2 个 production registry 各加 1 行；2 个 test 改断言；1 个新测试文件
- 解锁 `bedrock_invoke` family 后，调用方传 `ProtocolFamily="bedrock_invoke"` 不再 fail-loud
- **不会触发其它 18 个 family 的回归**（registry 是 map 查找，独立 entry）
- 解锁后 forwarder 行为变化：`bedrock_invoke` 路径从 `ErrUnknownProtocolFamily` → 走 BedrockEventStreamScanner + BedrockEventStreamAdapter；这是预期变化，回归测试需更新

## Failure modes

1. **Registry shadow / 重复**：MustRegister 两次同 family panic — 两个 registry 都有 dedupe 检查，已防
2. **Adapter <-> Scanner mismatch**：`bedrock_invoke` 必须两个 registry 都注册，否则 forwarder 入口 `f.Scanners.For` / `f.ProtocolAdapters.For` 不匹配 → 一边返回 scanner 一边返回 ErrUnknownProtocolFamily — 加测试覆盖
3. **e2e 测试构造 binary stream 失败**：A2 decoder_test.go 已有 encoder helper；e2e 复用 `bedrock_stream_scanner_test.go` 的 `encodeBedrockFrame` 逻辑（模板复制）
4. **forwarder.handleEventWithAdapter 把 evt.Data 喂 adapter** — 已确认（forwarder.go:198）。BedrockEventStreamAdapter 接受 `[]byte` ✓
5. **terminalSeen 信号**：forwarder 用 `evt.Type == "message_stop"` 判终止（forwarder.go:188）。Bedrock scanner emit `Type` = 内层 Anthropic event type，含 "message_stop" — 工作

## Decision points

| 项 | 决策 | 理由 |
|---|---|---|
| 是否新增 ForwardRequest 字段 | **否** | ProtocolFamily 已能驱动 adapter+scanner |
| 是否需要兼容 nil-adapter pass-through | **否** | adapter 始终非 nil（已注册） |
| 是否拆 A5 / A6 单独 commit | **合并 atomic** | scope 极小，分离反而造成短期 broken tests |
| 是否用 `/v1/messages` 路由触发 bedrock_invoke | **否** | 路由层属 OpenAPI HTTP 边界，与 wire-format 切帧解耦；e2e 直测 forwarder.Forward 即可，路由对接放 future atomic |
| e2e 测试位置 | `backend/internal/gateway/bedrock_e2e_test.go` 新文件 | 与既有 forwarder_test.go 隔离，便于 future 扩 OCAW e2e |

## 设计大纲

### Edit 1: `backend/internal/gateway/protocol_selector.go`

```go
// 移除 line 95-98 注释块（"bedrock_invoke 故意不在此处注册..."）
// 加一行（与现有 18 行 MustRegister 风格一致）：
r.MustRegister("bedrock_invoke", proto.NewBedrockEventStreamAdapter())
```

放在 `grok_chat` 之后、6 家 OpenAI 兼容直 API 之前（按"特殊 wire-format 路径"分组）。

### Edit 2: `backend/internal/gateway/stream_scanner.go`

```go
// 移除 line 121-125 注释块（"bedrock_invoke 故意不在此注册..."）
// 加一行（与 19 行 SSE family list 等价位置）：
r.MustRegister("bedrock_invoke", &BedrockEventStreamScanner{})
```

放在 `grok_chat` 之后（family 顺序与 protocol_selector 对齐）。

### Edit 3: `backend/internal/gateway/protocol_selector_test.go:185-189`

```go
// 旧：assert bedrock_invoke 返回 ErrUnknownProtocolFamily
// 新：assert bedrock_invoke 返回 BedrockEventStreamAdapter
adapter, err := r.For("bedrock_invoke")
if err != nil {
    t.Errorf("bedrock_invoke 应已注册（A5+A6），err=%v", err)
}
if _, ok := adapter.(*proto.BedrockEventStreamAdapter); !ok {
    t.Errorf("bedrock_invoke adapter 类型=%T 期望 *BedrockEventStreamAdapter", adapter)
}
```

### Edit 4: `backend/internal/gateway/stream_scanner_test.go:95-124`

```go
// 旧：assert bedrock_invoke 返回 ErrUnknownStreamScanner
// 新：assert bedrock_invoke 返回 BedrockEventStreamScanner
scanner, err := r.For("bedrock_invoke")
if err != nil {
    t.Errorf("bedrock_invoke 应已注册（A5+A6），err=%v", err)
}
if _, ok := scanner.(*BedrockEventStreamScanner); !ok {
    t.Errorf("bedrock_invoke scanner 类型=%T 期望 *BedrockEventStreamScanner", scanner)
}
```

### Edit 5: 新增 `backend/internal/gateway/bedrock_e2e_test.go`

A6 e2e 烟测：完整链路 binary stream → scanner → adapter → forwarder.Forward → http.ResponseWriter。

```go
func TestBedrockE2E_ForwarderPipeline(t *testing.T) {
    // 1. 构造 3-frame binary stream: message_start + content_block_delta + message_stop
    // 2. 用 default registries (含 A5 注册的 bedrock_invoke)
    // 3. forwarder.Forward(ctx, bytes.NewReader(stream), httptest.ResponseRecorder, ForwardRequest{ProtocolFamily: "bedrock_invoke"})
    // 4. 断言：endClass=StreamEndGraceful, no err, 3 SSE chunks 写入 ResponseRecorder
}
```

## 测试矩阵

1. `TestBedrockE2E_ForwarderPipeline` — happy 3-event 流走完整 pipeline → ResponseRecorder 收到 3 SSE chunks，draft.EndClass == StreamEndGraceful
2. `TestBedrockE2E_ProtocolFamilyMissingScanner_FailLoud` — 删除一边 registry 模拟不一致 → forwarder 返回 error（已被现有测试覆盖，确认未回归）
3. `TestBedrockE2E_BinaryFrameTooLarge` — 超 limits frame → ErrFrameTooLarge 传播为 endClass UnknownTermination
4. `TestBedrockE2E_ExceptionFrame` — Bedrock exception frame → endClass UnknownTermination + endErr ErrBedrockException
5. **回归**：18 个其它 family（SSE 路径）走 ForwardRequest{ProtocolFamily: "openai_chat"} 仍正常 — 现有 forwarder_test.go 已覆盖

## 平行交叉法

- claude lane plan: 本文件
- codex lane plan: codex bg dispatch 失败 2 次（写盘问题）；用 explore agent 替代 cross-lane gap，查全 bedrock_invoke 引用，结果纳入本 plan §"Edit 3+4"

## 引用源

- HUAKAI 内部：A1 / A2 / A3 / A4 commit 已合
- HUAKAI 内部：`forwarder.go` (设计已通用) / `forwarder_types.go`
- HUAKAI 内部：`protocol_selector.go:95-98` + `stream_scanner.go:121-125`（已知 gap markers）
- HUAKAI 内部：`protocol_selector_test.go:185-189` + `stream_scanner_test.go:95-124`（待翻转的 regression assert）
- 严禁读 aws-sdk-go / botocore / aws-encryption-sdk reference 实现源码（CLAUDE.md #11）

Lane: claude
Time: 2026-05-08T<UTC>
