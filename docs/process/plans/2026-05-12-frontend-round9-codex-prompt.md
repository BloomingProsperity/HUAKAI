# HUAKAI 前端 — Round 9（Codex 接手）

## 项目介绍

HUAKAI 是 MIT 协议的 AI gateway + 账号池 + 运营平台，给运营者用的控制台。当前在做 P1 Dashboard 总览页。

## Owner 8 轮 Gemini 失败简史

Round 1-8 由 Gemini 主笔，8 轮迭代后 Owner 看到 Round 8 浏览器实物截图，UI **完全 broken**：

- Tailwind v4 + Next.js 14 + shadcn-ui 手动 config 出错 → 0 样式生效
- 重复渲染（"HUAKAI" / "HUAKAI" 两次 + 双语 nav 重叠）
- 蓝色下划线 `<a>` 是浏览器默认，Tailwind reset 没生效
- 卡片 / grid 全失效，纯文字流式从上到下
- recharts container width/height -1 warning

## Owner 当前发言

> "？？？ 让 codex 去写前端！ 禁止使用 gemini"

**前端从此归 codex。Gemini 整体禁用。** Memory 已更新（`feedback_frontend_belongs_to_codex.md`）。

## Owner 历史发言（你过去几轮听到的 — 仍然有效）

- 现代 SaaS dashboard 调性，不要"工业极简"
- "我需要 sub2api 那样的"
- 禁 AI 表情 / chatbot 气泡 / 机器人 icon / "AI-powered" 装饰文案
- Unicode geometric shapes (●▲■◆○) 不算 emoji 可用
- **可引第三方库**（shadcn / Radix / Tremor / lucide-react / recharts / 自由），但 HUAKAI 要在第三方上加升级层 — 不能纯 wrap
- 借鉴 sub2api / Vercel / Linear / Stripe / Helicone 的 layout 模式
- 中文 UI 文案 + 中文注释

## 你的输入资源

### 关键参考 doc

```
docs/research/2026-05-12-sub2api-frontend-decomposition.md   (1274 行)
```

sonnet 帮 sub2api（Vue 3 + Tailwind + Pinia + chart.js + @lobehub/icons）完整拆解：

- 整体 layout：Sidebar (w-64/w-[72px] 折叠) + Main Content
- 设计 token：色板 #14b8a6 青色主 / #64748b 灰色辅 + 字号 + 间距 + 阴影
- 7 个核心 UI 组件（Card / DataTable / Badge / StatCard / Input / Button / Dropdown）
- 图标 @lobehub/icons + Icon.vue 134 icons / 5 size variants
- 图表 chart.js + vue-chartjs（Doughnut + 多线折线 + 双 Y 轴）
- i18n 中文文案体系
- Dashboard 总览页 5 行布局完整分解
- **第 10 章：Vue→React 完整映射表**（关键）

### 市场参考 brief

```
docs/research/2026-05-12-frontend-brief-market-sonnet.md   (869 行 / 15 dashboards)
docs/research/2026-05-12-frontend-brief-market-codex.md    (~1100 行 / 20+ ref UIs)
docs/research/2026-05-12-frontend-brief-huakai-summary.md  (885 行 / HUAKAI 12 页规划)
```

### 当前 frontend/ 状态（Round 8 留下的）

```
frontend/
├── app/
│   ├── globals.css         (Gemini Round 8 配置错的 CSS)
│   ├── layout.tsx          (lang=zh-CN，AppLayout 包 children)
│   └── dashboard/
│       ├── components/     (Round 6/7 残留 + 新 round8 mix)
│       ├── dashboard.module.css
│       ├── layout.tsx
│       └── page.tsx
├── components/
│   ├── dashboard/{StatCard.tsx, TrendChart.tsx}
│   ├── layout/{AppLayout.tsx, Sidebar.tsx, Header.tsx}
│   └── ui/{card.tsx, button.tsx, badge.tsx, table.tsx}  ← shadcn-ui
├── lib/
│   ├── utils.ts            (cn helper)
│   ├── api/huakai.ts       (getApiUrl)
│   └── dashboard-mock.ts   (MOCK_USAGE / MOCK_PROVIDER_ACCOUNTS / MOCK_CHART_DATA)
├── tailwind.config.ts      ← Gemini 配的 Tailwind v4 形态
├── postcss.config.js       ← @tailwindcss/postcss
├── components.json         ← shadcn-ui config
└── package.json            ← shadcn + recharts + lucide-react 已装
```

**Gemini 装的依赖**：@radix-ui/react-slot / @tailwindcss/postcss / class-variance-authority / clsx / lucide-react / recharts / tailwind-merge / tailwindcss-animate（dev: autoprefixer / postcss / tailwindcss v4）

### dev server

```bash
cd /home/codex/HUAKAI/frontend && npx next dev -p 3000
# 浏览器: http://localhost:3000/dashboard
```

## 你的任务（自由判断）

修 Round 8 broken UI，或者整个推倒重做。**自己读现状 + 自己判断**：

- 现在的 Tailwind v4 setup 是不是真有问题？是不是应该降回 Tailwind v3 + 标准 shadcn-ui 流程？
- 现有组件文件结构 (`app/dashboard/components` vs `components/dashboard` vs `components/layout`) 是不是要整理？
- 重复 nav 现象的根因在哪？
- 实际浏览器渲染问题如何 trace（dev tools console + network + computed styles）？

不要直接照搬 sub2api（它是 Vue，你是 React/Next.js），但**借鉴它的 layout 模式 / 色板 / 信息架构 / 组件命名**。

## 不变约束

- Next.js 14 App Router + TypeScript strict 锁定（不要换 Vite / Vue / Remix）
- 前端目录 `frontend/`
- **可引第三方库** + HUAKAI 自加升级层
- 仍禁 AI emoji（Unicode geometric shapes 允许）
- 仍禁 chatbot bubble / 机器人 icon / AI 装饰文案
- 中文注释规则
- inline style 禁

## 当前 P1 Dashboard 范围

- 状态条（时间 / 后端心跳 / 延迟）
- 6 个核心指标（今日 token / 成本 / 请求数 + 延迟 p50/p95/p99 / 并发 / cache hit ratio + 24h 趋势 / 健康账号比例）
- Top 5 Provider Accounts 表
- 异常告警条件区
- 侧边栏导航（P2-P5 占位）

mock 默认开：`frontend/lib/dashboard-mock.ts`

## 输出报告（自由格式，中文）

```
Round 9 — Codex 接手 P1 Dashboard

诊断 Round 8 broken UI 根因:
[3-5 段]

修复 / 重做策略:
[选 fix-in-place 还是 rewrite，给理由]

What I changed and why:
[3-5 段]

Files changed: [列表]

借鉴源:
- sub2api decomp 第 X 节 → 实现成 ...
- ...

Verification:
- npm run type-check 自跑
- npm run build 自跑
- 浏览器在 :3000 看实物（curl HTTP 200 + 抽查 rendered HTML 几个 class 是否生效）
- 显式列出 'Tailwind 已生效' 证据
```

直接开始。**不要再问我**，按你判断走。
