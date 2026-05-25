# W11-F F-1 — L2 HTTP/2 fork → ProxyEngine 真接线 (Claude plan-trio draft)

**Author**: Claude (PM-Orchestrator)
**Date**: 2026-05-25 UTC
**Mode**: CLAUDE.md #10 plan-trio — Claude + Codex 平行独立 draft; 本文件**未参考**
任何 codex-side draft (待 Owner / Claude 合成时才对比).
**Branch**: `claude/rust-hardening` (not pushed)
**Prereqs**: F-2 epic 已闭 (F-2.1/2.2/2.3/2.3+/2.5 all done, 3 轮 codex review clean
post c6c6d7d). F-1 是 W11-F 阶段下一个最重最险的子项 (task #21).

---

## 1. Goal

把 HUAKAI rust 网关的出站 HTTP/2 stack 从"stock hyper h2 (不可控 SETTINGS / pseudo-header
顺序)"升级到"vendored MIT http2 fork (`0x676e67/http2` git-dep @ `a33b27e`) + profile-driven
SETTINGS order / pseudo-header order, 配合 mimicry-boring TLS connector 形成完整 byte-level
mimicry 链路", 让 `--features mimicry-boring,mimicry-http2-fork` build 出来的 gateway 调
Anthropic API 时, ClientHello + 首个 SETTINGS frame + 首个 HEADERS frame 三段
byte 全部 match `BuiltinProfile::AnthropicClaudeCode` 模板.

延伸目标: 让 Codex / Gemini / Kiro profile 走同一 typed gate, 在 L2 byte 不 match 时
**fail-closed at builder** (与 F-2.3+ 的 L1 preflight gate 同语义).

---

## 2. Scope

**In scope**:
- 真实 TCP/TLS → HTTP/2 fork 连接 (替换 / 共存 hyper Client legacy)
- L2 typed preflight gate (镜像 F-2 的 L1 preflight 结构, 复用 `MimicryProductionCanaryError`)
- 真上游 byte-capture 验证 (F-2.5 风格, 但抓 H2 frame 不是 ClientHello)
- ProxyEngine `relay.rs` 适配 (能消费 fork-h2 Response Body, 不动 stock-h2 路径)
- `mimicry-http2-fork` feature 默认仍 off, 但 mimicry-boring 路径下当 feature 开启时
  真正使用 fork

**Out of scope**:
- 改 H2 protocol semantics (window mgmt / GOAWAY / RST_STREAM) — 跟 fork 默认走
- H1 fallback 路径 — `auxiliary_no_alpn` variant 等 H1-only profile 还是走 hyper h1
- GREASE frame 注入 (RFC 8701) — out-of-scope follow-up
- 平台 fingerprint divergence (F-2 codex 5th gap 那类) — F-1-followup
- backend/internal 任何 Go-侧改动

---

## 3. Sub-phase sequence

F-1 拆 5 个 sub-phase, 每个独立可 commit + 独立 codex review 闭环:

| sub | scope | 估时 | 风险 | 串行依赖 |
|---|---|---|---|---|
| **F-1a** | http2 fork connector spike | 1-1.5 codex-day | 中 | 无 |
| **F-1b** | builder 接入 + L2 typed gate | 1-1.5 codex-day | 中 | F-1a |
| **F-1c** | ProxyEngine relay 适配 | 2 codex-day | **高** (relay 是 hot path) | F-1b |
| **F-1d** | L2 runtime preflight runner | 1 codex-day | 中 | F-1a (gate 接口) |
| **F-1e** | 真上游 capture 验证 (F-2.5 风格) | 1 codex-day | 低 | F-1c |

**合计 ~6-7 codex-day**, 比 Owner 估的 5-8 day 区间贴近上限.

F-1a 与 F-1d 可在 F-1a 出 spike 后并行 (F-1d 只需 fork API 形状, 不需 connector 完成).

## 4. Per sub-phase 详细

### 4.1 F-1a — http2 fork connector spike

**Scope**: 写一个最小可工作的 `HttpTwoForkClient` (新 file
`proxy_engine/http2_fork_client.rs`), 把已有的 `mimicry::http2_adapter::HttpTwoMimicryAdapter`
从 `tokio::io::duplex` (`http2_adapter.rs:144-145` 的 in-memory 形态) 改为接受
真实的 `T: AsyncRead + AsyncWrite + Unpin + Send + 'static` IO (后续 F-1b 把
BoringTLS stream 喂进来). 输出 typed `SendRequest`-like handle.

**Files**:
- 新建 `crates/core_gateway/src/proxy_engine/http2_fork_client.rs` (~100-200 行)
- edit `crates/core_gateway/src/proxy_engine/mod.rs` (导出新类型, gated 在
  `mimicry-http2-fork` feature)

**Success criteria**:
- 拉一个 in-process mock H2 server (轮转 hyper::server::Builder + tokio TCP), 用
  fork client 连上 + 发 1 request + 拿 200 回 + 关连.
- 测试断言: server side 收到的 SETTINGS frame bytes order 与 profile.h2_settings_order
  逐项匹配; HEADERS frame pseudo-header 顺序按 profile.h2_pseudo_header_order.

**Mutation discriminator** (CLAUDE.md #14):
- "如果把 fork builder 换成 `http::client::Builder::new()` (hyper h2)" → SETTINGS
  order = h2 default (固定 1,3,4,6 顺序) ≠ Anthropic 模板的 wire order → 断言红.
- 现有 `http2_adapter.rs:107` (`http2::client::Builder::new()`) 已经走 fork, mutation
  把它替换为 stock h2 直接红.

**Blast radius**: 0 (新文件, feature-gated, 不动现有 hot path).

### 4.2 F-1b — builder 接入 + L2 typed gate

**Scope**: 在 `proxy_engine/http_client.rs` 加 `try_build_http2_client_with_profile(profile)
-> Result<GatewayHttp2Client, MimicryProductionCanaryError>`, 复用 F-2.3+ round 2
的 typed gate 模式 (`http_client.rs:77-83` 的 `try_build_http_client_with_profile`
shape).

- 新 typed gate `L2HttpPreflightStatus / L2HttpPreflightError` (新 module
  `mimicry/l2_preflight.rs`, 与 `mimicry/l1_preflight.rs` 同结构: `NotRequired
  / Pending { profile_mode } / Passed { profile_mode } / Failed(L2HttpPreflightError)`).
- 复用 `MimicryProductionCanaryError` (避免 F-2.3+ round 1 那种重复错误链路问题,
  见 commit `0d0646e` body).
- `is_dispatchable` 在 L2 上同样拒 Pending (D-S6 设计原则).

**Files**:
- 新建 `crates/core_gateway/src/mimicry/l2_preflight.rs` (~250 行, 镜像 l1_preflight)
- edit `crates/core_gateway/src/proxy_engine/http_client.rs` (add `try_build_http2_client_with_profile`)
- edit `crates/core_gateway/src/mimicry/mod.rs` (export L2 types)

**Success criteria**:
- Anthropic profile → `try_build_http2_client_with_profile` 返 `Ok`
- Codex / Kiro profile → `Err(KnownGap)` (h2 SETTINGS/pseudo-header 当前模板未填或不 match)
- Gemini profile → `Err(KnownGap("L2 preflight Pending for GeminiAdvanced: runtime h2
  byte-byte check not yet wired (F-1d)"))` (Pending fail-closed)

**Mutation discriminator**:
- 删 L2 preflight 调用 → Codex/Kiro/Gemini 都通过 builder → 3 个新测试
  `try_build_http2_client_with_profile_rejects_*` 同时红 (Codex 的 sanity baseline
  断言 dispatch canary 单独不拦 H2, 借鉴 F-2.3+ commit `0d0646e` 的双层断言模式).

**Blast radius**: 中. 新增 typed gate, 但不动 stock-h2 路径; 只有 `mimicry-http2-fork`
build 才编进来.

### 4.3 F-1c — ProxyEngine relay 适配 (最险)

**Scope**: 把 `relay.rs::relay_body` (`relay.rs:120` 起的 `relay_body<B>` 泛型签名)
从 "只接 hyper Response Body" 扩展为 "接 hyper Body 或 fork-h2 Body". 必须保
relay_body 现在的所有不变量 (上游断开分类 / W12-A spool / 第三方 P2 finding 2026-05-24
的"upstream 终态保留" patch in `relay.rs:218-243`).

设计选择 (Owner 拍板 §8):
- 选项 A: 把 fork-h2 Body 用 `Pin<Box<dyn http_body::Body<Data=Bytes, Error=...>>>`
  装箱, relay_body 接 `Box<dyn Body>`. 简单, 性能损失 ~1% (额外 vtable).
- 选项 B: 引入 `GatewayResponseBody` enum 双 variant (Hyper / ForkH2),
  relay_body 接 enum. 0 vtable, 但代码膨胀.

**Files**:
- edit `crates/core_gateway/src/proxy_engine/relay.rs` (扩签名 + 处理新 body 类型)
- 可能新增 `crates/core_gateway/src/proxy_engine/response_body.rs` (选项 B)

**Success criteria**:
- 既有 `proxy_engine::relay::tests::*` 全绿 (3 个测试: `relay_body_uses_configured_upstream_body_idle_timeout`
  / `relay_body_allows_longer_configured_upstream_idle_gap` / `relay_body_stops_when_downstream_write_idle_timeout_elapses`,
  per `relay.rs:706/749/867`).
- 新增 `relay_body_works_with_fork_h2_body` 测试: 跑 in-process mock h2 server
  (复用 F-1a 的 fixture), 用 fork client 发请求收响应, 验证 relay 完整传过 body bytes.
- W12-A spool durable-first 路径 (commit 052cd61) 对 fork-h2 body 也生效 — 测试
  断言 attempt event 包含正确 status.

**Mutation discriminator**:
- "如果 relay 用 stock Body trait 而不引 fork-Body" → fork-h2 测试 fail at compile
  (类型不匹配); 即使能编译, mock h2 server 收不到响应 byte → 红.
- "如果 W12-A spool 路径没接 fork-h2" → attempt event 漏 / status 错 → spool
  对账测试红.

**Blast radius**: **高**. relay 是出站 hot path. 风险: 任何破坏 body 流式分块 / 终态
分类 / spool 接线的改动都会让既有 W12-A / W12-B 测试红. 缓解: F-1c **拆 2 commit**:
- F-1c.1: 引 body 抽象 + 原 hyper 路径切到抽象 (无行为变化, 既有测试不动)
- F-1c.2: 接 fork-h2 path (新测试 + 既有测试全 pass)

### 4.4 F-1d — L2 runtime preflight runner

**Scope**: 实现 `L2HttpPreflightRunner` (`mimicry/l2_preflight.rs::run_profile_preflight`),
让 Gemini / 未来 profile 的 `Pending` 状态能 → `Passed`. Runner 做的事:
1. 用 fork builder 启 in-memory duplex (复用 `http2_adapter.rs::encode_request_exchange`)
2. 抓 SETTINGS frame bytes + HEADERS frame bytes
3. byte-byte 对比 profile expected
4. 全 match → `Passed { profile_mode }`; 任一不 match → `Failed(RuntimeMismatch { detail })`

**Files**:
- edit `crates/core_gateway/src/mimicry/l2_preflight.rs` (add runner)
- 可能 edit `crates/core_gateway/src/proxy_engine/http_client.rs` (在 try_build_http2_client
  内, 把 L1 preflight Pending case 调 L2 runner)

**Success criteria**:
- Anthropic profile → runner 返 `Passed` (build_http2_client_with_profile 通过)
- 测试 mutation: 改 profile.h2_settings_order 第一项 → runner 返
  `Failed(RuntimeMismatch)` → builder 返 Err
- Gemini Pending case 在 F-2.3a wiring 同款逻辑下也能转 Passed (但 Gemini 的 h2 profile
  需先有 capture data; 见 F-1e + F-1-followup)

**Mutation discriminator**:
- 把 runner 改成"无脑返 Passed" → 改 profile.h2_settings_order 的对照测试断言 Passed
  vs Failed 失效 → 红.

**Blast radius**: 中. 新 module, 与 F-1b typed gate 接口耦合; 不动 relay.

### 4.5 F-1e — 真上游 capture 验证

**Scope**: F-2.5 风格的 real-upstream H2 capture. 加 mitmproxy addon 抓真实
Anthropic Claude Code / Codex / Gemini / Kiro CLI 的首个 SETTINGS + HEADERS frame,
diff vs profile templates, 写 status doc.

**Files**:
- 新 `tools/fingerprint-collector/capture_h2_settings.py` (mitmproxy addon, ~80 行)
- 新 `tools/fingerprint-collector/captures/h2-<timestamp>.jsonl` (capture artifact)
- 新 `docs/process/release-readiness/W11-F-F1-status.md` (verdict doc, F-2.5 同款结构)

**Success criteria**:
- Anthropic 真上游 SETTINGS / HEADERS 与模板 byte-byte match → F-1 verdict =
  "Anthropic L2 byte-level 真上游验证通过"
- Codex/Kiro/Gemini 真上游 vs 模板的偏差按 F-2.5 §7 风格写出 (KnownGap 留, 与 F-2.5
  保持平行)

**Mutation**: F-2.5 同款, evidence-doc 不直接 mutate, 替代 = re-run capture 一条命令.

**Blast radius**: 0 (纯工具 + doc).

---

## 5. Risk register

| # | risk | 影响 | 缓解 |
|---|---|---|---|
| **R1** | http2 fork API 不暴露 custom-IO hook → F-1a 卡壳 | 全 F-1 停 | F-1a 先做 spike, 1 day timebox; 卡壳 surface Owner 选 fork-the-fork vs contribute upstream vs 放弃 fork |
| **R2** | fork 维护状态: `0x676e67/http2` 是个人 fork, 长期可用性未知 | dep 供应链 | rev 已 pin (a33b27e); F-1e 后期评估 vendor 化 (拷进 `vendor/http2`) 或换 maintained alt (rquest, reqwest-utils) |
| **R3** | relay 适配让 Anthropic SSE streaming / billing trailer 测试红 | W11/W12 既有保证破 | F-1c.1 + F-1c.2 拆 2 commit; F-1c.1 后跑全 lib test, 红就停 |
| **R4** | fork 的 SettingsOrder/PseudoOrder API 跨真实连接时被 fork 内部 H2 协议层覆盖, 不像 in-memory duplex 那样直透 | F-1d preflight 失效 | F-1a 测试就要在 real connect (mock server) 路径上断言 SETTINGS order, 不能只信 in-memory duplex |
| **R5** | mimicry-boring 与 mimicry-http2-fork 双 feature 组合的 compile path 是 untested matrix | release CI 漏报 | `tools/feature-matrix/verify.sh` 加 `mimicry-boring,mimicry-http2-fork` 组合 (现 `verify.sh` 只跑单 feature) |
| **R6** | Codex CLI 跨平台 native-tls (F-2.5 §7.1 已发现) → 一旦 codex 也走 mimicry-boring + fork, 仍有 platform divergence | byte-match 不 cross-platform | F-1 不试图解决; F-1-followup 接 F-2.5 codex 5th gap 模式, 标 platform_h2_fingerprint_divergence |
| **R7** | 性能回归: 多一层 fork client → fork-h2 vtable / 内存拷贝 | hot-path latency | F-1c 后跑 hdrhistogram bench (复用 dev-dep 已有), p99 增加 > 5% 即回滚 |

---

## 6. Acceptance criteria for whole F-1

**必须全部通过** (任一不过则 F-1 epic 未关):

1. `cargo test -p core_gateway --features mimicry-boring,mimicry-http2-fork --lib` —
   既有 293 tests + 新 F-1a/b/c/d 测试全绿
2. 既有 `cargo test -p core_gateway --features mimicry-boring --lib` (无 fork feature)
   一切照常, 默认 build 也照常
3. 新 byte-level 测试: real-connect (mock server) → SETTINGS frame bytes 逐字节 match
   profile order; HEADERS frame pseudo-header 顺序 match
4. L2 typed gate 测试: Anthropic 通过, Codex/Kiro/Gemini fail-closed (KnownGap reason
   非空且可检索)
5. F-1e status doc 提交, Anthropic verdict 标 "L2 byte-level 真上游验证通过"
6. CLAUDE.md #8 per-commit review: 每 sub-phase 至少 1 轮 codex review 闭环, P1
   findings 全清, P2 在 ≤2 轮内清
7. CLAUDE.md #14 mutation: 每 sub-phase 新测试都过"mentally invert the defect → 红"
   discipline, 至少 F-1a + F-1d 各跑一次 live mutation check (像 F-2.3+ 那样
   实际改代码 + 重跑, 不只是脑补)
8. CLAUDE.md #11 attestation: 每 commit body 含 `Clean-room-attestation:` 行,
   no copied source from 0x676e67/http2 (paraphrase 允许)
9. `tools/feature-matrix/verify.sh` 加 mimicry-boring + mimicry-http2-fork 组合后
   全绿 (R5 缓解)

---

## 7. Time estimate

| sub | nom | 上限 (+50% buffer) | trigger 上限 |
|---|---|---|---|
| F-1a | 1.5 | 2.25 | API spike 卡壳 |
| F-1b | 1.5 | 2.25 | typed gate 边界 case 多 |
| F-1c | 2 | 3 | relay 适配 break 既有测试需多轮回滚 |
| F-1d | 1 | 1.5 | runner profile diff 复杂 |
| F-1e | 1 | 1.5 | capture 触发 / mitmproxy 重装 |
| **合计** | **7** | **10.5** | — |

Owner OD: 估时 5-8 codex-day. 我的 nom 7 day 落区间内偏高; 上限 10.5 day 略超.
若 R1/R3 出险, 触发上限.

---

## 8. Owner decision points

| # | choice | 选项 | Claude 倾向 |
|---|---|---|---|
| **D-F1-A** | sub-phase 串行 vs 半并行 | A1 全串 F-1a→b→c→d→e; A2 F-1a 完后 b+d 并行 | A1. F-1c 是最险 phase, 全员注意力别分散 |
| **D-F1-B** | response body 抽象形态 | B1 `Box<dyn Body>` (简单+1% perf cost); B2 enum 双 variant (0 cost+代码膨胀) | B1 先做, 用 hdrhistogram 测; 真有 perf 红线再切 B2 |
| **D-F1-C** | http2 fork 长期归属 | C1 保持 git-dep pin rev; C2 vendor 进 `exploratory/rust-core-gateway/vendor/http2/`; C3 换 rquest / 其他 maintained | C1 短期 (F-1 内); C2/C3 留 F-1 完后 single follow-up commit 决定 |
| **D-F1-D** | F-1e 真上游 capture 路径 | D1 mitmproxy + CA install (F-2.5 同款, security risk per F-2.5 §8.6); D2 byte-only Python TCP proxy (无 CA, 安全更好但工程多写脚本) | D1 (复用 F-2.5 的 addon, 增量代码少) |
| **D-F1-E** | Codex / Kiro profile h2 字段空白如何处理 | E1 保 KnownGap 不填; E2 用 F-1e capture 补 wire 数据 + 关 KnownGap; E3 视 capture 真实结果再定 | E3. 按数据说话, 不预设结论 |

---

## 9. What could go wrong (3-5 真实 failure mode)

1. **http2 fork 不支持 custom IO 注入** (R1 实化): F-1a spike 发现 fork builder
   只接受 `tokio::net::TcpStream` 或类似具体类型, 而不是 generic `T: AsyncRead+AsyncWrite`.
   后果: 无法把 BoringTLS stream 喂进去, 整个 F-1 链路断. 应对: surface Owner,
   选 fork-the-fork (HUAKAI vendor 化并改 API) vs 换库 vs 接受 mimicry-boring 与
   mimicry-http2-fork 互斥不能合用 (退化方案).

2. **relay 适配让 SSE / billing trailer 测试连锁红** (R3 实化): F-1c 改 relay 签名
   触发既有 W11-D D-6 (header strip) / W12-B D-5 (body usage 解析) / W12-A spool 等
   断言失效. 后果: F-1c 多轮回滚, 时间爆掉上限. 应对: F-1c.1 / F-1c.2 拆 2 commit,
   每步全 lib test, 红就回退.

3. **fork 在 real-connect 上的 SETTINGS / pseudo-header order 与 in-memory duplex
   不一致** (R4 实化): fork 内部 H2 协议层可能在真连接时按 RFC 7540 重写顺序 / 合并
   值. 后果: F-1a in-memory test 绿, 但 F-1e real-upstream capture 显示 byte 不
   match. 应对: F-1a test 必须用 in-process mock server 而非 duplex; duplex test
   保留但加注 "encoder-level only" tag.

4. **mimicry-boring 与 mimicry-http2-fork 双 feature 编译失败** (R5 实化): 两 dep
   都引入 unsafe / FFI / build script, 组合时 link 冲突或 cmake/llvm-config 二次
   触发. 后果: feature-matrix CI 红, 但本地 spike 时可能漏. 应对: F-1a 第一件事
   就是 `cargo check --features mimicry-boring,mimicry-http2-fork`.

5. **Codex CLI 跨平台 native-tls 在 fork 上重现** (R6 实化): F-2.5 已发现 codex
   Windows 用 schannel 不送 template 字段. F-1 把 fork 接上后, 如果用户在 Windows
   build 也开 mimicry-http2-fork, 应该走 BoringSSL + fork 路径, 应该不会重现 schannel
   问题; 但若 mimicry-boring feature 在某 build 关而 mimicry-http2-fork 开, fork 配
   stock TLS 在 Windows 仍可能 schannel. 应对: F-1b L2 typed gate 加 "fork without
   mimicry-boring" cfg 守门, 拒此组合.

---

## 10. Out-of-scope follow-ups

- **F-1-followup-A**: vendor `0x676e67/http2` 进 `exploratory/rust-core-gateway/vendor/http2/`
  (D-F1-C C2 路径), 摆脱 git-dep 不可控性 (~0.5 codex-day)
- **F-1-followup-B**: H2 GREASE frame (RFC 8701) profile 字段 + adapter 支持 (~1 day)
- **F-1-followup-C**: F-1-platform-h2-divergence (F-2.5 §7.1 platform_fingerprint_divergence
  的 L2 对位; 如果 Windows native-tls h2 走出 schannel 路径) (~0.5 day)
- **F-1-followup-D**: F-2.5-Gemini-h2 capture (F-2.5 §7.2 model_api_ht_alpn variant 未触发,
  H2 negotiation 的 gemini 操作 capture 后才能 close) (~0.5 day)
- **F-1-followup-E**: F-2.5-Kiro upstream capture (F-2.5 §7.3, 需 Kiro CLI 真账号)
  (~1 day)
- **F-1-followup-F**: bench harness — hdrhistogram p50/p95/p99 自动跑, regression gate
  (~1 day)

合计 follow-up 约 4 codex-day, 不在 F-1 epic 内承诺.

---

## Self-check (CLAUDE.md #10 plan-trio 完成前自查)

- ✅ 文件名后缀 `-claude.md` (与 codex 平行 draft 区分)
- ✅ 未读任何 codex-side draft 文件 (本文件落盘时 codex dispatch 仍在 bg, 互不见)
- ✅ 每 sub-phase 给了 mutation discriminator 而不是只写"加测试"
- ✅ Risk register 7 项, 每项给缓解 (R1-R7)
- ✅ Owner decision points 5 项 (D-F1-A 至 E), 每项给 Claude 倾向但等 Owner 拍
- ✅ Failure modes 5 个, 全是真实可发生的 (而不是"假设 X 出错")
- ✅ Acceptance criteria 9 条, 含 cargo test / mutation / clean-room / feature-matrix
- ✅ Out-of-scope follow-ups 6 项, 不混进 F-1 承诺
- ⚠️ 时间估 7 day nom / 10.5 day 上限, 比 Owner 给的 5-8 day 偏高 — 这是真实风险, 不缩.
