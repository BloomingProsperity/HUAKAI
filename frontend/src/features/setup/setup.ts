/*
 * 首装向导纯逻辑与 API:全新部署(工作租户无管理员)时引导创建第一个管理员。
 * 后端 fail-closed:装完 install 一律 403,前端只是引导层不是授权边界。
 */
import { apiGet, apiSend } from '../../lib/api'

export interface SetupStatus {
  needs_setup: boolean
}

export async function fetchSetupStatus(signal?: AbortSignal): Promise<SetupStatus> {
  return apiGet<SetupStatus>('/setup/status', { signal })
}

export interface InstallRequest {
  email: string
  password: string
  display_name?: string
}

export async function installAdmin(req: InstallRequest): Promise<{ email: string }> {
  return apiSend<{ email: string }>('POST', '/setup/install', req)
}

/** 提交前的表单校验;返回错误文案,合法返回 null。与后端约束同口径(口令 8-128)。 */
export function validateInstallForm(email: string, password: string, confirm: string): string | null {
  const e = email.trim()
  if (!e || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(e)) return '请输入合法邮箱'
  if (password.length < 8) return '密码至少 8 位'
  if (password.length > 128) return '密码最长 128 位'
  if (password !== confirm) return '两次输入的密码不一致'
  return null
}

/** 后端错误码 → 用户文案。 */
export function installErrorText(code: string): string {
  switch (code) {
    case 'already_installed':
      return '系统已完成安装,请直接登录'
    case 'email_taken':
      return '该邮箱已被占用,请换一个'
    case 'weak_password':
      return '密码强度不足(至少 8 位)'
    case 'invalid_email':
      return '邮箱格式不合法'
    default:
      return '安装失败,请稍后重试'
  }
}
