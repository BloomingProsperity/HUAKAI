# HUAKAI 前端(React + Vite + TypeScript SPA)

中转站运维控制台。单页应用,生产期由网关二进制经 `go:embed` 提供(沿 sub2api/new-api 范式),
与后端 API 同源,无独立部署。旧 Next.js 实验前端已移除(归档于 `archive/frontend-nextjs-pre-vite`),
本目录为干净的 React+Vite 重建。

## 技术栈

- React 18 + TypeScript + Vite 5
- react-router-dom v6(`createBrowserRouter`)
- 无 UI 框架依赖:走自有设计 token(`src/styles/tokens.css`,反克隆基线),禁魔法色值

## 结构

```
frontend/
  index.html
  src/
    main.tsx            挂载入口
    app/                App / 路由 / 导航模型(管线即导航)
    shell/              外壳:顶栏 / 管线导航 / 布局
    pages/              页面(总览 + 占位)
    lib/api.ts          网关 API 客户端基座(fetch 封装 + 错误归一 + 混合鉴权)
    styles/             设计 token + 全局样式
```

## 开发

```bash
npm install
npm run dev          # http://localhost:5173;/api 代理到 HUAKAI_GATEWAY_ORIGIN(默认 :8080)
npm run build        # tsc -b && vite build → dist/
npm run typecheck
```

## 路线

按《docs/frontend/2026-06-24-源码梳理与前端编写方案.md》第四节逐个 P0 切片点亮 8 个域
(账号中心 → 路由与池 → API Key → 用量计费 → 用户租户 → 模型定价 → 系统 → 安全审计)。
当前为**地基切片**:外壳 + 管线导航 + API 基座 + 设计 token,域模块挂占位页。
embed 进单二进制(`backend/internal/webui/dist`)为后续独立切片(触网关 router,Owner-gated 部署前置)。
