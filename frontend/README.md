# HUAKAI Frontend — Vertical Closure 工具

本目录是 HUAKAI 项目的最小前端 wedge，用于 **vertical closure 手测 + E2E 路径验证**。
不是 Admin UI，不含 pool / user / billing 管理页。

## 目标

| 页面 | 路径 | 作用 |
|------|------|------|
| ChatPage | `/` | 直接向 HUAKAI 网关发 Anthropic Messages 形请求，支持 SSE 流式 |
| ObservabilityPage | `/observability` | 每 2 秒 poll `/debug/vars`，展示 cache token 命中率 |

## 技术栈

- Next.js 14 App Router + TypeScript strict mode
- 无 UI 库（无 shadcn / mui / antd）— 纯 CSS
- 无 SSE 第三方库 — fetch + ReadableStream + 行解析
- Next.js rewrites 把 `/v1/*` 和 `/debug/*` 反代到 `localhost:8080`

## 快速启动

```bash
cd frontend
npm install
npm run dev
# 浏览器打开 http://localhost:3000
```

需要 backend 先跑（`go run ./cmd/gateway` 或 `make run`），默认监听 `:8080`。

## 文件结构

```
frontend/
  package.json
  tsconfig.json
  next.config.mjs          # rewrites /v1/* /debug/* → :8080
  app/
    layout.tsx             # 极简 header + nav
    page.tsx               # ChatPage（< 250 LoC）
    observability/
      page.tsx             # ObservabilityPage（< 250 LoC）
    globals.css            # 纯 CSS base style
```

## 关联计划

见 `docs/plans/2026-05-08-vertical-closure-synthesis.md` §3 "前端 wedge 最小集"。
