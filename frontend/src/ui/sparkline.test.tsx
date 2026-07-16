import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Sparkline, sparklinePath } from './Sparkline'

describe('Sparkline', () => {
  it('数值变化会改变纵坐标且保留顺序', () => {
    expect(sparklinePath([0, 10, 5], 100, 20)).toBe('M 0.00 20.00 L 50.00 0.00 L 100.00 10.00')
  })

  it('空序列不渲染，全相等序列落在中线', () => {
    expect(renderToStaticMarkup(<Sparkline values={[]} label="空趋势" />)).toBe('')
    expect(sparklinePath([7, 7], 100, 20)).toContain('10.00')
  })
})
