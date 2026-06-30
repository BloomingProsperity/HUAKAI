import { apiSend } from '../lib/api'

/*
 * 新设备确认数据访问层。端点 POST /v1/auth/confirm-device(公开,/v1/auth/* 不带 token;token 即凭证)。
 * 后端 session_handler.go:277 newConfirmDeviceHandler:
 *   请求 {tenant_id:int64, token:string}(二者必填,缺则 400 invalid_device_confirmation_request)
 *   成功 200 {status:'device_confirmed'}
 *   失败 401 device_confirmation_invalid(不存在/跨租户/已用)/ 401 device_confirmation_expired(过期)/
 *        503 device_confirmation_backend_error。
 * 与 verify-email 同款:无需已登录,凭邮件链接里的 token 确认;确认同时腾出最老设备槽(rotation.go ConfirmDevice)。
 */

export interface ConfirmDeviceResponse {
  status?: string
}

/** 凭 token 确认新设备。tenantId 必须 > 0(否则后端 400)。 */
export async function confirmDevice(tenantId: number, token: string): Promise<ConfirmDeviceResponse> {
  return apiSend<ConfirmDeviceResponse>('POST', '/v1/auth/confirm-device', {
    tenant_id: tenantId,
    token,
  })
}
