import { apiGet, apiSend } from '../../lib/api'
import type {
  CreateMediaTaskRequest,
  CreateMediaTaskResponse,
  MediaTask,
  MediaTaskListResponse,
  MidjourneyAction,
  MidjourneyTaskRequest,
  SunoTaskRequest,
  VideoTaskRequest,
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

export function submitMidjourneyTask(
  action: MidjourneyAction,
  body: MidjourneyTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', `/mj/submit/${encodeURIComponent(action)}`, body, { signal })
}

export function submitMidjourneySwap(
  body: MidjourneyTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', '/mj/insight-face/swap', body, { signal })
}

export function getMidjourneyTask(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/mj/task/${id}/fetch`, { signal })
}

export function getMidjourneyImageSeed(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/mj/task/${id}/image-seed`, { signal })
}

export function listMidjourneyTasks(limit: number, signal?: AbortSignal): Promise<MediaTaskListResponse> {
  return apiSend<MediaTaskListResponse>('POST', '/mj/task/list-by-condition', { limit }, { signal })
}

export function submitSunoTask(
  body: SunoTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', '/suno/submit', body, { signal })
}

export function submitSunoAction(
  action: string,
  body: SunoTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', `/suno/submit/${encodeURIComponent(action)}`, body, { signal })
}

export function getSunoTask(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/suno/fetch/${id}`, { signal })
}

export function getSunoTaskByQuery(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>('/suno/fetch', { query: { id }, signal })
}

export function submitVideoTask(
  body: VideoTaskRequest,
  signal?: AbortSignal,
): Promise<CreateMediaTaskResponse> {
  return apiSend<CreateMediaTaskResponse>('POST', '/video/submit', body, { signal })
}

export function getVideoTask(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>(`/video/fetch/${id}`, { signal })
}

export function getVideoTaskByQuery(id: number, signal?: AbortSignal): Promise<MediaTask> {
  return apiGet<MediaTask>('/video/fetch', { query: { id }, signal })
}

export function listVideoTasks(limit: number, signal?: AbortSignal): Promise<MediaTaskListResponse> {
  return apiGet<MediaTaskListResponse>('/video/fetch', { query: { limit }, signal })
}
