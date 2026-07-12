import { describe, expect, it } from 'vitest'
import { ApiError } from '../lib/api'
import { callbackErrorMessage, isOAuthPendingBody, pendingErrorMessage } from './oauthCallback'

/*
 * 「社交登录无邮箱→补邮箱建号」前端纯逻辑测试(node,无 DOM/网络)。判别核心:
 *  ① isOAuthPendingBody 只对 code=oauth_pending_email_required 且带 pending_token 的体判为待补邮箱,
 *     否则一律 null(否则会把正常登录/异常体误当待补邮箱,或把待补邮箱当登录成功走去解析 session);
 *  ② 错误文案:主回调 401 归一,补邮箱步骤保留后端 code 且不引导回登录。
 */

describe('isOAuthPendingBody', () => {
  it('code + pending_token → 判为待补邮箱', () => {
    // 变异:若判定漏掉 code 检查(只看 pending_token)或漏掉 pending_token → 下列断言之一 RED。
    expect(isOAuthPendingBody({ code: 'oauth_pending_email_required', pending_token: 'ptok' })).toEqual({
      pendingToken: 'ptok',
    })
  })
  it('正常登录体(无 code)→ null', () => {
    expect(isOAuthPendingBody({} as { code?: string })).toBeNull()
    expect(isOAuthPendingBody(undefined)).toBeNull()
    expect(isOAuthPendingBody(null)).toBeNull()
  })
  it('有 code 但缺 pending_token → null(不可进补邮箱流程)', () => {
    expect(isOAuthPendingBody({ code: 'oauth_pending_email_required' })).toBeNull()
    expect(isOAuthPendingBody({ code: 'oauth_pending_email_required', pending_token: '' })).toBeNull()
  })
  it('其它 code → null', () => {
    expect(isOAuthPendingBody({ code: 'something_else', pending_token: 'x' })).toBeNull()
  })
})

describe('错误文案', () => {
  it('主回调 401 归一成验证失败(不泄露细节)', () => {
    expect(callbackErrorMessage(new ApiError(401, 'x', 'y'))).toBe('社交登录验证失败,请重新登录')
  })
  it('主回调非 401 带上后端 code', () => {
    expect(callbackErrorMessage(new ApiError(400, 'bad_state', '状态无效'))).toContain('bad_state')
  })
  it('非 ApiError 兜底文案', () => {
    expect(callbackErrorMessage(new Error('boom'))).toBe('社交登录完成失败,请重新登录')
  })
  it('补邮箱步骤错误保留 code(留在当前步重试)', () => {
    expect(pendingErrorMessage(new ApiError(400, 'oauth_code_invalid', '验证码错误'))).toContain('oauth_code_invalid')
    expect(pendingErrorMessage('x')).toBe('操作失败,请重试')
  })
})
