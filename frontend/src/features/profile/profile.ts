/*
 * 个人资料·安全 的纯逻辑(可单测,无 I/O / 无 React)。
 * 校验改密表单、归一化 2FA 状态展示、格式化通行密钥/绑定的展示文案。
 *
 * 安全纪律:本文件绝不接触密钥/密码明文之外的存储,且任何函数都不打印(0 console)。
 * 改密的 new/old 仅做长度/一致性等本地预校验,真正校验由后端 service 层完成。
 */
import type { OAuthBinding, PasskeyItem, TwoFAStatus } from './types'

export interface ChangePasswordForm {
  oldPassword: string
  newPassword: string
  confirmPassword: string
}

export const EMPTY_CHANGE_PASSWORD: ChangePasswordForm = {
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
}

export interface ChangePasswordPayload {
  old_password: string
  new_password: string
}

/**
 * 构造改密请求或返回错误。本地预校验对齐后端 self_account_handler 的护栏:
 *  - new_password 不能为空(后端 strings.TrimSpace 后空一律 400 invalid_password)。
 *  - 这里再要求 ≥8 位 + 两次一致,给用户即时反馈,减少无谓往返。
 * 旧密为空不在前端拦截(后端按 invalid_old_password 401 处理),仅去除首尾空白透传。
 */
export function buildChangePassword(
  form: ChangePasswordForm,
): ChangePasswordPayload | { error: string } {
  const next = form.newPassword
  // 判别核心:新密为空必须报错(对齐后端早返 400)。变异(放行空)→ 该断言 RED。
  if (next.trim().length === 0) return { error: '请填写新密码' }
  if (next.length < 8) return { error: '新密码至少 8 位' }
  if (next !== form.confirmPassword) return { error: '两次输入的新密码不一致' }
  if (next === form.oldPassword) return { error: '新密码不能与旧密码相同' }
  return { old_password: form.oldPassword, new_password: next }
}

/** 显示名本地预校验:1-100 字符、无控制符(对齐后端 userauth.ErrInvalidInput 约束)。 */
export function validateDisplayName(name: string): { ok: true; value: string } | { error: string } {
  const v = name.trim()
  if (v.length === 0) return { error: '显示名不能为空' }
  if (v.length > 100) return { error: '显示名最多 100 个字符' }
  // 控制字符(C0 0x00-0x1F、DEL 0x7F、C1 0x80-0x9F)一律拒绝,避免注入不可见字符。
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f-\u009f]/.test(v)) return { error: '显示名不能包含控制字符' }
  return { ok: true, value: v }
}

/**
 * 把后端 2FA status 响应归一成一个供 UI 直接消费的视图模型。
 * 后端字段:available(平台级开关)/ enabled(本人是否已开)/ backup_codes_remaining。
 */
export interface TwoFAView {
  available: boolean
  enabled: boolean
  backupCodesRemaining: number
  /** UI 主标签:平台关 → 平台未启用;本人开 → 已启用;否则 → 未启用。 */
  label: '已启用' | '未启用' | '平台未启用'
  tone: 'ok' | 'muted' | 'warn'
  /** 备用码偏少(<3)时提示用户重生成。 */
  lowBackupCodes: boolean
}

export function viewTwoFA(status: TwoFAStatus): TwoFAView {
  if (!status.available) {
    return { available: false, enabled: status.enabled, backupCodesRemaining: status.backup_codes_remaining, label: '平台未启用', tone: 'muted', lowBackupCodes: false }
  }
  if (status.enabled) {
    const remaining = status.backup_codes_remaining
    return { available: true, enabled: true, backupCodesRemaining: remaining, label: '已启用', tone: 'ok', lowBackupCodes: remaining < 3 }
  }
  return { available: true, enabled: false, backupCodesRemaining: status.backup_codes_remaining, label: '未启用', tone: 'warn', lowBackupCodes: false }
}

/** 6 位 TOTP 验证码本地预校验:必须是 6 位数字。空 / 非 6 位数字一律拦下。 */
export function isValidTotpCode(code: string): boolean {
  return /^\d{6}$/.test(code.trim())
}

/** 通行密钥展示名:有 name 用 name,否则用「通行密钥 #id」兜底。 */
export function passkeyLabel(p: PasskeyItem): string {
  const n = (p.name ?? '').trim()
  return n.length > 0 ? n : `通行密钥 #${p.id}`
}

/** 社交绑定 provider 的中文展示名(未知 provider 原样回显)。 */
export function providerLabel(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'github':
      return 'GitHub'
    case 'google':
      return 'Google'
    case 'telegram':
      return 'Telegram'
    case 'linuxdo':
      return 'LINUX DO'
    case 'oidc':
      return 'OIDC'
    default:
      return provider
  }
}

/** 解绑某 provider 后是否还剩别的绑定(供 UI 判断是否提示「这是最后一个登录方式」)。 */
export function bindingsWithout(bindings: OAuthBinding[], provider: string): OAuthBinding[] {
  return bindings.filter((b) => b.provider.toLowerCase() !== provider.toLowerCase())
}
