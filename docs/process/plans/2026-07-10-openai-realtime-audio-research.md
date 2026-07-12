# OpenAI Realtime 语音 — 形态存档 · 三镜对照 · HUAKAI 缺口与延后决策

> 存档性质:本文只记录形态/对照/缺口/代价与决策,不含实现。
> 证据分层:官方契约以 developers.openai.com(2026-07 抓取)为真相源;HUAKAI 断言均给 `file:line`(已亲验);三镜引用标 `<repo>@<sha>:file:line`。
> 铁律遵循:官方文档优先于三镜(见 MEMORY「官方契约优先于三镜」);缺口以本仓真码为准,不凭快照/记忆。

---

## 一、What is Realtime(OpenAI 实时语音形态)

OpenAI Realtime 是**事件驱动的 speech-to-speech 实时会话协议**,与 HUAKAI 现有「一请求一响应」的 HTTP relay 是根本不同的连接模型。五个维度:

### ① 端点与鉴权
- 端点:`wss://api.openai.com/v1/realtime?model=<model>`,**model 走 query 参数**;官方示例 `gpt-realtime-2` / `gpt-realtime-2.1`,HUAKAI fixture 用旧名 `gpt-4o-realtime-preview`。
- 鉴权**双模式**:
  - 服务端:标准 `Authorization: Bearer <OPENAI_API_KEY>` 头。
  - 浏览器/移动端:ephemeral client token,经 WS subprotocol 传入 `["realtime", "openai-insecure-api-key.<ephemeral_token>"]`(可附 `openai-organization.<ORG>` / `openai-project.<PROJ>`),令牌由 `POST /v1/realtime/client_secrets` 铸造。
  - 可选安全头 `OpenAI-Safety-Identifier`。
- 来源:`https://developers.openai.com/api/docs/guides/realtime-websocket`

### ② 会话协议(全 JSON over WS,事件驱动)
- **客户端事件**:`session.update`(配模型/语音/VAD turn_detection)、`input_audio_buffer.append`(推 base64 音频块,服务端不回确认)、`input_audio_buffer.commit`(关 VAD 时手动提交→触发 `committed`)、`input_audio_buffer.clear`、`conversation.item.create`(注入文本/音频消息)、`conversation.item.truncate`、`response.create`(触发一次生成)、`response.cancel`、`output_audio_buffer.clear`。
- **服务端事件**:`session.created`/`session.updated`、`conversation.item.added`/`item.done`、`response.created`、`response.output_audio.delta`(旧名 `response.audio.delta`,增量音频)、`response.output_audio_transcript.delta`、`response.output_text.delta`、`response.done`(**终态含全量 usage**)、`input_audio_buffer.speech_started`/`speech_stopped`/`committed`(VAD)、`error`。
- 来源:`https://developers.openai.com/api/docs/guides/realtime-conversations`

### ③ 音频格式
- 新版 GA(MIME 风格枚举):输入 `audio/pcm` @ 24kHz 单声道;输出 `audio/pcm` 或 `audio/pcmu`(G.711 µ-law)。
- 旧 beta 枚举:`input_audio_format`/`output_audio_format` ∈ {`pcm16`, `g711_ulaw`, `g711_alaw`}。
- 音频 base64 分帧:客户端 `input_audio_buffer.append` 逐块上推,服务端 `*.audio.delta` 逐块下发;可在 session 级或 per-response 级配置。

### ④ 传输 = 多路(「纯 WS」表述不准确)
官方明确三种 transport:
- **WebRTC**:浏览器/移动端直接采集/播放首选,经 `/v1/realtime/calls` 建会话。
- **WebSocket**:服务端已从媒体管线/呼叫系统拿到裸音频时用。
- **SIP**:电话语音 agent。
- 来源:`https://developers.openai.com/api/docs/guides/realtime`

### ⑤ 计费 = 按音频 token(**非按秒**)
- speech-to-speech 对话式会话按 input/output token 计费,**在 Response 创建时计入**。
- 音频↔token 换算:**用户音频 1 token/100ms、助手音频 1 token/50ms**。
- 费率(每 1M token,官方 pricing):`gpt-realtime-2` 音频 **输入 \$32 / 输出 \$64**;mini 变体 **\$10/\$20**;cached input 折价更低;input transcription(Whisper-1 / gpt-4o-transcribe)走**独立 rate card 单独计费**。
- **例外**:translation/transcription 流式会话改用 **duration-based(按时长)** 费率。
- **关键坑**:整段对话每次 Response 全量回灌模型,越靠后的 turn 越贵。
- 来源:`.../realtime-costs`、`.../pricing`

> ⚠️ 口径提醒(直接影响 HUAKAI 建模):HUAKAI fixture 的 `live_usage` 用**毫秒**(`session_duration_ms`/`input_audio_ms`/`output_audio_ms`),这只对得上 translation/transcription 的时长计费,**对不上主流 speech-to-speech 的 token 计费**。若照 fixture 的 ms 口径实现会计,会与官方主计费轴偏离。

---

## 二、三镜对照表

| 维度 | new-api | sub2api `@12d811b` | CLIProxyAPI `@26d45fd` | HUAKAI(本仓·已亲验) |
|---|---|---|---|---|
| **Realtime 语音/音频双向** | ✅ **有(完整)** | ❌ 无 | ❌ 无 | ❌ 无(仅 501 桩) |
| **WS↔WS 双向反代底座** | ✅ 有 | ✅ 有(文本/JSON) | ✅ 有(文本/JSON) | ❌ 无(go.mod 零 WS 依赖) |
| **音频 token 计费** | ✅ 有(双轨) | ❌ 无(仅文本 token) | ❌ 无 | ❌ 无(schema 未接线) |
| **WS 库依赖** | gorilla/websocket | coder/websocket | gorilla/websocket | 无 |

### new-api = 唯一做了真 Realtime 语音的镜(new-api@246d62a,行为级转述)
- 路由 `GET /v1/realtime` 进其总 relay 分发器的「OpenAI Realtime 格式」分支,用 gorilla websocket 升级器(子协议 `["realtime"]`、CheckOrigin 恒 true)把客户端升级为 WS(`controller/relay.go:79-86, 251-256`)。
- 一个 WS 助手层(`relay/websocket.go:15-46`)经适配器的 wss 请求方法、用 gorilla 默认拨号器拨上游 wss(`relay/channel/api_request.go:369-397`)。
- OpenAI realtime 处理器起两个 gopool goroutine 做客户端↔上游**双向逐帧泵**(`relay/channel/openai/relay_realtime.go:19-224`);其请求 URL 构造把 https→wss、http→ws,支持 Azure(`deployment=...&api-version=...`)与 OpenAI 原生两路(`openai/adaptor.go:99-160,196-210`)。
- **计费=双轨流式实时逐事件扣费**:上游 `response.done` 事件优先取真实 usage(input/output/text/audio/cached 明细),无 usage 则本地估算(一段 realtime token 计数逻辑按事件类型分计音频/文本 token、base64 时长换算,`relay_realtime.go:73-188` + `token_counter.go:303-355`);每次 done 即时扣费(余额不足即断流),连接结束落最终账(`service/quota.go:50-244`,音频配额计算逻辑区分文本/音频 in/out × 各倍率)。
- 支持 realtime 的适配器:openai 与 advancedcustom;白名单含 `gpt-4o-realtime-preview*`、`gpt-realtime`、`gpt-realtime-mini`、`gpt-realtime-1.5`(`openai/constant.go:52-60`)。
- 已知实现瑕疵(非缺失,借鉴时留意):收发缓冲通道写后被丢弃(default 分支空转)、日志重复打印(`relay_realtime.go:167-168`);均不影响主链路。

### sub2api = WS 底座成熟,但接的是文本 Codex,零音频
- **语音/音频反代=无**:全仓非测试代码 grep `input_audio|pcm16|voice|whisper|tts|output_audio` = 0;usage 只解析文本 token(`sub2api@12d811b:backend/internal/service/openai_ws_v2/passthrough_relay.go:24-28,759`)。代码里的「Realtime」仅指 admin realtime-traffic 仪表盘 + 注释借 Realtime 事件外形描述 Codex Responses WS。
- **WS↔WS 双向反代=完整已建生产级**(做语音代理会复用的底座):入口 `GET /openai/v1/responses`(Upgrade: websocket)→ `ResponsesWebSocket`(`openai_gateway_handler.go:1222`);客户端 `coderws.Accept`(:1255)↔上游 `coderws.Dial`(`openai_ws_client.go:92`);双向 pump=`passthrough_relay.go:114 Relay()`(Caddy 双隧道:`runClientToUpstream:369` + `runUpstreamToClient:219` + `runIdleWatchdog:241`),Text+Binary 都中继(:141-144,**具备二进制能力但无音频语义**),断开后 drain 兜尾包 usage(:255-258)。多档 ingress:passthrough / ctx_pool·shared·dedicated(连接池)/ http_bridge(WS→上游 HTTP/SSE)。
- 一句话:**做语音代理所需的 WS 管道全有,唯独没有音频语义层。**

### CLIProxyAPI = 同 sub2api,WS 双向但纯文本/JSON
- 三套 WS 机制均**非音频双工**:①下行 `ResponsesWebsocket`(`sdk/api/handlers/openai/openai_responses_websocket.go:213`,仅 `response.create`/`response.append`);②上游 `codex_websockets_executor.go` / `xai_websockets_executor.go` 拨 `wss://chatgpt.com/backend-api/codex/responses`(头 `OpenAI-Beta: responses_websockets=2026-02-06`,`:38`;426 回退 HTTP/SSE,`:272`);③反向 HTTP-over-WS 中继 `internal/wsrelay/*`(浏览器反连当 aistudio provider,半双工请求-响应)。
- **无实时语音**:全仓 grep 无 `/v1/realtime`、`input_audio_buffer`、`BidiGenerateContent`;Gemini 侧只有 HTTP SSE `streamGenerateContent`(`gemini_executor.go:277`);`pcm16→audio/pcm` 只是 translator 里普通内容的 MIME 映射(`interactions_gemini_common.go:1003`),非实时通道。

---

## 三、HUAKAI 现状与缺口(本仓 · 已亲验)

### A. audio REST 现状(已建、非 realtime)
- 三端点全 POST:`/v1/audio/speech`、`/v1/audio/transcriptions`、`/v1/audio/translations`(`cmd/gateway/routes.go:108-110`)。
- 计费三 scheme(`internal/audiopricing/catalog.go:15-17`):speech=`per_char`(按 rune 数)、transcriptions/translations=`per_second`(WAV 头精解 `duration.go:46-85`,非 WAV 按 8000bps 保守上界)或 `token`(settle 用上游真实 usage)。
- **生命周期=单发一次性 reserve→dispatch→settle**,无按秒增量/会话滚动计费;结算脱请求 ctx 防断连(`billing.go:151-156` WithoutCancel + **5s 上界**)。

### B. Realtime 缺口 = 501 桩 + 纯 schema/fixture,零运行时(已亲验)
| 项 | 证据 | 状态 |
|---|---|---|
| 运行时 | `cmd/gateway/routes.go:117` → `handleRealtimeRoadmap`(`routes.go:421-424`)固定返回 501 `realtime_not_available`,文案指向 **Phase 9+ F-RT-001** | 桩 |
| 测试锁定 | `openapi_consistency_test.go:552` 断言该 501 | — |
| WS 依赖 | `go.mod` grep `websocket/gorilla/nhooyr/coder/gobwas` **全空**;生产代码零 `http.Hijack`;`forwarder.go` 只做单向 SSE | 无 |
| 协议 schema | `internal/proto` 定义 `CapabilityLiveSession`(`envelope_validate.go:327/575`)、`LiveSessionNode`(transport∈{wss,sse}、modalities⊂{text,audio,video},`envelope_validate.go:987/994`)、`LiveUsage` 会计(`accounting.go:48-56`:SessionDurationMS/InputAudioMS/OutputAudioMS) | 已定形 |
| fixture | `internal/proto/fixtures/envelope/live_session_minimal.json`(ingress=/v1/realtime、transport=wss、streaming、accounting.live_usage) | 存在 |
| **接线** | `LiveUsage`/`SessionDurationMS`/`InputAudioMS` 在**非 proto、非 test 生产代码中零消费**(grep 空) | **未接线** |

结论:**Realtime 在 HUAKAI = 501 桩 + 未接线的 schema/fixture 脚手架,无任何可运行实现。** 属明确缺口(F-RT-001,Phase 9+)。

### C. 建 Realtime 需动的子系统(全双工 WS 与现一次性 HTTP relay 根本不同)
- **(a) 新 WS 网关包**:引入 WS 依赖(现无)+ 新 handler 做 HTTP Upgrade(替换 `routes.go:117` 的 501 桩)+ 向上游拨 WS + 双向帧泵。现有 `forwarder.go` 单向 SSE **不可复用**。
- **(b) 鉴权**:✅ **可零改复用** `authResolver.Resolve(ctx, *http.Request)`(`handler.go:35-37/115`),WS 握手 Upgrade 请求带 Bearer 即可,**不触 auth-core(非 Owner-gated)**。
- **(c) 计费 = 最大改造**:现为单发 reserve→settle,Realtime 需**会话级增量计量 + 收尾/滚动结算 + 中途断连的部分用量结算**。可复用 `audiopricing.SchemePerSecond`(SecondMicroUSD)+ 已存在未接线的 `LiveUsage` 作计量载体;但计费轴须对齐官方 **token(非 ms)**(见一.⑤ 口径提醒)。`billing.go:154-156` 的 WithoutCancel+5s 对长连接须重设计(5s 上界不适用长会话)。
- **(d) 并发槽 = 致命点**:`DBSlotManager` 租约固定 `DefaultLeaseDuration = 90 * time.Second`(`internal/pool/dispatcher/slot_manager.go:31`),超时孤儿回收,**全仓无 Renew/Extend/Heartbeat 续租机制**(已亲验:grep 仅命中 Lease/Duration,无续租)。WS 会话通常 >90s→现槽会被孤儿回收器误回收(in_flight_count 失真 / 重复占用)。必须新增槽心跳续租,或为长连接换一套并发计量。释放 `ReleaseFunc` 幂等(`slot_manager.go:122-133`)可复用,但须保证 WS 断开(含异常)必触发释放,否则槽泄漏(与 MEMORY「relay S1 并发槽失败路径泄漏」同域风险)。
- **(e) 连带**:quota 窗口(`quotaenforce.Reserve`)对长连接的预留/回补语义;审计 hop 链(现 HTTP 六跳,WS 需新事件流审计);上游凭证物化 `CredentialVault.Resolve`(可复用)。

---

## 四、建设工时估

前提:production-grade(含 acceptance + §14 变异测试 + E2E);写码全交本机 codex,Claude 只规划/验收(MEMORY 硬规则)。

| 子系统 | 内容 | 工时 |
|---|---|---|
| (a) WS 网关包 | WS 依赖引入 + Upgrade handler(替 501)+ 上游 WS 拨号 + 双向逐帧泵 | 4–6 人日 |
| (c) 计费改造 | 会话级增量计量 + 收尾/滚动结算 + 断连部分结算 + token 口径对齐官方(拆 ms→token) | 5–8 人日(**最大**) |
| (d) 并发槽续租 | 心跳续租(UPDATE lease_expires_at)或长连接替代计量 + 断开必释放 | 2–4 人日 |
| (e) quota/审计连带 | 长连接 quota 预留/回补语义 + WS 事件流审计 | 2–3 人日 |
| 集成 | 账号故障切换/重连语义 + acceptance/变异/E2E + OpenAPI 一致性 | 3–5 人日 |
| **合计** | | **≈ 16–26 人日** |

参照物:三镜中 new-api 的语音链路核心约 200 行(`relay_realtime.go`)+ 计费改造,但 HUAKAI 架构不同(envelope/proto 建模、reserve→settle 计费、DB 槽租约),计费改造与槽续租是硬骨头,不能直搬。量级与 MEMORY「Rust egress ~10–14 人日」同档偏上。

---

## 五、风险

1. **计费口径错位(高)**:fixture 的 ms `LiveUsage` 只对得上 translation/transcription 时长计费,主流 speech-to-speech 是 token 计费。若照 fixture 实现会计→与官方主计费轴偏离→**动钱**(money-coupled,Owner-gated)。
2. **并发槽泄漏/误回收(高)**:90s 租约 + 无续租,长会话必被孤儿回收器误回收;断连(尤其异常)未释放→槽泄漏。与既有 relay S1 同域。
3. **长连接结算断连丢账(中高)**:WithoutCancel+5s 上界不适用长会话;F-RT-001 明确要求 stream resume + partial usage settlement,漏则**掉钱/白吃**。
4. **传输多路错配(中)**:官方浏览器首选 WebRTC(经 `/v1/realtime/calls`),纯 WS 只覆盖「服务端已有裸音频」场景;若只做 WS,浏览器直连场景需 WebRTC 或前端桥,范围可能被低估。
5. **模型名漂移(低)**:官方已迁 `gpt-realtime-2`,fixture 仍用旧 `gpt-4o-realtime-preview`;白名单/路由需按官方现名维护(参照 new-api `constant.go:52-60`)。

---

## 六、决策:**延后做(F-RT-001,Phase 9+)**

**裁定**:本功能明确**延后**,当前不启动实现。理由:
- 非产品核心 relay 链的上线阻塞项(MEMORY「产品核心是 relay 非支付」);现 501 桩 + 测试锁定已是诚实的「未提供」形态,不误导用户。
- 主改造(计费会话级 token 计量、槽续租)**触钱且触并发核心**,属 Owner-gated,不自主启动。
- 三镜中仅 new-api 一家做了真语音,sub2api/CLIProxyAPI 均无——parity 压力低(WS 文本底座三镜有,但语音非普遍 parity 项)。

### 重启前置条件(全部满足方可启动)
1. **Owner 拍板计费口径**:确认 speech-to-speech 走**官方 token 计费**(用户 1tok/100ms、助手 1tok/50ms;费率 gpt-realtime-2 \$32/\$64),而非 fixture 的 ms 口径;translation/transcription 是否单独走时长计费。(money-coupled,必须 Owner)
2. **Owner 批准并发核心改动**:槽租约续租/心跳机制(改 `DBSlotManager`)或长连接替代并发计量。(触 auth/并发核心)
3. **传输范围定义**:先做 WS(服务端裸音频)还是含 WebRTC(浏览器直连);SIP 是否在范围内。
4. **断连结算契约明确**:stream resume + partial usage settlement 的语义与 DLQ/对账兜底(防掉钱/白吃)。
5. **WS 依赖选型**:三镜用 gorilla(new-api/CLIProxyAPI)或 coder/websocket(sub2api),需定并纳入 go.mod 许可审查(clean-room)。

启动时的第一切片建议:先 grep 核当前真码(勿凭本存档),按 (a)WS 管道 → (c)计费 → (d)槽续租 顺序切片,每片 worktree + 变异 + 三门绿,产出 Owner 亲检再提交。

---

## 附:官方来源 URL(2026-07 抓取)
- 总览+传输:`https://developers.openai.com/api/docs/guides/realtime`
- 端点+鉴权+音频:`.../realtime-websocket`
- 事件+格式:`.../realtime-conversations`
- 计费:`.../realtime-costs` · 费率:`.../pricing`
- SIP:`.../realtime-sip` · 转写:`.../realtime-transcription`

---
> 存档来源:调研工作流 wf_2f04a5ec-301(2026-07-10);状态=**延后做 F-RT-001**,非实现。
