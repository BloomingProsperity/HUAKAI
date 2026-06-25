import type {
  Comparator,
  CreateRuleRequest,
  CreateSilenceRequest,
  EventState,
  Severity,
  UpdateRuleRequest,
} from './types'

/*
 * Ops 告警控制台的纯逻辑(可单测):枚举/标签/语气、规则表单构造与校验、filters 解析、
 * 静默表单构造与校验、事件状态展示。校验严格镜像后端,客户端先挡住给清晰中文提示避免无谓 400:
 *   validateRule(service.go:392):name 非空、metric|metric_type 至少一个非空、comparator 限四值、
 *     threshold 有限数、severity 限三值、window_seconds>0、sustained/cooldown>=0。
 *   validateSilence(service.go:418):rule_id 若有须>0、starts/ends 非零且 ends 严格晚于 starts。
 */

/** 默认租户。单租户部署(运维者自跑实例)恒为 1;与 announcements/emailVerify 兜底一致。 */
export const DEFAULT_TENANT_ID = 1

// ── 比较符 ──────────────────────────────────────────────
export const COMPARATORS: ReadonlyArray<{ value: Comparator; label: string; symbol: string }> = [
  { value: 'gt', label: '大于', symbol: '>' },
  { value: 'gte', label: '大于等于', symbol: '≥' },
  { value: 'lt', label: '小于', symbol: '<' },
  { value: 'lte', label: '小于等于', symbol: '≤' },
]

export function comparatorSymbol(c: string): string {
  return COMPARATORS.find((x) => x.value === c)?.symbol ?? c
}

export function isComparator(c: string): c is Comparator {
  return COMPARATORS.some((x) => x.value === c)
}

// ── 级别 ────────────────────────────────────────────────
export const SEVERITIES: ReadonlyArray<{ value: Severity; label: string; tone: 'info' | 'warn' | 'danger' }> = [
  { value: 'info', label: '提示', tone: 'info' },
  { value: 'warning', label: '警告', tone: 'warn' },
  { value: 'critical', label: '严重', tone: 'danger' },
]

export function severityLabel(s: string): string {
  return SEVERITIES.find((x) => x.value === s)?.label ?? s
}

export function severityTone(s: string): 'info' | 'warn' | 'danger' | 'muted' {
  return SEVERITIES.find((x) => x.value === s)?.tone ?? 'muted'
}

export function isSeverity(s: string): s is Severity {
  return SEVERITIES.some((x) => x.value === s)
}

// ── 指标类型(runtime evaluator 已识别的内建类型;留空表示自定义 metric)──
export const METRIC_TYPES: ReadonlyArray<{ value: string; label: string }> = [
  { value: '', label: '自定义(用指标名)' },
  { value: 'cpu_usage_percent', label: 'CPU 使用率(%)' },
]

// ── 事件状态 ────────────────────────────────────────────
export const EVENT_STATES: ReadonlyArray<{ value: EventState; label: string; tone: 'danger' | 'ok' | 'muted' }> = [
  { value: 'firing', label: '触发中', tone: 'danger' },
  { value: 'resolved', label: '已恢复', tone: 'ok' },
  { value: 'manual_resolved', label: '手动恢复', tone: 'muted' },
]

export function eventStateLabel(s: string): string {
  return EVENT_STATES.find((x) => x.value === s)?.label ?? s
}

export function eventStateTone(s: string): 'danger' | 'ok' | 'muted' {
  return EVENT_STATES.find((x) => x.value === s)?.tone ?? 'muted'
}

/** 事件是否仍触发中(决定是否显示「手动恢复」操作)。仅 firing 可手动恢复。 */
export function isFiring(state: string): boolean {
  return state === 'firing'
}

// ── filters 解析:每行 "key=value",空行忽略;键重复后者覆盖 ──
/**
 * parseFilters 结果用判别式联合(ok 标志),而非把错误塞进 map。
 * 关键:过滤 map 的键是任意字符串,可能恰好叫 "error";若用 `{error}` 当错误哨兵,
 * `'error' in result` 既无法在 TS 里对索引签名类型收窄,运行时也会把名为 error 的过滤键误判成错误。
 */
export type ParseFiltersResult = { ok: true; filters: Record<string, string> } | { ok: false; error: string }

/**
 * 把多行 "key=value" 文本解析成维度过滤 map。
 * 键/值各 trim;键不可空;值允许含 '='(只按首个 '=' 切分)。空文本 → 空 map(合法)。
 */
export function parseFilters(text: string): ParseFiltersResult {
  const out: Record<string, string> = {}
  for (const rawLine of text.split('\n')) {
    const line = rawLine.trim()
    if (!line) continue
    const eq = line.indexOf('=')
    if (eq < 0) return { ok: false, error: `过滤条件「${line}」缺少 '='(应为 键=值)` }
    const key = line.slice(0, eq).trim()
    const value = line.slice(eq + 1).trim()
    if (!key) return { ok: false, error: `过滤条件「${line}」的键为空` }
    out[key] = value
  }
  return { ok: true, filters: out }
}

/** 把 filters map 反序列化回多行文本(回填编辑表单)。键排序保证稳定。 */
export function filtersToText(filters?: Record<string, string>): string {
  if (!filters) return ''
  return Object.keys(filters)
    .sort()
    .map((k) => `${k}=${filters[k]}`)
    .join('\n')
}

// ── 规则表单 ────────────────────────────────────────────
/** 规则表单态。数值字段用串(受控 input),提交时解析。 */
export interface RuleForm {
  name: string
  metric: string
  metricType: string
  comparator: Comparator
  threshold: string
  severity: Severity
  windowSeconds: string
  sustainedSeconds: string
  cooldownSeconds: string
  notifyEmail: boolean
  enabled: boolean
  filtersText: string
}

export const EMPTY_RULE_FORM: RuleForm = {
  name: '',
  metric: '',
  metricType: '',
  comparator: 'gt',
  threshold: '',
  severity: 'warning',
  windowSeconds: '300',
  sustainedSeconds: '0',
  cooldownSeconds: '0',
  notifyEmail: false,
  enabled: true,
  filtersText: '',
}

interface RuleCore {
  name: string
  metric: string
  metricType: string
  threshold: number
  windowSeconds: number
  sustainedSeconds: number
  cooldownSeconds: number
  filters: Record<string, string>
}

/** 解析非负整数(用于秒数字段)。空串按 fallback;非法/负数 → null。 */
function parseNonNegInt(raw: string, fallback: number): number | null {
  const v = raw.trim()
  if (!v) return fallback
  if (!/^\d+$/.test(v)) return null
  return Number.parseInt(v, 10)
}

/** 共享校验:镜像 validateRule。返回归一化字段或 {error}。 */
function validateRuleCore(form: RuleForm): RuleCore | { error: string } {
  const name = form.name.trim()
  if (!name) return { error: '请填写规则名称' }

  const metric = form.metric.trim()
  const metricType = form.metricType.trim()
  // metricKeyForRule:metric_type 优先,否则用 metric;两者皆空则 metricKey 为空 → 后端 400。
  if (!metric && !metricType) return { error: '请填写指标名,或选择指标类型' }

  if (!isComparator(form.comparator)) return { error: '比较符非法' }
  if (!isSeverity(form.severity)) return { error: '级别非法' }

  const threshold = Number.parseFloat(form.threshold.trim())
  if (!Number.isFinite(threshold)) return { error: '阈值必须是有限数字' }

  const windowSeconds = parseNonNegInt(form.windowSeconds, 0)
  if (windowSeconds === null || windowSeconds <= 0) return { error: '观察窗口(秒)必须为正整数' }

  const sustainedSeconds = parseNonNegInt(form.sustainedSeconds, 0)
  if (sustainedSeconds === null) return { error: '持续时长(秒)必须为非负整数' }
  const cooldownSeconds = parseNonNegInt(form.cooldownSeconds, 0)
  if (cooldownSeconds === null) return { error: '冷却时长(秒)必须为非负整数' }

  const parsed = parseFilters(form.filtersText)
  if (!parsed.ok) return { error: parsed.error }

  return { name, metric, metricType, threshold, windowSeconds, sustainedSeconds, cooldownSeconds, filters: parsed.filters }
}

/** 构造新建规则请求。校验镜像 validateRule;失败返回 {error}。 */
export function buildCreateRule(form: RuleForm, tenantId: number): CreateRuleRequest | { error: string } {
  const core = validateRuleCore(form)
  if ('error' in core) return core
  const req: CreateRuleRequest = {
    tenant_id: tenantId,
    name: core.name,
    metric: core.metric,
    comparator: form.comparator,
    threshold: core.threshold,
    severity: form.severity,
    window_seconds: core.windowSeconds,
    sustained_seconds: core.sustainedSeconds,
    cooldown_seconds: core.cooldownSeconds,
    notify_email: form.notifyEmail,
    enabled: form.enabled,
  }
  if (core.metricType) req.metric_type = core.metricType
  if (Object.keys(core.filters).length > 0) req.filters = core.filters
  return req
}

/** 构造改规则请求(提交表单全量字段)。同校验。 */
export function buildUpdateRule(form: RuleForm): UpdateRuleRequest | { error: string } {
  const core = validateRuleCore(form)
  if ('error' in core) return core
  return {
    name: core.name,
    metric: core.metric,
    // metric_type 总是显式传(空串=清空内建类型,回退自定义 metric)。
    metric_type: core.metricType,
    comparator: form.comparator,
    threshold: core.threshold,
    severity: form.severity,
    window_seconds: core.windowSeconds,
    sustained_seconds: core.sustainedSeconds,
    cooldown_seconds: core.cooldownSeconds,
    notify_email: form.notifyEmail,
    filters: core.filters,
    enabled: form.enabled,
  }
}

// ── 静默表单 ────────────────────────────────────────────
/** 静默表单态。startsAt/endsAt 为 datetime-local 串(本地时间);ruleId 空=全局静默。 */
export interface SilenceForm {
  ruleId: string
  reason: string
  startsAt: string
  endsAt: string
  platform: string
  groupId: string
  region: string
}

export const EMPTY_SILENCE_FORM: SilenceForm = {
  ruleId: '',
  reason: '',
  startsAt: '',
  endsAt: '',
  platform: '',
  groupId: '',
  region: '',
}

/** datetime-local(无时区,浏览器按本地解释)→ RFC3339(UTC)。空串 → null;非法 → undefined。 */
export function localToISO(local: string): string | null | undefined {
  const v = local.trim()
  if (!v) return null
  const d = new Date(v)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

/** 构造新建静默请求。镜像 validateSilence:starts/ends 必填且 ends 严格晚于 starts;rule_id 若填须>0。 */
export function buildCreateSilence(form: SilenceForm, tenantId: number): CreateSilenceRequest | { error: string } {
  const reason = form.reason.trim()
  if (!reason) return { error: '请填写静默原因' }

  const starts = localToISO(form.startsAt)
  if (starts === null) return { error: '请填写开始时间' }
  if (starts === undefined) return { error: '开始时间格式非法' }
  const ends = localToISO(form.endsAt)
  if (ends === null) return { error: '请填写结束时间' }
  if (ends === undefined) return { error: '结束时间格式非法' }
  if (Date.parse(ends) <= Date.parse(starts)) return { error: '结束时间必须晚于开始时间' }

  const req: CreateSilenceRequest = {
    tenant_id: tenantId,
    reason,
    starts_at: starts,
    ends_at: ends,
  }
  const rid = form.ruleId.trim()
  if (rid) {
    if (!/^\d+$/.test(rid) || Number.parseInt(rid, 10) <= 0) return { error: '规则 ID 必须为正整数(留空=全局静默)' }
    req.rule_id = Number.parseInt(rid, 10)
  }
  const platform = form.platform.trim()
  if (platform) req.platform = platform
  const groupId = form.groupId.trim()
  if (groupId) req.group_id = groupId
  const region = form.region.trim()
  if (region) req.region = region
  return req
}

/** 静默是否当前生效(now 在 [starts, ends) 内)。now 注入便于测试。 */
export function silenceActive(
  s: { starts_at: string; ends_at: string },
  now: number = Date.now(),
): boolean {
  const start = Date.parse(s.starts_at)
  const end = Date.parse(s.ends_at)
  if (Number.isNaN(start) || Number.isNaN(end)) return false
  return now >= start && now < end
}
