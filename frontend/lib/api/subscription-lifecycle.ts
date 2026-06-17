// 订阅生命周期 admin 写操作的纯逻辑层（零依赖 → 可直接 strip-types 单测）。
// 校验 + 请求体构造，逐条镜像后端 subscriptionhttp/subscription 真码不变式（禁止凭记忆）：
//   - extend：days>0 与 until 必须【恰好一个】(subscription/service.go ExtendSubscription：hasDays==hasUntil→ErrInvalidInput)。
//     注意：后端不接受「负天数缩短」，缩短须给绝对 until（更早时刻）。
//   - revoke：reason trim 后必须非空 (service.go RevokeSubscription：空 reason→ErrInvalidInput)。
//   - bulkAssign：user_ids 非空；后端逐用户软失败（userID≤0 出 error 项不整单失败），前端先剔无效再发。
//   - change-plan：admin 用 subscription_id，new_plan_id>0 (service.go ChangePlan)。
//   - 幂等：所有写操作带 X-Request-Id 头 (handler.go requestID→RequestID)；newRequestId 生成每次提交一枚键。
//
// 借鉴（CLEAN-ROOM，CLAUDE.md §11/§12/§16，仅功能/字段/动作形态，未抄码；融合 = 升级）：
//   - sub2api(LGPL) backend/internal/handler/admin/subscription_handler.go + service/subscription_service.go：
//     assign/bulk-assign(逐用户 status)/extend(delta 天)/reset-quota(日周月标志)/revoke(硬删) 动作集为主线。
//   - new-api(AGPL) controller/subscription.go：bind/invalidate/delete，无 extend/reset/bulk/change-plan/订阅券。
//   - CLIProxyAPI@21fad9db：纯中继，无订阅模块（无等价物）。
//   HUAKAI delta：cancel(软)与 revoke(硬+reason 必填)分立；缩短走绝对 until；change-plan 用户级换套餐(独有)；
//   统一 X-Request-Id 幂等。

// ── extend：days>0 XOR until ────────────────────────────────────────────

export interface ExtendInput {
  days?: number | null;
  until?: string | null; // RFC3339 绝对到期
}

// validateExtendInput：恰好一个（days>0 / until 非空）。镜像后端 hasDays==hasUntil→错。
// 返回错误文案；合法返回 null。
export function validateExtendInput(input: ExtendInput): string | null {
  const hasDays = input.days != null && input.days > 0;
  const hasUntil = input.until != null && input.until.trim() !== '';
  if (hasDays === hasUntil) {
    return '续期需且仅需提供「天数（>0）」或「到期时间」其一。';
  }
  return null;
}

// buildExtendBody：只放有效字段；缩短须用 until（后端不收负天数）。
export function buildExtendBody(tenantId: number, input: ExtendInput): Record<string, unknown> {
  const body: Record<string, unknown> = { tenant_id: tenantId };
  if (input.days != null && input.days > 0) body.days = input.days;
  if (input.until != null && input.until.trim() !== '') body.until = input.until.trim();
  return body;
}

// ── revoke：reason 必填 ─────────────────────────────────────────────────

export function validateRevokeReason(reason: string): string | null {
  if (reason.trim() === '') return '撤销订阅必须填写原因。';
  return null;
}

export function buildRevokeBody(tenantId: number, reason: string): Record<string, unknown> {
  return { tenant_id: tenantId, reason: reason.trim() };
}

// ── bulkAssign：解析多用户 ID ───────────────────────────────────────────

export type BulkUserIdsParse = { ids: number[]; error: null } | { ids: []; error: string };

// parseBulkUserIds：逗号/空白/换行分隔 → 正整数列表（去重保序）。任一非正整数 token 即整体报错；空列表报错。
export function parseBulkUserIds(raw: string): BulkUserIdsParse {
  const tokens = raw
    .split(/[\s,]+/)
    .map((t) => t.trim())
    .filter((t) => t !== '');
  const ids: number[] = [];
  const seen = new Set<number>();
  for (const tok of tokens) {
    const n = Number(tok);
    if (!Number.isInteger(n) || n <= 0) {
      return { ids: [], error: `无效的用户 ID：「${tok}」（需正整数）。` };
    }
    if (!seen.has(n)) {
      seen.add(n);
      ids.push(n);
    }
  }
  if (ids.length === 0) return { ids: [], error: '请至少输入一个用户 ID。' };
  return { ids, error: null };
}

export function buildBulkAssignBody(tenantId: number, userIds: number[], planId: number): Record<string, unknown> {
  return { tenant_id: tenantId, user_ids: userIds, plan_id: planId };
}

// ── change-plan：new_plan_id>0 ─────────────────────────────────────────

export function validateChangePlan(newPlanId: number): string | null {
  if (!Number.isInteger(newPlanId) || newPlanId <= 0) return '请选择有效的目标套餐。';
  return null;
}

// buildChangePlanBody：allow_downgrade 仅在 true 时带（省略=不允许降级，对齐后端零值 false）。
export function buildChangePlanBody(
  tenantId: number,
  newPlanId: number,
  allowDowngrade?: boolean,
): Record<string, unknown> {
  const body: Record<string, unknown> = { tenant_id: tenantId, new_plan_id: newPlanId };
  if (allowDowngrade) body.allow_downgrade = true;
  return body;
}

// ── 订阅券：grant_kind=subscription（端点强制，不由客户端传）──────────────

export interface SubscriptionVoucherInput {
  tenant_id: number;
  plan_id: number;
  amount_cents: number; // 名义价（信息性，兑换时不入余额）
  valid_from: string; // RFC3339
  valid_until: string; // RFC3339
  code?: string;
  currency_code?: string;
  max_redemptions?: number;
  single_use_per_user?: boolean;
  eligible_user_id?: number;
}

// buildSubscriptionVoucherBody：必填 tenant_id/plan_id/amount_cents/valid_from/valid_until；其余省空值。
export function buildSubscriptionVoucherBody(input: SubscriptionVoucherInput): Record<string, unknown> {
  const body: Record<string, unknown> = {
    tenant_id: input.tenant_id,
    plan_id: input.plan_id,
    amount_cents: input.amount_cents,
    valid_from: input.valid_from,
    valid_until: input.valid_until,
  };
  if (input.code && input.code.trim() !== '') body.code = input.code.trim();
  if (input.currency_code && input.currency_code.trim() !== '') body.currency_code = input.currency_code.trim();
  if (input.max_redemptions != null && input.max_redemptions > 0) body.max_redemptions = input.max_redemptions;
  if (input.single_use_per_user != null) body.single_use_per_user = input.single_use_per_user;
  if (input.eligible_user_id != null && input.eligible_user_id > 0) body.eligible_user_id = input.eligible_user_id;
  return body;
}

// ── 幂等键：每次提交一枚 X-Request-Id ───────────────────────────────────

// newRequestId：优先 crypto.randomUUID；无则退化为时间戳+随机（唯一即可，幂等键不需密码学强度）。
export function newRequestId(): string {
  const g = globalThis as unknown as { crypto?: { randomUUID?: () => string } };
  if (g.crypto && typeof g.crypto.randomUUID === 'function') {
    return g.crypto.randomUUID();
  }
  return `req-${Date.now()}-${Math.floor(Math.random() * 1e9).toString(36)}`;
}
