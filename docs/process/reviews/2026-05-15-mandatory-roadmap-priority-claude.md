# 2026-05-15 Mandatory Roadmap Priority Claude 平行评审

| Lane | Claude（与 codex δ 平行；非交流前独立产出） |
| Source | `docs/03_FEATURE_PARITY_MATRIX.md` 中 Disposition = `Mandatory Roadmap` 行 |
| Method | 不实施,只排序;打分维度: Operational Value × Effort × Dependency |

## Claude 看到的 Mandatory Roadmap 项 (24)

按当前 phase 标注的远景排序:
- Phase 6+: F-BILL-002 voucher, F-CACHE-001 response cache, F-OBS-001..005 dashboard tier (其中部分已 Spec Released)
- Phase 7+: F-UI-001 branding, F-I18N-001 i18n, F-OPS-003 multi-source ops dashboard, F-DEPLOY-001 multi-target, F-DEPLOY-002 K8s
- Phase 8+: F-SEC-003 signed images, F-OPS-002 in-dashboard self-upgrade
- Phase 9+: F-MM-001 multimodal, F-RT-001 WebSocket realtime, F-MODEL-002 rerank, F-PROTO-001 MCP/A2A bridging
- L4 SaaS: F-ARCH-001 two-tier topology, F-AUTH-003/004 SSO/community OAuth

## 三档分类 (Claude 推荐)

### 档 1 — "立刻可启动" (R-3 R-E mainline 不需要先完成)

| F-ID | 推荐理由 | Effort 估 | Dep |
|---|---|---|---|
| **F-CACHE-001 简单 L2 cache** | 直接降本/提速;独立模块,不依赖 transport mimicry;HUAKAI 营业卖点 (与 sub2api 差异化的"缓存透明"集) | 3-5 天 | Tx2 settlement (F-BILL-001 已 spec) |
| **F-AUTH-005 upstream credential mgmt** | Spec Released,已经审 → 实施;HUAKAI 信任链差异化的核心 (memory `project_core_trust_chain_differentiator`);R-3 R-E 之外可以平行 | 5-8 天 | F-BILL-001 spec (已) |

### 档 2 — "等 R-3 R-E 完成或并行" (依赖 transport 主线)

| F-ID | 推荐理由 | Dep |
|---|---|---|
| **F-CH-002 channel health auto-disable** | 跟 R-D 端到端真上游打通同期可做;运营痛点 (silent drop) | R-D mock 上游可起步,真上游测试需 R-E |
| **F-GW-003 SLO + 资源 budget** | 同上;运营可见;HUAKAI 与 LiteLLM 差异化 | R-E ramp 期同步可做 |

### 档 3 — "Phase 9+ 远景 / 暂搁置"

| F-ID | 为什么暂搁置 |
|---|---|
| F-MM-001 multimodal | 上游 model capability 矩阵大变,等 vendor 协议稳定 |
| F-RT-001 WebSocket realtime | 上游 protocol surface 不稳;OpenAI Realtime 自己还在变 |
| F-PROTO-001 MCP / A2A bridging | 协议生态不稳,先观察 |
| F-DEPLOY-002 K8s artifact | L1/L2 personal edition 不需要 K8s,L4 SaaS 时再做 |
| F-SEC-003 签名镜像 | 等 Phase 8 production hardening 整体 |
| F-UI-001 branding | 等 SaaS edition;personal edition 没必要 |
| F-I18N-001 i18n | 等 L3+ |

## Top-5 推荐启动顺序

1. **F-CACHE-001 simple L2 cache** — 立刻启动 (档 1 之首)。营业卖点 + 操作简单 + 不依赖 R-3 R-E。预计 3-5 天 codex 实施 + 1 天 review。
2. **F-AUTH-005 upstream credential mgmt** — 立刻启动 (Spec Released 加大优先级)。HUAKAI 信任链核心。5-8 天。
3. **F-CH-002 channel health auto-disable** — 与 R-3 R-D 同期开始 (R-D mock 上游就可)。运营痛点。3-4 天。
4. **F-GW-003 SLO + 资源 budget** — R-3 R-E ramp 期同步做。运营可观察。5-7 天。
5. **F-BILL-002 voucher (Phase 6)** — 商业模型需要 (推 SaaS 时)。5-7 天。

## 5 个 OCAW 决策点

(D1) 是否同意"档 3"远景项暂搁置,直到 Phase 8+? Claude 强推同意。  
(D2) F-CACHE-001 实施时机:与 R-3 R-D 完全并行,还是等 R-D close 再启动? Claude 推完全并行 (无 transport 依赖)。  
(D3) F-AUTH-005 实施 owner:Claude 还是 Codex? Claude 推 Codex (Spec 已审,直接实施)。  
(D4) F-RT-001 / F-MM-001 是否 Phase 8 时启动一个 mini-spike (1-2 天) 摸底? Claude 推 yes,可以摸底但不立刻实施。  
(D5) `docs/03_FEATURE_PARITY_MATRIX.md` 是否补一列 "2026-05-15 Triage Priority Tier" (档1/档2/档3)? Claude 推 yes,运维 + Owner 看 matrix 时直接可见排期。

## 横切观察

- HUAKAI 营业差异化 (memory `project_core_trust_chain_differentiator` 的"链路公开 / 透明 / 反掺水") 与 mandatory roadmap 的相关项:F-AUTH-005 upstream cred + F-BILL-001 Tx2 settlement (已 spec) + F-OBS 系列 + F-CACHE-001 透明缓存。这些是 HUAKAI 的核心卖点,优先级应高于 F-MM-001 / F-RT-001 等通用功能。
- Mandatory Roadmap 的 24 个里,真正与"立刻有运营价值"挂钩的是档 1 (2 项) + 档 2 (2 项) = 4 项,其它都是远景。

## 与 codex δ 可能分歧

- (a) codex δ 可能用纯 Operational-Value × Effort 二维打分,Claude 加 "依赖 transport 主线" 第三维度。
- (b) codex δ 可能推 F-MM-001 / F-RT-001 在 Phase 8 而不是 Phase 9+ (因为 Anthropic / OpenAI vendor 跑得快),Claude 坚持 Phase 9+ 因为协议仍在动。
- (c) codex δ 可能漏掉 F-AUTH-005 与 HUAKAI 信任链卖点的关联,Claude 强调。

Synthesis 期望: Owner 看 Claude vs codex 两版,选最终 Top-5 顺序;不齐处 Owner 拍板。

## 严禁

- 实施任何 mandatory item (本评审只排序)
- 改 parity matrix Disposition 列 (codex δ 负责加 Status 注 / 新节)
- 删任何 row

## Source

Read: `docs/03_FEATURE_PARITY_MATRIX.md`,`docs/specs/{streaming-forwarder,observability-billing,protocol-translation,pool-routing,rate-limiting,upstream-credential-management}.md` (Spec Released 项),memory `project_core_trust_chain_differentiator`,memory `project_real_vendor_account_scope`。

Lane: Claude parallel  
Agent: Claude Opus 4.7 (1M context)  
UTC timestamp: 2026-05-15T12:06:00Z
