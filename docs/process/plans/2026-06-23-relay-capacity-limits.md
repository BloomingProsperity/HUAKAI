# 计划:relay 数据面容量上限做成可配 + 抬默认(消除付费用户 413/砍流)

- 日期:2026-06-23
- 切片:feat/relay-capacity-limits
- 基线:origin/feat/frontend-portal @2d585d8b
- Owner 绿灯:第二遍排查后 AskUserQuestion 选"修(做成可配+抬默认)";并覆盖 gatewayhttp 碰撞写面门(已 claim 协调锁,板上无活锁)

## 背景

第二遍"强制 vs 可选"深挖(workflow w4gvgz4oe)发现 relay 数据面四个容量上限写死且极小,对"卖额度给陌生人"是真摩擦:付费用户发带图(单张 base64 ~1.5MiB)/长上下文请求会被 413,大单 SSE 事件(大 tool-call/Gemini 大块)会被砍流。HUAKAI 的 1MiB 是三镜里最极端离群值。

## 三镜对照(§16)

| 项目 | 入站请求体 | SSE 单事件缓冲 | 上游响应读取 |
|---|---|---|---|
| sub2api @e34ad2b | `max_body_size` 默认 256MiB | `max_line_size` 默认 500MiB | `upstream_response_read_max_bytes` 默认 128MiB |
| new-api @1ac0f58 | `MAX_REQUEST_BODY_MB` 默认 128MB | `STREAM_SCANNER_MAX_BUFFER_MB` 默认 128 | (随响应) |
| CLIProxyAPI @2a050dc | — | 写死 50MiB | — |
| **HUAKAI(现状)** | **写死 1MiB** | **写死 1MiB** | **写死 1MiB** | 

三镜全做成 config/env 可调且默认远高于 1MiB。HUAKAI 最严、且写死。**HUAKAI delta(生态升级)**:做成 env 可配 + 配合已有 per-key 限流(RPM60/并发5,PR#87)兜住放大后的滥用面 + scanner 已有 64MiB 硬上限钳制(`event_scanner.go:16` normalizeScannerCap)防内存爆。

## 范围与设计(本切片做 #1/#2/#3;#5 defer)

高价值的三个上限做成 env 可配、默认抬到中转站量级,**不改任何转发/计费逻辑、不碰 money 状态机**:

- **env(MB 单位,对齐 new-api 友好)**:`HUAKAI_MAX_REQUEST_BODY_MB`(默认 32,管 #1 入站请求体 + #2 全局 privacy 中间件)、`HUAKAI_MAX_SSE_EVENT_MB`(默认 16,管 #3 scanner,经现有 normalize 钳到 ≤64MiB)。
- **#1(gatewayhttp 自由函数)**:在现有 `chat_completions_validate.go` 内加包级配置 `maxRequestBodyBytes`(默认 32MiB)+ `ConfigureBodyLimits(reqBody int64)`,启动 wiring 阶段一次性设定、serve 期只读。**用包级 set-once 配置而非穿透 money 热路径多层自由函数签名**,把热路径改动降到最小(只换字面量)。`readChatRequestBody` 用 `maxRequestBodyBytes`。
  - **注**:gatewayhttp 是 god 包(codebudget 文件数已贴基线),故配置/测试**并进现有文件**而非新建文件,避免撞预算。
- **#2/#3(cmd/gateway 主包)**:照 `buildGatewayTimeoutConfig` 现成 env 范式,`privacy.Middleware(...)` 与 `ScannerBufferCap` 改读 env;wiring 处调 `gatewayhttp.ConfigureBodyLimits(...)`。
- env 样例 + docs/deploy 增两个 env 说明。

### #5 上游非流式响应上限——本切片 defer(协调式 follow-up)

落地中发现 #5 有**两份实现**:gatewayhttp legacy(`maxRawBufferedUpstreamBodyBytes`)+ `internal/gateway` HCSF canonical 路径(`upstream_dispatcher_hcsf.go:50 maxBufferedUpstreamResponseBytes`),两者刻意"同值"。只改一份会留不一致半成品,而 HCSF 在 `internal/gateway`=proxies 碰撞热区(Owner 此次只点头 gatewayhttp)。#5 又是四个里最低价值(有 `stream=true` 绕开)。故**本切片 defer #5,两份都保持 1MiB**,留作单独跨两包协调的 follow-up,避免半成品 + 越界碰撞包。

## 默认行为变化(default-flip:容量默认抬高)

旧 1MiB → 新默认 32/16/32MiB。这是放宽(更宽容),不是收紧——只会让以前被拒/被砍的合法大请求通过,不影响既有正常请求。属"默认行为变化"按规矩 Owner-gated,已获绿灯。运维可经三个 env 调回或调更高(scanner 上限 64MiB)。

## blast radius / 风险

- 改动限容量阈值常量→env;不碰转发/计费/路由/auth 逻辑。
- 风险:① 放大请求体=放大内存/DoS 面——已有 per-key 限流(RPM60/并发5)+ scanner 64MiB 硬钳兜底;② 包级 set-once 配置——文档化"启动设定、serve 只读、非并发写"不变量,测试用 t.Cleanup 还原默认且不并行。
- clean-room:容量阈值是公共做法,设计 HUAKAI 自有,不照搬三镜标识符。

## 测试(判别式)

- `ConfigureBodyLimits`:设定后 maxRequestBodyBytes/maxUpstreamResponseBytes 变更;<=0 保留默认。
- `readChatRequestBody`:`ConfigureBodyLimits(100,…)` 后发 200 字节 body → 应拒(ok=false);变异——若仍硬写 1<<20,200 字节会通过(ok=true)→ 测试 RED。自证式对照:同函数 50 字节 body 应通过。
- cmd/gateway env helper:MB 解析正确、空/非法回退默认。
- `privacyBodyLimitForRequest`:relay 路径→relayMax、非 relay(login/register/healthz)→nonRelayMax;变异——恒返回 relayMax 则非 relay 用例 RED。
- `privacy.MiddlewareFunc`:同中间件下大上限路径放过、小上限路径 413(自证式 80 字节跨两上限)。

## 审查结果与处理(workflow wtnrac9u7,5 agent 对抗)

零 S0/S1(blockers 空)。两条 S2 已在本切片修掉(非 defer):

- **S2-1(我引入的回归性放大)**:privacy.Middleware 在 auth 之前全局缓冲 body,我把它从 8MiB 抬到
  32MiB,对未认证的 login/register 同样生效→pre-auth 内存放大面 4×。**修法**:privacy 新增 additive
  `MiddlewareFunc(func(*http.Request) int)`(`Middleware(int)` 委派它,旧调用方/测试不变),cmd/gateway
  按 `isAIRelayPath` 区分——relay 路径放宽到 maxRequestBody,非 relay 维持切片前的 8MiB
  (`nonRelayPrivacyBodyLimitBytes`)。精确中和我引入的放大,非 relay 路径零新破坏(仍 8MiB,不收紧)。
- **S2-2(隐性死开关)**:HUAKAI_MAX_REQUEST_BODY_MB 实际只管 gatewayhttp 主链 + 全局 privacy(relay),
  但 images/embeddings/audio 等兄弟端点各持硬编码逐端点上限(2~25MiB),运维设 env 调不到它们=配了不生效。
  **修法**:收窄两份 .env 注释 + 本 plan 显式声明作用范围,兄弟端点逐端点可配化列为单独 follow-up
  (对齐 #5 defer 写法,避免误导运维)。兄弟包不在本次 Owner 绿灯的 gatewayhttp 写面,且各自独立包,
  真正接 env 另起切片。
