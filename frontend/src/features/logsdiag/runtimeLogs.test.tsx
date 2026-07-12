import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'

const client = vi.hoisted(() => ({ get: vi.fn(), send: vi.fn() }))
vi.mock('../../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../../lib/api')>('../../lib/api')
  return { ...actual, apiGet: client.get, apiSend: client.send }
})

import { listRuntimeLogs } from './api'
import type { RuntimeLogRow } from './api'
import { appendOlderLogs, fmtAttrs, fmtLogTime, levelToneOf, mergeRuntimeLogs } from './runtimeLogs'
import { RuntimeLogsPanel } from './RuntimeLogsPanel'

function row(id: number, msg = 'm'): RuntimeLogRow {
  return { id, created_at: '2026-07-12T10:00:00Z', level: 'warn', component: 'c', message: msg, attrs: {} }
}

describe('mergeRuntimeLogs(轮询增量合并)', () => {
  it('按 id 去重、降序、封顶(变异:漏去重会重复行/漏排序会乱序)', () => {
    const merged = mergeRuntimeLogs([row(5), row(3)], [row(7), row(5), row(6)], 3)
    expect(merged.map((r) => r.id)).toEqual([7, 6, 5])
  })
  it('追加更旧页只补缺失 id', () => {
    const out = appendOlderLogs([row(5), row(4)], [row(4), row(2)])
    expect(out.map((r) => r.id)).toEqual([5, 4, 2])
  })
})

describe('格式化', () => {
  it('时间转本地紧凑形态;非法输入原样返回', () => {
    expect(fmtLogTime('not-a-date')).toBe('not-a-date')
    expect(fmtLogTime('2026-07-12T10:00:00Z')).toMatch(/^\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  })
  it('attrs 压缩为 k=v 单行;error 级别用 crit 色调', () => {
    expect(fmtAttrs({ a: 'x', n: 3 })).toBe('a=x n=3')
    expect(fmtAttrs({})).toBe('')
    expect(levelToneOf('error')).toBe('danger')
    expect(levelToneOf('warn')).toBe('warn')
  })
})

describe('runtime-logs API', () => {
  it('锁定路径与查询参数(变异:改错路径/漏过滤 → 红)', async () => {
    client.get.mockReset()
    client.get.mockResolvedValue({ items: [], next_before_id: 0 })
    await listRuntimeLogs({ level: 'error', request_id: 'r1', before_id: 9, limit: 50 })
    expect(client.get).toHaveBeenCalledWith('/v1/admin/ops/runtime-logs', {
      query: { level: 'error', request_id: 'r1', before_id: 9, limit: 50 },
      signal: undefined,
    })
  })
})

describe('渲染冒烟', () => {
  it('面板初始渲染含过滤控件与实时开关', () => {
    client.get.mockReset()
    client.get.mockResolvedValue({ items: [], next_before_id: 0 })
    const html = renderToStaticMarkup(<RuntimeLogsPanel />)
    expect(html).toContain('运行日志')
    expect(html).toContain('request_id')
    expect(html).toContain('实时:关')
    expect(html).toContain('全部级别')
  })
})
