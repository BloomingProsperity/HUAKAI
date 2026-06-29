import { describe, expect, it } from 'vitest'
import {
  buildPurchaseRequest,
  cancelRenewGuidance,
  capLabel,
  changeablePlans,
  clampBarPercent,
  formatCaps,
  formatPrice,
  formatResetCountdown,
  formatValidity,
  friendlyChangePlanError,
  isOverLimit,
  isSubscriptionActive,
  purchaseGuidance,
  sortProgressWindows,
  subscriptionStatusLabel,
  subscriptionStatusTone,
  validateChangePlan,
  validatePurchasable,
  windowLabel,
} from './subscriptions'
import type { SubscriptionProgressView, SubscriptionView } from './types'

describe('formatPrice', () => {
  it('分 → 两位小数(除以 100)', () => {
    // 判别核心:1999 分 = $19.99。变异成除 1 或除 1000 → RED。
    expect(formatPrice(1999, 'USD')).toBe('$19.99')
  })
  it('CNY 用 ¥ 符号', () => {
    expect(formatPrice(5000, 'CNY')).toBe('¥50.00')
  })
  it('未知币种回退为「数额 币种」', () => {
    expect(formatPrice(1234, 'JPY')).toBe('12.34 JPY')
  })
})

describe('formatValidity', () => {
  it('正数 → N 天', () => {
    expect(formatValidity(30)).toBe('30 天')
  })
  it('0/负数 → 不限期', () => {
    // 判别核心:0 必须当不限期(变异成 0 天 → 误导用户立即到期 → RED)。
    expect(formatValidity(0)).toBe('不限期')
  })
})

describe('capLabel / formatCaps', () => {
  it('空/null → 不限', () => {
    expect(capLabel(null)).toBe('不限')
    expect(capLabel('')).toBe('不限')
  })
  it('有值 → $ 前缀', () => {
    // 判别核心:必须带 $ 且原样保留后端小数串(变异成丢 $ 或重算 → RED)。
    expect(capLabel('12.50000000')).toBe('$12.50000000')
  })
  it('三档一并格式化', () => {
    const caps = formatCaps({ daily_cap_usd: '5', weekly_cap_usd: null, monthly_cap_usd: '100' })
    expect(caps.daily).toBe('$5')
    expect(caps.weekly).toBe('不限')
    expect(caps.monthly).toBe('$100')
  })
})

describe('buildPurchaseRequest', () => {
  it('planId 原样落到 plan_id', () => {
    // 判别核心:必须落 plan_id=42(变异成 0 或漏字段 → 后端 invalid_plan → RED)。
    expect(buildPurchaseRequest(42)).toEqual({ plan_id: 42 })
  })
})

describe('validatePurchasable', () => {
  it('可购 → null', () => {
    expect(validatePurchasable({ id: 1, enabled: true, for_sale: true })).toBeNull()
  })
  it('停用 → 提示', () => {
    expect(validatePurchasable({ id: 1, enabled: false, for_sale: true })).toContain('停用')
  })
  it('不可售 → 提示', () => {
    // 判别核心:for_sale=false 必须拦下(变异成放行 → 用户对不可售套餐下单 → RED)。
    expect(validatePurchasable({ id: 1, enabled: true, for_sale: false })).toContain('不可购买')
  })
  it('非法 id → 提示', () => {
    expect(validatePurchasable({ id: 0, enabled: true, for_sale: true })).toContain('无效')
  })
})

describe('clampBarPercent', () => {
  it('普通值四舍五入', () => {
    expect(clampBarPercent(42.6)).toBe(43)
  })
  it('超过 100 夹到 100', () => {
    // 判别核心:超额时进度条不能溢出(变异成不夹 → 返回 150 → RED)。
    expect(clampBarPercent(150)).toBe(100)
  })
  it('负数/非有限 夹到 0', () => {
    expect(clampBarPercent(-5)).toBe(0)
    expect(clampBarPercent(Number.NaN)).toBe(0)
  })
})

describe('formatResetCountdown', () => {
  it('一天以上 → 天 + 小时', () => {
    // 判别核心:1 天 = 86400 秒;90000 秒 = 1 天 1 小时(变异成 3600 切分 → 天数错位 → RED)。
    expect(formatResetCountdown(90000)).toBe('1 天 1 小时后重置')
  })
  it('小时级', () => {
    expect(formatResetCountdown(7200)).toBe('2 小时 0 分后重置')
  })
  it('分钟级', () => {
    expect(formatResetCountdown(120)).toBe('2 分后重置')
  })
  it('0/负数 → 即将重置', () => {
    expect(formatResetCountdown(0)).toBe('即将重置')
  })
})

describe('windowLabel', () => {
  it('三种窗口中文', () => {
    expect(windowLabel('calendar_day')).toBe('当日')
    expect(windowLabel('calendar_week')).toBe('本周')
    expect(windowLabel('calendar_month')).toBe('本月')
  })
  it('未知原样回显', () => {
    expect(windowLabel('calendar_year')).toBe('calendar_year')
  })
})

function progressRow(kind: string): SubscriptionProgressView {
  return {
    window_kind: kind,
    cap: '10',
    consumed: '5',
    remaining: '5',
    overage: '0',
    request_count: 0,
    window_start: '2026-06-25T00:00:00Z',
    window_end: '2026-06-26T00:00:00Z',
    usage_percent: 50,
    resets_in_seconds: 3600,
    over_limit: false,
    over_limit_amount: '0',
  }
}

describe('sortProgressWindows', () => {
  it('日→周→月,未知排最后', () => {
    // 判别核心:乱序输入要被排成 day,week,month,未知 type 殿后(变异成排最前 → RED)。
    const rows = [progressRow('calendar_month'), progressRow('zzz'), progressRow('calendar_day'), progressRow('calendar_week')]
    const sorted = sortProgressWindows(rows).map((r) => r.window_kind)
    expect(sorted).toEqual(['calendar_day', 'calendar_week', 'calendar_month', 'zzz'])
  })
  it('不改原数组', () => {
    const rows = [progressRow('calendar_month'), progressRow('calendar_day')]
    sortProgressWindows(rows)
    expect(rows[0].window_kind).toBe('calendar_month')
  })
})

describe('subscriptionStatusLabel / Tone', () => {
  it('生效中 → ok', () => {
    expect(subscriptionStatusLabel('active')).toBe('生效中')
    expect(subscriptionStatusTone('active')).toBe('ok')
  })
  it('已过期 → danger', () => {
    // 判别核心:过期必须是 danger 语气(变异成 ok → 用户误以为仍可用 → RED)。
    expect(subscriptionStatusLabel('expired')).toBe('已过期')
    expect(subscriptionStatusTone('expired')).toBe('danger')
  })
  it('待生效 → warn', () => {
    expect(subscriptionStatusTone('pending')).toBe('warn')
  })
})

describe('isOverLimit', () => {
  it('over_limit=true 即超额', () => {
    // 判别核心:over_limit 标志优先, 即便百分比未到 100(变异成只看百分比 → 漏判 → RED)。
    expect(isOverLimit({ over_limit: true, usage_percent: 80 })).toBe(true)
  })
  it('百分比 >100 兜底', () => {
    expect(isOverLimit({ over_limit: false, usage_percent: 120 })).toBe(true)
  })
  it('未超额 → false', () => {
    expect(isOverLimit({ over_limit: false, usage_percent: 50 })).toBe(false)
  })
})

describe('purchaseGuidance', () => {
  it('幂等命中:强调无需重复下单', () => {
    // 判别核心:idempotent=true 不能再说「订单已创建」(会误导用户重复支付)。变异成忽略 idempotent → RED。
    const g = purchaseGuidance('T123', true)
    expect(g).toContain('无需重复下单')
    expect(g).not.toContain('订单已创建')
  })
  it('新建单:含支付指引', () => {
    const g = purchaseGuidance('T999', false)
    expect(g).toContain('T999')
    expect(g).toContain('支付')
  })
})

describe('isSubscriptionActive', () => {
  const sub: SubscriptionView = {
    id: 1,
    plan_id: 2,
    status: 'active',
    starts_at: '2026-06-01T00:00:00Z',
    expires_at: '2026-07-01T00:00:00Z',
    created_at: '2026-06-01T00:00:00Z',
  }
  it('active → true', () => {
    expect(isSubscriptionActive(sub)).toBe(true)
  })
  it('null → false', () => {
    expect(isSubscriptionActive(null)).toBe(false)
  })
  it('expired → false', () => {
    // 判别核心:非 active 必须 false(变异成只判 null → 过期订阅被当生效 → RED)。
    expect(isSubscriptionActive({ ...sub, status: 'expired' })).toBe(false)
  })
})

describe('changeablePlans', () => {
  const plans = [
    { id: 1, enabled: true, for_sale: true },
    { id: 2, enabled: true, for_sale: true }, // 当前套餐
    { id: 3, enabled: false, for_sale: true }, // 停用
    { id: 4, enabled: true, for_sale: false }, // 不可售
  ]
  it('剔除当前套餐 + 只留可购', () => {
    // 判别核心:id=2(当前)被排除,id=3/4(停用/不可售)被过滤,只剩 id=1。
    // 变异(不排除当前)→ 含 id=2 → RED;变异(不过滤不可售)→ 含 id=4 → RED。
    expect(changeablePlans(plans, 2).map((p) => p.id)).toEqual([1])
  })
  it('无当前订阅(currentPlanId 为 null)时不排除任何套餐(仅按可购过滤)', () => {
    expect(changeablePlans(plans, null).map((p) => p.id)).toEqual([1, 2])
  })
})

describe('validateChangePlan', () => {
  it('合法目标 → null', () => {
    expect(validateChangePlan(3, 2)).toBeNull()
  })
  it('与当前套餐相同 → 拦下', () => {
    // 判别核心:同档换无意义且后端会拒,前端必须先拦(变异成放行 → 发无效请求 → RED)。
    expect(validateChangePlan(2, 2)).toContain('当前套餐')
  })
  it('非法 id → 提示选择', () => {
    expect(validateChangePlan(0, 2)).toContain('选择')
  })
})

describe('cancelRenewGuidance', () => {
  it('强调到期保留、不立即失效', () => {
    // 判别核心:必须含「到期」,不能含「立即/失效」误导词(变异成误导文案 → RED)。
    const g = cancelRenewGuidance('2026-07-01T00:00:00Z')
    expect(g).toContain('到期')
    expect(g).toContain('不再自动续费')
    expect(g).not.toContain('立即失效')
  })
})

describe('friendlyChangePlanError', () => {
  it('降级码 → 引导联系管理员', () => {
    expect(friendlyChangePlanError('downgrade_not_allowed')).toContain('降级')
  })
  it('未知码 → 回退文案', () => {
    expect(friendlyChangePlanError('boom', '兜底')).toBe('兜底')
  })
})
