# 出口(Go↔Rust sidecar)+ Relay 质量验收 + 可观测性 实现计划

> 2026-07-03 Claude 起草。Owner 指令:「写进实现计划,一步一步来,先完成手上的。我们出口是 rust 模块。
> go-rust 衔接你也要搞个监控,这一块日志系统要记录和拆分详细,不能出错,和方便后续维护。」
> 本计划把本会话累积的方向(国内厂多模态接入 + relay 保真/性能 + 出口衔接监控 + 上线内测)收敛成一份
> 分批、可逐步执行的实现路线。**核心纪律**:每步都先亲核真码、每片过 §14 变异 + 对抗审查零 S0/S1。

---

## 0. 现状(已亲核验证,非记忆)

**已打通(真上游):**
- 豆包(火山方舟)**文本 chat / 工具调用 / 多模态理解** 三连通 —— HUAKAI 生产 `OpenAICompatPassthroughAdapter` 真码打活 ark(`test(provider) 0bdf2ea3`,`doubao_live_upstream_test.go`,`live_upstream` tag)。
- 混元 **Responses API** 通 —— 国际 TokenHub(`tokenhub-intl.tencentmaas.com/v1/responses`,`input`/`instructions` 形态,非 chat/completions)。
- 六家国内厂**代码接入 2026-07-02 批已全落地**;默认端点已对齐国内站(`7ec6da57`)。

**前端 + 账号转API(两只读审计核实):**
- 前端 57 页真接线、零假数据,核心链**配账号→发key→看用量→调价 端到端可点通**;缺口都是边角。
- 账号转API **内测级可走通**:后端采集流(4 类凭据 oauth/api_key/session/upstream_passthrough + OAuth 交换器)活线 + 启动 fail-closed 强校验;前端建号→凭据 vault→池组→渠道→model 绑定→发 key 全套 UI 齐 + 接线测试兜底。
- **二进制确实内嵌并服务前端**(Dockerfile `npm run build`→`COPY dist`→`go build -tags embed`;`middleware.go:130` 挂 SPA)。历史「Dockerfile gate 前端」问题已修。

**出口架构(本计划重点,已亲核):**
- 出口=**两轨**:①同步透传轨(`OpenAICompatPassthroughAdapter` / gemini passthrough,body 逐字节透传,chat 族全模态零/一行代码);②异步任务轨(`mediatask` 统一框架 + 每家薄翻译,视频/图片/音乐)。
- **真出口(TLS 层)= Rust tls-sidecar**:Go `SidecarClient` 经 Unix socket + 长度前缀二进制帧(上限 1MB)接本地 BoringSSL sidecar(`internal/transport/mimicry/sidecar_client.go`);`transport/factory.go` 按 `SidecarSocketPath` 选路,`SidecarFallbackEnabled` 默认 false=fail-closed;错误分类 `sidecar_unavailable`/`sidecar_profile_unavailable` 已有。
- **缺口(Owner 点名)**:go↔rust 边界**几乎无日志**(sidecar 文件 grep 无 slog/zap);mimicry 指纹模板未进镜像(Dockerfile 未 COPY `tools/fingerprint-collector/templates`)→ OAuth 账号中转走通用兜底指纹,抗风控打折。

---

## 1. 核心纪律:每家上游的「出厂三关」

Owner 定的红线:**200 OK 不算数**。每家上游 / 每个模态接入后,必须过以下三关才算「可上线」,而非「能返回」:

| 关 | 判据 | 怎么测 |
|---|---|---|
| **保真** | 请求参数一字不差转上游;上游返回**不吞 token / 不吞字段**(尤其流式逐事件);usage 计费不算错 | 流式逐 SSE 事件 diff(客户端收到 == 上游发出);非流字段全等 |
| **首字延迟** | HUAKAI 流式 TTFT − 直连 TTFT = 中转增量,须有上限 | 同 prompt 直连 vs 过 HUAKAI 各测 TTFT,算差值 |
| **并发** | 并发打满真实触发排队/拒绝/槽释放,无 500、无漏并发、结算后槽释放 | per-key 并发 cap + 账号级槽 + 用户级,压测 RPM/TPM |

> 这三关写成**可复现的验收 harness**(见工作流 B),每家上游跑一遍出数据,归档到 `docs/architecture/runtime-logic/`。

---

## 2. 工作流(分批,先完成手上的)

### 批 0(手上的,先跑完)
- **全模态能力面调研 workflow**(w16jhk8b0 跑中)→ 产出「厂商×模态能力矩阵 + 三镜对照 + 缺口分批」。**这是批 2/3 的输入**,跑完再定薄翻译/新 lane 的具体顺序。
- 不新开建设切片,等矩阵。

### 工作流 A —— go↔rust 出口衔接监控 + 分层日志(Owner 重点,最高优先)
**目标**:出口是命门,衔接不能出错、要可观测、好维护。分层记录、职责单一、不堆砌。

- **A1 边界结构化日志(slog,分层)**:在 `sidecar_client.go` 拨号/发帧/收帧/错误四个点补结构化 slog,字段规范固定:`component=egress_sidecar` + `profile_id` + `socket_path`(脱敏)+ `frame_bytes` + `phase`(dial/write/read/close)+ `error_class`(复用已有 `sidecar_unavailable`/`sidecar_profile_unavailable`)+ `request_id`(全链继承)。**分层**:握手层 / 帧传输层 / 上游响应层各一类,互斥不混。
- **A2 衔接监控指标**:sidecar 拨号成功率、帧往返延迟、fallback 触发次数(fail-closed 下应恒 0,非 0 即告警)、profile 命中率。落到现有 obs/metrics。
- **A3 fail-closed 可见性**:sidecar 不可用时当前静默 fail-closed → 补一条 Warn 级审计 + 指标,让运维立刻看到「出口降级/拒服务」而非黑盒。
- **A4 Rust 侧 tracing 对齐**:Rust sidecar 用 `tracing` 结构化日志,与 Go 侧 `request_id` 串起来(帧头带 request_id 透传),做到一个请求 go↔rust 两侧日志可关联。**这是「全链 request_id」(日志片 G)在出口段的落地。**
- **A5 mimicry 模板进镜像**:Dockerfile 补 COPY `tools/fingerprint-collector/templates`,消除通用兜底指纹;加启动自检日志「已加载 N 个真指纹模板 / 走兜底」。
- 依赖:与既有**日志计划 A-H**(`docs/process/plans/2026-07-02-logging-observability-plan-claude.md`)合流——A1/A4 即片 G(全链 request_id + trace_id)在出口段的具体实现;片 F(DLQ 可见)/片 H(采样+脱敏 CI)照旧排在后面。

### 工作流 B —— Relay 质量验收 harness(出厂三关)
- **B1 保真验收**:流式逐事件 diff harness —— 同一请求,一路直连上游、一路过 HUAKAI 全链 gateway,逐 SSE 事件比对,断言零吞字段/零吞 token,usage 一致。先验豆包(有 key),再随 key 到位逐家。
- **B2 TTFT/延迟验收**:直连 vs 过 HUAKAI 的 TTFT 差值 + 总延迟增量,出数据、设上限。
- **B3 并发验收**:全链 gateway 下并发打满,测 RPM/TPM + per-key/账号槽 + 排队/拒绝/结算后槽释放(§17 模块配合)。
- 产出:每家上游一张「出厂三关」体检表,归档 runtime-logic 文档。

### 工作流 C —— 模态货架分批建(已有 w16jhk8b0 全模态调研矩阵支撑)

**调研关键翻案(亲核 `routes.go` 真码)**:HUAKAI 出站货架比预期完整——chat(4 ingress/30+ family)/completions/messages+count_tokens/responses+compact/embeddings/rerank/images(gen/edit/**variations**)/audio(speech/transcriptions/translations)/gemini 原生 **全是活线**;media-tasks/mj/suno/video 四 router 已挂;**proto 信封层已建好 `CapabilityBatch/File/LiveSession/Video/Audio/Image` 全套能力节点**(只差 HTTP lane)。

**对标 new-api 的真实缺口收敛为「两硬一软 + 两个超越机会」**:
- 【硬缺口①】**realtime WebSocket**:唯一从零(new-api 有,HUAKAI 是 501 占位 `handleRealtimeRoadmap`,proto `CapabilityLiveSession` 已在)。
- 【硬缺口②·最高杠杆】**异步媒体最后一公里**:mediatask 框架 + 4 router + 自有钱路径 + orphan 对账**都已在**,但只是「单一 `ProviderBaseURL` 通用透传」——缺各厂真实方言薄翻译 + 成熟后台轮询 + 三段计费钩子,无法一个 key 直连豆包 Seedance/通义 wanx/智谱 cogvideox/MiniMax/Veo/Sora 原生。
- 【软缺口】**moderations 对外 relay**(小 lane,仅 OpenAI 有真上游)。
- 【超越机会】**batch / files**:**new-api 自己也没做**(batch 无 relay 路由、files 501 占位),而 HUAKAI proto 已建能力节点——是差异化卖点(batch 半价走量)。

**分批(工时估自调研):**
- **批 1(配置即通,代码≈0,~1-2 周)**:注册 12 家渠道账号 + 配 base_url/key/model 别名(`registrydefault`+`pool/api.go` vendor 映射已在)→ 冒烟 chat/vision/tools/embeddings/responses/同步 image/同步 audio 全绿。依赖:12 家 key。
- **批 2(薄翻译·异步视频图片,~3-6 周)**:`mediatask/provider.go` 单一 HTTPProvider → per-family adaptor + 各厂 video/image translator + 后台轮询(超时 sweeper + **per-task CAS 防重复退款** + 三段计费钩子)。**正好补 HUAKAI 历史坑 #4 非原子/#5·#7 白吃成本**。范本 `videoclient/translate.go`。依赖:各厂视频 key。
- **批 3(新 lane,proto 地基已在故偏下限,~6-12 周)**:realtime WS(F-RT-001)→ batch+files(成对,proto 节点已建)→ moderations。
- **国际端点**:优先 `upstream_passthrough` 凭据(带 base_url 覆盖)无代码支持国际站(混元 TokenHub 走此 + Responses)。混元 TokenHub 只吃 `/responses`,是**批3 Responses 转译**或 upstream_passthrough+endpoint 覆盖的用例。

### 工作流 D —— 上线内测 checklist
| 项 | 类型 | 工时 | 阻塞? |
|---|---|---|---|
| 生成 audit ed25519 私钥 + 备 5 必填 env(credential/session key、bootstrap token、release_mode、db url)+ 跑迁移 + 建首个 admin | 运维配置 | ~半天 | 是(生产拒启) |
| relay 质量三关验豆包(工作流 B) | 工程验收 | 1-2 天 | 是(你的红线) |
| mimicry 模板进镜像(工作流 A5) | 小修 | ~1h | 否(质量) |
| go↔rust 边界日志/监控(工作流 A1-A4) | 工程 | 与 B 合流 | 否但强烈建议先做 |

---

## 3. 排期与依赖

```
批0(手上的:全模态调研 w16jhk8b0)   ← 正在跑,等矩阵
  ├─→ 工作流 A(go↔rust 监控+日志)   ← Owner 最高优先,可与 B 并行开
  ├─→ 工作流 B(relay 质量三关 harness) ← 先豆包,出「出厂标准」
  ├─→ 工作流 C(模态货架分批)         ← 依赖批0矩阵定顺序
  └─→ 工作流 D(内测 checklist)       ← 配置项随时可做
```

**先完成手上的**=等全模态调研矩阵 + 先落工作流 A/B(出口监控 + 质量 harness),这两样是「能返回→能上线」的转化关键,也是 Owner 本轮点名的两刀。

---

## 4. 决策点(Owner-gated,需签核)

- **计费补行**(glm-5.x / hunyuan-turbos-latest / 豆包带日期后缀名):money + schema,撞上 `pricingUnavailable`;迁移稿可先备,合并需签核。
- **翻 sidecar 默认开**:出口从 Go 默认切成 Rust sidecar 默认(R-SIDECAR-001/002),是默认行为翻转 + 出口姿态变更,Owner-gated。当前计划只补监控/日志/模板,不擅自翻默认。
- **国际端点默认策略**:是否给某些厂默认挂国际站 / 双通道,默认行为翻转,Owner 定。
- **各厂真 key**:Gemini AIza / 智谱 / 通义 / 文心 国内 key —— 到位才能逐家过出厂三关。

---

## 5. 不做 / 排除

- 全球推理托管云(Fireworks/DeepInfra/SiliconFlow/Cerebras)—— Owner 明确不接。
- 真支付网关 —— 手动 admin 充值已够,非上线 blocker。
- 不发明新出口抽象:一切走「同步透传轨 + 异步任务轨 + Rust sidecar TLS」现有三件套,加厂只加薄翻译/配置。
