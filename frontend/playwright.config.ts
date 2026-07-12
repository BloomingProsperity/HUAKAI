import { defineConfig, devices } from '@playwright/test'

// HUAKAI 前端浏览器级 E2E。对已在运行的 vite dev(5173,代理到本地网关 8080)跑真实点击。
// 仅本机 dev 用;不进生产构建。运行前需 vite dev + 网关 + admin 号就绪(见 e2e/README 或对话记录)。
export default defineConfig({
  testDir: './e2e',
  timeout: 30_000,
  expect: { timeout: 8_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: [['list']],
  use: {
    baseURL: process.env.HK_E2E_BASE || 'http://localhost:5173',
    headless: true,
    actionTimeout: 8_000,
    navigationTimeout: 15_000,
    ignoreHTTPSErrors: true,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
