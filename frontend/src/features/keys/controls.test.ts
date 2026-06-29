import { describe, expect, it } from 'vitest'
import {
  buildIPAllowlist,
  buildIPBlacklist,
  buildModelAllowlist,
  emptyGroupForm,
  emptyQuotaForm,
  firstInvalidIP,
  groupDisplay,
  groupToForm,
  ipAllowlistFromView,
  ipBlacklistFromView,
  isPlausibleIPorCIDR,
  listToText,
  metricLabel,
  modelAllowlistFromView,
  parseList,
  quotaToForm,
  trimDecimal,
  validateGroup,
  validateQuota,
} from './controls'
import type { KeyGroupView, KeyQuotaView } from './controlsTypes'

// ── 配额(money 敏感)─────────────────────────────────────────────────────────
import type { QuotaForm } from './controls'
// 构造完整 QuotaForm 的测试 helper(窗口/模式默认留空,单测里按需覆盖)。
function qf(partial: Partial<QuotaForm>): QuotaForm {
  return { limitUsd: '', metric: 'cost-usd', windowKind: '', windowSeconds: 0, mode: '', ...partial }
}

describe('validateQuota', () => {
  it('空串归一为无限额 "0"', () => {
    const r = validateQuota(qf({ limitUsd: '   ', metric: 'cost-usd' }))
    expect(r.ok).toBe(true)
    // 判别核心:空输入必须显式下发 "0"(无限额)。变异(返回原空串)→ 此断言 RED。
    if (r.ok) expect(r.value.limit_usd).toBe('0')
  })

  it('合法非负十进制通过并透传 metric', () => {
    const r = validateQuota(qf({ limitUsd: '25.50', metric: 'request-count' }))
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.limit_usd).toBe('25.50')
      // 判别核心:metric 必须透传,不能被吞成默认。变异(硬编 cost-usd)→ RED。
      expect(r.value.metric).toBe('request-count')
    }
  })

  it('窗口/模式 round-trip:保存限额必须携带 GET 回的 window_kind/mode,避免后端重置成 day+enforce', () => {
    // 判别核心(S1 回归):form 带 calendar_month/observe 时,PUT 体必须原样回传这两个字段。
    // 变异(validateQuota 丢弃 window/mode)→ window_kind/mode 为 undefined,本断言 RED;
    // 那正是「保存月度限额被静默改成每日阻断」的真 bug。
    const r = validateQuota(qf({ limitUsd: '100', windowKind: 'calendar_month', windowSeconds: 0, mode: 'observe' }))
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value.window_kind).toBe('calendar_month')
      expect(r.value.mode).toBe('observe')
    }
    // 空窗口/模式时不下发(让后端首次设置走默认),避免发空串。
    const r2 = validateQuota(qf({ limitUsd: '5' }))
    if (r2.ok) {
      expect(r2.value.window_kind).toBeUndefined()
      expect(r2.value.mode).toBeUndefined()
    }
  })

  it('负数 / 非数字拒绝(镜像后端 parseLimitUSD 非负约束)', () => {
    // 判别核心:负号必须拒。变异(放宽正则允许 '-')→ 此断言 RED。
    expect(validateQuota(qf({ limitUsd: '-1' })).ok).toBe(false)
    expect(validateQuota(qf({ limitUsd: 'abc' })).ok).toBe(false)
    expect(validateQuota(qf({ limitUsd: '1.2.3' })).ok).toBe(false)
    // 边界:0 与纯整数与小数都合法。
    expect(validateQuota(qf({ limitUsd: '0' })).ok).toBe(true)
    expect(validateQuota(qf({ limitUsd: '100' })).ok).toBe(true)
  })

  it('quotaToForm:metric=requests → request-count;limit_usd 去尾随 0', () => {
    const view = {
      limit_usd: '25.00000000',
      metric: 'requests',
    } as unknown as KeyQuotaView
    const f = quotaToForm(view)
    expect(f.limitUsd).toBe('25')
    // 判别核心:后端 metric "requests" 必须映射为前端 'request-count'。变异(恒 cost-usd)→ RED。
    expect(f.metric).toBe('request-count')
    expect(quotaToForm({ limit_usd: '1.50000000', metric: 'cost_usd' } as unknown as KeyQuotaView).metric).toBe('cost-usd')
  })

  it('emptyQuotaForm 默认 cost-usd 空上限 + 空窗口/模式', () => {
    expect(emptyQuotaForm()).toEqual({ limitUsd: '', metric: 'cost-usd', windowKind: '', windowSeconds: 0, mode: '' })
  })

  it('metricLabel 给中文', () => {
    expect(metricLabel('cost-usd')).toContain('USD')
    expect(metricLabel('request-count')).toBe('请求次数')
  })
})

// ── 分组 ──────────────────────────────────────────────────────────────────────
describe('validateGroup', () => {
  it('空串 → group_id:null(清除绑定)', () => {
    const r = validateGroup({ groupId: '  ' })
    expect(r.ok).toBe(true)
    // 判别核心:空必须下发 null。变异(下发 0 或省略)→ 后端 invalid_group_id,断言 RED。
    if (r.ok) expect(r.value.group_id).toBeNull()
  })

  it('正整数串通过', () => {
    const r = validateGroup({ groupId: '42' })
    expect(r.ok).toBe(true)
    if (r.ok) expect(r.value.group_id).toBe(42)
  })

  it('0 / 负数 / 非数字拒绝(镜像 *GroupID<=0 即 400)', () => {
    // 判别核心:0 与负数必须拒(后端正整数约束)。变异(正则放宽到含 0)→ RED。
    expect(validateGroup({ groupId: '0' }).ok).toBe(false)
    expect(validateGroup({ groupId: '-3' }).ok).toBe(false)
    expect(validateGroup({ groupId: 'x' }).ok).toBe(false)
  })

  it('groupToForm / groupDisplay 回填与展示', () => {
    expect(groupToForm({ api_key_id: 1, group_id: 9 } as KeyGroupView).groupId).toBe('9')
    expect(groupToForm({ api_key_id: 1 } as KeyGroupView).groupId).toBe('')
    expect(emptyGroupForm()).toEqual({ groupId: '' })
    expect(groupDisplay(null)).toBe('未绑定')
    expect(groupDisplay({ api_key_id: 1 } as KeyGroupView)).toBe('未绑定')
    expect(groupDisplay({ api_key_id: 1, group_id: 5, group_name: 'VIP' } as KeyGroupView)).toBe('VIP')
    // 判别核心:有 id 无 name 时退回 #id 而非「未绑定」。变异(无名即视为未绑定)→ RED。
    expect(groupDisplay({ api_key_id: 1, group_id: 5 } as KeyGroupView)).toBe('#5')
  })
})

// ── 列表解析 + IP 校验 ─────────────────────────────────────────────────────────
describe('parseList', () => {
  it('按行拆、trim、丢空行、保序去重', () => {
    // 判别核心:重复条目去重 + 空行剔除。变异(去掉 dedup)→ 含重复,断言 RED。
    expect(parseList('1.1.1.1\n 2.2.2.2 \n\n1.1.1.1\n')).toEqual(['1.1.1.1', '2.2.2.2'])
    expect(parseList('  \n\n')).toEqual([])
  })
  it('listToText 往返', () => {
    expect(listToText(['a', 'b'])).toBe('a\nb')
    expect(listToText(null)).toBe('')
    expect(listToText(undefined)).toBe('')
  })
})

describe('isPlausibleIPorCIDR', () => {
  it('接受 IPv4 / IPv4-CIDR / IPv6 / IPv6-CIDR', () => {
    expect(isPlausibleIPorCIDR('10.0.0.1')).toBe(true)
    expect(isPlausibleIPorCIDR('192.168.0.0/16')).toBe(true)
    expect(isPlausibleIPorCIDR('2001:db8::1')).toBe(true)
    expect(isPlausibleIPorCIDR('fd00::/8')).toBe(true)
  })
  it('拒绝域名 / 越界八位组 / 越界掩码 / 多斜杠', () => {
    // 判别核心:域名(无数字 IP 形态)必须拒。变异(去掉四段校验)→ 'example.com' 放行,RED。
    expect(isPlausibleIPorCIDR('example.com')).toBe(false)
    // 八位组 >255 必须拒。
    expect(isPlausibleIPorCIDR('999.0.0.1')).toBe(false)
    // IPv4 掩码 >32 必须拒。变异(去掉掩码上界)→ RED。
    expect(isPlausibleIPorCIDR('10.0.0.0/33')).toBe(false)
    // IPv6 掩码 >128 必须拒。
    expect(isPlausibleIPorCIDR('fd00::/129')).toBe(false)
    // 多个 '/' 必须拒。
    expect(isPlausibleIPorCIDR('10.0.0.0/16/8')).toBe(false)
    expect(isPlausibleIPorCIDR('  ')).toBe(false)
  })
})

describe('firstInvalidIP', () => {
  it('返回首个非法条目,全合法返回 null', () => {
    expect(firstInvalidIP(['10.0.0.1', '2.2.2.2/24'])).toBeNull()
    // 判别核心:必须返回「首个」非法项。变异(返回最后一个/恒 null)→ 顺序/存在性断言 RED。
    expect(firstInvalidIP(['10.0.0.1', 'bad-host', '3.3.3.3'])).toBe('bad-host')
  })
})

describe('build*Body / *FromView', () => {
  it('构造 PUT 体字段名正确(镜像后端 json tag)', () => {
    expect(buildIPAllowlist(['1.1.1.1'])).toEqual({ ip_allowlist: ['1.1.1.1'] })
    expect(buildIPBlacklist(['2.2.2.2'])).toEqual({ ip_blacklist: ['2.2.2.2'] })
    expect(buildModelAllowlist(['gpt-4o'])).toEqual({ allowed_models: ['gpt-4o'] })
    // 空数组=清空(白名单清空=放行全部)。
    expect(buildIPAllowlist([])).toEqual({ ip_allowlist: [] })
  })
  it('从视图取列表,缺省回空数组', () => {
    expect(ipAllowlistFromView({ api_key_id: 1, ip_allowlist: ['1.1.1.1'] })).toEqual(['1.1.1.1'])
    expect(ipBlacklistFromView({ api_key_id: 1 })).toEqual([])
    expect(modelAllowlistFromView({ api_key_id: 1, allowed_models: ['m'] })).toEqual(['m'])
  })
})

describe('trimDecimal', () => {
  it('裁尾随 0、整数不留点、非十进制原样', () => {
    expect(trimDecimal('25.00000000')).toBe('25')
    expect(trimDecimal('1.50')).toBe('1.5')
    expect(trimDecimal('3')).toBe('3')
    expect(trimDecimal('')).toBe('')
    expect(trimDecimal('xyz')).toBe('xyz')
  })
})
