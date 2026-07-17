import type { CSSProperties, ReactNode } from 'react'
import { Link } from 'react-router-dom'

export interface StatCardProps {
  label: string
  value: string
  valueTitle?: string
  hint?: string
  tone?: 'default' | 'danger' | 'warn' | 'ok'
  to?: string
  icon?: ReactNode
  sparkline?: ReactNode
}

export function StatCard({ label, value, valueTitle, hint, tone = 'default', to, icon, sparkline }: StatCardProps) {
  const content = (
    <>
      <span style={labelStyle}>
        {icon && <span aria-hidden="true" style={iconStyle}>{icon}</span>}
        {label}
      </span>
      <strong title={valueTitle ?? value} data-tone={tone} style={{ ...valueStyle, color: toneColors[tone] }}>{value}</strong>
      {hint && <span style={hintStyle}>{hint}</span>}
      {sparkline && <span style={sparklineStyle}>{sparkline}</span>}
    </>
  )

  return to ? <Link to={to} style={cardStyle}>{content}</Link> : <div style={cardStyle}>{content}</div>
}

const cardStyle: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-1)', minWidth: 0, padding: 'var(--hk-space-3) var(--hk-space-4)', border: '1px solid var(--hk-line)', borderRadius: 'var(--hk-radius-lg)', background: 'var(--hk-surface)', boxShadow: 'var(--hk-shadow-1)', color: 'inherit', textDecoration: 'none' }
const labelStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-500)', fontSize: 12, lineHeight: 1.4 }
const iconStyle: CSSProperties = { width: 24, height: 24, display: 'grid', placeItems: 'center', flex: 'none', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-primary-50)', color: 'var(--hk-primary-600)', fontSize: 12 }
const valueStyle: CSSProperties = { display: 'block', minWidth: 0, maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', fontSize: 'clamp(16px, 2vw, 20px)', lineHeight: 1.3, fontWeight: 700 }
const hintStyle: CSSProperties = { color: 'var(--hk-ink-500)', fontSize: 12, lineHeight: 1.5 }
const sparklineStyle: CSSProperties = { display: 'block', minHeight: 30, marginTop: 'auto', paddingTop: 'var(--hk-space-1)' }
const toneColors: Record<NonNullable<StatCardProps['tone']>, string> = {
  default: 'var(--hk-ink-900)',
  danger: 'var(--hk-danger)',
  warn: 'var(--hk-warn)',
  ok: 'var(--hk-success)',
}
