import { describe, expect, it } from 'vitest'
import {
  buildListQuery,
  buildReconcileRequest,
  formatCents,
  mapOrphanRows,
  needsBackChargeConfirm,
  outcomeLabel,
  parseTenantFilter,
  reconcileStatusLabel,
  statusLabel,
  statusTone,
  summarizeReconcile,
} from './orphanreconcile'
import type { OrphanItem } from './types'

describe('buildListQuery', () => {
  it('正整数 tenant_id 与 limit 都下发', () => {
    expect(buildListQuery(7, 100)).toEqual({ tenant_id: 7, limit: 100 })
  })

  it('tenant_id 为 null/0/负数/非整数一律省略(避免 platform_admin 反被 400)', () => {
    // 判别核心:后端要求 tenant_id 必须为正整数,否则 invalid_tenant_id 400。
    // 变异(无条件赋值 tenant_id)→ 这些断言 RED。
    expect('tenant_id' in buildListQuery(null, 100)).toBe(false)
    expect('tenant_id' in buildListQuery(0, 100)).toBe(false)
    expect('tenant_id' in buildListQuery(-3, 100)).toBe(false)
    expect('tenant_id' in buildListQuery(1.5, 100)).toBe(false)
  })

  it('limit 非正/非整数一律省略', () => {
    // 变异(无条件赋值 limit)→ RED。
    expect('limit' in buildListQuery(7, 0)).toBe(false)
    expect('limit' in buildListQuery(7, -1)).toBe(false)
    expect(buildListQuery(7, 50)).toEqual({ tenant_id: 7, limit: 50 })
  })
})

describe('parseTenantFilter', () => {
  it('空串=null,正整数=过滤,非法=null', () => {
    expect(parseTenantFilter('')).toBeNull()
    expect(parseTenantFilter('   ')).toBeNull()
    expect(parseTenantFilter('42')).toBe(42)
    // 判别核心:0 / 负数 / 小数 / 字母一律 null。变异(放宽正则)→ RED。
    expect(parseTenantFilter('0')).toBeNull()
    expect(parseTenantFilter('-5')).toBeNull()
    expect(parseTenantFilter('1.5')).toBeNull()
    expect(parseTenantFilter('abc')).toBeNull()
  })
})

describe('buildReconcileRequest (money 守卫)', () => {
  it('reconciled + 追扣 → ok 且 back_charge=true', () => {
    const r = buildReconcileRequest('reconciled', true, '  上游已计费  ')
    expect(r.ok).toBe(true)
    if (r.ok) {
      expect(r.value).toEqual({ status: 'reconciled', back_charge: true, reason: '上游已计费' })
    }
  })

  it('cancelled/ignored + 追扣 → 必须拒(money 守卫,镜像后端 reconcile.go:177)', () => {
    // 判别核心:back_charge 仅 reconciled 合法。
    // 变异(删掉 status!=='reconciled' 守卫)→ 这两条本应拒却放行,RED。
    expect(buildReconcileRequest('cancelled', true, '').ok).toBe(false)
    expect(buildReconcileRequest('ignored', true, '').ok).toBe(false)
  })

  it('cancelled/ignored 不追扣 → ok(仅标记不动钱)', () => {
    expect(buildReconcileRequest('cancelled', false, '').ok).toBe(true)
    expect(buildReconcileRequest('ignored', false, '').ok).toBe(true)
  })

  it('非法 status → 拒', () => {
    expect(buildReconcileRequest('weird', false, '').ok).toBe(false)
  })

  it('空白 reason 省略(不进 body)', () => {
    const r = buildReconcileRequest('reconciled', false, '   ')
    expect(r.ok).toBe(true)
    // 判别核心:空白 reason 不得出现在 body。变异(无条件塞 reason)→ RED。
    if (r.ok) expect('reason' in r.value).toBe(false)
  })
})

describe('needsBackChargeConfirm', () => {
  it('仅 reconciled+追扣需 money 二次确认', () => {
    // 判别核心:只有真追扣才动钱;取消/忽略即便误传 backCharge 也不该触发钱确认。
    // 变异(只看 backCharge 不看 status)→ 第二/三条 RED。
    expect(needsBackChargeConfirm('reconciled', true)).toBe(true)
    expect(needsBackChargeConfirm('cancelled', true)).toBe(false)
    expect(needsBackChargeConfirm('ignored', true)).toBe(false)
    expect(needsBackChargeConfirm('reconciled', false)).toBe(false)
  })
})

describe('statusTone / statusLabel', () => {
  it('pending→warn(待关注),reconciled→ok,cancelled/ignored→muted', () => {
    // 判别核心:pending 必须是 warn(需操作员关注),不可与 reconciled(ok)同级。
    expect(statusTone('pending')).toBe('warn')
    expect(statusTone('reconciled')).toBe('ok')
    expect(statusTone('cancelled')).toBe('muted')
    expect(statusTone('ignored')).toBe('muted')
    expect(statusTone('???')).toBe('muted')
  })

  it('中文标签,未知回退原值', () => {
    expect(statusLabel('pending')).toBe('待处置')
    expect(statusLabel('reconciled')).toBe('已对账')
    expect(statusLabel('cancelled')).toBe('已取消')
    expect(statusLabel('ignored')).toBe('已忽略')
    expect(statusLabel('x')).toBe('x')
    expect(statusLabel('')).toBe('—')
  })
})

describe('reconcileStatusLabel', () => {
  it('下拉用中文标签明确标出哪个可追扣', () => {
    expect(reconcileStatusLabel('reconciled')).toBe('对账(可追扣)')
    expect(reconcileStatusLabel('cancelled')).toBe('取消(不追扣)')
    expect(reconcileStatusLabel('ignored')).toBe('忽略(不追扣)')
  })
})

describe('formatCents', () => {
  it('分→美元两位小数', () => {
    // 判别核心:必须 /100。变异(去掉 /100)→ 1234 会显示 $1234.00 而非 $12.34,RED。
    expect(formatCents(1234)).toBe('$12.34')
    expect(formatCents(0)).toBe('$0.00')
    expect(formatCents(5)).toBe('$0.05')
    expect(formatCents(100)).toBe('$1.00')
    expect(formatCents(Number.NaN)).toBe('—')
  })
})

describe('outcomeLabel', () => {
  it('captured 与各未扣码各有中文解释,空=空串', () => {
    expect(outcomeLabel('captured')).toBe('已追扣到余额')
    // 判别核心:未扣到的码必须明确提示「孤儿保持待处置」,不能与 captured 混。
    expect(outcomeLabel('hold_not_held')).toContain('未追扣')
    expect(outcomeLabel('task_archived')).toContain('未追扣')
    expect(outcomeLabel('no_estimate')).toContain('未追扣')
    expect(outcomeLabel('holdref_unparseable')).toContain('未追扣')
    expect(outcomeLabel('')).toBe('')
    expect(outcomeLabel(undefined)).toBe('')
    expect(outcomeLabel('novel_code')).toBe('novel_code')
  })
})

describe('summarizeReconcile', () => {
  it('真追扣到钱 → 强调金额', () => {
    const s = summarizeReconcile({
      advanced: true,
      back_charged: true,
      captured_cents: 1234,
      status: 'reconciled',
      back_charge_outcome: 'captured',
    })
    // 判别核心:扣到钱必须把金额展示出来。
    expect(s).toContain('$12.34')
    expect(s).toContain('已追扣')
  })

  it('请求追扣但未扣到(409 路径)→ 提示保持待处置 + 原因', () => {
    const s = summarizeReconcile({
      advanced: false,
      back_charged: false,
      captured_cents: 0,
      status: 'pending',
      back_charge_outcome: 'hold_not_held',
    })
    // 判别核心:未扣到时绝不能显示「已追扣」误导操作员。
    expect(s).toContain('未追扣到费用')
    expect(s).not.toContain('已追扣 $')
  })

  it('仅标记(取消/忽略)→ 说明已标记、未追扣', () => {
    const s = summarizeReconcile({
      advanced: true,
      back_charged: false,
      captured_cents: 0,
      status: 'cancelled',
    })
    expect(s).toContain('已取消')
    expect(s).toContain('未追扣')
  })

  it('幂等:已是终态,advanced=false 且无追扣 → 无变化提示', () => {
    const s = summarizeReconcile({
      advanced: false,
      back_charged: false,
      captured_cents: 0,
      status: 'reconciled',
    })
    expect(s).toContain('幂等')
  })
})

describe('mapOrphanRows', () => {
  it('精确映射 money 邻接列表展示且保留原 DTO', () => {
    const item: OrphanItem = {
      id: 8, task_id: 31, tenant_id: 5, user_id: 12, provider: 'acme',
      provider_task_id: 'provider-task-abcdefghijkl', estimated_cents: 1234,
      reconcile_status: 'pending', observed_at: 'bad-time',
    }
    const [row] = mapOrphanRows([item])
    // 变异金额换算、ID 来源、状态语气或截断规则都会变红。
    expect(row).toMatchObject({
      id: 8, task: '#31', tenant: '#5', user: '#12', provider: 'acme',
      providerTaskId: 'provider-t…ijkl', estimatedCharge: '$12.34',
      status: '待处置', statusTone: 'warn', observedAt: 'bad-time',
    })
    expect(row.item).toBe(item)
  })
})
