# W11-F F-2 指纹 L1 TLS 缺口闭环 — synthesis (2026-05-24)

> CLAUDE.md #10 双稿合成:
> - Claude draft: `2026-05-24-w11f-f2-fingerprint-l1-claude.md` — per-profile wire 闭环视角
> - Codex draft: `2026-05-24-w11-f-f2-l1-tls-gap-closure-codex.md` — L1 preflight gate 视角
> - Spec-dig 附件: `2026-05-24-w11f-f2-kiro-gemini-spec-dig.md` — 实读 4 profile JSON wire-level 数据
>
> **结论**: 两稿互补, ~30% 共识 + 显著的角度差异. Codex 强调"门禁不可绕开 + 一致 L1 preflight 抽象"; Claude
> 强调"具体哪些字段缺口怎么修 + 3 个 backend mismatch 的命运"; 两个角度合起来才是完整 F-2.

---

## §0 双稿独立性 + 共识度

- **Claude 稿**: 实读 mimicry/profile.rs / tls_profile.rs / openssl_adapter.rs + 2026-05-22 synthesis §4; 重点是"per-profile 缺口分类 + sub-phases F-2.1..F-2.5". Spec-dig 后续追写, 含 4 profile JSON 实读 + backend 命运分类.
- **Codex 稿**: 实读 mimicry/* 全部 .rs + tests/mimicry_*_test.rs + tools/feature-matrix/verify.sh + tools/recapture/RUNBOOK.md; 重点是"production dispatch gate 不可绕开 + L1 preflight 抽象 + D-F2-1..5 决策门". 未读 JSON 模板, 不知 backend 实际分布.
- **共识度**: ~30%. Codex 强决策门 + 防 fail-open + 强测试纪律; Claude 强 wire-level 实现 + per-profile 命运决策. 几乎正交.

---

## §1 Spec-dig 关键发现 (合成最权威基础, 来自 Claude spec-dig 文件)

**实读 4 profile JSON** 后修正既有假设:

| Profile | tls_backend (template) | 当前 backend_intent() 结果 | F-2 闭环路径 |
|---|---|---|---|
| Anthropic | (未显式, 推测 native-tls/openssl) | OpenSslAdapter (现) | **不动 — 零回归基线** |
| CodexCli | native-tls/openssl | **KnownGapBlocked** (硬编码 mode == CodexCli) | 改 match_policy + 启 ML-KEM + cipher 52394 + 关 GREASE |
| KiroCli | **rustls** | **UnsupportedTemplate** ("rustls is observation-only after D3") | **永久 KnownGap** (rustls wire shape 不可在 OpenSSL 精确复刻) |
| GeminiAdvanced | **nodejs** (wraps OpenSSL) | **UnsupportedTemplate** ("尚未声明可用 transport backend") | 改 backend_intent NodeJs arm → OpenSslAdapter + 51 cipher + variants 路由 |

**Critical**:
- **3 个非-Anthropic profile 都首项 4588 (X25519MLKEM768 PQ)** → ML-KEM 启用是 cross-cutting blocker.
- **ETM ext22 仅 CodexCli + GeminiAdvanced 有** (Kiro / Anthropic 无).
- **Codex CLI ALPN 为空** (反直觉, OpenSSL adapter `apply_alpn` 需支持空).
- **Gemini 有 2 variants** (model_api_ht_alpn 启 h2/http1.1 ALPN, auxiliary_no_alpn 空) — `has_sample_set_variants()` 已识别但运行时切换待 specifier-dig.

---

## §2 双稿强一致 (synthesis 直接采纳)

### §2.1 范围共识
- 仅 L1 TLS 改造; F-1 L2 H2 接线单独 plan.
- F-3 profile 模型升级进 roadmap, 本轮不动.
- Anthropic profile 零回归基线, 全程守门.
- D-10 (mimicry resolver bypass) 不退化.

### §2.2 Canary 阻断政策 (Owner 2026-05-23 已决)
F-2 完成不解锁 production / canary. 必须 + F-1 L2 接线 + 真上游 capture 双满足.

### §2.3 测试纪律 (CLAUDE.md #14 + Codex preflight 强制)
- 每测必有 mutation check 注释.
- 真握手 / 真连接验证, 非 unit stub.
- L2-incomplete profile 在 production 必 fail-closed.
- Known-gap profile 不删, 保 Feature Flag off / Mandatory Roadmap.

### §2.4 文件冻结
- backend/internal/{gatewayhttp,gateway,proto} — 不动.
- 新文件可加 (mimicry/l1_preflight.rs Codex 提出, 同意).

---

## §3 Codex gap (Claude 漏的) — synthesis 全部采纳

| # | Codex 抓到 | Claude 漏的 原因 |
|---|---|---|
| **G-COX-1** | **L1 preflight 抽象**: `L1TlsPreflightStatus` 与 `L1TlsPreflightError` 统一类型, backend-specific runners 后跟 feature gate; 新文件 `mimicry/l1_preflight.rs`. | Claude 只想到 per-profile 改 OpenSslAdapter, 未识别"集中 preflight 入口"对未来扩展的杠杆 |
| **G-COX-2** | **production dispatch 必须先调 preflight, 不能凭 policy decision 放行**. 即 `try_build_http_client_with_profile` fallible builder, 强测试. | Claude 假设 OpenSslAdapter::new_with_profile 已含 preflight (line 146 调 run_profile_preflight), 但未在 dispatch 路径加显式 gate |
| **G-COX-3** | **D-F2-3 L1-only production status**: 即使 F-2 闭环, L2 incomplete profile 仍 production 阻断. 不准 "L1 通过就放" 旁路. | Claude 已默认 Canary 阻断政策, 但未显式说 dispatch 路径加 L2-state-check assertion |
| **G-COX-4** | **D-F2-4 capture recency**: stale 但 stable 的 HUAKAI 自家 capture 是否准产? 建议 require recapture 或 Owner waiver. | Claude 完全没考虑 capture 数据老化 |
| **G-COX-5** | **Feature Preservation Mapping 表**: 每个 profile 状态对应 outcome (Implemented / Safe Equivalent / Mandatory Roadmap / Feature Flag off). | Claude 提了 Kiro KnownGap 但未做整张状态表 |
| **G-COX-6** | **Verification commands**: 完整 cargo test matrix + clippy + feature-matrix verify.sh + codex review. | Claude 没列具体命令 |
| **G-COX-7** | **运维 docs**: tools/recapture/RUNBOOK.md + tools/feature-matrix/README.md 若 gate 语义变化必须同步更新. | Claude 没识别 docs 触点 |

---

## §4 Claude gap (Codex 漏的) — synthesis 全部采纳

| # | Claude 抓到 | Codex 漏的 原因 |
|---|---|---|
| **G-CLD-1** | **每个 profile 的 wire-level 真实字段集** (cipher 52394 / 4588 PQ group / ETM ext22 / sigalg 26 ids / ALPN 空) — 来自 spec-dig JSON 实读. | Codex 未读 JSON templates, 不知 backend 分布 + 字段缺口实际是什么 |
| **G-CLD-2** | **3 个非-Anthropic profile 走 3 个不同 backend** (openssl / rustls / nodejs) — backend_intent 当前对每个返不同 reason 的阻断. F-2 实现路径取决于此. | Codex 假设 L1 preflight 一站式抽象就够, 未发现 Kiro 是根本性 rustls 不可复刻 |
| **G-CLD-3** | **ML-KEM PQ group 4588 是 cross-cutting blocker** — 3 个 profile 都要; OpenSSL 3.2+ 才支持; HUAKAI vendor build 版本需 spike. | Codex 未提 PQ group / ML-KEM 一个字 |
| **G-CLD-4** | **Gemini 2 variants 路由问题** — 当前 has_sample_set_variants 识别但运行时切换未读. | Codex 提 SampleSetRandomized policy 但未追到 Gemini 具体 variants 行为 |
| **G-CLD-5** | **per-profile gap-fields 拆解** (codex_cli_known_gap_fields → +kiro_cli_known_gap_fields + gemini_advanced_known_gap_fields). | Codex 提 known-gap 守门但未具体拆字段 |
| **G-CLD-6** | **Kiro 永久 KnownGap 推荐** — rustls wire 不可在 OpenSSL 精确复刻, 接受能力降级. | Codex 假设所有 profile 都可修 |

---

## §5 冲突 + 解决

### §5.1 D-F2-1 启动错误形态 (Codex) vs F-2.2 KnownGap detection 扩展 (Claude)
- Codex: 倾向"传 typed Result 通过 GatewayState::new"
- Claude: 倾向"扩 match_policy() 改 per-profile gap lookup"
- **裁定**: **二者不冲突, 互补**. Codex 的 Result 抛出是上层 (dispatch gate); Claude 的 per-profile gap lookup 是底层 (识别 KnownGap). 按 Codex Result 接住底层错误.

### §5.2 D-F2-5 新文件 `mimicry/l1_preflight.rs` (Codex) vs Claude 修 existing `openssl_adapter.rs`
- Codex: 新文件, cohesive preflight 责任.
- Claude: 在 openssl_adapter.rs 加 PQ group + cipher 52394 等.
- **裁定**: **二者都做**. l1_preflight.rs 集中 preflight gate + status types; openssl_adapter.rs 仍是底层实施. l1_preflight 调 openssl_adapter::run_profile_preflight, 加 status wrapper.

### §5.3 D-F2-3 L1-only production status
- Codex: 严格禁 (L2 incomplete profile 不能凭 L1 通过放产线).
- Claude: 同意 (Canary 阻断政策已是上游决定).
- **裁定**: 一致, 无冲突. Synthesis 强 emphasized 一遍.

### §5.4 OD-F2-1 Kiro 路径
- Claude: 永久 KnownGap (a).
- Codex: 未直接谈 — 标 Feature Preservation 为 "Mandatory Roadmap or Feature Flag off; not deleted".
- **裁定**: **(a) 永久 KnownGap + Mandatory Roadmap 标记**. Owner round-2 可决 vendor rustls 或 boring_wire.rs 字节级 builder 作 F-3 子任务.

---

## §6 子计划 (合并) — synthesis 权威

| sub-phase | 内容 | 文件 | 估时 |
|---|---|---|---|
| **F-2.1** | spec-dig: 4 profile wire 实读 + 3 backend 分类 | `2026-05-24-w11f-f2-kiro-gemini-spec-dig.md` (docs) | 0.5 codex-day ✅ done |
| **F-2.2** | 新 `mimicry/l1_preflight.rs` (Codex G-COX-1) + per-profile gap lookup (Claude G-CLD-5) + dispatch gate `try_build_http_client_with_profile` fallible builder | l1_preflight.rs (new), profile.rs, tls_profile.rs, dispatch.rs, http_client.rs | 1 codex-day |
| **F-2.3a** | OpenSSL ML-KEM PQ group 4588 spike + 启用 + apply_supported_groups 顺序硬控 (Claude G-CLD-3) | openssl_adapter.rs | 0.5 codex-day spike + 0.5 day 实施 |
| **F-2.3b** | Codex CLI 闭: cipher 52394 + 关 GREASE + extension stable order | openssl_adapter.rs, tests/mimicry_openssl_adapter_test.rs | 1 codex-day |
| **F-2.3c** | Gemini 闭: backend_intent NodeJs → OpenSslAdapter + 51-cipher + 2 variants 切换 | profile.rs, openssl_adapter.rs, tests | 1 codex-day |
| **F-2.4** | Kiro 永久 KnownGap 标记 + Mandatory Roadmap 文档化 (synthesis §5.4) | tls_profile.rs (+kiro_cli_known_gap_fields), profile.rs match_policy, docs/05_ROADMAP.md | 0.3 codex-day |
| **F-2.5** | 真上游 capture 验证 (Codex / Gemini, Kiro 跳过) + recapture RUNBOOK 更新 | tools/recapture/RUNBOOK.md, fixture | 0.5 codex-day |
| **F-2 docs** | Feature Preservation Mapping 表落 docs + 守门 commit ladder | docs/05_ROADMAP.md, docs/process/release-readiness/W11-F-status.md | 0.2 codex-day |
| **总计** | **8 sub-phase, 6-7 commit** | | **~5.5 codex-day** (synthesis 估 3-4, 合成后 +1.5 因 preflight 抽象 + spec-dig 反映出 Gemini variants 复杂度) |

**顺序硬约束**: F-2.1 → F-2.2 (preflight gate 落) → F-2.3a (PQ blocker) → F-2.3b/c (并行) → F-2.4 → F-2.5 → F-2 docs.

---

## §7 决策矩阵 (合并 D-F2-* + OD-F2-*)

Owner 请逐项 ☐ 选项 或 ☐ **全部默认推荐**:

| # | 决策 | 推荐 | 备注 |
|---|---|---|---|
| **D-S1** | F-2.2 启动错误形态 — typed Result through GatewayState::new vs fail-fast wrapper? | **(a) typed Result + 顶层 fail-fast wrapper 兜底** | Codex D-F2-1 + Claude 修订: 二者都做 |
| **D-S2** | 新文件 `mimicry/l1_preflight.rs` 加吗? | **(a) 加** | Codex D-F2-5, Claude 同意 |
| **D-S3** | Kiro 路径 — 永久 KnownGap (Mandatory Roadmap) vs 走 boring_wire 字节级 builder vs vendor rustls? | **(a) 永久 KnownGap + 进 F-3 Mandatory Roadmap** | rustls wire 不可在 OpenSSL 精确复刻 |
| **D-S4** | Gemini backend_intent NodeJs arm — 改为 OpenSslAdapter 路径? | **(a) 是** | Node.js TLS 底层 = OpenSSL, 字段集兼容 |
| **D-S5** | OpenSSL ML-KEM (group 4588) 启用 — vendor build 版本检查 | **(a) 先 spike** verify openssl-sys 接 OpenSSL 3.2+; 若不支持 → 推 vendor 升级或 3 profile 全永久 KnownGap | Claude G-CLD-3 spec-dig 必做 |
| **D-S6** | Codex CLI ALPN 为空 — OpenSSL adapter `apply_alpn` 需读 verify 支持空? | **(b) F-2.3b 前先 1h spike**; 若不支持改 builder | Spec-dig §5 R-spec-3 |
| **D-S7** | Capture recency policy — stale 但 stable 的自家 capture 是否准产? | **(a) require recapture or Owner waiver** if 老于 90 天 | Codex D-F2-4 |
| **D-S8** | F-2 完成后是否立即 advance F-1? | **(a) 是** | F-1 是 canary unblock 另一半 prerequisite |
| **D-S9** | Canary 阻断 — F-2 完成后是否 unblock canary? | **(b) 否** | 还需 F-1 + 真上游 capture 双满足 |
| **D-S10** | sub2api `tls_fingerprint_profile.go` (LGPL) 字段级分解模型借鉴? | **(b) 否, 进 F-3 roadmap** | LGPL paraphrase-only per CLAUDE.md §11; F-2 不碰 |

---

## §8 风险 + 失败模式 (合并 Codex Failure Modes + Claude R-F2-*)

| # | 风险 | 缓解 |
|---|---|---|
| **R-S1** | OpenSSL ML-KEM vendor build 不支持 → F-2.3a 退化 全 3 profile 阻断 | D-S5 先 spike, 失败 surface Owner 决升级 vendor 或 3 profile 永久 KnownGap |
| **R-S2** | Anthropic 字节级测试集脆弱, F-2.3 改 apply_supported_groups 引漂移 | F-2.2 前先 cargo test --features mimicry-openssl 拉 baseline; 改后 diff |
| **R-S3** | mimicry-openssl pre-existing 8 红 backlog — F-2 改动后回升 / 解锁? | F-2.1 已 verify Anthropic; 8 红是 native-tls/openssl + ec_point_formats / encrypt_then_mac 路径独立, F-2 不应触 |
| **R-S4** | Gemini variants 运行时切换未实施 → variants 标 SampleSetRandomized 但实际握手不切 | F-2.3c spec-dig openssl_adapter.rs variants 处理; 若无, 写入 sub-phase |
| **R-S5** | L1 preflight 集中后, 跨 feature 编译 (default / mimicry-boring / mimicry-openssl / mimicry-http2-fork) cfg 复杂度 | F-2.2 沿 boring_tls_connector HybridStream 模式; Codex verify_profile_dispatchable_for_production 已存, 复用 |
| **R-S6** | Preflight 集中后启动慢 (per-profile handshake) → boot latency degradation | F-2.2 仅对 enabled profile 做 preflight; lazy init not eager |
| **R-S7** | Recapture flow 在 staging 不就绪 → F-2.5 不能完成 → canary 永远阻 | Owner 协调 ops; 若长期不就绪 → 接受 F-2 不解 canary 限制, 文档化 |
| **R-S8** | known-gap profile 误标 — 把 Kiro 标 KnownGap 后用户期望 Kiro 工作 | F-2 docs commit 写 release notes + docs/05_ROADMAP.md 明 Kiro 进 roadmap |

---

## §9 引证表 (CLAUDE.md #12 fusion-upgrade)

| 借鉴源 | repo@SHA | 模式 | HUAKAI delta | 维度 |
|---|---|---|---|---|
| **sub2api** | `Wei-Shaw/sub2api@f59d9a5f8e0983f4a92c1ba8c4ecd8d33f7e4427:backend/internal/pkg/tlsfingerprint/dialer.go:361-364` (stale-but-stable, 2025-02 pushed; LGPL paraphrase only) | Go fork uTLS 字节级 ClientHello + GREASE 一等概念 + per-account binding | F-2 范围内 HUAKAI 不借鉴, 进 F-3 roadmap; F-2 实际只闭 OpenSSL 路径 | 架构 (roadmap only) |
| **cliproxyapi** | `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/api/handlers/request_body.go:14` (2026-05-21, 29 天内 ✅) | uTLS 造 L1 但 L2 走 Go 原生 stack | F-2 与 cliproxyapi L1 一致; F-1 在 L2 上突破其能力 (synthesis §4 line 385) | 算法 |
| **BoringSSL** | TBD specifier-dig fresh SHA + PQ group support commit | Apache-2.0 (MIT vendor 允许 per CLAUDE.md §12) | F-2.3a 利用现成 BoringSSL PQ API; 不 vendor BoringSSL 代码, 通过 boring crate 调用 | 算法 |
| **OpenSSL 3.2+ ML-KEM** | TBD specifier-dig fresh SHA + commit (openssl/openssl SSL_CTX_set1_groups_list ML-KEM support) | Apache-2.0 | F-2.3a 通过 SSL_CTX_set1_groups_list("X25519MLKEM768:X25519") | 算法 |

**Specifier follow-up in F-2.1 final pass** (本 spec-dig 已部分覆盖):
- BoringSSL + OpenSSL ML-KEM commit recency check (CLAUDE.md #12 90 天)
- HUAKAI vendor openssl-sys version 验证
- Anthropic profile 字节级测试 baseline diff

---

## §10 执行启动门 (Owner approval needed)

Owner 必须显式批准下面 3 项才能进入 F-2.2 实施:

1. ☐ **§7 决策矩阵全部默认推荐** (或指定 override)
2. ☐ **认可 F-2 估时 ~5.5 codex-day** (vs synthesis §4 估 3-4, 涨 1.5 因 preflight 抽象 + Gemini variants 复杂度)
3. ☐ **认可 F-2 完成不解 canary 限制** (D-S9 (b)); 需 + F-1 + 真上游 capture 双满足才解

回答 "**全部默认 + 启动**" ⇒ Claude 立刻进 F-2.2 (l1_preflight.rs 新建 + per-profile gap lookup + dispatch gate).
或者指定 override + 启动: "**D-S5 (b), D-S3 (b), 启动**" ⇒ Claude 据此修 plan 后启动.

---

**Clean-room-attestation**: original HUAKAI design synthesis; all reference-project citations are evidence pointers (no source/comments copied). sub2api (LGPL) paraphrase-only per CLAUDE.md §11; BoringSSL/OpenSSL ML-KEM (Apache-2.0) function-level API only.

**Source files read for this synthesis**:
- `2026-05-24-w11f-f2-fingerprint-l1-claude.md` (Claude 自家 draft)
- `2026-05-24-w11-f-f2-l1-tls-gap-closure-codex.md` (Codex independent draft)
- `2026-05-24-w11f-f2-kiro-gemini-spec-dig.md` (Claude spec-dig 实读 4 JSON)

**Lane**: F-2 synthesis (planning).
**Agent**: Claude (合成 + decision matrix).
**Timestamp**: 2026-05-24 UTC.
