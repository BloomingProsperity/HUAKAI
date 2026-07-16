import type { BadgeTone } from '../../ui/StatusBadge'
import type { TwoFAAdoptionStats } from './actions'
import { formatAdoptionRate } from './actions'
import type { AdminUser, CreateUserRequest } from './types'

/*
 * 用户管理的纯逻辑(可单测)。创建请求构造 + 角色/状态枚举与展示。
 */
export const USER_ROLES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'user', label: '普通用户' },
  { value: 'admin', label: '管理员' },
]

/**
 * 创建用户时可选的角色:仅 user。后端 setUserCreateRequest 对任何 role!='user' 一律返回
 * 403 admin_role_forbidden(越权护栏),故创建下拉不能暴露 admin,否则选了必失败。
 * 提升管理员要走专门的角色变更路径,不在新建流程。
 */
export const CREATE_USER_ROLES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'user', label: '普通用户' },
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
  // 创建仅允许 user 角色(后端越权护栏拒 admin),客户端先挡住给清晰提示。
  if (!CREATE_USER_ROLES.some((r) => r.value === form.role)) return { error: '新建用户仅支持普通用户角色' }
  const req: CreateUserRequest = { email, password: form.password, role: form.role }
  const dn = form.displayName.trim()
  if (dn) req.display_name = dn
  return req
}

/** 启停目标:active → 停用(disabled);其余 → 启用(active)。 */
export function toggleStatusTarget(status: string): string {
  return status === 'active' ? 'disabled' : 'active'
}

export const USERS_PAGE_LIMIT = 100
export const USERS_PAGE_LIMIT_OPTIONS = [25, 50, 100] as const

export interface UserStatView {
  label: string
  value: string
  hint: string
}

/** 统计端点到两张统计卡的唯一映射；缺数时明确不可用，不把失败伪装成零。 */
export function mapUserStats(stats: TwoFAAdoptionStats | null): UserStatView[] {
  if (!stats) {
    return [
      { label: '总用户', value: '—', hint: '全租户统计暂不可用' },
      { label: '2FA 普及率', value: '—', hint: '全租户统计暂不可用' },
    ]
  }
  return [
    {
      label: '总用户',
      value: `${stats.total_users.toLocaleString('zh-CN')} 人`,
      hint: '全租户用户，不受当前分页影响',
    },
    {
      label: '2FA 普及率',
      value: formatAdoptionRate(stats),
      hint: `${stats.enabled_users.toLocaleString('zh-CN')} 人已开启 / 共 ${stats.total_users.toLocaleString('zh-CN')} 人`,
    },
  ]
}

export interface UserTableRow {
  id: number
  email: string
  role: string
  status: string
  statusText: string
  statusTone: BadgeTone
  userGroup: string
  remark: string
  balance: string
  createdAt: string
}

/** 后端列表项到表格行的纯映射；空组与空备注都给可辨认占位。 */
export function mapUserRows(users: AdminUser[]): UserTableRow[] {
  return users.map((user) => {
    const date = new Date(user.created_at)
    const statusTones: Record<string, BadgeTone> = { active: 'ok', disabled: 'muted', locked: 'danger' }
    return {
      id: user.id,
      email: user.email,
      role: roleLabel(user.role),
      status: user.status,
      statusText: statusLabel(user.status),
      statusTone: statusTones[user.status] ?? 'muted',
      userGroup: user.user_group.trim() || '未分组',
      remark: user.remark.trim() || '无备注',
      balance: `${user.balance} USD`,
      createdAt: Number.isNaN(date.getTime())
        ? '—'
        : date.toLocaleString('zh-CN', { hour12: false }),
    }
  })
}

export interface UserPaginationView {
  page: number
  start: number
  end: number
  canPrevious: boolean
  canNext: boolean
  scopeText: string
}

/** offset 分页展示映射；搜索无筛选总数时只用满页信号，不冒充精确总数。 */
export function mapUserPagination(input: {
  offset: number
  limit: number
  returnedCount: number
  totalUsers: number | null
  searching: boolean
}): UserPaginationView {
  const { offset, limit, returnedCount, totalUsers, searching } = input
  const safeLimit = limit > 0 ? limit : USERS_PAGE_LIMIT
  const start = returnedCount > 0 ? offset + 1 : 0
  const end = returnedCount > 0 ? offset + returnedCount : 0
  const hasExactPageTotal = !searching && totalUsers !== null
  const canNext = hasExactPageTotal
    ? end < totalUsers
    : returnedCount >= safeLimit
  const totalContext = totalUsers === null
    ? '全体总数暂不可用'
    : `全体共 ${totalUsers.toLocaleString('zh-CN')} 人`

  return {
    page: Math.floor(offset / safeLimit) + 1,
    start,
    end,
    canPrevious: offset > 0,
    canNext,
    scopeText: searching
      ? `当前搜索第 ${start}–${end} 条 · ${totalContext}`
      : `第 ${start}–${end} 条 · ${totalContext}`,
  }
}
