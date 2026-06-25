/*
 * 媒体任务(AI 绘图/视频/音频异步任务)类型。对应后端 mediataskhttp:
 * GET /v1/media-tasks(列表,{items})、GET /v1/media-tasks/{id}(单条)。均 session 鉴权。
 * 本前端只读任务记录;提交(POST)会触发真实媒体生成与计费,属 Owner-gated,不在此页。
 */

/** 任务状态(后端 mediatask.Status)。 */
export type MediaTaskStatus = 'queued' | 'in_progress' | 'succeeded' | 'failed' | 'expired' | string

/** 单条媒体任务(后端 mediatask.Task,只取前端展示用字段)。 */
export interface MediaTask {
  id: number
  task_type: string
  status: MediaTaskStatus
  provider: string
  provider_task_id?: string
  request_id: string
  /** 预估扣费(分)。 */
  estimated_cents: number
  /** 实际扣费(分);完成后才有。 */
  actual_cents?: number | null
  /** 失败时的错误分类。 */
  error_class?: string
  /** 进度 0-100。 */
  progress: number
  created_at: string
  updated_at: string
  finished_at?: string | null
}

/** GET /v1/media-tasks 响应。 */
export interface MediaTaskListResponse {
  items: MediaTask[]
}
