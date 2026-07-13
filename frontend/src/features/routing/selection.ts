import type { CreateBindingRequest, PoolBinding, UpdateBindingRequest } from './types'
import type { BadgeTone } from '../../ui/StatusBadge'

/*
 * 路由绑定的纯逻辑(可单测)。selection_mode 是核心:strict_priority(默认,同优先级均匀打散)
 * 与 priority_weighted(按 weight 加权倾斜)—— 对应后端 PR#118 选号能力。
 */

export const SELECTION_MODES: ReadonlyArray<{ value: string; label: string; hint: string }> = [
  { value: 'strict_priority', label: '严格优先级', hint: '同优先级账号均匀打散(默认)' },
  { value: 'priority_weighted', label: '按权重加权', hint: '同优先级内按 static_weight 加权分流' },
]

export const FALLBACK_CLASSES: ReadonlyArray<{ value: string; label: string }> = [
  { value: 'normal', label: '常规' },
  { value: 'context_window', label: '上下文超限' },
  { value: 'safety', label: '内容安全' },
  { value: 'quota', label: '配额' },
  { value: 'manual', label: '手动' },
]

export function selectionModeLabel(mode: string): string {
  return SELECTION_MODES.find((m) => m.value === mode)?.label ?? mode
}
export function fallbackClassLabel(cls: string): string {
  return FALLBACK_CLASSES.find((c) => c.value === cls)?.label ?? cls
}

export interface BindingTableRow {
  id: number
  model: string
  pool: string
  priority: number
  weight: number
  selectionMode: string
  selectionTone: BadgeTone
  fallbackClass: string
  status: string
  statusTone: BadgeTone
  binding: PoolBinding
}

/** 将路由绑定转换为表格展示行，不改变绑定本身及请求语义。 */
export function mapBindingRows(bindings: PoolBinding[]): BindingTableRow[] {
  return bindings.map((binding) => ({
    id: binding.id,
    model: `#${binding.model_id}`,
    pool: `#${binding.pool_group_id}`,
    priority: binding.priority,
    weight: binding.weight,
    selectionMode: selectionModeLabel(binding.selection_mode),
    selectionTone: binding.selection_mode === 'priority_weighted' ? 'info' : 'muted',
    fallbackClass: fallbackClassLabel(binding.fallback_class),
    status: binding.enabled ? '启用' : '停用',
    statusTone: binding.enabled ? 'ok' : 'muted',
    binding,
  }))
}

export interface BindingEditForm {
  priority: string
  weight: string
  selectionMode: string
  fallbackClass: string
  enabled: boolean
}

export function editFormFromBinding(b: PoolBinding): BindingEditForm {
  return {
    priority: String(b.priority),
    weight: String(b.weight),
    selectionMode: b.selection_mode,
    fallbackClass: b.fallback_class,
    enabled: b.enabled,
  }
}

function parseInt0(s: string): number | undefined {
  const n = Number(s.trim())
  return Number.isInteger(n) && n >= 0 ? n : undefined
}

/**
 * 构造 PATCH 体。**后端把 PATCH 当整行覆盖、省略字段重置默认**(见 UpdateBindingRequest 注释),
 * 所以这里【回填当前全部已知字段】+ 套用表单改动,而不是只发 diff —— 否则单字段编辑会把
 * 其它字段静默重置(weight→1、selection_mode→strict_priority 等)造成数据损坏。
 * priority/weight 表单值非法时回退到原值(不把非法值写下去)。
 * 注:后端还有 max_parallel_requests/disabled_reason/生效窗等字段当前列表 DTO 不返回,前端无从回填,
 * 这些仍会被后端清空——属残留,需后端改 COALESCE 部分更新(本前端修复覆盖所有 UI 可见字段)。
 */
export function buildBindingUpdate(original: PoolBinding, form: BindingEditForm): UpdateBindingRequest {
  const priority = parseInt0(form.priority)
  const weight = parseInt0(form.weight)
  return {
    priority: priority ?? original.priority,
    weight: weight ?? original.weight,
    selection_mode: form.selectionMode || original.selection_mode,
    fallback_class: form.fallbackClass || original.fallback_class,
    enabled: form.enabled,
    // 回填可空字段的当前值,避免被后端整行覆盖时清空。
    provider_model_id_override: original.provider_model_id_override ?? null,
    rpm_limit: original.rpm_limit ?? null,
    tpm_limit: original.tpm_limit ?? null,
  }
}

/** 是否有可见字段改动(供模态判断"无改动则跳过提交",因 buildBindingUpdate 现在恒返回全字段)。 */
export function hasBindingChanges(original: PoolBinding, form: BindingEditForm): boolean {
  const priority = parseInt0(form.priority)
  const weight = parseInt0(form.weight)
  return (
    (priority !== undefined && priority !== original.priority) ||
    (weight !== undefined && weight !== original.weight) ||
    form.selectionMode !== original.selection_mode ||
    form.fallbackClass !== original.fallback_class ||
    form.enabled !== original.enabled
  )
}

export interface BindingCreateForm {
  modelId: string
  poolGroupId: string
  priority: string
  weight: string
  selectionMode: string
  fallbackClass: string
}

export const EMPTY_CREATE_BINDING: BindingCreateForm = {
  modelId: '',
  poolGroupId: '',
  priority: '0',
  weight: '1',
  selectionMode: 'strict_priority',
  fallbackClass: 'normal',
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
  const req: CreateBindingRequest = {
    model_id,
    pool_group_id,
    selection_mode: form.selectionMode,
    fallback_class: form.fallbackClass,
  }
  const priority = parseInt0(form.priority)
  if (priority !== undefined) req.priority = priority
  const weight = parsePositive(form.weight)
  if (weight !== undefined) req.weight = weight
  return req
}
