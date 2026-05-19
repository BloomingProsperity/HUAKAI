# 2026-05-19 Codex Review v5 P1 Cleanup — Claude 计划

| Owner directive | "先修 3 P1 再 push 全部" (codex review v5 `--base main` 反馈) |
| Scope | In: `backend/internal/transport/policy.go`, `backend/internal/provider/registrydefault/default.go`, `backend/internal/gatewayhttp/chat_completions_stream.go` 三处独立 fix。Out: 不动 rust / frontend / proto / pool / billing / audit / 其他 gateway 路径。 |
| Success criteria | (1) Gemini standard API-key (`platform=gemini`) 上行能找到 transport policy 不再 fail-fast "unknown provider"。(2) `openai_responses` protocol family 的 dispatch endpoint 为 `/v1/responses`, 不再发到 chat completions。(3) 跨协议 stream (client protocol ≠ resolved.ProtocolFamily) 在 dispatch 前先 translate, 不再原样转发。三处各自 unit test PASS + `go build ./...` PASS + `go test ./internal/gatewayhttp/... ./internal/transport/... ./internal/provider/registrydefault/... -race -count=1 -timeout 180s` PASS。 |
| Time estimate | 90-120 分钟 codex (3 commits, 每个含 build + test verify)。 |
| Blast radius | gateway dispatch 路径核心改动。错误可能让所有 Gemini / OpenAI Responses / 跨协议 stream 路径回归。Mitigated by per-fix unit test + 不动其他 protocol family。 |
| Failure modes | (a) Gemini const 加错位置导致 transport policy map 误命中其他 provider — mitigated by 加在 const block 末尾 + 单独 map entry。(b) Responses adapter endpoint 写死可能影响 mock / dev 路径 — mitigated by `Endpoint` field 已是空串走默认的 opt-in 设计。(c) Stream translate 引入额外 body 解析可能 race 或大 body OOM — mitigated by 跟 non-streaming dispatch 用同一 HCSF 翻译 helper, 不新写解析。 |
| Decision points | 如果发现 fix 需碰 production schema / billing / auth / 其他 protocol family, 停下 surface Owner。 |
| Per-commit codex review | 每 commit 后 `codex exec review --uncommitted --full-auto`, HIGH 阻断, MEDIUM 视情修。 |

## Phase A — P1#1 Gemini transport policy

**Symptom**: `backend/internal/transport/policy.go` 仅注册 `ProviderGeminiAdvanced=gemini_advanced`, 没 `ProviderGemini=gemini`。当 vault 选出 `platform=gemini` 账号 (标准 Gemini generativelanguage API), `UpstreamDispatcher` 把 `AccountInfo.Platform` 喂给 `TransportFactory.For("gemini")` → `ErrUnknownProvider`, 上游请求未发就 fail。

**Fix**:
1. 加 const `ProviderGemini ProviderCode = "gemini"` (`google generativelanguage standard API`)。
2. `allowedModesByProvider[ProviderGemini]` 加: `TransportModeStandard:true`, `TransportModeDiagnosticsOnly:true` (标准 API key 路径不允许 mimicry; mimicry 留给 `ProviderGeminiAdvanced` 的网页 session 反转)。
3. `isKnownMode` 不变 (mode 集合没变)。

**Test**: 加 `TestValidateModeForProvider_Gemini_StandardOnly` (验证 gemini+standard PASS, gemini+mimicry_gemini_advanced fail-fast)。

**Commit msg**: `transport 加 Provider=gemini 注册修标准 API-key 路径 unknown provider fail`

## Phase B — P1#2 OpenAI Responses 端点

**Symptom**: `backend/internal/provider/registrydefault/default.go:91` `ProtocolOpenAIResponses` 复用 `&openai.PassthroughAdapter{}` (Endpoint 留空 → 默认 `/v1/chat/completions`)。Responses-shape body (`input` 字段) 发到 ChatCompletions endpoint, 上游 reject + 解析层走 Chat-shape parser, 整条 Responses 路径 broken。

**Fix**: `openai.PassthroughAdapter` 已有 `Endpoint string` 字段 (passthrough.go:31)。改为:
```go
r.MustRegister(ProtocolOpenAIResponses, &openai.PassthroughAdapter{
    Endpoint: "https://api.openai.com/v1/responses",
})
```
注释更新: 删除 "Responses API 的专属 adapter 待后续 atomic 单独实现", 改为 "Responses API 仅 endpoint 区分; body shape / SSE 已在 HCSF translate 层处理"。

**Test**: 加 `TestBuild_OpenAIResponses_EndpointIsResponsesAPI` (cast back to `*openai.PassthroughAdapter`, 断言 `.Endpoint == "https://api.openai.com/v1/responses"`)。

**Commit msg**: `provider OpenAI Responses adapter 端点指 /v1/responses 防 body 发到 ChatCompletions`

## Phase C — P1#3 跨协议 stream translate

**Symptom**: `chat_completions_stream.go:88` 直接送 `InboundBody: ex.body` 给 `Dispatcher.Dispatch`, 当 `ex.clientProtocol != ex.resolved.ProtocolFamily` (e.g., 客户端发 OpenAI Chat → resolved family `anthropic_messages`), upstream 收到 OpenAI body, 返回 Anthropic SSE 给 OpenAI 客户端, 双向都没翻译。

**对比 non-streaming**: `chat_completions_handler.go:217` 也是同样 raw `InboundBody`, 但 `chat_completions_dispatch.go` 有 `HCSF dispatch` 分支处理 cross-protocol — 非流式路径有 HCSF translate, 流式没接。

**Fix 策略**:
1. 在 `handleStreamingResponse` 入口加跨协议 detect:
   ```go
   needsHCSF := needsCrossProtocolTranslation(ex.clientProtocol, ex.resolved.ProtocolFamily)
   ```
   `needsCrossProtocolTranslation` 复用 dispatch 层已有 helper (如果有), 否则加一个: `ClientProtocol → ProtocolFamily` 对应表 (e.g., openai_chat → openai_chat/openai_codex 算同协议; openai_chat → anthropic_messages 算跨)。
2. 若 needsHCSF, 改走 `ex.d.Dispatcher.DispatchHCSF(...)` (或类似已有 cross-protocol dispatch entry); 否则保留 `Dispatch` 走 passthrough。
3. 上游 SSE reader 也需要 translate (跟 non-streaming HCSF 路径同 forwarder)。

**实施 detail**: 先读 `chat_completions_dispatch.go:252` 周围 HCSF 分支, 看 non-streaming 怎么挂接, 然后在 stream 路径复用同 helper。如果发现 stream 路径需要全新 translator (因为 SSE 增量不能一次性翻译), 停下 surface Owner — 这超出 bug fix 范畴, 需要单独设计。

**Test**: 加 `TestHandleStreamingResponse_CrossProtocol_HCSFInvoked` (mock dispatcher 验证当 client=openai_chat, family=anthropic_messages 时 HCSF 分支被调用, raw body 没直接到 upstream)。

**Commit msg**: `gateway 流式跨协议 dispatch 接 HCSF translate 防 raw body 直送上游`

## 验证 (每 Phase 结束)

```bash
cd backend
GOCACHE=/tmp/go-cache go build ./...
GOCACHE=/tmp/go-cache go test ./internal/transport/... ./internal/provider/registrydefault/... ./internal/gatewayhttp/... -race -count=1 -timeout 180s
codex exec review --uncommitted --full-auto -c model_reasoning_effort=xhigh --enable fast_mode < /dev/null
```

## 提交策略

- 3 个独立 commit, 一 commit 一模块 (transport / provider / gateway)。
- 不 push, Claude 主线 review 后统一 push 21 commits。

## 风险与不做

- 不读参考反代源码 (clean-room policy CLAUDE.md #11/#12)。
- 不动 production default behavior (除了把已 broken 的路径修对)。
- 不引新依赖。
- 不改 schema / migration / billing 表。

## Source files read (Claude)

- `backend/internal/transport/policy.go`
- `backend/internal/provider/registrydefault/default.go`
- `backend/internal/gatewayhttp/chat_completions_stream.go`
- `backend/internal/gatewayhttp/chat_completions_dispatch.go` (grep 跨协议 helper 用)
- `backend/internal/provider/openai/passthrough.go` (确认 Endpoint 字段可配)
- `backend/internal/gatewayhttp/chat_completions_dispatch_test.go` (理解 cross-protocol test pattern)

Lane: planner
Agent: Claude Opus 4.7 (1M context)
UTC timestamp: 2026-05-19T09:55:00Z
