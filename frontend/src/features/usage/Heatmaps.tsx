/*
 * 小方格展示组件:分段方格计量条 + 日历热力网格。纯展示,数据/聚合来自 windowMeter.ts、heatmap.ts。
 */
import { meterCells } from './windowMeter'
import type { HeatGrid } from './heatmap'

const TOTAL_CELLS = 24

/** 分段方格计量条:pct 决定填满几格;tone 决定填充色。 */
export function MeterCells({ pct, tone = 'ok', total = TOTAL_CELLS }: { pct: number; tone?: 'ok' | 'warn' | 'danger'; total?: number }) {
  const on = meterCells(pct, total)
  const onClass = tone === 'danger' ? 'is-danger' : tone === 'warn' ? 'is-warn' : 'is-on'
  return (
    <div className="hk-meter__cells" role="img" aria-label={`已用 ${Math.round(pct)}%`}>
      {Array.from({ length: total }, (_, i) => (
        <span key={i} className={`hk-meter__cell${i < on ? ` ${onClass}` : ''}`} />
      ))}
    </div>
  )
}

/**
 * 日历热力网格:每天一个小方格,周为列、周一起星期为行。
 * title 为悬停文案生成器(接 day+value)。value 为空网格显示占位。
 */
export function HeatMap({
  grid,
  title,
  formatValue,
  unit,
}: {
  grid: HeatGrid
  title: string
  formatValue: (v: number) => string
  unit: string
}) {
  return (
    <section>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: 6 }}>
        <h4 style={{ margin: 0, fontSize: 13, color: 'var(--hk-ink-900)' }}>{title}</h4>
        <div className="hk-heat-legend">
          <span>少</span>
          <i style={{ background: 'var(--hk-heat-l0)' }} />
          <i style={{ background: 'var(--hk-heat-l1)' }} />
          <i style={{ background: 'var(--hk-heat-l2)' }} />
          <i style={{ background: 'var(--hk-heat-l3)' }} />
          <i style={{ background: 'var(--hk-heat-l4)' }} />
          <span>多</span>
        </div>
      </div>
      {grid.cells.length === 0 ? (
        <div className="hk-empty">所选时段暂无数据。</div>
      ) : (
        <div className="hk-heat" style={{ overflowX: 'auto' }}>
          {grid.cells.map((c) => (
            <span
              key={c.day}
              className={`hk-heat__cell hk-heat__cell--l${c.level}`}
              style={{ gridRow: c.row + 1, gridColumn: c.col + 1 }}
              title={`${c.day} · ${formatValue(c.value)}${unit}`}
            />
          ))}
        </div>
      )}
    </section>
  )
}
