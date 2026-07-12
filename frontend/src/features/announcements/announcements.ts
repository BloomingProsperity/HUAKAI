import type { CreateAnnouncementRequest, Severity, UpdateAnnouncementRequest } from './types'

/*
 * 公告管理的纯逻辑(可单测):级别枚举/展示、表单构造与校验、生效态判定、删除二次确认文案。
 * 校验镜像后端 service.go:validateAnnouncement(167):title/body 非空、severity 限三值、
 * expires_at 若有必须严格晚于 published_at。客户端先挡住给清晰中文提示,避免无谓 400。
 */

/** 默认租户。单租户部署(运维者自跑实例)恒为 1;与 emailVerify.ts 的兜底一致。 */
export const DEFAULT_TENANT_ID = 1

/** 级别枚举 + 中文标签 + 徽章语气。顺序即下拉顺序。 */
export const SEVERITIES: ReadonlyArray<{ value: Severity; label: string; tone: 'info' | 'warn' | 'danger' }> = [
  { value: 'info', label: '通知', tone: 'info' },
  { value: 'warning', label: '警告', tone: 'warn' },
  { value: 'critical', label: '严重', tone: 'danger' },
]

export function severityLabel(severity: string): string {
  return SEVERITIES.find((s) => s.value === severity)?.label ?? severity
}

export function severityTone(severity: string): 'info' | 'warn' | 'danger' | 'muted' {
  return SEVERITIES.find((s) => s.value === severity)?.tone ?? 'muted'
}

/** 公告表单态。published_at / expires_at 为 datetime-local 串(本地时间,空=不设置)。 */
export interface AnnouncementForm {
  title: string
  body: string
  severity: Severity
  active: boolean
  publishedAt: string
  expiresAt: string
}

export const EMPTY_ANNOUNCEMENT_FORM: AnnouncementForm = {
  title: '',
  body: '',
  severity: 'info',
  active: true,
  publishedAt: '',
  expiresAt: '',
}

/**
 * 把 datetime-local 串(无时区,浏览器按本地解释)转 RFC3339(UTC),供后端解析。
 * 空串 → undefined(交后端默认:published_at 缺省取 now)。非法串 → null(调用方据此报错)。
 */
export function localToRFC3339(local: string): string | undefined | null {
  const v = local.trim()
  if (!v) return undefined
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return null
  return d.toISOString()
}

/**
 * 构造新建请求。校验:标题/正文非空、级别合法、生效起止可解析且止须晚于起。
 * 返回 {error} 时调用方展示中文提示,不发请求。
 */
export function buildCreate(form: AnnouncementForm, tenantId: number): CreateAnnouncementRequest | { error: string } {
  const base = validateCore(form)
  if ('error' in base) return base
  const req: CreateAnnouncementRequest = {
    tenant_id: tenantId,
    title: base.title,
    body: base.body,
    severity: form.severity,
    active: form.active,
  }
  if (base.publishedAt !== undefined) req.published_at = base.publishedAt
  if (base.expiresAt !== undefined) req.expires_at = base.expiresAt
  return req
}

/**
 * 构造编辑请求(全量提交局部字段)。同样校验;expires_at 空串显式传 null 以清空
 * (后端 optionalTime:null 清除过期时间)。
 */
export function buildUpdate(form: AnnouncementForm): UpdateAnnouncementRequest | { error: string } {
  const base = validateCore(form)
  if ('error' in base) return base
  const req: UpdateAnnouncementRequest = {
    title: base.title,
    body: base.body,
    severity: form.severity,
    active: form.active,
  }
  if (base.publishedAt !== undefined) req.published_at = base.publishedAt
  // 编辑时:有过期时间则设置,空串则显式清空(null),区别于新建的「不传」。
  req.expires_at = base.expiresAt === undefined ? null : base.expiresAt
  return req
}

interface CoreValid {
  title: string
  body: string
  publishedAt: string | undefined
  expiresAt: string | undefined
}

/** 共享校验:返回归一化后的字段,或 {error}。 */
function validateCore(form: AnnouncementForm): CoreValid | { error: string } {
  const title = form.title.trim()
  const body = form.body.trim()
  if (!title) return { error: '请填写标题' }
  if (!body) return { error: '请填写正文' }
  if (!SEVERITIES.some((s) => s.value === form.severity)) return { error: '级别非法' }

  const publishedAt = localToRFC3339(form.publishedAt)
  if (publishedAt === null) return { error: '生效时间格式非法' }
  const expiresAt = localToRFC3339(form.expiresAt)
  if (expiresAt === null) return { error: '过期时间格式非法' }

  // 后端要求 expires_at 严格晚于 published_at(service.go:182)。published_at 缺省时按 now 兜底比较。
  if (expiresAt !== undefined) {
    const startMs = publishedAt !== undefined ? Date.parse(publishedAt) : Date.now()
    if (Date.parse(expiresAt) <= startMs) return { error: '过期时间必须晚于生效时间' }
  }
  return { title, body, publishedAt, expiresAt }
}

/**
 * 当前展示态:用于列表徽章。active=false → 已停用;active 但未到生效时间 → 待生效;
 * active 且已过期 → 已过期;否则 → 生效中。now 注入便于测试。
 */
export type DisplayState = 'disabled' | 'scheduled' | 'expired' | 'live'

export function displayState(
  a: { active: boolean; published_at: string; expires_at?: string | null },
  now: number = Date.now(),
): DisplayState {
  if (!a.active) return 'disabled'
  const pub = Date.parse(a.published_at)
  if (!Number.isNaN(pub) && pub > now) return 'scheduled'
  if (a.expires_at) {
    const exp = Date.parse(a.expires_at)
    if (!Number.isNaN(exp) && exp <= now) return 'expired'
  }
  return 'live'
}

export function displayStateLabel(state: DisplayState): string {
  switch (state) {
    case 'disabled':
      return '已停用'
    case 'scheduled':
      return '待生效'
    case 'expired':
      return '已过期'
    case 'live':
      return '生效中'
  }
}

export function displayStateTone(state: DisplayState): 'ok' | 'warn' | 'muted' {
  switch (state) {
    case 'live':
      return 'ok'
    case 'scheduled':
      return 'warn'
    default:
      return 'muted'
  }
}

/** 启停目标:active → 停用;停用 → 启用。供列表行内快捷切换。 */
export function toggleActiveTarget(active: boolean): boolean {
  return !active
}
