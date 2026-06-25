import { describe, expect, it } from 'vitest'
import {
  decideCallbackOutcome,
  oauthErrorMessage,
  parseCallbackParams,
  type CallbackParams,
  type PendingOAuth,
} from './oauthCallback'

describe('parseCallbackParams', () => {
  it('从 query 串解析 code/state/error(带或不带前导 ?)', () => {
    const a = parseCallbackParams('?code=abc&state=xyz')
    expect(a.code).toBe('abc')
    expect(a.state).toBe('xyz')
    expect(a.error).toBeNull()
    const b = parseCallbackParams('error=access_denied&error_description=user%20said%20no')
    expect(b.error).toBe('access_denied')
    expect(b.errorDescription).toBe('user said no')
  })
  it('空白值归一为 null(避免把 ?code= 当成有效 code)', () => {
    // 判别核心:空串/纯空白必须当缺失。变异(直接返回 q.get 不判空)→ code 变 "" 非 null → RED。
    const p = parseCallbackParams('?code=&state=%20%20')
    expect(p.code).toBeNull()
    expect(p.state).toBeNull()
  })
})

describe('decideCallbackOutcome', () => {
  const okParams: CallbackParams = { code: 'CODE1', state: 'ST1', error: null, errorDescription: null }
  const pending: PendingOAuth = { provider: 'github', tenantId: 1, state: 'ST1' }

  it('参数齐全且 state 一致 → complete,带回 provider/tenant', () => {
    const out = decideCallbackOutcome({ params: okParams, pending })
    expect(out.kind).toBe('complete')
    if (out.kind === 'complete') {
      expect(out.provider).toBe('github')
      expect(out.tenantId).toBe(1)
      expect(out.code).toBe('CODE1')
      expect(out.state).toBe('ST1')
    }
  })

  it('上游回跳带 error → error(优先于一切)', () => {
    const out = decideCallbackOutcome({
      params: { ...okParams, error: 'access_denied' },
      pending,
    })
    expect(out.kind).toBe('error')
    if (out.kind === 'error') expect(out.message).toContain('取消')
  })

  it('缺 code 或 state → error,不发请求', () => {
    expect(decideCallbackOutcome({ params: { ...okParams, code: null }, pending }).kind).toBe('error')
    expect(decideCallbackOutcome({ params: { ...okParams, state: null }, pending }).kind).toBe('error')
  })

  it('无暂存上下文 → error(无法得知 provider/tenant)', () => {
    // 判别核心:pending 为 null 必须判错。变异(忽略 pending 缺失直接 complete)→ provider 为 undefined 仍发请求 → RED。
    const out = decideCallbackOutcome({ params: okParams, pending: null })
    expect(out.kind).toBe('error')
    if (out.kind === 'error') expect(out.message).toContain('登录上下文')
  })

  it('暂存 state 与回跳 state 不一致 → error(CSRF 防护)', () => {
    // 判别核心:state 串流必须拦。变异(删掉 state 比对分支)→ 不一致也 complete → RED。
    const out = decideCallbackOutcome({
      params: okParams,
      pending: { provider: 'github', tenantId: 1, state: 'DIFFERENT' },
    })
    expect(out.kind).toBe('error')
    if (out.kind === 'error') expect(out.message).toContain('state')
  })

  it('暂存 state 为空时跳过交叉校验(后端 cookie 仍兜底)→ complete', () => {
    const out = decideCallbackOutcome({
      params: okParams,
      pending: { provider: 'github', tenantId: 1, state: '' },
    })
    expect(out.kind).toBe('complete')
  })

  it('tenantId 非法(<=0)→ error', () => {
    const out = decideCallbackOutcome({ params: okParams, pending: { provider: 'github', tenantId: 0, state: 'ST1' } })
    expect(out.kind).toBe('error')
  })
})

describe('oauthErrorMessage', () => {
  it('已知错误码翻译成友好中文', () => {
    expect(oauthErrorMessage('access_denied', null)).toContain('取消')
    expect(oauthErrorMessage('server_error', null)).toContain('暂时不可用')
  })
  it('未知错误码原样附带 + 描述', () => {
    expect(oauthErrorMessage('weird_code', 'boom')).toContain('weird_code')
    expect(oauthErrorMessage('weird_code', 'boom')).toContain('boom')
  })
})
