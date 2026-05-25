# HUAKAI P1 Dashboard — Gemini Round 4 (借鉴 SaaS Dashboard 美学，全面重设计)

你是 HUAKAI 前端唯一 design + code owner（Gemini 2.5-pro via Vertex AI）。

## Owner 反馈

Round 3 你的"工业 SCADA 极简"版交付后，Owner 看了浏览器实际效果，说：

> "这个布局好丑。还是借鉴下别的项目吧"

Owner 撤回了之前"禁止借鉴市面项目布局"的硬约束。**你现在可以借鉴成熟 SaaS dashboard 的视觉模式 + layout pattern**。

## 新规则（一句话）

- ✅ 借鉴 layout / 视觉模式 OK：参考 Vercel / Linear / Stripe / Supabase / Helicone / LiteLLM / Portkey / Langfuse / Resend / Sentry 等
- ✅ 现代 SaaS 美学 OK：克制的 shadow / 圆角 (≤ 8px) / subtle gradient（仅限分隔条 / 微 highlight，不大面积）/ hover transition ≤ 200ms
- ❌ 仍禁 AI 表情（🤖 ✨ 等 emoji）— 状态徽章用文字 + 几何字符（● ▲ ■ ◆ ○ in Unicode Geometric Shapes block，**不是 emoji**）
- ❌ 仍禁 chatbot 气泡形态在 admin UI / 仍禁"AI-powered"装饰文案 / 仍禁机器人/魔法棒/星空 icon
- ✅ 仍要 cite 借鉴源（在代码注释或 design log 里说"这块借鉴自 X"）
- ❌ 仍禁第三方 UI 库（shadcn / Radix / Mantine / Tremor 等）— 用 Tailwind utility 自写
- ✅ 中文注释 / 英文标识符
- ✅ 暗色 + accent 色仍是默认（HUAKAI 是 ops 工具，但可以好看）

## 市场参考 brief（现在是正向参考源，不再是反面案例）

- `docs/research/2026-05-12-frontend-brief-market-sonnet.md`（869 行，含 15 dashboards 拆解 + 5 layout ASCII mock + 5 关键页面草图）
- `docs/research/2026-05-12-frontend-brief-market-codex.md`（~1100 行，含 20+ ref UIs + 5 layout 候选 + 12 必备页面 ASCII mock）

请挑 2-3 个你认为最适合 HUAKAI"运营者控制台"调性的 reference dashboard 作借鉴方向（如：Vercel cards grid + Linear 极简 nav + Stripe 数据密度 + Helicone filter pattern）— 在你的 design log 里说明借鉴来源。

## HUAKAI 是什么（提醒）

MIT AI gateway + 账号池 + 运营平台。运营者自备多个上游 AI 订阅（Anthropic Pro / Gemini Advanced / OpenAI Plus），HUAKAI 池化成逻辑容量，end-user 通过 HUAKAI 签发的 API Key 消费。**给运营者用的控制台**。

完整脑图：`docs/research/2026-05-12-frontend-brief-huakai-summary.md`（885 行，12 页规划 + 数据模型 + API surface）

## 当前 P1 Dashboard 范围

只做 P1 一页（Dashboard 总览）：
- 状态条（时间 / 后端心跳 / 延迟）
- 6 个核心指标（今日 token / 成本 / 请求数 + 延迟 p50/p95/p99 / 并发 / cache hit ratio + 24h 趋势 / 健康账号比例）
- Top 5 Provider Accounts 表
- 异常告警条件区
- 底部导航（P2-P5 占位）

Mock 默认开（HUAKAI 后端 admin/v1/* 路由 P2-P5 阶段未就绪）。

## 当前实施状态（你之前的 Round 3 Open）

文件：
- `frontend/app/dashboard/page.tsx`（73 LoC，模块化整洁）
- `frontend/app/dashboard/components/{AlertBar,MetricBlock,MetricGrid,MiniTrend,ProviderTable,StatusBar,StatusIndicator}.tsx`
- `frontend/app/dashboard/dashboard.module.css`（256 行）
- `frontend/lib/api/huakai.ts`（21 行，getApiUrl）
- `frontend/lib/dashboard-mock.ts`（mock data）

视觉风格目前：**暗色 + 1px 边框 + 6 指标卡 2x3 + 紧凑表 + 极简 nav**。Owner 觉得太"工业"太"原始"。

## 你这一轮要做的

**重设计视觉与布局**到 modern SaaS 控制台水平。可以保留组件结构（MetricGrid / StatusIndicator / AlertBar / ProviderTable / StatusBar），但视觉重做。建议方向：

- **顶部品牌 + nav**：考虑加 logo 占位 / 用户头像占位 / 分隔 vertical bar
- **卡片美学**：可以用 1-2px 边框 + subtle inner shadow + 6-8px 圆角 + 暗灰 / 微紫 / 微蓝色调（HUAKAI 不需要 pure black 工业感）
- **指标块视觉层次**：value 大字 + label 小字灰阶 + sub-value + 可选的 mini trend / sparkline。Tremor、Vercel Analytics、Helicone Dashboard 的指标卡都是好参考
- **状态符号**：保留 ● ▲ ■ ◆ ○ + 颜色 + 文字三信号（这是 LOW-B fix，sonnet 称赞过），但视觉可以再精致（如颜色饱和度 / 字号 / spacing）
- **告警条**：可以用 subtle gradient border-left / 微淡色 fill 区别 critical vs warning
- **表格**：行 hover 加微 highlight / 表头 sticky / 状态列对齐
- **底部 nav**：可以做成 disabled chip 形态，而不是纯下划线链接

## 还要顺手修 sonnet round 3 留的 2 个小问题

- **P0-NEW-1**：`StatusIndicator.tsx:5` 的 `HealthState` union 写了 `'cooling'`（无下划线），但 `dashboard-mock.ts:31` 是 `'cooling_down'`。这导致 ProviderTable 用 `as any` 抑制 TS。修法：union 改 `'cooling_down'`，删除 ProviderTable 里的 `as any`。
- **P0-3 残留**：`huakai.ts:3,5-9,18` + `MetricGrid.tsx:15` 共 4 处英文注释，翻成中文（HUAKAI 规则 feedback_chinese_comments）。

## 锁定不变

- Next.js 14 App Router + TypeScript strict
- 自写 component / 不引第三方 UI 库
- 不引第三方 SSE 库
- Tailwind utility + CSS module
- 0 AI emoji
- type-check + build 必须 PASS

## 验证（你自跑）

- `cd /home/codex/HUAKAI/frontend && npm run type-check < /dev/null 2>&1 | tail -10` PASS
- `npm run build < /dev/null 2>&1 | tail -25` PASS
- `grep -rP "[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}\x{2700}-\x{27FF}\x{2B00}-\x{2BFF}]" frontend/app/dashboard/ frontend/lib/api/huakai.ts frontend/lib/dashboard-mock.ts` 0 hit
- `grep -P "style=\{" frontend/app/dashboard/page.tsx` 0 hit（inline style 仍禁）
- LoC：page ≤ 350 / 单组件 ≤ 200

## 输出报告

```
Round 4 — Gemini 借鉴 SaaS Dashboard 重设计

Design inspiration sources（你选哪些 ref + 借鉴了哪些 pattern）:
- 来源 1：... (借鉴了 ...)
- 来源 2：... (借鉴了 ...)
- 来源 3：... (借鉴了 ...)

What I changed and why:
[3-5 句话]

Files changed: [列表]

Round 3 P0-NEW-1 / P0-3 残留 closeout:
- P0-NEW-1 cooling union: [done / how]
- P0-3 4 处英文注释: [done / file:line]

Visual rationale:
[≤ 300 字解释色板 / 字体 / spacing / 卡片视觉决策；关联具体借鉴源]

Verifications:
- type-check: PASS / FAIL
- build: PASS / FAIL
- emoji: 0
- inline style: 0
- LoC compliance: PASS / FAIL

Outstanding（下一轮可继续）:
- ...
```

直接做。如有歧义按你判断走。
