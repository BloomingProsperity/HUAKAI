import { describe, expect, it } from 'vitest'
import {
  buildCreateOrderRequest,
  buildExportRange,
  buildOrderListQuery,
  buildOutTradeNo,
  buildProviderConfig,
  canRefund,
  defaultExportRange,
  EMPTY_CREATE_ORDER_FORM,
  EMPTY_ORDER_FILTER,
  EXPORT_MAX_WINDOW_DAYS,
  formatCents,
  hasAnyAction,
  MAX_AMOUNT_CENTS,
  orderActions,
  parseAmountToCents,
  parseRefundAmount,
  providerKindLabel,
  refundRequestStatusLabel,
  refundRequestStatusTone,
  statusLabel,
  statusTone,
  toRfc3339,
  type CreateOrderForm,
  type OrderFilterForm,
} from './ordersadmin'

function filter(over: Partial<OrderFilterForm>): OrderFilterForm {
  return { ...EMPTY_ORDER_FILTER, ...over }
}

describe('buildOrderListQuery', () => {
  it('tenant_id 缺失 / 非正 → 报错(后端硬性必填,前端先拦)', () => {
    // 判别核心:无 tenant_id 必须报错。变异(允许空 tenant 通过)→ 此断言 RED。
    expect(buildOrderListQuery(filter({}), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
    expect(buildOrderListQuery(filter({ tenantId: '0' }), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
    expect(buildOrderListQuery(filter({ tenantId: '-3' }), 50, 0)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
  })

  it('仅 tenant_id 时,空筛选字段全部省略(不污染 query)', () => {
    const r = buildOrderListQuery(filter({ tenantId: '7' }), 50, 0)
    expect('query' in r).toBe(true)
    const q = (r as { query: Record<string, unknown> }).query
    expect(q).toEqual({ tenant_id: 7, limit: 50, offset: 0 })
    // 判别核心:status/user_id/时间为空时绝不出现在 query 里。
    expect('status' in q).toBe(false)
    expect('user_id' in q).toBe(false)
    expect('created_from' in q).toBe(false)
  })

  it('用户 ID 非正 → 报错', () => {
    expect(buildOrderListQuery(filter({ tenantId: '7', userId: '0' }), 50, 0)).toEqual({ error: '用户 ID 必须为正整数' })
  })

  it('齐全字段全部带上,时间转 RFC3339', () => {
    const r = buildOrderListQuery(
      filter({ tenantId: '7', userId: '9', status: 'pending', createdFrom: '2026-01-01T00:00' }),
      20,
      40,
    )
    const q = (r as { query: Record<string, unknown> }).query
    expect(q.tenant_id).toBe(7)
    expect(q.user_id).toBe(9)
    expect(q.status).toBe('pending')
    expect(q.limit).toBe(20)
    expect(q.offset).toBe(40)
    expect(String(q.created_from)).toMatch(/^\d{4}-\d{2}-\d{2}T.*Z$/)
  })
})

describe('orderActions 状态机', () => {
  it('pending 可确认+可取消,不可重试', () => {
    // 判别核心:pending 必须可取消。变异(canCancel 恒 false)→ RED。
    expect(orderActions('pending')).toEqual({ canConfirm: true, canCancel: true, canRetry: false })
  })

  it('paid 可确认+可重试,不可取消', () => {
    // 判别核心:paid 不可取消(撤单只针对未支付挂单)。变异(canCancel 恒 true)→ RED。
    expect(orderActions('paid')).toEqual({ canConfirm: true, canCancel: false, canRetry: true })
  })

  it('recharging 仅可重试', () => {
    expect(orderActions('recharging')).toEqual({ canConfirm: false, canCancel: false, canRetry: true })
  })

  it('终止态(completed/refunded/cancelled/failed/expired)无任何动作', () => {
    for (const s of ['completed', 'refunded', 'cancelled', 'failed', 'expired']) {
      expect(orderActions(s)).toEqual({ canConfirm: false, canCancel: false, canRetry: false })
      expect(hasAnyAction(s)).toBe(false)
    }
  })

  it('pending/paid/recharging 至少有一个动作', () => {
    expect(hasAnyAction('pending')).toBe(true)
    expect(hasAnyAction('paid')).toBe(true)
    expect(hasAnyAction('recharging')).toBe(true)
  })
})

describe('statusLabel / statusTone', () => {
  it('已知状态给中文,未知状态回落原值', () => {
    expect(statusLabel('completed')).toBe('已完成')
    expect(statusLabel('weird')).toBe('weird')
  })
  it('completed=ok,failed=danger', () => {
    // 判别核心:failed 必须 danger。变异(返回 ok)→ RED。
    expect(statusTone('completed')).toBe('ok')
    expect(statusTone('failed')).toBe('danger')
  })
})

describe('formatCents', () => {
  it('整数分转两位小数,无浮点误差', () => {
    // 判别核心:1999 cents = 19.99 而非 19.9899999。变异(用 /100 浮点)易现误差。
    expect(formatCents(1999, 'CNY')).toBe('19.99 CNY')
    expect(formatCents(5, 'CNY')).toBe('0.05 CNY')
    expect(formatCents(-1234, 'USD')).toBe('-12.34 USD')
  })
})

describe('toRfc3339', () => {
  it('空串 → 空串;非法 → 空串', () => {
    expect(toRfc3339('')).toBe('')
    expect(toRfc3339('not-a-date')).toBe('')
  })
})

describe('parseRefundAmount(money 敏感)', () => {
  it('空串=全额退,落到订单原额(maxCents)而非 0', () => {
    // 判别核心(S1 回归):后端 amount_cents<=0 即 ErrInvalidAmount(store_postgres_refund.go:64),
    // 没有 0=全额兜底。空输入必须解析为订单原额(正数),否则全额退实际发 0 → 后端 400 退款不可用。
    // 变异(空串返回 {amountCents:0})→ 本断言 RED。
    expect(parseRefundAmount('', 5000)).toEqual({ amountCents: 5000 })
    expect(parseRefundAmount('   ', 5000)).toEqual({ amountCents: 5000 })
  })

  it('空串但订单原额未知(maxCents<=0)→ 报错,绝不发 0', () => {
    // 变异(maxCents<=0 时仍返回 0)→ 本断言 RED;那正是会被后端 400 拒的坏路径。
    expect(parseRefundAmount('', 0)).toEqual({ error: '无法确定订单金额,请显式填写退款金额' })
  })

  it('元 → cents 用整数运算,无浮点误差', () => {
    // 判别核心:19.99 元必须是 1999 cents,而非 1998.9999…。变异(parseFloat*100)易现误差。
    expect(parseRefundAmount('19.99', 5000)).toEqual({ amountCents: 1999 })
    expect(parseRefundAmount('0.05', 5000)).toEqual({ amountCents: 5 })
    expect(parseRefundAmount('10', 5000)).toEqual({ amountCents: 1000 })
    expect(parseRefundAmount('0.1', 5000)).toEqual({ amountCents: 10 })
  })

  it('超过订单金额 → 报错(前端先拦 refund_exceeds_available)', () => {
    // 判别核心:超额必须拦。变异(删 maxCents 上限判断)→ 此断言 RED。
    expect(parseRefundAmount('60', 5000)).toEqual({ error: '退款金额不能超过订单金额' })
    // 恰好等于上限放行。
    expect(parseRefundAmount('50', 5000)).toEqual({ amountCents: 5000 })
  })

  it('非法格式 / 非正数 → 报错', () => {
    expect(parseRefundAmount('abc', 5000)).toEqual({
      error: '退款金额必须是非负数,最多两位小数',
    })
    expect(parseRefundAmount('1.999', 5000)).toEqual({
      error: '退款金额必须是非负数,最多两位小数',
    })
    expect(parseRefundAmount('-5', 5000)).toEqual({
      error: '退款金额必须是非负数,最多两位小数',
    })
    expect(parseRefundAmount('0', 5000)).toEqual({
      error: '退款金额必须大于 0(留空表示全额退款)',
    })
  })
})

describe('canRefund', () => {
  it('仅 completed 可退款(后端 ErrOrderNotRefundable)', () => {
    // 判别核心:只有 completed 可退。变异(paid 也放行)→ RED。
    expect(canRefund('completed')).toBe(true)
    for (const s of ['pending', 'paid', 'recharging', 'refunded', 'cancelled', 'failed']) {
      expect(canRefund(s)).toBe(false)
    }
  })
})

describe('refundRequestStatus 标签/语气', () => {
  it('中文标签 + 语气', () => {
    expect(refundRequestStatusLabel('pending')).toBe('待审批')
    expect(refundRequestStatusLabel('weird')).toBe('weird')
    // 判别核心:rejected 必须 danger,approved 必须 ok。变异(对调)→ RED。
    expect(refundRequestStatusTone('approved')).toBe('ok')
    expect(refundRequestStatusTone('pending')).toBe('warn')
    expect(refundRequestStatusTone('rejected')).toBe('danger')
  })
})

describe('buildExportRange', () => {
  it('from/to 必填(后端 required),空 → 报错', () => {
    // 判别核心:缺 from 或 to 必须报错(后端硬性必填)。变异(允许空)→ RED。
    expect(buildExportRange('', '2026-02-01T00:00')).toEqual({ error: '请选择有效的导出起始时间' })
    expect(buildExportRange('2026-01-01T00:00', '')).toEqual({ error: '请选择有效的导出截止时间' })
  })

  it('from 晚于 to → 报错', () => {
    expect(buildExportRange('2026-02-01T00:00', '2026-01-01T00:00')).toEqual({
      error: '起始时间不能晚于截止时间',
    })
  })

  it('跨度超 366 天 → 报错(后端 date_range_too_large)', () => {
    // 判别核心:超窗必须先拦。变异(删窗口判断)→ RED。
    const r = buildExportRange('2024-01-01T00:00', '2026-01-01T00:00')
    expect('error' in r).toBe(true)
  })

  it('合法窗 → 返回 RFC3339 from/to', () => {
    const r = buildExportRange('2026-01-01T00:00', '2026-01-31T00:00')
    expect('from' in r).toBe(true)
    const ok = r as { from: string; to: string }
    expect(ok.from).toMatch(/^\d{4}-\d{2}-\d{2}T.*Z$/)
    expect(ok.to).toMatch(/^\d{4}-\d{2}-\d{2}T.*Z$/)
  })

  it('EXPORT_MAX_WINDOW_DAYS 对齐后端 366', () => {
    expect(EXPORT_MAX_WINDOW_DAYS).toBe(366)
  })
})

describe('defaultExportRange', () => {
  it('给出近 30 天的 datetime-local 形态', () => {
    const now = new Date('2026-06-29T12:00:00')
    const r = defaultExportRange(now)
    // 判别核心:from 是 to 之前约 30 天;形态可被 buildExportRange 再消费(往返一致)。
    expect(r.to).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    expect(r.from).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/)
    const built = buildExportRange(r.from, r.to)
    expect('from' in built).toBe(true)
  })
})

describe('providerKindLabel', () => {
  it('已知渠道给中文,未知回落原值', () => {
    expect(providerKindLabel('manual')).toBe('手动充值(manual)')
    expect(providerKindLabel('taobao')).toBe('淘宝/闲鱼(taobao)')
    expect(providerKindLabel('alipay')).toBe('alipay')
  })
})

describe('buildProviderConfig', () => {
  it('checkout_url 留空合法(不设链接),enabled 透传', () => {
    // 判别核心:空 URL 必须放行且 checkoutUrl 归一为空串;变异(空也报错)→ RED。
    expect(buildProviderConfig({ enabled: true, checkoutUrl: '' })).toEqual({ enabled: true, checkoutUrl: '' })
    expect(buildProviderConfig({ enabled: false, checkoutUrl: '   ' })).toEqual({ enabled: false, checkoutUrl: '' })
  })

  it('非 http(s) 形态的 URL → 报错(前端体验护栏)', () => {
    // 判别核心:非法 URL 必须拦;变异(删 URL 形态校验)→ 此断言 RED。
    expect(buildProviderConfig({ enabled: true, checkoutUrl: 'item.taobao.com/x' })).toEqual({
      error: '跳转链接必须是 http(s):// 开头的有效 URL(留空表示不设链接)',
    })
    expect(buildProviderConfig({ enabled: true, checkoutUrl: 'javascript:alert(1)' })).toEqual({
      error: '跳转链接必须是 http(s):// 开头的有效 URL(留空表示不设链接)',
    })
  })

  it('合法 http(s) URL 透传(trim)', () => {
    expect(buildProviderConfig({ enabled: true, checkoutUrl: '  https://item.taobao.com/x  ' })).toEqual({
      enabled: true,
      checkoutUrl: 'https://item.taobao.com/x',
    })
    expect(buildProviderConfig({ enabled: false, checkoutUrl: 'http://pay.example/abc' })).toEqual({
      enabled: false,
      checkoutUrl: 'http://pay.example/abc',
    })
  })
})

describe('parseAmountToCents(代客建单 money)', () => {
  it('元 → 正整数分,无浮点误差', () => {
    // 判别核心:19.99 元=1999 分而非 1998.99…;变异(parseFloat*100)易现误差。
    expect(parseAmountToCents('19.99')).toEqual({ cents: 1999 })
    expect(parseAmountToCents('0.05')).toEqual({ cents: 5 })
    expect(parseAmountToCents('10')).toEqual({ cents: 1000 })
    expect(parseAmountToCents('0.1')).toEqual({ cents: 10 })
  })

  it('非正 / 非法格式 → 报错', () => {
    // 判别核心:0 与负数必须拒(后端 amount_cents<=0 即 ErrInvalidAmount)。变异(放行 0)→ RED。
    expect(parseAmountToCents('0')).toEqual({ error: '金额必须大于 0' })
    expect(parseAmountToCents('0.00')).toEqual({ error: '金额必须大于 0' })
    expect(parseAmountToCents('abc')).toEqual({ error: '金额必须是非负数,最多两位小数' })
    expect(parseAmountToCents('1.999')).toEqual({ error: '金额必须是非负数,最多两位小数' })
    expect(parseAmountToCents('-5')).toEqual({ error: '金额必须是非负数,最多两位小数' })
  })

  it('超账本上限 → 报错(后端 maxAmountCents 防溢出卡单)', () => {
    // 判别核心:超上限必须先拦;变异(删上限判断)→ 此断言 RED。
    const overWhole = String(MAX_AMOUNT_CENTS / 100 + 1) // 比上限多 1 元
    expect(parseAmountToCents(overWhole)).toEqual({ error: '金额超出账本可表示上限' })
  })
})

describe('buildOutTradeNo', () => {
  it('字符集仅 [A-Za-z0-9_-],非法 suffix 字符被剔除(后端 validateOutTradeNo)', () => {
    // 判别核心:out_trade_no 必须只含合法字符,否则后端 ErrInvalidInput;变异(原样拼接 suffix)→ RED。
    const n = buildOutTradeNo(1, 2, 1000, 'a/b c:d')
    expect(n).toMatch(/^[A-Za-z0-9_-]+$/)
    expect(n).toBe('admin-t1-u2-1000-abcd')
  })

  it('同一建单意图 + 同一 suffix → 同一稳定单号(幂等防双账)', () => {
    expect(buildOutTradeNo(3, 7, 500, 'x123')).toBe(buildOutTradeNo(3, 7, 500, 'x123'))
  })
})

function createForm(over: Partial<CreateOrderForm>): CreateOrderForm {
  return { ...EMPTY_CREATE_ORDER_FORM, ...over }
}

describe('buildCreateOrderRequest(代客建单 money)', () => {
  it('租户/用户/金额齐全 → 返回正整数分 + 合法单号 + 渠道', () => {
    const r = buildCreateOrderRequest(createForm({ tenantId: '1', userId: '9', amount: '10.50', providerKind: 'taobao' }), 1700000000000)
    expect('error' in r).toBe(false)
    const ok = r as { tenantId: number; userId: number; amountCents: number; outTradeNo: string; providerKind: string }
    expect(ok.tenantId).toBe(1)
    expect(ok.userId).toBe(9)
    expect(ok.amountCents).toBe(1050)
    expect(ok.providerKind).toBe('taobao')
    expect(ok.outTradeNo).toMatch(/^[A-Za-z0-9_-]+$/)
  })

  it('租户 ID 缺失 / 非正 → 报错(后端 ErrInvalidInput)', () => {
    // 判别核心:无 tenant 必须拦;变异(允许空 tenant)→ RED。
    expect(buildCreateOrderRequest(createForm({ userId: '9', amount: '10' }), 1)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
    expect(buildCreateOrderRequest(createForm({ tenantId: '0', userId: '9', amount: '10' }), 1)).toEqual({ error: '请填写有效的租户 ID(正整数)' })
  })

  it('用户 ID 缺失 / 非正 → 报错', () => {
    expect(buildCreateOrderRequest(createForm({ tenantId: '1', amount: '10' }), 1)).toEqual({ error: '请填写有效的用户 ID(正整数)' })
    expect(buildCreateOrderRequest(createForm({ tenantId: '1', userId: '-2', amount: '10' }), 1)).toEqual({ error: '请填写有效的用户 ID(正整数)' })
  })

  it('金额非正 → 报错(money 关键:绝不发 amount_cents<=0)', () => {
    // 判别核心:0 金额必须拦在前端(后端 ErrInvalidAmount);变异(放行 0)→ RED。
    expect(buildCreateOrderRequest(createForm({ tenantId: '1', userId: '9', amount: '0' }), 1)).toEqual({ error: '金额必须大于 0' })
  })

  it('nowMs 进入单号作为去重 suffix(不同时间戳 → 不同单号)', () => {
    const a = buildCreateOrderRequest(createForm({ tenantId: '1', userId: '9', amount: '10' }), 111)
    const b = buildCreateOrderRequest(createForm({ tenantId: '1', userId: '9', amount: '10' }), 222)
    const an = (a as { outTradeNo: string }).outTradeNo
    const bn = (b as { outTradeNo: string }).outTradeNo
    expect(an).not.toBe(bn)
  })

  it('渠道默认 manual(非法值回落 manual)', () => {
    const r = buildCreateOrderRequest(
      // @ts-expect-error 故意传非法渠道值,测回落
      createForm({ tenantId: '1', userId: '9', amount: '10', providerKind: 'weird' }),
      1,
    )
    expect((r as { providerKind: string }).providerKind).toBe('manual')
  })
})
