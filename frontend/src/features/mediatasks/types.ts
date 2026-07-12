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
