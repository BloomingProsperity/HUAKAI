# 2026-05-15 Round 1 Cross-Discuss Synthesis (Claude × Codex)

| Method | CLAUDE.md #10 parallel-draft：4 个 lane 由 Claude 和 Codex 独立产出，本文档为交叉对比 + 分歧 surface |
| Owner directive | "定个计划，能并行就并行。不能再拖了。最大限度将codex用起来" + "交叉并行" |
| Round 1 scope | α R-3 R-D first atom + β R-3 R-E mainline plan + γ HIGH risk mitigation + δ Mandatory Roadmap Top-5 |

## 总评

| Lane | Codex 文件 | Claude 文件 | Agree | Conflict | Gap |
|---|---|---|---|---|---|
| α R-3 R-D | (代码) `tests/common/capture_artifact.rs` + `tests/{common/mod,mimicry_capture_test}.rs` | `docs/plans/2026-05-15-r-3-r-d-claude.md` | 第一 atom 方向（先 capture artifact writer）一致 | 无 HIGH 冲突 | Claude 推荐的 `DispatchFlavor` 枚举尚未引入,Codex 暂走 baseline path |
| β R-3 R-E | `docs/plans/2026-05-15-r-3-r-e-mainline-codex.md` (235 行) | `docs/plans/2026-05-15-r-3-r-e-mainline-claude.md` (75 行) | "R-D + R-SEC-002 是 R-E 前置" 一致 | OCAW D1：Codex 推 mTLS TCP (mTLS over TCP 默认),Claude 推 UDS 优先 | Codex 列了 4 OCAW；Claude 列了 4 OCAW，**有 1 个 D 选项分歧** |
| γ HIGH risk | `docs/reviews/2026-05-15-high-risks-mitigation-codex.md` (195 行) + 我手补的 risk register Triage Notes | `docs/reviews/2026-05-15-high-risks-mitigation-claude.md` (77 行) | 3 HIGH 都保留 Open,mitigation 路径一致 | 无 | Codex `cargo deny` 因沙箱网络受限未跑通；Claude 推荐与 Codex 给的"L2 deny policy + CI gate"完全一致 |
| δ Mandatory Roadmap | `docs/reviews/2026-05-15-mandatory-roadmap-priority-codex.md` (162 行) + `docs/plans/.../priority-codex.md` (13 行) + `docs/03_FEATURE_PARITY_MATRIX.md` 改 | `docs/reviews/2026-05-15-mandatory-roadmap-priority-claude.md` (85 行) | "三档分类 + 立刻可启动 / 等 R-E / 远景" 框架一致 | **Top-5 大相径庭** | Codex 找到 **19** 项实际 Mandatory Roadmap，Claude 凭背景说 24 项,**数差异 5 项需要复查** |

## Lane α — R-3 R-D first atom（详）

**Codex 选择**：实施 `write_tls_clienthello_artifact()` helper + 接到现有 `mimicry_capture_test::baseline_hyper_rustls` 上,baseline capture 跑完会写 JSON artifact 到 `$CARGO_TARGET_DIR`。新 `tests/common/capture_artifact.rs` 127 行。

**Claude 推荐过**：`DispatchFlavor` 枚举 + ProxyEngine 配置 + e2e harness。

**对比**：Codex 选择更窄、更先 land。Claude 推荐更宽，要改 ProxyEngine 配置层。**Codex 的窄路径优先 land,Claude 的宽路径可作后续 atom**。无 HIGH 冲突。

**Cargo verify (Claude 跑)**：`cargo test -p core_gateway --features "mimicry-openssl mimicry-http2-fork" --tests --no-fail-fast` → **16 binaries / 194 passed / 0 failed**。Codex 改动未引入回归。

**Owner 决策点**：α 的 atom 可以单独 commit 然后下一 atom (DispatchFlavor 枚举或别的) 继续推。

## Lane β — R-3 R-E mainline plan（详）

**Codex 4 OCAW**：(1) transport baseline UDS+HMAC vs mTLS TCP vs shared-secret loopback-dev (2) Mainline RPC runtime: grpc-go + gRPC / HTTP-JSON shim / dual-stack (3) Shadow traffic policy: route-only / mock-local / real-upstream shadow (4) Promotion + fallback retention 窗口。

**Claude 4 OCAW**：(D1) UDS vs TCP+mTLS 默认（推 UDS）(D2) hyper-rustls fallback 保留多久（推 12 个月）(D3) ramp 触发回退手动 vs 自动（推手动 + 自动告警）(D4) R-3 R-E commit 后是否同步 R-D 团队（推必须）。

**Agree**：
- R-D + R-SEC-002 都是 R-E 前置
- 不删 fallback
- 切换前 GO/NO-GO 闸门必须 GREEN

**Conflict / 选项分歧**：
- Codex D1 三个选项,推荐 **mTLS TCP** 是 codex 隐含默认（gRPC over TCP 容易）；Claude 推 **UDS 优先** (HUAKAI L1/L2 单机部署),mTLS 备选 (L4 SaaS)。
- 这个分歧 surface 给 Owner 是个清晰的 P0 决策点。

**Gap**：Codex 详细给了 RPC runtime + shadow traffic policy 这两个 Claude 没覆盖的维度。Claude 给了 ramp 0.001 → 1.0 这个 Codex 没明示的具体比例。

**Owner 决策点**：(1) UDS 还是 mTLS TCP？(2) RPC runtime 三个选项？(3) ramp 比例（按 Claude 还是 Codex 隐含）？(4) fallback 保留 12 月 / 24 月 / 永久？

## Lane γ — HIGH risk mitigation（详）

**Codex 做了**：
- 3 HIGH 一一收敛分析
- 真实可跑命令清单 (6 个 cargo deny / cargo tree 命令)
- 当场跑 `cargo tree --edges=normal | grep -Ei 'gpl|lgpl|agpl'` 在 default + mimicry feature graph 上 → **未命中**任何 GPL/LGPL/AGPL
- `cargo deny check licenses` **失败** (沙箱网络 / crates.io download blocked)
- 4 步后续 (L1 deny.toml / L2 CI / L3 per-feature audit / L4 ban list 维持)
- 试图改 risk register 但 apply_patch 失败

**Claude 补**：手工把 Triage Notes 写进 `docs/10_RISK_REGISTER.md` 表后,引用 Codex review doc 作为证据。

**Claude review 视角**：
- R-SEC-002: 与 R-E plan 兼容,R-E 切换前 GREEN
- R-TRANSPORT-001: 升 Ongoing-Monitor 而非 Mitigated（policy + cron 监控 + 真上游回放）
- R-LIC-003: 加 `.deny.toml` + PR 纪律,Mitigated 后

**Agree**: 3 HIGH 都保留 Open,mitigation 路径一致。

**Conflict**: 无。

**Gap**: Codex 没跑通 cargo deny — 需要在有网环境补跑。Claude 推荐加 `.deny.toml` ban 列表显式含 `wreq-util` / `rquest-util`,Codex 也列在 step L4 — 一致。

**Owner 决策点**：(1) 是否批准 PR 添加 `.deny.toml` ? (2) cargo deny CI gate 谁加 (Codex)？(3) 真上游回放 cron 是否启动 (R-E ramp Phase 2 之前必须就位)？

## Lane δ — Mandatory Roadmap Top-5（详 — 最大分歧）

**Codex Top-5**：
1. F-OBS-003 — 4-state failed-stream billing (Phase 4.5)
2. F-OBS-004 — async processor chain
3. F-OBS-005 — DLQ / priority / dual-write
4. F-BILL-002 — voucher redemption (Phase 6)
5. F-AUTH-006 — OAuth bootstrap commercial blocker (legal/spec gate only)

**Claude Top-5**：
1. F-CACHE-001 simple L2 cache (Phase 6, 营业卖点)
2. F-AUTH-005 upstream credential mgmt (Spec Released)
3. F-CH-002 channel health auto-disable
4. F-GW-003 SLO + 资源 budget
5. F-BILL-002 voucher

**Conflict — significant**:
- Codex Top-3 全是 F-OBS-003/004/005 — Phase 4.5 的 4-state billing + async + DLQ 三件套
- Claude Top-5 完全没列 F-OBS-003/004/005

**Gap — Claude 漏了**: F-OBS-003/004/005 是 Phase 4.5 (R-E 之前) 的关键 dependency-critical 项,Claude 在排序时没充分考虑 dependency depth 维度。

**Gap — Codex 漏了 / 没强调**: F-CACHE-001 (营业卖点 / 与 sub2api 差异化) + F-AUTH-005 (HUAKAI 信任链卖点)。

**数差异**：Codex 找到 **19** 个实际 Mandatory Roadmap 行,Claude 凭背景说 **24** 项 — Codex 实测胜出。

**Owner 决策点**: 
- (1) Phase 4.5 F-OBS-003/004/005 先做（Codex 推），还是 F-CACHE-001 + F-AUTH-005 先做（Claude 推）？
- (2) Mandatory Roadmap 实际计数 19 还是 24（Codex 实测 19，Claude 漏查）— 我推 Codex 数字。
- (3) F-AUTH-006 是否启动 legal/spec gate（Codex 推 yes，Claude 没列）？
- (4) F-AUTH-005 实施 owner 是 Codex 还是 Claude（Claude 推 Codex 直接实施 since Spec Released）？
- (5) 是否同时双线（Phase 4.5 F-OBS 三件套 + F-CACHE-001）— 推荐路径。

## 我推荐的 Owner 路径

1. **采纳 Codex δ 输出作为主线**：Top-5 = F-OBS-003 / 004 / 005 / F-BILL-002 / F-AUTH-006（Phase 4.5 trio 先做）
2. **Claude 的 F-CACHE-001 + F-AUTH-005 作为 Next Wave**（Top-5 完成后立即启动）
3. **R-3 R-E plan 走 Claude 推荐**：UDS 默认 + 12 月 fallback 保留
4. **R-LIC-003 立刻补 `.deny.toml`**（Codex 给的命令清单已可执行,只缺 doc + config）
5. **α 的 R-D first atom 立即 commit**，下一 atom 推 DispatchFlavor 枚举 + ProxyEngine 配置（Claude 推荐路径）

## Source files read

- `/tmp/codex_lane_alpha.log`（α 完整 log）
- `/tmp/codex_lane_beta.log`
- `/tmp/codex_lane_gamma.log`
- `/tmp/codex_lane_delta.log`
- 4 个 Claude 平行 doc + 4 个 codex doc + parity matrix diff + risk register patch

Lane: synthesizer (Claude 决策视角)  
Agent: Claude Opus 4.7 (1M context)  
UTC timestamp: 2026-05-15T12:30:00Z
