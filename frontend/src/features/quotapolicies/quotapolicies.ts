import type { BadgeTone } from '../../ui/StatusBadge'
import type {
  Metric,
  Mode,
  PolicyFilters,
  PolicyForm,
  QuotaPolicy,
  QuotaPolicyRequest,
  ScopeKind,
  WindowKind,
} from './types'

/*
 * 配额策略页纯逻辑(可单测,无 DOM/网络副作用):
 *   - 列表 query 构造(tenant_id>0 才下发、空筛选项省略、enabled 三态)
 *   - 表单前端校验(镜像后端 validateRequest 约束,validate.go:24):
 *       枚举白名单 / scope_id 非空且 ≤255 / 数值非负十进制 / fixed 窗口须 window_seconds>0 /
 *       valid_until 须严格晚于 valid_from
 *   - 枚举 → 中文标签;mode → 徽章语气
 *   - limit_value/burst_value 十进制字符串原样处理(绝不 Number() 化,防精度丢失)
 * 全部为同步纯函数,便于 §14 变异测试打红。
 */

export type QueryValue = string | number | undefined

// ── 枚举集合(镜像后端白名单,validate.go:21-33)────────────────────────────────
export const SCOPE_KINDS: ScopeKind[] = [
  'global',
  'user',
  'api_key',
  'channel',
  'pool_group',
  'provider_account',
]
export const METRICS: Metric[] = ['requests', 'tokens_estimated', 'cost_usd', 'concurrency']
export const WINDOW_KINDS: WindowKind[] = [
  'none',
  'fixed',
  'calendar_day',
  'calendar_week',
  'calendar_month',
  'manual',
]
export const MODES: Mode[] = ['enforce', 'observe', 'manual_first', 'disabled']

/** scope_id 长度上限,镜像后端 maxScopeIDLen(validate.go:37)。 */
export const MAX_SCOPE_ID_LEN = 255

const SCOPE_KIND_SET = new Set<string>(SCOPE_KINDS)
const METRIC_SET = new Set<string>(METRICS)
const WINDOW_KIND_SET = new Set<string>(WINDOW_KINDS)
const MODE_SET = new Set<string>(MODES)

/**
 * 构造列表 query。
 *   - tenant_id 仅在 >0 时下发(=0 表示 operator 用自身作用域,后端 routes.go:122)
 *   - scope_kind / metric 空串省略(后端 filterValue 空串=不过滤,validate.go:251)
 *   - enabled 仅 'true'/'false' 下发,'' 省略(后端 enabledFilter,validate.go:262)
 *   - limit / offset 直接透传(调用方保证范围)
 */
export function buildListQuery(
  tenantId: number,
  filters: PolicyFilters,
  limit: number,
  offset: number,
): Record<string, QueryValue> {
  const q: Record<string, QueryValue> = { limit, offset }
  // 判别核心:tenant_id 必须 >0 才进 query;<=0 一律省略(避免给后端发 tenant_id=0 被判 invalid)。
  if (tenantId > 0) q.tenant_id = tenantId
  if (filters.scopeKind !== '') q.scope_kind = filters.scopeKind
  if (filters.metric !== '') q.metric = filters.metric
  if (filters.enabled !== '') q.enabled = filters.enabled
  return q
}

// ── 中文标签 ─────────────────────────────────────────────────────────────────
export function scopeKindLabel(v: string): string {
  switch (v) {
    case 'global':
      return '全局'
    case 'user':
      return '用户'
    case 'api_key':
      return 'API Key'
    case 'channel':
      return '渠道'
    case 'pool_group':
      return '池分组'
    case 'provider_account':
      return '上游账号'
    default:
      return v || '—'
  }
}

export function metricLabel(v: string): string {
  switch (v) {
    case 'requests':
      return '请求数'
    case 'tokens_estimated':
      return '预估 Token'
    case 'cost_usd':
      return '成本(USD)'
    case 'concurrency':
      return '并发数'
    default:
      return v || '—'
  }
}

export function windowKindLabel(v: string): string {
  switch (v) {
    case 'none':
      return '无窗口'
    case 'fixed':
      return '固定窗口'
    case 'calendar_day':
      return '自然日'
    case 'calendar_week':
      return '自然周'
    case 'calendar_month':
      return '自然月'
    case 'manual':
      return '手动重置'
    default:
      return v || '—'
  }
}

export function modeLabel(v: string): string {
  switch (v) {
    case 'enforce':
      return '强制拦截'
    case 'observe':
      return '仅观测'
    case 'manual_first':
      return '先人工'
    case 'disabled':
      return '已停用'
    default:
      return v || '—'
  }
}

/**
 * mode → 徽章语气:
 *   enforce=强制拦截(danger,真会拦);observe=仅观测(info,不拦只记);
 *   manual_first=先人工(warn);disabled=已停用(muted)。
 */
export function modeTone(mode: string): BadgeTone {
  switch (mode) {
    case 'enforce':
      return 'danger'
    case 'observe':
      return 'info'
    case 'manual_first':
      return 'warn'
    case 'disabled':
      return 'muted'
    default:
      return 'muted'
  }
}

export interface QuotaPolicyTableRow {
  id: number
  scope: string
  scopeId: string
  metric: string
  window: string
  limit: string
  burst: string
  mode: string
  modeTone: BadgeTone
  priority: number
  status: string
  statusTone: BadgeTone
  policy: QuotaPolicy
}

/** 仅派生配额策略展示字段，十进制字符串与原策略对象均保持原语义。 */
export function mapQuotaPolicyRows(policies: QuotaPolicy[]): QuotaPolicyTableRow[] {
  return policies.map((policy) => ({
    id: policy.id,
    scope: scopeKindLabel(policy.scope_kind),
    scopeId: policy.scope_id,
    metric: metricLabel(policy.metric),
    window: `${windowKindLabel(policy.window_kind)}${policy.window_kind === 'fixed' && policy.window_seconds > 0 ? ` · ${policy.window_seconds}s` : ''}`,
    limit: formatDecimal(policy.limit_value),
    burst: formatDecimal(policy.burst_value),
    mode: modeLabel(policy.mode),
    modeTone: modeTone(policy.mode),
    priority: policy.priority,
    status: policy.enabled ? '启用' : '停用',
    statusTone: policy.enabled ? 'ok' : 'muted',
    policy,
  }))
}

/** mode='enforce' 是真会拦请求的高影响模式,新建/编辑保存须二次确认。 */
export function isEnforce(mode: string): boolean {
  return mode === 'enforce'
}

// ── 十进制字符串处理(原样,防精度丢失)──────────────────────────────────────────

/** 非负十进制字符串校验(镜像后端 parseNonNegativeDecimal,validate.go:280):整数或带小数,无负号。 */
const DECIMAL_RE = /^[0-9]+(\.[0-9]+)?$/

/**
 * 展示十进制:裁掉小数部分无意义的尾随 0(纯整数不留小数点);非法/空值原样返回。
 * 仅用于展示,绝不改变回传值(回传始终用原始字符串)。
 */
export function formatDecimal(raw: string): string {
  const v = raw.trim()
  if (!DECIMAL_RE.test(v)) return raw
  if (!v.includes('.')) return v
  const trimmed = v.replace(/0+$/, '').replace(/\.$/, '')
  return trimmed === '' ? '0' : trimmed
}

// ── 表单校验(镜像后端 validateRequest,validate.go:24)──────────────────────────
export type FormValidation =
  | { ok: true; value: QuotaPolicyRequest }
  | { ok: false; error: string }

/**
 * 校验表单并产出请求体(镜像后端 validateRequest 的并集约束,validate.go:24):
 *   - scope_kind / metric / window_kind / mode 必须在白名单内
 *   - scope_id trim 后非空且 ≤255('*' 表示 global)
 *   - limit_value 必填、非负十进制;burst_value 可空(空=0)、非负十进制
 *   - window_seconds 非负整数;window_kind=fixed 时必须 >0
 *   - priority 整数
 *   - valid_from/valid_until 空或 RFC3339;valid_until 须严格晚于 valid_from
 * 前端先拦,避免无谓 400;后端仍是权威。
 */
export function validatePolicyForm(form: PolicyForm): FormValidation {
  if (!SCOPE_KIND_SET.has(form.scopeKind)) {
    return { ok: false, error: '作用域类型(scope_kind)非法' }
  }
  const scopeId = form.scopeId.trim()
  if (scopeId === '') {
    return { ok: false, error: '作用域 ID(scope_id)必填(全局用 *)' }
  }
  // 判别核心:scope_id 必须 ≤255(镜像后端 maxScopeIDLen);超长必须拒。
  if (scopeId.length > MAX_SCOPE_ID_LEN) {
    return { ok: false, error: `作用域 ID 长度须 ≤ ${MAX_SCOPE_ID_LEN}` }
  }
  if (!METRIC_SET.has(form.metric)) {
    return { ok: false, error: '指标(metric)非法' }
  }
  if (!WINDOW_KIND_SET.has(form.windowKind)) {
    return { ok: false, error: '窗口类型(window_kind)非法' }
  }
  if (!MODE_SET.has(form.mode)) {
    return { ok: false, error: '模式(mode)非法' }
  }

  // window_seconds:空串=0;否则须非负整数。
  const wsRaw = form.windowSeconds.trim()
  let windowSeconds = 0
  if (wsRaw !== '') {
    if (!/^[0-9]+$/.test(wsRaw)) {
      return { ok: false, error: '窗口秒数(window_seconds)须为非负整数' }
    }
    windowSeconds = Number(wsRaw)
  }
  // 判别核心:window_kind=fixed 时 window_seconds 必须 >0(镜像后端 validate.go:75)。
  if (form.windowKind === 'fixed' && windowSeconds <= 0) {
    return { ok: false, error: '固定窗口必须指定窗口秒数(>0)' }
  }

  // limit_value 必填、非负十进制(字符串原样保留)。
  const limitRaw = form.limitValue.trim()
  if (limitRaw === '') {
    return { ok: false, error: '上限(limit_value)必填' }
  }
  if (!DECIMAL_RE.test(limitRaw)) {
    return { ok: false, error: '上限(limit_value)须为非负的十进制数' }
  }

  // burst_value 可空(空=0),非负十进制。
  const burstRaw = form.burstValue.trim()
  if (burstRaw !== '' && !DECIMAL_RE.test(burstRaw)) {
    return { ok: false, error: '突发上限(burst_value)须为非负的十进制数' }
  }

  // priority:空=后端默认 100;否则须整数(可负,后端只要求 int32)。
  const prioRaw = form.priority.trim()
  let priority: number | undefined
  if (prioRaw !== '') {
    if (!/^-?[0-9]+$/.test(prioRaw)) {
      return { ok: false, error: '优先级(priority)须为整数' }
    }
    priority = Number(prioRaw)
  }

  // valid_from / valid_until:空或 RFC3339;valid_until 须严格晚于 valid_from。
  const fromRaw = form.validFrom.trim()
  const untilRaw = form.validUntil.trim()
  if (fromRaw !== '' && !isRFC3339(fromRaw)) {
    return { ok: false, error: '生效时间(valid_from)须为 RFC3339' }
  }
  if (untilRaw !== '') {
    if (!isRFC3339(untilRaw)) {
      return { ok: false, error: '失效时间(valid_until)须为 RFC3339' }
    }
    // valid_from 空时后端按 now() 作为下界;前端只在两者都给出时本地校验严格大小,
    // 否则交后端权威判定(避免本地时钟与后端 now() 不一致误拦)。
    if (fromRaw !== '') {
      const from = Date.parse(fromRaw)
      const until = Date.parse(untilRaw)
      // 判别核心:valid_until 必须严格晚于 valid_from(镜像后端 !until.After(validFrom),validate.go:137)。
      if (!(until > from)) {
        return { ok: false, error: '失效时间必须晚于生效时间' }
      }
    }
  }

  const value: QuotaPolicyRequest = {
    scope_kind: form.scopeKind,
    scope_id: scopeId,
    metric: form.metric,
    window_kind: form.windowKind,
    window_seconds: windowSeconds,
    limit_value: limitRaw,
    mode: form.mode,
    enabled: form.enabled,
  }
  // 仅在用户填了的可选字段才进请求体(其余交后端套默认),与后端指针字段语义一致。
  if (burstRaw !== '') value.burst_value = burstRaw
  if (priority !== undefined) value.priority = priority
  if (fromRaw !== '') value.valid_from = fromRaw
  if (untilRaw !== '') value.valid_until = untilRaw
  const reason = form.reason.trim()
  if (reason !== '') value.reason = reason
  return { ok: true, value }
}

/**
 * RFC3339 时间串的轻量校验:必须可被 Date.parse 解析,且含 'T' 与时区/Z 标记。
 * 仅做粗筛(后端 time.Parse(RFC3339) 是权威),目的是挡掉明显非法输入。
 */
export function isRFC3339(s: string): boolean {
  if (!/T/.test(s)) return false
  // 须带时区:Z 或 ±HH:MM。
  if (!/(Z|[+-][0-9]{2}:[0-9]{2})$/.test(s)) return false
  return !Number.isNaN(Date.parse(s))
}

/** 把后端策略 DTO 拍平成表单初值(供编辑时 useState 初始化);十进制字段经 formatDecimal 仅做展示裁尾。 */
export function policyToForm(p: QuotaPolicy): PolicyForm {
  return {
    scopeKind: (SCOPE_KIND_SET.has(p.scope_kind) ? p.scope_kind : 'global') as ScopeKind,
    scopeId: p.scope_id,
    metric: (METRIC_SET.has(p.metric) ? p.metric : 'requests') as Metric,
    windowKind: (WINDOW_KIND_SET.has(p.window_kind) ? p.window_kind : 'fixed') as WindowKind,
    windowSeconds: String(p.window_seconds),
    limitValue: formatDecimal(p.limit_value),
    burstValue: formatDecimal(p.burst_value),
    mode: (MODE_SET.has(p.mode) ? p.mode : 'enforce') as Mode,
    priority: String(p.priority),
    enabled: p.enabled,
    validFrom: p.valid_from ?? '',
    validUntil: p.valid_until ?? '',
    reason: '',
  }
}

/** 新建表单的缺省初值(贴近后端默认:fixed 窗口、enforce 模式、priority 100)。 */
export function emptyPolicyForm(): PolicyForm {
  return {
    scopeKind: 'user',
    scopeId: '',
    metric: 'requests',
    windowKind: 'fixed',
    windowSeconds: '3600',
    limitValue: '',
    burstValue: '',
    mode: 'enforce',
    priority: '100',
    enabled: true,
    validFrom: '',
    validUntil: '',
    reason: '',
  }
}
