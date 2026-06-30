/*
 * 新设备确认页纯逻辑:从 URL query 解出 token / tenant_id,提交前校验,把后端错误码归一成中文文案。
 * 不触碰 DOM / fetch / store,便于单测。与 emailVerify 同款「点邮件链接确认」形态。
 *
 * 背景:当账号开启「新设备需确认」策略(后端 DevicePolicy=confirm + MaxActiveFamilies>0)时,
 * 在新设备登录会被 403 device_confirmation_required 挡住,后端发确认邮件;用户点邮件里的链接进本页,
 * 页面自动 POST /v1/auth/confirm-device {tenant_id, token} 完成确认。token 仅经邮件交付(不在响应体)。
 */

import { ApiError } from '../lib/api'

/** 默认租户。单租户部署恒为 1;链接未带 tenant_id 时回退此值。 */
export const DEFAULT_TENANT_ID = 1

export interface DeviceConfirmParams {
  /** 确认 token(确认邮件链接中的 token,经 trim)。 */
  token: string
  /** 租户 ID;链接缺省或非法时回退 DEFAULT_TENANT_ID。 */
  tenantId: number
}

/**
 * 从 URL query 串解析确认参数。接受 '?token=...&tenant_id=...' 或不带 '?' 的裸 query。
 * token 经 trim;tenant_id 解析失败 / <=0 一律回退默认租户(避免把 0 发给后端必然 400)。
 */
export function parseDeviceConfirmParams(search: string): DeviceConfirmParams {
  const q = new URLSearchParams(search.startsWith('?') ? search.slice(1) : search)
  const token = (q.get('token') ?? '').trim()
  const raw = q.get('tenant_id')
  const parsed = raw === null ? NaN : Number(raw)
  const tenantId = Number.isInteger(parsed) && parsed > 0 ? parsed : DEFAULT_TENANT_ID
  return { token, tenantId }
}

/** 提交前校验。token 为空(链接缺 token)返回中文错误文案;合法返回 null。 */
export function validateDeviceConfirmParams(p: DeviceConfirmParams): string | null {
  if (!p.token) return '确认链接缺少 token,请使用确认邮件中的完整链接。'
  return null
}

/** 后端错误码:token 不存在/跨租户/已被消费(session_handler.go,401)。 */
export const DEVICE_INVALID_CODE = 'device_confirmation_invalid'
/** 后端错误码:token 过期(401)。 */
export const DEVICE_EXPIRED_CODE = 'device_confirmation_expired'

/**
 * 把确认失败的异常归一成中文文案。判别核心:invalid 与 expired 必须各自映射到专属提示
 * (用户需分清是「链接已用/无效」还是「过期」),否则分不清该重登还是重发。
 * 变异(把 invalid/expired 并入兜底)→ 测试 RED。
 */
export function deviceConfirmErrorMessage(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.code === DEVICE_INVALID_CODE) {
      return '确认链接无效或已被使用,请重新登录以获取新的设备确认邮件。'
    }
    if (e.code === DEVICE_EXPIRED_CODE) {
      return '确认链接已过期,请重新登录以获取新的设备确认邮件。'
    }
    if (e.code === 'invalid_device_confirmation_request') {
      return '确认请求无效,请检查链接是否完整。'
    }
    return `设备确认失败:${e.message}(${e.code})`
  }
  return '设备确认失败,请稍后重试。'
}
