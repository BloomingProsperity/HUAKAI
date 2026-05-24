# W11-F F-2.1 specifier-dig: 4 profile wire-level 实读 (2026-05-24)

> 实读 4 个 builtin profile JSON template + 对照 OpenSslAdapter / BoringSSL 现支持集 +
> 跨 profile 字段 diff. 此文件是 F-2 plan trio 的 prerequisite — 把 Claude draft / Codex
> draft / synthesis 都建立在真实 wire 字节上, 不空猜.

## §0 Source files read

- `tools/fingerprint-collector/templates/codex-cli.json` (211 lines, full read)
- `tools/fingerprint-collector/templates/kiro-cli.json` (150 lines, full read)
- `tools/fingerprint-collector/templates/gemini-advanced.json` (307 lines, full read)
- `tools/fingerprint-collector/templates/anthropic-claude-code.json` (line 1-30, partial — Anthropic 基线, 不动)
- `crates/core_gateway/src/mimicry/profile.rs` (line 1-220)
- `crates/core_gateway/src/mimicry/tls_profile.rs` (line 1-163 含 codex_cli_known_gap_fields)
- `crates/core_gateway/src/mimicry/openssl_adapter.rs` (line 1-200)

---

## §1 4 个 profile wire-level 对照表

| 字段 | Anthropic | CodexCli (chatgpt.com) | KiroCli (q.us-east-1) | Gemini (cloudcode-pa) |
|---|---|---|---|---|
| **tls_backend (template-declared)** | (未显式, 推测 native-tls/openssl) | **native-tls/openssl** ✅ | **rustls** ⚠️ | **nodejs** ⚠️ (wraps OpenSSL) |
| grease | 未启用 | false | **true** | false |
| extension_order | stable | stable | **randomized** | stable |
| cipher_suites count | 17 | 30 | 10 | **51** |
| cipher 52394 (DHE-CHACHA) | 不含 | **含** ✅ | 不含 | **含** ✅ |
| extensions count | 15 | 11 | 10 | 12 |
| **ext 22 (ETM)** | ❌ 不含 | ✅ 含 | ❌ 不含 | ✅ 含 |
| ext 51 (key_share) | ✅ | ✅ | ✅ | ✅ |
| **supported_groups[0]** | 29 (X25519) | **4588 (X25519MLKEM768 PQ)** | **4588** | **4588** |
| sig_algos count | 9 | 26 | 10 | 26 |
| alpn_protocols | ["http/1.1"] | **[] (无 ALPN)** | [] | ["h2", "http/1.1"] |
| ec_point_formats | [0] | [0, 1, 2] | [0] | [0, 1, 2] |
| key_share_groups | (待读) | [4588, 29] | [4588, 29] | [4588, 29] |
| psk_modes | (待读) | [1] | [1] | [1] |
| sample_count | 5 | 6 | 6 | 6 (含 2 variants) |
| h2_settings.available | (待读) | false | false | false |

**关键发现**:

1. **3 个非-Anthropic profile 走 3 个不同 backend** — 都被 backend_intent 阻断**但 reasons 不同**:
   - CodexCli: native-tls/openssl backend OK, 但 `match_policy() == KnownGapBlocked` 因为硬编码 `mode == ProfileMode::CodexCli` (`profile.rs:164`).
   - KiroCli: `tls_backend = rustls` → `backend_intent()` 返 `UnsupportedTemplate { reason: "tls_backend=rustls is observation-only after D3" }` (`profile.rs:194-199`).
   - GeminiAdvanced: `tls_backend = nodejs` → `backend_intent()` 走 default match arm → `UnsupportedTemplate { reason: "tls_backend=nodejs 尚未声明可用 transport backend" }` (`profile.rs:201-206`).

2. **PQ group 4588 (X25519MLKEM768) 在 3 个 profile 都是首项** — Codex / Kiro / Gemini 都已 post-quantum 时代. **HUAKAI 当前 OpenSslAdapter 是否启用 ML-KEM hybrid group?** 取决于 system OpenSSL version (3.2+ 支持). 这是 F-2 闭环的 cross-cutting blocker — 需 specifier-dig OpenSSL build flags.

3. **ETM ext22 仅 CodexCli + GeminiAdvanced 有** — Anthropic 与 KiroCli 都无. 同步 `openssl_adapter.rs:31` 已 native handle ETM. 但 ja3 中 "Codex/Kiro/Gemini" 一起被 synthesis §4 line 413 标为缺口 — 实际 Kiro 不缺 ETM, 缺的是 backend (rustls) 的根本性 mismatch.

4. **GeminiAdvanced 有 2 个 TLS variants**: model_api_ht_alpn (含 ALPN h2+http/1.1, ext list 12 项, ja4 `t13d5212_ht_*`) 与 auxiliary_no_alpn (无 ALPN, ext list 11 项, ja4 `t13d5211_00_*`). 模型 API 的握手与辅助连接握手不同 — `match_policy() == SampleSetRandomized` 的设计就为此 (`tls_profile.rs:has_sample_set_variants()`).

5. **CodexCli ALPN 为空** — 实际 chatgpt.com 协商 ALPN, 但 Codex CLI client 自己不在 ClientHello 中 advertise. 这是反向常识 (主流 client 都 advertise h2+http/1.1). OpenSSL adapter `apply_alpn()` 必须支持空 ALPN 配置.

---

## §2 缺口分类 (修正 Claude draft §3)

按 **是否本轮 F-2 可闭环** 分类:

### G-A: CodexCli — KnownGap-by-hard-coding 但实际 backend 可走 (可修)
- 原因: `match_policy()` 硬编码 CodexCli == KnownGapBlocked, 但 tls_backend=native-tls/openssl 实际可走 OpenSslAdapter.
- 闭环路径: 
  1. F-2.2 把硬编码改为 per-profile gap-list lookup.
  2. F-2.3 实施 ML-KEM PQ group + cipher 52394 + 关闭 OpenSSL 默认 GREASE 注入 + extension stable order.
- 难度: **中** (需 OpenSSL build 支持 ML-KEM, 需手控 supported_groups 顺序).
- 估时: 1.5-2 codex-day.

### G-B: KiroCli — rustls backend 不可在 OpenSSL 路径精确复刻 (待 Owner 决)
- 原因: Kiro CLI 真实是 Rust + rustls TLS stack. extension_order=randomized, grease=true. **rustls 的 wire bytes 与 OpenSSL 的 wire bytes 系统性不同** — 单独字段闭合不能让 ja3_hash 匹配 (`ed5338278fb7f0fb5cfd4ad58a98241f`).
- 选项:
  - (a) **接受 Kiro 永久 KnownGap**: 标 KnownGapBlocked, 不上 production. 损失 Kiro 流量能力.
  - (b) **走 BoringSSL 字节级 builder**: 绕开 OpenSSL adapter, 直接构造 ClientHello bytes 匹配 rustls wire shape. 需大量 boring_wire.rs 扩展 — 3-5 codex-day.
  - (c) **真 vendor rustls**: 引入 rustls 依赖 + 用 rustls + GREASE 模拟. CLAUDE.md §11 L3 检查: rustls 是 Apache-2.0/MIT — vendor 允许. 但 D3 决定 "rustls is observation-only after D3" — 反 Owner 决.
- **推荐**: (a) 永久 KnownGap; Owner round-2 可决 (b)/(c) 为 roadmap.
- 估时 (路径 a): 0.3 codex-day. (路径 b/c): 单独 plan.

### G-C: GeminiAdvanced — nodejs backend 实际 wraps OpenSSL, 可走 OpenSslAdapter (中等可修)
- 原因: Node.js TLS 底层是 OpenSSL. 字段集与 OpenSSL native 可比. 51 个 cipher 含 52394 + ext22 ETM + 4588 PQ group + 2 variants (h2 ALPN 与 no ALPN).
- 闭环路径:
  1. F-2.2 改 `backend_intent()` 对 TlsBackend::NodeJs 返 OpenSslAdapter (不是 UnsupportedTemplate) — 加备注 "Node.js TLS 底层 = OpenSSL, 字段集兼容".
  2. F-2.3 适配 OpenSslAdapter 接受 51-cipher 列表 (verify apply_cipher_suites 不 hard-limit) + 2 variants 切换 (variant=model_api_ht_alpn 时启 ALPN, 否则空).
- 难度: 中 (variants 路由 + 51 cipher 注入).
- 估时: 1-1.5 codex-day.

### G-D: Anthropic — 已验证, 零回归基线
- 不动. F-2 全程必保 Anthropic profile 测试零回归 (P2-F2-4).

### G-E: ML-KEM PQ group 4588 (跨 Codex/Kiro/Gemini cross-cutting)
- 原因: 3 个非-Anthropic profile 都首项 4588. OpenSSL 3.2+ 支持 ML-KEM hybrid groups, 通过 `SSL_CTX_set1_groups_list("X25519MLKEM768:X25519")` 配置.
- 实施: F-2.3 加 `apply_pq_groups` 或直接扩 `apply_supported_groups` 让首项 4588 通过.
- Blocker: HUAKAI vendor 的 OpenSSL 版本是否 3.2+? 需 cargo.lock / openssl-sys 检查.
- 估时: 0.5 codex-day spike + 0.5 codex-day 实施.

---

## §3 修正 F-2 子计划 (Claude draft §4 修订)

| sub-phase | 内容 | 估时 |
|---|---|---|
| **F-2.1** (本文件) | spec-dig: 4 profile wire 实读 + backend 分类 | 0.5 codex-day ✅ done |
| **F-2.2** | match_policy + backend_intent 改造: per-profile gap lookup + NodeJs → OpenSslAdapter 路径; +Kiro 永久 KnownGap (Owner 决 OD-F2-2) | 0.5 codex-day |
| **F-2.3a** | OpenSSL ML-KEM PQ group 4588 启用 + apply_supported_groups 顺序硬控 | 1 codex-day |
| **F-2.3b** | Codex CLI 闭: cipher 52394 + 关 GREASE + extension stable order | 1 codex-day |
| **F-2.3c** | Gemini 闭: 51-cipher 注入 + 2 variants 路由 (model_api_ht_alpn 启 ALPN) | 1 codex-day |
| **F-2.5** | 真上游 capture 验证 (Codex / Gemini, Kiro 跳过若 OD-F2-2 = a) | 0.5 codex-day |
| **总计** | | **~4.5 codex-day** (synthesis 估 3-4, 修正后偏高 0.5-1) |

**顺序硬约束**: F-2.1 → F-2.2 → F-2.3a (PQ group blocker) → F-2.3b/c (并行) → F-2.5.

---

## §4 决策点 OD-F2-* (修订)

| # | 决策 | Spec-dig 推荐 (代替 Claude draft §7) |
|---|---|---|
| **OD-F2-1** | Kiro 路径 (a/b/c)? | **(a) 永久 KnownGap**: rustls wire shape 不可在 OpenSSL 精确复刻; (b/c) 进 F-3 roadmap |
| **OD-F2-2** | NodeJs backend 是否纳 OpenSslAdapter 路径? | **(a) 是**: Node.js TLS 底层 = OpenSSL, 字段集兼容; backend_intent 加 NodeJs arm |
| **OD-F2-3** | OpenSSL ML-KEM 启用 — vendor build 版本检查 | **(a) 先 spike** verify openssl-sys 接 OpenSSL 3.2+; 若 < 3.2 → 推 vendor 升级或永久 KnownGap |
| **OD-F2-4** | Canary 阻断 — F-2 完成后是否 unblock Codex/Gemini canary? | **(b) 不**: 还需 F-1 + 真上游 capture 双满足 (synthesis §4 line 414) |
| **OD-F2-5** | F-2 完成是否立即 advance F-1? | **(a) 是**: F-1 是 canary unblock 另一半 |

---

## §5 风险修正

- **R-spec-1** OpenSSL vendor build 不支持 ML-KEM → F-2.3a 退化 — 全部 3 个 profile 都阻断. **缓解**: F-2.3a 先 spike (1h) verify, 失败立刻 surface Owner 决 OD-F2-3 升级 vendor or 永久 3 profile KnownGap.
- **R-spec-2** Gemini 2 variants 实际握手如何选择? 当前 `has_sample_set_variants()` 返 true 但 OpenSslAdapter 是否能动态选 variant 应用? 需 specifier-dig openssl_adapter.rs 的 variant 处理 (未读 line 200+).
- **R-spec-3** Codex CLI ALPN 空在 OpenSSL adapter 是否支持? `apply_alpn` (`openssl_adapter.rs:134`) 需读 — 若 hard 要 non-empty, Codex 闭环失败.

---

## §6 与 F-2 plan trio 的关系

- 本 spec-dig 文件: F-2.1 deliverable (虽然按 Phase 顺序应在 plan synthesis 之后 commit). **写完置入 plan trio 一起 surface Owner**, 让 Owner 在 synthesis OD-F2-* 决策时手上有 wire 数据.
- 不修 Claude draft (`...-claude.md`) — CLAUDE.md #10 平行稿纪律. Codex draft (`...-codex.md`) 独立写, 不读本文件 (但本文件先到, 若 Codex 也独立得出相同发现, 双重验证).
- Synthesis 文件 (`...-synthesis.md`) 是合成: Claude 草稿 + Codex 草稿 + 本 spec-dig + Owner 决策 OD-F2-* 后的最终执行 spec.

**Lane**: F-2 specifier-dig (实读).
**Agent**: Claude (HUAKAI 自家代码 + JSON 模板, 无 clean-room 限制).
**Timestamp**: 2026-05-24 UTC.
