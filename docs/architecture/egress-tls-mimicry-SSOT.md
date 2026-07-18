# 出口 / TLS 指纹 / mimicry —— 唯一权威文档(SSOT)

> **本文是出口传输、TLS 指纹、客户端 mimicry 领域的唯一真相源。**
> 历史上这块散在 40+ 份计划/风险台账/研究/代码 TODO 里,大量过期且互相打架,导致反复把"故意缓做"误当"缺失"。**以后只看本文。** 其余散计划已删(git history 可查)或降为历史;仍权威的只有:本文 + 代码 + 4 份研究抓包文档 + `docs/specs/` + 风险台账 R-* 条目。
>
> 建档:2026-07-15,基于 4 路只读通读全部文档 + 核代码现场交叉判定。所有事实带 file:line 或研究文档出处。

---

## 0. 一句话现状

**保号骨架(IP 强绑定、凭证安全、救活型调度)硬;指纹层与 sub2 平手;真短板是"行为聚合 + 应用层强伪装没打开 + Gemini 出站还用假 UA"。** 出口默认走 Go uTLS + 强制 HTTP/1.1(正确,对齐真客户端);Rust BoringSSL sidecar 已实现但默认没部署;codex/gemini 的 mimicry 是 Owner 主动**缓做(DEFERRED)**,不是漏做。

---

## 1. 当前生产真实状态(代码事实)

| 项 | 默认值 | env / file:line |
|---|---|---|
| mimicry 总开关 | **默认开**(`!="false"` 即开) | `HUAKAI_TRANSPORT_MIMICRY`,`transport/mimicry_switch.go:16-18` |
| sidecar(是否走 Rust) | **默认空=不走**,走 Go uTLS | `HUAKAI_TRANSPORT_SIDECAR_SOCKET`,`config.go:253`;`factory.go:202,230` |
| sidecar 回退 | **默认 false**(fail-closed) | `HUAKAI_TRANSPORT_SIDECAR_FALLBACK`,`factory.go:76-78` |
| 强制 H1 | **默认开**,锁 http/1.1 | `HUAKAI_TRANSPORT_FORCE_H1`,`mimicry/utls_dialer.go:51-52` |

**默认出站路径**:
- **反转/OAuth 号**(被 `transportModeForProvider` 判为 mimicry)→ **Go uTLS native mimicry**(`factory.go:230`),ALPN 锁 http/1.1。
- **api-key 官方号** → **裸 Go standard transport**(`factory.go:241-282`,`http.DefaultTransport.Clone()` + `Proxy=nil`);指纹层对官方 key 通道本就不敏感,不伪装(安全不变量 I1)。

**vendor→mimicry mode 映射**(`gatewayhttp/chat_completions_dispatch.go:130-153`):claude_code / chatgpt(codex)/ gemini_advanced / antigravity / cursor / copilot / kiro / windsurf 各有 mode;其余一律 standard。

**覆盖不对称(关键)**:
- Go 模板认 **4 家**(`tools/fingerprint-collector/templates/`:anthropic/codex/gemini/kiro)。
- Rust sidecar 内置只认 **1 家**(anthropic,`registry.go:41-48` `SidecarProfileForMode` 只映射 claude_code)。
- cursor/copilot/antigravity/windsurf **无模板** → mimicry mode 下 fail-closed(`factory.go:578-583`,除非 `HUAKAI_TRANSPORT_PHASE_A_FALLBACK=true`)。

---

## 2. 强制 H1 = 拍板决定,且正确("不做 H2" 不是缺口)

- **决策**:`HUAKAI_TRANSPORT_FORCE_H1` 默认开,出口锁 HTTP/1.1;"仅换 BoringSSL sidecar 能自洽 h2 时才关"(`docs/deploy/go-live-readiness.md:38`)。review 文档明写"**已拍板的『不做 H2』出口决策**"。
- **依据(实测)**:真客户端模型 API 全走 HTTP/1.1——
  - Gemini CLI:"**48 个 request 全部 HTTP/1.1**",即使 ALPN 广告了 `h2+http/1.1`(`docs/research/2026-05-14-gemini-cli-request-signature.md`)。
  - Kiro CLI:模型 API HTTP/1.1,"抓包没有显示使用 HTTP/2"(`docs/research/2026-05-14-kiro-cli-request-signature.md`)。
  - Codex:reqwest 默认协商,未强制 h2(`docs/research/2026-05-14-codex-cli-request-signature-codex.md`)。
  - Anthropic native 2.1.187:ALPN 仅 http/1.1(见 §3)。
- **结论**:既然真客户端全走 H1,强制 H1 = 逐字节对齐;`tls-sidecar/src/profile.rs` 的 `[profile.h2_settings]` 空 TODO = **设计如此、留空正确,不是缺口**,走 Rust 也不用补(Rust H2 桥接能力已实现见 §5,属冗余保留)。
- **唯一细微不完美**:Gemini/老 Node Anthropic 客户端 ClientHello 里 ALPN *广告* h2(虽走 H1);强制 H1 会把 ALPN 收窄成只 http/1.1——对 Claude native 逐字节精确,对 Gemini 是"少广告个 h2"的微小差异(协议本身仍对)。要逐字节就在其 profile 里 ALPN 广告 `h2+http/1.1`、连接仍 H1,与"做 H2"无关。

---

## 3. 四家真客户端指纹实测(权威值 + 成熟度诚实标注)

| 客户端 | HTTP | ALPN | JA3 / JA4 | 真 UA | 抓包成熟度 |
|---|---|---|---|---|---|
| **Anthropic Claude Code**(native 2.1.187) | H1 | 仅 http/1.1 | JA4 `t13d1714h1_5b57614c22b0_43ade6aba3df`(`profile.rs`,sub2 双验证) | `claude-cli/2.1.63 (external, cli)` | **TLS 已抓(权威);H2 SETTINGS 未抓(因走 H1 不需要)** |
| **Gemini CLI**(model API `ht` 变体) | H1 | h2+http/1.1 | JA3 `55ba29…` / JA4 `t13d5212_ht_9b003dc3eba7_4e5c652b160e` | `GeminiCLI/0.41.2/gemini-3.1-pro-preview … google-api-nodejs-client/9.15.1` | **wire 级已抓(权威,mitmproxy)** |
| **Kiro CLI** | H1 | — | JA3 **随机化(每次变,不可固定判等)** / JA4 稳定前缀 `t13d0910_00_5a0d15427bfb` | `aws-sdk-rust/… AmazonQ-For-CLI` | **wire 级已抓(权威)** |
| **OpenAI Codex CLI** | H1 | (中性 SNI 抓) | JA4 `t13d3011_00_18932205182d_ef824704554f` | `codex_cli_rs/…`(originator `codex_cli_rs`) | **TLS JA3/JA4 已抓**(用中性主机 `no-sni.vercel-infra.com` 抓 ClientHello,指纹与目标无关);**HTTP/header 层为源码级分析;H2 SETTINGS 未抓** |

> **codex 状态裁定(消除历史"打架")**:模板 `tools/fingerprint-collector/templates/codex-cli.json` 的 `tls_layer.source="capture"`(TLS 真抓)+ `http_layer/auth_layer` 为 `source-analysis`(源码级)。风险台账"codex ja3 已 PASS"指 TLS 层;05-14 研究文档"仅源码级"指 HTTP/auth 层。**两者都对一半,不矛盾**:codex TLS 指纹已抓、应用层是源码推的。

**判等注意**:Kiro 的 JA3 随机化,须用 JA4 前缀 + cipher 集合 + supported groups + header 层判定,**不能固定 JA3**;Gemini 须保留 `ht`(模型 API)与 `00`(辅助连接)两个变体并存。

---

## 4. codex / gemini mimicry = Owner 主动缓做(DEFERRED,非漏做)

- **D-4=A**(2026-05-27,commit 7a34ae7,`RISK_REGISTER` R-CHG-MIMICRY-001):ChatGPT/codex 本轮只接 OAuth,**不启用 codex_cli mimicry sidecar profile**。阻塞点=Rust 生产 dispatch builder 对 Pending 状态 fail-closed(F-2.3a runtime preflight 未完成)。→ DEFERRED / Mandatory Roadmap。
- **D-5=A**(2026-05-27,R-GEM-MIMICRY-001):Gemini 本轮只接 OAuth,**不做 Gemini mimicry sidecar**。同款 Rust builder fail-closed 阻塞。→ DEFERRED / Mandatory Roadmap。
- **解锁条件**:Rust §14b runtime preflight(F-2.3a)接通 + OCAW(Owner 授权)切片。

---

## 5. Rust sidecar 状态

- **代码位置**:`exploratory/rust-core-gateway/merged/crates/tls-sidecar/`(BoringSSL;模块 ja4/boring_ctx/connect/h2_bridge/profile)。vendored boring fork 溯源见该 crate 下 `vendor/boring/MODIFICATIONS.md`(Apache-2.0 attribution;来历=HUAKAI fork boring 5.1 加 `set_extension_order` 等公开 setter 修扩展顺序,Owner 显式授权读 boring 源)。
- **架构定位**:纯传输层(ja4/boring/connect/h2_settings),**不碰 body**;body 伪装永久留 Go。
- **未默认部署**:三份 compose 无 sidecar 服务/socket;`SidecarSocketPath` 默认空→走 Go uTLS;`SidecarFallbackEnabled` 默认 false=fail-closed。接线机制:配了 socket 才走 sidecar,否则保持 uTLS(2026-05-24 sidecar-transport-factory 落地)。
- **已解决**:R-SIDECAR-001(boring raw sigalgs,commit 8cfc5467)、R-SIDECAR-002(H2 ALPN raw tunnel→Rust 端 own H2 framing + h2_bridge,commits c0fe5231 等)。
- **残留待补(启用生产 sidecar 前)**:① anthropic profile 的 H2 SETTINGS 值未抓(空 TODO;但因走 H1,非阻塞);② 扩展 13 signature_algorithms 存 26 个真 ID 但 boring 只用 10 项字符串,字节级不精确,且现有 JA3/JA4 wire 测试**不校验扩展 13 内容**(绿测会掩盖此不一致——保留告警,见 2026-06-02 sidecar-sigalgs 落地);③ 模板进镜像 + go↔rust 边界日志/监控。
- **翻 sidecar 默认开** = 默认行为翻转 + 出口姿态变更,**Owner-gated**,未执行。

---

## 6. "走 Rust、删 Go" 路线图(**Owner 2026-07-18 拍板定死为唯一方向**,分步)

> **锁死**:出口 TLS 伪装唯一走 Rust sidecar;Go uTLS 已废,**不再补功能/修 bug**,验证后删。
> 执行序 + 全链路真读证据见 `docs/process/plans/2026-07-18-rust-egress-migration-lockdown.md`。
> **本路线图漏了一条耦合(该计划已补)**:DB 每账号自定义指纹+轮换池(`tlsfpresolve` 建 Go uTLS RT)
> 整条建在 Go uTLS 上,而 sidecar 协议(`proto.rs ControlRequest`)只收 `profile_id`、不收 inline profile
> → 删 Go(下方第 5 步)会废掉该功能,除非扩协议加 `inline_profile`(D1,推荐,保功能)或退役(D2)。

现状:出口默认 Go uTLS;Rust sidecar 只内置 anthropic 且未部署。删 Go-native 前必须 Rust 接得住,否则生产打穿。步骤:
1. **补 codex/gemini/kiro 指纹进 Rust profile**(现只 anthropic;抓包数据已有,是 JSON→TOML profile 格式搬运,非重新抓)+ Go 侧 mode→sidecar-profile 映射补齐四家。
2. **补完 Rust 生产 dispatch builder 的 runtime preflight(F-2.3a)**——现对非 anthropic profile fail-closed(卡 Pending),这是四家没上生产的直接技术卡点,也是 D-4/D-5 解锁条件。
3. **sidecar 接进 docker-compose + 设默认出口**(现三份 compose 都没部署)。**[Owner-gated:默认行为翻转]**
4. **Gemini 出站换回真 UA**(现仍是自编 `HUAKAI-GeminiCLI/1.0`,`provider/gemini/code_assist.go:41`;真 UA `GeminiCLI/0.41.2…` 已在抓包里)。
5. 全 vendor 出站验证通过后 **删 Go-native uTLS**(`utls_dialer.go`/`template.go`/`db_profile.go`);**保留 sidecar 客户端**(`sidecar_client.go` 等,是走 Rust 的必经路)。**[Owner-gated:删码]**

(H2 SETTINGS 不在路线图——真客户端走 H1,不需要。)

---

## 7. 安全不变量(永久保留,写码/审查必守)

- **I1 · scope**:伪装仅施加**反转/OAuth 订阅号**;apikey 官方开发者 key **永不**伪装。
- **I2 · 真模板**:system/工具名重写必须用**真** Claude Code 模板;**假模板比不做更糟**(抓真样本是硬前提)。
- **不做激进 ban-evasion**:不做逐请求指纹轮换 / 设备码重置 / WAF 欺骗 / 匿名薅号(与保号"像真客户端"是两回事;真客户端指纹是固定真实的)。
- **不碰缓存键**:伪装只作用于 dispatch 专用 body 拷贝。
- **应用层强伪装(R7)现状**:6 步引擎只开了 step5(改 metadata.user_id),其余(system/tool 形状注入)默认关/paused;Gemini 出站仍自编 UA。这是"应用层伪装没打开",属 §6 路线图 + Owner-gated。

---

## 8. 原始证据 / 权威源指路(不复制,只指路)

- **真抓包/源码研究**(永久保留,勿删):`docs/research/2026-05-14-{codex-cli,gemini-cli,kiro-cli}-request-signature*.md` + `2026-05-16-vendor-fingerprint-data-sonnet.md`(含四家 OAuth client_id/endpoint)。
- **代码真相**:`backend/internal/transport/mimicry/{utls_dialer,sidecar_client,registry,template}.go` + `testdata/` + `tools/fingerprint-collector/templates/`;`backend/internal/provider/{anthropic,openai,gemini,antigravity,grok}/`。
- **规格(实现多为 DEFERRED)**:`docs/specs/{device-fingerprint-binding,request-pacing-mimicry,outbound-ip-pool,active-anti-detection}.md`。
- **风险台账**:`docs/10_RISK_REGISTER.md` 的 R-TRANSPORT-001 / R-REL-002 / R-SIDECAR-001/002 / R-CHG-MIMICRY-001 / R-GEM-MIMICRY-001 / R-POOL-001。
- **近期过程文档**:`2026-06-24-mimicry-global-switch`、`2026-07-03-{r7-mimicry-full-activation,egress-relay-quality-observability}`、`2026-07-10-claude-oauth-serving-mimicry-*`、`2026-06-02-sidecar-*`。

---

## 9. 已作废断言黑名单(防后人误引)

以下说法**均已过时/被推翻**,任何文档若仍这么写都不算数:
1. "Anthropic/真客户端走 HTTP/2、需 fork x/net/http2 做 H2 SETTINGS/HPACK 指纹" → **错**,全走 H1、强制 H1。
2. 旧 Anthropic JA3 `de88744b20558d50f03a5f0ea176ee98` → **非真值**(早期 OpenSSL 派生样本;真值见 §3 的 t13d JA4)。
3. "Anthropic 路径暂停/留空" → **错**,anthropic 真指纹已抓并 serving。
4. "gemini 仍是 stub" / "只有 3 家真 mode、ClaudeCode 归 fallback" → **错**,四家均有真指纹。
5. "core_gateway 是生产数据面 / 8 vendor 全闭环 / rquest 集成" → **全错**,core_gateway 已退役(2026-06-02 方向 C)、Rust 降为非默认 sidecar、生产走 Go uTLS、rquest 弃用。
6. "codex mimicry 应拒绝 / 需上游书面授权" → **已被推翻**,现为 Owner D-4/D-5 缓做 + R7 默认做。
7. "H2 SETTINGS 空 = 缺口" → **错**,走 H1 故 H2 不需要,留空是设计。
