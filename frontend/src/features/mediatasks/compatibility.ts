import type {
  MediaResource,
  MediaResourceKind,
  MediaTask,
  MidjourneyAction,
  MidjourneySubmitForm,
  MidjourneySwapForm,
  MidjourneyTaskRequest,
  SunoActionForm,
  SunoSubmitForm,
  SunoTaskRequest,
  VideoSubmitForm,
  VideoTaskRequest,
} from './types'

export const MIDJOURNEY_ACTIONS: readonly MidjourneyAction[] = [
  'imagine',
  'describe',
  'blend',
  'change',
  'simple-change',
  'action',
  'modal',
  'shorten',
  'edits',
  'video',
  'upload-discord-images',
]

export const DEFAULT_MIDJOURNEY_FORM: MidjourneySubmitForm = {
  action: 'imagine',
  prompt: '',
  botType: 'MID_JOURNEY',
  notifyHook: '',
  base64Images: '',
  customID: '',
  commandAction: '',
  state: '',
  indexJSON: '',
  maskBase64: '',
}

export const DEFAULT_MIDJOURNEY_SWAP_FORM: MidjourneySwapForm = {
  sourceBase64: '',
  targetBase64: '',
}

export const DEFAULT_SUNO_FORM: SunoSubmitForm = {
  lyrics: '',
  style: '',
  title: '',
  instrumental: false,
  customMode: false,
  description: '',
  notifyHook: '',
  mv: '',
  modelVersion: '',
  continueAt: '',
  continueClipID: '',
}

export const DEFAULT_SUNO_ACTION_FORM: SunoActionForm = {
  ...DEFAULT_SUNO_FORM,
  action: '',
}

export const DEFAULT_VIDEO_FORM: VideoSubmitForm = {
  model: '',
  prompt: '',
  duration: '5',
  image: '',
  width: '',
  height: '',
  fps: '',
  seed: '',
  count: '1',
  responseFormat: '',
}

export function buildMidjourneySubmitRequest(
  form: MidjourneySubmitForm,
  requestID: string,
): { action: MidjourneyAction; body: MidjourneyTaskRequest } {
  const action = normalizeMidjourneyAction(form.action)
  const prompt = form.prompt.trim()
  const customID = form.customID.trim()
  const commandAction = form.commandAction.trim()
  const state = form.state.trim()
  const images = parseBase64Images(form.base64Images, 'Base64 图片')
  const mask = form.maskBase64.trim()
    ? normalizeImageBase64(form.maskBase64, '蒙版图片')
    : ''

  if (action === 'imagine' && !prompt) throw new Error('请填写 Prompt')
  if ((action === 'describe' || action === 'blend' || action === 'upload-discord-images') && images.length === 0) {
    throw new Error('请填写 Base64 图片')
  }
  if (!prompt && images.length === 0 && !customID && !commandAction && !state && !mask) {
    throw new Error('请填写 Prompt、图片或动作参数')
  }

  const body: MidjourneyTaskRequest = { request_id: requiredRequestID(requestID) }
  assignText(body, 'prompt', prompt)
  assignText(body, 'customId', customID)
  assignText(body, 'botType', form.botType)
  assignText(body, 'notifyHook', form.notifyHook)
  assignText(body, 'action', commandAction)
  assignText(body, 'state', state)
  if (images.length > 0) body.base64Array = images
  if (mask) body.maskBase64 = mask
  if (form.indexJSON.trim()) body.index = parseJSONValue(form.indexJSON, 'Index')
  return { action, body }
}

export function buildMidjourneySwapRequest(
  form: MidjourneySwapForm,
  requestID: string,
): MidjourneyTaskRequest {
  const sourceBase64 = normalizeImageBase64(required(form.sourceBase64, '源图片'), '源图片')
  const targetBase64 = normalizeImageBase64(required(form.targetBase64, '目标图片'), '目标图片')
  return { request_id: requiredRequestID(requestID), sourceBase64, targetBase64 }
}

export function buildSunoSubmitRequest(form: SunoSubmitForm, requestID: string): SunoTaskRequest {
  return buildSunoBody(form, requestID, false)
}

export function buildSunoActionRequest(
  form: SunoActionForm,
  requestID: string,
): { action: string; body: SunoTaskRequest } {
  const action = normalizeSunoAction(form.action)
  return { action, body: buildSunoBody(form, requestID, true) }
}

export function buildVideoSubmitRequest(form: VideoSubmitForm, requestID: string): VideoTaskRequest {
  const body: VideoTaskRequest = {
    request_id: requiredRequestID(requestID),
    model: required(form.model, '模型'),
    prompt: required(form.prompt, 'Prompt'),
    duration: positiveNumber(form.duration, '时长'),
  }
  const image = form.image.trim()
  if (image) body.image = normalizeImageReference(image)
  assignOptionalPositiveInteger(body, 'width', form.width, '宽度')
  assignOptionalPositiveInteger(body, 'height', form.height, '高度')
  assignOptionalPositiveInteger(body, 'fps', form.fps, 'FPS')
  assignOptionalInteger(body, 'seed', form.seed, '种子')
  assignOptionalPositiveInteger(body, 'n', form.count, '生成数量')
  assignText(body, 'response_format', form.responseFormat)
  return body
}

export function normalizeSunoAction(raw: string): string {
  const value = raw.trim()
  if (!value) throw new Error('请填写动作')
  if (!/^[A-Za-z0-9_-]+$/.test(value)) throw new Error('动作只能包含字母、数字、- 或 _')
  return value
}

export function parseTaskID(raw: string): number {
  const value = Number(raw.trim())
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error('任务 ID 必须是正整数')
  return value
}

export function parseTaskLimit(raw: string): number {
  const value = Number(raw.trim())
  if (!Number.isInteger(value) || value < 1 || value > 200) throw new Error('数量必须是 1–200 的整数')
  return value
}

export function mergeMediaTasks(current: MediaTask[], incoming: MediaTask[]): MediaTask[] {
  const currentIDs = new Set(current.map((task) => task.id))
  const incomingByID = new Map(incoming.map((task) => [task.id, task]))
  const additions = incoming.filter((task) => !currentIDs.has(task.id))
  return [...additions, ...current.map((task) => incomingByID.get(task.id) ?? task)]
}

export function filterMediaTasksByProvider(tasks: MediaTask[], provider: string): MediaTask[] {
  const normalized = provider.trim().toLowerCase()
  return tasks.filter((task) => task.provider.trim().toLowerCase() === normalized)
}

export function extractMediaResources(result: unknown, preferredKind: MediaResourceKind): MediaResource[] {
  const resources: MediaResource[] = []
  const seen = new Set<string>()

  const add = (src: string, kind: MediaResourceKind, label: string) => {
    if (seen.has(src) || resources.length >= 24) return
    seen.add(src)
    resources.push({ src, kind, label: label || `${kind} 结果` })
  }

  const visit = (value: unknown, path: string[], hint: MediaResourceKind | undefined, depth: number) => {
    if (depth > 7 || resources.length >= 24 || value == null) return
    if (typeof value === 'string') {
      const resource = resourceFromString(value, hint ?? preferredKind, path[path.length - 1] ?? '结果')
      if (resource) add(resource.src, resource.kind, resource.label)
      return
    }
    if (Array.isArray(value)) {
      value.forEach((item, index) => visit(item, [...path, String(index + 1)], hint, depth + 1))
      return
    }
    if (typeof value !== 'object') return
    for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
      visit(item, [...path, key], mediaKindFromKey(key) ?? hint, depth + 1)
    }
  }

  visit(result, [], undefined, 0)
  return resources
}

export function formatMediaResult(result: unknown): string {
  if (result === undefined || result === null) return '暂无结构化结果'
  const text = JSON.stringify(result, (key, value) => {
    if (typeof value !== 'string') return value
    const dataMatch = /^data:([^;,]+);base64,(.+)$/i.exec(value)
    if (dataMatch && dataMatch[2].length > 80) return `data:${dataMatch[1]};base64,[${dataMatch[2].length} 字符]`
    if (/base64|b64/i.test(key) && value.length > 80) return `[base64 ${value.length} 字符]`
    return value
  }, 2)
  if (!text) return String(result)
  return text.length > 12_000 ? `${text.slice(0, 12_000)}\n…结果过长，已截断` : text
}

function buildSunoBody(form: SunoSubmitForm, requestID: string, isAction: boolean): SunoTaskRequest {
  const lyrics = form.lyrics.trim()
  const continueClipID = form.continueClipID.trim()
  if (!lyrics && (!isAction || !continueClipID)) {
    throw new Error(isAction ? '请填写歌词/描述或续写片段 ID' : '请填写歌词/描述')
  }

  const body: SunoTaskRequest = {
    request_id: requiredRequestID(requestID),
    make_instrumental: form.instrumental,
    custom_mode: form.customMode,
  }
  if (form.customMode) assignText(body, 'input', lyrics)
  else assignText(body, 'prompt', lyrics)
  assignText(body, 'gpt_description_prompt', form.description)
  assignText(body, 'notify_hook', form.notifyHook)
  assignText(body, 'tags', form.style)
  assignText(body, 'title', form.title)
  assignText(body, 'mv', form.mv)
  assignText(body, 'model_version', form.modelVersion)
  assignText(body, 'continue_clip_id', continueClipID)
  if (form.continueAt.trim()) body.continue_at = nonNegativeNumber(form.continueAt, '续写起点')
  return body
}

function normalizeMidjourneyAction(action: MidjourneyAction): MidjourneyAction {
  if (!MIDJOURNEY_ACTIONS.includes(action)) throw new Error('不支持的 Midjourney 动作')
  return action
}

function parseBase64Images(raw: string, label: string): string[] {
  return raw.split(/\r?\n/)
    .map((item) => item.trim())
    .filter(Boolean)
    .map((item, index) => normalizeImageBase64(item, `${label} ${index + 1}`))
}

function normalizeImageReference(raw: string): string {
  const value = raw.trim()
  if (/^https?:\/\//i.test(value)) return value
  return normalizeImageBase64(value, '参考图')
}

function normalizeImageBase64(raw: string, label: string): string {
  const value = raw.trim()
  const dataMatch = /^data:(image\/(?:png|jpe?g|webp|gif|avif));base64,(.*)$/i.exec(value)
  if (dataMatch) {
    const payload = compactBase64(dataMatch[2])
    if (!isValidBase64Payload(payload)) throw new Error(`${label}不是有效 Base64`)
    return `data:${dataMatch[1].toLowerCase()};base64,${payload}`
  }
  const payload = compactBase64(value)
  if (!isValidBase64Payload(payload)) throw new Error(`${label}不是有效 Base64`)
  return payload
}

function isValidBase64Payload(value: string): boolean {
  if (!value || value.length % 4 === 1 || !/^[A-Za-z0-9+/]*={0,2}$/.test(value)) return false
  const firstPadding = value.indexOf('=')
  if (firstPadding >= 0 && firstPadding < value.length - 2) return false
  const unpadded = value.replace(/=+$/, '')
  const padded = unpadded + '='.repeat((4 - (unpadded.length % 4)) % 4)
  try {
    return btoa(atob(padded)).replace(/=+$/, '') === unpadded
  } catch {
    return false
  }
}

function compactBase64(value: string): string {
  return value.replace(/\s+/g, '')
}

function resourceFromString(
  raw: string,
  hintedKind: MediaResourceKind,
  label: string,
): MediaResource | null {
  const value = raw.trim()
  if (!value) return null
  const dataMatch = /^data:((?:image\/(?:png|jpe?g|webp|gif|avif))|(?:audio\/(?:mpeg|mp3|wav|x-wav|ogg|mp4|aac|flac))|(?:video\/(?:mp4|webm|quicktime|x-m4v)));base64,(.*)$/i.exec(value)
  if (dataMatch) {
    const payload = compactBase64(dataMatch[2])
    if (!isValidBase64Payload(payload)) return null
    return { kind: dataMatch[1].split('/', 1)[0].toLowerCase() as MediaResourceKind, src: value, label }
  }
  if (/^https?:\/\//i.test(value)) {
    return { kind: mediaKindFromURL(value) ?? hintedKind, src: value, label }
  }
  if (/base64|b64/i.test(label) || mediaKindFromKey(label) !== undefined) {
    const payload = compactBase64(value)
    if (isValidBase64Payload(payload)) {
      return { kind: hintedKind, src: `data:${mimeForKind(hintedKind)};base64,${payload}`, label }
    }
  }
  return null
}

function mediaKindFromKey(key: string): MediaResourceKind | undefined {
  const value = key.toLowerCase()
  if (value.includes('image') || value.includes('thumbnail') || value === 'b64_json') return 'image'
  if (value.includes('audio') || value.includes('song')) return 'audio'
  if (value.includes('video')) return 'video'
  return undefined
}

function mediaKindFromURL(raw: string): MediaResourceKind | undefined {
  const path = raw.split(/[?#]/, 1)[0].toLowerCase()
  if (/\.(png|jpe?g|webp|gif|avif)$/.test(path)) return 'image'
  if (/\.(mp3|wav|ogg|m4a|flac|aac)$/.test(path)) return 'audio'
  if (/\.(mp4|webm|mov|m4v)$/.test(path)) return 'video'
  return undefined
}

function mimeForKind(kind: MediaResourceKind): string {
  switch (kind) {
    case 'image': return 'image/png'
    case 'audio': return 'audio/mpeg'
    case 'video': return 'video/mp4'
  }
}

function required(value: string, label: string): string {
  const normalized = value.trim()
  if (!normalized) throw new Error(`请填写${/^[A-Za-z]/.test(label) ? ' ' : ''}${label}`)
  return normalized
}

function requiredRequestID(value: string): string {
  const normalized = value.trim()
  if (!normalized) throw new Error('缺少请求 ID')
  return normalized
}

function assignText<T extends object, K extends keyof T>(target: T, key: K, raw: string): void {
  const value = raw.trim()
  if (value) target[key] = value as T[K]
}

function positiveNumber(raw: string, label: string): number {
  const value = Number(raw.trim())
  if (!Number.isFinite(value) || value <= 0) throw new Error(`${label}必须大于 0`)
  return value
}

function nonNegativeNumber(raw: string, label: string): number {
  const value = Number(raw.trim())
  if (!Number.isFinite(value) || value < 0) throw new Error(`${label}不能小于 0`)
  return value
}

function assignOptionalPositiveInteger<T extends object, K extends keyof T>(
  target: T,
  key: K,
  raw: string,
  label: string,
): void {
  if (!raw.trim()) return
  const value = Number(raw.trim())
  if (!Number.isSafeInteger(value) || value <= 0) throw new Error(`${label}必须是正整数`)
  target[key] = value as T[K]
}

function assignOptionalInteger<T extends object, K extends keyof T>(
  target: T,
  key: K,
  raw: string,
  label: string,
): void {
  if (!raw.trim()) return
  const value = Number(raw.trim())
  if (!Number.isSafeInteger(value)) throw new Error(`${label}必须是整数`)
  target[key] = value as T[K]
}

function parseJSONValue(raw: string, label: string): unknown {
  try {
    return JSON.parse(raw)
  } catch {
    throw new Error(`${label} JSON 格式无效`)
  }
}
