import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// HUAKAI 前端构建配置。
// base 必须是绝对根 '/':勿改回 './'——相对 base 下深层路由(如 /oauth/callback)会把资源解析到
// /oauth/assets/*,404 兜底成 index.html → 浏览器拿 HTML 当 JS → 白屏。
export default defineConfig({
  plugins: [react()],
  base: '/',
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
  server: {
    port: 5173,
    proxy: {
      '/api': {
        target: process.env.HUAKAI_GATEWAY_ORIGIN || 'http://127.0.0.1:8080',
        changeOrigin: true,
      },
    },
  },
})
