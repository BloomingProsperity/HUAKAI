# 出口 / TLS 指纹 / mimicry —— 唯一权威文档(SSOT)

> **本文是出口传输、TLS 指纹、客户端 mimicry 领域的唯一真相源。**
> 历史上这块散在 40+ 份计划/风险台账/研究/代码 TODO 里,大量过期且互相打架,导致反复把"故意缓做"误当"缺失"。**以后只看本文。** 其余散计划已删(git history 可查)或降为历史;仍权威的只有:本文 + 代码 + 4 份研究抓包文档 + `docs/specs/` + 风险台账 R-* 条目。
>
> 建档:2026-07-15,基于 4 路只读通读全部文档 + 核代码现场交叉判定。所有事实带 file:line 或研究文档出处。

---

## 0. 一句话现状

**出口 TLS mimicry 已翻转为 Rust BoringSSL sidecar 唯一实现。** IPC v2、八类独立 profile、数据库动态 inline profile、proxy、结构化错误、取消和超时均已接线；Go uTLS、native fallback、旧总开关和运行时依赖已经删除。单镜像双进程、启动 preflight、`/readyz`、进程退出/重启、100/500/1000 并发及故障注入均已通过；只剩受控真实厂商账号验证、独立复审和发布流程，当前不把未执行的真实账号验证写成已通过。

---

## 1. 当前生产真实状态(代码事实)

| 项 | 默认值 | env / file:line |
|---|---|---|
| mimicry 出口 | **只允许 Rust sidecar** | `transport/factory.go`;`cmd/gateway/wiring.go` |
| sidecar socket | **默认 `/run/huakai/tls-sidecar.sock`** | `HUAKAI_TRANSPORT_SIDECAR_SOCKET`;`internal/config/config.go` |
| sidecar 启动门 | **协议、能力或任一必需 profile 缺失均拒绝启动** | `mimicry/profile_catalog.go`;`cmd/gateway/wiring.go` |
| 强制 H1 | **默认不覆盖**,按 profile ALPN | `HUAKAI_TRANSPORT_FORCE_H1`;`internal/config/config.go` |

**默认出站路径**:
- **反转/OAuth 号**(被 `transportModeForProvider` 判为 mimicry)→ 只走 Rust sidecar；socket、IPC capability 或 profile 不可用时拒绝启动/请求，不允许降级到 standard。
- **api-key 官方号** → **Go standard transport**(`http.DefaultTransport.Clone()` + `Proxy=nil`)；代理只能由账号绑定解析器显式注入，不经过 sidecar(安全不变量 I1)。

**vendor→mimicry mode 映射**(`gatewayhttp/chat_completions_dispatch.go:130-153`):claude_code / chatgpt(codex)/ gemini_advanced / antigravity / cursor / copilot / kiro / windsurf 各有 mode;其余一律 standard。

**覆盖状态**:
- Rust sidecar 已内置并接线 **8 个独立 profile**，ready 协议逐项报告实际加载情况。
- Anthropic、Codex、Gemini、Kiro 使用已有实测合同；Antigravity、Cursor、Copilot、Windsurf 使用明确标识的 Safe Equivalent，不能冒充真实客户端抓包结论。
- 每个 mimicry mode 都有独立映射；未知 mode、缺 profile 和过期 sidecar 一律 fail-closed。

---

## 2. ALPN 与强制 H1

- **默认合同**：Rust sidecar 按 profile 的 ALPN 发出 ClientHello；服务端选中 `h2` 时由现有 H2 bridge 承接，选中 `http/1.1` 时走 raw tunnel。
- **兼容开关**：`HUAKAI_TRANSPORT_FORCE_H1=true` 会通过 IPC v2 显式下发，把本次握手 ALPN 收窄为 `http/1.1`。该开关默认关闭，只用于故障隔离。
- **失败姿态**：非法布尔配置阻止启动；sidecar 未声明 `force_h1` capability 时 readiness fail-closed；不能再出现 Go 接受配置、Rust 静默忽略的半接线状态。
- **测试合同**：Go 线缆测试确认字段过 IPC，Rust ClientHello 测试确认只改变 ALPN，不改变 cipher、group、signature algorithm。

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

## 4. 四类内置 profile 与动态 profile

- Rust sidecar 已内置并由 Go mode 映射接通八类独立 profile；四类来自现有实测合同，四类是明确标识的 Safe Equivalent。
- `ready` 返回真实 capability 和已加载 profile 清单；Go preflight 不再靠连接假目标或解析错误文本判断可用性。
- 账号绑定和租户轮换得到的数据库动态 profile 通过 IPC v2 `inline_profile` 下发，由 Rust 严格校验后构建 BoringSSL ClientHello；不再由 Go uTLS 执行。
- 未有内部实测 profile 的 Cursor、Copilot、Antigravity、Windsurf 不得伪造“真实指纹”结论；其 Safe Equivalent 仍由 Rust 执行并接受同一 fail-closed/readiness 合同。
- 当前实现已通过 Go 全量与真实 PostgreSQL 集成、Rust workspace 常规与 ignored 压力/故障测试、100/500/1000 并发、静态门、单镜像冷构建和双进程生命周期 smoke；真实厂商受控验证与最终独立复审仍取决于 §6 发布门。

---

## 5. Rust sidecar 状态

- **代码位置**:`exploratory/rust-core-gateway/merged/crates/tls-sidecar/`(BoringSSL;模块 ja4/boring_ctx/connect/h2_bridge/profile)。vendored boring fork 溯源见该 crate 下 `vendor/boring/MODIFICATIONS.md`(Apache-2.0 attribution;来历=HUAKAI fork boring 5.1 加 `set_extension_order` 等公开 setter 修扩展顺序,Owner 显式授权读 boring 源)。
- **架构定位**:纯传输层(ja4/boring/connect/h2_settings),**不碰 body**;body 伪装永久留 Go。
- **已接部署合同**:Dockerfile 同时构建 gateway 与 sidecar；入口先等待 sidecar ready 再启动 gateway，任一子进程退出都会结束容器；direct/prod Compose 使用同一 `/run/huakai` socket 和 `/readyz`。
- **已解决**:R-SIDECAR-001(boring raw sigalgs,commit 8cfc5467)、R-SIDECAR-002(H2 ALPN raw tunnel→Rust 端 own H2 framing + h2_bridge,commits c0fe5231 等)。
- **IPC v2 已解决**:`ready/capabilities`、builtin/inline profile、proxy、ForceH1、结构化错误、取消与超时已接通；Go 和 Rust 都拒绝未知版本、未知 operation 与缺失 capability。
- **已验证**:冷构建、非 root UDS 权限、数据库/Redis/sidecar readiness、sidecar/gateway 任一退出带动容器退出、`unless-stopped` 自动重启恢复。
- **残留发布门**:四类真实 vendor 小成本 smoke、最终独立复审、唯一 PR 与合并后主线全量复验。本地大并发、长连接取消/回收、proxy 故障矩阵、全量 Go/Rust/静态门和容器生命周期已经通过。

---

## 6. "走 Rust、删 Go" 路线图(**Owner 2026-07-18 拍板定死为唯一方向**,分步)

> **锁死**:出口 TLS 伪装唯一走 Rust sidecar;Go uTLS 已废,**不再补功能/修 bug**,验证后删。
> 执行序 + 全链路真读证据见 `docs/process/plans/2026-07-18-rust-egress-migration-lockdown.md`。
四类 builtin、真实 preflight 和动态 inline profile 已由 `29cfc50a` 完成。剩余步骤：

1. **S5 单镜像可运行交付（已完成）**：固定 Rust/BoringSSL builder；镜像同时包含 gateway 和 sidecar；入口负责 UDS、ready、SIGTERM 和双进程退出；direct/prod Compose 使用同一合同。
2. **S6 重测试与容器 smoke（本地门已完成）**：wire mutation、HTTP/HTTPS/SOCKS5、100/500/1000 并发、长连接取消/回收、故障注入和容器生命周期已覆盖；只剩具备受控账号时执行四类真实 vendor 小成本 smoke。
3. **S7 Rust-only 翻转（已完成）**：mimicry 分支要求 sidecar，缺失/不可用 fail-closed；native Go uTLS、Go 模板执行、native fallback、旧环境变量和死测试已删除；只保留 sidecar client 与 standard transport。
4. **S8 发布门**：Go/Rust 全量、codebudget、冷构建和三套 Compose 已通过；完成独立 review 后创建唯一 PR。Owner 已授权 PR 测试全绿后合并，并在干净主线执行全量复验。

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
- **代码真相**:`backend/internal/transport/{factory,policy}.go`、`backend/internal/transport/mimicry/{sidecar_client,sidecar_protocol,profile_catalog,db_profile}.go`、`exploratory/rust-core-gateway/merged/crates/tls-sidecar/`；以及 `backend/internal/provider/{anthropic,openai,gemini,antigravity,grok}/`。
- **规格(实现多为 DEFERRED)**:`docs/specs/{device-fingerprint-binding,request-pacing-mimicry,outbound-ip-pool,active-anti-detection}.md`。
- **风险台账**:`docs/10_RISK_REGISTER.md` 的 R-TRANSPORT-001 / R-REL-002 / R-SIDECAR-001/002 / R-CHG-MIMICRY-001 / R-GEM-MIMICRY-001 / R-POOL-001。
- **当前唯一执行计划**:`docs/process/plans/2026-07-18-rust-egress-migration-lockdown.md`。旧过程计划不再作为状态入口，必要追溯使用 Git 历史。

---

## 9. 已作废断言黑名单(防后人误引)

以下说法**均已过时/被推翻**,任何文档若仍这么写都不算数:
1. "所有 profile 都必须全局强制 H1" → **错**，默认按各 profile 的 ALPN；强制 H1 只是临时兼容开关。
2. 旧 Anthropic JA3 `de88744b20558d50f03a5f0ea176ee98` → **非真值**(早期 OpenSSL 派生样本;真值见 §3 的 t13d JA4)。
3. "Anthropic 路径暂停/留空" → **错**,anthropic 真指纹已抓并 serving。
4. "gemini 仍是 stub" / "只有 3 家真 mode、ClaudeCode 归 fallback" → **错**,四家均有真指纹。
5. "core_gateway 已是生产数据面 / 8 vendor 已全闭环 / Go uTLS 是最终出口" → **全错**；当前目标是独立 Rust sidecar 成为唯一 mimicry 出口，未完成的 provider 必须 fail-closed 或补齐 profile。
6. "Codex/Gemini sidecar profile 仍是 Deferred" → **错**，四类内置 profile、preflight 和 IPC 已由 `29cfc50a` 完成；尚未完成的是容器默认切换与真实 vendor smoke。
7. "H2 SETTINGS 永远不需要" → **错**；只有明确协商 H1 的 profile 不需要，能够协商 h2 的 profile 必须由 wire 测试证明 bridge 与 settings 合同。
