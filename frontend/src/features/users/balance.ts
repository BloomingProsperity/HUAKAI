/*
 * 管理员手动调额纯逻辑(可单测,无 DOM/网络副作用)。money 敏感:
 * 这里把后端 balance_credit_handler.go + payment/admin_credit.go 的硬约束镜像到前端,
 * 提前拦掉无谓 400 并保证金额/方向语义正确。后端仍是权威。
 *
 * 后端约束来源(production 码):
 *   - 金额非零、reason 非空、idempotency_key 非空                (balance_credit_handler.go:82)
 *   - amount 符号即方向:正=加款(credit)、负=扣款(debit)        (admin_credit.go:104 负数走 debit 分支)
 *   - debit 当前被 ErrAdminDebitNotSupported 拒(400)             (admin_credit.go:104-106)
 *   - 金额最多 2 位小数、>0 的绝对值、≤ maxAmountCents(10 亿美元) (order.go:142 decimalAmountToCents)
 *   - currency 仅支持 USD                                         (admin_credit.go:216)
 *   - tenant_id / user_id 必须为正                                (admin_credit.go:209)
 */

export type AdjustDirection = 'credit' | 'debit'

/** 金额绝对值上限(美元),镜像后端 maxAmountCents=100_000_000_000 分 = 10 亿美元(types.go:19)。 */
export const MAX_ADJUST_AMOUNT_USD = 1_000_000_000

/**
 * 生成幂等键。优先 crypto.randomUUID(浏览器原生),不可用时回退时间戳+随机串。
 * 同一次提交意图复用同一 key,后端凭此把重复点击/重发合并为一次入账。
 */
export function newAdjustmentKey(): string {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID()
  }
  return `ba-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/** 方向 → 中文标签。 */
export function directionLabel(dir: AdjustDirection): string {
  return dir === 'credit' ? '加款' : '扣款'
}

/** 校验金额输入(字符串)结果:ok 时携带归一化的正数金额串(绝对值,2 位小数内)。 */
export type AmountValidation =
  | { ok: true; magnitude: string }
  | { ok: false; error: string }

/**
 * 校验金额输入(用户只输正的绝对值,方向由单独的 direction 决定)。
 * 判别核心(逐条均可变异打红):
 *   - 必须是正的十进制数(>0):空/0/负号/非数字一律拒                → 变异(放行 0/负)即 RED
 *   - 小数位 ≤ 2(后端 cents 粒度,order.go:146 Truncate(2) 不等即 400)→ 变异(放宽到 3 位)即 RED
 *   - ≤ MAX_ADJUST_AMOUNT_USD                                       → 变异(去掉上限)即 RED
 */
export function validateAmount(raw: string): AmountValidation {
  const v = raw.trim()
  // 仅接受十进制正数;前导/尾随符号、科学计数、负号一律拒。
  if (!/^[0-9]+(\.[0-9]+)?$/.test(v)) {
    return { ok: false, error: '金额必须是正的十进制数(如 10 或 9.99)' }
  }
  const n = Number(v)
  if (!Number.isFinite(n) || n <= 0) {
    return { ok: false, error: '金额必须大于 0' }
  }
  // 小数位上限 2(美分粒度);多于 2 位后端会判 invalid。
  const dot = v.indexOf('.')
  if (dot >= 0 && v.length - dot - 1 > 2) {
    return { ok: false, error: '金额最多 2 位小数(美分粒度)' }
  }
  if (n > MAX_ADJUST_AMOUNT_USD) {
    return { ok: false, error: `单次金额不能超过 ${MAX_ADJUST_AMOUNT_USD} 美元` }
  }
  return { ok: true, magnitude: v }
}

/** 校验原因(审计必填):trim 后非空。返回错误文案或 null。 */
export function validateReason(reason: string): string | null {
  // 判别核心:reason 是审计字段,后端必填(handler.go:82 Reason==""→400);空则拒。
  if (reason.trim() === '') return '请填写调额原因(用于审计,必填)'
  return null
}

/** 整体校验输入:tenant_id / user_id 为正、金额合法、原因非空。 */
export type AdjustValidation =
  | { ok: true; signedAmount: string; magnitude: string }
  | { ok: false; error: string }

/**
 * 校验一次调额意图并产出带符号的金额串。
 * 判别核心:
 *   - direction==='debit' 时金额加负号(符号即方向,admin_credit.go:104)→ 变异(永远不加负号)即 RED
 *   - tenant_id/user_id 必须为正                                       → 变异(放行 <=0)即 RED
 */
export function validateAdjustment(
  tenantId: number,
  userId: number,
  direction: AdjustDirection,
  rawAmount: string,
  reason: string,
): AdjustValidation {
  if (!Number.isInteger(tenantId) || tenantId <= 0) {
    return { ok: false, error: 'tenant_id 必须为正整数' }
  }
  if (!Number.isInteger(userId) || userId <= 0) {
    return { ok: false, error: 'user_id 必须为正整数' }
  }
  const amt = validateAmount(rawAmount)
  if (!amt.ok) return amt
  const reasonErr = validateReason(reason)
  if (reasonErr) return { ok: false, error: reasonErr }
  // 符号即方向:扣款取负,加款保持正。
  const signed = direction === 'debit' ? `-${amt.magnitude}` : amt.magnitude
  return { ok: true, signedAmount: signed, magnitude: amt.magnitude }
}

/**
 * 构造调额请求体。signedAmount 须已带方向符号(由 validateAdjustment 产出);
 * idempotencyKey 由调用方持有(同一意图复用),缺省时现生成。
 * 判别核心:必须把带符号的 amount、tenant_id、user_id、reason、非空 idempotency_key
 * 全部带上,缺一即重复提交防护/方向语义失效。
 */
export function buildAdjustmentRequest(
  tenantId: number,
  userId: number,
  signedAmount: string,
  reason: string,
  idempotencyKey?: string,
): import('./types').BalanceAdjustmentRequest {
  return {
    tenant_id: tenantId,
    user_id: userId,
    amount: signedAmount,
    reason: reason.trim(),
    idempotency_key: idempotencyKey || newAdjustmentKey(),
  }
}
