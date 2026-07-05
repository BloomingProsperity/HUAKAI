import { describe, expect, it } from 'vitest'
import { ApiError } from '../lib/api'
import { errorMessage } from './errorFallback'

describe('errorMessage 归一', () => {
  it('ApiError → message + code', () => {
    const e = errorMessage(new ApiError(500, 'usage_query_failed', '用量后端不可用'))
    expect(e.title).toBe('请求出错')
    expect(e.detail).toBe('用量后端不可用(usage_query_failed)')
  })

  it('路由 ErrorResponse 404 → 页面不存在', () => {
    const e = errorMessage({ status: 404, statusText: 'Not Found' })
    expect(e.title).toBe('页面不存在')
    expect(e.detail).toContain('404')
  })

  it('路由 ErrorResponse 401/403 → 无权访问', () => {
    expect(errorMessage({ status: 401, statusText: 'Unauthorized' }).title).toBe('无权访问')
    expect(errorMessage({ status: 403, statusText: 'Forbidden' }).title).toBe('无权访问')
  })

  it('其它路由 status → 通用页面出错带 statusText/status', () => {
    const e = errorMessage({ status: 500, statusText: 'Internal Server Error' })
    expect(e.title).toBe('页面出错')
    expect(e.detail).toContain('500')
    expect(e.detail).toContain('Internal Server Error')
  })

  it('原生 Error → 展示 message,不暴露 stack', () => {
    const e = errorMessage(new Error('Cannot read properties of undefined'))
    expect(e.title).toBe('页面渲染出错')
    expect(e.detail).toBe('Cannot read properties of undefined')
  })

  it('空 message 的 Error → 兜底文案', () => {
    expect(errorMessage(new Error('')).detail).toBe('发生了未知错误。')
    expect(errorMessage(new Error('   ')).detail).toBe('发生了未知错误。')
  })

  it('字符串错误 → 原样(trim)', () => {
    expect(errorMessage('  网络中断  ').detail).toBe('网络中断')
  })

  it('未知形态(null/数字/空串)→ 兜底', () => {
    expect(errorMessage(null).detail).toBe('发生了未知错误,请刷新重试。')
    expect(errorMessage(42).detail).toBe('发生了未知错误,请刷新重试。')
    expect(errorMessage('').detail).toBe('发生了未知错误,请刷新重试。')
  })

  it('ApiError 优先于 Error 分支(instanceof 顺序)', () => {
    // ApiError 继承 Error;必须先命中 ApiError 分支拿到 code,而非落到 Error 分支
    const e = errorMessage(new ApiError(403, 'admin_forbidden', '无权'))
    expect(e.detail).toContain('admin_forbidden')
  })

  it('伪 ErrorResponse(status 非数字)不被误判,落 Error/兜底', () => {
    // status 是字符串 → 不是 ErrorResponse,且非 Error/字符串 → 兜底
    expect(errorMessage({ status: '404', statusText: 'x' }).detail).toBe('发生了未知错误,请刷新重试。')
  })
})
