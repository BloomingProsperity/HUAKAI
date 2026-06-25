import { describe, expect, it } from 'vitest'
import { centsToUSD, formatTaskCost, isActive, statusLabel, statusTone, taskTypeLabel } from './mediatasks'

describe('statusTone', () => {
  it('状态映射配色,大小写无关', () => {
    // 判别核心:failed/expired 必须红。变异(failed→muted)→ RED。
    expect(statusTone('succeeded')).toBe('ok')
    expect(statusTone('IN_PROGRESS')).toBe('warn')
    expect(statusTone('failed')).toBe('danger')
    expect(statusTone('expired')).toBe('danger')
    expect(statusTone('queued')).toBe('info')
    expect(statusTone('weird')).toBe('muted')
  })
})

describe('statusLabel / taskTypeLabel', () => {
  it('中文名映射', () => {
    expect(statusLabel('in_progress')).toBe('生成中')
    expect(statusLabel('succeeded')).toBe('已完成')
    expect(taskTypeLabel('image')).toBe('绘图')
    expect(taskTypeLabel('video')).toBe('视频')
  })
})

describe('isActive', () => {
  it('仅 queued/in_progress 算进行中(决定是否轮询)', () => {
    // 判别核心:succeeded 不是进行中。变异(去掉判断恒 true)→ 已完成也被当进行中 → RED。
    expect(isActive('queued')).toBe(true)
    expect(isActive('in_progress')).toBe(true)
    expect(isActive('succeeded')).toBe(false)
    expect(isActive('failed')).toBe(false)
  })
})

describe('centsToUSD', () => {
  it('分→元补零,整数运算', () => {
    // 判别核心:补零。变异(去 padStart)→ 5 分得 "0.5" 而非 "0.05" → RED。
    expect(centsToUSD(1240)).toBe('12.40')
    expect(centsToUSD(5)).toBe('0.05')
    expect(centsToUSD(0)).toBe('0.00')
  })
})

describe('formatTaskCost', () => {
  it('有实际扣费用实际值;否则展示预估并加"预估"前缀', () => {
    // 判别核心:有 actual_cents 时必须用实际值且无"预估"字样。
    // 变异(无视 actual 恒用 estimated)→ 实际 300 的任务显示预估 500 → RED。
    expect(formatTaskCost({ estimated_cents: 500, actual_cents: 300, status: 'succeeded' })).toBe('$3.00')
    expect(formatTaskCost({ estimated_cents: 500, actual_cents: null, status: 'in_progress' })).toBe('预估 $5.00')
    expect(formatTaskCost({ estimated_cents: 500, actual_cents: undefined, status: 'queued' })).toBe('预估 $5.00')
  })
})
