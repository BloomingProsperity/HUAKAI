import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiClient = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: apiClient.get, apiSend: apiClient.send }
})

import { createMediaTask, getMediaTask, listMediaTasks } from './api'
import {
  buildMediaTaskRequest,
  centsToUSD,
  DEFAULT_MEDIA_TASK_FORM,
  formatTaskCost,
  isActive,
  pollMediaTaskUpdates,
  statusLabel,
  statusTone,
  taskTypeLabel,
} from './mediatasks'

describe('媒体任务展示逻辑', () => {
  it('状态配色和标签覆盖终态、活跃态与未知态', () => {
    expect(statusTone('succeeded')).toBe('ok')
    expect(statusTone('IN_PROGRESS')).toBe('warn')
    expect(statusTone('failed')).toBe('danger')
    expect(statusTone('expired')).toBe('danger')
    expect(statusTone('queued')).toBe('info')
    expect(statusTone('weird')).toBe('muted')
    expect(statusLabel('in_progress')).toBe('生成中')
  })

  it('任务类型兼容通用创建端的 *_generation 值', () => {
    expect(taskTypeLabel('image_generation')).toBe('绘图')
    expect(taskTypeLabel('music_generation')).toBe('音乐')
    expect(taskTypeLabel('video_generation')).toBe('视频')
  })

  it('仅 queued/in_progress 触发详情轮询', () => {
    expect(isActive('queued')).toBe(true)
    expect(isActive('in_progress')).toBe(true)
    expect(isActive('succeeded')).toBe(false)
    expect(isActive('failed')).toBe(false)
  })

  it('分转美元补零且实际费用优先于预估费用', () => {
    expect(centsToUSD(1240)).toBe('12.40')
    expect(centsToUSD(5)).toBe('0.05')
    expect(formatTaskCost({ estimated_cents: 500, actual_cents: 300, status: 'succeeded' })).toBe('$3.00')
    expect(formatTaskCost({ estimated_cents: 500, actual_cents: null, status: 'in_progress' })).toBe('预估 $5.00')
  })

  it('详情轮询单项失败时仍合并其他成功任务', async () => {
    const updated = await pollMediaTaskUpdates([1, 2], async (id) => {
      if (id === 1) throw new Error('单项暂时不可用')
      return {
        id,
        task_type: 'video_generation',
        status: 'succeeded',
        provider: 'http',
        request_id: `r-${id}`,
        estimated_cents: 100,
        progress: 100,
        created_at: '2026-07-12T00:00:00Z',
        updated_at: '2026-07-12T00:01:00Z',
      }
    })
    expect(updated).toHaveLength(1)
    expect(updated[0]).toMatchObject({ id: 2, status: 'succeeded' })
  })
})

describe('媒体创建请求组装', () => {
  it('图片任务锁定后端真实四字段，并把模型、Prompt、参数放进 input_params', () => {
    const request = buildMediaTaskRequest({
      ...DEFAULT_MEDIA_TASK_FORM,
      taskKind: 'image',
      model: ' gpt-image-1 ',
      prompt: ' 一只猫 ',
      parametersJSON: '{"size":"1024x1024","n":2}',
    }, ' web-123 ')
    expect(request).toEqual({
      request_id: 'web-123',
      task_type: 'image_generation',
      provider: 'http',
      input_params: { model: 'gpt-image-1', prompt: '一只猫', size: '1024x1024', n: 2 },
    })
    expect(request).not.toHaveProperty('model')
    expect(request).not.toHaveProperty('prompt')
  })

  it('音乐与视频使用各自 task_type，但继续走通用 http provider', () => {
    const base = { ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p' }
    expect(buildMediaTaskRequest({ ...base, taskKind: 'music' }, 'r1')).toMatchObject({ task_type: 'music_generation', provider: 'http' })
    expect(buildMediaTaskRequest({ ...base, taskKind: 'video' }, 'r2')).toMatchObject({ task_type: 'video_generation', provider: 'http' })
  })

  it('空必填、坏 JSON、非对象与固定字段覆盖都在发请求前拒绝', () => {
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: '', prompt: 'p' }, 'r')).toThrow('请填写模型')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: '' }, 'r')).toThrow('请填写 Prompt')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p', parametersJSON: '{' }, 'r')).toThrow('格式无效')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p', parametersJSON: '[]' }, 'r')).toThrow('必须是对象')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p', parametersJSON: '{"model":"shadow"}' }, 'r')).toThrow('不能覆盖 model')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p', parametersJSON: '{"prompt":"shadow"}' }, 'r')).toThrow('不能覆盖 prompt')
    expect(() => buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, model: 'm', prompt: 'p' }, ' ')).toThrow('缺少请求 ID')
  })
})

describe('媒体任务 API 请求契约', () => {
  beforeEach(() => {
    apiClient.get.mockReset().mockResolvedValue({ items: [] })
    apiClient.send.mockReset().mockResolvedValue({ task_id: 9, status: 'queued' })
  })

  it('列表与详情锁定 GET 路径及查询参数', async () => {
    const controller = new AbortController()
    await listMediaTasks(80, controller.signal)
    await getMediaTask(42, controller.signal)
    expect(apiClient.get.mock.calls).toEqual([
      ['/v1/media-tasks', { query: { limit: 80 }, signal: controller.signal }],
      ['/v1/media-tasks/42', { signal: controller.signal }],
    ])
  })

  it('创建锁定 POST /v1/media-tasks 与关键 body，且依赖统一 session API', async () => {
    const body = buildMediaTaskRequest({ ...DEFAULT_MEDIA_TASK_FORM, taskKind: 'video', model: 'veo-3', prompt: '海浪' }, 'web-9')
    const controller = new AbortController()
    await createMediaTask(body, controller.signal)
    expect(apiClient.send).toHaveBeenCalledWith('POST', '/v1/media-tasks', body, { signal: controller.signal })
    expect(body).toMatchObject({
      request_id: 'web-9', task_type: 'video_generation', provider: 'http',
      input_params: { model: 'veo-3', prompt: '海浪' },
    })
  })
})
