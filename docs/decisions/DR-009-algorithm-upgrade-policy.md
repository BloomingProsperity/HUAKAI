# DR-009: Algorithm Upgrade Policy — 8 Decisions + Client Transparency + Seller Hard Floor

| Field | Value |
| --- | --- |
| Status | Decided |
| Date opened | 2026-05-02 |
| Date decided | 2026-05-02 |
| Owner | Owner |
| Affected docs | docs/03_FEATURE_PARITY_MATRIX.md, docs/specs/*, docs/16_PHASED_DELIVERY_PLAN.md, docs/10_RISK_REGISTER.md |
| Supersedes | — |
| Superseded by | — |

## Question

Owner directive 2026-05-02: HUAKAI 是开源 AI gateway 竞赛产品。三份平行 plan（Claude 28 项 / Codex 26 项 / synthesis 30 项）已落盘 ([docs/plans/2026-05-02-huakai-algo-upgrade-{claude,codex,synthesis}.md](../plans/)）。Synthesis §6 列出了 8 个执行前必须 Owner 拍板的决策点。Owner 选 "**完整升级 + 保证卖方的利益 + 用户端要显示一些切换的情况**"。

本 DR 把 Owner 决议 + 推论约束钉死，作为 Phase A-E 所有 spec 扩展的引用源头。

## Owner Decision

### 8 决议（synthesis §6 详表）

| # | 决策 | Owner 选择 | 备注 |
|---|---|---|---|
| 1 | 账号 auto-disable | **C 分级** — 铁证 keyword 命中（OAuth invalid_grant / KYC / org_disabled / token_revoked / deactivated_workspace 等）自动 permanent disable；模糊 (持续 5xx / 边界 4xx) 仅挂红灯等 operator 点 | 减少误伤、保护账号资产 |
| 2 | 多 attempt 计费归因 | **C 双账** — 客户账单 `succeeded_on`；后台 analytics `dollar_weighted` | 客户账单干净；操作员看到全 attempt 成本 |
| 3 | Model substitution | **C 显式 opt-in + 客户响应头标注** — 默认禁；route policy 勾允许；换了必带 `X-Huakai-Model-Substituted` + `X-Huakai-Substitution-Reason` | 客户不流失同时知情 |
| 4 | Quantile reserve | **C 分层** — 新用户 P99 严；正常 P95；wallet 信用 P90 宽 | 防超卖同时不误报 |
| 5 | Drain privacy | **A** — drain 决策只看 token usage 元数据，不读 prompt body | 合规 + billing capture |
| 6 | Capacity graph 范围 | **B** — Personal 单租户；SaaS 强制租户隔离 | SaaS 防租户互窃 |
| 7 | Sticky 算法 | **B HRW Rendezvous Hashing** | 账号增减时 sticky 命中率 60% → 99% |
| 8 | Stream 中途换账号 | **B opt-in 头** — 默认禁；客户带 `Idempotent-Stream-Replay: true` 才允许 | 默认安全防双扣费纠纷 |

### 客户透明度响应头清单（Owner directive: 用户端要显示切换情况）

每次请求若发生以下任一情况，必须带相应响应头（除非客户带 `X-Huakai-Quiet: true`）。Synthesis §6.5 详表，钉死如下：

```
X-Huakai-Account-Failover-Count    int       — failover attempt 数
X-Huakai-Migration-Reason          enum      — sticky 打破原因
X-Huakai-Migration-Cache-Lost      bool      — Vendor-X2 prompt cache 是否失效
X-Huakai-Migration-Cost-Delta-Usd  decimal   — 迁移导致的预估成本增加
X-Huakai-Model-Substituted         "from→to" — 仅 Q3 开启时
X-Huakai-Substitution-Reason       enum      — 同上配套
X-Huakai-Quota-Headroom-Usd        decimal   — 当前 binding 剩余配额
X-Huakai-Routing-Strategy          enum      — A03 strategy 启用时
X-Huakai-Migration-Action          enum      — A04 决策 (stay/migrate_*/fail_closed)
X-Huakai-Stream-Boundary           enum      — A12a 阻止 retry 时 (debug 模式)
Retry-After                        int       — wait_or_fail 决策建议
```

### 卖方利益硬底线（不可妥协）

| 算法 | 不可让步原因 |
|---|---|
| A09 二阶段 quota reserve+settle | 高并发超卖 = 直接亏钱；sub2api 现 settle-only 是 HUAKAI 必须升级的关键差距 |
| A26 drain 决策 = E[value] > E[cost] 仍 drain | 默认贪婪收 usage 保 billing capture |
| A11 attempt DAG 全 attempt 写 audit | 后台完整成本可见（Q2 dollar_weighted 落点） |
| A22 hysteresis FSM | 不轻易 permanent_disable，只 Q1 铁证关键词才自动；保护账号库存 |
| A07 3-scope storm controller | 防 OAuth refresh 自杀打爆 vendor endpoint |
| A12a stream-safe boundary | 防双扣费纠纷（已发字节后 retry 客户会投诉重复内容） |
| A19 cross-vendor min-residual graph | 维持账号组合最大可用容量；不让单 vendor drain 完才 fallback |

## Implications That Flow Immediately

### Phase 排序（synthesis §7 钉死）

```
Phase A (~80h, 与 N+5b spine 协同):
  A01 Binding-aware scheduler          (P0, 14h)
  A06 Credential lease state machine    (P0, 10h)
  A07 3-scope refresh storm controller  (P0, 10h)
  A13 Provider error normalization rule (P0, 12h)
  A15 Versioned pricing vector          (P0, 12h)
  A22 Health hysteresis FSM             (P0, 12h)
  + DR-009 propagation (~10h)

Phase B (~70h, P0 之 spine 之外):
  A02, A09, A11, A12a, A23

Phase C (~56h, capacity + stream):
  A17, A19, A21, A25, A26, A04

Phase D (~84h, P1):
  A03, A05a, A05b, A08a, A08b, A10, A12b, A14, A16a, A18, A20, A24, A29
  注: A20 includes merged A28 (Claude 跨 vendor fault-domain 故障感知 →
      A20 Fault-Domain Spillover Guard 的 domain_health 指数衰减输入)

Phase E (~16h, P2/P3):
  A16b, A27, A14b
```

### Spec 扩展依赖（不动 backend/admin/OpenAPI 直到 spine 0011 落地）

- **A01** 需 `api_key_bindings` 表 → 已在 N+5b spine plan 0011 migration ([docs/plans/2026-05-02-accapi-spine.md](../plans/2026-05-02-accapi-spine.md))
- **A06** 需 `provider_accounts.credential_version` + `request_attempts.credential_version` → spine 0011
- **A09** 需 `billing_ledger_claims` 已存在 + 加 `pricing_snapshot_version` 列
- **A15** 需 `pricing_snapshots` 表 + Merkle hash 链
- **A22** 需统一 `account_state` 列 + transition log

## Propagation Checklist

- [ ] Update [docs/03_FEATURE_PARITY_MATRIX.md](../03_FEATURE_PARITY_MATRIX.md) — add 30 A-IDs as F-* rows or extend existing
- [ ] Update [docs/specs/pool-routing.md](../specs/pool-routing.md) — extend with A01/A02/A03/A04/A05a/A05b
- [ ] Update [docs/specs/upstream-credential-management.md](../specs/upstream-credential-management.md) — extend with A06/A07/A08a/A08b
- [ ] Update [docs/specs/observability-billing.md](../specs/observability-billing.md) — extend with A09/A10/A15/A16a/A11
- [ ] Update [docs/specs/rate-limiting.md](../specs/rate-limiting.md) — extend with A13/A14/A21/A22
- [ ] Update [docs/specs/streaming-forwarder.md](../specs/streaming-forwarder.md) — extend with A12a/A25/A26/A27
- [ ] New [docs/specs/capacity-graph.md](../specs/capacity-graph.md) — A17/A18/A19/A20
- [ ] New [docs/specs/client-identity.md](../specs/client-identity.md) — A23/A24
- [ ] New [docs/specs/model-substitution.md](../specs/model-substitution.md) — A29 + Q3 opt-in mechanics
- [ ] Update [docs/10_RISK_REGISTER.md](../10_RISK_REGISTER.md) — R-OVERSELL-001 mitigation = A09; R-DOUBLE-CHARGE-001 = A12a
- [ ] Update [docs/16_PHASED_DELIVERY_PLAN.md](../16_PHASED_DELIVERY_PLAN.md) — Phase A-E ordering

## Open Questions Carried Forward

- A05a HRW seed source: Owner 偏好 `acct.priority_seed` 列还是固定 `acct.id` 即可？（synthesis 默认后者；增 seed 列可防对手猜命中）
- A29 model substitution table source of truth: 配置文件 vs 数据库表？建议数据库 + version 化，与 A15 pricing snapshot 同样的 Merkle 化模式

## Single-Line Summary

**完整升级 + 卖方利益 + 客户透明** = 8 决议 C 类化（Q1/Q2/Q3/Q4）+ B 类透明化（Q6/Q7/Q8）+ A09/A26/A11/A22/A07/A12a/A19 七项硬底线 + 11 个客户响应头 + 30 算法 A-IDs 分 5 phase ~297h 工作量。
