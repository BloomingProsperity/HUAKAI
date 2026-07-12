import { createElement } from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiClient = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))

vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: apiClient.get, apiSend: apiClient.send }
})

import {
  getMidjourneyImageSeed,
  getMidjourneyTask,
  getSunoTask,
  getSunoTaskByQuery,
  getVideoTask,
  getVideoTaskByQuery,
  listMidjourneyTasks,
  listVideoTasks,
  submitMidjourneySwap,
  submitMidjourneyTask,
  submitSunoAction,
  submitSunoTask,
  submitVideoTask,
} from './api'
import {
  buildMidjourneySubmitRequest,
  buildMidjourneySwapRequest,
  buildSunoActionRequest,
  buildSunoSubmitRequest,
  buildVideoSubmitRequest,
  DEFAULT_MIDJOURNEY_FORM,
  DEFAULT_SUNO_ACTION_FORM,
  DEFAULT_SUNO_FORM,
  DEFAULT_VIDEO_FORM,
  extractMediaResources,
  filterMediaTasksByProvider,
  formatMediaResult,
  mergeMediaTasks,
  normalizeSunoAction,
  parseTaskID,
  parseTaskLimit,
} from './compatibility'
import type { MediaTask } from './types'
import { COMPATIBILITY_POLL_INTERVAL_MS, CompatibilityTaskPanel } from './CompatibilityTaskPanel'
import { MediaTasksPage } from './MediaTasksPage'

describe('Midjourney 表单到请求体', () => {
  it('imagine 精确保留 Prompt、模式、幂等键与动作参数', () => {
    const request = buildMidjourneySubmitRequest({
      ...DEFAULT_MIDJOURNEY_FORM,
      prompt: '  雨夜里的控制室  ',
      botType: 'NIJI_JOURNEY',
      notifyHook: ' https://hook.test/mj ',
      customID: ' button-7 ',
      commandAction: ' UPSCALE ',
      state: ' ready ',
      indexJSON: '{"slot":2}',
    }, ' mj-web-1 ')
    expect(request).toEqual({
      action: 'imagine',
      body: {
        request_id: 'mj-web-1',
        prompt: '雨夜里的控制室',
        customId: 'button-7',
        botType: 'NIJI_JOURNEY',
        notifyHook: 'https://hook.test/mj',
        action: 'UPSCALE',
        state: 'ready',
        index: { slot: 2 },
      },
    })
  })

  it('describe 组装逐行图片数组，并在空必填或坏 Base64 时拒绝', () => {
    const request = buildMidjourneySubmitRequest({
      ...DEFAULT_MIDJOURNEY_FORM,
      action: 'describe',
      prompt: '',
      base64Images: 'YWJj\ndata:image/png;base64,ZA==',
    }, 'mj-web-2')
    expect(request.body).toEqual({
      request_id: 'mj-web-2',
      botType: 'MID_JOURNEY',
      base64Array: ['YWJj', 'data:image/png;base64,ZA=='],
    })
    expect(() => buildMidjourneySubmitRequest(DEFAULT_MIDJOURNEY_FORM, 'r')).toThrow('请填写 Prompt')
    expect(() => buildMidjourneySubmitRequest({
      ...DEFAULT_MIDJOURNEY_FORM,
      action: 'describe',
      base64Images: 'not@@base64',
    }, 'r')).toThrow('不是有效 Base64')
    expect(() => buildMidjourneySubmitRequest({
      ...DEFAULT_MIDJOURNEY_FORM,
      action: 'describe',
      base64Images: 'a=',
    }, 'r')).toThrow('不是有效 Base64')
  })

  it('换脸严格发送双图，缺图和坏图均不会进入网络层', () => {
    expect(buildMidjourneySwapRequest({ sourceBase64: 'c291cmNl', targetBase64: 'dGFyZ2V0' }, 'swap-1')).toEqual({
      request_id: 'swap-1',
      sourceBase64: 'c291cmNl',
      targetBase64: 'dGFyZ2V0',
    })
    expect(() => buildMidjourneySwapRequest({ sourceBase64: '', targetBase64: 'dA==' }, 'r')).toThrow('请填写源图片')
    expect(() => buildMidjourneySwapRequest({ sourceBase64: 'cw==', targetBase64: '%%%bad' }, 'r')).toThrow('目标图片不是有效 Base64')
  })
})

describe('Suno 表单到请求体', () => {
  it('普通提交保留 false 器乐开关，并映射歌词、风格、标题与两种模型字段', () => {
    expect(buildSunoSubmitRequest({
      ...DEFAULT_SUNO_FORM,
      lyrics: ' Verse one ',
      style: ' synthpop ',
      title: ' Night Relay ',
      mv: 'chirp-v4',
      modelVersion: 'v4.5',
      notifyHook: 'https://hook.test/suno',
      instrumental: false,
    }, ' suno-1 ')).toEqual({
      request_id: 'suno-1',
      make_instrumental: false,
      custom_mode: false,
      prompt: 'Verse one',
      tags: 'synthpop',
      title: 'Night Relay',
      mv: 'chirp-v4',
      model_version: 'v4.5',
      notify_hook: 'https://hook.test/suno',
    })
  })

  it('自定义模式改用 input，空歌词在请求前失败', () => {
    expect(buildSunoSubmitRequest({
      ...DEFAULT_SUNO_FORM,
      customMode: true,
      instrumental: true,
      lyrics: 'Chorus hook',
      description: 'bright chorus',
    }, 'suno-2')).toEqual({
      request_id: 'suno-2',
      make_instrumental: true,
      custom_mode: true,
      input: 'Chorus hook',
      gpt_description_prompt: 'bright chorus',
    })
    expect(() => buildSunoSubmitRequest(DEFAULT_SUNO_FORM, 'r')).toThrow('请填写歌词/描述')
  })

  it('动作端按 handler 字符规则校验，允许只带续写片段', () => {
    expect(buildSunoActionRequest({
      ...DEFAULT_SUNO_ACTION_FORM,
      action: 'extend-v2',
      continueClipID: 'clip-7',
      continueAt: '12.5',
    }, 'act-1')).toEqual({
      action: 'extend-v2',
      body: {
        request_id: 'act-1',
        make_instrumental: false,
        custom_mode: false,
        continue_clip_id: 'clip-7',
        continue_at: 12.5,
      },
    })
    expect(() => normalizeSunoAction('')).toThrow('请填写动作')
    expect(() => normalizeSunoAction('../extend')).toThrow('只能包含')
  })
})

describe('视频表单到请求体', () => {
  it('把模型、Prompt、时长与数值选项组装成后端原始 JSON 类型', () => {
    expect(buildVideoSubmitRequest({
      ...DEFAULT_VIDEO_FORM,
      model: ' kling-v1 ',
      prompt: ' 海边延时摄影 ',
      duration: '6.5',
      image: 'https://img.test/start.png',
      width: '1280',
      height: '720',
      fps: '24',
      seed: '-7',
      count: '2',
      responseFormat: 'url',
    }, ' video-1 ')).toEqual({
      request_id: 'video-1',
      model: 'kling-v1',
      prompt: '海边延时摄影',
      duration: 6.5,
      image: 'https://img.test/start.png',
      width: 1280,
      height: 720,
      fps: 24,
      seed: -7,
      n: 2,
      response_format: 'url',
    })
  })

  it('空必填、非正时长与坏 Base64 参考图均被拒绝', () => {
    expect(() => buildVideoSubmitRequest({ ...DEFAULT_VIDEO_FORM, model: '', prompt: 'p' }, 'r')).toThrow('请填写模型')
    expect(() => buildVideoSubmitRequest({ ...DEFAULT_VIDEO_FORM, model: 'm', prompt: '' }, 'r')).toThrow('请填写 Prompt')
    expect(() => buildVideoSubmitRequest({ ...DEFAULT_VIDEO_FORM, model: 'm', prompt: 'p', duration: '0' }, 'r')).toThrow('时长必须大于 0')
    expect(() => buildVideoSubmitRequest({ ...DEFAULT_VIDEO_FORM, model: 'm', prompt: 'p', image: '@@@' }, 'r')).toThrow('参考图不是有效 Base64')
  })
})

describe('兼容查询、轮询合并与媒体展示', () => {
  it('任务 ID 和列表数量拒绝越界值', () => {
    expect(parseTaskID('42')).toBe(42)
    expect(() => parseTaskID('0')).toThrow('正整数')
    expect(() => parseTaskID('1.5')).toThrow('正整数')
    expect(parseTaskLimit('200')).toBe(200)
    expect(() => parseTaskLimit('201')).toThrow('1–200')
  })

  it('部分轮询成功只更新成功项，失败项保留原状态', () => {
    expect(COMPATIBILITY_POLL_INTERVAL_MS).toBe(5000)
    const current = [task(1, 'in_progress', 10), task(2, 'in_progress', 20)]
    const merged = mergeMediaTasks(current, [task(2, 'succeeded', 100)])
    expect(merged.map((item) => ({ id: item.id, status: item.status, progress: item.progress }))).toEqual([
      { id: 1, status: 'in_progress', progress: 10 },
      { id: 2, status: 'succeeded', progress: 100 },
    ])
  })

  it('兼容列表端返回用户全量任务时只保留当前族', () => {
    const midjourney = { ...task(1, 'succeeded', 100), provider: 'midjourney' }
    const video = { ...task(2, 'succeeded', 100), provider: 'video' }
    expect(filterMediaTasksByProvider([midjourney, video], 'midjourney')).toEqual([midjourney])
    expect(filterMediaTasksByProvider([midjourney, video], 'video')).toEqual([video])
  })

  it('同时识别 URL、data URL 与裸 Base64，并拒绝危险协议', () => {
    const result = {
      images: [
        { url: 'https://cdn.test/a.png' },
        { b64_json: 'YWJj' },
        { image_url: 'javascript:alert(1)' },
      ],
      image: 'aGVsbG8=',
      audio_url: 'https://cdn.test/song.mp3',
      video: { base64: 'ZA==' },
      preview: 'data:image/webp;base64,ZWY=',
      unsafe_svg: 'data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=',
    }
    expect(extractMediaResources(result, 'image')).toEqual([
      { kind: 'image', src: 'https://cdn.test/a.png', label: 'url' },
      { kind: 'image', src: 'data:image/png;base64,YWJj', label: 'b64_json' },
      { kind: 'image', src: 'data:image/png;base64,aGVsbG8=', label: 'image' },
      { kind: 'audio', src: 'https://cdn.test/song.mp3', label: 'audio_url' },
      { kind: 'video', src: 'data:video/mp4;base64,ZA==', label: 'base64' },
      { kind: 'image', src: 'data:image/webp;base64,ZWY=', label: 'preview' },
    ])
  })

  it('结果 JSON 隐去超长 Base64，仍保留普通字段供排障', () => {
    const text = formatMediaResult({ status: 'ok', b64_json: 'a'.repeat(120) })
    expect(text).toContain('"status": "ok"')
    expect(text).toContain('[base64 120 字符]')
    expect(text).not.toContain('a'.repeat(120))
  })

  it('实际结果面板按资源类型输出图片、音频播放器与视频播放器', () => {
    const mediaTask = task(9, 'succeeded', 100)
    mediaTask.result = {
      image_url: 'https://cdn.test/a.png',
      audio_url: 'https://cdn.test/a.mp3',
      video_url: 'data:video/mp4;base64,ZA==',
    }
    const html = renderToStaticMarkup(createElement(CompatibilityTaskPanel, {
      title: '结果',
      tasks: [mediaTask],
      preferredKind: 'video',
    }))
    expect(html).toContain('<img')
    expect(html).toContain('<audio controls=""')
    expect(html).toContain('<video controls=""')
    expect(html).toContain('任务 #9')
    expect(html).toContain('100%')
  })

  it('媒体页把总览和三族专属台聚合进同一 hk-seg', () => {
    const html = renderToStaticMarkup(createElement(MediaTasksPage))
    expect(html).toContain('class="hk-seg"')
    expect(html).toContain('任务总览')
    expect(html).toContain('Midjourney')
    expect(html).toContain('Suno')
    expect(html).toContain('>视频<')
  })
})

describe('三族兼容 API 契约', () => {
  beforeEach(() => {
    apiClient.get.mockReset().mockResolvedValue({ id: 7, status: 'queued' })
    apiClient.send.mockReset().mockResolvedValue({ task_id: 7, status: 'queued' })
  })

  it('Midjourney 五端点锁定真实方法、路径与 body', async () => {
    const signal = new AbortController().signal
    const submitBody = { request_id: 'm1', prompt: 'p' }
    const swapBody = { request_id: 'm2', sourceBase64: 'cw==', targetBase64: 'dA==' }
    await submitMidjourneyTask('imagine', submitBody, signal)
    await submitMidjourneySwap(swapBody, signal)
    await getMidjourneyTask(31, signal)
    await getMidjourneyImageSeed(32, signal)
    await listMidjourneyTasks(25, signal)
    expect(apiClient.send.mock.calls).toEqual([
      ['POST', '/mj/submit/imagine', submitBody, { signal }],
      ['POST', '/mj/insight-face/swap', swapBody, { signal }],
      ['POST', '/mj/task/list-by-condition', { limit: 25 }, { signal }],
    ])
    expect(apiClient.get.mock.calls).toEqual([
      ['/mj/task/31/fetch', { signal }],
      ['/mj/task/32/image-seed', { signal }],
    ])
  })

  it('Suno 四端点锁定普通/动作提交与 path/query 查询', async () => {
    const signal = new AbortController().signal
    const body = { request_id: 's1', prompt: 'p', make_instrumental: false, custom_mode: false }
    await submitSunoTask(body, signal)
    await submitSunoAction('extend-v2', body, signal)
    await getSunoTask(41, signal)
    await getSunoTaskByQuery(42, signal)
    expect(apiClient.send.mock.calls).toEqual([
      ['POST', '/suno/submit', body, { signal }],
      ['POST', '/suno/submit/extend-v2', body, { signal }],
    ])
    expect(apiClient.get.mock.calls).toEqual([
      ['/suno/fetch/41', { signal }],
      ['/suno/fetch', { query: { id: 42 }, signal }],
    ])
  })

  it('视频三端点覆盖提交、单项 path/query 与无 id 列表分支', async () => {
    const signal = new AbortController().signal
    const body = { request_id: 'v1', model: 'veo', prompt: 'p', duration: 5 }
    await submitVideoTask(body, signal)
    await getVideoTask(51, signal)
    await getVideoTaskByQuery(52, signal)
    await listVideoTasks(60, signal)
    expect(apiClient.send).toHaveBeenCalledWith('POST', '/video/submit', body, { signal })
    expect(apiClient.get.mock.calls).toEqual([
      ['/video/fetch/51', { signal }],
      ['/video/fetch', { query: { id: 52 }, signal }],
      ['/video/fetch', { query: { limit: 60 }, signal }],
    ])
  })
})

function task(id: number, status: string, progress: number): MediaTask {
  return {
    id,
    task_type: 'video_generate',
    status,
    provider: 'video',
    request_id: `r-${id}`,
    estimated_cents: 100,
    progress,
    created_at: '2026-07-12T00:00:00Z',
    updated_at: '2026-07-12T00:01:00Z',
  }
}
