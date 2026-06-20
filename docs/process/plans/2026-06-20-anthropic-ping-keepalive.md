# 修复:Anthropic 上游 ping 保活帧被判未知事件 → 整流截断并计费(S1)

> 日期:2026-06-20 · 切片:核心逻辑 bug(对抗猎 bug workflow wtmvos069 确认 C1-a,3/3 refuter;并经本人真码三环复核)
> 基线 `feat/frontend-portal` @ ec09c266(含 #81) · 落点 `backend/internal/proto/anthropic/sse.go`(`proto/*` 非碰撞写面)

## 1. 缺陷(本人真码三环复核,非盲信投票)

Anthropic Messages 流式 SSE 在 TTFT 与稀疏 token 间隙周期性发 `event: ping`(`data:{"type":"ping"}`)保活帧。

- **环1**:`proto/anthropic/sse.go` 的 `providerEventSwitch`(:187-249)只有 message_start/content_block_*/message_delta/message_stop 六个 case,**无 ping case** → 落 default(:247-249)返回 `ErrUnknownEventType` + 一条 loss。现存 `sse_test.go:229` `TestAT_PROTO_002_08` 直接用 `"ping"` 当未知事件夹具并断言返 ErrUnknownEventType —— **bug 被测试固化**。
- **环2**:`gateway/forwarder.go` `handleEventWithAdapter`(:319/:347)只特判 `evt.Type=="error"`,**ping 直接喂 `ProviderEventToCanonicalEvents`**(无前置过滤);:356 拿到非 nil err 早返。
- **环3**:`forwarder.go:267-268` 非 ClientDisconnect 的 err → `EndClass=UnknownTermination`(**且不 drain**,drain 只在 :266 ClientDisconnect 分支);:279-284 已交付则注入 terminalErrorFrame(腰斩响应给客户端);`finish` 带非空 acc 走 settle。

## 2. 后果

- 若 ping 在已交付内容之后到达(常见):流被腰斩 + 客户端收到注入的终止 error 帧;`DeliveredTokenCount>0` → settle → **客户被计费一笔截断/失败请求**(`chat_completions_stream.go:291-296`,billing `state.go` Partial→Chargeable)。
- 若 ping 紧跟 message_start 在首字节前到达:请求直接失败。
- 影响面:**任何稍有延迟的 Claude 流式请求**(`protocol_selector.go:93/109` 把 anthropic.Adapter 注册给 anthropic_messages + vertex_anthropic 主 Claude 路径)。pre-launch 无真流量故未暴露。

严重度 **S1**(money-coupled:对失败请求计费 + 核心 Claude 流式路径被破坏)。

## 3. 三家 + 自家对照(#16)

- **HUAKAI 自家 `proto/dify/sse.go:135-136`**:`if chunk.Event == "ping"` 静默跳过、不记 loss(注释"避免长流账面噪音")——**自家既有的标准做法**,anthropic adapter 是不对称遗漏。
- `~/refs/CLIProxyAPI` Claude 翻译器有 `case "ping"` 显式处理 Anthropic ping。
- `~/refs/sub2api` Anthropic 主路径 passthrough,原样转发 ping 字节(不解析,自然容忍)。
- `~/refs/new-api` relay passthrough 同理转发。
- 结论:容忍 ping 是 Anthropic 流式协议的硬要求,四方一致;只有 HUAKAI anthropic 上游 adapter 漏了。

## 4. 修法(对齐自家 dify 模式)

`providerEventSwitch` 增 `case "ping":` 返回 `(nil, nil, nil)` —— 保活帧无载荷,静默吞掉:无 canonical 事件、无 loss(它是正常协议非损失)、无 error。default 分支保持不变(真正未知事件仍返 ErrUnknownEventType,保留协议漂移信号)。

blast radius:单文件 + 测试。不动 forwarder/gatewayhttp(碰撞面)。可逆、纯 additive case。客户端语义:ping 本就不该转给客户端,跳过即正确。

## 5. 测试

- **改** `TestAT_PROTO_002_08`:未知事件夹具从 `"ping"` 换成真正未知类型(如 `"totally_unknown_event"`),继续守"未知事件→ErrUnknownEventType+loss"路径。
- **新增** `TestAT_PROTO_002_PingKeepaliveTolerated`:直接喂 ping 事件,断言 `err==nil`、0 canonical 事件、0 loss(**不**用 runStream,因其会吞 ErrUnknownEventType 掩盖判别性)。
  - **变异判据**:删掉 `case "ping"` → ping 落 default → 返 ErrUnknownEventType+loss → `err==nil` 断言 RED。

## 6. 成功标准 / blast radius / 风险 / 决策点

- 成功:build/vet 绿;新测试 GREEN 且变异 RED;proto/anthropic + gateway + cmd/gateway 干净基线 `-count=1` fail 0;对抗审查零 S0/S1。
- blast radius:`anthropic/sse.go` 单 case + 两个测试;无 schema/money/auth 改动;无新依赖。
- 风险:极低。唯一行为变化 = ping 不再截断流(修正)。default 未知事件语义不变。
- 决策点:无需 Owner gate(纯 bug 修正,非 money/schema/auth/deploy 改动,非默认翻转——修的是把正常协议误判为异常)。安全网=变异测试 + 对抗审查 + 干净基线。
