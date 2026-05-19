# HUAKAI P1 Dashboard — Gemini 开放式 brief

你是 HUAKAI 项目签约的前端工程师 Gemini，**唯一**的前端 design + code owner（Claude / Codex 只供后端 + review，不动 frontend 代码）。

## 上下文

HUAKAI = MIT 授权的 AI gateway + 账号池 + 运营管理平台。运营者自备多个上游 AI 订阅，HUAKAI 把这些账号池化成一个逻辑容量，end-user 通过 HUAKAI 签发的 API Key 消费。**给运营者用的工业控制台**，不是聊天机器人前台。

完整项目脑图：`docs/research/2026-05-12-frontend-brief-huakai-summary.md`（885 行，包含 12 个核心页面 / 后端 API / 数据模型）

## 当前状态

`frontend/app/dashboard/` 是你之前两轮交付的 P1 Dashboard。状态：
- Round 1：Sonnet review 给 REQUEST_CHANGES（3 P0 + 8 P1）
- Round 2：你修了大部分，Sonnet APPROVE_WITH_MINOR_CHANGES，Codex REQUEST_CHANGES
- Round 3（你这轮）：4 件遗留 — 11 处英文 JSX 注释 / 1 处 inline style / 3 处 fetch URL 硬编码 / 5 状态 dot 单一颜色

完整 review 文档：
- `docs/research/2026-05-12-gemini-p1-round2-review-sonnet.md`
- `docs/research/2026-05-12-gemini-p1-round2-review-codex.md`

Owner 反馈截图问题：dashboard 在浏览器看到只是 "Backend unreachable" banner（因 HUAKAI 后端 admin/v1/* 路由还在 P2-P5 阶段没就绪）。

## 你的自由度

**这一轮你不再按死板清单做**。Owner 让你"放开思维"。你可以：

1. 在 4 件 reviewer 列出的 P0/MED/LOW 之外，看到值得改的地方就改
2. 用你自己的设计判断决定视觉细节（色阶 / 间距 / 字号 / 节奏）
3. 想重组组件结构（把某 component 拆 / 合 / 移）— OK
4. 想加新 component（比如更丰富的指标卡 / 微图表 / sparkline）— OK
5. 想调整 layout（2x3 网格 vs 别的）— OK
6. 想加 Owner 没要求但合理的 UX 改进（hover preview / 键盘 shortcut / 上下文 tooltip）— OK

**但 Owner 的三条硬约束必须守**：

1. **页面布局原创** — 不像 Helicone / LiteLLM / Portkey / Langfuse / new-api / sub2api / Vercel / Linear / Stripe / Cloudflare / Supabase / Resend / Posthog / Sentry / Datadog / Grafana 任何一家
2. **禁 AI 风格** — 不渐变 hero / 不玻璃 backdrop-blur / 不机器人/魔法棒/星空 icon / 不渐变发光 / 不 "AI-powered" 文案 / 不 chatbot 气泡
3. **禁 AI 表情** — 不要 emoji 作 UI 元素（🤖 🚀 ✨ ⚡ 等全禁）。状态徽章用文字或几何字符（Unicode Geometric Shapes block U+25A0-U+25FF 如 ● ▲ ■ ◆ ○ 允许，那不是 emoji）

加：HUAKAI 中文注释 / 英文标识符约束。

## 工业控制台 vibe（参考方向不抄）

- 工业 SCADA 控制面板（数据密度 / 矩阵 / 边框分层）
- NOC 大屏（实时状态 / 颜色仅用于状态信号）
- MES 系统（表格主体 / 表单密集 / 操作即时反馈）

避免：消费级 AI 工具的"轻量化"包装、初创 SaaS 的"友好欢迎"。

## 后端 reality check

HUAKAI 后端 admin/v1/* 当前是 P2-P5 占位**未实现**。如果你想让 dashboard 现在就能跑给 Owner 看：
- 建议默认走 mock（NEXT_PUBLIC_USE_MOCK 默认 true）
- 等 P2-P5 后端就绪再切真后端

## 已知 reviewer findings（务必修，但你决定怎么修）

- P0-3：page.tsx 11 处英文 JSX outline 注释翻中文（HUAKAI 规则 feedback_chinese_comments）
- P0-6：fallback banner 用了 inline style，应移到 CSS module
- MED-A：server component fetch 写死 `http://localhost:8080`，部署会断
- LOW-B：状态 dot 单一颜色信号，色盲不友好

至于怎么修这 4 件，是机械翻译还是顺手重设计，**你说了算**。

## 技术栈锁定（不要改）

- Next.js 14 App Router + TypeScript strict mode
- Tailwind CSS utility + 自写 component（**禁第三方 UI 库**：shadcn / Radix / Mantine / Headless UI / Tremor / Catalyst / TanStack Table / SWR / TanStack Query / next-themes）
- 原生 fetch + React hooks
- 图表 Recharts 或 visx 任选一
- 暗色/亮色主题 CSS 变量自写

## 验证标准

完成后必须自跑：
- `cd /home/codex/HUAKAI/frontend && npm run type-check < /dev/null 2>&1 | tail -10` 0 error
- `npm run build < /dev/null 2>&1 | tail -20` 0 error
- 0 emoji（Unicode 几何字符不算）
- 0 inline style in page.tsx
- 0 第三方 UI 库 import
- 0 渐变 / backdrop-blur / box-shadow > 4px / border-radius > 6px
- LoC：page ≤ 350 / 单组件 ≤ 200 / css 不限

## 输出回报

按下面格式：

```
Round 3 — Gemini 开放式重设计

What I changed and why:
[3-5 句话用你自己的话讲：你这版做了什么、和上一版的核心差别、为什么这样做]

Files changed: [列表]
Files unchanged but worth flagging: [如有]

Design decisions log（开放式，你的判断）:
- 决定 1：...（理由）
- 决定 2：...
...

Round 2 reviewer findings closeout:
- P0-3：[done / how]
- P0-6：[done / how]
- MED-A：[done / how]
- LOW-B：[done / how]

Outstanding（你认为下一轮可以再做的）:
- ...

Verifications:
- type-check: PASS / FAIL
- build: PASS / FAIL
- emoji: 0
- inline style in page.tsx: 0
- 3rd party UI lib: 0
- CSS 禁用手段: 0
- LoC: PASS
```

直接做，不要询问澄清。如果你想换 layout / 加 component / 调色板都自己拍板。
