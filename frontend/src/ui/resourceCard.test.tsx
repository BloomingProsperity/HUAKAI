import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { ResourceCard } from './ResourceCard'

describe('ResourceCard', () => {
  it('渲染大数、两枚分解徽标与主操作', () => {
    const html = renderToStaticMarkup(<MemoryRouter><ResourceCard
      title="上游账号"
      value="4/5"
      badges={[{ label: '可用', value: '4', tone: 'ok' }, { label: '不可用', value: '1', tone: 'danger' }]}
      action={{ label: '管理上游账号', to: '/accounts' }}
    /></MemoryRouter>)
    expect(html).toContain('<strong')
    expect(html).toContain('4/5')
    expect(html).toContain('可用 4')
    expect(html).toContain('不可用 1')
    expect(html).toContain('href="/accounts"')
  })

  it('一期退化卡可诚实省略分解徽标', () => {
    const html = renderToStaticMarkup(<MemoryRouter><ResourceCard title="在线模型数" value="12" action={{ label: '管理模型服务', to: '/models' }} /></MemoryRouter>)
    expect(html).toContain('在线模型数')
    expect(html).not.toContain('data-tone')
  })
})
