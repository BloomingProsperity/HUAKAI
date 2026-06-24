import type { CreateUserRequest } from './types'

/*
 * 用户管理的纯逻辑(可单测)。创建请求构造 + 角色/状态枚举与展示。
 */
export const USER_ROLES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'user', label: '普通用户' },
  { value: 'admin', label: '管理员' },
]

export function roleLabel(role: string): string {
  return USER_ROLES.find((r) => r.value === role)?.label ?? role
}

export function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return '正常'
    case 'disabled':
      return '已停用'
    case 'locked':
      return '已锁定'
    default:
      return status
  }
}

export interface CreateUserForm {
  email: string
  password: string
  displayName: string
  role: string
}

export const EMPTY_CREATE_USER: CreateUserForm = {
  email: '',
  password: '',
  displayName: '',
  role: 'user',
}

export function buildCreateUser(form: CreateUserForm): CreateUserRequest | { error: string } {
  const email = form.email.trim()
  if (!email || !email.includes('@')) return { error: '请填写有效邮箱' }
  if (form.password.length < 8) return { error: '密码至少 8 位' }
  if (!USER_ROLES.some((r) => r.value === form.role)) return { error: '角色非法' }
  const req: CreateUserRequest = { email, password: form.password, role: form.role }
  const dn = form.displayName.trim()
  if (dn) req.display_name = dn
  return req
}

/** 启停目标:active → 停用(disabled);其余 → 启用(active)。 */
export function toggleStatusTarget(status: string): string {
  return status === 'active' ? 'disabled' : 'active'
}
