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
