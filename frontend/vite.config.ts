/// <reference types="vitest" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// HUAKAI 前端构建配置。
// base 必须是绝对根 '/':勿改回 './'——相对 base 下深层路由(如 /oauth/callback)会把资源解析到
// /oauth/assets/*,404 兜底成 index.html → 浏览器拿 HTML 当 JS → 白屏。
export default defineConfig({
  plugins: [react()],
  base: '/',
  // vitest 只跑单元测试(src 下);Playwright 的 e2e/ 由 `playwright test` 独立跑,勿被 vitest 误扫。
  test: {
    include: ['src/**/*.{test,spec}.{ts,tsx}'],
    exclude: ['node_modules', 'dist', 'e2e/**'],
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      output: {
        // 把 React 运行时与路由库拆成独立 vendor chunk:它们极少变动,与频繁迭代的应用代码分离后,
        // 浏览器可跨应用更新长期缓存 vendor(应用代码改了不必重下 React),首屏主包也随之变小。
        manualChunks: {
          vendor: ['react', 'react-dom', 'react-router-dom'],
        },
      },
    },
  },
  // 仅 dev/preview:把前端实际调用的网关 API 前缀转发到本地网关。生产由网关 go:embed 同源提供,不经此代理。
  server: {
    port: 5173,
    proxy: {
      '/api': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/v1': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/admin': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/.well-known': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      // 只代理首装 API 两条(正则精确匹配);/setup 本身是前端路由,不能整段代理。
      '^/setup/(status|install)$': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
  preview: {
    port: 4173,
    proxy: {
      '/api': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/v1': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/admin': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      '/.well-known': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
      // 只代理首装 API 两条(正则精确匹配);/setup 本身是前端路由,不能整段代理。
      '^/setup/(status|install)$': { target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080', changeOrigin: true },
    },
  },
})
