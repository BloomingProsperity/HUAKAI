import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { OneTimeSecretBox } from './OneTimeSecretBox'
import { PlatformCredentialsPage } from './PlatformCredentialsPage'

describe('平台凭证关键渲染', () => {
  it('聚合页把两类复杂凭证放在同一页分签', () => {
    const html = renderToStaticMarkup(<PlatformCredentialsPage />)
    expect(html).toContain('平台凭证')
    expect(html).toContain('运维令牌')
    expect(html).toContain('平台级 API Key')
    expect(html).toContain('签发运维令牌')
  })

  it('一次性展示框必须同时出现明文、复制入口与不可回看警告', () => {
    const html = renderToStaticMarkup(
      <OneTimeSecretBox kind="运维令牌" plaintext="hua_once_secret" keyPrefix="hua_once" onClose={vi.fn()} />,
    )
    expect(html).toContain('hua_once_secret')
    expect(html).toContain('复制明文')
    expect(html).toContain('关闭后不可再查看')
    expect(html).toContain('已保存并关闭')
  })
})
