import type { CSSProperties, ReactNode } from 'react'
import { Link } from 'react-router-dom'

export type ResourceBadgeTone = 'ok' | 'warn' | 'danger' | 'neutral'

export interface ResourceBadge {
  label: string
  value: string
  tone?: ResourceBadgeTone
}

export interface ResourceCardProps {
  title: string
  value: string
  icon?: ReactNode
  badges?: ResourceBadge[]
  action: { label: string; to: string }
}

export function ResourceCard({ title, value, icon, badges = [], action }: ResourceCardProps) {
  return (
    <article style={cardStyle}>
      <div style={titleRowStyle}>{icon && <span aria-hidden="true" style={iconStyle}>{icon}</span>}<span>{title}</span></div>
      <strong style={valueStyle}>{value}</strong>
      {badges.length > 0 && <div style={badgesStyle}>{badges.slice(0, 2).map((badge) => <span key={badge.label} data-tone={badge.tone ?? 'neutral'} style={{ ...badgeStyle, ...BADGE_TONES[badge.tone ?? 'neutral'] }}>{badge.label} {badge.value}</span>)}</div>}
      <Link to={action.to} style={actionStyle}>{action.label}</Link>
    </article>
  )
}

const BADGE_TONES: Record<ResourceBadgeTone, CSSProperties> = {
  ok: { color: 'var(--hk-success)', background: 'var(--hk-success-soft)' },
  warn: { color: 'var(--hk-warn)', background: 'var(--hk-warn-soft)' },
  danger: { color: 'var(--hk-danger)', background: 'var(--hk-danger-soft)' },
  neutral: { color: 'var(--hk-ink-500)', background: 'var(--hk-line-soft)' },
}
const cardStyle: CSSProperties = { display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 176, padding: 'var(--hk-space-3)', border: '1px solid var(--hk-line-soft)', borderRadius: 'var(--hk-radius-md)', background: 'var(--hk-surface)' }
const titleRowStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-500)', fontSize: 12 }
const iconStyle: CSSProperties = { width: 24, height: 24, display: 'grid', placeItems: 'center', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-primary-50)', color: 'var(--hk-primary-600)' }
const valueStyle: CSSProperties = { marginTop: 'var(--hk-space-3)', color: 'var(--hk-ink-900)', fontSize: 25, lineHeight: 1.2 }
const badgesStyle: CSSProperties = { display: 'flex', flexWrap: 'wrap', gap: 'var(--hk-space-1)', marginTop: 'var(--hk-space-2)' }
const badgeStyle: CSSProperties = { padding: '2px 7px', borderRadius: 'var(--hk-radius-pill)', fontSize: 10, fontWeight: 600 }
const actionStyle: CSSProperties = { display: 'flex', justifyContent: 'center', marginTop: 'auto', padding: '7px var(--hk-space-2)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-sm)', color: 'var(--hk-primary-600)', fontSize: 12, fontWeight: 600 }
