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

  it('长值区有截断兜底，并用 title 暴露完整值', () => {
    const html = renderToStaticMarkup(<StatCard label="总成本" value="$241.27" valueTitle="$241.2743402048" />)
    // 判别核心：去掉 overflow/text-overflow 或误把短值写入 title 都会打红。
    expect(html).toContain('title="$241.2743402048"')
    expect(html).toContain('overflow:hidden')
    expect(html).toContain('text-overflow:ellipsis')
    expect(html).toContain('white-space:nowrap')
  })

  it.each([
    ['warn', 'var(--hk-warn)'],
    ['ok', 'var(--hk-success)'],
  ] as const)('%s tone 使用对应语义令牌', (tone, token) => {
    const html = renderToStaticMarkup(<StatCard label="健康状态" value="8" tone={tone} />)
    expect(html).toContain(`data-tone="${tone}"`)
    expect(html).toContain(`color:${token}`)
  })
})
