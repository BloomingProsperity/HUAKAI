import type { CreateBindingRequest, FallbackClass, PoolBinding, UpdateBindingRequest } from './types'
import type { BadgeTone } from '../../ui/StatusBadge'

/*
 * 路由绑定的纯逻辑(可单测)。selection_mode 是核心:strict_priority(默认,同优先级均匀打散)
 * 与 priority_weighted(按账号 static_weight 加权倾斜)。
 */

export const SELECTION_MODES: ReadonlyArray<{ value: string; label: string; hint: string }> = [
  { value: 'strict_priority', label: '严格优先级', hint: '同优先级账号均匀打散(默认)' },
  { value: 'priority_weighted', label: '按权重加权', hint: '同优先级内按 static_weight 加权分流' },
]

/** 绑定运行时类别及其运维说明；本数组也是所有表单可选值的唯一来源。 */
export const FALLBACK_CLASSES: ReadonlyArray<{ value: FallbackClass; label: string; hint: string }> = [
  { value: 'normal', label: 'normal · 主类', hint: '主类；请求总从 normal 开始。' },
  {
    value: 'context_window',
    label: 'context_window · 上下文',
    hint: '上下文超限降级；需管理员确认目标池/模型确有更大窗口，系统不代验。',
  },
  {
    value: 'safety',
    label: 'safety · 内容安全',
    hint: '内容安全降级；配置后即生效，仅上游内容策略拒绝触发，本地审核与租户策略仍为终态。',
  },
  { value: 'quota', label: 'quota · 限流配额', hint: '限流配额降级；承接绑定、账号或上游容量耗尽。' },
  {
    value: 'manual',
    label: 'manual · 瞬态兜底',
    hint: '通用瞬态故障兜底；承接上游 5xx、连接/首字节超时或空响应。',
  },
]

const FALLBACK_CLASS_VALUES = new Set<string>(FALLBACK_CLASSES.map((item) => item.value))

export function isFallbackClass(value: unknown): value is FallbackClass {
  return typeof value === 'string' && FALLBACK_CLASS_VALUES.has(value)
}

/** 只为旧响应缺值保留兼容；非法或缺失值都不会进入表单选项与请求体。 */
export function normalizeFallbackClass(value: unknown): FallbackClass {
  return isFallbackClass(value) ? value : 'normal'
}

export function fallbackClassError(value: unknown): string | undefined {
  return isFallbackClass(value) ? undefined : '降级类必须是 normal、context_window、safety、quota 或 manual'
}

export function fallbackClassOption(value: unknown): (typeof FALLBACK_CLASSES)[number] {
  const normalized = normalizeFallbackClass(value)
  return FALLBACK_CLASSES.find((item) => item.value === normalized) ?? FALLBACK_CLASSES[0]
}

function fallbackClassTone(value: FallbackClass): BadgeTone {
  switch (value) {
    case 'normal':
      return 'muted'
    case 'context_window':
      return 'info'
    case 'safety':
      return 'danger'
    case 'quota':
    case 'manual':
      return 'warn'
  }
}

export function selectionModeLabel(mode: string): string {
  return SELECTION_MODES.find((m) => m.value === mode)?.label ?? mode
}

export interface BindingTableRow {
  id: number
  model: string
  pool: string
  priority: number
  selectionMode: string
  selectionTone: BadgeTone
  fallbackClass: FallbackClass
  fallbackClassLabel: string
  fallbackClassHint: string
  fallbackClassTone: BadgeTone
  status: string
  statusTone: BadgeTone
  binding: PoolBinding
}

/** 将路由绑定转换为表格展示行，不改变绑定本身及请求语义。 */
export function mapBindingRows(bindings: PoolBinding[]): BindingTableRow[] {
  return bindings.map((binding) => {
    const fallbackClass = normalizeFallbackClass(binding.fallback_class)
    const fallbackOption = fallbackClassOption(fallbackClass)
    return {
      id: binding.id,
      model: `#${binding.model_id}`,
      pool: `#${binding.pool_group_id}`,
      priority: binding.priority,
      selectionMode: selectionModeLabel(binding.selection_mode),
      selectionTone: binding.selection_mode === 'priority_weighted' ? 'info' : 'muted',
      fallbackClass,
      fallbackClassLabel: fallbackOption.label,
      fallbackClassHint: fallbackOption.hint,
      fallbackClassTone: fallbackClassTone(fallbackClass),
      status: binding.enabled ? '启用' : '停用',
      statusTone: binding.enabled ? 'ok' : 'muted',
      binding,
    }
  })
}

/** class 筛选只作用于已加载列表，不把前端字段伪装成后端查询参数。 */
export function filterBindingRows(rows: BindingTableRow[], fallbackClass: FallbackClass | ''): BindingTableRow[] {
  return fallbackClass === '' ? rows : rows.filter((row) => row.fallbackClass === fallbackClass)
}

/** 返回当前启用绑定里没有 normal 主类的模型，供运维在列表上直接发现断路配置。 */
export function enabledModelIdsWithoutNormal(bindings: PoolBinding[]): number[] {
  const enabledModels = new Set<number>()
  const modelsWithNormal = new Set<number>()
  for (const binding of bindings) {
    if (!binding.enabled) continue
    enabledModels.add(binding.model_id)
    if (normalizeFallbackClass(binding.fallback_class) === 'normal') modelsWithNormal.add(binding.model_id)
  }
  return [...enabledModels].filter((modelID) => !modelsWithNormal.has(modelID)).sort((a, b) => a - b)
}

export interface BindingEditForm {
  priority: string
  selectionMode: string
  fallbackClass: FallbackClass
  maxParallelRequests: string
  enabled: boolean
}

export function editFormFromBinding(b: PoolBinding): BindingEditForm {
  return {
    priority: String(b.priority),
    selectionMode: b.selection_mode,
    fallbackClass: normalizeFallbackClass(b.fallback_class),
    maxParallelRequests: b.max_parallel_requests == null ? '' : String(b.max_parallel_requests),
    enabled: b.enabled,
  }
}

function parseInt0(s: string): number | undefined {
  const n = Number(s.trim())
  return Number.isInteger(n) && n >= 0 ? n : undefined
}

function parseOptionalInt0(s: string): number | null | undefined {
  if (s.trim() === '') return null
  return parseInt0(s)
}

export function maxParallelRequestsError(value: string): string | undefined {
  return parseOptionalInt0(value) === undefined ? '最大并发请求数必须是大于等于 0 的整数，留空表示不限' : undefined
}

/**
 * 构造 PATCH 体。**后端把 PATCH 当整行覆盖、省略字段重置默认**(见 UpdateBindingRequest 注释),
 * 所以这里回填当前仍由界面管理的字段与既有可空字段。fallback_class 与
 * max_parallel_requests 都有运行时消费，即使没改也必须回填；weight 仍不由界面管理。
 * priority 表单值非法时回退到原值(不把非法值写下去)。
 */
export function buildBindingUpdate(original: PoolBinding, form: BindingEditForm): UpdateBindingRequest {
  const priority = parseInt0(form.priority)
  const maxParallelRequests = parseOptionalInt0(form.maxParallelRequests)
  const fallbackClass = isFallbackClass(form.fallbackClass) ? form.fallbackClass : normalizeFallbackClass(original.fallback_class)
  return {
    priority: priority ?? original.priority,
    selection_mode: form.selectionMode || original.selection_mode,
    enabled: form.enabled,
    fallback_class: fallbackClass,
    // 回填可空字段的当前值,避免被后端整行覆盖时清空。
    provider_model_id_override: original.provider_model_id_override ?? null,
    rpm_limit: original.rpm_limit ?? null,
    tpm_limit: original.tpm_limit ?? null,
    max_parallel_requests: maxParallelRequests === undefined ? original.max_parallel_requests ?? null : maxParallelRequests,
  }
}

/** 是否有可见字段改动(供模态判断"无改动则跳过提交",因 buildBindingUpdate 现在恒返回全字段)。 */
export function hasBindingChanges(original: PoolBinding, form: BindingEditForm): boolean {
  const priority = parseInt0(form.priority)
  const maxParallelRequests = parseOptionalInt0(form.maxParallelRequests)
  return (
    (priority !== undefined && priority !== original.priority) ||
    form.selectionMode !== original.selection_mode ||
    (isFallbackClass(form.fallbackClass) && form.fallbackClass !== normalizeFallbackClass(original.fallback_class)) ||
    (maxParallelRequests !== undefined && maxParallelRequests !== (original.max_parallel_requests ?? null)) ||
    form.enabled !== original.enabled
  )
}

export interface BindingCreateForm {
  modelId: string
  poolGroupId: string
  priority: string
  selectionMode: string
  fallbackClass: FallbackClass
  maxParallelRequests: string
}

export const EMPTY_CREATE_BINDING: BindingCreateForm = {
  modelId: '',
  poolGroupId: '',
  priority: '0',
  selectionMode: 'strict_priority',
  fallbackClass: 'normal',
  maxParallelRequests: '',
}

function parsePositive(s: string): number | undefined {
  const n = Number(s.trim())
  return Number.isInteger(n) && n > 0 ? n : undefined
}

export function buildBindingCreate(form: BindingCreateForm): CreateBindingRequest | { error: string } {
  const model_id = parsePositive(form.modelId)
  if (!model_id) return { error: '请填写有效的 model_id' }
  const pool_group_id = parsePositive(form.poolGroupId)
  if (!pool_group_id) return { error: '请填写有效的 pool_group_id' }
  const fallbackError = fallbackClassError(form.fallbackClass)
  if (fallbackError) return { error: fallbackError }
  const req: CreateBindingRequest = {
    model_id,
    pool_group_id,
    selection_mode: form.selectionMode,
    fallback_class: form.fallbackClass,
  }
  const maxParallelRequests = parseOptionalInt0(form.maxParallelRequests)
  if (maxParallelRequests === undefined) return { error: maxParallelRequestsError(form.maxParallelRequests)! }
  if (maxParallelRequests !== null) req.max_parallel_requests = maxParallelRequests
  const priority = parseInt0(form.priority)
  if (priority !== undefined) req.priority = priority
  return req
}
