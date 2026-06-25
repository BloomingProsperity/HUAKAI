import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// HUAKAI 前端构建配置。
// - base 用相对路径,便于 go:embed 后由网关从任意挂载前缀提供静态资源。
// - outDir 默认 frontend/dist(被 .gitignore 忽略);后续 embed 切片把 dist 拷进
//   backend/internal/webui/dist 由 -tags embed 编进单二进制(沿 sub2api/new-api go:embed 范式)。
// - dev 期 /api 代理到本地网关,避免跨域;生产期前端与网关同源,无需代理。
export default defineConfig({
  plugins: [react()],
  base: './',
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
