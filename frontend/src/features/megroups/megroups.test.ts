import { describe, expect, it } from 'vitest'
import { formatRatio, mapMeGroupTableRows, ratioDisplay, ratioTone, userGroupLabel } from './megroups'

describe('formatRatio', () => {
  it('去尾零收敛("1.50000000"→"1.5")', () => {
    expect(formatRatio('1.50000000')).toBe('1.5')
    expect(formatRatio('2.00000000')).toBe('2')
    expect(formatRatio('0.80000000')).toBe('0.8')
  })
  it('非数字原样 trim,空给空', () => {
    expect(formatRatio('  abc ')).toBe('abc')
    expect(formatRatio('   ')).toBe('')
  })
})

describe('ratioDisplay(后端不泄露策略)', () => {
  it('公开且有倍率 → "N×"', () => {
    expect(ratioDisplay({ ratio: '1.50000000', has_public_ratio: true })).toBe('1.5×')
  })
  it('未公开 → "未公开"(即便 ratio 不存在也不崩)', () => {
    expect(ratioDisplay({ has_public_ratio: false })).toBe('未公开')
    expect(ratioDisplay({ ratio: undefined, has_public_ratio: false })).toBe('未公开')
  })
  it('安全护栏:未公开但 ratio 有值 → 仍「未公开」,绝不泄露(后端正常会省略 ratio,前端防御纵深)', () => {
    // 这是 has_public_ratio 短路护栏专属保护的 case:即便 ratio 带值也不得显示
    expect(ratioDisplay({ ratio: '1.50000000', has_public_ratio: false })).toBe('未公开')
  })
  it('防御:标记公开但 ratio 缺失/空 → 仍显未公开(不显空 ×,不臆造默认)', () => {
    expect(ratioDisplay({ has_public_ratio: true })).toBe('未公开')
    expect(ratioDisplay({ ratio: '   ', has_public_ratio: true })).toBe('未公开')
  })
})

describe('ratioTone', () => {
  it('公开有倍率 → info,否则 muted', () => {
    expect(ratioTone({ ratio: '1.5', has_public_ratio: true })).toBe('info')
    expect(ratioTone({ has_public_ratio: false })).toBe('muted')
    expect(ratioTone({ has_public_ratio: true })).toBe('muted')
  })
})

describe('userGroupLabel', () => {
  it('内建等级中文,自定义原样,空给默认', () => {
    expect(userGroupLabel('default')).toBe('默认等级')
    expect(userGroupLabel('VIP')).toBe('VIP')
    expect(userGroupLabel('enterprise')).toBe('企业版')
    expect(userGroupLabel('team_gold')).toBe('team_gold')
    expect(userGroupLabel('')).toBe('默认等级')
    expect(userGroupLabel('   ')).toBe('默认等级')
  })
})

describe('mapMeGroupTableRows', () => {
  it('名称、ID、倍率与语气严格对列，未公开倍率不得泄露', () => {
    expect(mapMeGroupTableRows([
      { pool_group_id: 3, name: '快速组', ratio: '1.50000000', has_public_ratio: true },
      { pool_group_id: 9, name: '', ratio: '8.00000000', has_public_ratio: false },
    ])).toEqual([
      { id: 3, name: '快速组', groupId: '3', ratio: '1.5×', tone: 'info' },
      { id: 9, name: '分组 #9', groupId: '9', ratio: '未公开', tone: 'muted' },
    ])
  })
})
