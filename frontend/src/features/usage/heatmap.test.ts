import { describe, expect, it } from 'vitest'
import { buildHeatGrid, heatLevel, pickCache, pickCost, weekdayMonFirst } from './heatmap'
import type { KeyUsageTimeSeriesPoint } from './types'

function pt(day: string, cost: string, cacheRead = 0, cacheCreation = 0, input = 0): KeyUsageTimeSeriesPoint {
  return {
    day,
    requested_model: 'm',
    total_cost: cost,
    tokens: { input, output: 0, cache_read: cacheRead, cache_creation: cacheCreation },
    request_count: 1,
  }
}

describe('heatLevel', () => {
  it('0 用量为 0 档', () => {
    expect(heatLevel(0, 100)).toBe(0)
    expect(heatLevel(5, 0)).toBe(0)
  })
  it('按比例分 1..4 档', () => {
    // 判别:25% → ceil(1)=1;26% → ceil(1.04)=2(档界咬住)
    expect(heatLevel(25, 100)).toBe(1)
    expect(heatLevel(26, 100)).toBe(2)
    expect(heatLevel(100, 100)).toBe(4)
    expect(heatLevel(1, 100)).toBe(1)
  })
})

describe('weekdayMonFirst', () => {
  it('周一=0 周日=6', () => {
    expect(weekdayMonFirst('2026-07-13')).toBe(0) // 周一
    expect(weekdayMonFirst('2026-07-12')).toBe(6) // 周日
  })
})

describe('buildHeatGrid', () => {
  it('按天聚合同日多模型', () => {
    const grid = buildHeatGrid([pt('2026-07-13', '1.0'), pt('2026-07-13', '2.0')], pickCost)
    expect(grid.cells).toHaveLength(1)
    expect(grid.cells[0].value).toBeCloseTo(3.0)
    expect(grid.max).toBeCloseTo(3.0)
  })
  it('网格行列坐标(周一起、周为列)', () => {
    const grid = buildHeatGrid([pt('2026-07-13', '1'), pt('2026-07-20', '2')], pickCost)
    const mon1 = grid.cells.find((c) => c.day === '2026-07-13')!
    const mon2 = grid.cells.find((c) => c.day === '2026-07-20')!
    expect(mon1.row).toBe(0)
    expect(mon1.col).toBe(0)
    expect(mon2.col).toBe(1) // 判别:相邻周应在下一列,而非同列
  })
  it('缓存 pick 取读+写', () => {
    expect(pickCache(pt('2026-07-13', '0', 100, 50))).toBe(150)
    expect(pickCost(pt('2026-07-13', '1.5'))).toBeCloseTo(1.5)
  })
  it('空输入返回空网格', () => {
    expect(buildHeatGrid([], pickCost).cells).toHaveLength(0)
  })
})
