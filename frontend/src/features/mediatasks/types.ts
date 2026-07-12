export type MediaTaskStatus = 'queued' | 'in_progress' | 'succeeded' | 'failed' | 'expired' | string
export type MediaTaskKind = 'image' | 'music' | 'video'

export interface MediaTask {
  id: number
  task_type: string
  status: MediaTaskStatus
  provider: string
  provider_task_id?: string
  request_id: string
  input_params?: Record<string, unknown>
  result?: unknown
  estimated_cents: number
  actual_cents?: number | null
  error_class?: string
  progress: number
  created_at: string
  updated_at: string
  finished_at?: string | null
}

export interface MediaTaskListResponse {
  items: MediaTask[]
}

export interface MediaTaskCreateForm {
  taskKind: MediaTaskKind
  model: string
  prompt: string
  parametersJSON: string
}

export interface CreateMediaTaskRequest {
  request_id: string
  task_type: string
  provider: 'http'
  input_params: Record<string, unknown>
}

export interface CreateMediaTaskResponse {
  task_id: number
  status: MediaTaskStatus
}

export type MidjourneyAction =
  | 'imagine'
  | 'describe'
  | 'blend'
  | 'change'
  | 'simple-change'
  | 'action'
  | 'modal'
  | 'shorten'
  | 'edits'
  | 'video'
  | 'upload-discord-images'

export interface MidjourneySubmitForm {
  action: MidjourneyAction
  prompt: string
  botType: string
  notifyHook: string
  base64Images: string
  customID: string
  commandAction: string
  state: string
  indexJSON: string
  maskBase64: string
}

export interface MidjourneySwapForm {
  sourceBase64: string
  targetBase64: string
}

export interface MidjourneyTaskRequest {
  request_id: string
  prompt?: string
  customId?: string
  botType?: string
  notifyHook?: string
  action?: string
  state?: string
  base64Array?: string[]
  index?: unknown
  maskBase64?: string
  sourceBase64?: string
  targetBase64?: string
}

export interface SunoSubmitForm {
  lyrics: string
  style: string
  title: string
  instrumental: boolean
  customMode: boolean
  description: string
  notifyHook: string
  mv: string
  modelVersion: string
  continueAt: string
  continueClipID: string
}

export interface SunoActionForm extends SunoSubmitForm {
  action: string
}

export interface SunoTaskRequest {
  request_id: string
  gpt_description_prompt?: string
  prompt?: string
  mv?: string
  title?: string
  tags?: string
  continue_at?: number
  continue_clip_id?: string
  make_instrumental: boolean
  model_version?: string
  custom_mode: boolean
  input?: string
  notify_hook?: string
}

export interface VideoSubmitForm {
  model: string
  prompt: string
  duration: string
  image: string
  width: string
  height: string
  fps: string
  seed: string
  count: string
  responseFormat: string
}

export interface VideoTaskRequest {
  request_id: string
  model: string
  prompt: string
  duration: number
  image?: string
  width?: number
  height?: number
  fps?: number
  seed?: number
  n?: number
  response_format?: string
}

export type CompatibilityQueryMode = 'path' | 'query'
export type MediaResourceKind = 'image' | 'audio' | 'video'

export interface MediaResource {
  kind: MediaResourceKind
  src: string
  label: string
}
