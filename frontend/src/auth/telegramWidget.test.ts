import { describe, expect, it } from 'vitest'
import { telegramWidgetParams, telegramWidgetReady } from './telegramWidget'

describe('telegramWidgetParams', () => {
  it('数值字段转字符串、丢弃 undefined/null、字符串原样保留', () => {
    // 判别核心:后端按收到的字符串重算 HMAC,id/auth_date 必须转成字符串且不丢字段,否则校验必失败。
    // 变异(不 String() 数值,或不丢 undefined)→ id 变成数字 / 出现 photo_url:"undefined",断言 RED。
    const params = telegramWidgetParams({
      id: 424242,
      first_name: 'Ada',
      last_name: undefined,
      username: 'ada_dev',
      photo_url: null as unknown as string,
      auth_date: 1751280000,
      hash: 'abc123',
    })
    expect(params.id).toBe('424242')
    expect(params.auth_date).toBe('1751280000')
    expect(params.first_name).toBe('Ada')
    expect(params.username).toBe('ada_dev')
    expect(params.hash).toBe('abc123')
    expect('last_name' in params).toBe(false)
    expect('photo_url' in params).toBe(false)
  })
})

describe('telegramWidgetReady', () => {
  it('齐备需 id + auth_date + hash 三者皆非空', () => {
    // 判别核心:缺任一命门字段都不可提交(后端缺则 ErrInvalidInput)。
    // 变异(只看 hash 不看 id/auth_date)→ 缺 id 仍判 ready,本断言 RED。
    expect(telegramWidgetReady({ id: '1', auth_date: '2', hash: '3' })).toBe(true)
    expect(telegramWidgetReady({ auth_date: '2', hash: '3' })).toBe(false)
    expect(telegramWidgetReady({ id: '1', hash: '3' })).toBe(false)
    expect(telegramWidgetReady({ id: '1', auth_date: '2' })).toBe(false)
    expect(telegramWidgetReady({})).toBe(false)
  })
})
