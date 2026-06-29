import { describe, expect, it } from 'vitest'
import {
  buildListQuery,
  isDisputeStatus,
  isResolvable,
  OPERATOR_NOTE_MAX,
  shortDisputeID,
  statusLabel,
  statusTone,
  validateResolve,
} from './disputes'

/*
 * §14 变异测试:每个断言都能在对应缺陷被引入时打红。
 * 重点守护:列表 query 的 status 下发判别(money 列表过滤正确性)、
 * 裁决校验(status 合法枚举 + 备注上限 + tenant_id 正)、终态判定。
 */

describe('buildListQuery', () => {
  it('tenant_id/limit/offset 必带', () => {
    // 变异(漏带 tenant_id)→ 缺键,断言 RED。
    const q = buildListQuery(7, { status: '' }, 100, 20)
    expect(q.tenant_id).toBe(7)
    expect(q.limit).toBe(100)
    expect(q.offset).toBe(20)
  })
  it('status 为空串时不下发 status', () => {
    // 判别核心:空串本不该出现在 query;变异(去掉 !== \'\' 守卫)→ status 被设成 ""，断言 RED。
    const q = buildListQuery(1, { status: '' }, 50, 0)
    expect('status' in q).toBe(false)
  })
  it('status 为合法枚举时下发该值', () => {
    // 变异(把 if 分支删掉/恒不下发)→ q.status 为 undefined，断言 RED。
    const q = buildListQuery(1, { status: 'rejected' }, 50, 0)
    expect(q.status).toBe('rejected')
  })
})

describe('isDisputeStatus', () => {
  it('仅认四个合法枚举', () => {
    // 变异(放宽成恒 true)→ 'bogus' 也返回 true，末断言 RED。
    expect(isDisputeStatus('open')).toBe(true)
    expect(isDisputeStatus('reviewing')).toBe(true)
    expect(isDisputeStatus('resolved')).toBe(true)
    expect(isDisputeStatus('rejected')).toBe(true)
    expect(isDisputeStatus('bogus')).toBe(false)
    expect(isDisputeStatus('')).toBe(false)
  })
})

describe('validateResolve', () => {
  it('tenant_id 非正拒绝', () => {
    // 变异(去掉 tenantId<=0 守卫)→ 0 本应报错却放行，断言 RED。
    const r = validateResolve(0, 'resolved', '')
    expect(r.ok).toBe(false)
  })
  it('非法 status 拒绝(防止给后端发 invalid status)', () => {
    // 判别核心:'approve' 不是后端枚举;变异(用 status 直接放行不校验)→ ok 变 true，断言 RED。
    const r = validateResolve(1, 'approve', '')
    expect(r.ok).toBe(false)
  })
  it('合法裁决产出 trim 后的 body', () => {
    // 变异(漏 trim)→ operator_note 含首尾空白，第二断言 RED。
    const r = validateResolve(3, 'rejected', '  维持扣费  ')
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value).toEqual({ tenant_id: 3, status: 'rejected', operator_note: '维持扣费' })
    }
  })
  it('备注超上限拒绝、刚好等于上限放行(边界判别)', () => {
    // 判别核心:>4000 拒、==4000 放行;变异(阈值用 >= 或 >4001)→ 其一断言 RED。
    const over = validateResolve(1, 'resolved', 'x'.repeat(OPERATOR_NOTE_MAX + 1))
    expect(over.ok).toBe(false)
    const exact = validateResolve(1, 'resolved', 'x'.repeat(OPERATOR_NOTE_MAX))
    expect(exact.ok).toBe(true)
  })
})

describe('isResolvable', () => {
  it('open/reviewing 可裁决,resolved/rejected 已终态', () => {
    // 变异(恒 true)→ 'resolved' 也判可裁决，倒数第二断言 RED。
    expect(isResolvable('open')).toBe(true)
    expect(isResolvable('reviewing')).toBe(true)
    expect(isResolvable('resolved')).toBe(false)
    expect(isResolvable('rejected')).toBe(false)
  })
})

describe('statusTone / statusLabel', () => {
  it('终态语气区分:resolved=ok / rejected=danger', () => {
    // 判别核心:退款与驳回必须用不同语气,运营一眼可辨。
    // 变异(两者返回同一 tone)→ 断言 RED。
    expect(statusTone('resolved')).toBe('ok')
    expect(statusTone('rejected')).toBe('danger')
    expect(statusTone('open')).toBe('warn')
    expect(statusTone('reviewing')).toBe('info')
  })
  it('未知状态走中性 + 原样标签', () => {
    expect(statusTone('weird')).toBe('muted')
    expect(statusLabel('weird')).toBe('weird')
  })
  it('已裁决/已驳回标签互不相同(避免误判退款)', () => {
    // 变异(两标签写成同串)→ 断言 RED。
    expect(statusLabel('resolved')).not.toBe(statusLabel('rejected'))
  })
})

describe('shortDisputeID', () => {
  it('空回退破折号,长串缩写头尾', () => {
    expect(shortDisputeID('')).toBe('—')
    const long = 'disp_' + 'a'.repeat(40)
    const out = shortDisputeID(long)
    expect(out.includes('…')).toBe(true)
    expect(out.startsWith('disp_aaaaaaa')).toBe(true)
  })
})
