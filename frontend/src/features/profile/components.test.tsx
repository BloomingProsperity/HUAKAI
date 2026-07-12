import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { PasskeyRegistrationControls } from './PasskeyCard'

const handlers = {
  onNameChange: vi.fn(),
  onMethodChange: vi.fn(),
  onStepUpChange: vi.fn(),
  onBegin: vi.fn(),
  onFinish: vi.fn(),
  onCancelPending: vi.fn(),
}

describe('Passkey 注册关键渲染分支', () => {
  it('浏览器不支持时添加按钮禁用且给出可见提示', () => {
    const html = renderToStaticMarkup(
      <PasskeyRegistrationControls
        {...handlers}
        supported={false}
        pending={false}
        busy={false}
        name=""
        method="password"
        stepUpValue=""
      />,
    )
    expect(html).toContain('添加 Passkey')
    expect(html).toContain('disabled')
    expect(html).toContain('当前浏览器不支持 WebAuthn 注册')
  })

  it('两步验证 begin 后要求新码并展示完成按钮', () => {
    const html = renderToStaticMarkup(
      <PasskeyRegistrationControls
        {...handlers}
        supported
        pending
        busy={false}
        name="安全密钥"
        method="two_factor_code"
        stepUpValue=""
      />,
    )
    expect(html).toContain('第一次两步验证码已被安全消费')
    expect(html).toContain('新的动态码 / 另一枚备用码')
    expect(html).toContain('完成 Passkey 注册')
  })
})
