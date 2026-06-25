import { describe, expect, it } from 'vitest'
import { countUnread, isUnread, severityLabel, severityTone } from './notifications'

describe('isUnread', () => {
  it('read_at 空(null/undefined/空串)=未读,有值=已读', () => {
    expect(isUnread({ read_at: null })).toBe(true)
    expect(isUnread({ read_at: undefined })).toBe(true)
    expect(isUnread({ read_at: '   ' })).toBe(true)
    expect(isUnread({ read_at: '2026-06-25T10:00:00Z' })).toBe(false)
  })
})

describe('countUnread', () => {
  it('只数未读', () => {
    // 判别核心:已读不计。变异(去 filter 直接 length)→ 得 3 而非 2 → RED。
    const items = [
      { read_at: null },
      { read_at: '2026-06-25T10:00:00Z' },
      { read_at: '' },
    ]
    expect(countUnread(items)).toBe(2)
  })
})

describe('severityTone', () => {
  it('三档严重度映射 + 兜底,大小写无关', () => {
    // 判别核心:critical 必须红。变异(critical→muted)→ RED。
    expect(severityTone('info')).toBe('info')
    expect(severityTone('WARNING')).toBe('warn')
    expect(severityTone('critical')).toBe('danger')
    expect(severityTone('weird')).toBe('muted')
  })
})

describe('severityLabel', () => {
  it('中文名映射', () => {
    expect(severityLabel('info')).toBe('通知')
    expect(severityLabel('warning')).toBe('提醒')
    expect(severityLabel('critical')).toBe('重要')
  })
})
