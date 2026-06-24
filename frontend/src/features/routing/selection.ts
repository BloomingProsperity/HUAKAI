import type { CreateBindingRequest, PoolBinding, UpdateBindingRequest } from './types'

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
 * 构造 PATCH 体:只发与原绑定【不同】的字段(避免无谓写入);priority/weight 非负整数才带。
 */
export function buildBindingUpdate(original: PoolBinding, form: BindingEditForm): UpdateBindingRequest {
  const req: UpdateBindingRequest = {}
  const priority = parseInt0(form.priority)
  if (priority !== undefined && priority !== original.priority) req.priority = priority
  const weight = parseInt0(form.weight)
  if (weight !== undefined && weight !== original.weight) req.weight = weight
  if (form.selectionMode && form.selectionMode !== original.selection_mode) req.selection_mode = form.selectionMode
  if (form.fallbackClass && form.fallbackClass !== original.fallback_class) req.fallback_class = form.fallbackClass
  if (form.enabled !== original.enabled) req.enabled = form.enabled
  return req
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
