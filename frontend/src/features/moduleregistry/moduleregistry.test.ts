import { describe, expect, it } from 'vitest'
import {
  countByProbe,
  extractCategories,
  groupByCategory,
  mapModuleRows,
  probeLabel,
  probeTone,
} from './moduleregistry'
import type { ModuleView } from './types'

// 构造一组有区分度的模块 fixture:跨 3 个 category、4 种探针状态。
function fixture(): ModuleView[] {
  return [
    { id: 'billing.service', category: 'money-path', title: 'Billing', live_probe: { status: 'ok' } },
    { id: 'billing.refund', category: 'money-path', title: 'Refund', live_probe: { status: 'degraded' } },
    { id: 'routing.selector', category: 'routing', title: 'Selector', live_probe: { status: 'unknown' } },
    { id: 'creds.rotation', category: 'credentials', title: 'Rotation', live_probe: { status: 'error' } },
  ]
}

describe('probeTone', () => {
  it('四种状态映射到不同语气', () => {
    // 判别核心:每个状态映射到指定语气。变异(任一 case 改语气)→ 对应断言 RED。
    expect(probeTone('ok')).toBe('ok')
    expect(probeTone('degraded')).toBe('warn')
    expect(probeTone('error')).toBe('danger')
    // unknown 必须是 muted 而非 danger —— 后端明确 unknown 不是错误。
    expect(probeTone('unknown')).toBe('muted')
  })
  it('unknown 与 error 语气不同(防把未知误报成失败)', () => {
    // 变异(把 unknown case 删掉走 default→muted 仍对;但若误并入 error→danger)→ RED。
    expect(probeTone('unknown')).not.toBe(probeTone('error'))
  })
})

describe('probeLabel', () => {
  it('给出中文标签', () => {
    expect(probeLabel('ok')).toBe('正常')
    expect(probeLabel('degraded')).toBe('降级')
    expect(probeLabel('error')).toBe('失败')
    expect(probeLabel('unknown')).toBe('未知')
  })
})

describe('extractCategories', () => {
  it('去重 + 字典序升序', () => {
    // 判别核心:money-path 出现两次只保留一个,且整体有序。
    // 变异(去掉 Set 去重)→ 长度变 4,首断言 RED;变异(去掉 sort)→ 顺序变,第二断言 RED。
    expect(extractCategories(fixture())).toEqual(['credentials', 'money-path', 'routing'])
  })
  it('丢弃空 category', () => {
    // 变异(去掉空串守卫)→ 会多出一个空串项,断言 RED。
    const withBlank: ModuleView[] = [
      { id: 'a', category: '', title: 'A', live_probe: { status: 'ok' } },
      { id: 'b', category: 'x', title: 'B', live_probe: { status: 'ok' } },
    ]
    expect(extractCategories(withBlank)).toEqual(['x'])
  })
})

describe('groupByCategory', () => {
  it('同 category 聚为一组,组内顺序保留,无模块丢失', () => {
    const groups = groupByCategory(fixture())
    // 判别核心:3 组(money-path 聚合两条);变异(每模块各成一组)→ 组数变 4,RED。
    expect(groups.map((g) => g.category)).toEqual(['money-path', 'routing', 'credentials'])
    const money = groups[0]
    expect(money.modules.map((m) => m.id)).toEqual(['billing.service', 'billing.refund'])
    // 总模块数守恒:变异(归组时漏掉某条)→ 总数 < 4,RED。
    const total = groups.reduce((n, g) => n + g.modules.length, 0)
    expect(total).toBe(4)
  })
  it('空 category 归入「(未分类)」组', () => {
    const groups = groupByCategory([
      { id: 'a', category: '', title: 'A', live_probe: { status: 'ok' } },
    ])
    expect(groups[0].category).toBe('(未分类)')
  })
})

describe('countByProbe', () => {
  it('各状态精确计数,total = 总数', () => {
    const c = countByProbe(fixture())
    // 判别核心:ok/degraded/error/unknown 各 1,total=4。
    // 变异(把 degraded 误并入 unknown)→ degraded=0 且 unknown=2,断言 RED。
    expect(c).toEqual({ ok: 1, degraded: 1, error: 1, unknown: 1, total: 4 })
  })
  it('缺失/未识别状态归入 unknown,且计数总和 = total', () => {
    const odd: ModuleView[] = [
      { id: 'a', category: 'x', title: 'A', live_probe: { status: 'ok' } },
      // 模拟后端未来新增/异常状态串:不能漏计。
      { id: 'b', category: 'x', title: 'B', live_probe: { status: 'weird' as never } },
    ]
    const c = countByProbe(odd)
    expect(c.unknown).toBe(1)
    // 各桶之和必须等于 total —— 变异(default 分支不计数)→ 和 < total,RED。
    expect(c.ok + c.degraded + c.error + c.unknown).toBe(c.total)
  })
})

describe('mapModuleRows', () => {
  it('精确映射能力、探针与 catalog 展示列', () => {
    const module: ModuleView = {
      id: 'billing.service', category: 'money-path', title: 'Billing', capabilities: ['settle', 'refund'],
      live_probe: { status: 'degraded', detail: '延迟升高' },
      catalog: { status: 'tested', parity: 'strong', feature_id: 'F-7', pkgs: ['billing', 'ledger'], section: '§5' },
    }
    const [row] = mapModuleRows([module])
    // 变异字段来源、拼接或探针语气都会使精确断言变红。
    expect(row).toEqual({
      id: 'billing.service', title: 'Billing', capabilities: ['settle', 'refund'],
      probe: '降级', probeTone: 'warn', probeDetail: '延迟升高',
      catalogSummary: 'tested · strong', featureId: 'F-7', packages: 'billing, ledger', section: '§5',
    })
  })
})
