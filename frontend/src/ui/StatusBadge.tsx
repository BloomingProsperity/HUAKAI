import type { CSSProperties, ReactNode } from 'react'

/*
 * 状态徽章。用语义色把账号健康/运行态做成一眼可辨的胶囊。色值只引设计 token。
 */
export type BadgeTone = 'ok' | 'warn' | 'danger' | 'muted' | 'info'

const TONE_STYLE: Record<BadgeTone, CSSProperties> = {
  ok: { color: 'var(--hk-primary-600)', background: 'var(--hk-primary-50)', borderColor: 'var(--hk-primary-100)' },
  warn: { color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)', borderColor: 'var(--hk-warn-soft)' },
  danger: { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)', borderColor: 'var(--hk-danger-soft)' },
  info: { color: '#235a82', background: '#e8f1f8', borderColor: '#cfe0ee' },
  muted: { color: 'var(--hk-ink-500)', background: 'var(--hk-surface-sunken)', borderColor: 'var(--hk-line)' },
}

export function StatusBadge({ tone, children }: { tone: BadgeTone; children: ReactNode }) {
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 4,
        fontSize: 12,
        lineHeight: 1.4,
        padding: '1px 8px',
        borderRadius: 'var(--hk-radius-pill)',
        border: '1px solid',
        whiteSpace: 'nowrap',
        ...TONE_STYLE[tone],
      }}
    >
      {children}
    </span>
  )
}

/** 把账号健康态映射成徽章语气 + 中文标签。 */
export function healthTone(healthState: string): BadgeTone {
  switch (healthState) {
    case 'active':
    case 'healthy':
      return 'ok'
    case 'cooling_down':
    case 'rate_limited':
    case 'overloaded':
    case 'degraded':
      return 'warn'
    case 'error':
    case 'disabled':
    case 'unhealthy':
      return 'danger'
    default:
      return 'muted'
  }
}
