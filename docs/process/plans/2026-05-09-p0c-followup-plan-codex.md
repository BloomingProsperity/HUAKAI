# HCSF v0.4 P-0c Follow-up Plan — Codex Lane

**日期**: 2026-05-09
**Lane**: codex (xhigh + fast_mode)
**对应 Claude lane**: docs/process/plans/2026-05-09-p0c-followup-plan-claude.md（写作时未见）
**触发**: P-0b sonnet review 4 MED 收尾
**Clean-room posture**: 只读 HUAKAI 内部代码 / spec / agent 规则；未读取 `~/refs/` 上游源码。

## TL;DR

P-0c 应作为一个独立、短平快的修复 sprint，建议在 P-1 capability graph IR 之前执行。M1-M3 是 validator/test 完整性问题，改动集中在 `backend/internal/proto`，风险低到中；M4 是真实架构缺口：`proto.HCSF` alias 的 sunset 注释声称 `ValidateEnvelope` 会制造迁移压力，但当前生产入口没有调用，OpenAI/Gemini non-streaming adapter 还会返回零值 `&HCSF{}` 成功结果 [backend/internal/proto/proto.go:13-19](../../../backend/internal/proto/proto.go#L13) [backend/internal/proto/openai_sse.go:148-155](../../../backend/internal/proto/openai_sse.go#L148) [backend/internal/proto/gemini_sse.go:103-110](../../../backend/internal/proto/gemini_sse.go#L103)。

我的推荐是：P-0c 立即修 M1-M3；M4 采用 **(b) production lightweight Version guard 作为主方案**，再把 **(a) debug full validation** 作为开发/测试辅助，而不是把完整 `ValidateEnvelope` 塞进当前 `forwarder.Forward` 热路径。原因是当前 `Forward` 处理的是 `SSEEvent -> []any CanonicalEvent -> client chunks`，并没有 HCSF envelope 对象可验证 [backend/internal/gateway/forwarder.go:185-242](../../../backend/internal/gateway/forwarder.go#L185) [backend/internal/gateway/forwarder.go:293-298](../../../backend/internal/gateway/forwarder.go#L293)。

## 1. 4 MED 修复 phase 切分

### Phase P-0c-A: validator strictness (M1 + M2)

Scope: `backend/internal/proto/envelope_validate.go` + focused tests in `backend/internal/proto/envelope_test.go`。

M1 fix:

- 当前证据：`CapabilityProjection.Capability` 和 `Verdict` 注释为必填，`Verdict` 还限定为 `preserved/lossy/unsupported/native_required` [backend/internal/proto/projection.go:20-35](../../../backend/internal/proto/projection.go#L20)。但 `validateProviderProjection` 当前只检查非 preserved 是否有 loss、silent drop、native_required 是否有 `NativePath`，没有检查空 capability、非法 capability、空 verdict、非法 verdict [backend/internal/proto/envelope_validate.go:303-328](../../../backend/internal/proto/envelope_validate.go#L303)。
- 建议实现：增加 `Capability != ""`、`Capability in AllCapabilityKinds`、`Verdict != ""`、`Verdict in ProjectionVerdict enum`。`AllCapabilityKinds` 已集中列出合法 kind [backend/internal/proto/capability_graph.go:31-48](../../../backend/internal/proto/capability_graph.go#L31)，ProjectionVerdict enum 已集中定义 [backend/internal/proto/projection.go:3-18](../../../backend/internal/proto/projection.go#L3)。
- 测试新增：空 capability、非法 capability、空 verdict、非法 verdict、preserved 无 loss 通过、lossy/unsupported 有非 silent loss 通过、native_required 缺 `NativePath` 仍报 INV-7。
- 工作量：1-2 小时。
- 风险：现有 fixture 若存在隐式零值 projection，会开始失败。`NewEmptyEnvelope` 默认 `CapabilityResults` 是空数组，不受影响 [backend/internal/proto/envelope.go:69-71](../../../backend/internal/proto/envelope.go#L69)。

M2 fix:

- 当前证据：`ValidateEnvelope` 注释把 INV-5 定义为 `RequestMeta` 必填字段 [backend/internal/proto/envelope_validate.go:20-33](../../../backend/internal/proto/envelope_validate.go#L20)，`validateRequestMeta` 也只用 INV-5 报 `RequestMeta.*` 缺失 [backend/internal/proto/envelope_validate.go:77-92](../../../backend/internal/proto/envelope_validate.go#L77)。但 `validateStreamPlan` 对 `StreamPlan.Mode` 缺失/非法也报 INV-5 [backend/internal/proto/envelope_validate.go:330-342](../../../backend/internal/proto/envelope_validate.go#L330)，与 `StreamPlan.Mode` 自身必填 enum 定义不一致 [backend/internal/proto/stream_plan.go:44-65](../../../backend/internal/proto/stream_plan.go#L44)。
- 建议实现：新增 `INV-13`，语义为 `StreamPlan required fields / enum validity`。不要复用 INV-6，因为 INV-6 当前是 `BufferedResponse + StreamEvents` 互斥 [backend/internal/proto/envelope_validate.go:97-105](../../../backend/internal/proto/envelope_validate.go#L97)。同步更新 `ValidateEnvelope` 注释从 `INV-1..12` 到 `INV-1..13`。
- 测试新增：`StreamPlan.Mode=""` 报 INV-13；`StreamPlan.Mode="bogus"` 报 INV-13；合法 `buffered/streaming/replay` 仍通过 mode 校验 [backend/internal/proto/stream_plan.go:10-14](../../../backend/internal/proto/stream_plan.go#L10)。
- 工作量：0.5-1 小时。
- 风险：新增 invariant ID 可能需要 Owner 认可；如果 Owner 不想新增编号，备选是扩 INV-6 message，但我不推荐，因为 INV-6 已有清晰含义。

### Phase P-0c-B: INV-1 deep round-trip coverage (M3)

Scope: `backend/internal/proto/envelope_test.go`，必要时增加 test-only helper。

- 当前证据：`TestINV1_RoundTripDeepEqual` 只构造一个 text node，marshal/unmarshal 后 deep-equal [backend/internal/proto/envelope_test.go:212-231](../../../backend/internal/proto/envelope_test.go#L212)。同时现有 fixture walker 只证明 fixture 文件 round-trip 稳定，并不保证 unit test 覆盖全部 capability / edge / projection 组合 [backend/internal/proto/fixtures_test.go:90-119](../../../backend/internal/proto/fixtures_test.go#L90)。
- 建议实现：新增一个复杂 envelope round-trip test，而不是替换原最小 test。测试对象应覆盖：
  - 14 capability families / 15 concrete node kinds。代码注释说明是 14 families，但 `AllCapabilityKinds` 实际包含 `tool_use` 与 `tool_result` 两个 concrete kind，所以测试应覆盖列表中的全部 15 个 kind，避免漏掉 tool_result [backend/internal/proto/capability_graph.go:3-10](../../../backend/internal/proto/capability_graph.go#L3) [backend/internal/proto/capability_graph.go:31-48](../../../backend/internal/proto/capability_graph.go#L31)。
  - 5 种 edge type：provides / requires / mutually_exclusive / loses / requires_native [backend/internal/proto/capability_graph.go:125-149](../../../backend/internal/proto/capability_graph.go#L125)。
  - graph-level、node-level、edge-level、projection-level `ProtocolLossEntry`，且每条都非 silent drop；`IsSilentDrop` 当前只看 Reason/Note/Verdict/Code [backend/internal/proto/protocol_loss.go:73-77](../../../backend/internal/proto/protocol_loss.go#L73)。
  - `ProviderProjection.CapabilityResults` 中覆盖 preserved/lossy/unsupported/native_required 四种 verdict。
  - `Extensions` 至少包含 `vendor:` 与 `experimental:` 前缀，沿用 INV-12 规则 [backend/internal/proto/envelope_validate.go:369-381](../../../backend/internal/proto/envelope_validate.go#L369)。
- 测试方式：`json.Marshal -> json.Unmarshal -> reflect.DeepEqual`，然后再跑 `ValidateEnvelope`，避免只证明 JSON 稳定却没证明 schema 合法。
- 工作量：3-5 小时。
- 风险：构造所有 node payload 容易因为 tagged-union 约束遗漏字段；当前 validator 要求 `Kind` 与恰好一个 payload pointer 对应 [backend/internal/proto/capability_graph.go:86-122](../../../backend/internal/proto/capability_graph.go#L86) [backend/internal/proto/envelope_validate.go:110-188](../../../backend/internal/proto/envelope_validate.go#L110)。缓解：写 test-only node factory，每个 concrete kind 单独一行表驱动 case。

### Phase P-0c-C: HCSF alias sunset guard (M4)

Scope: `backend/internal/proto` 为主；只有 Owner 选择 option (c) 时才动 `backend/internal/gateway`。

- 当前证据：`HCSF` 是 `HCSFEnvelope` alias，注释说现有 `&HCSF{}` 调用点会在 `ValidateEnvelope` 时因 Version 非 `"0.4"` 失败，从而制造迁移压力 [backend/internal/proto/proto.go:13-19](../../../backend/internal/proto/proto.go#L13)。但 `ValidateEnvelope` 当前只在 proto 测试/fixture 中直接使用，生产路径没有统一调用；函数本身只是定义了顺序校验 [backend/internal/proto/envelope_validate.go:34-63](../../../backend/internal/proto/envelope_validate.go#L34)。
- 当前破口：OpenAI/Gemini `ProviderResponseToCanonical` 已能解析 raw response 成 `CanonicalResponse` [backend/internal/proto/openai_sse.go:530-570](../../../backend/internal/proto/openai_sse.go#L530) [backend/internal/proto/gemini_sse.go:450-490](../../../backend/internal/proto/gemini_sse.go#L450)，但外层方法最终返回 `&HCSF{}` [backend/internal/proto/openai_sse.go:148-155](../../../backend/internal/proto/openai_sse.go#L148) [backend/internal/proto/gemini_sse.go:103-110](../../../backend/internal/proto/gemini_sse.go#L103)。Anthropic/Bedrock non-streaming 仍是 `ErrNotImplemented`，没有假成功 [backend/internal/proto/anthropic_sse.go:83-89](../../../backend/internal/proto/anthropic_sse.go#L83) [backend/internal/proto/bedrock_eventstream.go:67-75](../../../backend/internal/proto/bedrock_eventstream.go#L67)。
- 建议实现：先做 production lightweight Version guard，要求任何 concrete adapter 只要返回 `(*HCSF, nil)`，就必须至少满足 `env != nil && env.Version == HCSFVersion`；若当前方法无法构造带正确 request metadata 的完整 envelope，则返回显式 error，不再成功返回零值 alias。完整 `ValidateEnvelope` 不宜在当前 response adapter 中强制，因为 `ProviderResponseToCanonical(ctx, raw)` 签名没有 `RequestMeta` 入参，而 `ValidateEnvelope` 会要求 `RequestMeta` 非空 [backend/internal/proto/envelope_validate.go:77-92](../../../backend/internal/proto/envelope_validate.go#L77)。
- 测试新增：对 OpenAI/Gemini non-streaming method 用最小合法 raw response 调用，断言不允许 `nil error + Version==""`；对 helper 测 nil、zero envelope、`NewEmptyEnvelope` version；可另加 `go test -tags debug` 的 full validation test，但不要让普通生产测试依赖 debug tag。
- 工作量：4-7 小时，取决于 Owner 是否允许 OpenAI/Gemini non-streaming 方法从“假成功”改为 fail-loud。
- 风险：这是 medium-risk 行为变化。repo 当前生产 forwarder 走 streaming event adapter，不调用 `ProviderResponseToCanonical`；handler 的真实请求路径进入 `Forwarder.Forward` [backend/internal/gatewayhttp/chat_completions_handler.go:315-324](../../../backend/internal/gatewayhttp/chat_completions_handler.go#L315)，所以短期 blast radius 小。但若外部测试或未来 caller 依赖 non-streaming 假成功，会暴露出来。

## 2. M4 架构决策（4 选项 + 推荐）

| 选项 | 做法 | 优点 | 代价 / 风险 | Codex verdict |
|---|---|---|---|---|
| (a) debug build-tag full `ValidateEnvelope` | 新增 `//go:build debug` helper，在 debug/test 构建中对 envelope 做完整 `ValidateEnvelope`，release 构建 no-op 或不接线。 | 能在开发期抓 M1/M2 类 schema 漏洞；不增加 production hot path 成本。 | 当前 `ProviderResponseToCanonical(ctx, raw)` 没有 request metadata，完整 validate 会误杀合法 response envelope 构造尝试；production alias sunset 仍无压力。 | 作为辅助可做，不应单独作为 M4 修复。 |
| (b) production lightweight Version guard | 在所有 `*HCSF` adapter 边界加极轻检查：nil / `Version != HCSFVersion` 直接 fail-loud；成功返回不得是零值 alias。 | 真正进入 production/test 普通构建；开销仅字符串比较级别；直接修复 `&HCSF{}` 假成功。 | 只能证明版本/非零 alias，不能证明 capability graph / projection / policy 全合法；当前 streaming event path没有 envelope，因此覆盖不到每个 SSE event。 | **推荐作为 P-0c 主方案**。 |
| (c) `forwarder.Forward` 集中 validate（可关闭） | 在 `StreamForwarder` 加 validation mode / toggle，尝试在统一入口验证 canonical object。 | 统一治理点；未来适合 P-1/P-2 完整 HCSF IR 流经 forwarder 后集中接入。 | 现在 `Forward` 只拿 scanner event、adapter event output 和 `ForwardRequest`，没有 HCSF envelope [backend/internal/gateway/forwarder.go:66-95](../../../backend/internal/gateway/forwarder.go#L66)。强行做会变成 no-op 或每事件构造临时 envelope，性能和语义都不干净。 | P-0c 不做；记录为 P-2/P-1 后续验收点。 |
| (d) 推到 P-2 ClientAdapter 落地 | 现在只补注释/roadmap，等 ClientAdapter 完整落地再全量 validate。 | 不扰动当前代码。 | M4 MED 保持存在，alias sunset 迁移压力继续为空；P-1 可能继续在无 guard 的基础上扩 IR。 | 不推荐。 |

Concrete recommendation:

1. P-0c 选择 **(b)**：生产构建加 lightweight Version guard，禁止 `nil error + zero HCSF`。
2. 同 sprint 加 **(a)** 的 debug-only full validation helper/test，但只作为开发质量门，不宣称 production 已完整语义校验。
3. 明确不做 **(c)**，直到 HCSF envelope 真正流经 `Forward`；当前 `Forward` 的正确职责仍是 protocol family lookup、scanner lookup、event adapter、client chunk flush [backend/internal/gateway/forwarder.go:59-66](../../../backend/internal/gateway/forwarder.go#L59) [backend/internal/gateway/forwarder.go:84-94](../../../backend/internal/gateway/forwarder.go#L84)。
4. 不接受 **(d)** 作为 M4 closure，只能作为 Owner 显式延期。

## 3. P-0c 何时执行

推荐：**立即执行，在 P-1 capability graph IR 之前完成。**

理由：

- M1/M2 是 schema validator 本身的必填/编号正确性；P-1 若继续扩 capability graph，会把当前松校验当成稳定契约。
- M3 是 INV-1 对复杂 envelope 的证据；P-1 要依赖 14 families / edges / projection，不应建立在只测一个 text node 的 round-trip 上 [backend/internal/proto/envelope_test.go:212-231](../../../backend/internal/proto/envelope_test.go#L212)。
- M4 是 alias sunset guard，不先定会让 P-1/P-2 继续增加 `*HCSF` 调用面而没有统一失败模式。

不建议与 P-1 并行，除非拆成两人完全不重叠的 lane：一人只做 P-0c validator/tests，另一人只做 P-1 docs/design。代码实现层面应先落 P-0c，否则 P-1 很容易和 validator helper/test factory 冲突。

预计耗时：

- P-0c-A: 2-3 小时。
- P-0c-B: 3-5 小时。
- P-0c-C: 4-7 小时。
- 总计：1 个工作日内可完成；若 Owner 对 M4 选择需要讨论，则拆成 “A+B 先执行，C 等拍板”。

建议验收命令：

```bash
cd backend && go test ./internal/proto ./internal/gateway ./internal/gatewayhttp
cd backend && go test -tags debug ./internal/proto
```

如后续要 commit，按项目规则先 stage，再跑 `codex exec review --uncommitted --full-auto`；该纪律适用于 doc-only 和 code commit [CLAUDE.md:55-60](../../../CLAUDE.md#L55)。

## 4. 三维 delta 分类

依据 CLAUDE.md 的三维 taxonomy：架构升级是 module/data-flow/contract surface，算法升级是 scoring/selection/failure/retry 机制，生态升级是 ops/observability/release/audit surface [CLAUDE.md:89-96](../../../CLAUDE.md#L89)。

| MED | 分类 | 原因 |
|---|---|---|
| M1 validateProviderProjection 必填/enum | 架构升级 | 强化 HCSF projection contract surface；不涉及 scoring/selection algorithm。 |
| M2 StreamPlan INV 编号 | 架构升级 | 修正 invariant taxonomy 与错误归因，让 `RequestMeta` 与 `StreamPlan` contract 分离。 |
| M3 INV-1 deep round-trip test | 生态升级 + 架构升级 | 主要是 release/test gate 质量提升；同时证明 canonical schema 的复杂组合可稳定 round-trip。 |
| M4 ValidateEnvelope 无调用 / alias sunset guard | 架构升级 + 生态升级 | 主体是 adapter boundary/data-flow guard；debug tag / review gate 属于工程生态质量面。 |

没有一项属于算法升级；这些修复不改变路由评分、fallback 策略、failure demotion 或 retry policy。

## 5. Owner 决策点

1. **是否批准 P-0c 立即执行且阻塞 P-1 code work**：Codex 推荐立即执行。
2. **M2 invariant 编号**：Codex 推荐新增 `INV-13` 给 `StreamPlan` required/enum validity；备选是复用 INV-6 或只改 message。
3. **M4 主方案**：Codex 推荐 option (b) production lightweight Version guard + option (a) debug full validation helper；不推荐 P-0c 做 option (c)，不推荐延期 option (d)。
4. **OpenAI/Gemini non-streaming fake success 是否允许改成 fail-loud**：Codex 推荐允许。因为当前方法返回零值 `&HCSF{}`，完整 envelope 又缺 `RequestMeta` 输入，继续成功返回会保留架构债 [backend/internal/proto/openai_sse.go:148-155](../../../backend/internal/proto/openai_sse.go#L148) [backend/internal/proto/gemini_sse.go:103-110](../../../backend/internal/proto/gemini_sse.go#L103)。
5. **debug tag 是否进入常规 CI**：Codex 推荐普通 CI 跑 `go test ./...`，P-0c 或 release gate 额外跑 `go test -tags debug ./internal/proto`；是否长期纳入 CI 由 Owner/Claude 综合决定。

## 风险与盲点

- `CapabilityKind` 口径有“14 families / 15 concrete node kinds”的细节。测试应覆盖 `AllCapabilityKinds` 全列表，而文档说明仍按 14 families 叙述，避免 tool_result 被漏测 [backend/internal/proto/capability_graph.go:3-10](../../../backend/internal/proto/capability_graph.go#L3) [backend/internal/proto/capability_graph.go:31-48](../../../backend/internal/proto/capability_graph.go#L31)。
- Full `ValidateEnvelope` 不能无脑用于 `ProviderResponseToCanonical(ctx, raw)`，因为签名缺少 request metadata，而 validator 对 `RequestMeta` 是强必填 [backend/internal/proto/envelope_validate.go:77-92](../../../backend/internal/proto/envelope_validate.go#L77)。这不是 validator 错，是 adapter 边界信息不足。
- 当前 `forwarder.Forward` 是真实生产 streaming path，但它不携带 HCSF envelope；在这里做完整 envelope validation 需要等 P-1/P-2 让 IR 真正流经此层 [backend/internal/gateway/forwarder.go:66-95](../../../backend/internal/gateway/forwarder.go#L66)。
- M1 strictness 可能暴露 fixture 或旧测试里未填 projection 的隐性债务。该风险应通过 test fail-fast 接受，不应放宽 validator。
- 本 plan 没有读取 Claude lane；双 lane synthesis 时需要 Owner/Claude 对照冲突点，尤其是 M2 INV 编号和 M4 fake-success 行为变化。

## Source citations

- HCSF envelope/version/new-empty defaults: [backend/internal/proto/envelope.go:5-93](../../../backend/internal/proto/envelope.go#L5)
- HCSF alias sunset note and adapter interfaces: [backend/internal/proto/proto.go:13-35](../../../backend/internal/proto/proto.go#L13)
- ValidateEnvelope order and invariant comments: [backend/internal/proto/envelope_validate.go:20-63](../../../backend/internal/proto/envelope_validate.go#L20)
- RequestMeta / projection / StreamPlan validators: [backend/internal/proto/envelope_validate.go:77-92](../../../backend/internal/proto/envelope_validate.go#L77), [backend/internal/proto/envelope_validate.go:303-349](../../../backend/internal/proto/envelope_validate.go#L303)
- Projection required fields and verdict enum: [backend/internal/proto/projection.go:3-50](../../../backend/internal/proto/projection.go#L3)
- Capability kinds and node tagged union: [backend/internal/proto/capability_graph.go:3-48](../../../backend/internal/proto/capability_graph.go#L3), [backend/internal/proto/capability_graph.go:86-122](../../../backend/internal/proto/capability_graph.go#L86)
- Edge types: [backend/internal/proto/capability_graph.go:125-173](../../../backend/internal/proto/capability_graph.go#L125)
- ProtocolLoss silent-drop rule: [backend/internal/proto/protocol_loss.go:16-25](../../../backend/internal/proto/protocol_loss.go#L16), [backend/internal/proto/protocol_loss.go:73-77](../../../backend/internal/proto/protocol_loss.go#L73)
- Current INV-1 tests / fixture round-trip: [backend/internal/proto/envelope_test.go:212-231](../../../backend/internal/proto/envelope_test.go#L212), [backend/internal/proto/fixtures_test.go:90-119](../../../backend/internal/proto/fixtures_test.go#L90)
- OpenAI/Gemini zero HCSF returns and response parsers: [backend/internal/proto/openai_sse.go:148-155](../../../backend/internal/proto/openai_sse.go#L148), [backend/internal/proto/openai_sse.go:530-570](../../../backend/internal/proto/openai_sse.go#L530), [backend/internal/proto/gemini_sse.go:103-110](../../../backend/internal/proto/gemini_sse.go#L103), [backend/internal/proto/gemini_sse.go:450-490](../../../backend/internal/proto/gemini_sse.go#L450)
- Forwarder production streaming shape: [backend/internal/gateway/forwarder.go:59-95](../../../backend/internal/gateway/forwarder.go#L59), [backend/internal/gateway/forwarder.go:185-242](../../../backend/internal/gateway/forwarder.go#L185), [backend/internal/gateway/forwarder.go:293-298](../../../backend/internal/gateway/forwarder.go#L293)
- Handler entry into Forwarder: [backend/internal/gatewayhttp/chat_completions_handler.go:315-324](../../../backend/internal/gatewayhttp/chat_completions_handler.go#L315)
- Plan/cross-discuss/source rules and delta taxonomy: [CLAUDE.md:55-75](../../../CLAUDE.md#L55), [CLAUDE.md:89-96](../../../CLAUDE.md#L89)

## Tail block

Source files read:

- `.agents/skills/pm-orchestrator/SKILL.md`
- `CLAUDE.md`
- `docs/specs/protocol-translation.md`
- `backend/go.mod`
- `backend/internal/proto/envelope_validate.go`
- `backend/internal/proto/envelope_test.go`
- `backend/internal/proto/fixtures_test.go`
- `backend/internal/proto/envelope.go`
- `backend/internal/proto/proto.go`
- `backend/internal/proto/projection.go`
- `backend/internal/proto/capability_graph.go`
- `backend/internal/proto/stream_plan.go`
- `backend/internal/proto/protocol_loss.go`
- `backend/internal/proto/openai_sse.go`
- `backend/internal/proto/gemini_sse.go`
- `backend/internal/proto/anthropic_sse.go`
- `backend/internal/proto/bedrock_eventstream.go`
- `backend/internal/gateway/forwarder.go`
- `backend/internal/gateway/gateway.go`
- `backend/internal/gatewayhttp/chat_completions_handler.go`

Lane: codex planning lane
Agent: GPT-5 Codex
UTC timestamp: 2026-05-10T15:04:52Z
Claude lane status: not read
Upstream reference source: not read
