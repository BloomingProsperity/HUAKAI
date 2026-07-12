import { test, expect } from '@playwright/test'
import { login, hasErrorBoundary } from './helpers'

// 全路由 smoke:登录后逐个访问每条路由,断言页面不崩(无路由错误边界)、无未捕获 JS 异常、
// 页面加载期无 5xx 网络响应。这是"每个页面能开、按钮所在的界面正常渲染"的浏览器级证明。
const ROUTES: string[] = [
  '/overview', '/keys', '/usage', '/usage-records', '/accounts', '/models', '/routing',
  '/users', '/wallet', '/orders', '/subscriptions', '/checkin', '/redeem', '/affiliate',
  '/rankings', '/my-groups', '/notifications', '/profile', '/media-tasks', '/available-channels',
  '/activity', '/security', '/system', '/health', '/ops', '/playground',
  '/admin/affiliates', '/admin/alerting', '/admin/announcements', '/admin/backup',
  '/admin/billing-claims', '/admin/broadcast', '/admin/cache', '/admin/catalogs',
  '/admin/channel-health', '/admin/channel-test-templates', '/admin/credential-renew',
  '/admin/disputes', '/admin/dlq', '/admin/groups', '/admin/hermes', '/admin/logs',
  '/admin/model-registry', '/admin/model-sync', '/admin/moderation', '/admin/modules',
  '/admin/orders', '/admin/orphan-reconcile', '/admin/pricing', '/admin/proxies',
  '/admin/quota-policies', '/admin/risk', '/admin/route-rules', '/admin/subscriptions',
  '/admin/tls-fingerprints', '/admin/version', '/admin/vouchers',
]

test('全路由 smoke:每页可开、不崩、无 5xx', async ({ page }) => {
  test.setTimeout(300_000) // 56 路由单测遍历,给足时间
  const failures: string[] = []
  const pageErrors: string[] = []
  page.on('pageerror', (e) => pageErrors.push(e.message))

  await login(page)

  for (const route of ROUTES) {
    pageErrors.length = 0
    const bad5xx: string[] = []
    const onResp = (r: import('@playwright/test').Response) => {
      const url = r.url()
      if (r.status() >= 500 && (url.includes('/v1/') || url.includes('/admin/'))) {
        bad5xx.push(`${r.status()} ${new URL(url).pathname}`)
      }
    }
    page.on('response', onResp)
    try {
      await page.goto(route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(350) // 给首屏 fetch 一点时间触发
      if (await hasErrorBoundary(page)) failures.push(`${route} → 路由错误边界(页面崩溃)`)
      if (pageErrors.length) failures.push(`${route} → JS异常: ${pageErrors.slice(0, 2).join(' | ')}`)
      if (bad5xx.length) failures.push(`${route} → 5xx: ${bad5xx.slice(0, 3).join(', ')}`)
    } catch (e) {
      failures.push(`${route} → 导航失败: ${(e as Error).message.slice(0, 80)}`)
    } finally {
      page.off('response', onResp)
    }
  }

  if (failures.length) console.log('\n=== SMOKE 失败明细 ===\n' + failures.join('\n'))
  expect(failures, `共 ${ROUTES.length} 路由,失败 ${failures.length}`).toEqual([])
})
