# W11-F F-2 指纹 L1 TLS 缺口闭环 — Claude plan draft (2026-05-24)

> CLAUDE.md #10 parallel-draft: 本文件由 Claude 独立产出, 不读 Codex draft
> (`2026-05-24-w11f-f2-fingerprint-l1-codex.md` 并行写入). 二稿合成后产
> `2026-05-24-w11f-f2-fingerprint-l1-synthesis.md`.
>
> **范围**: HUAKAI Rust 数据面 mimicry 模块为 Codex CLI / Kiro CLI / Gemini Advanced
> 三个 builtin profile 闭合 L1 TLS ClientHello 字段缺口, 让 backend_intent() 从
> KnownGapBlocked / UnsupportedTemplate 升级到可实际 dispatch 的 OpenSslAdapter
> (或 BoringSSL 路径). Anthropic profile L1 已字节级验证 — 参考实现.

---

## §0 上游决策回顾 (F-2 的边界)

来源 `docs/process/plans/2026-05-22-rust-hardening-plan.md` §4 + Owner 2026-05-23 决:

1. **F-3 进 roadmap**: profile 模型从 JSON 模板升级到字段级分解数据库实体 — 不在本轮.
2. **F-1 单独 plan**: L2 HTTP/2 fork 接 ProxyEngine — 不在本轮 (估 5-8 codex-day).
3. **F-2 范围**: 仅 L1 TLS ClientHello 字段闭合; ETM ext22 / JA4 / supported_groups / signature_algorithms 等可枚举 wire-level 字段. 估 3-4 codex-day.
4. **Canary 阻断 (§4 Canary 策略)**: L1-only 通过但 L2 缺 capture → 禁上 production / canary. F-2 闭环不解锁 canary; 必须 + F-1 L2 接线 + 真上游 capture 双满足.
5. **判别性测试纪律 (synthesis §4)**: F-2 测试必须 "捕获 ClientHello 字节, 对 profile 涵盖的 JA3/JA4 相关字段断言; 缺口字段闭合后断言特定扩展存在且位置正确". Mutation: 回退缺口修复 → 字段存在性断言红.

---

## §1 范围 + 不变量

### §1.1 必修
- Codex CLI profile: 4 项已记录 gap (cipher_suites 52394 / extensions ETM ext22 顺序 / supported_groups 4588 first / signature_algorithms 26 ids) — 见 `tls_profile.rs:136-163 codex_cli_known_gap_fields()`.
- Kiro CLI profile: backend_intent 现路由到 OpenSslAdapter, 但 Anthropic 之外是否真有 wire-level 缺口需 spec dig (synthesis §4 line 413 说 "Codex/Kiro/Gemini profile 同理需 F-2 缺口闭环").
- Gemini Advanced profile: 同上.

### §1.2 不动
- Anthropic Claude Code profile: L1 字节级已验证 (synthesis §4 line 380, line 412). 现 backend_intent → OpenSslAdapter, fixture 在 `mimicry/profiles/anthropic_claude_code.json`. **绝对不能因 F-2 改动行为变化**. Phase 2A 期间 anthropic 测试是回归基线, F-2 必保零回归.

### §1.3 必守的不变量
- A4 + B-R4 (Phase 1/2): raw credential 永不入 mimicry 任何 wire fixture / Debug / log.
- Phase 1/2 全部 7+5 守门测试 (A1 / A3 / A4 / A5 / D-9 / D-11 / D-12 / 5 reconcile 场景) 在 F-2 改动后零回归.
- D-10 (mimicry resolver bypass): backend_resolver 必须先调 backend_intent 后看 feature, F-2 闭环不能 reintroduce bypass.

---

## §2 现状评估

### §2.1 builtin profiles 状态对照表

| Profile | mode | backend (template) | match_policy() | backend_intent() | gap count | L1 验证 |
|---|---|---|---|---|---|---|
| AnthropicClaudeCode | anthropic-claude-code | NativeTlsOpenSsl | ExactStable / SampleSetRandomized (depends on variants) | OpenSslAdapter | 0 | ✅ 字节级 |
| CodexCli | openai_codex_cli | (template-declared, 多半 native-tls/openssl) | **KnownGapBlocked** (硬编码 mode == CodexCli) | **KnownGapBlocked { reason }** | 4 (cipher / ext / groups / sigalg) | ❌ 阻断 |
| KiroCli | kiro_cli | TBD (specifier-dig 需读 kiro-cli.json) | ExactStable / SampleSetRandomized (推测) | OpenSslAdapter (推测) | 0 (未声明) | ❓ 未验证 |
| GeminiAdvanced | gemini_advanced | TBD | ExactStable / SampleSetRandomized (推测) | OpenSslAdapter (推测) | 0 (未声明) | ❓ 未验证 |

**关键发现**: profile.rs:163-171 的 `match_policy()` 当前**只硬编码 CodexCli 为 KnownGapBlocked**:
```rust
pub fn match_policy(&self) -> ProfileMatchPolicy {
    if self.mode == ProfileMode::CodexCli {
        ProfileMatchPolicy::KnownGapBlocked
    } else if self.tls.has_sample_set_variants() {
        ProfileMatchPolicy::SampleSetRandomized
    } else {
        ProfileMatchPolicy::ExactStable
    }
}
```
即 Kiro / Gemini 若 JSON 模板声明 `tls_backend=native-tls/openssl`, **会被 backend_intent 误认为可 dispatch**, 但实际可能也有 wire-level 缺口 — 现行代码**不阻断**. 这是潜在 fail-open: Kiro/Gemini 可能在生产被允许走 OpenSslAdapter 但 ClientHello 字节与目标 profile 不一致, 比纯诚实客户端更可疑.

### §2.2 OpenSslAdapter 已实现字段
`openssl_adapter.rs:32-34` 现有 `OPENSSL_NATIVE_EXTENSION_IDS: &[u16] = &[0, 10, 11, 13, 16, 21, 22, 23, 35, 43, 45, 51, 65281]`:
- ext 0 (SNI), 10 (supported_groups), 11 (ec_point_formats), 13 (signature_algorithms), 16 (ALPN), 21 (padding), 22 (encrypt_then_mac), 23 (extended_master_secret), 35 (session_ticket), 43 (supported_versions), 45 (psk_key_exchange_modes), 51 (key_share), 65281 (renegotiation_info)

**ETM ext22 已 native 处理** (`OPENSSL_NATIVE_ENCRYPT_THEN_MAC_EXTENSION: u16 = 22`).

`from_profile_builder()` 调用顺序 (line 132-138):
- apply_cipher_suites
- apply_alpn
- apply_supported_groups
- apply_signature_algorithms
- apply_ec_point_formats
- apply_extensions (返 ClientHelloProfileOptions)
- run_profile_preflight (capture 真握手 ClientHello bytes 比对 profile)

Preflight 会捕 wire ClientHello + diff 模板期望 → 不匹配返 `PreflightFailed`. 即 OpenSslAdapter **构造时已 fail-fast on profile mismatch** — 但只覆盖 OpenSSL 已支持的字段集.

### §2.3 BoringSSL backend
`boring_wire.rs` + `mimicry-boring` feature 提供 BoringSSL 字节级 ClientHello builder, Anthropic profile 走这条路. 当前 backend_intent 在 BoringSSL 路径不主动检 gap — 完全依赖 boring_wire.rs 内部.

Codex CLI 的 cipher 52394 (TLS_DHE_RSA_WITH_CHACHA20_POLY1305_SHA256) 在 BoringSSL **不支持** (BoringSSL 砍掉了 DHE 套件家族, 历史决定). 这是 KnownGap 不可绕开的根本原因.

---

## §3 缺口分类

按 **可修闭环路径** 分类:

### G-A ETM ext22 在 BoringSSL 路径 (可能为 Kiro/Gemini)
- 现状: OpenSSL 已 native 处理 (line 31). BoringSSL 是否同样 native 处理 ETM? 需 specifier dig boring_wire.rs.
- 难度: 低 (若 BoringSSL 已支持, 仅需 wire layer 注入); 中 (若需 fork patch).

### G-B JA4 衍生算法稳定性 (所有 profile)
- 现状: profile.rs `ja4_stable_prefix` 字段已有, 但**没有 builder 验证逻辑** — JA4 应是 ClientHello bytes 的衍生哈希, 不是独立要 mimic 的字段.
- 含义: F-2 工作不是 "implement JA4 输出", 而是 "保证 ClientHello bytes 一致 → JA4 自动一致".
- 难度: 0 (若其它字段都对, JA4 自动对); 验证测试需 wire capture 后跑 JA4 计算并断言匹配.

### G-C supported_groups 顺序 (Codex CLI specific)
- 现状: Codex CLI 模板 `supported_groups` starts with 4588 (X25519MLKEM768, post-quantum); BoringSSL spike capture starts with 65073 (GREASE).
- 难度: 中. BoringSSL 在生产版本是否支持 X25519MLKEM768? 时间线: BoringSSL 2025 中后期开始支持. **若启用 PQ-experiments build flag**, BoringSSL 可输出 4588. 需 specifier-dig BoringSSL 当前 vendor commit + 是否 PQ 启用.

### G-D signature_algorithms 26 ids 完整性 (Codex CLI)
- 现状: Codex CLI 模板 26 个 sigalg IDs, OpenSSL public API 子集可输出 (例如 RSA-PSS 系列). 完整精确复刻需 patched build 或 wire-level 字段重写.
- 难度: 高. 可能需要把 ClientHello bytes 重写而非通过 SSL_CTX API.

### G-E cipher_suites 52394 (DHE-CHACHA, Codex CLI 独占)
- 现状: BoringSSL 砍掉 DHE — **永远不可能**通过 BoringSSL 闭环. 必须走 OpenSSL backend.
- 难度: 不阻塞 F-2 (OpenSSL 已支持 cipher 52394? 需 spec-dig); **架构决定**: Codex CLI profile **必须用 OpenSSL adapter, 不能用 BoringSSL 路径**.

### G-F extension order (所有 profile 但通常细粒度)
- 现状: profile.rs `extension_order` 标 Stable / Randomized; `extensions` Vec<u16> 列顺序. 当前 OpenSslAdapter 通过 `apply_extensions` + preflight diff 验证; BoringSSL 走 client_hello_builder.rs 字节级.
- 难度: 低 (若已通过 preflight); 中 (若 builder 重写顺序需 fork patch).

---

## §4 子计划 sub-phases

### F-2.1 Specifier-dig: Kiro/Gemini 真实缺口枚举 + JSON template 实读 (0.5 codex-day)
TDD-spike. **必须先做**, 否则 §2.1 推测部分不可信:
1. 读 `tools/fingerprint-collector/templates/kiro-cli.json` + `gemini-advanced.json` 实拉 wire 字段
2. 对照 OpenSslAdapter 已支持集 / BoringSSL builder 字节范围 → 列实际缺口
3. 拿 capture fixture 跑现 OpenSslAdapter 的 preflight, 看是否 PreflightFailed (现 fail-fast 在哪里, 测试集是否覆盖 Kiro/Gemini)
4. 产出 `docs/process/plans/2026-05-24-w11f-f2-kiro-gemini-spec-dig.md` 含每个 profile 的 wire 差异 + 缓解方案 (用现有 OpenSSL adapter / patch / fork wreq)

### F-2.2 KnownGap detection 扩展 (Kiro/Gemini 不再 silently dispatch) (0.5 codex-day)
现在 `match_policy()` 只硬编码 CodexCli 为 KnownGapBlocked. 改造:
1. 把 `codex_cli_known_gap_fields()` 拆为 per-profile 函数 (codex_cli_/kiro_cli_/gemini_advanced_) + per-profile 真实 gap list.
2. `known_gap_fields()` 据 mode dispatch.
3. `match_policy()` 改: 任一 profile 若有 non-empty gap fields → KnownGapBlocked.
4. 测试: 加 unit test 对每个 profile 验证 backend_intent 的预期 (Anthropic → OpenSslAdapter; Codex → KnownGapBlocked; Kiro/Gemini → 视 F-2.1 spec dig 结论).
**这是失败保护**: 在 F-2.3-N 真闭环前, 拒绝 silently dispatch Kiro/Gemini 到不匹配的 adapter (CLAUDE.md "Feature Preservation Rule" + 安全 default).

### F-2.3 Codex CLI cipher_suites 52394 + supported_groups 4588 启用 (1.5 codex-day)
- 路径 A: 走 OpenSSL adapter; 通过 cipher_suites 配置启用 52394 (DHE-CHACHA, OpenSSL 支持). supported_groups 顺序前置 4588 (X25519MLKEM768 — OpenSSL 3.2+ 支持 ML-KEM hybrid groups, 但需确认 vendor build). 若 OpenSSL build 不支持 ML-KEM, 走路径 B.
- 路径 B (fallback): mark Codex CLI 永久 KnownGapBlocked, 不闭 — 但这降级 product 能力, 需 Owner 决.
- 测试:
  - Real handshake against test fixture server with profile-conforming verifier
  - Capture ClientHello bytes; assert cipher_suites contains 52394 at position N
  - Mutation: comment out the cipher_suites配置 → 红
- 估时: 1 codex-day 实施 + 0.5 codex-day Owner decision + spike 验证 OpenSSL ML-KEM.

### F-2.4 ETM ext22 wire-level 在 BoringSSL 路径 (按 F-2.1 结论决) (0.5 codex-day)
- 若 F-2.1 标 Kiro/Gemini 走 BoringSSL 且 ETM ext22 缺 → 通过 boring_wire.rs 在 ClientHello bytes 注入 ext22.
- 否则: skip; 落 Owner decision 决.

### F-2.5 真上游 capture 验证 (per profile) (0.5 codex-day)
- 对 Codex/Kiro/Gemini 三 profile 各跑一次 staging upstream capture (不算 production canary, 是 internal smoke).
- 把 capture bytes diff profile template; 任一字段不匹配 → 留 spec issue, 不上 canary.
- 这是 Canary 策略 (synthesis §4) 的 prerequisite, F-2 完成的标志.

**总计估时**: 3.5 codex-day (vs synthesis 估 3-4). 接 F-1 后才能上 canary.

---

## §5 接受门 (acceptance gates) P2-F2-*

### P2-F2-1 KnownGap 扩展正确性 (F-2.2 守门)
Fixture: 4 个 builtin profile load → backend_intent 返:
- Anthropic → OpenSslAdapter (零回归 baseline)
- Codex → KnownGapBlocked (无变化)
- Kiro → 视 F-2.1 结论 (KnownGapBlocked / OpenSslAdapter)
- Gemini → 视 F-2.1 结论

Mutation: 把 `match_policy()` 改回只硬编码 CodexCli → Kiro/Gemini 若 spec dig 标 gap 的测试红.

### P2-F2-2 Codex CLI 闭环 — 真 handshake (F-2.3 守门)
Fixture: OpenSslMimicryAdapter::new_with_profile(codex_cli_profile) → connect against test upstream → capture ClientHello → diff 模板.
- cipher_suites 必含 52394
- supported_groups[0] 必是 4588 (post-quantum first)
- ETM ext22 在 extensions 列表里

Mutation: 注释 apply_cipher_suites 中 52394 注入 → 测试红.

### P2-F2-3 真上游 capture 同步 (F-2.5 守门, gated)
对 Codex/Kiro/Gemini 三 profile, staging 上游 capture bytes 经 mimicry-handshake-tracer 提取后, diff 模板 ja3_hash + ja4 + extensions list 一致.
- Mutation: 把 capture 字节随机篡改 1 bit → diff 红 (这是 fixture 自身完整性测试).

### P2-F2-4 Anthropic 零回归 (F-2 全程守门)
所有 F-2 sub-phase commit 后, Anthropic profile 现有 mimicry test 集 (`anthropic_test.rs` + `mimicry_dispatch_test.rs`) 必须全绿. 任一红 → 立刻 revert.

### P2-F2-5 D-10 不退化 (F-2 全程)
backend_resolver 在 mimicry-boring feature 编译时, 仍必须先调 backend_intent 后看 feature (D-10 守门).
F-2.2 扩 KnownGap 后, Kiro/Gemini 若 KnownGap 必返 BlockKnownGap, 不 silently dispatch.

---

## §6 文件触点

### Create
```
docs/process/plans/2026-05-24-w11f-f2-kiro-gemini-spec-dig.md   F-2.1 输出
```

### Modify
```
exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs
  - match_policy() 移 hard-coded CodexCli → 通用 per-profile gap fields lookup
  - known_gap_fields() 据 mode dispatch
  
exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs
  - codex_cli_known_gap_fields()  保持 (现 4 gap)
  - +kiro_cli_known_gap_fields() / gemini_advanced_known_gap_fields()  (按 F-2.1 结论)
  
exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs
  - apply_cipher_suites: 显式启用 52394 (DHE-CHACHA) 若 profile 含此 cipher
  - apply_supported_groups: 把 4588 (X25519MLKEM768) 前置若 profile 模板首项是 PQ group
  - (可能) +apply_pq_groups helper if OpenSSL ML-KEM API 区分
  
exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mimicry_dispatch_test.rs
  - 加 per-profile backend_intent test (4 个 profile × 1 assert = 4 new test)
  - 加 Codex CLI 真 handshake capture test (TODO mark, 待 F-2.3 实施)
```

### 冻结包 (不可改)
- `backend/internal/{gatewayhttp,gateway,proto}` — Go 线, 与 F-2 无关
- `src/mimicry/anthropic_test.rs` — Anthropic baseline, F-2 不动

---

## §7 决策点 OD-F2-*

| # | 决策 | 推荐 | 备注 |
|---|---|---|---|
| **OD-F2-1** | Kiro/Gemini profile 现状 — 是否 silently 走 OpenSslAdapter (现行) 还是先 KnownGapBlocked? | 推荐: **KnownGapBlocked 默认** until F-2.1 spec-dig 证明 wire 一致 | 安全 default; 不让 fail-open 沉默 |
| **OD-F2-2** | Codex CLI cipher_suites 52394 是否值得修? | 推荐 **(a) 修**: 这是 Codex CLI 区别于其它的关键 cipher; 不修则 Codex 永远 KnownGapBlocked | 估 1 codex-day; OpenSSL 已支持 |
| **OD-F2-3** | OpenSSL ML-KEM (X25519MLKEM768 = group 4588) 是否启用? | 推荐 **(a) 检查 vendor build**; 启用; fallback 是 path B 永久 KnownGap | 取决于 system OpenSSL version (3.2+); 若 vendor 不支持需 patched build → 大幅膨胀 |
| **OD-F2-4** | F-2.5 真上游 capture 验证, staging 环境是否就绪? | 推荐 **(a) 启动 staging 配置**; 否则 Canary 阻断 (synthesis §4) 永生效 | 跨基础设施事 — 可能需 ops 协调 |
| **OD-F2-5** | F-2 完成后是否 advance 到 F-1 (L2 接线)? | 推荐 **(a) 是**: F-1 是 Canary 解锁的另一半 prerequisite | 接下来 5-8 codex-day |
| **OD-F2-6** | sub2api `tls_fingerprint_profile.go:19-100` (LGPL-3.0) 的字段级分解模型是否要 paraphrase 借鉴? | 推荐 **(b) 否**: 进 F-3 roadmap, F-2 不碰 | F-3 进 roadmap, 不在本轮 |

---

## §8 引证表 (fusion-upgrade per CLAUDE.md #12)

| 借鉴源 | repo@SHA | 模式 | HUAKAI delta | 维度 |
|---|---|---|---|---|
| **sub2api** | `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/pkg/tlsfingerprint/dialer.go:361-364` | Go 端 LGPL 实现, 把 TLS profile 存 db, fork uTLS 渲染 ClientHello; GREASE 一等概念 | HUAKAI delta: Rust 数据面字节级 ClientHello builder + 编译期 profile (F-3 roadmap 提升到字段级 db) | 架构 + 算法 |
| **utls** | `refraction-networking/utls` (BSD-3-Clause, 90 天 recency 待 first-cite 验证) | Go 端 fork 1:1 ClientHello bytes, 包括 ALPN/extension order/cipher 等 | HUAKAI Rust port 同思路, 但用 BoringSSL/OpenSSL native API 而非自己 fork TLS | 算法 |
| **cliproxyapi** | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14` | uTLS 造 TLS ClientHello, **但** HTTP/2 走 Go 原生 stack (synthesis §4 line 385) | F-2 范围只覆盖 L1, 与 cliproxyapi 一致; F-1 在 L2 上突破其能力 | 算法 |
| **BoringSSL** | `google/boringssl` (Apache-2.0, MIT-vendor 允许 per CLAUDE.md §12 permitted-license) | BoringSSL ClientHello PQ groups (X25519MLKEM768 group 4588) 自 2025 H2 开始支持 | HUAKAI: 利用现成 PQ API 复刻 Codex CLI 模板; 不 vendor BoringSSL 代码, 通过 boring crate 调用 | 算法 + 生态 |

First-cite recency check (CLAUDE.md #12 90 天):
- sub2api: pushed 2025-02-21 → **stale**, 但是 LGPL paraphrase reference, 不 vendor, 引证仍有效
- cliproxyapi: pushed 2026-05-21 → ✅ 29 天内
- utls: 待 specifier-dig 拉 fresh SHA
- BoringSSL: 待 specifier-dig 拉 fresh SHA + PQ group support commit

Specifier follow-up needed: utls + BoringSSL 真实 recency + PQ group support evidence.

---

## §9 失败模式 + 风险

- **R-F2-1** OpenSSL build 不支持 ML-KEM (cipher 52394 OK 但 group 4588 不出) → F-2.3 退化. **缓解**: 路径 B 永久 Codex CLI KnownGapBlocked. 接受能力降级, 不破坏其它 profile.
- **R-F2-2** F-2.1 specifier-dig 发现 Kiro/Gemini 实际无 wire 差异 (我们假设有) → F-2.2 改动 over-engineering. **缓解**: 不预先实现 kiro_cli_/gemini_advanced_known_gap_fields(), F-2.1 dig 出 gap 才加.
- **R-F2-3** OpenSSL adapter preflight 当前 fail-fast 范围未覆盖 PQ groups → F-2.3 注入 cipher_suites 52394 后 preflight 误报 unexpected_extension. **缓解**: F-2.3 一起更新 preflight 期望集.
- **R-F2-4** Anthropic profile 字节级测试集脆弱 (anthropic_test.rs) — F-2 改 apply_supported_groups 可能让 Anthropic profile group 顺序漂移 → 红. **缓解**: F-2.3 实施前先跑 Anthropic 现 cargo test --features mimicry-openssl 拉 baseline; 改后 diff.
- **R-F2-5** mimicry-openssl 现有 8 个 pre-existing test failure (backlog) — F-2 改动后是否会回升或解锁? **缓解**: F-2.1 先核 Anthropic + 验证 8 个 pre-existing 失败是否仍仅限 native_tls/openssl ec_point_formats / encrypt_then_mac 路径 (mimicry_openssl_adapter_test).

---

## §10 估时 + commit sequence

| sub-phase | commit | 估时 |
|---|---|---|
| F-2.1 specifier-dig Kiro/Gemini | 1 commit (spec doc only) | 0.5 codex-day |
| F-2.2 KnownGap extend | 1 commit (profile.rs + tls_profile.rs + tests) | 0.5 codex-day |
| F-2.3 Codex CLI cipher/groups 闭 | 1 commit (openssl_adapter.rs + handshake test) | 1.5 codex-day |
| F-2.4 ETM ext22 BoringSSL (按 F-2.1 决) | 0-1 commit | 0-0.5 codex-day |
| F-2.5 真上游 capture (per-profile) | 1 commit (test infra + fixture) | 0.5 codex-day |
| **总计** | **4-5 commit** | **~3 codex-day** |

**顺序**: F-2.1 → F-2.2 → F-2.3 → F-2.4 (optional) → F-2.5. F-2.1 是 prerequisite; F-2.5 是 prerequisite for F-1 Canary unblock.

---

**Clean-room-attestation**: original HUAKAI design draft; references to non-HUAKAI projects are evidence-pointers only, no source/comments/tests copied. sub2api LGPL: paraphrase-only per CLAUDE.md §11. BoringSSL Apache-2.0: function-level API call only (not vendoring source).

**Source files read by Claude (this draft)**:
- `docs/process/plans/2026-05-22-rust-hardening-plan.md` §4 (lines 370-417)
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs` (lines 1-220, partial)
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs` (lines 1-163)
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs` (lines 1-200)
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/` Glob (file list confirmation)
- `Wei-Shaw/sub2api@f59d9a5f:backend/internal/pkg/tlsfingerprint/dialer.go:361-364` (cited via synthesis §4 paraphrase, 不本会话 fresh-read)
- `router-for-me/CLIProxyAPI@21fad9db:sdk/api/handlers/request_body.go:14` (cited via synthesis §4 paraphrase)

**Specifier-dig follow-up needed in F-2.1**:
- `tools/fingerprint-collector/templates/kiro-cli.json` + `gemini-advanced.json` deep read
- BoringSSL vendor commit + PQ group support evidence
- utls fresh recency + PQ support
- Codex CLI 模板 cipher_suites / supported_groups raw bytes
- Anthropic profile 字节级测试 (anthropic_test.rs) baseline diff

**Lane**: F-2 specifier (planning, not implementation).
**Agent**: Claude.
**Timestamp**: 2026-05-24 UTC.
