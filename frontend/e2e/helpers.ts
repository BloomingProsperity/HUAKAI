import { type Page, expect } from '@playwright/test'

// dev admin 登录凭据(本机 dev 号,非真实机密)。可用 env 覆盖。
export const ADMIN_EMAIL = process.env.HK_E2E_EMAIL || 'admin@huakai.ai'
export const ADMIN_PASSWORD = process.env.HK_E2E_PASSWORD || 'Huakai#2026dev'
export const TENANT_ID = process.env.HK_E2E_TENANT || '1'

// 用邮箱密码登录,登录成功后停在运营台首页(非 /login)。
export async function login(page: Page): Promise<void> {
  await page.goto('/login')
  // 登录表单:租户(数字)/邮箱/密码/提交。选择器走类型(表单为内联样式,无 testid)。
  const tenant = page.locator('input[inputmode="numeric"]').first()
  if (await tenant.count()) {
    await tenant.fill(TENANT_ID)
  }
  await page.locator('input[type="email"]').first().fill(ADMIN_EMAIL)
  await page.locator('input[type="password"]').first().fill(ADMIN_PASSWORD)
  await page.locator('button[type="submit"]').first().click()
  // 登录成功后应离开 /login(重定向到 /overview 或 /)。
  await expect(page).not.toHaveURL(/\/login/, { timeout: 15_000 })
}

// 判定当前页面是否命中路由级错误边界(ErrorFallback 的"刷新重试"按钮)。
export async function hasErrorBoundary(page: Page): Promise<boolean> {
  return (await page.getByRole('button', { name: '刷新重试' }).count()) > 0
}
