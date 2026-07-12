import { renderToStaticMarkup } from 'react-dom/server'
import { MemoryRouter } from 'react-router-dom'
import { describe, expect, it } from 'vitest'
import { PublicKeyUsagePage } from './PublicKeyUsagePage'

describe('公开凭 Key 查用量页', () => {
  it('渲染免登录说明 + 复用 KeyUsageAnalytics(带 API Key 输入)', () => {
    const html = renderToStaticMarkup(
      <MemoryRouter>
        <PublicKeyUsagePage />
      </MemoryRouter>,
    )
    expect(html).toContain('无需登录')
    // 复用的分析组件:必含 API Key 输入(变异:若没嵌入分析组件则查不了)
    expect(html).toContain('Key 级分析')
    expect(html).toContain('登录管理端')
  })
})
