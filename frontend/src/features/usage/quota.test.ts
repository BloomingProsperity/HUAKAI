import { describe, expect, it } from 'vitest'
import { quotaProgress } from './quota'

describe('quotaProgress', () => {
  it('cap≤0 或非数 → 无上限,不画条', () => {
    expect(quotaProgress('5', '0').unlimited).toBe(true)
    expect(quotaProgress('5', '').unlimited).toBe(true)
    expect(quotaProgress('5', 'abc').unlimited).toBe(true)
  })

  it('正常区间:pct 正确、tone=ok', () => {
    const p = quotaProgress('5', '10')
    expect(p.unlimited).toBe(false)
    expect(p.pct).toBe(50)
    expect(p.over).toBe(false)
    expect(p.tone).toBe('ok')
  })

  it('≥80% → warn', () => {
    // 判别核心:0.8 阈值。变异(去掉 ≥0.8 分支)→ tone 退回 ok,本断言 RED。
    expect(quotaProgress('8', '10').tone).toBe('warn')
    expect(quotaProgress('9.5', '10').tone).toBe('warn')
  })

  it('超额 → over=true、tone=danger、pct clamp 到 100', () => {
    const p = quotaProgress('12', '10')
    expect(p.over).toBe(true)
    expect(p.tone).toBe('danger')
    expect(p.pct).toBe(100) // 条宽 clamp,不溢出
  })

  it('小数 cap/consumed 正常解析', () => {
    expect(quotaProgress('2.5', '5').pct).toBe(50)
  })
})
