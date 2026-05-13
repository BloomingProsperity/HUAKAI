# HUAKAI 前端 — Round 8

## 项目介绍

HUAKAI 是 MIT 协议的 AI gateway + 账号池 + 运营平台，给运营者用的控制台。当前在做 P1 Dashboard 总览页。

Round 1-7 已迭代过 7 轮，Owner 最后一轮（Round 7）后说："这个介面一点都不好看。我需要 sub2api 那样的"，让你拆解 sub2api UI 风格。

## 你的核心输入：sub2api 完整拆解文档

```
docs/research/2026-05-12-sub2api-frontend-decomposition.md  (1274 行)
```

这份文档由 sonnet 帮你扫读 `~/refs/sub2api/frontend/`（Vue 3 + Vite + Tailwind + Pinia + chart.js + @lobehub/icons）后生成，包含：

- **整体 Layout 架构**：Sidebar (w-64 / w-[72px] 可折叠) + Main Content 两栏布局
- **完整设计 Token**：色彩系统（#14b8a6 青色主 + #64748b 灰色辅）+ 字号 + 间距 + 阴影
- **7 个核心 UI 组件详解**：Card / DataTable / Badge / StatCard / Input / Button / Dropdown
- **图标方案**：@lobehub/icons + Icon.vue 自实现（134 icons, 5 size variants）
- **图表**：chart.js + vue-chartjs 实现 Doughnut + Line（多线 + 双 Y 轴）
- **i18n 中文文案体系**
- **Dashboard 总览页 5 行布局完整分解**
- **Top-N Table 虚拟列表 + 桌面 / 移动双响应式**
- **第 10 章：Vue→React 完整映射表**（你直接用得上的关键章节）

请**读这份文档**然后**重做 HUAKAI P1 Dashboard**。

## HUAKAI tech stack 锁定

- Next.js 14 App Router + TypeScript strict
- 前端目录 `frontend/`
- 可引第三方库（Owner 撤回了"不引第三方"约束）— shadcn / Radix / Tremor / lucide-react / heroicons / recharts / @tanstack/react-virtual / 自由
- 仍禁 AI emoji（Unicode geometric shapes ●▲■◆○ 不算 emoji 允许）
- 仍禁 chatbot bubble / 机器人 icon / AI 装饰文案

## 当前 P1 Dashboard 范围

只做 P1 一页（Dashboard 总览）：

- 状态条（时间 / 后端心跳 / 延迟）
- 6 个核心指标（今日 token / 成本 / 请求数 + 延迟 p50/p95/p99 / 并发 / cache hit ratio + 24h 趋势 / 健康账号比例）
- Top 5 Provider Accounts 表
- 异常告警条件区
- 侧边栏导航（P2-P5 占位）

数据来源：`frontend/lib/dashboard-mock.ts`（mock 默认开）。

## Round 7 现状（FYI，你自己判断要保留 / 重做）

- `frontend/app/dashboard/components/round7/` 已有 Header / MetricCard / MetricGrid / ProviderTable（侧边栏 + 主内容 layout，Tailwind utility）
- `frontend/app/dashboard/components/` 老一代（Round 6）components 也还在
- `frontend/app/dashboard/page.tsx` 现在 import round7 dir
- `frontend/app/layout.tsx` `lang=zh-CN`

## 输出报告（自由格式）

```
Round 8 — Gemini sub2api 拆解 + 自主重做

sub2api decomp 学到了什么 / 借鉴了哪些 token / 哪些组件:
[3-5 段]

React/Next 映射决策 (用哪些第三方库 / 哪些自写):
[3-5 段]

What I changed and why:
[3-5 段]

Files changed: [...]

Verification:
- type-check / build 自跑
- 浏览器在 :3000 看实物
```

直接开始。
