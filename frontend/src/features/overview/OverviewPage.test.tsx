import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { QuotaCardContent, UsageCardContent } from './OverviewPage'

describe('概览页状态呈现', () => {
  it('无配额限制时呈现正向空态与用量明细出口', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter><QuotaCardContent state="ok" items={[]} headline={null} /></MemoryRouter>,
    )

    expect(html).toContain('data-tone="positive"')
    expect(html).toContain('配额无上限')
    expect(html).toContain('href="/usage-records"')
  })

  it('无用量时同时提供接入指引与密钥管理出口', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter><UsageCardContent state="ok" bars={[]} /></MemoryRouter>,
    )

    expect(html).toContain('data-tone="neutral"')
    expect(html).toContain('href="/integration"')
    expect(html).toContain('href="/keys"')
  })

  it('加载态使用骨架且不再显示加载文案', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter><UsageCardContent state="loading" bars={null} /></MemoryRouter>,
    )

    expect(html).toContain('hk-skeleton-block')
    expect(html).not.toContain('加载中')
  })
})
