# L1 TLS BoringSSL fork backend — Claude Lane Plan

- Lane: Claude PM-orchestrator (plan-only)
- UTC: 2026-05-24T08:05Z
- 互补 lane: docs/process/plans/2026-05-24-boringssl-fork-backend-codex.md(codex,后台跑中)
- 前置决策(Owner 已锁):
  - **AS-D1 (2026-05-24)**:Anthropic OAuth 切片"现在就接 mimicry transport"
  - **STAGE 0 transport** (2026-05-24):双轨 Go utls 先走 + BoringSSL fork 同期启动
  - **BoringSSL 语言路径** (2026-05-24):**Rust 子层用 cloudflare/boring crate + sidecar 接 Go gatewayhttp**
  - **D-rust-1 (2026-05-09)**:RPC 协议选 **gRPC (tonic + prost)**
- 参考 anchor:[docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md)
- CLAUDE.md 条款:#10 parallel-draft / #11 clean-room / #12 fresh-fetch / #13 包结构 / #14 测试质量 / #15 ref 对照

> 【2026-06-02 已更新】本计划中的“Rust core_gateway 未接通生产”“~5/15 步”
> 是 2026-05-24 对旧 `core_gateway` 的历史锚点。当前 BoringSSL Rust sidecar 已落
> Phase 1-3 基础能力：JA4 a/b/c/d + profile expectation、H2 SETTINGS 逐字段控制
> (`crates/tls-sidecar/src/h2_settings.rs`)、Anthropic profile 对真抓包字段校准；Go 侧
> `backend/internal/transport/mimicry/sidecar_client.go` 与
> `backend/cmd/gateway/wiring.go` 已接通 sidecar socket。但 exact-fidelity / 生产启用仍未闭：
> R-SIDECAR-001 raw sigalgs 10/26 gap 与 R-SIDECAR-002 ALPN=h2 raw tunnel/H2 framing
> 在任何生产 sidecar 配置启用前必须解决或明确限制；同时还剩更多 vendor profile 与真上游验证。
> 以下为历史计划。

## §1 目标范围

HUAKAI 要 high-fidelity TLS mimicry,达到**逐字节控制**以下 5 个 fingerprint 维度,补 Go utls 的天花板:

| Fingerprint 维度 | Go utls 现能力 | BoringSSL Rust sidecar 目标 | 差距 |
|---|---|---|---|
| **JA3** | 通过 `ClientHelloTemplate` 选 preset(Chrome/Firefox)+ utls.HelloCustom | 逐字节自定 cipher suite 序 + extensions 序 + supported_versions + key_share + signature_algorithms | utls preset 是 enum,模板里写完字段 utls runtime 仍按 BoringSSL/Go 规约重排 |
| **JA4** | 无支持 | 全 5 段(JA4_a/b/c/d 客户端;JA4S 服务端响应)逐字段控制 | utls 没 JA4 实现 |
| **H2 settings** | 走 Go `net/http2` 默认,SETTINGS / WINDOW_UPDATE / PRIORITY frames 受 Go runtime 控 | 用 `h2` Rust crate + boring 提供的 TLS,逐 SETTING_INITIAL_WINDOW_SIZE / SETTING_MAX_CONCURRENT_STREAMS / 等 9 个字段控制顺序 + 值 | Go runtime 把 H2 wire 协议藏起来,不可改 |
| **ECH** (Encrypted ClientHello) | 不支持 | boring 0.4+ 提供 SSL_set1_ech_config_list | utls 0 ECH |
| **PQ** (post-quantum X25519MLKEM768) | 不支持 | boring 跟随 BoringSSL master,2025+ 支持 PQ key share | Go runtime 跟 PQ 慢半年 |

**总目标**:跟 Anthropic Claude CLI / Cursor app / Copilot client 真客户端 wire-level **完全一致**,vendor backend 不能从 fingerprint 区分 HUAKAI 出站和原生客户端。

## §2 现状与缺口锚点

### 2.1 Go transport mimicry (现生产)

- [`backend/internal/transport/mimicry/utls_dialer.go`](backend/internal/transport/mimicry/utls_dialer.go) — refraction-networking/utls 包装
- [`backend/internal/transport/mimicry/template.go`](backend/internal/transport/mimicry/template.go) — ClientHelloTemplate enum
- [`backend/internal/transport/mimicry/registry.go`](backend/internal/transport/mimicry/registry.go) — 注册表
- [`backend/internal/transport/mimicry/testdata/`](backend/internal/transport/mimicry/testdata/) — 测试模板

[[project_huakai_codex_mimicry_verified]] 2026-05-19 sandbox 抓包验证 `mimicry_chatgpt` wire ja3 = real Codex CLI 0.128.0(`27718d56...`)。**JA3 已能通过 utls preset 复刻,但 H2 wire 没控**,JA4/ECH/PQ 全缺。

### 2.2 应用层 mimicry(非 transport,本 plan 不动)

- [`backend/internal/gateway/mimicry_compose.go`](backend/internal/gateway/mimicry_compose.go) — R7.6 6 步 body 变换 composer(system rewrite / cache_control / tool name / metadata 等)— **应用层逻辑,不是 wire-level 反检测**

### 2.3 Rust core_gateway 现状

[`exploratory/rust-core-gateway/`](exploratory/rust-core-gateway/) — M-rust-1..M-rust-10 atom 全 done(2026-05-09 SUMMARY.md):
- M-rust-1: workspace + config + error + tracing
- M-rust-2: listener + mock upstream + streaming
- M-rust-3: **gRPC route.proto + RouteClient + tonic** ← BoringSSL sidecar 直接复用
- M-rust-5: account_planner + proxy_engine bearer(Anthropic/OpenAI/Gemini/Codex)
- M-rust-6: stream_pipeline(SSE parser + usage extract)
- M-rust-9: prometheus + /metrics

但 [[project_two_data_planes]] 2026-05-21:Rust core_gateway 未接通生产,~5/15 步。**BoringSSL sidecar 是新 crate,跟 core_gateway proxy_engine 协作或独立 binary 都可**(D-1 决策)。

## §3 参考项目方案

### 3.1 cloudflare/boring (Rust ↔ BoringSSL C FFI)

- `~/refs/boring/`(local HEAD 3921f35 — git log 上轮)
- 子包:`boring-sys`(底层 FFI bindings)、`boring`(safe wrapper)、`hyper-boring`(hyper 集成)、`tokio-boring`(async 集成)
- **关键能力**:
  - `SslContextBuilder::set_cipher_list` 控 cipher 序
  - `SslContextBuilder::set_curves` 控 supported_groups 序
  - `SslContextBuilder::set_sigalgs_list` 控 signature_algorithms 序
  - `SslContextBuilder::set_alpn_protos` 控 ALPN
  - `set1_ech_config_list` ECH
  - 0.4+ master 跟 BoringSSL upstream PQ key share

### 3.2 hyperium/h2 (Rust H2 crate)

- `~/refs/h2/`(本地 clone)
- 提供 SETTINGS frame builder,可逐字段控:
  - SETTINGS_HEADER_TABLE_SIZE / SETTINGS_ENABLE_PUSH / SETTINGS_MAX_CONCURRENT_STREAMS / SETTINGS_INITIAL_WINDOW_SIZE / SETTINGS_MAX_FRAME_SIZE / SETTINGS_MAX_HEADER_LIST_SIZE
  - WINDOW_UPDATE / PRIORITY frames
- HUAKAI 借鉴 SETTINGS 控制(behavior paraphrase,**不复制 raw code**)

### 3.3 wreq (Rust HTTP/TLS 伪装)

- `~/refs/wreq/`(BSD)— [[project_relay_core_path]] 提到 clewdr 用 wreq
- 提供 ClientHello + H2 高保真模板;**HUAKAI 借鉴 fingerprint 实现思路但不 vendor**(因为我们要自维护 boring 调用,不绕第三方 facade)

### 3.4 envoy-ai-gateway

- `~/refs-latest/envoy-ai-gateway-extracted/ai-gateway-main/`(Apache-2.0)
- 借鉴:outbound transport 配置模式 + per-vendor profile 表设计

### 3.5 ref 选择 rationale

| 项目 | 用途 | 选择理由 |
|---|---|---|
| cloudflare/boring | **核心** Rust→C FFI | 主流 Rust BoringSSL bindings,生产可用 |
| hyperium/h2 | H2 frame 控 | Rust 圈 H2 协议唯一成熟实现 |
| wreq | fingerprint 实现思路 | 借鉴 behavior,不 vendor |
| envoy-ai-gateway | transport gating 模式 | outbound 配置 first-class control plane 参考 |
| **不选** rustls | 纯 Rust TLS | 不基于 BoringSSL,不达逐字节控制目标 |

## §4 文件级范围

**新增**:
```
exploratory/rust-core-gateway/crates/tls-sidecar/        (新 crate)
  Cargo.toml
  src/main.rs                              gRPC server 入口 (复用 tonic)
  src/proto/tls.proto                      新 gRPC service: TlsConnect + TlsRequest
  src/boring_ctx.rs                        cloudflare/boring 配置封装
  src/h2_settings.rs                       hyperium/h2 SETTINGS frame 控
  src/profile.rs                           profile 加载(TOML / DB)
  src/jacard4.rs                           JA4 计算(本机算,不调外部)
  src/ech.rs                               ECH config 处理
  src/handlers/                            per-vendor handler (anthropic/cursor/copilot/...)
  tests/                                   wireshark+JA3 integration

backend/internal/transport/mimicry/sidecar_client.go      (新)
  type SidecarClient struct{ conn *grpc.ClientConn }
  func (c *SidecarClient) RoundTrip(req *http.Request) (*http.Response, error)
  func (c *SidecarClient) Dial(...) (net.Conn, error)     # gRPC bidi-stream 包装

backend/internal/transport/mimicry/sidecar_client_test.go (新)

backend/internal/transport/mimicry/profile_router.go     (新)
  按 vendor 路由 RoundTripper:
    - sidecar mode → SidecarClient
    - utls fallback → 现 UtlsDialer
    - stdlib → http.DefaultTransport
```

**改动**:
- [`backend/internal/transport/mimicry/registry.go`](backend/internal/transport/mimicry/registry.go) — profile router 注入点
- [`backend/cmd/gateway/wiring.go`](backend/cmd/gateway/wiring.go) — wire SidecarClient(配置:sidecar endpoint)

**禁止新增**:
- ❌ `backend/internal/gateway/` (冻结)
- ❌ `backend/internal/gatewayhttp/` (冻结)
- ❌ `backend/internal/proto/` (冻结)

新文件全部在 `backend/internal/transport/mimicry/`(已存在,not frozen)+ `exploratory/rust-core-gateway/crates/tls-sidecar/`(新子 crate)。

## §5 切片建议 Phase 1-6

### Phase 1: Rust sidecar skeleton + gRPC IPC (1.5 周)

**Spec**:
- 新 crate `tls-sidecar`,workspace 加 boring + tonic + tokio
- `tls.proto` 定义:`TlsConnect(profile_id, target_host, port) → ConnectAck`;`TlsRequest(stream)` bidi
- `BoringCtx::from_profile(profile)` 配 SslContext(JA3 preset 一致)
- Go `SidecarClient` 起 gRPC client + `RoundTrip` 包装
- Phase 1 仅复刻 JA3(其它 fingerprint 后续 Phase),wire 输出跟 Go utls preset 一致

**风险测试** (CLAUDE.md #14):
- **R-P1-A**:sidecar 启动失败 → Go gatewayhttp 必须 fallback to utls(R-LC1 同款 mutation)
- **R-P1-B**:gRPC 连接 drop → 重连退避 + audit `sidecar_disconnect`(mutation:无重连)
- **R-P1-C**:wire JA3 抓包对照 = Go utls 同 profile 输出 (mutation:profile 错配 → JA3 不同)

**Mutation 自检** (CLAUDE.md #14 discriminating fixture):
- 把 Rust `set_cipher_list` 改成 BoringSSL 默认 → wireshark JA3 应变 → test 必须红
- 把 gRPC profile_id 错路由(anthropic→cursor)→ test 必须红

### Phase 2: JA4 计算 + per-vendor profile (1 周)

**Spec**:
- `jacard4.rs` 按 fingerprintjs/ja4 公开规范计算 5 段
- profile 加 ja4_a/b/c/d/d2 字段(TOML)
- 抓包对照 Anthropic Claude CLI / Cursor app / Copilot client 真客户端 JA4

**风险测试**:
- **R-P2-A**:profile JA4 不匹配 vendor 真客户端 → vendor backend 风控 (mutation:profile 用 Chrome JA4 vs Anthropic CLI JA4)
- **R-P2-B**:JA4 计算 bug(段分隔错)→ wireshark 检测不到 (mutation:省一段)

### Phase 3: H2 SETTINGS frame 控制 (1.5 周)

**Spec**:
- `h2_settings.rs` 包装 hyperium/h2;profile 加 h2_settings 字段(map)
- 控:HEADER_TABLE_SIZE / ENABLE_PUSH / MAX_CONCURRENT_STREAMS / INITIAL_WINDOW_SIZE / MAX_FRAME_SIZE / MAX_HEADER_LIST_SIZE
- 顺序也控(SETTINGS frame 内 ID 顺序)

**风险测试**:
- **R-P3-A**:SETTINGS 缺一字段 → vendor backend 检测 (mutation:省 MAX_CONCURRENT_STREAMS)
- **R-P3-B**:WINDOW_UPDATE 时机错 → wireshark 抓包顺序不一致

### Phase 4: ECH (Encrypted ClientHello) (1 周)

**Spec**:
- `ech.rs` 用 boring 0.4+ `set1_ech_config_list`
- profile 加 ech_config_list 字段(DNS HTTPS record 抓取或 hardcode)

**风险测试**:
- **R-P4-A**:ECH config 过期/无效 → 必须 fail open (post-quantum 仍 wire 出去) (mutation:无效 config → silent 退化是 bad)

### Phase 5: PQ key share X25519MLKEM768 (0.5 周)

**Spec**:
- 跟随 BoringSSL master tag(boring crate 0.4+)
- profile 加 pq_enabled bool;Phase 5 开启 = 主流 vendor 已支持 PQ

### Phase 6: profile DB + ops dashboard (1 周)

**Spec**:
- migration `provider_account` 加 mimicry_profile_id 列
- ops dashboard 显示 per-vendor profile + 抓包对照状态

## §6 风险测试矩阵汇总

| ID | 风险 | 真实损失 | mutation 自检 | 判别 fixture |
|---|---|---|---|---|
| R-P1-A | sidecar 挂 → 出站全断 | 全 vendor 出站失败 | 删 utls fallback | sidecar dial 失败注入 |
| R-P1-B | gRPC drop 不重连 | 临时 vendor 断流后无恢复 | 删重连退避 | gRPC stream close 注入 |
| R-P1-C | profile 错配 vendor | JA3 与真客户端不同 → vendor 风控 | profile_id router 错 | wireshark 抓 JA3 对照 |
| R-P2-A | JA4 错配 | 高保真 vendor 风控触发 | profile JA4 写 Chrome 不写 CLI | 抓真 Anthropic CLI JA4 |
| R-P2-B | JA4 计算 bug | 计算出错 vendor 仍能检 | 省一段计算 | 已知 vendor JA4 校对 |
| R-P3-A | SETTINGS 缺字段 | H2 wire 偏差 vendor 检 | 省 MAX_CONCURRENT_STREAMS | hyperium/h2 frame inspect |
| R-P3-B | WINDOW_UPDATE 时机错 | wire 顺序异常 | 时机调换 | wireshark 顺序对照 |
| R-P4-A | ECH 无效 silent fallback | 用户以为加密了实际明文 | silent fallback | ECH config 错给 |

每条都满足 CLAUDE.md #14 mutation 自检 + 判别 fixture。

## §7 D 决策点

### D-Sidecar-1: IPC 协议

| 选项 | 大白话 | ref 对照 |
|---|---|---|
| (A) **gRPC bidi stream** (Owner 已锁 D-rust-1 = tonic+prost) | sidecar 起 gRPC server,Go 用 grpc-go client;TLS connect + stream 都走 gRPC | rust-core-gateway/proto/route.proto 已用此模式 |
| (B) Unix socket + raw TCP forward | 简单 pipe,sidecar 单进程拨号;Go 把 plaintext 灌进 socket | (无既有 ref) |
| (C) HTTP/1.1 CONNECT 代理 | sidecar 当 HTTP proxy;Go 走标准 proxy mode | envoy 模式 |

**Claude 推荐**:(A) gRPC bidi — 已锁,**无需 surface**。

### D-Sidecar-2: boring fork commit pin 还是 floating

| 选项 | 大白话 | ref |
|---|---|---|
| (A) Pin 到 cloudflare/boring 当前 release(0.4.x)| Cargo.toml 固定 minor 版本 | rust crate 主流 |
| (B) Floating master | git dep,跟 BoringSSL upstream;PQ 早进 | (less common) |
| (C) HUAKAI 自己 fork boring → boring-huakai | 可定制 ETM / JA4 / ECH | [[project_l1_tls_boringssl]] "真资产 = 自维护 boring C 层 fork" |

**Claude 推荐**:**Phase 1 用 (A) pin 0.4.x**;**Phase 4-5 进入 (B) floating(为 PQ)**;**Phase 6+ 才进 (C) 自 fork**。**Owner 待拍**。

### D-Sidecar-3: sidecar binary 形态

| 选项 | 大白话 | ref |
|---|---|---|
| (A) **独立 binary**,跟 Go gatewayhttp 一对一部署 | systemd / supervisor 双进程 | core_gateway 独立 binary |
| (B) sidecar crate 编 .so 给 Go cgo dlopen | 单进程,跨语言 FFI | (HUAKAI 反对 cgo 路径,但 .so dlopen 是子方案) |
| (C) sidecar 跟 Rust core_gateway 合一(M-rust-1..10 + tls-sidecar)| 单 Rust binary | exploratory 现已 multi-crate workspace |

**Claude 推荐**:(A) 独立 binary。**Why**:[[project_two_data_planes]] 现 core_gateway 未接通生产 + 加 TLS sidecar 复杂度合并风险;独立 binary 允许 sidecar 先就位,后期再考虑跟 core_gateway 合一。**Owner 待拍**。

### D-Sidecar-4: profile 存储

| 选项 | 大白话 | ref |
|---|---|---|
| (A) TOML 文件 + sidecar 热 reload | ops 改文件,signal reload | envoy 配置模式 |
| (B) PG 表 `mimicry_profile`(跟 TR-D1 health 同期加 schema) | dashboard 可改 | [[project_two_data_planes]] HUAKAI 主依 PG |
| (C) Rust crate 内 const | 编译期校验 | utls 现模式 |

**Claude 推荐**:Phase 1 用 (A) TOML;Phase 6 升级到 (B) PG。**Owner 待拍**。

### D-Sidecar-5: 失败 fallback 行为

| 选项 | 大白话 |
|---|---|
| (A) sidecar fail → 退到 Go utls(降保真但出站通)| fail-open |
| (B) sidecar fail → 当场拒绝出站 + 报警(fail-closed)| 高保真要求 / 信任链 |
| (C) per-vendor 决定:Anthropic fail-closed,Copilot fail-open | 灵活 |

**Claude 推荐**:(B) fail-closed,**因 [[project_trust_ledger_failclosed_policy]] 信任链优先**。Phase 1 测试时用 (A) 降级 OK。**Owner 待拍**。

## §8 验证

- **Phase 1 验收**:sidecar 起来后,Go gatewayhttp 出站 anthropic.com → wireshark 抓包 JA3 一致 Go utls mimicry_chatgpt
- **Phase 2-5**:每 Phase 抓真客户端(Anthropic CLI / Cursor / Copilot)wire 对照 → fingerprint 100% 一致
- Rust 子层 unit:`cargo test -p tls-sidecar`
- Go 子层 unit:`go test -C backend ./internal/transport/mimicry/...`
- Integration:Owner 本机起 sidecar + gatewayhttp + 真 vendor 出站抓包
- 真 vendor 测试限 [[project_real_vendor_account_scope]] 4 vendor(Anthropic/OpenAI/Gemini/Codex)

## §9 Source files read

**HUAKAI**:
- `backend/internal/transport/mimicry/utls_dialer.go:1-30` (本轮 head)
- `backend/internal/gateway/mimicry_compose.go:1-50` (本轮 head)
- `exploratory/rust-core-gateway/SUMMARY.md:1-40` (本轮 read)
- `exploratory/rust-core-gateway/merged/` 目录 + `codex-lane/` 目录(ls)
- `backend/internal/transport/mimicry/` 目录(ls registry.go / template.go / utls_dialer.go)

**Refs**:
- `cloudflare/boring@local:3921f35` (~/refs/boring local clone,boring/boring-sys/hyper-boring/tokio-boring 子包)
- `hyperium/h2@local`(~/refs/h2 本地 clone)
- `wreq@local`(~/refs/wreq,BSD,借鉴 fingerprint 思路)
- `envoyproxy/ai-gateway@3d3d346d09e4`(anchor 表 latest,outbound transport 模式)
- 公开规范:JA3 (Salesforce),JA4 (FoxIO),RFC 9000 (H2),draft-ietf-tls-esni-17 (ECH)

**Recency check (CLAUDE.md #12)**:CLIProxyAPI/litellm/sub2api/new-api/portkey/envoy/helicone/llmgateway 8 项 latest SHA 在 anchor 表;boring/h2/wreq/rustls 是本地 clone(非用作 cite 主轴,只作 behavior 提取),不强制 fresh fetch;若 codex synthesis 时需要 fresh,补 GitHub API 查 boring crate latest。

## §10 Lane attribution + UTC

- Agent: claude-opus-4-7
- Session: HUAKAI 2026-05-24,接 anthropic/placeholder/refresh 3 plan synthesis 后启动
- Lane: PM-orchestrator + specifier(plan-only,实施 lane 转 codex executor + 部分由 Claude 自驱[[feedback_anti_detection_specs_claude_writes]])
- UTC: 2026-05-24T08:05Z
- Cross-discuss target: `docs/process/plans/2026-05-24-boringssl-fork-backend-codex.md`(codex 后台跑中)
- Synthesis 文件: `docs/process/plans/2026-05-24-boringssl-fork-backend-synthesis.md`(codex 完工后)

**Plan 间依赖**:
- Phase 1 完工 → AS-D1 mimicry transport 就位 → 解锁 C1 Anthropic OAuth 切片实施
- Phase 1 完工 → 解锁 PH-D1 placeholder 6 vendor 实施(cursor/copilot 出站需 mimicry profile)
- Phase 2-3 完工 → high-fidelity vendor backend 风控规避
