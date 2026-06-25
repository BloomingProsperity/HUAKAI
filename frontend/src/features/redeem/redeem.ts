import type { RedeemRequest, RedeemResult } from './types'

/*
 * 兑换码页纯逻辑(可单测, 不触 DOM / 不发请求)。
 * 职责:券码规范化 + 前端校验、idempotency_key 生成、金额格式化、
 *      兑换成功摘要、后端错误码 → 友好中文提示(含限流软提示)。
 */

/** 券码最大长度(防止误粘贴超长串打到后端; 后端自有更严校验, 这里只做体感)。 */
export const CODE_MAX_LEN = 64

/**
 * 规范化用户输入的券码:去首尾空白、去内部空白(粘贴常带空格/换行)、转大写。
 * 后端按指纹/哈希匹配, 大小写敏感与否以后端为准 —— 这里统一大写仅为展示一致与减少误差,
 * 不改变是否成功的判定(若后端区分大小写, 仍以后端结果为准)。
 */
export function normalizeCode(raw: string): string {
  return raw.replace(/\s+/g, '').toUpperCase()
}

/** 前端先校验券码:空 → 提示填写;超长 → 提示过长。通过返回 null。 */
export function validateCode(raw: string): string | null {
  const code = normalizeCode(raw)
  if (!code) return '请输入兑换码'
  if (code.length > CODE_MAX_LEN) return `兑换码不能超过 ${CODE_MAX_LEN} 个字符`
  return null
}

/**
 * 生成幂等键。优先用 crypto.randomUUID(浏览器原生), 不可用时回退到时间戳+随机串。
 * 同一次提交意图复用同一个 key, 后端凭此把重复点击/重发合并为一次入账(idempotent=true)。
 */
export function newIdempotencyKey(): string {
  const c = globalThis.crypto
  if (c && typeof c.randomUUID === 'function') {
    return c.randomUUID()
  }
  return `vr-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`
}

/**
 * 构造兑换请求体。idempotencyKey 由调用方持有(同一意图复用), 缺省时现生成一个。
 * 判别核心:必须带规范化后的 code 与非空 idempotency_key, 否则重复提交防护失效。
 */
export function buildRedeemRequest(rawCode: string, idempotencyKey?: string): RedeemRequest {
  return {
    code: normalizeCode(rawCode),
    idempotency_key: idempotencyKey || newIdempotencyKey(),
  }
}

/**
 * 分 → 货币展示串。amountCents 以「分」为单位, 除 100 取两位小数。
 * 判别核心:必须除以 100(变异成除 1 或除 1000 → 金额错位 → RED)。
 */
export function formatMoney(amountCents: number, currencyCode: string): string {
  const major = amountCents / 100
  const symbol = CURRENCY_SYMBOLS[currencyCode.toUpperCase()] ?? ''
  const text = major.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
  return symbol ? `${symbol}${text}` : `${text} ${currencyCode.toUpperCase()}`
}

const CURRENCY_SYMBOLS: Record<string, string> = {
  USD: '$',
  CNY: '¥',
  RMB: '¥',
  EUR: '€',
  GBP: '£',
}

/**
 * 兑换成功摘要(展示给用户的一句话)。
 * idempotent=true 时强调「此前已兑换, 未重复入账」, 避免用户以为白兑了再点。
 * 订阅券(subscription 非空)走订阅文案, 余额券报到账金额 + 当前余额。
 */
export function summarizeRedeem(result: RedeemResult): string {
  if (result.subscription) {
    const kind = result.subscription.result_kind === 'renewed' ? '续期' : '开通'
    const days = result.subscription.applied_validity_days
    const base = `订阅已${kind}, 有效期 +${days} 天`
    return result.idempotent ? `${base}(此前已兑换, 未重复授予)` : base
  }
  const credited = formatMoney(result.redemption.amount_cents, result.redemption.currency_code)
  const balance = formatMoney(result.balance_cents, result.voucher.currency_code || result.redemption.currency_code)
  if (result.idempotent) {
    return `此前已兑换该码, 未重复入账。当前余额 ${balance}`
  }
  return `兑换成功, 到账 ${credited}。当前余额 ${balance}`
}

/**
 * 后端兑换错误码 → 友好中文提示。
 * voucher_attempt_limited(HTTP 429, BurstLimiter)给出「稍后再试」的软提示而非冷冰冰报错。
 * 判别核心:限流码必须映射到含「稍后」的提示(变异成原样回显 code → 文案缺失 → RED)。
 */
export function friendlyRedeemError(code: string, fallbackMessage?: string): string {
  switch (code) {
    case 'voucher_attempt_limited':
      return '尝试过于频繁, 请稍后再试(已触发限流保护)'
    case 'voucher_not_found':
      return '兑换码无效, 请核对后重试'
    case 'voucher_expired':
      return '该兑换码已过期'
    case 'voucher_not_yet_valid':
      return '该兑换码尚未生效, 请稍后再试'
    case 'voucher_exhausted':
      return '该兑换码已被领完'
    case 'voucher_revoked':
      return '该兑换码已被作废'
    case 'voucher_wrong_user':
      return '该兑换码不属于当前账户'
    case 'voucher_already_redeemed':
      return '该兑换码已被你兑换过'
    case 'voucher_idempotency_conflict':
      return '请求冲突, 请刷新后重新兑换'
    case 'invalid_voucher_request':
      return '兑换码格式不正确'
    case 'session_token_required':
      return '登录状态已失效, 请重新登录后再兑换'
    case 'gateway_not_configured':
    case 'voucher_backend_error':
      return '兑换服务暂时不可用, 请稍后再试'
    default:
      return fallbackMessage || '兑换失败, 请稍后再试'
  }
}

/** 限流码判定:UI 据此把提示渲染成「等待」语气而非「错误」语气。 */
export function isRateLimited(code: string): boolean {
  return code === 'voucher_attempt_limited'
}
