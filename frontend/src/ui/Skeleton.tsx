import type { CSSProperties } from 'react'

type Size = number | string

const animationRules = `
@keyframes hk-skeleton-breathe { 0%, 100% { opacity: .45; } 50% { opacity: 1; } }
.hk-skeleton-block { animation: hk-skeleton-breathe 1.5s ease-in-out infinite; }
@media (prefers-reduced-motion: reduce) { .hk-skeleton-block { animation: none; opacity: .7; } }
`

function Block({ width = '100%', height = 14 }: { width?: Size; height?: Size }) {
  return <span className="hk-skeleton-block" style={{ display: 'block', width, height, borderRadius: 'var(--hk-radius-sm)', background: 'var(--hk-line-soft)', border: '1px solid var(--hk-line)', boxSizing: 'border-box' }} />
}

/* 骨架仅表达布局占位，对辅助技术隐藏，避免重复播报无意义内容。 */
export function Skeleton({ width, height }: { width?: Size; height?: Size }) {
  return <span aria-hidden="true"><style>{animationRules}</style><Block width={width} height={height} /></span>
}

export function SkeletonText({ lines = 3 }: { lines?: number }) {
  return (
    <span aria-hidden="true" style={stackStyle}>
      <style>{animationRules}</style>
      {Array.from({ length: lines }, (_, index) => (
        <Block key={index} width={index === lines - 1 && lines > 1 ? '72%' : '100%'} height={12} />
      ))}
    </span>
  )
}

export function SkeletonRows({ rows = 3, cols = 4 }: { rows?: number; cols?: number }) {
  return (
    <span aria-hidden="true" style={stackStyle}>
      <style>{animationRules}</style>
      {Array.from({ length: rows }, (_, row) => (
        <span data-skeleton-row="true" key={row} style={rowStyle}>
          {Array.from({ length: cols }, (_, col) => (
            <span data-skeleton-cell="true" key={col} style={{ flex: col === 0 ? '1.4 1 0' : '1 1 0' }}><Block height={12} /></span>
          ))}
        </span>
      ))}
    </span>
  )
}

const stackStyle: CSSProperties = { display: 'flex', flexDirection: 'column', gap: 'var(--hk-space-2)', width: '100%' }
const rowStyle: CSSProperties = { display: 'flex', gap: 'var(--hk-space-3)', padding: 'var(--hk-space-2) 0', borderBottom: '1px solid var(--hk-line-soft)' }
