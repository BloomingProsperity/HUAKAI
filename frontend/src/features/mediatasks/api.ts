import { apiGet, apiSend } from '../../lib/api'
import type {
  CreateMediaTaskRequest,
  CreateMediaTaskResponse,
  MediaTask,
  MediaTaskListResponse,
} from './types'

export function listMediaTasks(limit = 50, signal?: AbortSignal): Promise<MediaTaskListResponse> {
  return apiGet<MediaTaskListResponse>('/v1/media-tasks', { query: { limit }, signal })
}

export function getMediaTask(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/v1/media-tasks/${id}`, { signal })
}

export function createMediaTask(
  body: CreateMediaTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', '/v1/media-tasks', body, { signal })
}
