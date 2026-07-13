import type { CSSProperties } from 'react'
import { Link } from 'react-router-dom'

export type EmptyStateTone = 'neutral' | 'positive' | 'unavailable'

export interface EmptyStateProps {
  icon?: string
  title: string
  hint?: string
  tone?: EmptyStateTone
  action?: { label: string; to?: string; onClick?: () => void }
  secondaryAction?: { label: string; to: string }
}

const TONE_STYLE: Record<EmptyStateTone, CSSProperties> = {
  neutral: { background: 'var(--hk-surface-sunken)', color: 'var(--hk-ink-500)' },
  positive: { background: 'var(--hk-primary-50)', color: 'var(--hk-primary-600)' },
  unavailable: { background: 'var(--hk-danger-soft)', color: 'var(--hk-danger)' },
}

/* 紧凑空态用于解释当前状态，并在确有下一步时提供明确出口。 */
export function EmptyState({ icon = '○', title, hint, tone = 'neutral', action, secondaryAction }: EmptyStateProps) {
  const hasActions = Boolean(action || secondaryAction)

  return (
    <section style={wrap}>
      <span aria-hidden="true" data-tone={tone} style={{ ...iconStyle, ...TONE_STYLE[tone] }}>
        {icon}
      </span>
      <h3 style={titleStyle}>{title}</h3>
      {hint && <p style={hintStyle}>{hint}</p>}
      {hasActions && (
        <div data-testid="empty-state-actions" style={actionsStyle}>
          {action?.to ? (
            <Link to={action.to} style={primaryActionStyle}>{action.label}</Link>
          ) : action ? (
            <button type="button" onClick={action.onClick} style={primaryActionStyle}>{action.label}</button>
          ) : null}
          {secondaryAction && (
            <Link to={secondaryAction.to} style={secondaryActionStyle}>{secondaryAction.label}</Link>
          )}
        </div>
      )}
    </section>
  )
}

const wrap: CSSProperties = { display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 'var(--hk-space-2)', padding: 'var(--hk-space-6) var(--hk-space-4)', textAlign: 'center' }
const iconStyle: CSSProperties = { width: 36, height: 36, display: 'grid', placeItems: 'center', borderRadius: 'var(--hk-radius-pill)', fontSize: 18, lineHeight: 1 }
const titleStyle: CSSProperties = { margin: 0, color: 'var(--hk-ink-900)', fontSize: 15, fontWeight: 600 }
const hintStyle: CSSProperties = { margin: 0, maxWidth: 420, color: 'var(--hk-ink-500)', fontSize: 13, lineHeight: 1.6 }
const actionsStyle: CSSProperties = { display: 'flex', flexWrap: 'wrap', justifyContent: 'center', gap: 'var(--hk-space-2)', marginTop: 'var(--hk-space-2)' }
const primaryActionStyle: CSSProperties = { display: 'inline-flex', alignItems: 'center', minHeight: 34, boxSizing: 'border-box', padding: '0 var(--hk-space-3)', border: '1px solid var(--hk-primary-600)', borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-primary-500)', color: '#fff', font: 'inherit', fontSize: 13, fontWeight: 600, cursor: 'pointer', textDecoration: 'none' }
const secondaryActionStyle: CSSProperties = { ...primaryActionStyle, borderColor: 'var(--hk-line)', background: 'var(--hk-surface)', color: 'var(--hk-ink-700)', fontWeight: 500 }
