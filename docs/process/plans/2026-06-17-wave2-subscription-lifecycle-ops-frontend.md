# Wave2 切片计划 — 订阅生命周期 admin 写操作（前端接线）

日期：2026-06-17 · Lane：Claude PM 自驱 · 风险：低（纯前端接线，接已存在后端端点，无 schema/money/auth 核心改动）

## 背景

Wave1（登录方式补全）已收官（PR#8–#13）。转 Wave2 admin 后台补全。本切片取「最小且高价值」起步：
订阅生命周期 admin 写操作。后端 `subscriptionhttp` 全部端点 done-active，前端 `admin/operations` 仅接了
`assignSubscription`（单个指派）+ 套餐 CRUD，缺生命周期写操作。

## 真契约（已读后端真码，禁止凭记忆）

前缀 `/v1/admin/subscriptions`，admin token 轨（`huakai_admin_token` Bearer），`resolveAdmin` 强制
`platform_admin`，`tenant_id` 取自 body。幂等键 = `X-Request-Id` 请求头（`handler.go:679` → `requestID(r)`）。

| 操作 | 方法+路径 | 请求体 | 响应 | 关键校验（service.go） |
|---|---|---|---|---|
| cancel | POST `/assignments/{id}/cancel` | `{tenant_id}` | `{subscription}` 200 | tenant_id>0 & sub_id>0 |
| extend | POST `/assignments/{id}/extend` | `{tenant_id, days?, until?}` | `{subscription}` 200 | **days>0 XOR until**（`hasDays==hasUntil→错`，service.go:216-220）|
| reset-quota | POST `/assignments/{id}/reset-quota` | `{tenant_id}` | `{subscription}` 200 | 重建全部窗口（无 per-window 标志）|
| change-plan | POST `/assignments/{id}/change-plan` | `{tenant_id, new_plan_id, allow_downgrade?}` | `{subscription}` 200 | new_plan_id>0；admin 用 sub_id |
| revoke | POST `/assignments/{id}/revoke` | `{tenant_id, reason}` | `{subscription}` 200 | **reason 必填**（trim 后非空，service.go:282-285）|
| bulkAssign | POST `/assignments/bulk` | `{tenant_id, user_ids[], plan_id}` | `{results:[{user_id,ok,error?,idempotent?,subscription?}]}` 200 | 逐用户软失败（userID≤0 出 error 项不整单失败）|
| 订阅券 | POST `/vouchers` | `{tenant_id, plan_id, amount_cents, valid_from, valid_until, code?, currency_code?, max_redemptions?, single_use_per_user?, eligible_user_id?}` | `{voucher, code}` 201 | 先 GetPlan 确认套餐存在 |

## 三家对照（已 specifier lane 实读 ~/refs，CLAUDE.md §11/§12/§16；融合，未抄码）

- **sub2api**（LGPL，tiebreaker §16，最全）：assign / bulk-assign（逐用户 status map）/ extend（delta 天，可负=缩短）/
  reset-quota（daily/weekly/monthly 三标志可选）/ revoke=硬删（无 reason）/ redeem-codes generate（type=subscription, group_id, validity_days）。**无 change-plan**。幂等仅单个 assign/extend。
- **new-api**（AGPL）：bind / create-user-subscription / invalidate（软）/ delete（硬）/ plan 配置更新。无 extend/reset-quota/bulk/change-plan/revoke-reason/订阅券。
- **CLIProxyAPI**（纯中继）：无订阅模块（无等价物，源码已证）。

### HUAKAI fusion delta（融合即升级，三维度）

| 能力 | sub2api | new-api | HUAKAI delta | 维度 |
|---|---|---|---|---|
| cancel vs revoke | 合并为一个硬删 | invalidate(软)/delete(硬) 二分但都无 reason | **cancel(软,状态迁移+降级) 与 revoke(硬结+reason 必填)分立** | 架构+生态 |
| extend | delta 天(可负缩短) | 无 | days>0 **XOR** 绝对 until（缩短走 until 而非负天）| 架构 |
| reset-quota | per-window 三标志 | 无 | 单操作重建全部窗口（更粗；per-window 列 roadmap）| 算法 |
| change-plan | 无 | 无 | **HUAKAI 独有**用户级换套餐(sub_id XOR user_id)| 架构+算法 |
| bulk-assign | 逐用户 status map | 无 | parity + **统一 X-Request-Id 幂等** | 生态 |
| 订阅券 | redeem-codes generate | 无 | 复用 voucher 子系统(grant_kind=subscription) | 架构 |

### 诚实 roadmap（Feature Preservation，非静默丢弃）

- sub2api reset-quota 支持 per-window（仅重置日/周/月之一）；HUAKAI 当前重置全部窗口 → 记 roadmap「per-window 选择性重置」（HUAKAI 是超集，缺的是更细控制）。
- sub2api redeem `create-and-redeem` 一步原子（建券即兑给某用户）；HUAKAI 建/兑分离 → 记 roadmap。

## 改动（3 文件 + 1 测试）

1. **新建 `frontend/lib/api/subscription-lifecycle.ts`**（零依赖纯逻辑，可直接 strip-types 单测）：
   `validateExtendInput`（days>0 XOR until）、`validateRevokeReason`、`parseBulkUserIds`、`validateChangePlan`、
   `buildExtendBody/buildRevokeBody/buildBulkAssignBody/buildChangePlanBody/buildSubscriptionVoucherBody`、`newRequestId`。
2. **扩 `frontend/lib/api/adminOperations.ts`**：本地 `adminPostIdem`（POST+X-Request-Id 头）+ 7 个 client fn + `BulkAssignResult` 类型。
3. **扩 `frontend/app/admin/operations/page.tsx`**：订阅生命周期面板（按 user 查 → 逐行 续期/重置/改套餐/取消/撤销）+ 套餐行 批量指派/生成订阅券。

## 强测试（CLAUDE.md §14，变异验证）

`frontend/lib/api/subscription-lifecycle.test.ts`：
- 直接单测纯逻辑（判别性 fixture）：extend XOR（both→错/neither→错/单边→过）、revoke reason 必填、bulk 解析、change-plan>0、body builder 收正确字段。
- 源文本接线断言 adminOperations.ts：各端点路径 + `X-Request-Id` 头（fnBody 切到首个 `\n}`，避免圈进邻函数注释）。
- 每条 mutation 实测转红再还原。

## 成功判据

- `tsc --noEmit` 干净；`node --experimental-strip-types --test` 全绿；每测变异红验证。
- 开 PR squash 合并入 feat/frontend-portal，清 worktree + 释放 coordination 锁。

## blast radius / 风险

- 纯前端、低风险；不碰后端/schema/auth 核心。浏览器实操（真打端点）需部署后手测（本地无 admin token/真库），逻辑层用单测+源文本接线兜住。
