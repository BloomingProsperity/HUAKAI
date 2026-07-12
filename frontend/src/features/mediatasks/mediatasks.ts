import type { BadgeTone } from '../../ui/StatusBadge'
import type { CreateMediaTaskRequest, MediaTask, MediaTaskCreateForm, MediaTaskKind } from './types'

/*
 * 媒体任务纯逻辑(与 React/网络解耦,便于变异):状态配色/中文名、任务类型名、计费展示、是否进行中。
 */

/** 状态 → 徽章配色。succeeded=绿、failed/expired=红、in_progress=黄、queued=灰。 */
export function statusTone(status: string): BadgeTone {
  switch (status.trim().toLowerCase()) {
    case 'succeeded':
      return 'ok'
    case 'in_progress':
      return 'warn'
    case 'failed':
    case 'expired':
      return 'danger'
    case 'queued':
      return 'info'
    default:
      return 'muted'
  }
}

/** 状态中文名。 */
export function statusLabel(status: string): string {
  switch (status.trim().toLowerCase()) {
    case 'queued':
      return '排队中'
    case 'in_progress':
      return '生成中'
    case 'succeeded':
      return '已完成'
    case 'failed':
      return '失败'
    case 'expired':
      return '已过期'
    default:
      return status || '未知'
  }
}

/** 任务类型中文名(常见 image/video/audio;未知原样)。 */
export function taskTypeLabel(taskType: string): string {
  switch (taskType.trim().toLowerCase()) {
    case 'image':
    case 'image_generation':
      return '绘图'
    case 'video':
    case 'video_generation':
      return '视频'
    case 'audio':
      return '音频'
    case 'music':
    case 'music_generation':
      return '音乐'
    default:
      return taskType || '任务'
  }
}

/** 是否进行中(用于决定是否轮询刷新)。 */
export function isActive(status: string): boolean {
  const s = status.trim().toLowerCase()
  return s === 'queued' || s === 'in_progress'
}

/**
 * 计费展示(分→元)。完成有实际扣费用实际,否则展示预估并标注。
 * 整数运算 + padStart 补零,避免浮点误差。
 */
export function formatTaskCost(task: Pick<MediaTask, 'estimated_cents' | 'actual_cents' | 'status'>): string {
  const hasActual = task.actual_cents != null
  const cents = hasActual ? (task.actual_cents as number) : task.estimated_cents
  const dollars = centsToUSD(cents)
  // 判别核心:有实际扣费用实际值且不带"预估"前缀;否则用预估值加"预估"。
  return hasActual ? `$${dollars}` : `预估 $${dollars}`
}

/** 分→美元字符串(整数运算补零)。 */
export function centsToUSD(cents: number): string {
  const sign = cents < 0 ? '-' : ''
  const abs = Math.abs(Math.trunc(cents))
  const whole = Math.floor(abs / 100)
  const frac = abs % 100
  return `${sign}${whole}.${String(frac).padStart(2, '0')}`
}

export const DEFAULT_MEDIA_TASK_FORM: MediaTaskCreateForm = {
  taskKind: 'image',
  model: '',
  prompt: '',
  parametersJSON: '{}',
}

export function buildMediaTaskRequest(form: MediaTaskCreateForm, requestID: string): CreateMediaTaskRequest {
  const model = form.model.trim()
  const prompt = form.prompt.trim()
  const normalizedRequestID = requestID.trim()
  if (!normalizedRequestID) throw new Error('缺少请求 ID')
  if (!model) throw new Error('请填写模型')
  if (!prompt) throw new Error('请填写 Prompt')

  const parameters = parseParameters(form.parametersJSON)
  for (const fixed of ['model', 'prompt']) {
    if (fixed in parameters) throw new Error(`高级参数不能覆盖 ${fixed}`)
  }
  return {
    request_id: normalizedRequestID,
    task_type: taskTypeValue(form.taskKind),
    provider: 'http',
    input_params: { model, prompt, ...parameters },
  }
}

export function newMediaTaskRequestID(): string {
  const uuid = globalThis.crypto?.randomUUID?.()
  if (uuid) return `web-${uuid}`
  return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

export async function pollMediaTaskUpdates(
  ids: number[],
  load: (id: number) => Promise<MediaTask>,
): Promise<MediaTask[]> {
  const results = await Promise.allSettled(ids.map(load))
  return results.flatMap((result) => result.status === 'fulfilled' ? [result.value] : [])
}

function taskTypeValue(kind: MediaTaskKind): string {
  switch (kind) {
    case 'image': return 'image_generation'
    case 'music': return 'music_generation'
    case 'video': return 'video_generation'
  }
}

function parseParameters(raw: string): Record<string, unknown> {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw.trim() || '{}')
  } catch {
    throw new Error('参数 JSON 格式无效')
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw new Error('参数 JSON 必须是对象')
  }
  return parsed as Record<string, unknown>
}
