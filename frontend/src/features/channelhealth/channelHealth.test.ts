import { describe, expect, it } from 'vitest'
import {
  actionLabel,
  actionNeedsConfirm,
  buildListQuery,
  buildOverride,
  buildTenantQuery,
  canOverride,
  clampLimit,
  confidenceLabel,
  DEFAULT_LIMIT,
  eventLabel,
  mapChannelHealthRows,
  MAX_LIMIT,
  signalLabel,
  stateLabel,
  stateTone,
} from './channelHealth'

// 完整坐标的列表项基样,各用例按需覆盖单字段以验证判别逻辑。
const baseItem = {
  tenant_id: 7,
  vendor: 'Anthropic',
  account_credential_id: 42,
  credential_version: 3,
  provider_account_id: 11,
}

describe('buildListQuery', () => {
  it('tenant_id 必带,limit/offset 给了才下发', () => {
    expect(buildListQuery(7, 50, 100)).toEqual({ tenant_id: 7, limit: 50, offset: 100 })
    // 判别核心:不传 limit/offset 时不得出现在 query(汇总端点无分页);
    // 变异(无条件赋值)→ 含 undefined 键 → RED。
    const q = buildListQuery(7)
    expect(q).toEqual({ tenant_id: 7 })
    expect('limit' in q).toBe(false)
    expect('offset' in q).toBe(false)
  })
})

describe('buildTenantQuery', () => {
  it('只带 tenant_id', () => {
    expect(buildTenantQuery(9)).toEqual({ tenant_id: 9 })
  })
})

describe('stateTone', () => {
  it('健康/爬坡/受损/封停按语义分级,未知中性', () => {
    expect(stateTone('active')).toBe('ok')
    expect(stateTone('ramping')).toBe('info')
    expect(stateTone('degraded')).toBe('warn')
    // 判别核心:cooling_down(自动冷却,会自愈)必须 warn,
    // manual_paused(人工封停,不自愈)必须 danger,二者不可同级。
    expect(stateTone('cooling_down')).toBe('warn')
    expect(stateTone('manual_paused')).toBe('danger')
    expect(stateTone('disabled')).toBe('danger')
    expect(stateTone('whatever')).toBe('muted')
  })
})

describe('stateLabel', () => {
  it('给中文标签,未知回退原值', () => {
    expect(stateLabel('active')).toBe('健康')
    expect(stateLabel('cooling_down')).toBe('冷却中')
    expect(stateLabel('ramping')).toBe('恢复爬坡中')
    expect(stateLabel('manual_paused')).toBe('人工暂停')
    expect(stateLabel('xyz')).toBe('xyz')
    expect(stateLabel('')).toBe('—')
  })
})

describe('confidenceLabel / signalLabel / eventLabel', () => {
  it('置信/信号/事件中文化,未知回退原值', () => {
    expect(confidenceLabel('operator_override')).toBe('人工干预')
    expect(confidenceLabel('zzz')).toBe('zzz')
    expect(signalLabel('rate_limit')).toBe('限流')
    expect(signalLabel('account_suspended')).toBe('账号被封')
    expect(signalLabel('weird')).toBe('weird')
    expect(eventLabel('channel_ramp_rolled_back')).toBe('爬坡回滚')
    expect(eventLabel('mystery')).toBe('mystery')
  })
})

describe('actionLabel', () => {
  it('三动作中文标签', () => {
    expect(actionLabel('pause')).toBe('人工暂停')
    expect(actionLabel('resume')).toBe('恢复')
    expect(actionLabel('force-active')).toBe('强制置健康')
  })
})

describe('actionNeedsConfirm', () => {
  it('pause/force-active 需二次确认,resume 不需', () => {
    expect(actionNeedsConfirm('pause')).toBe(true)
    // 判别核心:force-active 绕过自动冷却保护,必须 true;
    // 变异(漏掉 force-active 分支)→ 返回 false → RED。
    expect(actionNeedsConfirm('force-active')).toBe(true)
    expect(actionNeedsConfirm('resume')).toBe(false)
  })
})

describe('canOverride', () => {
  it('坐标齐才可写', () => {
    expect(canOverride(baseItem)).toBe(true)
  })

  it('任一坐标缺失即不可写', () => {
    expect(canOverride({ ...baseItem, tenant_id: 0 })).toBe(false)
    expect(canOverride({ ...baseItem, vendor: '  ' })).toBe(false)
    expect(canOverride({ ...baseItem, account_credential_id: 0 })).toBe(false)
    // 判别核心:credential_version<=0 必须判不可用(后端 ChannelKey.Validate 会 400);
    // 变异(去掉该判定)→ 返回 true → RED。
    expect(canOverride({ ...baseItem, credential_version: 0 })).toBe(false)
    expect(canOverride({ ...baseItem, provider_account_id: 0 })).toBe(false)
  })
})

describe('buildOverride', () => {
  it('坐标齐 + reason 非空 → 构造请求体,vendor 归一为小写', () => {
    const out = buildOverride(baseItem, '  限流频繁,临时封停  ')
    expect(out.ok).toBe(true)
    if (!out.ok) return
    expect(out.providerAccountId).toBe(11)
    expect(out.body).toEqual({
      tenant_id: 7,
      // 判别核心:vendor 必须 trim+toLowerCase(镜像后端 handler:173),
      // 变异(原样透传)→ 'Anthropic' ≠ 'anthropic' → RED。
      vendor: 'anthropic',
      account_credential_id: 42,
      credential_version: 3,
      reason: '限流频繁,临时封停',
    })
  })

  it('空 reason → 拒(镜像后端 reason_required)', () => {
    const out = buildOverride(baseItem, '   ')
    expect(out.ok).toBe(false)
    if (out.ok) return
    expect(out.error).toContain('原因')
  })

  it('坐标不全 → 拒', () => {
    const out = buildOverride({ ...baseItem, credential_version: 0 }, '理由')
    expect(out.ok).toBe(false)
  })
})

describe('clampLimit', () => {
  it('非正/非整回退默认,超上限截到 MAX_LIMIT', () => {
    expect(clampLimit(0)).toBe(DEFAULT_LIMIT)
    expect(clampLimit(-5)).toBe(DEFAULT_LIMIT)
    expect(clampLimit(1.5)).toBe(DEFAULT_LIMIT)
    expect(clampLimit(50)).toBe(50)
    // 判别核心:超 MAX_LIMIT 必须截断(后端 >200 即 400);
    // 变异(直接返回入参)→ 返回 999 → RED。
    expect(clampLimit(999)).toBe(MAX_LIMIT)
  })
})

describe('mapChannelHealthRows', () => {
  it('把状态、信号、恢复进度和动作条件映射为表格行', () => {
    const source = {
      ...baseItem,
      channel_id: 'anthropic:42:v3',
      state: 'cooling_down',
      score: 91.236,
      reason_class: 'rate_limit',
      confidence_tier: 'observed',
      policy_version: 'v1',
      ramp_stage_pct: 25,
      ramp_failure_count: 2,
      cooldown_until: 'bad-date',
      updated_at: undefined,
    }
    const [row] = mapChannelHealthRows([source])
    expect(row).toMatchObject({
      key: '11:3:42',
      state: '冷却中',
      stateTone: 'warn',
      score: '91.24',
      signal: '限流',
      confidence: '已观测',
      recovery: '冷却至 bad-date',
      recoveryDetail: '爬坡 25% · 失败 2',
      updatedAt: '—',
      writable: true,
    })
    // 判别核心:健康状态必须映射到 warn；变异为 ok/danger 会直接证红。
    expect(row.stateTone).toBe('warn')
    expect(row.item).toBe(source)
  })
})
