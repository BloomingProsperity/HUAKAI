import { test, expect } from '@playwright/test'

/*
 * 首装黄金路径(需空库网关:HK_E2E_BASE 指向代理到全新 DB 的 preview 才有意义;
 * 已装环境跑它会在第一步就失败,故用 env 开关显式启用)。
 * 流程:进 /login 被引导到 /setup → 建管理员 → 完成页 → 去登录 → 新号登录直落运营台首页。
 */
test('首装:登录页引导到向导,建管理员后新号直落运营台', async ({ page }) => {
  test.skip(process.env.HK_E2E_FRESH !== '1', '需 HK_E2E_FRESH=1 + 空库网关')
  test.setTimeout(120_000)

  await page.goto('/login')
  await expect(page).toHaveURL(/\/setup/, { timeout: 15_000 })
  await expect(page.getByRole('heading', { name: '初始化 HUAKAI' })).toBeVisible()

  await page.getByLabel(/管理员邮箱/).fill('first-admin@example.com')
  await page.getByLabel(/^密码/).fill('Fr3shPass!9')
  await page.getByLabel(/确认密码/).fill('Fr3shPass!9')
  await page.getByRole('button', { name: /创建管理员并完成安装/ }).click()

  await expect(page.getByText('创建成功')).toBeVisible({ timeout: 15_000 })
  await page.getByRole('link', { name: /去登录/ }).click()
  await expect(page).toHaveURL(/\/login/)

  await page.locator('input[type="email"]').first().fill('first-admin@example.com')
  await page.locator('input[type="password"]').first().fill('Fr3shPass!9')
  await page.locator('button[type="submit"]').first().click()

  // 新管理员直落运营台首页:经营总览段 + 管理后台侧栏。
  await expect(page).not.toHaveURL(/\/(login|setup)/, { timeout: 15_000 })
  await expect(page.getByRole('heading', { name: '经营总览' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('管理后台').first()).toBeVisible()
})
