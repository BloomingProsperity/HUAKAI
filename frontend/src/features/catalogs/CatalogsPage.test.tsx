import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { DataListTable } from '../../ui/DataListTable'
import { CatalogsPage, ChannelsCard, channelColumns } from './CatalogsPage'
import { mapChannelCatalogRows } from './catalogs'
import type { ChannelCatalogItem } from './types'

const legacyChannel: ChannelCatalogItem = {
  id: 91,
  pool_group_id: 73,
  name: 'channel-compat-marker',
  failover_status_codes: [598],
  enabled: true,
  created_at: '2026-07-14T00:00:00Z',
}

describe('channel 目录 UI 只暴露真实生效字段', () => {
  it('表单 DOM 不出现 failover_status_codes 控件,有效字段仍存在', () => {
    const html = renderToStaticMarkup(<ChannelsCard tenantId={7} />)

    expect(html).toContain('名称(name)')
    expect(html).toContain('pool_group_id(正整数)')
    expect(html).toContain('启用')
    expect(html).toContain('原因(reason,可选,写入审计)')
    expect(html).not.toContain('失败转移状态码')
    expect(html).not.toContain('failover_status_codes')
  })

  it('列表 DOM 忽略旧响应中的状态码,仍展示名称、池组与状态', () => {
    const rows = mapChannelCatalogRows([legacyChannel])
    const html = renderToStaticMarkup(
      <DataListTable label="channel 目录" rows={rows} rowKey={(row) => row.id} columns={channelColumns} />,
    )

    expect(html).toContain('channel-compat-marker')
    expect(html).toContain('73')
    expect(html).toContain('启用')
    // 变异:把死列加回 channelColumns,列名或独特状态码会使断言转红。
    expect(html).not.toContain('失败转移码')
    expect(html).not.toContain('598')
  })

  it('目录入口文案不再宣称 channel 状态码会驱动失败转移', () => {
    const html = renderToStaticMarkup(<CatalogsPage />)
    expect(html).toContain('池组路由条目')
    expect(html).not.toContain('路由失败转移条目')
  })
})
