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

当前前端源码不是产品规格，也不能作为后续重构的可信依据。页面能力范围以
真实后端源码、`docs/openapi/openapi.yaml` 和 `docs/specs/` 下当前合同为准，身份与
租户边界以 `docs/process/plans/2026-07-16-three-role-single-level-tenant-model-codex.md`
为权威合同。每个页面实施前必须重新形成来源可追溯的逐页规格，未经核实的旧页面清单
和现有前端排版均不得作为实现依据。
