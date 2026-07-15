import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { RoutingTabs } from './RoutingPage'
import { RoutingOverrideList } from './RoutingOverridesPanel'
import type { ModelRoutingOverride } from './types'

const item: ModelRoutingOverride = {
  id: 17,
  pool_group_id: 9,
  model: 'gpt-pin',
  provider_account_ids: [11, 13],
  enabled: true,
  created_at: '2026-07-15T01:00:00Z',
  updated_at: '2026-07-15T02:00:00Z',
}

describe('routing 强制 pin 分签与空态', () => {
  it('两分签都有可访问状态且文案区分绑定与强制 pin', () => {
    const html = renderToStaticMarkup(<RoutingTabs value="overrides" onChange={() => undefined} />)
    expect(html).toContain('role="tablist"')
    expect(html).toContain('绑定')
    expect(html).toContain('强制 pin')
    expect(html).toContain('aria-selected="true"')
  })

  it('空数组显示可操作空态，有数据时改为精确账号子集表格', () => {
    const empty = renderToStaticMarkup(
      <RoutingOverrideList items={[]} loading={false} busyID={null} onEdit={() => undefined} onDelete={() => undefined} />,
    )
    expect(empty).toContain('没有强制 pin')
    expect(empty).toContain('新建强制 pin')

    const populated = renderToStaticMarkup(
      <RoutingOverrideList items={[item]} loading={false} busyID={null} onEdit={() => undefined} onDelete={() => undefined} />,
    )
    expect(populated).not.toContain('没有强制 pin')
    expect(populated).toContain('gpt-pin')
    expect(populated).toContain('#11、#13')
  })
})
