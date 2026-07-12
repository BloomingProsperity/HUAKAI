import { test, expect } from '@playwright/test'
import { login } from './helpers'

// 首页经营总览正证:admin 登录后首页必须渲染"经营总览"段,且统计卡出真值
// (按数字格式正向匹配,排除空串/undefined/错误文案的假阳)。
test('首页经营总览:统计卡出真值、告警块渲染、窗口切换真发请求', async ({ page }) => {
  await login(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '经营总览' })).toBeVisible({ timeout: 15_000 })

  // 请求数卡的值必须是千分位整数格式(0 也合法),正向匹配。
  const requestCard = page.locator('text=请求数').locator('..').locator('span').nth(1)
  await expect(requestCard).toHaveText(/^\d{1,3}(,\d{3})*$/, { timeout: 15_000 })

  // 告警块三态之一必须呈现:告警行 / 无告警 / 明示不可用——不允许只剩标题。
  await expect(page.getByText('最近告警')).toBeVisible()
  await expect(page.getByRole('link', { name: /告警控制台/ })).toBeVisible()
  await expect(
    page.getByText(/暂无告警|告警数据不可用|告警中|已恢复/).first(),
  ).toBeVisible({ timeout: 15_000 })

  // 窗口切换:截获 window=7d 的总览响应,断言卡片渲染值 = 该响应的 requests
  // (与组件同款 en-US 千分位),防"请求发了但组件忽略响应仍显旧值"。
  const sevenDay = page.waitForResponse(
    (r) => r.url().includes('/v1/admin/usage/overview') && r.url().includes('window=7d') && r.ok(),
    { timeout: 15_000 },
  )
  await page.getByLabel('总览时间窗口').selectOption('7d')
  const body = (await (await sevenDay).json()) as { totals: { requests: number } }
  const expected = body.totals.requests.toLocaleString('en-US')
  await expect(requestCard).toHaveText(expected, { timeout: 15_000 })
})
