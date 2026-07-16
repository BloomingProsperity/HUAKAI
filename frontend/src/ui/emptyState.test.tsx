import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { EmptyState } from './EmptyState'

describe('EmptyState 关键渲染分支', () => {
  it('无行动时不渲染按钮区', () => {
    const html = renderToStaticMarkup(<EmptyState title="暂无记录" />)
    expect(html).not.toContain('data-testid="empty-state-actions"')
    expect(html).not.toContain('<button')
  })

  it('链接行动与点击行动使用不同标签', () => {
    const buttonHtml = renderToStaticMarkup(<EmptyState title="请求失败" action={{ label: '重试', onClick: () => undefined }} />)
    expect(buttonHtml).toContain('<button')
    expect(buttonHtml).toContain('重试</button>')

    const linkHtml = renderToStaticMarkup(<MemoryRouter><EmptyState title="暂无密钥" action={{ label: '新建密钥', to: '/keys' }} /></MemoryRouter>)
    expect(linkHtml).toContain('<a')
    expect(linkHtml).toContain('href="/keys"')
  })

  it('tone 会切换图标底色语气', () => {
    const positive = renderToStaticMarkup(<EmptyState title="一切正常" tone="positive" />)
    expect(positive).toContain('data-tone="positive"')
    expect(positive).toContain('background:var(--hk-primary-50)')
    const unavailable = renderToStaticMarkup(<EmptyState title="暂不可用" tone="unavailable" />)
    expect(unavailable).toContain('data-tone="unavailable"')
    expect(unavailable).toContain('background:var(--hk-danger-soft)')
  })
})
