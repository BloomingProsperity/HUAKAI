import type { CSSProperties } from 'react'
import { Link } from 'react-router-dom'

export interface DonutSegment {
  label: string
  value: number
  color: string
  to?: string
}

export interface DonutProps {
  segments: DonutSegment[]
  total: number
  label: string
  formatTotal?: (value: number) => string
}

/** 环形分段只接受正有限值，防止坏数据生成负周长或 NaN。 */
export function donutSlices(segments: DonutSegment[]) {
  const clean = segments.map((segment) => ({ ...segment, value: Number.isFinite(segment.value) && segment.value > 0 ? segment.value : 0 }))
  const sum = clean.reduce((total, segment) => total + segment.value, 0)
  let offset = 0
  return clean.filter((segment) => segment.value > 0).map((segment) => {
    const percent = sum === 0 ? 0 : (segment.value / sum) * 100
    const slice = { ...segment, percent, offset }
    offset += percent
    return slice
  })
}

export function Donut({ segments, total, label, formatTotal = (value) => value.toLocaleString('zh-CN') }: DonutProps) {
  const slices = donutSlices(segments)
  if (slices.length === 0) return null
  return (
    <div style={wrapStyle}>
      <svg viewBox="0 0 120 120" width="156" height="156" role="img" aria-label={label} data-testid="donut-chart">
        <circle cx="60" cy="60" r="42" fill="none" stroke="var(--hk-line-soft)" strokeWidth="16" />
        {slices.map((slice) => (
          <circle
            key={slice.label}
            cx="60"
            cy="60"
            r="42"
            fill="none"
            stroke={slice.color}
            strokeWidth="16"
            pathLength="100"
            strokeDasharray={`${slice.percent} ${100 - slice.percent}`}
            strokeDashoffset={-slice.offset}
            transform="rotate(-90 60 60)"
          />
        ))}
        <text x="60" y="56" textAnchor="middle" style={totalStyle}>{formatTotal(total)}</text>
        <text x="60" y="73" textAnchor="middle" style={captionStyle}>总调用量</text>
      </svg>
      <ul style={legendStyle}>
        {slices.map((slice) => {
          const content = <><i style={{ ...dotStyle, background: slice.color }} /> <span style={legendLabelStyle}>{slice.label}</span><strong>{slice.percent.toFixed(1)}%</strong></>
          return <li key={slice.label} style={legendRowStyle}>{slice.to ? <Link to={slice.to} style={legendLinkStyle}>{content}</Link> : content}</li>
        })}
      </ul>
    </div>
  )
}

const wrapStyle: CSSProperties = { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 'var(--hk-space-4)', flexWrap: 'wrap' }
const legendStyle: CSSProperties = { display: 'flex', flex: '1 1 150px', flexDirection: 'column', gap: 'var(--hk-space-2)', minWidth: 140, padding: 0, margin: 0, listStyle: 'none' }
const legendRowStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', color: 'var(--hk-ink-700)', fontSize: 12 }
const legendLinkStyle: CSSProperties = { display: 'flex', alignItems: 'center', gap: 'var(--hk-space-2)', width: '100%', color: 'inherit' }
const legendLabelStyle: CSSProperties = { flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }
const dotStyle: CSSProperties = { width: 8, height: 8, flex: 'none', borderRadius: '50%' }
const totalStyle: CSSProperties = { fill: 'var(--hk-ink-900)', fontSize: 14, fontWeight: 700 }
const captionStyle: CSSProperties = { fill: 'var(--hk-ink-500)', fontSize: 8 }
