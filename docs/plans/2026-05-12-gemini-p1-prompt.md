你是 HUAKAI 项目签约的前端工程师 Gemini。请仔细阅读以下完整 brief，理解 Owner 三条硬约束，然后按 §G 任务规格落地 P1 Dashboard 页面到 /home/codex/HUAKAI/frontend/ 目录下。

你可以用：Read / Edit / Write / Bash（npm run type-check / npm run build 是允许的；不允许 npm install 新包除非 brief §F 明示）。完成后按 §I 模板回报。

不要询问澄清，直接做。如有歧义按你的判断走，在回报里说明假设。

完整 brief 内容（一字不漏读完再开工）：

---START_OF_BRIEF---
# HUAKAI 前端原创设计 Brief — 交付给 Gemini

- 日期：2026-05-12（UTC）
- 调用方：HUAKAI PM-Orchestrator（Claude）
- 受派方：Gemini Code Assist（gemini CLI 0.41.2，oauth-personal）
- 项目根：`/home/codex/HUAKAI`
- 前端路径：`frontend/`（Next.js 14 App Router + TypeScript strict mode，当前仅 2 页测试 wedge）

## A. Owner 三条硬约束（违反 = 直接打回重做）

1. **页面布局必须原创**。不允许抄袭或借鉴 Helicone / LiteLLM / Portkey / Langfuse / Braintrust / Phoenix / new-api / sub2api / Vercel / Linear / Stripe / Cloudflare / Supabase / Resend / Posthog / Sentry / Datadog / Grafana 等任何市面上的 AI gateway / observability / SaaS dashboard 项目的页面布局。Layout 必须从 HUAKAI 自身的业务实体与数据密度需求重新推导。
2. **禁 AI 风格**。不允许：渐变 hero 横幅 / 玻璃态毛玻璃卡片（backdrop-blur）/ 机器人/魔法棒/星空/闪电/AI 字样装饰 icon / 渐变发光按钮 / "AI-powered" 装饰文案 / chatbot 气泡形态出现在 admin UI / 生成式占位文案。
3. **禁 AI 表情**。不允许 emoji 作为 UI 元素：包括 🤖 🚀 ✨ ⚡ 🪄 🧠 💡 🔥 ❤️ 🎉 等任何表情符号。状态徽章用文字（"healthy" / "degraded" / "failed"）或几何 dot（实心圆 + 颜色）或边框色块，不用 ✅❌⚠️。

参考：Sonnet 与 Codex 调研 brief（[`docs/research/2026-05-12-frontend-brief-market-sonnet.md`](../research/2026-05-12-frontend-brief-market-sonnet.md)、[`docs/research/2026-05-12-frontend-brief-market-codex.md`](../research/2026-05-12-frontend-brief-market-codex.md)）只能作为**反面案例**阅读 — 看完之后必须做"不像哪个"的对照，不允许做"像哪个"的借鉴。

## B. HUAKAI 是什么（你必须先理解）

HUAKAI 是 MIT 授权的 AI gateway + 账号中心 + 运营管理平台。一句话：**运营者自备多个上游 AI 订阅（Anthropic Pro / Gemini Advanced / OpenAI Plus / Azure 按量 等），HUAKAI 把这些账号池化成一个逻辑容量，end-user 通过 HUAKAI 签发的 API Key 消费这个池子**。

它不是聊天机器人前台。它是给**运营者用的工业控制台**。界面气质应像：
- 工业 SCADA 控制面板（数据密度 / 矩阵 / 边框分层 / 单色为主）
- 网络运维 NOC 大屏（实时状态 / 颜色仅用于状态信号 / 不装饰）
- 制造业 MES 系统（表格主体 / 表单密集 / 操作即时反馈）

要避免的气质：消费级 AI 工具的"轻量化"包装、初创 SaaS 的"友好欢迎"调性。

## C. 完整页面清单（12 页，按优先级 L0/L1/L2 三层）

### L0 — MVP 必须有

| # | 页面 | 用户故事 | 主要 backend endpoint |
|---|------|---------|----------------------|
| P1 | Dashboard 总览 | 运营者打开首页看到全局运营状态 | `GET /admin/v1/usage`、`GET /debug/vars`、`GET /admin/v1/provider-accounts?limit=5` |
| P2 | API Key 管理 | 给 end-user 签发 / 撤销 HUAKAI API Key | `POST /admin/v1/api-keys`、`GET /admin/v1/api-keys`、`POST /admin/v1/api-keys/{id}/revoke` |
| P3 | Provider 账号管理 | 添加 / 启用 / 禁用上游账号 | `GET/POST/PATCH /admin/v1/provider-accounts`、`POST /admin/v1/provider-accounts/{id}/clear-rate-limit` |
| P4 | Pool & Channel 管理 | 创建 Pool、绑定 Channel、配置路由参数 | `GET/POST/PATCH /admin/v1/pools`、`GET/POST /admin/v1/pools/{id}/channels` |
| P5 | Usage & Billing | 查 end-user token 消费 / 成本分解 | `GET /admin/v1/usage?group_by=user`、`GET /admin/v1/billing/claims`、`GET /admin/v1/billing/events` |

### L1 — 完整性

| # | 页面 | 用户故事 |
|---|------|---------|
| P6 | Request & Audit Logs | 调查单个 request 的完整 routing 链路 / attempt chain |
| P7 | Provider Health Map | 实时看所有 Provider × Account 的健康矩阵 |
| P8 | Quota & Rate Limiting | 为 user / key 设置消费上限 + 监控 |

### L2 — Nice-to-have

| # | 页面 | 用户故事 |
|---|------|---------|
| P9 | System Settings / Feature Flags | Edition 切换、特性开关、KMS rotation |
| P10 | Plugin Marketplace（暂搁） | Plugin 启用 / 配置（Phase 5+） |
| P11 | Live Chat Test | 直接对网关发请求做手测（当前 `frontend/app/page.tsx` 的进阶版） |
| P12 | Observability 看板 | Cache hit / 渠道延迟 / 异步处理器进度（当前 `/observability` 进阶版） |

## D. 关键后端 API 细节

### D.1 鉴权
- 客户端入口 `/v1/*` 用 `Authorization: Bearer <HUAKAI_API_KEY>`
- 管理员入口 `/admin/v1/*` 当前无认证（Phase 7 待加）；前端先用 hardcoded `X-Admin-Token` header
- 不要在前端硬编码任何明文 secret

### D.2 错误信封（所有 endpoint 统一）
```json
{
  "error": {
    "code": "rate_limited",
    "message": "human readable",
    "details": {}
  }
}
```
HTTP 状态码 4xx / 5xx；前端按 `error.code` 走对应文案。

### D.3 流式（仅 `/v1/messages`、`/v1/chat/completions`）
- SSE 行协议
- 终态分类 13 种：`cache_hit / fresh / error / tool_use / max_tokens / stop_sequence / cancel_client / cancel_upstream / timeout_upstream / output_token_zero / upstream_5xx / partial_recovered / synthetic_terminal`
- 前端用原生 fetch + ReadableStream 解析（不引第三方 SSE 库）

### D.4 实体核心字段（绝对不省略）

**provider_accounts**:
- account_type：oauth / api_key / service_account
- credential_state：valid / refreshing / failed / revoked
- health_state：operational / degraded / failed / cooling_down
- in_flight_count（并发计数）
- cap_concurrency（并发上限，默认 4）
- cap_quota_daily（日额度，optional）
- quota_status：active / exhausted

**api_keys**:
- key_prefix（前 8 位明文 + 后面 `****`）
- user_id（关联到 users 表）
- name（运营者备注）
- created_at / expires_at（optional）/ revoked_at
- 明文 key 仅在 POST 创建响应里出现一次，前端只能弹一次性 modal

**pool_groups**:
- name
- routing_policy_version
- top_k_default（1-10）
- sticky_wait_timeout_ms / fallback_wait_timeout_ms
- enabled

**usage_records**（聚合时分组用）:
- 维度：tenant / user / api_key / provider_account / pool_group / model
- 指标：token_count、cache_creation_tokens、cache_read_tokens、input_cost、output_cost、cache_cost、request_count、cache_hit_rate

## E. HUAKAI 原创视觉系统（你必须自己推导，下面只给原则）

不给你套色板 / 不给你 layout 截图 / 不给你"参考站"链接。你按以下原则**自己设计**：

### E.1 设计原则（按优先级）
1. **数据密度优先**：每屏可看的有用数据条数 > 留白。表格、矩阵、卡片可以紧凑，但不挤压可读性。
2. **结构表达业务**：HUAKAI 数据模型有 3 层（Pool → Channel → ProviderAccount），UI 必须让 3 层关系一眼可见。
3. **状态信号即颜色**：颜色只用于表达数据状态（healthy / degraded / failed / quota_exhausted / cooling），不用于美化或品牌。
4. **操作即时反馈**：任何写操作（revoke、disable、clear-rate-limit）3 秒内必须有响应（loading → success/failure 文字提示，不用 toast 气泡浮层）。
5. **键盘可达**：所有列表 / 表单可 Tab 键穿过，Enter 提交，Esc 关闭 modal。

### E.2 不允许采用的视觉手段（避雷清单）
- 渐变背景（linear-gradient / radial-gradient）
- 阴影（box-shadow 半径 > 4px）
- 圆角 > 6px
- 玻璃态 / backdrop-blur / 透明叠层
- 动画：除了 loading spinner / 状态变更的 ≤ 200ms fade，不允许任何 hover 弹跳、缩放、彩光
- 大字号 hero（> 32px 标题）
- 图标库整套引入（FontAwesome / heroicons 等）。如必须用图标，自己用 SVG 几何元素手绘（线条、矩形、圆点组合）

### E.3 允许的视觉手段
- 单色 + 1 个 accent 色（你选，但不要 AI 工具流行色：蓝紫渐变 / 青绿 / 樱花粉 全禁）
- 暗色或亮色主题二选一；你判断哪个更适合工业控制台气质，给出理由
- 边框分层（1px solid 网格 / 表格 / 卡片边界）
- 文字层级用字重 + 字号差表达，不用色彩
- 等宽字体用于：数字 / token / id / timestamp / hash
- 状态指示：用文字 + 几何 dot（5-6px 实心圆 + 颜色），dot 颜色与状态绑定

### E.4 字体
- 主字体：自选系统字体栈或单一 webfont（不要 Inter，Inter 在 AI 工具里过度使用 — 给我换一个，比如 IBM Plex Sans / Source Sans / system-ui）
- 等宽字体：JetBrains Mono / Source Code Pro / system monospace

## F. 技术栈（已锁定，不要改）

- Framework：Next.js 14 App Router + TypeScript strict mode
- CSS：Tailwind CSS（保留），但**禁止用任何 component 库**（不要 shadcn / Radix / Mantine / Headless UI / Tremor / Catalyst）。所有组件你从 Tailwind utility 自己写。
- 状态：React 内建（useState/useReducer）+ Zustand（如果跨页共享必须，先证明必须）
- 数据 fetching：fetch + 自己写 hooks（不要 SWR、不要 TanStack Query — 这两个引入了它们自己的 cache 决策，HUAKAI 这边数据语义敏感）
- 表单：原生 form + 自己做 validation。zod 可用于 schema 校验。
- 表格：自己写。**不要 TanStack Table** — 因为引入了大量它的 column model 假设，与 HUAKAI 的字段语义不天然贴合。
- 图表：Recharts 或 visx 二选一。**不要 Tremor** — Tremor 是 SaaS dashboard 套件，气质冲突。
- 暗色/亮色：自己用 CSS 变量切换，不引 next-themes。

理由：所有第三方 UI 库都携带它们自己的设计语言，引入后 HUAKAI 就变成"用了 X 库的项目"，丧失原创性。底层 Tailwind utility + 自写组件给你最大原创空间。

## G. 第一个 Gemini 任务（先做 P1 Dashboard 一页）

**不要一次性写 12 页**。先做 1 页（P1 Dashboard），Owner 看完判定视觉方向通过后再展开。

### 任务规格

文件：`frontend/app/dashboard/page.tsx`（新建 — 不要动当前 `app/page.tsx` 的 ChatPage）
辅助：`frontend/app/dashboard/components/*.tsx`（按需拆）
样式：Tailwind utility + `frontend/app/dashboard/dashboard.module.css`（如果有 utility 不够表达的）

### Dashboard P1 内容

1. **顶部状态条**：当前时刻、当前时区、与后端心跳延迟（数字 ms，用等宽字体）
2. **6 个核心指标块**（2 行 × 3 列，无圆角无阴影）：
   - 今日 token 总量（input / output / cache 三分）
   - 今日成本估算（USD / RMB 双显示，可切换）
   - 今日请求数 + 平均延迟 p50/p95/p99
   - 当前并发数 / 全池 cap_concurrency
   - cache hit ratio（百分比 + 趋势小折线，折线最长 24h）
   - 健康账号比例（healthy / total，含 degraded 警示数）
3. **Top 5 Provider Accounts 紧凑表**（不用 hover effect / 不用 selectable rows）：
   - 列：name / provider / health_state（dot + text）/ in_flight/cap / quota_status / last_dispatch_at
4. **告警区**：仅当任一账号 health_state ∈ {degraded, failed} 时出现一条无背景色的横向通栏（左侧 4px accent 色条），文字写明账号名 + 状态 + 链接到 P3 详情
5. **底部导航 hint**：纯文字链接到 P2-P5（不要按钮）

### Mock 数据约束

后端可能未全部就绪。**先用 mock JSON**（写在 `frontend/lib/dashboard-mock.ts`），用 `process.env.NEXT_PUBLIC_USE_MOCK === '1'` 切换。真实接入留 TODO。

### 验收清单（Owner 看的时候过这几条）

- [ ] 无渐变 / 无 backdrop-blur / 无 box-shadow > 4px / 无圆角 > 6px
- [ ] 无 emoji 字符
- [ ] 无第三方 component 库 import（仅 React、Next、Recharts/visx、Zustand 可选）
- [ ] 数字字段使用等宽字体
- [ ] 状态颜色仅用于状态信号（不用于装饰按钮 / hero / 边框美化）
- [ ] 全页面 Tab 键可达
- [ ] 不像 Helicone / Vercel / Linear / Stripe / Supabase 任一站的 dashboard
- [ ] 不像 ChatGPT / Claude / Gemini / Perplexity 任一 AI 工具的设置页
- [ ] 视觉给出明确"工业控制台"气质（用文字 1-2 句话向 Owner 解释你的视觉选择理由）

### 输出格式

完成后给 Owner（通过我转交）：
1. 主文件路径 + 行数
2. 子组件文件路径
3. CSS 模块文件路径（如有）
4. 1 张 ASCII mockup（终端文字版，~30 行）展示页面结构
5. 视觉理由说明（≤ 200 字）：为什么选这个色板 / 字体 / 布局，关联到"工业控制台"气质
6. mock 数据假设清单（你假设了哪些后端字段还没接通）
7. 已知待补：哪些交互 / 边缘 case 第二轮再做

## H. 工作纪律

- 中文注释 / 英文标识符（[CLAUDE.md `feedback_chinese_comments`](../../CLAUDE.md)）
- 不创建 .env / .env.local（不要写凭据）
- 不安装新 npm 包（除了上面 F 列出的 Recharts/visx/Zustand 可二选一）
- 不修改 `frontend/next.config.mjs` 的 rewrites 规则
- 不动后端代码 — 后端 stub 不通就用 mock
- 文件内单文件 LoC 上限 350（页面），200（组件），CSS module 不限
- 任何对视觉的"创意"选择必须给 1 句话理由，禁止"看起来好看"这种无根据陈述

## I. 完成后回报模板

```
Page: P1 Dashboard
Main file: frontend/app/dashboard/page.tsx (XXX LoC)
Subcomponents:
  - frontend/app/dashboard/components/MetricBlock.tsx (XXX LoC)
  - ...
CSS: frontend/app/dashboard/dashboard.module.css (XXX LoC)
Mock data: frontend/lib/dashboard-mock.ts (XXX LoC)

Visual rationale:
[≤ 200 字解释色板 / 字体 / 布局选择，关联工业控制台气质]

ASCII mockup:
[~30 行终端可视]

Mock assumptions:
- [假设 1]
- [假设 2]
...

Outstanding for round 2:
- [待补 1]
- [待补 2]
...

Compliance:
- No market layout clone: confirm
- No AI style: confirm
- No AI emoji: confirm
- No third-party component lib: confirm
```

不要 paste 整页 tsx 在回报里（太长）；Owner 会直接看文件。
---END_OF_BRIEF---
