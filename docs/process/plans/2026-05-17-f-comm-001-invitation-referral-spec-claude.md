---
plan_id: 2026-05-17-f-comm-001-invitation-referral-spec-claude
lane: claude (PM/specifier)
status: drafted
counterpart_plan: docs/process/plans/2026-05-16-f-comm-001-invitation-referral-spec-codex.md
spec_target: docs/specs/community-invitation-referral.md
spec_id: F-COMM-001
phase: 6 (Commerce/Growth)
utc: 2026-05-17T14:08:00Z
---

# F-COMM-001 邀请/推荐系统 Spec 草拟 — Claude 平行计划

## 0 缘起

Codex round 1 (commit 79a6865) 写了 pre-plan `docs/process/plans/2026-05-16-f-comm-001-invitation-referral-spec-codex.md`,
但实际 spec 文件还没出. 按 CLAUDE.md #10 "BOTH Claude and Codex independently draft their own plan FIRST" 的要求,
Claude 这边也独立产一份 plan, 然后跟 codex 平行对照, 再让 codex specifier 写实际 spec.

## 1 Spec 目标 (Claude 视角)

写出 `docs/specs/community-invitation-referral.md`, 覆盖 HUAKAI Phase 6 商业基础三件之一 (剩两件是 F-PAY-001 真付费 + F-DASH-001 老板看板).

核心场景:
- 老用户产 invitation code → 发出去
- 新用户拿 code → 注册 → 触发首次成功 billing event → 老用户得 reward
- 老用户累计有效推荐 N 个 → 解锁 tier (silver / gold / platinum)
- 全链路有 audit trail (F-AUDIT-1 receipt + F-TRUST-1-B 签名)

## 2 Claude 视角的设计原则

1. **派生 over 拥有**: invitation/referral/reward 表都引用 F-AUTH-007 user + F-BILL-002 voucher + F-AUDIT-001 receipt,
   不自建第二套 user/billing 视图. 这跟 F-AUDIT-1-A 刚刚落地的 derived-first 原则一致.
2. **触发点严格 = 首次成功 billing event**: 避 sub2api / new-api 普遍的 "注册即给" 漏洞,
   防 fake account 刷 reward. 触发点必须是 billing_events.event_type='claim_committed' 且 amount > 0.
3. **Anti-abuse 三层**: device fingerprint (F-FP-001) + IP cooling window (72hr) + email domain blocklist;
   缺一层就标 reward.status=pending 不发放.
4. **审计 over 信任**: 每个 reward 必走 audit_ledger_entries + F-TRUST-1-B ed25519 sign + F-AUDIT-1 receipt;
   用户在 receipt 中能看到自己被 reward 的 USD 数, 商家不能事后改.
5. **Tier 推广是 incentive 而非 funnel**: 不引入推荐链树, 不引入多级佣金 (sub2api 也没做; 留 Phase 7+).
   Tier 解锁纯靠累计有效推荐数.

## 3 Claude 视角的数据模型 (不抄 reference)

四张表 (HUAKAI 自定 schema):

| 表 | 关键字段 | 不变量 | 备注 |
|---|---|---|---|
| `invitations` | id, tenant_id, code (UNIQUE), inviter_user_id, created_at, expires_at, usage_count, max_usage | code 全局 UNIQUE; usage_count <= max_usage | code = 8 位 Crockford base32; per-tenant 月配额 = 100 |
| `referrals` | id, referee_user_id (UNIQUE), referrer_user_id, invitation_id, status, qualified_at, first_billing_event_id | 1 referee 只能被 1 个 referrer; status ∈ {pending, qualified, rewarded, rejected} | qualified 触发 = first_billing_event_id 非空 |
| `referral_rewards` | id, referrer_user_id, referee_user_id, referral_id, reward_type, amount_usd_micros, issued_at, receipt_id | reward 跟 receipt 1:1 | reward_type ∈ {credit, voucher}; voucher 走 F-BILL-002 |
| `tier_progress` | user_id (PK), total_qualified_referrals, current_tier, tier_unlocked_at | current_tier ∈ {bronze, silver, gold, platinum}; 阈值 = (0, 3, 10, 50) | tier 只升不降 |

所有表都加 append-only friendly 设计 (新写入不改老行; status 改用 UPDATE 但有审计).

## 4 Claude 视角的 API endpoint (5)

| Method | Path | 鉴权 | 作用 |
|---|---|---|---|
| POST | `/v1/invitations` | user JWT | 生成新 invitation code; body = {max_usage?, expires_in?} |
| GET | `/v1/invitations/{code}/preview` | 公开 | 新用户预览 inviter 名 + reward 期望 (不曝光 PII) |
| POST | `/v1/auth/register` (扩 ?invitation_code=) | 公开 | 注册时同步 invitation 绑定 (跟 F-AUTH-007 共用) |
| GET | `/v1/referrals/my` | user JWT | 老用户看 referral list + 各自 status + reward |
| GET | `/v1/tiers/my` | user JWT | 老用户看 tier progress + 已解锁 tier |

## 5 Claude 视角的跨 spec 依赖图

```
F-COMM-001
├── F-AUTH-007 (用户认证 + register 流程)
├── F-BILL-002 (voucher reward 类型)
├── F-AUDIT-001 (每个 reward 出 receipt)
├── F-TRUST-001 (reward 签名进 ledger)
├── F-PRIV-001 (invitation/preview/referrals 全经 Redactor)
├── F-FP-001 (device fingerprint anti-abuse, R-4 闭环后)
└── F-OBS-005 (reward async pipeline 走 DLQ)
```

## 6 Claude 视角的 AT 计划 (15-20 条)

| AT-ID | 场景 |
|---|---|
| AT-COMM-001-001 | 生成 invitation code 全局 UNIQUE |
| AT-COMM-001-002 | 同 user per-tenant 月配额 100 触底拒绝 |
| AT-COMM-001-003 | 过期 invitation code 注册被拒 |
| AT-COMM-001-004 | usage_count > max_usage 注册被拒 |
| AT-COMM-001-005 | preview endpoint 不曝光 PII |
| AT-COMM-001-006 | 注册时同步绑 referrer |
| AT-COMM-001-007 | 1 referee 只能被 1 referrer 绑定 (UNIQUE constraint) |
| AT-COMM-001-008 | 注册后未触发 billing event → status=pending |
| AT-COMM-001-009 | 触发首次 billing event → status=qualified + reward 计算 |
| AT-COMM-001-010 | 同 IP 短窗内多 referee → status=rejected (cooling window) |
| AT-COMM-001-011 | 同 device fingerprint → status=rejected (F-FP-001) |
| AT-COMM-001-012 | email 黑名单域 → status=rejected |
| AT-COMM-001-013 | reward 触发后 receipt 真有 reward 行 (F-AUDIT 联动) |
| AT-COMM-001-014 | reward 经 F-TRUST sign, 篡改 reward 表后 verify FAIL |
| AT-COMM-001-015 | 累计 3 referral → silver tier 解锁 |
| AT-COMM-001-016 | 累计 10 referral → gold tier 解锁 |
| AT-COMM-001-017 | 累计 50 referral → platinum tier 解锁 |
| AT-COMM-001-018 | tier 只升不降 (老 referral 撤销不退级) |
| AT-COMM-001-019 | referrals/my 只返回自己当 referrer 的行 (cross-user 隔离) |
| AT-COMM-001-020 | tiers/my 返回当前 tier + 距离下一级 N |

## 7 Claude 视角的 vs Sub2API 差异化要点 (写到 spec §10)

> **重要说明 (CLAUDE.md #12 合规)**: 本节列出的"Reference 普遍做法"列均为
> **Claude 二手认知占位**, 未读 source, **不构成 source-backed 评估**.
> Codex specifier 写 spec §10 时必须先读 ~/refs/sub2api / ~/refs/new-api /
> ~/refs/all-api-hub 真 source, 用 `<repo>@<sha>:<file>:<line>` 引证, 重写或
> 删除占位描述. **不允许直接复用本表内容入 spec**. HUAKAI delta 列和三维度列
> 是 Claude 设计意图, codex 可保留或调整.

| 维度 | Reference 普遍做法 (占位, 待 codex 调研重写) | HUAKAI delta (Claude 设计意图) | 三维度 |
|---|---|---|---|
| 触发点 | (待 codex 读 source 确认 reference 触发点策略) | 必须首次成功 billing event 才发 reward | 算法 |
| 审计 | (待 codex 调研 reference reward 透明度) | 走 F-AUDIT-1 receipt 让用户可验自己得了多少 | 生态 |
| 防篡改 | (待 codex 调研 reference 后台修改能力) | F-TRUST-1-B 签名 + ledger 不可改 | 算法 + 架构 |
| Anti-abuse | (待 codex 调研 reference anti-abuse 维度) | + F-FP-001 device fingerprint cross-check | 算法 |
| Tier 推广 | (待 codex 调研 reference tier 设计) | 累计阈值分 4 tier 解锁不同上限 | 算法 |

## 8 Sub-phase 拆分 (跟 codex round 1 plan 对齐, 给 codex round 2 执行)

| Sub | 内容 | 估时 |
|---|---|---|
| round-2-A | 读 plan + 锚 spec (voucher / receipt / auth) | 30 min |
| round-2-B | 读 ~/refs/ 3 reference paraphrase | 30 min |
| round-2-C | 写 spec docs/specs/community-invitation-referral.md | 2 hr |
| round-2-D | matrix sync (docs/03_FEATURE_PARITY_MATRIX.md 加行) | 15 min |

总 3-3.5 hr.

## 9 跟 codex round 1 plan 对照 (Claude vs Codex)

| 维度 | Claude plan (本文) | Codex round 1 plan |
|---|---|---|
| 数据表数 | 4 (invitations / referrals / referral_rewards / tier_progress) | 4 (一致) |
| API endpoint 数 | 5 | 5 (一致) |
| reward 触发点 | 首次成功 billing event | "新用户首次成功 billing event" (一致) |
| anti-abuse 三层 | FP + IP + email | "F-FP-001 + IP cooling + email" (一致) |
| invitation code 长度 | 8 位 Crockford base32 | "短随机字符串 (8 位)" (一致, Claude 加进制) |
| tier 阈值 | (3, 10, 50) | "累计 N 人解锁" (Claude 把 N 具体化) |
| 差异化维度 | 5 行 vs Sub2API (含 dimensions 列) | "至少 5 行" (一致) |

差异: Claude plan 加了 (a) 具体 base32 进制 + Crockford 选型 (避混淆字符), (b) tier 具体阈值 (3/10/50),
(c) AT 数定 20 条, (d) AT 列表细化到 cross-user 隔离 + tier 只升不降.

无冲突 — Claude plan 是 codex round 1 plan 的细化, 不是对立方案.

## 10 风险

- **R-COMM-001**: anti-abuse 触发率高于预期可能让 reward 派发率暴跌. Mitigation: 上线先开 "审计但不拒" 模式, 收集真实 abuse 数据, 一周后再切硬拒.
- **R-COMM-002**: tier 阈值定得太松可能成本溢出. Mitigation: tier reward 设月封顶 (gold = $50/月 voucher), 兜底防被刷穿.
- **R-COMM-003**: F-FP-001 还没实施 (Rust R-4 后), 第一版 anti-abuse 缺 device 维度. Mitigation: spec 标注 R-4 闭环后回写, 初版只跑 IP + email.

## 11 Owner 决策点 (留)

- Tier 阈值 (3 / 10 / 50) — 是否调整
- Reward 货币 (credit vs voucher) — 默 voucher (走 F-BILL-002)
- Per-tenant 月配额 (100) — 是否调整
- Tier 月封顶 ($X) — 待 Owner 拍

## 12 决议

Codex round 1 plan + Claude 本计划无冲突. 让 codex specifier round 2 用上面 §3-§7 当骨架写 spec.
所有 reference 行为 paraphrase, source cite 按 CLAUDE.md #12, 字段名 HUAKAI 自定不抄 reference.
