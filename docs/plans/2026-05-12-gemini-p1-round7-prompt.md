# HUAKAI 前端 — Round 7

## 项目介绍

HUAKAI 是 MIT 协议的 AI gateway + 账号池 + 运营平台。

- **谁用**：运营者（Owner / SRE / 客服）。**不是** end-user，**不是** 开发者。
- **干啥用**：运营者自备多家 AI 订阅（Anthropic Pro / Gemini Advanced / OpenAI Plus 等），HUAKAI 把它们池化成逻辑容量，end-user 通过 HUAKAI 签发的 API Key 消费。
- **当前进度**：P1 Dashboard 总览页已经做了 6 轮。Round 6 你已经做了 SaaS 美学版（max-w 1400px + StatusIndicator 紧凑 + Badge + MiniTrend），Owner 看完仍不满意。

## Owner 新发言

> "这个介面一点都不好看。我需要 sub2api 那样的！！ 你让 gemini 去拆解下他们的 UI 怎么写的。然后网上去找类似的，或者别的网站是怎么做的。"

Owner 想要的方向：**深度参考 sub2api 的 UI 风格 + 上网找类似的好看的 admin dashboard 项目，在那基础上重做 HUAKAI P1 Dashboard**。

## 你的输入资源

### sub2api 完整前端源码（直接 read）

路径：`~/refs/sub2api/frontend/`

技术栈：Vue 3 + Vite + Tailwind + Pinia + chart.js + @lobehub/icons + vue-i18n

值得拆解的目录：

- `~/refs/sub2api/frontend/src/views/` — 页面级组件
- `~/refs/sub2api/frontend/src/components/` — 复用 UI 组件
- `~/refs/sub2api/frontend/src/styles/` + `style.css` + `tailwind.config.js` — 全套 design token / 色板 / spacing 体系
- `~/refs/sub2api/frontend/src/i18n/` — 中文文案体系
- `~/refs/sub2api/frontend/src/assets/` — 图标 / 视觉素材

请你**自己 read** sub2api 的：
1. Dashboard / 总览页是怎么布局的（grid? card layout? sidebar+main?）
2. 用什么色板（dark / light / 是否多主题？accent 色怎么选？）
3. 用什么字号阶梯 / spacing 节奏
4. 状态徽章 / 数据卡片 / 表格 / 告警条 怎么设计
5. icon 选择思路（@lobehub/icons 是哪家？是 lucide / heroicons / 自绘？）
6. 中文文案怎么组织

### 上网找类似的好看的 admin dashboard

你有 WebFetch 工具。建议搜索 / 直接访问：
- 公开 AI gateway / proxy 项目的截图（new-api / portkey / litellm dashboard）
- Vercel / Linear / Stripe / Supabase / Cloudflare / Sentry / Helicone / Plausible / Posthog dashboard
- dribbble / behance 上的 "admin dashboard" / "ops console" 设计稿

挑 **2-3 个** 你认为最适合 HUAKAI 运营者控制台的方向，把它们的 layout / 视觉 / 信息架构融合到 HUAKAI 上。

## HUAKAI tech stack 锁定

- Next.js 14 App Router + TypeScript strict（**不要**换成 Vue / Vite — 后端契约已稳）
- 前端在 `frontend/` 目录
- **可引第三方库**（Owner 2026-05-12 撤回了"不引第三方"约束）— shadcn / Radix / Tremor / lucide-react / heroicons / chart.js / recharts / @tanstack/react-virtual / 自由
- 前提：HUAKAI 自己在第三方库上加升级层（自有 theme / 自有数据接口 / 自有 admin 行为），不是纯 wrap
- 仍禁 AI emoji（Unicode geometric shapes ●▲■◆○ U+25A0-25FF 不算 emoji 允许用）
- 仍禁 chatbot bubble / 机器人 icon / "AI-powered" 装饰文案

## 当前 P1 Dashboard 范围

只做 P1 一页（Dashboard 总览）：

- 状态条（时间 / 后端心跳 / 延迟）
- 6 个核心指标（今日 token / 成本 / 请求数 + 延迟 p50/p95/p99 / 并发 / cache hit ratio + 24h 趋势 / 健康账号比例）
- Top 5 Provider Accounts 表
- 异常告警条件区
- 底部导航或侧边栏（P2-P5 占位）

数据来源：`frontend/lib/dashboard-mock.ts`（mock 默认开）。

## 已知 Round 6 残留问题（FYI，你自己判断要不要继续接手）

- `frontend/app/globals.css` 缺 `--color-accent-green` / `--color-accent-purple` 变量定义 → MiniTrend 3 张图实际同色
- `frontend/app/layout.tsx` 越界改了 `lang="en"`（UI 全中文）+ 删了 P2-P5 全局 NAV_LINKS
- StatusIndicator compact 模式仅靠 `title` → a11y 弱

Owner 让你重做，你可以**保留** Round 6 哪些好的部分（紧凑布局 / Badge / 趋势图）+ **抛弃** 哪些（fallback layout 越界 / 颜色缺失）— 你自己判断。

## 输出报告（自由格式）

```
Round 7 — Gemini sub2api 拆解 + 网络参考 + 自主重做

sub2api 拆解 (你看到了什么 + 学到了什么):
[3-5 段]

网络参考 (你查了什么 + 借鉴了什么):
[2-3 段]

What I changed and why:
[3-5 段]

Files changed: [...]

Verification:
- type-check / build 自跑
- 浏览器在 :3000 看实物
```

直接开始。
