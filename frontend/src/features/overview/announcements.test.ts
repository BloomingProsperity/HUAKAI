import { describe, expect, it } from 'vitest'
import {
  bannerTone,
  filterDismissed,
  isAnnouncementVisible,
  persistDismissed,
  readDismissed,
  visibleAnnouncements,
} from './announcements'
import type { UserAnnouncement, UserAnnouncementListResponse } from './announcements'

const NOW = Date.parse('2026-06-29T12:00:00Z')

function ann(over: Partial<UserAnnouncement>): UserAnnouncement {
  return {
    id: 1,
    tenant_id: 7,
    title: '标题',
    body: '正文',
    severity: 'info',
    active: true,
    published_at: '2026-06-28T00:00:00Z',
    expires_at: null,
    created_by_admin: null,
    created_at: '2026-06-28T00:00:00Z',
    updated_at: '2026-06-28T00:00:00Z',
    ...over,
  }
}

function resp(items: UserAnnouncement[]): UserAnnouncementListResponse {
  return { object: 'announcement_list', items, limit: 50, offset: 0 }
}

describe('isAnnouncementVisible', () => {
  it('已发布且未过期 → 可见', () => {
    expect(isAnnouncementVisible(ann({}), NOW)).toBe(true)
  })

  it('published_at 在未来 → 不可见', () => {
    // 判别核心:未来发布必须排除。若实现漏了 pub>now 分支,此断言会红。
    expect(isAnnouncementVisible(ann({ published_at: '2026-07-01T00:00:00Z' }), NOW)).toBe(false)
  })

  it('expires_at 已过 → 不可见', () => {
    // 判别核心:已过期必须排除。若实现把 <= 写成 < 或漏了过期分支,此断言会红。
    expect(isAnnouncementVisible(ann({ expires_at: '2026-06-29T11:59:59Z' }), NOW)).toBe(false)
  })

  it('expires_at 未来 → 仍可见', () => {
    expect(isAnnouncementVisible(ann({ expires_at: '2026-07-01T00:00:00Z' }), NOW)).toBe(true)
  })

  it('published_at 空串(零值)按已发布兜底放行', () => {
    expect(isAnnouncementVisible(ann({ published_at: '' }), NOW)).toBe(true)
  })
})

describe('visibleAnnouncements', () => {
  it('过滤窗口外并按 severity 权重排序(critical 在最前)', () => {
    const info = ann({ id: 1, severity: 'info', published_at: '2026-06-28T10:00:00Z' })
    const critical = ann({ id: 2, severity: 'critical', published_at: '2026-06-20T00:00:00Z' })
    const future = ann({ id: 3, severity: 'critical', published_at: '2026-07-05T00:00:00Z' })
    const out = visibleAnnouncements(resp([info, critical, future]), NOW)
    // 判别核心①:未来公告(id=3)被过滤掉,只剩 2 条。
    expect(out.map((a) => a.id)).toEqual([2, 1])
    // 判别核心②:critical(id=2)排在 info(id=1)之前,即便它发布更早。
    // 若实现仅按 published_at 排序(忽略 severity),id=1 会排前面,此断言会红。
    expect(out[0].severity).toBe('critical')
  })

  it('同 severity 时按 published_at 降序(新的在前)', () => {
    const older = ann({ id: 1, severity: 'warning', published_at: '2026-06-20T00:00:00Z' })
    const newer = ann({ id: 2, severity: 'warning', published_at: '2026-06-28T00:00:00Z' })
    const out = visibleAnnouncements(resp([older, newer]), NOW)
    // 判别核心:同权重下新的(id=2)在前。若降序写成升序,顺序会反,此断言会红。
    expect(out.map((a) => a.id)).toEqual([2, 1])
  })

  it('null 响应返回空数组(不抛错)', () => {
    expect(visibleAnnouncements(null, NOW)).toEqual([])
  })
})

describe('bannerTone', () => {
  it('critical→danger、warning→warn、其余→info', () => {
    expect(bannerTone('critical')).toBe('danger')
    expect(bannerTone('warning')).toBe('warn')
    expect(bannerTone('info')).toBe('info')
    // 未知 severity 回退中性 info
    expect(bannerTone('whatever')).toBe('info')
  })
})

describe('dismiss 本地记忆', () => {
  function memStore(): Storage & { _map: Map<string, string> } {
    const map = new Map<string, string>()
    return {
      _map: map,
      getItem: (k: string) => map.get(k) ?? null,
      setItem: (k: string, v: string) => void map.set(k, v),
      removeItem: (k: string) => void map.delete(k),
      clear: () => map.clear(),
      key: () => null,
      length: 0,
    } as unknown as Storage & { _map: Map<string, string> }
  }

  it('persist 后 read 能取回同一个 id(往返一致)', () => {
    const st = memStore()
    const after = persistDismissed(st, new Set<number>(), 42)
    // 判别核心①:返回集合含新 id。
    expect(after.has(42)).toBe(true)
    // 判别核心②:回写后另起一次 read 仍能取回(序列化往返正确)。
    expect(readDismissed(st).has(42)).toBe(true)
  })

  it('persist 不原地修改原集合(返回新集合)', () => {
    const orig = new Set<number>([1])
    const next = persistDismissed(null, orig, 2)
    // 判别核心:原集合不被污染(仍只有 1),新集合含 1、2。
    expect([...orig]).toEqual([1])
    expect(next.has(1)).toBe(true)
    expect(next.has(2)).toBe(true)
  })

  it('readDismissed 对损坏内容回退空集(不抛错)', () => {
    const st = memStore()
    st.setItem('hk.overview.announcements.dismissed', '{not json')
    expect(readDismissed(st).size).toBe(0)
    // 非数组 JSON 也回退空集
    st.setItem('hk.overview.announcements.dismissed', '{"a":1}')
    expect(readDismissed(st).size).toBe(0)
  })

  it('filterDismissed 剔除已关闭 id', () => {
    const items = [ann({ id: 1 }), ann({ id: 2 }), ann({ id: 3 })]
    const out = filterDismissed(items, new Set<number>([2]))
    // 判别核心:id=2 被滤掉。若实现把 has 取反或漏过滤,此断言会红。
    expect(out.map((a) => a.id)).toEqual([1, 3])
  })
})
