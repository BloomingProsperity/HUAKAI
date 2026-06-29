import { describe, expect, it } from 'vitest'
import { formatAdoptionRate, validateGroup, validateRemark } from './actions'

describe('formatAdoptionRate', () => {
  it('total>0 给四舍五入百分比', () => {
    // 判别核心:3/4=0.75 → "75%"。变异(去掉 *100 或漏 round)→ 断言 RED。
    expect(formatAdoptionRate({ enabled_users: 3, total_users: 4, enabled_rate: 0.75 })).toBe('75%')
    expect(formatAdoptionRate({ enabled_users: 1, total_users: 3, enabled_rate: 1 / 3 })).toBe('33%')
  })
  it('无用户(total=0)回退破折号,不漏 NaN%', () => {
    // 变异(去掉 total<=0 守卫)→ 0/0=NaN 会渲染 "NaN%",此断言 RED。
    expect(formatAdoptionRate({ enabled_users: 0, total_users: 0, enabled_rate: NaN })).toBe('—')
  })
})

describe('validateGroup', () => {
  it('空拒绝、超 64 拒绝、合法过', () => {
    // 变异(把 g===\'\' 守卫删掉)→ 空串本应报错却返回 null,首断言 RED。
    expect(validateGroup('')).toBe('用户组不能为空')
    expect(validateGroup('   ')).toBe('用户组不能为空')
    expect(validateGroup('a'.repeat(65))).toBe('用户组最多 64 字符')
    expect(validateGroup('vip')).toBeNull()
  })
})

describe('validateRemark', () => {
  it('超 1024 拒绝、空与正常放行', () => {
    // 变异(阈值改成 >1025 或删守卫)→ 1025 长度本应报错却放行,首断言 RED。
    expect(validateRemark('x'.repeat(1025))).toBe('备注最多 1024 字符')
    expect(validateRemark('')).toBeNull()
    expect(validateRemark('正常备注')).toBeNull()
  })
})
