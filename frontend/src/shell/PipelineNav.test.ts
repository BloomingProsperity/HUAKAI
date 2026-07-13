import { describe, expect, it } from 'vitest'
import { PIPELINE_NAV } from '../app/nav'
import { buildDisplaySections } from './PipelineNav'

const EXPECTED_PATHS = [
  '/accounts',
  '/activity',
  '/admin/affiliates',
  '/admin/alerting',
  '/admin/announcements',
  '/admin/backup',
  '/admin/billing-claims',
  '/admin/broadcast',
  '/admin/cache',
  '/admin/catalogs',
  '/admin/channel-health',
  '/admin/channel-test-templates',
  '/admin/credential-renew',
  '/admin/disputes',
  '/admin/dlq',
  '/admin/groups',
  '/admin/hermes',
  '/admin/logs',
  '/admin/model-registry',
  '/admin/model-sync',
  '/admin/moderation',
  '/admin/modules',
  '/admin/orders',
  '/admin/orphan-reconcile',
  '/admin/platform-credentials',
  '/admin/pricing',
  '/admin/proxies',
  '/admin/quota-policies',
  '/admin/risk',
  '/admin/route-rules',
  '/admin/subscriptions',
  '/admin/tls-fingerprints',
  '/admin/version',
  '/admin/vouchers',
  '/affiliate',
  '/available-channels',
  '/checkin',
  '/health',
  '/integration',
  '/keys',
  '/media-tasks',
  '/models',
  '/my-groups',
  '/notifications',
  '/ops',
  '/orders',
  '/overview',
  '/playground',
  '/profile',
  '/redeem',
  '/routing',
  '/security',
  '/subscriptions',
  '/system',
  '/trust',
  '/usage',
  '/usage-records',
  '/users',
  '/wallet',
]

describe('PipelineNav 信息架构', () => {
  it('重组后完整保留全部唯一路由', () => {
    const paths = PIPELINE_NAV.flatMap((section) => section.items.map((item) => item.path)).sort()
    expect(paths).toEqual(EXPECTED_PATHS)
    expect(new Set(paths).size).toBe(paths.length)
  })

  it('运营台使用一个概览入口、五个业务分组和末尾个人组', () => {
    const sections = buildDisplaySections(PIPELINE_NAV, true)
    expect(sections.filter((section) => section.standalone).map((section) => section.label)).toEqual(['概览'])
    expect(sections.filter((section) => !section.standalone).map((section) => section.label)).toEqual([
      '网关资源',
      '用户与财务',
      '安全与审计',
      '观测与运维',
      '设置',
      '我的账户',
    ])
    expect(sections.at(-1)?.items.map((item) => item.path)).toEqual(
      PIPELINE_NAV.filter((section) => section.shell === 'user').flatMap((section) => section.items.map((item) => item.path)),
    )
  })

  it('普通用户使用概览和三个常开自然分组', () => {
    const sections = buildDisplaySections(PIPELINE_NAV, false)
    expect(sections.every((section) => section.shell === 'user')).toBe(true)
    expect(sections.filter((section) => !section.standalone).map((section) => section.label)).toEqual([
      '我的账户',
      '用量与计费',
      '更多',
    ])
  })
})
