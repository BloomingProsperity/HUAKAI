import { test, expect } from '@playwright/test'
import { login } from './helpers'

// 已装系统访问 /setup 必须被送去登录页(fail-closed 的前端面)。
test('已安装:/setup 直接送去 /login', async ({ page }) => {
  await page.goto('/setup')
  await expect(page).toHaveURL(/\/login/, { timeout: 15_000 })
})

// 管理员登录后落运营台首页(sub2 分流形态),侧栏是管理后台且含「我的账户」组,
// 旧的「进入管理后台」切换门必须不存在。
test('管理员登录落运营台首页,单侧栏含我的账户、无切换门', async ({ page }) => {
  await login(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '经营总览' })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('管理后台').first()).toBeVisible()
  await expect(page.getByRole('button', { name: /我的账户/ })).toBeVisible()
  await expect(page.getByText('进入管理后台')).toHaveCount(0)
})
