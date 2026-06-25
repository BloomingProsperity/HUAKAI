import { ApiError } from '../lib/api'

/*
 * 错误边界纯逻辑(可单测):把 react-router useRouteError 抛出的各种形态归一成
 * { title, detail } 友好中文文案。覆盖 ApiError(网关错误)、路由 ErrorResponse(status/statusText)、
 * 原生 Error、字符串、未知。绝不把原始 stack/对象直接糊给用户。
 */

export interface FriendlyError {
  title: string
  detail: string
}

/** 是否 react-router 的 ErrorResponse(throw new Response 或 loader 错误)。结构判定,不依赖 import。 */
function isRouteErrorResponse(err: unknown): err is { status: number; statusText: string; data?: unknown } {
  return (
    typeof err === 'object' &&
    err !== null &&
    'status' in err &&
    typeof (err as { status: unknown }).status === 'number' &&
    'statusText' in err
  )
}

export function errorMessage(err: unknown): FriendlyError {
  // 网关 ApiError:带 code,展示 message + code。
  if (err instanceof ApiError) {
    return { title: '请求出错', detail: `${err.message}(${err.code})` }
  }
  // 路由层 ErrorResponse:按 HTTP 状态给文案。
  if (isRouteErrorResponse(err)) {
    if (err.status === 404) return { title: '页面不存在', detail: '该地址没有对应页面(404)。' }
    if (err.status === 401 || err.status === 403) {
      return { title: '无权访问', detail: `登录态或权限不足(${err.status})。请重新登录后再试。` }
    }
    return { title: '页面出错', detail: `${err.statusText || '请求失败'}(${err.status})。` }
  }
  // 原生 Error(渲染期抛出):展示 message,不暴露 stack。
  if (err instanceof Error) {
    const msg = err.message.trim()
    return { title: '页面渲染出错', detail: msg || '发生了未知错误。' }
  }
  // 字符串。
  if (typeof err === 'string' && err.trim()) {
    return { title: '页面出错', detail: err.trim() }
  }
  // 兜底。
  return { title: '页面出错', detail: '发生了未知错误,请刷新重试。' }
}
