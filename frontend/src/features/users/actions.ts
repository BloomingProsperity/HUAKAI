/*
 * 用户运维动作的纯逻辑(可单测)。仅做前端校验镜像与展示格式化,真正的鉴权/审计/落库在后端。
 * 校验阈值刻意镜像 adminuserhttp:group 1..64 字符、remark ≤1024 字符。
 */

/** 2FA 普及率统计响应(GET /admin/v1/users/2fa-adoption-stats)。 */
export interface TwoFAAdoptionStats {
  enabled_users: number
  total_users: number
  /** 后端已算好的占比(0..1);前端展示时再转百分比。 */
  enabled_rate: number
}

/** 可解绑的社交登录 provider(镜像 userauth/social_login.go 支持的家族)。 */
export const SOCIAL_PROVIDERS: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'google', label: 'Google' },
  { value: 'github', label: 'GitHub' },
  { value: 'qq', label: 'QQ' },
  { value: 'wechat', label: '微信' },
  { value: 'dingtalk', label: '钉钉' },
  { value: 'discord', label: 'Discord' },
  { value: 'telegram', label: 'Telegram' },
  { value: 'linuxdo', label: 'LinuxDo' },
  { value: 'nodeseek', label: 'NodeSeek' },
  { value: 'oidc', label: 'OIDC' },
]

/**
 * 把占比格式化为整数百分比展示;total=0(无用户)或非有限值时回退 "—",避免 0/0=NaN 漏到 UI。
 * 取整用 Math.round(rate*100),以 enabled_rate 为准(后端已算)。
 */
export function formatAdoptionRate(stats: TwoFAAdoptionStats): string {
  if (!stats || stats.total_users <= 0 || !Number.isFinite(stats.enabled_rate)) return '—'
  return `${Math.round(stats.enabled_rate * 100)}%`
}

/** 校验 group:非空且 ≤64 字符。返回错误文案或 null(合法)。镜像后端 invalid_group。 */
export function validateGroup(group: string): string | null {
  const g = group.trim()
  if (g === '') return '用户组不能为空'
  if (g.length > 64) return '用户组最多 64 字符'
  return null
}

/** 校验 remark:≤1024 字符(允许空=清空备注)。返回错误文案或 null(合法)。镜像后端 invalid_remark。 */
export function validateRemark(remark: string): string | null {
  if (remark.length > 1024) return '备注最多 1024 字符'
  return null
}
