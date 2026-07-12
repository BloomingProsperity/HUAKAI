import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { DlqPage } from './DlqPage'
import { ObsDlqTable } from './ObsDlqTab'
import type { ObsDlqRecord } from './types'

const RECORD: ObsDlqRecord = {
  id: 'dead-1',
  outbox_event_id: 'outbox-8',
  tenant_id: 7,
  event_type: 'email.retry',
  priority: 'critical',
  payload: { target: 'masked' },
  dead_at: '2026-07-12T00:00:00Z',
  dead_reason: '超过最大重试次数',
  attempt_count: 5,
  outbox_status: 'failed_dead',
  failure_reason: 'smtp unavailable',
  created_at: '2026-07-11T00:00:00Z',
  next_retry_at: '2026-07-12T00:05:00Z',
}

describe('死信关键渲染', () => {
  it('同一 DLQ 页面明确提供三个分签', () => {
    const html = renderToStaticMarkup(<DlqPage />)
    expect(html).toContain('传统死信')
    expect(html).toContain('用量记录死信')
    expect(html).toContain('观测死信')
  })

  it('观测列表展示真实 ID、事件、死信状态与重放按钮', () => {
    const html = renderToStaticMarkup(
      <ObsDlqTable
        rows={[RECORD]}
        loading={false}
        replayingID={null}
        expandedID={null}
        onToggle={vi.fn()}
        onReplay={vi.fn()}
      />,
    )
    expect(html).toContain('dead-1')
    expect(html).toContain('email.retry')
    expect(html).toContain('观测死信')
    expect(html).toContain('超过最大重试次数')
    expect(html).toContain('重放')
  })

  it('空列表显示专属空态，不伪装成传统死信', () => {
    const html = renderToStaticMarkup(
      <ObsDlqTable
        rows={[]}
        loading={false}
        replayingID={null}
        expandedID={null}
        onToggle={vi.fn()}
        onReplay={vi.fn()}
      />,
    )
    expect(html).toContain('暂无观测死信')
  })
})
