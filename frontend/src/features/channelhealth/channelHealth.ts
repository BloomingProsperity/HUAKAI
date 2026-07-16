import type { BadgeTone } from '../../ui/StatusBadge'
import type {
  ChannelHealthItem,
  ChannelHealthOverrideRequest,
  OverrideAction,
} from './types'

/*
 * 渠道健康台纯逻辑(可单测,无 DOM/网络副作用):
 *   - 列表 query 构造(tenant_id 必带,limit/offset 透传)
 *   - 健康态 → 徽章语气 + 中文标签
 *   - 信号类别 / 置信层级的中文化
 *   - 从列表项构造写动作请求体(镜像后端 ChannelKey.Validate 约束,types.go:84)
 *   - 写动作的前端可用性判定(坐标齐才允许)
 * 全部为同步纯函数,便于变异测试打红。
 */

export type QueryValue = string | number | undefined

/** 默认/最大列表分页,镜像后端 defaultChannelHealthListLimit/maxChannelHealthListLimit(handler:18-19)。 */
export const DEFAULT_LIMIT = 50
export const MAX_LIMIT = 200

/**
 * 构造列表/汇总 query。tenant_id 必带(后端 parsePositiveQueryInt 要求正整数,否则 400);
 * limit/offset 仅在传入时下发(汇总端点不需要分页时省略)。
 */
export function buildListQuery(
  tenantId: number,
  limit?: number,
  offset?: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = { tenant_id: tenantId }
  if (limit !== undefined) q.limit = limit
  if (offset !== undefined) q.offset = offset
  return q
}

/** 详情/汇总 query:只需 tenant_id。 */
export function buildTenantQuery(tenantId: number): Record<string, QueryValue> {
  return { tenant_id: tenantId }
}

/**
 * 健康态 → 徽章语气:
 *   active=健康(ok);ramping=恢复爬坡中(info);degraded/cooling_down=受损/冷却(warn);
 *   disabled/manual_paused=不可用(danger);未知=中性(muted)。
 * 判别核心:cooling_down 必须 warn(受损但会自愈),manual_paused 必须 danger(人工停掉,不自愈),
 * 二者不可同级——否则运维分不清「自动冷却」与「人工封停」。
 */
export function stateTone(state: string): BadgeTone {
  switch (state) {
    case 'active':
      return 'ok'
    case 'ramping':
      return 'info'
    case 'degraded':
    case 'cooling_down':
      return 'warn'
    case 'disabled':
    case 'manual_paused':
      return 'danger'
    default:
      return 'muted'
  }
}

/** 健康态 → 中文标签。 */
export function stateLabel(state: string): string {
  switch (state) {
    case 'active':
      return '健康'
    case 'degraded':
      return '受损'
    case 'cooling_down':
      return '冷却中'
    case 'ramping':
      return '恢复爬坡中'
    case 'disabled':
      return '已禁用'
    case 'manual_paused':
      return '人工暂停'
    default:
      return state || '—'
  }
}

/** 置信层级中文化(ConfidenceTier,types.go:50)。 */
export function confidenceLabel(tier: string): string {
  switch (tier) {
    case 'observed':
      return '已观测'
    case 'inferred':
      return '推断'
    case 'operator_override':
      return '人工干预'
    default:
      return tier || '—'
  }
}

/** 信号类别中文化(SignalClass,types.go:27;只挑常见几类,其余原样回退)。 */
export function signalLabel(signal: string): string {
  switch (signal) {
    case 'none':
      return '无'
    case 'success':
      return '成功'
    case 'channel_error':
      return '渠道错误'
    case 'rate_limit':
      return '限流'
    case 'timeout':
      return '超时'
    case 'forbidden':
      return '被拒'
    case 'upstream_5xx':
      return '上游 5xx'
    case 'local_gateway_5xx':
      return '本地网关 5xx'
    case 'latency_p99':
      return 'P99 延迟超标'
    case 'account_suspended':
      return '账号被封'
    case 'token_revoked':
      return 'Token 撤销'
    case 'credential_revoked':
      return '凭证撤销'
    case 'account_disabled':
      return '账号停用'
    case 'subscription_or_workspace_disabled':
      return '订阅/工作区停用'
    case 'policy_auto_disabled':
      return '策略自动禁用'
    case 'manual_override':
      return '人工干预'
    default:
      return signal || '—'
  }
}

/** 审计事件类型中文化(AuditEventType,types.go:57)。 */
export function eventLabel(eventType: string): string {
  switch (eventType) {
    case 'channel_health_degraded':
      return '渠道受损'
    case 'channel_disabled':
      return '渠道禁用'
    case 'channel_recovered':
      return '渠道恢复'
    case 'channel_ramp_started':
      return '开始爬坡恢复'
    case 'channel_ramp_rolled_back':
      return '爬坡回滚'
    case 'channel_manual_override':
      return '人工干预'
    default:
      return eventType || '—'
  }
}

/** 写动作 → 中文标签。 */
export function actionLabel(action: OverrideAction): string {
  switch (action) {
    case 'pause':
      return '人工暂停'
    case 'resume':
      return '恢复'
    case 'force-active':
      return '强制置健康'
  }
}

/**
 * 写动作是否为高影响(需二次确认):
 *   pause=人工封停该渠道、force-active=绕过自动冷却强制上线,均高影响必须二次确认;
 *   resume=解除人工暂停回到自动判定,影响相对温和。
 * 判别核心:force-active 必须为 true(绕过保护机制),否则运维会误以为是安全操作。
 */
export function actionNeedsConfirm(action: OverrideAction): boolean {
  return action === 'pause' || action === 'force-active'
}

/**
 * 写动作请求体可用性判定:列表项是否含齐 ChannelKey.Validate 所需坐标。
 * 镜像后端约束(types.go:84):tenant_id>0、vendor 非空、account_credential_id>0、credential_version>0;
 * provider_account_id>0(URL {id},parseAdminPoolID 要求正)。任一不满足即不可写。
 * 判别核心:credential_version<=0 必须判不可用(后端会 400 invalid_channel_health_subject)。
 */
export function canOverride(item: Pick<
  ChannelHealthItem,
  'tenant_id' | 'vendor' | 'account_credential_id' | 'credential_version' | 'provider_account_id'
>): boolean {
  return (
    item.tenant_id > 0 &&
    item.vendor.trim() !== '' &&
    item.account_credential_id > 0 &&
    item.credential_version > 0 &&
    item.provider_account_id > 0
  )
}

/** 从列表项构造写动作请求体的结果:ok 时带请求体,否则带中文错误。 */
export type OverrideBuild =
  | { ok: true; providerAccountId: number; body: ChannelHealthOverrideRequest }
  | { ok: false; error: string }

/**
 * 从列表项 + 原因构造写动作请求体。坐标取自列表项(后端列表已返回完整坐标),
 * reason 经 trim 后必须非空(镜像 handler:186 reason_required)。坐标不齐则拒。
 */
export function buildOverride(
  item: Pick<
    ChannelHealthItem,
    'tenant_id' | 'vendor' | 'account_credential_id' | 'credential_version' | 'provider_account_id'
  >,
  reason: string,
): OverrideBuild {
  if (!canOverride(item)) {
    return { ok: false, error: '该渠道坐标不完整,无法执行人工干预' }
  }
  const r = reason.trim()
  // 判别核心:空 reason 必须拒(后端 reason_required 400);前端先拦免无谓往返。
  if (r === '') {
    return { ok: false, error: '请填写操作原因(供审计)' }
  }
  return {
    ok: true,
    providerAccountId: item.provider_account_id,
    body: {
      tenant_id: item.tenant_id,
      // vendor 后端会 ToLower + trim(handler:173),前端同步归一,保证回显一致。
      vendor: item.vendor.trim().toLowerCase(),
      account_credential_id: item.account_credential_id,
      credential_version: item.credential_version,
      reason: r,
    },
  }
}

/** 分页校验:limit ∈ [1,MAX_LIMIT](镜像 parseChannelHealthPagination,handler:220)。 */
export function clampLimit(limit: number): number {
  if (!Number.isInteger(limit) || limit <= 0) return DEFAULT_LIMIT
  return Math.min(limit, MAX_LIMIT)
}

export interface ChannelHealthTableRow {
  key: string
  channelId: string
  coordinates: string
  vendor: string
  state: string
  stateTone: BadgeTone
  score: string
  signal: string
  confidence: string
  recovery: string
  recoveryDetail: string | null
  updatedAt: string
  writable: boolean
  item: ChannelHealthItem
}

/** 渠道健康 DTO 到运维列表展示行的纯映射。 */
export function mapChannelHealthRows(items: ChannelHealthItem[]): ChannelHealthTableRow[] {
  return items.map((item) => ({
    key: `${item.provider_account_id}:${item.credential_version}:${item.account_credential_id}`,
    channelId: item.channel_id,
    coordinates: `acct #${item.provider_account_id} · cred #${item.account_credential_id} v${item.credential_version}`,
    vendor: item.vendor || '—',
    state: stateLabel(item.state),
    stateTone: stateTone(item.state),
    score: formatHealthNumber(item.score),
    signal: signalLabel(item.reason_class),
    confidence: confidenceLabel(item.confidence_tier),
    recovery: item.cooldown_until ? `冷却至 ${formatHealthTimestamp(item.cooldown_until)}` : '—',
    recoveryDetail:
      item.state === 'ramping' || item.ramp_stage_pct > 0
        ? `爬坡 ${item.ramp_stage_pct}% · 失败 ${item.ramp_failure_count}`
        : null,
    updatedAt: formatHealthTimestamp(item.updated_at),
    writable: canOverride(item),
    item,
  }))
}

export function formatHealthTimestamp(iso?: string): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? iso : date.toLocaleString('zh-CN', { hour12: false })
}

export function formatHealthNumber(value: number): string {
  if (typeof value !== 'number' || Number.isNaN(value)) return '—'
  return String(Math.round(value * 100) / 100)
}
