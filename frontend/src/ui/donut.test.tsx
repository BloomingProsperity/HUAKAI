import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { Donut, donutSlices } from './Donut'

describe('Donut', () => {
  it('按真实值计算占比与累积偏移', () => {
    const slices = donutSlices([
      { label: 'A', value: 30, color: 'red' },
      { label: 'B', value: 70, color: 'blue' },
    ])
    expect(slices.map((slice) => slice.percent)).toEqual([30, 70])
    expect(slices[1].offset).toBe(30)
  })

  it('渲染中心总量、图例与下钻，忽略非正值', () => {
    const html = renderToStaticMarkup(<MemoryRouter><Donut label="模型分布" total={10} segments={[
      { label: '有效模型', value: 10, color: 'var(--hk-primary-500)', to: '/usage?model=x' },
      { label: '坏数据', value: -1, color: 'red' },
    ]} /></MemoryRouter>)
    expect(html).toContain('data-testid="donut-chart"')
    expect(html).toContain('有效模型')
    expect(html).toContain('100.0%')
    expect(html).not.toContain('坏数据')
  })
})
