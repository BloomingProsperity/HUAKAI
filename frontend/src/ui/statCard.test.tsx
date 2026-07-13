import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { StatCard } from './StatCard'

describe('StatCard 关键渲染分支', () => {
  it('无 to 时渲染普通卡片', () => {
    const html = renderToStaticMarkup(<StatCard label="请求数" value="12" />)
    expect(html).toContain('<div')
    expect(html).not.toContain('<a')
    expect(html).toContain('请求数')
  })

  it('有 to 时整卡渲染为链接', () => {
    const html = renderToStaticMarkup(<MemoryRouter><StatCard label="错误数" value="2" to="/logs" /></MemoryRouter>)
    expect(html).toContain('<a')
    expect(html).toContain('href="/logs"')
    expect(html).toContain('错误数')
    expect(html).toContain('>2</strong>')
  })

  it('可选 icon 与 sparkline 槽会渲染且旧 props 仍兼容', () => {
    const html = renderToStaticMarkup(<StatCard label="今日请求量" value="18" icon="↗" sparkline={<span>趋势槽</span>} />)
    expect(html).toContain('↗')
    expect(html).toContain('趋势槽')
    expect(html).toContain('今日请求量')
  })
})
