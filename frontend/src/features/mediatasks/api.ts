import { apiGet } from '../../lib/api'
import type { MediaTask, MediaTaskListResponse } from './types'

/*
 * 媒体任务数据访问层(只读)。session 鉴权(tokenForPath 对 /v1/media-tasks 发 session token)。
 * 后端:internal/mediataskhttp/handlers.go(MountRoutes,routes.go:306 在 SessionMiddleware 组内)。
 */

/** 任务列表。limit 1-200(后端校验)。 */
export function listMediaTasks(limit = 50, signal?: AbortSignal): Promise<MediaTaskListResponse> {
  return apiGet<MediaTaskListResponse>('/v1/media-tasks', { query: { limit }, signal })
}

/** 单条任务状态(轮询进行中任务用)。 */
export function getMediaTask(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/v1/media-tasks/${id}`, { signal })
}
