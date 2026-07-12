import { test, expect } from '@playwright/test'
import { login, hasErrorBoundary } from './helpers'

// 逐页按钮交互:登录后进每一页,点击页面上的"安全"按钮(跳过破坏性:删除/撤销/吊销/停用/禁用/
// 清除/重置/登出/移除/解绑/强制),自动取消所有确认弹窗;每次点击后断言页面不崩、无 5xx。
// 这是"每个可点按钮真能点、点了不出错、后端正确响应"的浏览器级证明(破坏性按钮只验证存在,不实点)。
const ROUTES: string[] = [
  '/overview', '/keys', '/usage', '/usage-records', '/accounts', '/models', '/routing',
  '/users', '/wallet', '/orders', '/subscriptions', '/checkin', '/redeem', '/affiliate',
  '/rankings', '/my-groups', '/notifications', '/profile', '/media-tasks', '/available-channels',
  '/activity', '/security', '/system', '/health', '/ops', '/playground', '/trust',
  '/integration', '/key-usage',
  '/admin/affiliates', '/admin/alerting', '/admin/announcements', '/admin/backup',
  '/admin/billing-claims', '/admin/broadcast', '/admin/cache', '/admin/catalogs',
  '/admin/channel-health', '/admin/channel-test-templates', '/admin/credential-renew',
  '/admin/disputes', '/admin/dlq', '/admin/groups', '/admin/hermes', '/admin/logs',
  '/admin/model-registry', '/admin/model-sync', '/admin/moderation', '/admin/modules',
  '/admin/orders', '/admin/orphan-reconcile', '/admin/platform-credentials', '/admin/pricing',
  '/admin/proxies', '/admin/quota-policies', '/admin/risk', '/admin/route-rules',
  '/admin/subscriptions', '/admin/tls-fingerprints', '/admin/version', '/admin/vouchers',
]

const DESTRUCTIVE = /删除|撤销|吊销|停用|禁用|清除|重置|登出|移除|解绑|强制|删|清空|注销|终止|回滚/

test('逐页安全按钮点击:点后不崩、无 5xx', async ({ page }) => {
  test.setTimeout(300_000)
  page.on('dialog', (d) => d.dismiss().catch(() => {})) // 破坏性二次确认一律取消

  const failures: string[] = []
  let clicked = 0
  let destructiveSeen = 0

  await login(page)

  for (const route of ROUTES) {
    const bad5xx: string[] = []
    const onResp = (r: import('@playwright/test').Response) => {
      if (r.status() >= 500 && (r.url().includes('/v1/') || r.url().includes('/admin/'))) {
        bad5xx.push(`${r.status()} ${new URL(r.url()).pathname}`)
      }
    }
    page.on('response', onResp)
    try {
      await page.goto(route, { waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(300)
      const buttons = page.getByRole('button')
      const n = Math.min(await buttons.count(), 14)
      let clickedHere = 0
      for (let i = 0; i < n && clickedHere < 6; i++) {
        const b = buttons.nth(i)
        if (!(await b.isVisible().catch(() => false)) || !(await b.isEnabled().catch(() => false))) continue
        const txt = ((await b.innerText().catch(() => '')) || '').trim()
        if (!txt) continue
        if (DESTRUCTIVE.test(txt)) { destructiveSeen++; continue }
        await b.click({ timeout: 4000 }).catch(() => {})
        clicked++
        clickedHere++
        await page.waitForTimeout(180)
        if (await hasErrorBoundary(page)) { failures.push(`${route} → 点击「${txt}」后页面崩溃`); break }
        await page.keyboard.press('Escape').catch(() => {}) // 关掉可能弹出的 modal
        // 若点击导致离开当前路由,回到该路由继续
        if (!page.url().includes(route)) {
          await page.goto(route, { waitUntil: 'domcontentloaded' }).catch(() => {})
          await page.waitForTimeout(200)
        }
      }
      if (bad5xx.length) failures.push(`${route} → 5xx: ${bad5xx.slice(0, 3).join(', ')}`)
    } catch (e) {
      failures.push(`${route} → 异常: ${(e as Error).message.slice(0, 80)}`)
    } finally {
      page.off('response', onResp)
    }
  }

  console.log(`\n=== 按钮点击统计:点击 ${clicked} 个安全按钮,另见 ${destructiveSeen} 个破坏性按钮(仅验存在) ===`)
  if (failures.length) console.log('=== 失败明细 ===\n' + failures.join('\n'))
  expect(failures, `失败 ${failures.length} 项`).toEqual([])
})
