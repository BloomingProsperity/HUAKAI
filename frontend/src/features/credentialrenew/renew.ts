import type { BadgeTone } from '../../ui/StatusBadge'
import type { RenewStatusQueryParams, RenewStatusRow } from './types'

/*
 * 凭证续期监控页纯逻辑(可单测,无 DOM/网络副作用):
 *   - 列表 query 构造(limit 钳到后端 [1,500];tenant_id 仅正整数下发;cursor 非空才下发)
 *   - 续期健康度判定 → 徽章语气 + 中文标签(综合 state / 失败计数 / 临期窗口)
 *   - 到期/续期窗口的相对时间展示(临期高亮的判别基础)
 * 全部为同步纯函数,便于变异测试打红。
 */

export type QueryValue = string | number | undefined

/** 后端续期列表的分页约束(镜像 credentialstore.DefaultRenewStatusLimit / MaxRenewStatusLimit)。 */
export const DEFAULT_RENEW_LIMIT = 100
export const MAX_RENEW_LIMIT = 500
export const MIN_RENEW_LIMIT = 1

/**
 * 把请求的 limit 钳到后端可接受区间 [1,500]。
 * 判别核心:0/负数 → 抬到 1;>500 → 压回 500;非整数 → 向下取整后再钳;NaN/缺省 → 默认 100。
 * (后端 parseCredentialRenewStatusLimit:limit<=0 或 >500 直接 400,故前端先钳避免无谓报错。)
 */
export function clampRenewLimit(limit: number | undefined): number {
  if (limit === undefined || !Number.isFinite(limit)) return DEFAULT_RENEW_LIMIT
  const n = Math.floor(limit)
  if (n < MIN_RENEW_LIMIT) return MIN_RENEW_LIMIT
  if (n > MAX_RENEW_LIMIT) return MAX_RENEW_LIMIT
  return n
}

/**
 * 构造续期列表 query。
 *   - limit:始终下发,先经 clampRenewLimit 钳进 [1,500]。
 *   - tenant_id:仅当为正整数时下发(后端 tenant_id<=0 即 tenant_id_invalid 400);非正/缺省一律省略。
 *   - cursor:仅当为非空串时下发(空串/缺省省略,代表取首页)。
 */
export function buildRenewStatusQuery(params: RenewStatusQueryParams): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = {
    limit: clampRenewLimit(params.limit),
  }
  // 判别核心:只有正整数 tenant_id 才下发,否则省略(避免给后端发 tenant_id=0/负)。
  if (
    params.tenantId !== undefined &&
    Number.isInteger(params.tenantId) &&
    params.tenantId > 0
  ) {
    q.tenant_id = params.tenantId
  }
  // 判别核心:cursor 必须非空才下发;空串会被后端当首页但更易混淆,故前端直接省略。
  const cursor = (params.cursor ?? '').trim()
  if (cursor !== '') {
    q.cursor = cursor
  }
  return q
}

/** 续期健康度等级:从最紧迫到最健康。 */
export type RenewHealth = 'failing' | 'disabled' | 'expired' | 'due' | 'soon' | 'healthy' | 'na'

/** 「即将到期」窗口:距离 access token 过期 ≤ 此毫秒数即视为 soon(高亮提醒)。默认 24h。 */
export const SOON_WINDOW_MS = 24 * 60 * 60 * 1000

/**
 * 判定单条凭证的续期健康度。优先级(从高到低):
 *   1. failure_count>0 或 failure_class 非空 → failing(续期在报错,最需介入)
 *   2. state 非 active(如 disabled/error)→ disabled(账号侧已停用/异常)
 *   3. access_expires_at 已过 now → expired(已过期,续期已失败或未触发)
 *   4. 已进入 refresh_before_at 窗口(now>=refresh_before_at)→ due(到了应续期点)
 *   5. 距 access_expires_at ≤ SOON_WINDOW_MS → soon(即将到期)
 *   6. 有过期时刻且尚远 → healthy
 *   7. 完全无过期/续期窗口信息(如 api_key 长期凭证)→ na(不适用)
 *
 * 判别核心:failing 必须压过其它一切状态(哪怕未过期);na 仅在既无过期时刻也无续期窗口时出现。
 */
export function renewHealth(row: RenewStatusRow, nowMs: number): RenewHealth {
  if (row.failure_count > 0 || (row.failure_class ?? '') !== '') {
    return 'failing'
  }
  if (normalizeState(row.state) !== 'active') {
    return 'disabled'
  }
  const expMs = parseTime(row.access_expires_at)
  const refreshMs = parseTime(row.refresh_before_at)
  if (expMs !== null && expMs <= nowMs) {
    return 'expired'
  }
  if (refreshMs !== null && nowMs >= refreshMs) {
    return 'due'
  }
  if (expMs !== null && expMs - nowMs <= SOON_WINDOW_MS) {
    return 'soon'
  }
  if (expMs !== null) {
    return 'healthy'
  }
  return 'na'
}

/** 续期健康度 → 徽章语气。 */
export function renewHealthTone(h: RenewHealth): BadgeTone {
  switch (h) {
    case 'healthy':
      return 'ok'
    case 'soon':
    case 'due':
      return 'warn'
    case 'failing':
    case 'expired':
    case 'disabled':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 续期健康度 → 中文标签。 */
export function renewHealthLabel(h: RenewHealth): string {
  switch (h) {
    case 'healthy':
      return '健康'
    case 'soon':
      return '即将到期'
    case 'due':
      return '待续期'
    case 'expired':
      return '已过期'
    case 'failing':
      return '续期失败'
    case 'disabled':
      return '已停用'
    default:
      return '不适用'
  }
}

/** 把后端 state 文本归一(trim+小写),供判定使用。空串视为 active 的相反——保持原样空串。 */
export function normalizeState(state: string): string {
  return (state ?? '').trim().toLowerCase()
}

/**
 * 解析 ISO 时间为毫秒;null/空串/非法 → null(不参与时间判定)。
 * 判别核心:非法时间不能当成 0(否则会被误判成「已过期」),必须返回 null。
 */
export function parseTime(iso: string | null): number | null {
  if (iso === null) return null
  const s = iso.trim()
  if (s === '') return null
  const t = Date.parse(s)
  return Number.isNaN(t) ? null : t
}

/**
 * 把目标时刻渲染成相对 now 的中文描述(供「距到期」「上次刷新」列)。
 *   - null → 「—」
 *   - 未来:「X 后」;过去:「X 前」
 * 判别核心:同样的间隔在未来/过去要给出方向相反的文案(不能都说「前」)。
 */
export function relativeTime(iso: string | null, nowMs: number): string {
  const t = parseTime(iso)
  if (t === null) return '—'
  const diff = t - nowMs
  const abs = Math.abs(diff)
  const text = humanizeDuration(abs)
  if (diff === 0) return '刚刚'
  return diff > 0 ? `${text}后` : `${text}前`
}

/** 把毫秒间隔压成「X天/X小时/X分钟/X秒」的最粗粒度中文。 */
export function humanizeDuration(ms: number): string {
  const sec = Math.floor(ms / 1000)
  if (sec < 60) return `${sec} 秒`
  const min = Math.floor(sec / 60)
  if (min < 60) return `${min} 分钟`
  const hr = Math.floor(min / 60)
  if (hr < 24) return `${hr} 小时`
  const day = Math.floor(hr / 24)
  return `${day} 天`
}

/** 失败摘要:把 failure_class + failure_count 拼成一行中文(无失败 → 「—」)。 */
export function failureSummary(row: RenewStatusRow): string {
  if (row.failure_count <= 0 && (row.failure_class ?? '') === '') return '—'
  const cls = (row.failure_class ?? '').trim() || '未知'
  return `${cls} ×${Math.max(row.failure_count, 0)}`
}

export interface RenewTableRow {
  id: number
  health: RenewHealth
  healthLabel: string
  healthTone: BadgeTone
  tenantName: string
  tenantID: string
  accountName: string
  accountID: string
  vendor: string
  authMode: string
  version: string
  expiresIn: string
  renewWindow: string
  lastRefresh: string
  failure: string
  failureTone: 'danger' | 'muted'
}

/** 把续期状态映射为表格展示模型，凭证值不会进入展示层。 */
export function mapRenewTableRows(rows: RenewStatusRow[], nowMs: number): RenewTableRow[] {
  return rows.map((row) => {
    const health = renewHealth(row, nowMs)
    return {
      id: row.id,
      health,
      healthLabel: renewHealthLabel(health),
      healthTone: renewHealthTone(health),
      tenantName: row.tenant_name || '—',
      tenantID: `#${row.tenant_id}`,
      accountName: row.account_name || '—',
      accountID: `#${row.account_id}`,
      vendor: row.vendor || '—',
      authMode: row.auth_mode || '—',
      version: `v${row.credential_version}`,
      expiresIn: relativeTime(row.access_expires_at, nowMs),
      renewWindow: relativeTime(row.refresh_before_at, nowMs),
      lastRefresh: relativeTime(row.last_refresh_at, nowMs),
      failure: failureSummary(row),
      failureTone: health === 'failing' ? 'danger' : 'muted',
    }
  })
}
