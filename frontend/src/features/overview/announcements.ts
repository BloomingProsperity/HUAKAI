/*
 * 用户侧公告(概览页顶部横幅)数据访问层 + 类型 + 纯逻辑。
 *
 * 端点(动手前已核 backend 真码):
 *   - 用户读公告  GET /v1/announcements
 *     → announcementhttp.MountUserRoutes(handlers.go:118-120),newUserListHandler(handlers.go:129-159)
 *     → 接线 backend/cmd/gateway/routes.go:244(announcementhttp.MountUserRoutes(r, UserDeps{...}))
 *
 * 鉴权:session。后端从 session 上下文解析租户(handlers.go:293-295,SessionFromContext → TenantID),
 * 路径不带 /admin 前缀,故前端 tokenForPath 自动注入 session token(tokenForPath.ts:42-52),
 * 与概览其它卡(/v1/me/*、/v1/api-keys)同源。无 session 时后端 401,横幅整体降级不展示。
 *
 * 后端只返回当前生效的公告(ListActive,handlers.go:143),前端无需再过滤 active/已过期;
 * 但仍按 published_at/expires_at 做一层防御性"窗口内"判定,避免后端策略变动时误展示。
 */

import { apiGet } from '../../lib/api'

/** 公告级别(后端 announcement.Severity 枚举,字符串透传)。已知三值,未知值回退中性样式。 */
export type AnnouncementSeverity = 'info' | 'warning' | 'critical' | string

/**
 * 用户侧公告项,镜像 announcementhttp.announcementResponse(handlers.go:99-111)。
 * 时间字段为 RFC3339 字符串(formatTime,handlers.go:472-477);可能为空串(零值)。
 * 用户横幅只用到 id/title/body/severity/published_at/expires_at,其余字段保留以对齐 DTO。
 */
export interface UserAnnouncement {
  id: number
  tenant_id: number
  title: string
  body: string
  severity: AnnouncementSeverity
  active: boolean
  published_at: string
  expires_at?: string | null
  created_by_admin?: number | null
  created_at: string
  updated_at: string
}

/** 列表响应,镜像 announcementListResponse(handlers.go:92-97)。 */
export interface UserAnnouncementListResponse {
  object: string
  items: UserAnnouncement[]
  limit: number
  offset: number
}

/** 拉取当前生效公告。session 鉴权(tokenForPath 自动注入)。 */
export async function listUserAnnouncements(signal?: AbortSignal): Promise<UserAnnouncementListResponse> {
  return apiGet<UserAnnouncementListResponse>('/v1/announcements', { signal })
}

// ───────────────────────── 纯逻辑(可单测、无 DOM/无网络) ─────────────────────────

/** 解析 RFC3339 时间串为毫秒;空串/无效返回 null(后端零值会给空串,见 formatTime)。 */
function parseTimeMs(s: string | null | undefined): number | null {
  if (!s) return null
  const t = Date.parse(s)
  return Number.isFinite(t) ? t : null
}

/**
 * 防御性判定:公告在 now 时刻是否"窗口内可展示"。
 * - published_at 晚于 now(尚未发布)→ 不展示;
 * - expires_at 早于/等于 now(已过期)→ 不展示;
 * - published_at 空串(无效/零值)按"已发布"放行(后端 ListActive 已过滤,这里只兜底);
 * - expires_at 空/缺省 → 不过期。
 * 判别核心:未来发布与已过期两个边界都必须排除。
 */
export function isAnnouncementVisible(a: UserAnnouncement, now: number): boolean {
  const pub = parseTimeMs(a.published_at)
  if (pub !== null && pub > now) return false
  const exp = parseTimeMs(a.expires_at)
  if (exp !== null && exp <= now) return false
  return true
}

/**
 * 从响应里挑出可展示的公告:过滤窗口外的,再按 severity 权重 + 发布时间排序。
 * - 权重 critical(3) > warning(2) > 其它(1),最危急的排最前;
 * - 同权重按 published_at 降序(新的在前,published_at 无效视为最旧)。
 * 判别核心:critical 必须排在 warning/info 之前(而非按时间一刀切)。
 */
export function visibleAnnouncements(resp: UserAnnouncementListResponse | null, now: number): UserAnnouncement[] {
  const items = resp?.items ?? []
  const visible = items.filter((a) => isAnnouncementVisible(a, now))
  const weight = (s: AnnouncementSeverity): number => (s === 'critical' ? 3 : s === 'warning' ? 2 : 1)
  return [...visible].sort((a, b) => {
    const dw = weight(b.severity) - weight(a.severity)
    if (dw !== 0) return dw
    const pa = parseTimeMs(a.published_at) ?? 0
    const pb = parseTimeMs(b.published_at) ?? 0
    return pb - pa
  })
}

/** 横幅配色语气:critical=danger、warning=warn、其余=info。供组件选 token。 */
export type BannerTone = 'danger' | 'warn' | 'info'

export function bannerTone(severity: AnnouncementSeverity): BannerTone {
  if (severity === 'critical') return 'danger'
  if (severity === 'warning') return 'warn'
  return 'info'
}

// ───────────────────────── 关闭(本地记忆) ─────────────────────────

const DISMISS_KEY = 'hk.overview.announcements.dismissed'

/** 从 localStorage 读已关闭的公告 id 集合;解析失败回退空集(不抛错,降级为全部展示)。 */
export function readDismissed(storage: Pick<Storage, 'getItem'> | null | undefined): Set<number> {
  if (!storage) return new Set()
  try {
    const raw = storage.getItem(DISMISS_KEY)
    if (!raw) return new Set()
    const arr = JSON.parse(raw)
    if (!Array.isArray(arr)) return new Set()
    return new Set(arr.filter((x): x is number => typeof x === 'number' && Number.isFinite(x)))
  } catch {
    return new Set()
  }
}

/**
 * 把一个 id 并入已关闭集合并回写 localStorage,返回新集合(不可变,便于 setState)。
 * 写入异常(隐私模式/配额满)静默吞掉,只返回内存里的新集合,UI 关闭仍即时生效。
 * 判别核心:返回的集合必须包含新关闭的 id(原集合不被原地修改)。
 */
export function persistDismissed(
  storage: Pick<Storage, 'getItem' | 'setItem'> | null | undefined,
  current: Set<number>,
  id: number,
): Set<number> {
  const next = new Set(current)
  next.add(id)
  if (storage) {
    try {
      storage.setItem(DISMISS_KEY, JSON.stringify([...next]))
    } catch {
      /* 持久化失败不影响内存态关闭 */
    }
  }
  return next
}

/** 从已排序的可展示公告里剔除已关闭的;判别核心:dismissed 内的 id 必须被滤掉。 */
export function filterDismissed(items: UserAnnouncement[], dismissed: Set<number>): UserAnnouncement[] {
  return items.filter((a) => !dismissed.has(a.id))
}
