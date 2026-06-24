import { describe, expect, it } from 'vitest'
import { buildKeyUpdate, toIsoOrEmpty, type KeyEditForm } from './edit'
import type { ApiKeyView } from './types'

const withExpiry = { api_key_id: 1, name: 'old', expires_at: '2026-01-01T00:00:00.000Z' } as unknown as ApiKeyView
const neverExpire = { api_key_id: 2, name: 'old', expires_at: null } as unknown as ApiKeyView

function form(over: Partial<KeyEditForm>): KeyEditForm {
  return { name: 'old', expiryMode: 'keep', expiryDate: '', ...over }
}

describe('buildKeyUpdate 到期三态', () => {
  it('keep:不下发 expires_at(保持原到期不变)', () => {
    // 判别核心:keep 模式绝不带 expires_at。变异(keep 也发 '')→ 误清原到期,本断言 RED。
    expect(buildKeyUpdate(withExpiry, form({ name: 'new', expiryMode: 'keep' }))).toEqual({ name: 'new' })
  })

  it('never:原有到期 → 下发空串清除(永不过期)', () => {
    // 判别核心:never 必须发 ''(而非省略)。变异(never 省略)→ 清不掉到期,本断言 RED。
    expect(buildKeyUpdate(withExpiry, form({ expiryMode: 'never' }))).toEqual({ expires_at: '' })
  })

  it('never:原本就永不过期 → 无改动 noop(不发冗余空串)', () => {
    expect(buildKeyUpdate(neverExpire, form({ expiryMode: 'never' }))).toEqual({ noop: true })
  })

  it('date:设定到期 → 下发 ISO', () => {
    const r = buildKeyUpdate(neverExpire, form({ expiryMode: 'date', expiryDate: '2027-06-01T12:00' }))
    expect('expires_at' in (r as object)).toBe(true)
    expect((r as { expires_at: string }).expires_at.startsWith('2027-06-01')).toBe(true)
  })

  it('date:与原到期同一时刻 → 不算改动 noop', () => {
    expect(buildKeyUpdate(withExpiry, form({ expiryMode: 'date', expiryDate: '2026-01-01T00:00:00.000Z' }))).toEqual({ noop: true })
  })

  it('改名:仅变更时下发;名称空报错', () => {
    expect(buildKeyUpdate(withExpiry, form({ name: 'old' }))).toEqual({ noop: true })
    expect(buildKeyUpdate(withExpiry, form({ name: '  ' }))).toEqual({ error: 'Key 名称不能为空' })
  })

  it('date 模式日期非法 → 报错', () => {
    expect(buildKeyUpdate(neverExpire, form({ expiryMode: 'date', expiryDate: 'bad' }))).toEqual({ error: '请选择有效的到期时间' })
  })
})

describe('toIsoOrEmpty', () => {
  it('空/非法 → 空串', () => {
    expect(toIsoOrEmpty('')).toBe('')
    expect(toIsoOrEmpty('nope')).toBe('')
  })
})
