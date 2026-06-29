import { useEffect, useMemo, useState } from 'react'
import {
  bannerTone,
  filterDismissed,
  listUserAnnouncements,
  persistDismissed,
  readDismissed,
  visibleAnnouncements,
} from './announcements'
import type { BannerTone, UserAnnouncement, UserAnnouncementListResponse } from './announcements'

/*
 * 概览页顶部用户公告横幅。拉 /v1/announcements(session 鉴权),展示当前生效公告。
 * 设计取向:
 *   - 纯展示 + 可关闭,无任何改动型动作(用户读取侧),故无 window.confirm;
 *   - 端点不可用/无 session/无公告 → 整体不渲染(返回 null),不打扰概览主内容,不显示报错条;
 *   - 关闭状态记在 localStorage(按公告 id),刷新后保持已关闭;
 *   - 排序/过滤/窗口判定全在 announcements.ts 纯逻辑里(已单测),组件只做取数与渲染。
 */

const TONE_STYLE: Record<BannerTone, { bg: string; border: string; accent: string }> = {
  danger: { bg: 'var(--hk-danger-50, #fef2f2)', border: 'var(--hk-danger)', accent: 'var(--hk-danger)' },
  warn: { bg: 'var(--hk-warn-50, #fffbeb)', border: 'var(--hk-warn)', accent: 'var(--hk-warn)' },
  info: { bg: 'var(--hk-surface-sunken)', border: 'var(--hk-line)', accent: 'var(--hk-primary-500)' },
}

export function AnnouncementBanner() {
  const [resp, setResp] = useState<UserAnnouncementListResponse | null>(null)
  const [state, setState] = useState<'loading' | 'ok' | 'fail'>('loading')
  // 已关闭 id 集合(初始从 localStorage 读)。
  const [dismissed, setDismissed] = useState<Set<number>>(() =>
    readDismissed(typeof window !== 'undefined' ? window.localStorage : null),
  )

  useEffect(() => {
    const ctrl = new AbortController()
    listUserAnnouncements(ctrl.signal)
      .then((r) => {
        if (ctrl.signal.aborted) return
        setResp(r)
        setState('ok')
      })
      .catch(() => {
        // 公告是锦上添花:任何失败(401/未配置/网络)都静默降级,横幅整体不渲染。
        if (!ctrl.signal.aborted) setState('fail')
      })
    return () => ctrl.abort()
  }, [])

  // 取数完成后按当前时刻过滤+排序;Date.now 在 render 时取一次即可(横幅无需秒级刷新)。
  const items = useMemo(() => {
    if (state !== 'ok') return []
    const visible = visibleAnnouncements(resp, Date.now())
    return filterDismissed(visible, dismissed)
  }, [state, resp, dismissed])

  if (state !== 'ok' || items.length === 0) return null

  function handleDismiss(id: number) {
    setDismissed((prev) => persistDismissed(typeof window !== 'undefined' ? window.localStorage : null, prev, id))
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-3)' }}>
      {items.map((a) => (
        <BannerItem key={a.id} a={a} onDismiss={() => handleDismiss(a.id)} />
      ))}
    </div>
  )
}

function BannerItem({ a, onDismiss }: { a: UserAnnouncement; onDismiss: () => void }) {
  const tone = bannerTone(a.severity)
  const s = TONE_STYLE[tone]
  return (
    <section
      role="status"
      style={{
        display: 'flex',
        alignItems: 'flex-start',
        gap: 'var(--hk-space-3)',
        padding: 'var(--hk-space-3) var(--hk-space-4)',
        background: s.bg,
        border: `1px solid ${s.border}`,
        borderLeft: `4px solid ${s.accent}`,
        borderRadius: 'var(--hk-radius-md)',
      }}
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 4, flex: 1, minWidth: 0 }}>
        <strong style={{ fontSize: 14, color: 'var(--hk-ink-900)' }}>{a.title}</strong>
        {a.body && (
          <span style={{ fontSize: 13, color: 'var(--hk-ink-700)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
            {a.body}
          </span>
        )}
      </div>
      <button
        type="button"
        onClick={onDismiss}
        aria-label="关闭此公告"
        title="关闭"
        style={{
          flex: '0 0 auto',
          border: 'none',
          background: 'transparent',
          color: 'var(--hk-ink-500)',
          cursor: 'pointer',
          fontSize: 16,
          lineHeight: 1,
          padding: 2,
        }}
      >
        ×
      </button>
    </section>
  )
}
