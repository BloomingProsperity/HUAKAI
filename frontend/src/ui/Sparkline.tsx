import type { CSSProperties } from 'react'

export interface SparklineProps {
  values: number[]
  label: string
  width?: number
  height?: number
}

/** 把数值序列压到固定视窗；单值与全相等序列均落在中线，避免无效坐标。 */
export function sparklinePath(values: number[], width: number, height: number): string {
  const clean = values.map((value) => (Number.isFinite(value) ? value : 0))
  if (clean.length === 0) return ''
  const min = Math.min(...clean)
  const max = Math.max(...clean)
  const range = max - min
  return clean.map((value, index) => {
    const x = clean.length === 1 ? width / 2 : (index / (clean.length - 1)) * width
    const y = range === 0 ? height / 2 : height - ((value - min) / range) * height
    return `${index === 0 ? 'M' : 'L'} ${x.toFixed(2)} ${y.toFixed(2)}`
  }).join(' ')
}

export function Sparkline({ values, label, width = 160, height = 36 }: SparklineProps) {
  const d = sparklinePath(values, width, height)
  if (!d) return null
  return (
    <svg viewBox={`0 0 ${width} ${height}`} width="100%" height={height} role="img" aria-label={label} style={svgStyle}>
      <path d={d} fill="none" stroke="var(--hk-primary-600)" strokeWidth="2" vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

const svgStyle: CSSProperties = { display: 'block', overflow: 'visible' }
