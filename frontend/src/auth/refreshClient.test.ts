import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ensureFreshSession } from './refreshClient'
import { clearAll, getRefreshToken, getTokens, setSessionTokens } from './store'

/*
 * refreshClient 接线测试:用 fetch mock 覆盖刷新-轮换-清态这条安全敏感路径。
 * 重点守护 family-安全不变量:刷新成功必须把后端「轮换后的新 refresh token」写回 store,
 * 否则单标签下一次刷新会复用旧 token 二次消费,触发后端 family 重放检测撤销整条会话族。
 */
function mockFetch(impl: () => { ok: boolean; status?: number; body: unknown }) {
  return vi.fn(async () => {
    const r = impl()
    return {
      ok: r.ok,
      status: r.status ?? (r.ok ? 200 : 400),
      json: async () => r.body,
    } as Response
  })
}

beforeEach(() => {
  clearAll()
  setSessionTokens({ sessionToken: 'hus_old', refreshToken: 'husr_old', sessionExpiresAt: '2026-06-25T10:00:00Z' })
})
afterEach(() => {
  vi.restoreAllMocks()
})

describe('ensureFreshSession 刷新成功', () => {
  it('写回轮换后的新 session+refresh token(family-安全不变量)', async () => {
    globalThis.fetch = mockFetch(() => ({
      ok: true,
      body: {
        session: { session_token: 'hus_new', refresh_token: 'husr_new', session_expires_at: '2026-06-25T10:15:00Z' },
      },
    }))
    const ok = await ensureFreshSession()
    expect(ok).toBe(true)
    // 判别核心:新 refresh token 必须写回。变异(doRefresh 不调 setSessionTokens)→ 仍是 husr_old → RED。
    expect(getRefreshToken()).toBe('husr_new')
    expect(getTokens().sessionToken).toBe('hus_new')
  })

  it('请求体带当前 refresh token,打到 /v1/sessions/refresh', async () => {
    const f = mockFetch(() => ({
      ok: true,
      body: { session: { session_token: 'hus_new', refresh_token: 'husr_new', session_expires_at: '2026-06-25T10:15:00Z' } },
    }))
    globalThis.fetch = f
    await ensureFreshSession()
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('/v1/sessions/refresh')
    expect(JSON.parse(init.body as string)).toEqual({ refresh_token: 'husr_old' })
  })
})

describe('ensureFreshSession 刷新失败', () => {
  it('不可恢复错误码(replay)→ clearAll 强制登出', async () => {
    globalThis.fetch = mockFetch(() => ({ ok: false, status: 409, body: { error: { code: 'refresh_token_replay' } } }))
    const ok = await ensureFreshSession()
    expect(ok).toBe(false)
    // 判别核心:logout 类错误必须清态。变异(失败不分类/不 clearAll)→ token 仍在 → RED。
    expect(getTokens().sessionToken).toBeNull()
    expect(getRefreshToken()).toBeNull()
  })

  it('瞬时错误(backend_error)→ 保留会话,不踢人', async () => {
    globalThis.fetch = mockFetch(() => ({ ok: false, status: 503, body: { error: { code: 'session_backend_error' } } }))
    const ok = await ensureFreshSession()
    expect(ok).toBe(false)
    // 瞬时失败必须保留现有会话(否则后端抖动就把人踢了)。
    expect(getTokens().sessionToken).toBe('hus_old')
    expect(getRefreshToken()).toBe('husr_old')
  })

  it('网络异常(fetch reject)→ 保留会话', async () => {
    globalThis.fetch = vi.fn(async () => {
      throw new Error('network down')
    })
    const ok = await ensureFreshSession()
    expect(ok).toBe(false)
    expect(getTokens().sessionToken).toBe('hus_old')
  })

  it('无 refresh token → 直接返回 false 不发请求', async () => {
    clearAll()
    setSessionTokens({ sessionToken: 'hus_x', refreshToken: null, sessionExpiresAt: null })
    const f = vi.fn()
    globalThis.fetch = f as never
    const ok = await ensureFreshSession()
    expect(ok).toBe(false)
    expect(f).not.toHaveBeenCalled()
  })
})
