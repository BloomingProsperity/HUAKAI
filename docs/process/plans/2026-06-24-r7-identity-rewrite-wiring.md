# R7 身份改写【接线】切片计划（默认关）

- 日期：2026-06-24
- 分支：`feat/r7-identity-rewrite-wiring`（off `feat/frontend-portal` @5ee9a0ea）
- 作者：Claude（PM-Orchestrator / 实现）
- 状态：实现前计划工件（CLAUDE.md §9 plan-before-execute）

## 1. 范围（本切片做什么 / 不做什么）

**做：**
- 新增子包 `backend/internal/mimicryidentity`：据池账号上下文构造
  `gateway.MimicryPlan`（至少 step5 `metadata.user_id` 改写），并提供把 plan
  应用到请求体的入口（内部调 `gateway.ApplyMimicryPlan`）。
- 把该入口接进两条非流式构造上游请求体的 dispatch 路径（详见 §2）。
- 新增运维开关（env，默认关）。默认关 = 整管线 no-op = 请求体字节完全不变。
- fail-open：external account id 为空时不带 plan、不改写。
- 设备/会话指纹确定性派生（SHA256，免存储）。

**不做（明确排除，Owner-gated 二阶段）：**
- **翻默认（默认开）严禁做**——那是 Owner-gated，不在本切片。本切片
  开关默认 `false`，且无任何代码路径在缺省配置下置 `Enabled=true`。
- 不改 DB schema、不改账号解析链、不把 external account UUID 塞进
  `provider.AccountInfo`（那会牵动 account 解析，留作后续接线切片）。
  本切片接线点把 external account id 作为显式入参；现网默认它来自尚未
  接通的来源 → 取空 → fail-open（不改写）。这保证本切片零行为变更、
  子包逻辑完整且可单测。

## 2. dispatch 接入点（file:line，base 5ee9a0ea）

两条非流式构造上游请求体的路径都经 `gateway.UpstreamDispatcher.Dispatch`：
- 非流式（buffered）：`backend/internal/gatewayhttp/chat_completions_handler.go:736`
  `dispatchRawBuffered` 内 `Dispatch(... InboundBody: ex.upstreamInboundBody(ex.body) ...)`。
- 流式：`backend/internal/gatewayhttp/chat_completions_stream.go:143`
  `executeStreamingAttempt` 内 `Dispatch(... InboundBody: ex.upstreamInboundBody(inboundBody) ...)`。

接线方式：在这两处把传给 `InboundBody` 的"dispatch 专用 body 拷贝"先过
`mimicryidentity` 入口，再交给 `Dispatch`。**绝不动 `ex.body`**（原始客户端
body，缓存键的输入）。

上游请求体真正成型位置：`gateway.UpstreamDispatcher.Dispatch`
(`upstream_dispatcher.go:135`) 内，body 经
`ApplyDispatchBodyControls`→`EnforceCacheControlLimit`→`maybeInjectAnthropicBreakpoints`
后在 `adapter.BuildRequest` (line 172) 处装进 `*http.Request`。本切片在
`gatewayhttp` 调用方一侧、`Dispatch` 之前完成 metadata 改写，落在已有
body-transform 之前，互不耦合。

## 3. CCH / 缓存签名风险分析（本切片头号风险）

两类"缓存签名"：

1. **HUAKAI L2 响应缓存键**（`l2cache.BuildKey`，
   `chat_completions_stream.go:74` `l2CacheKeyForModel`）：输入含
   `Body: ex.body`。该键在 `serveL2CacheIfAvailable`
   (`chat_completions_handler.go:539`) **早于** dispatch（736/143）算定，
   且输入是 `ex.body`（原始客户端 body）。本切片只改写"传给 `Dispatch` 的
   body 拷贝"（`ex.upstreamInboundBody(...)` 的产物），**从不触碰 `ex.body`**。
   → L2 缓存键证明不变。`cch_preserved` 结论：改写在缓存键计算之后发生，
   且作用对象是另一份 body，不参与缓存键。

2. **Anthropic 上游 prompt 缓存（CCH 命中）**：上游对 system/tools/messages
   前缀做哈希命中缓存。`metadata` 不在被缓存的 prompt 前缀内，且本切片
   **只动 `metadata.user_id` 这一字段**（沿用既有 `RewriteMetadataUserID`
   原子，它用 raw 子对象重序列化，非 metadata 字节不变）。
   → 上游 prompt 缓存命中不受影响。

**结论**：改写仅落在 `metadata` 子树，其余字节逐字不变；两类缓存签名均不
错位。这是本切片的硬不变量，用测试 D 逐字节自证。

## 4. REFERENCE PROJECTS IN SCOPE（§16 三镜，纯机制描述，禁拷标识符）

- **sub2api**（`~/refs/sub2api`，默认 tiebreaker，唯一有此能力者）：
  `backend/internal/service/identity_service.go` 的身份重写函数 +
  `metadata_userid.go` 解析/格式化。机制：
  - **fail-open**：池账号 UUID 或缓存设备指纹为空时，原样返回 body 不改写
    （即 `external_account_id==''` 跳过）。
  - **确定性会话派生**：新 session 由 `SHA256(账号ID::原session)` 派生成
    UUID 形态，免存储。
  - **只动 metadata.user_id**：用 surgical set 改单字段，保留其余原始字节
    （注释明确为避免 thinking 块等被重序列化破坏）。
  - 重写后值与原值相等时不改（no-op）。
  - 另有可选 session-id masking（15 分钟固定伪装会话），本切片不纳入。
- **new-api**（`~/refs/new-api`）：**无等价**。`user_id` 命中均为 vendor DTO
  字段（coze `relay/channel/coze/dto.go`、zhipu 等）与 channel-affinity 设置
  读取，**无 Anthropic metadata.user_id 身份伪装重写路径**。source-cited
  no-equivalent。
- **CLIProxyAPI**（`~/refs/CLIProxyAPI`）：**仅读不写**。
  `sdk/cliproxy/auth/selector.go` 从 `metadata.user_id` 抽 session id 做
  账号亲和选择，**不改写/不注入身份**以匹配池账号。对"重写"无等价。

HUAKAI delta（融合升级）：
- 架构升级：身份改写独立成 `mimicryidentity` 子包，复用已建的 6-step
  `ApplyMimicryPlan` 组合器与 `RewriteMetadataUserID` 原子（沉淀的纯函数），
  而非堆进 gateway/service 大包；接线点在 dispatch 调用方一侧，作用于
  dispatch 专用 body 拷贝，与 L2 缓存键输入物理隔离。
- 算法升级：设备/会话指纹用 `SHA256(serverSecret::accountID)` 确定性派生，
  serverSecret 来源固定（注释标注轮换会致指纹突变，需固定来源）。
- 生态升级：默认关运维开关 + fail-open，缺身份不阻断请求。

## 5. 成功标准

- 子包 `mimicryidentity` build/vet 绿；受影响包 `go test -count=1` 全绿。
- codebudget 门绿（新文件落新子包，gatewayhttp 接线仅加少量行）。
- 默认关：开关关/未配置 → 经接线后请求体与原 body 字节等价。
- fail-open：Enabled 但 external account id 空 → 不改写、字节等价。
- 开启且有身份 → `metadata.user_id` 真被改写成与账号匹配的派生值。
- 非 metadata 字节逐字不变（CCH 自证）。

## 6. 变异测试矩阵

| 测试 | 断言 | 变异（注入缺陷）| 预期 |
|------|------|----------------|------|
| A 默认关零变更 | 开关关 → body 字节等价 | 把默认翻成开 | 变红 |
| B fail-open | Enabled 但 ext id 空 → 字节等价 | 空也强行改写 / fail-closed 阻断 | 变红 |
| C 开启+有身份 | metadata.user_id = 期望派生值且 ≠ 原值 | 短路改写步骤 | 变红 |
| D CCH 字节不变 | 非 metadata 序列化逐字节不变 | 让改写顺手动别的字段 | 变红 |

读 env 的测试用 `-count=1`。

## 7. blast radius / 回滚

- 新子包 + 两处 gatewayhttp 接线，默认关 → 零行为变更。
- 回滚 = 删子包 + 撤接线行；无 schema、无依赖新增。

## 8. Owner-gated 排除声明

**翻默认（默认开）= 默认行为翻转 = CLAUDE.md §2 Owner-gated，本切片不做。**
本切片只交付"默认关接线 + fail-open + 子包逻辑 + 变异测试"。
