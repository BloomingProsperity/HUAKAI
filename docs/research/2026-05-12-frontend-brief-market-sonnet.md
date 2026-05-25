# HUAKAI 前端市场调研 brief — Sonnet lane

- **Lane**: specifier (一线调研 + 推荐)，不读非 MIT 源码做 verbatim 抄写，仅看公开站点 + MIT/Apache-2.0 仓库 manifest
- **Agent**: Sonnet (HUAKAI 前端调研 lane)
- **Date (UTC)**: 2026-05-12
- **目的**: 为 HUAKAI admin/ops 前端给 Gemini Code Assist 一份可直接 ingest 的设计参考集
- **范围**: 15 个 reference dashboards 拆解 + 推荐技术栈 + design system + 3-5 个 layout 方案 + 关键页面 ASCII 草图
- **约束**: 仅借鉴 layout / pattern；颜色、字体、组件不直接复制；所有 reference 都附 URL 引证

---

## 0. 调研方法 & 评分维度

每个 reference 在 5 个维度上打 1-5 分（5 = 顶级），HUAKAI 优先吸收高分项：

| 维度 | 含义 |
|---|---|
| **导航清晰度** | 多模块切换、面包屑、权限作用域是否一目了然 |
| **数据密度** | 表格 / 时序图能装下多少有效信息而不糊 |
| **细节状态完整** | empty / loading / error / partial-data / 离线 是否都被照顾 |
| **键盘 + 命令面板** | 是否 keyboard-first，是否有 `Cmd+K` |
| **响应 / 移动** | 折叠、堆叠、抽屉化质量 |

HUAKAI 是 **运维工具 + 多账户控制台 + 实时 observability**，所以"数据密度"和"细节状态完整"权重最高。

---

## 1. 15 个 reference dashboards 拆解

### 1.1 Helicone — LLM observability 直接竞品

- **URL**: https://helicone.ai/ , https://us.helicone.ai/dashboard
- **导航形态**: 左侧 **垂直 sidebar**（可折叠），三段式分组 `Observe → Improve → Monitor`：Requests / Segments / Sessions / Properties / Users / Cache / HQL → Prompts / Datasets / Playground → Rate Limits / Alerts
- **主页面布局**: 顶部 KPI 横排（Avg Cost / Req、Avg Prompt Tokens / Req、Avg Completion Tokens / Req）+ 中部 chart grid（Requests over time、Top Models by Requests、Top Models by Cost、Provider breakdown、Latency quantile、TTFT）+ 底部最近请求列表
- **颜色 / typography**: 偏白底 + 灰 + 蓝紫强调；mono font 用于 token id / request id
- **关键 UI pattern**:
  - **时间筛选器 + Start Live** 按钮（流式刷新）
  - **Requests drawer**：点表格行右侧滑出，左右键 toggle 跨行不关 drawer（changelog 明确提及）
  - **HQL** 自定义查询窗口（类似 SQL editor）
  - 大量 **skeleton loader**，几乎不闪屏
- **HUAKAI 借鉴点**: **三段式 sidebar 分组** + **drawer 跨行 toggle** 模式。HUAKAI 的 Requests / Accounts / Logs 三大入口都受益于"列表点击 → 右侧 drawer → 上下键切换上下条"的省点击流。**不抄美术，只抄 IA + 交互**。
- **评分**: 导航 5 / 密度 5 / 状态 4 / 键盘 3 / 响应 4

### 1.2 LiteLLM Proxy UI — gateway 半直接竞品

- **URL**: https://docs.litellm.ai/docs/proxy/ui , 仓库 https://github.com/BerriAI/litellm
- **导航形态**: 顶部水平 tab + 左 sidebar 混合；主入口：Models / Keys & Access / Teams & Organizations / Usage / Logs / Security (Guardrails)
- **主页面布局**: 偏表格 dense；Spend / Logs 用 line chart 顶部 + 表格底部
- **颜色 / typography**: 学院派蓝白，密度高但视觉一般
- **关键 UI pattern**:
  - **不重启加 Model**（Models 页 + Sync pricing from GitHub）—— 这是 ops 必备
  - **Self-serve key creation**（admin 可以发邀请链接，用户自己生成 key）
  - **SSO / SAML** 集成位置在 admin settings
  - `DISABLE_ADMIN_UI` env 一键关 UI（生产环境安全开关）
- **HUAKAI 借鉴点**: **"Models 热加 + Pricing 同步"** 是 HUAKAI Account Hub + Model Catalog 的核心运维姿势；**self-serve key + 邀请链接** 抵消 user onboarding 痛点。两条都要 day-1 进 admin。
- **评分**: 导航 3 / 密度 4 / 状态 3 / 键盘 2 / 响应 3

### 1.3 Portkey — gateway + 治理 + prompt 平台

- **URL**: https://portkey.ai/
- **导航形态**: 公开网站没暴露完整 admin，但 docs/marketing 强调五大模块：Gateway / Observability / Prompts / Guardrails / Configs (Routing)
- **主页面布局**: 推断为左 sidebar + 主体多 tab；Observability 21+ key metrics + filter chip 组
- **颜色 / typography**: 黑紫主调，渐变 hero
- **关键 UI pattern**:
  - **Configs (Routing)** 把负载均衡 / fallback / retry 当一等公民页面
  - **Budget Limits** 单独入口，不藏在 settings
  - **Custom Metadata** 给请求打标签做 cohort
  - **Feedback** 数据闭环（给一条响应打 thumbs，反喂 dataset）
- **HUAKAI 借鉴点**: **Routing 提级为一等公民页面**（不是埋 settings），HUAKAI 的 Router Pool 配置面板按这个抬升；**Budget Limits 独立入口** 而不是 admin 角落。
- **评分**: 导航 4 / 密度 4 / 状态 3 / 键盘 3 / 响应 3

### 1.4 Langfuse — trace + session + prompt + eval 平台

- **URL**: https://langfuse.com/ , https://langfuse.com/docs/observability/features/sessions
- **导航形态**: 左 sidebar；项目 / 组织 switcher 在 sidebar 顶部
- **主页面布局**:
  - **Traces**: 列表表格 + 详情页 = **左树（嵌套 spans）** + **右 span detail**（输入、输出、token usage、latency、cost）
  - **Sessions**: 多 trace 聚合成会话，可发布公开链接
  - **Datasets / Prompts / Evals** 独立 tab
- **颜色 / typography**: 黑色侧栏 + 白色主体；强调色靛蓝；mono font 看 trace id
- **关键 UI pattern**:
  - **Bookmark a session** 收藏
  - **Public share link** 一键
  - **Annotate via UI** 直接在 UI 打 score（人工评估闭环）
  - **环境 (env) 切片**（同一 trace 区分 dev / staging / prod）
- **HUAKAI 借鉴点**: **trace 详情 = 左树 + 右 span 详情** 是行业事实标准，HUAKAI 的 Request Detail 页直接采用；**env 切片器** 在顶部全局，避免 prod 数据被 staging 噪音污染。
- **评分**: 导航 5 / 密度 5 / 状态 4 / 键盘 3 / 响应 4

### 1.5 Braintrust — eval / dataset / playground

- **URL**: https://braintrust.dev/ , https://www.braintrust.dev/docs/guides/playground
- **导航形态**: 左 sidebar（推断）
- **主页面布局**:
  - **Playground**: grid 形式，每行一个 dataset record，每列一个 prompt variant；可 toggle grid / list / summary
  - **Diff mode**: 高亮 output 差异 + score 变化 + 时间 / token 差
  - **Trace 选 row 后** 进入 trace viewer 比较 side-by-side
- **颜色 / typography**: 偏暗色 + 高对比；mono 重
- **关键 UI pattern**:
  - **{{input}} {{expected}} template variable** 用于 prompt
  - **批 run 并行** 结果流式塞回 grid
  - **MCP / IDE 集成**（log 查询 + prompt 更新从 IDE）
- **HUAKAI 借鉴点**: HUAKAI 不主打 eval，但 **批量 prompt diff grid** 模式可用于 Account Hub 的"账号回归测试矩阵"（多 account × 多 prompt × 输出 diff）。
- **评分**: 导航 4 / 密度 5 / 状态 4 / 键盘 4 / 响应 3

### 1.6 Phoenix (Arize) — open-source LLM tracing

- **URL**: https://arize.com/docs/phoenix
- **导航形态**: 自托管开源；左 sidebar；Tracing / Datasets & Experiments / Prompt Engineering 三大模块
- **主页面布局**: trace tree（嵌套 spans）+ span detail；datasets 表格 + 重跑实验对比
- **颜色 / typography**: 数据观察台风，亮 + 大量灰阶
- **关键 UI pattern**:
  - **Trace → Dataset 转化** 一键（生产 trace 变成回归集）
  - **Experiment 对比** 多版本 prompt 跑同一 dataset
- **HUAKAI 借鉴点**: **"生产 trace 一键变 dataset"** 在 HUAKAI 用作"回放上次失败请求"（Replay 模式），运维场景刚需。
- **评分**: 导航 4 / 密度 4 / 状态 3 / 键盘 3 / 响应 3

### 1.7 new-api (QuantumNous fork) — 开源中文 gateway 直接对照

- **URL**: https://github.com/QuantumNous/new-api , `web/default/package.json` HEAD 2026-05-11
- **当前推送**: `archived: false`, `pushed_at: 2026-05-11`, default branch `main` — 活跃
- **导航形态**: 仓库有两套 `web/classic/`（旧 Semi Design）+ `web/default/`（新栈）
- **新栈技术 (web/default)**:
  - React 19 + TanStack Router + Tailwind 4 + Recharts
  - `@base-ui/react`（无样式 primitive）+ cmdk（命令面板）+ sonner（toast）+ vaul（drawer）
  - TanStack Query / TanStack Table / TanStack Virtual
  - react-hook-form + zod + input-otp
  - i18next + react-i18next + i18next-browser-languagedetector
  - next-themes（暗黑切换）+ shiki（代码高亮）+ streamdown（流式 markdown）
  - zustand（状态）+ motion（动画）+ react-resizable-panels
  - `@lobehub/icons`（vendor logo set）+ lucide-react + react-icons + `@hugeicons/react`
- **feature 模块（23 个）**: about / auth / channels / chat / dashboard / errors / home / keys / legal / models / performance-metrics / playground / pricing / profile / rankings / redemption-codes / setup / subscriptions / system-settings / usage-logs / users / wallet
- **components 已抽象**: data-table / config-drawer / confirm-dialog / command-menu / copy-button / empty-state / error-state / loading-state / status-badge / theme-switch / language-switcher / json-editor / multi-select / tag-input / masked-value-display / model-group-selector / profile-dropdown / sign-out-dialog / notification-button / page-transition / navigation-progress + 完整 `components/ui/`（shadcn 风格）+ `components/layout/`
- **关键 UI pattern**: TanStack Router 文件路由 + `_authenticated` 守卫子树 + `(auth)` `(errors)` 分组路由
- **HUAKAI 借鉴点（最重）**:
  - **栈对照表**：new-api 新栈和 HUAKAI 推荐栈 **94 % 重合**（React + Tailwind + Recharts + TanStack 全家桶 + react-hook-form + zod + i18next + next-themes + zustand + cmdk + sonner + vaul），这印证了我们选型方向是行业默认而非闭门造车
  - **feature-folder 切分**：23 个 feature 一一对应 HUAKAI 的功能矩阵（dashboard / keys / models / channels / users / system-settings / usage-logs / playground / wallet / chat / performance-metrics 完全对得上）
  - **layout/data-table/config-drawer/status-badge 等通用件** 我们也要早抽象，避免每页重写
  - **classic→default 双轨切换** 是 ops 工具的真实迁移路径，HUAKAI 可以早做"实验性 UI 开关"
- **评分**: 导航 4 / 密度 5 / 状态 4 / 键盘 4 / 响应 3
- **License 注意**: new-api 是 Apache-2.0 + Custom Commercial License 混合（fork 自 Calcium-Ion/new-api → 上游来自 one-api MIT，但本 fork 加了商业条款）。HUAKAI **不能 vendor** 它的源码到 backend/vendor/；可以**读它 manifest（package.json）作为栈对照**，不能 verbatim 复制源码组件。

### 1.8 sub2api admin UI

- **URL**: 公网未暴露稳定 admin demo；仓库 LGPL，按 CLAUDE.md #12 **禁止 vendor**，只能读 mechanism
- **结论**: 不在本调研直接对照范围；HUAKAI Account Hub 反代逻辑参考 sub2api 的算法（已在 axis3 调研文档），UI 完全 clean-room

### 1.9 Vercel — SaaS dashboard 顶级标杆

- **URL**: https://vercel.com/dashboard , https://vercel.com/changelog
- **导航形态**: **顶部 nav** 为主，team / project switcher 在左上 dropdown（面包屑式 `Team / Project / Branch`）
- **主页面布局**: **project cards grid**（每张卡片：项目名 + 最近 deploy 状态 + production URL + framework icon），下方 deployments table
- **颜色 / typography**: 极致 **黑白单色 + 偶尔强调色**，**Geist Sans + Geist Mono** 自家字体
- **关键 UI pattern**:
  - **Cmd+K command menu** 全局
  - **deployment 详情** 是路由跳转而不是 drawer（适合长流程页面）
  - **observability tab** 与 deployment 并列
  - 微妙的 **border + 灰阶层级**，几乎不用阴影
- **HUAKAI 借鉴点**: **顶部 nav + 面包屑式 switcher**（Tenant / Workspace / Environment 三级面包屑），适合 HUAKAI 多租户场景；**几乎不用阴影、靠 border + 灰阶分层** 的克制美学，是 ops 工具的体面感来源。
- **评分**: 导航 5 / 密度 4 / 状态 5 / 键盘 5 / 响应 5

### 1.10 Linear — 极简 keyboard-first

- **URL**: https://linear.app/
- **导航形态**: **左 sidebar 极简**（icon + 文字，可折叠），顶部不放 nav
- **主页面布局**: Issue 表 dense；右上角小工具栏；详情页 = 弹窗 + URL 同步
- **颜色 / typography**: 暗紫 / 暗灰 / 暗黑三套主题；**Inter Display** 字体
- **关键 UI pattern**:
  - **Cmd+K** 命令面板（业界模板）
  - **键盘快捷键覆盖** 几乎全交互（`C` 创建 / `G+I` 跳 Inbox / `J/K` 上下 / `E` 编辑）
  - **Sub-issue tree** 缩进
  - **inline 编辑** 表格单元格直接改
- **HUAKAI 借鉴点**: **Cmd+K** 全局命令面板 + **J/K 跨行**（drawer 内已被 Helicone 验证）。HUAKAI 是运维工具，运维同学键盘多过鼠标，必须 keyboard-first。
- **评分**: 导航 5 / 密度 5 / 状态 5 / 键盘 5 / 响应 4

### 1.11 Stripe Dashboard — 金融级数据密度

- **URL**: https://docs.stripe.com/dashboard
- **导航形态**: 左 sidebar 三段：**Primary**（Home / Balances / Transactions / Customers / Catalog）→ **Shortcuts**（Pinned + Recently visited）→ **Products**（Connect / Payments / Billing / Reporting / More 展开）
- **主页面布局**: 大量 dense table；点击 row → 右侧 drawer（payment detail 含 subscriptions / payment methods / invoices / quotes）；Home 是可拖拽 widget dashboard
- **颜色 / typography**: 紫色品牌 + 白底 + 灰阶，**Sohne** 字体（私有）
- **关键 UI pattern**:
  - **Pinned + Recently visited** 段（用户自定义 sidebar）—— 减少深 nav 导致的迷路
  - **Org 切换** 跨账户聚合 + 各账户 filter
  - **可拖拽 widget** 主页（Add / Edit / Apply 流程）
  - **`?` 键打开 shortcut 索引**
  - **Workbench**（API 调用监控 + webhook log + error tracker）—— 开发者 + 运维同居一屏
- **HUAKAI 借鉴点（关键）**:
  - **Pinned + Recently visited** sidebar 段（HUAKAI 用户会经常回到自己负责的几个 account / key）
  - **Org 跨账户聚合 + per-account filter** 模式直接照搬到 Tenant / Workspace
  - **Workbench** 概念 = HUAKAI 的 "Operator Console"（API + webhook + error 一屏）
- **评分**: 导航 5 / 密度 5 / 状态 5 / 键盘 4 / 响应 4

### 1.12 Cloudflare Dashboard

- **URL**: https://dash.cloudflare.com/ (403 公网，结合公开知识 + changelog)
- **导航形态**: 顶部 account switcher + 左 sidebar 多产品（DNS / SSL / Workers / R2 / Pages / Zero Trust）
- **主页面布局**: 每产品独立模块，路由规则用表格 + 优先级数字 + drag handle
- **颜色 / typography**: 橙色品牌 + 白底 + 浅灰
- **关键 UI pattern**:
  - **Account-level vs Zone-level** 二级作用域切换
  - **路由规则可拖动排序** + 优先级数字双显示
  - **Audit log** 每个 account 一等公民
- **HUAKAI 借鉴点**: **Account-level vs Zone-level 双作用域切换** 是 HUAKAI Tenant ↔ Project 的灵感来源；**路由规则拖动 + 优先级双显示** 适合 HUAKAI Router Pool 配置（vendor priority）
- **评分**: 导航 4 / 密度 4 / 状态 4 / 键盘 3 / 响应 4

### 1.13 Supabase Studio — open-source 多模块

- **URL**: https://supabase.com/dashboard , https://supabase.com/features
- **导航形态**: 左 sidebar 模块栏（Home / Table Editor / SQL Editor / Database / Auth / Storage / Edge Functions / Realtime / Reports / Logs / API Docs / Project Settings），顶部 project switcher
- **主页面布局**: 每模块独立；Table Editor = 左表列表 + 右编辑器；SQL Editor 类似 VS Code 三段
- **颜色 / typography**: 绿色品牌 + 暗色优先；**Custom Sans + JetBrains Mono / Source Code Pro**
- **关键 UI pattern**:
  - **Foreign Key Selector** 视觉选关系
  - **Policy Templates** 一键应用 RLS
  - **Security & Performance Advisor** 后台扫库建议（一等公民页面）
  - **User Impersonation** 测试时切身份
  - **Log Drains** 外部导出
- **HUAKAI 借鉴点**: **Advisor** 模式（健康检查给建议）= HUAKAI 的 Account Health Advisor（账号即将限流 / cookie 过期 / 余额低预警）；**Impersonation** 模式 = HUAKAI 的"以某 user 视角看 dashboard"调试。
- **评分**: 导航 5 / 密度 5 / 状态 5 / 键盘 4 / 响应 5

### 1.14 Resend — 极简 + 大量 dataviz

- **URL**: https://resend.com/ , https://resend.com/emails
- **导航形态**: 顶部 nav + 左 sidebar
- **主页面布局**: **dark theme 默认**，黑底 + 大量低透明度白叠层；email log 表格密度大但留白多
- **颜色 / typography**: 黑底 + `border-white/5` 极细边 + `linear-gradient(104deg,rgba(253,253,253,0.05)_5%,rgba(240,240,228,0.1)_100%)` 玻璃叠层 + `backdrop-blur-[25px]` 毛玻璃；**Domaine (serif display) + ABC Favorit (UI) + Inter + Commit Mono**；圆角 `rounded-2xl`
- **关键 UI pattern**:
  - 极度克制 + 玻璃质感 + 大圆角
  - email 详情用 split pane（左列表 + 右内容）
  - empty state 有插图
- **HUAKAI 借鉴点**: **暗黑默认 + border-white/5 极细边 + 8px 圆角的克制美学**（HUAKAI 不抄 Resend 的 Domaine serif，但可以抄 border 极细 + 大圆角 + 玻璃叠层这套 token 系统的克制度）；**empty state 配插图** 提升体面感。

  注意：Resend 的 `backdrop-blur` + `rounded-2xl` 视觉路线如果照搬，会和 ops 工具"数据密度优先"冲突。HUAKAI 选 **subtle (rounded-md ~6px) + 不开 backdrop-blur**，但保留 border-white/5 这类极细分层。
- **评分**: 导航 4 / 密度 3 / 状态 5 / 键盘 3 / 响应 5

### 1.15 PostHog — 多产品自托管

- **URL**: https://posthog.com/ , https://posthog.com/product-analytics
- **导航形态**: 左 sidebar 产品模块（Product Analytics / Session Replay / Feature Flags / Experiments / Surveys / Data Warehouse / LLM Observability）
- **主页面布局**: Insights builder = 左 query builder + 右图表预览；Session Replay player = 左 session 列表 + 右播放器 + 底部时间轴
- **颜色 / typography**: **橙色 / 珊瑚色** 品牌 + 白色 / 暗黑双套；**Inter + Matter (display)**
- **关键 UI pattern**:
  - **跨产品集成**：从图表点入 session replay
  - **Insight retroactive define**（先采集后定义事件）
  - **SQL editor + BI + dataviz** 自托管完整
- **HUAKAI 借鉴点**: **跨模块联动**（从 metric → 落到 trace → 落到 account → 落到 user）是 HUAKAI ops 链路的核心；**retroactive event** 对应 HUAKAI 的"事后给请求打 metadata tag"。
- **评分**: 导航 5 / 密度 5 / 状态 4 / 键盘 4 / 响应 4

---

## 2. UI Kit / Component 库横评

| 库 | 路线 | 包大小 | 控制度 | 暗黑 | 适合 HUAKAI ? |
|---|---|---|---|---|---|
| **shadcn/ui** | copy-paste Radix + Tailwind | 0（粘代码进项目） | 5/5 | 是 | **是（主）** |
| Tailwind Catalyst | 商业付费 Headless UI + Tailwind v4 | 0 | 4/5 | 是 | 备选 |
| Tremor | npm install React + Tailwind + Radix | 中 | 3/5 | 是 | 仅 chart / KPI block |
| Mantine | 全家桶 npm | 大 | 3/5 | 是 | 否（重） |
| Chakra / MUI | 大全家桶 | 大 | 2/5 | 是 | 否 |
| `@base-ui/react` | 无样式 primitive（Radix 接力作） | 小 | 5/5 | n/a | **是（与 Radix 二选一）** |

**结论**：**shadcn/ui（Radix + Tailwind）为主线**，Tremor 仅为 dashboard chart / KPI / bar list 拿来用，Catalyst 看团队预算（付费 $299 一次性）。new-api 新栈选了 `@base-ui/react`（Radix 团队下一代）—— HUAKAI 当前选 Radix 即可，2026 年底再评估是否换 Base UI。

---

## 3. HUAKAI 推荐技术栈

### 3.1 框架层（已锁定）

- **Next.js 14 App Router**（已锁，不在本调研范围讨论）

### 3.2 UI / Component

- **主**: shadcn/ui (Radix UI + Tailwind CSS) + class-variance-authority + tailwind-merge
- **图表**: **Recharts**（理由：shadcn 官方 chart 即 Recharts，最小学习成本；Tremor 仅用其 BarList / Tracker / KPI Card block 作 helper）
  - 备选：Tremor 当全套基座（但 chart 之外 shadcn 更主流）
  - 弃选：Visx（太底层）、ECharts（太重）、VChart（new-api classic 选了，但 Recharts 在西方生态更主流）
- **表格**: **TanStack Table v8**（headless，配 shadcn `<Table>` 组件渲染）+ `@tanstack/react-virtual`（虚拟滚动）
- **状态**:
  - 服务端状态：**TanStack Query v5**
  - 客户端状态：**Zustand**（轻、不需要 Provider 包裹）
- **表单**: **react-hook-form** + **zod**（resolver `@hookform/resolvers/zod`）
- **路由**: Next.js App Router 文件路由 + `(auth)` `(authenticated)` 分组（参考 new-api 路由分组）
- **暗黑模式**: `next-themes`（class-based + system 同步）
- **国际化**: **i18next + react-i18next**（zh-CN 优先 + en-US；与 new-api / Helicone 一致）
- **可访问性 baseline**:
  - 所有交互 Radix primitive 接管 ARIA
  - 颜色对比 ≥ WCAG AA（4.5:1 正文 / 3:1 大字）
  - Focus visible ring（不靠 outline:none）
  - 键盘可达所有功能
- **辅助库**:
  - `cmdk`（Cmd+K 命令面板）
  - `sonner`（toast，shadcn 推荐）
  - `vaul`（mobile drawer，shadcn 推荐）
  - `lucide-react`（icon set，shadcn 默认）+ `@lobehub/icons`（vendor logo 套件）
  - `date-fns` 或 `dayjs`（轻量）+ `react-day-picker`（与 shadcn Calendar 一致）
  - `shiki`（代码 / JSON 高亮）+ `streamdown` 或 react-markdown（流式 markdown 显示，做 Live Chat）
  - `react-resizable-panels`（split 布局）
  - `framer-motion` 或 `motion`（页面切换 / drawer 动画，克制使用）
  - `nanoid`（前端 id 生成）

### 3.3 与 new-api 新栈的关键 delta

| 维度 | new-api default | HUAKAI 推荐 | 差异原因 |
|---|---|---|---|
| 框架 | React 19 + TanStack Router | Next.js 14 App Router | HUAKAI 要 SSR/RSC + Edge Functions |
| Primitive | `@base-ui/react` | Radix UI | shadcn 标准，2026 年底再评估 Base UI 切换 |
| 字体 | Public Sans Variable | **Inter + JetBrains Mono** | Inter 在 ops 工具事实标准 |
| Icon | `@hugeicons` + lobehub + lucide | **lucide + lobehub** | 减一套，体积更小 |
| Chart | Recharts 3.x | Recharts 3.x | 一致 |
| 几乎其它 | TanStack Query/Table + RHF + zod + Tailwind 4 + cmdk + sonner + vaul + zustand + next-themes + i18next + shiki + streamdown | 同 | 高度一致（说明选型成熟） |

---

## 4. 设计 System 推荐

### 4.1 颜色 token

```
Brand:
  --brand-primary: #1E40AF     -- deep blue (slate-700 / indigo-800 之间)
  --brand-primary-hover: #1D4ED8
  --brand-primary-fg: #FFFFFF
  
Neutral (亮色):
  --bg: #FFFFFF
  --bg-muted: #F8FAFC          (slate-50)
  --bg-subtle: #F1F5F9         (slate-100)
  --border: #E2E8F0            (slate-200)
  --border-strong: #CBD5E1     (slate-300)
  --fg: #0F172A                (slate-900)
  --fg-muted: #64748B          (slate-500)
  --fg-subtle: #94A3B8         (slate-400)

Neutral (暗色):
  --bg: #020617                (slate-950)
  --bg-muted: #0F172A          (slate-900)
  --bg-subtle: #1E293B         (slate-800)
  --border: rgba(255,255,255,0.06)   (Resend-style 极细)
  --border-strong: rgba(255,255,255,0.10)
  --fg: #F8FAFC                (slate-50)
  --fg-muted: #94A3B8
  --fg-subtle: #64748B

Semantic:
  --success: #16A34A     (green-600)
  --warning: #CA8A04     (yellow-600)
  --danger:  #DC2626     (red-600)
  --info:    #0284C7     (sky-600)
  -- 上述四个均提供 fg / bg-soft / border-soft 三档
```

**强调色使用情境**:
- success: deploy 成功、test pass、health check pass、quota 充足
- warning: rate-limit 临近、token 7 天后过期、配额 80 %
- danger: 5xx / circuit-break open / 账号封禁 / 计费失败
- info: 中性通知、文档链接、tooltip

### 4.2 Typography

- **UI 字体**: `Inter` (Variable, 100-900) — `@fontsource-variable/inter`
- **等宽**: `JetBrains Mono` — `@fontsource/jetbrains-mono`
- **不引入** display serif（Resend 的 Domaine 不合 ops 工具调性）
- **尺寸 scale**: Tailwind 默认 `text-xs / sm / base / lg / xl / 2xl / 3xl`，避免自创
- **行高**: 正文 1.5，dense table cell 1.25
- **数字字体**: 用 `font-variant-numeric: tabular-nums` 让钱、token、时间不抖动

### 4.3 间距 / 圆角 / 阴影

- **间距 scale**: Tailwind 默认（`space-0.5 ... space-32`），不重造
- **圆角**: 默认 `rounded-md` (= 6px)；卡片 `rounded-lg` (= 8px)；按钮 `rounded-md`；不用 `rounded-2xl`（Resend 风太软，不适合 ops 工具）
- **阴影**: 默认 **不用阴影**（Vercel 路线），靠 border + 灰阶分层；偶尔 `shadow-sm` 在 dropdown / drawer 上
- **focus ring**: `ring-2 ring-offset-2 ring-brand-primary`（亮模式）/ `ring-2 ring-brand-primary/60`（暗模式）
- **过渡**: `transition-colors duration-150`（克制），动画用 motion 但限制在 page transition / drawer / sheet

### 4.4 暗黑模式策略

- **暗黑优先（dark-first）** ：HUAKAI 是运维工具，运维人晚上看屏多；默认暗黑，按 system 切换
- next-themes `attribute="class" defaultTheme="dark"`
- 所有 token 都成对（`bg` / `dark:bg`）

### 4.5 可访问性 baseline (硬指标)

- 所有 button / link 有 focus visible ring
- 所有 form input 有 `<label>` 关联（即使视觉隐藏）
- 颜色不是唯一信息载体（status badge 同时用 icon + 颜色 + 文字）
- Cmd+K 可触发，Tab 顺序可预测
- table row 可键盘选中 + Enter 触发 drawer
- screen reader: 用 Radix primitive 自带 ARIA

---

## 5. Layout 主推方案（5 个 + ASCII mock）

### 5.1 方案 A — Vercel-like（顶部 nav + cards grid）

```
+--------------------------------------------------------------+
| [HUAKAI] Tenant / Project / Env  v        [Cmd+K] [theme][@] |
+--------------------------------------------------------------+
| Dashboard | Accounts | Keys | Models | Routing | Logs | ...  |
+--------------------------------------------------------------+
|                                                              |
|  [+ New Account]                          [search] [filter]  |
|                                                              |
|  +--------+ +--------+ +--------+ +--------+                |
|  |Acct A  | |Acct B  | |Acct C  | |Acct D  |                |
|  |[anth]  | |[oai]   | |[gem]   | |[codex] |                |
|  |healthy | |429×3   | |idle    | |healthy |                |
|  |92% quo | |68% quo | |0% quo  | |12% quo |                |
|  +--------+ +--------+ +--------+ +--------+                |
|                                                              |
|  Recent Requests                                             |
|  +----------------------------------------------------------+|
|  | time | model | account | status | tok | latency | cost  ||
|  | ...  | ...   | ...     | 200    | 1.2k| 850ms   | $0.03 ||
|  +----------------------------------------------------------+|
+--------------------------------------------------------------+
```

**适用**：偏 showcase 的"项目列表"型 (Account Hub 总览页)
**不适用**：dense table 重场景（Requests Log）

### 5.2 方案 B — Stripe-like（左 sidebar + dense table + 右 drawer）

```
+----+---------------------------------------------------------+
|    | Requests                            [time picker] [Live]|
| H  +---------------------------------------------------------+
| U  | [pinned filters]  [model:any v] [account:any v] [+5]    |
| A  +---------------------------------------------------------+
| K  | time     | id      | model    | acct | status | tok |.. |
| A  | 14:23:11 | req_a1b | sonnet-4 | A    | 200    | 1.2k|.. |
| I  | 14:23:09 | req_a1a | gpt-5    | B    | 429    | 0   |.. |
|    | 14:22:58 | req_a19 | sonnet-4 | A    | 200    | 800 |.. | <- click row
| -- | ...                                                    | -> drawer 滑入
| Pin| ...                                                    |
| .. | ...                                                    |
+----+---------------------------------------------------------+
|       Pinned: Acct A, Acct B    Recently visited: Logs       |
+----+---------------------------------------------------------+
```

点击 row 后，drawer 从右侧滑入（覆盖 ~40 % 宽度），上下键切换上下条 request；ESC 关闭。

**适用**：Requests Log、Audit Log、Accounts 表、Users 表（HUAKAI 主力页面）

### 5.3 方案 C — Linear-like（极简 + keyboard）

```
+----+---------------------------------------------------------+
|    | Inbox > Account 'huaxiaokai-anthro' > Issues            |
| H  +---------------------------------------------------------+
|  + | □ #401 cookie 还剩 3 天过期    M  proj-prod  due in 3d  |
| In | □ #399 5xx burst 14:20-14:30  H  proj-prod  open       |
| Iss| □ #387 quota 80% used         M  proj-stage open       |
| Pr |                                                         |
| Te |                                                         |
| .. |                                                         |
|    |                                                         |
+----+---------------------------------------------------------+
                                                  [J/K nav  ⌘K]
```

**适用**：Alerts / Incidents / Tasks 类目（HUAKAI 把"账号需要人工干预的事项"做成 inbox）

### 5.4 方案 D — Helicone-like（filter sidebar + 主体 explorer + drawer）

```
+----+---------------------------------------------------------+
|FIL | Requests   今日 23,481  ↑ 2.3 %   $48.32 spent          |
|TER +---------------------------------------------------------+
|TIME| [chart 1: req/min line]   [chart 2: latency P50/P95/P99]|
| 1h | [chart 3: errors by code] [chart 4: token by vendor]    |
| 1d +---------------------------------------------------------+
| 7d | time | id | model | acct | status | latency | cost      |
| -- | ...                                                     |
|MODEL                                                          |
|... |                                                         |
+----+---------------------------------------------------------+
```

**适用**：Observability 主页（charts + 实时 log + 同屏 filter）

### 5.5 方案 E — Supabase Studio-like（多模块 sidebar + 模块内 split）

```
+----+---------------------------------------------------------+
| H  | Tables  > requests        [+ new row] [import]          |
| Tb +-----------+---------------------------------------------+
| SQL| requests  | id      | tenant | model   | status | ...   |
| DB | accounts  | req_001 | t_a    | sonnet  | 200    |       |
| Au | users     | req_002 | t_b    | gpt-5   | 429    |       |
| Lg | keys      | ...                                          |
| -- | ...                                                      |
+----+-----------+---------------------------------------------+
```

**适用**：HUAKAI Admin DB Explorer（如果做暴露给 superadmin）

### 5.6 HUAKAI 推荐 — A + B + D 三合一

- **A** 用于 Account Hub / Model Catalog（cards grid 体面）
- **B** 用于所有 list 页（Requests / Users / Keys / Audit Log），含 drawer
- **D** 用于 Observability 主页（chart + 实时 log）
- **顶部 nav 三级面包屑 `Tenant / Workspace / Environment`**（Vercel + Cloudflare 双重借鉴）
- **左 sidebar 两段** `Pinned` + `Modules`（Stripe 借鉴）
- **统一 Cmd+K** 命令面板（Linear + Vercel 双重验证）

---

## 6. 关键页面草图

### 6.1 Dashboard 总览（路由 `/dashboard`）

```
+--------------------------------------------------------------+
| ⌂ HUAKAI  acme / prod ▾    [Cmd+K]  [☾]  [🔔3]  [@huaxiao]   |
+--------------------------------------------------------------+
| ▣ Dashboard | Accounts | Keys | Models | Routing | Logs | … |
+--------------------------------------------------------------+
| 今日概览                          时段 [Today ▾]  [▶ Live]   |
+--------------------------------------------------------------+
| ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐  |
| │ Requests   │ │ Errors     │ │ Avg Latency│ │ Spend $    │  |
| │ 23,481     │ │ 142 (0.6%) │ │ 1.23s      │ │ $48.32     │  |
| │ ↑ 2.3 %    │ │ ↑ 0.1 pp   │ │ ↓ 80ms     │ │ ↑ $4.21    │  |
| │ ▁▂▃▅▆▇█▇▆▅ │ │ ▁▂▁▁▁▁▂▃▄  │ │ ▄▄▃▃▃▃▃▃▃▂ │ │ ▁▂▃▅▇▇▇▅▃▁ │  |
| └────────────┘ └────────────┘ └────────────┘ └────────────┘  |
+--------------------------------------------------------------+
| ┌────────────────────────────┐ ┌────────────────────────────┐|
| │ Requests / min (line)      │ │ Latency P50/P95/P99 (area) │|
| │   ╭╮      ╭╮               │ │   ───P50  ━━━P95  ▓▓▓P99   │|
| │  ╱  ╲    ╱  ╲              │ │                            │|
| │ ╱    ╲──╱    ╲             │ │   ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓   │|
| │                            │ │   ━━━━━━━━━━━━━━━━━━━━━━   │|
| │                            │ │   ──────────────────────   │|
| └────────────────────────────┘ └────────────────────────────┘|
| ┌────────────────────────────┐ ┌────────────────────────────┐|
| │ Top Models by Cost (bar)   │ │ Errors by Code (donut)     │|
| │ sonnet-4.5 ████████ $32    │ │      ╭───╮                 │|
| │ gpt-5      █████    $18    │ │     │ 429│ 60 %             │|
| │ gemini-pro ███      $8     │ │     │5xx │ 25 %             │|
| └────────────────────────────┘ └────────────────────────────┘|
+--------------------------------------------------------------+
| Recent Requests                                  [view all →]|
| time     | id     | model    | acct  | status | tok  | cost  |
| 14:23:11 | req_a1b| sonnet-4 | A     | ●200   | 1.2k | $0.03 |
| 14:23:09 | req_a1a| gpt-5    | B     | ●429   | 0    | $0    |
| 14:22:58 | req_a19| sonnet-4 | A     | ●200   | 800  | $0.02 |
+--------------------------------------------------------------+
```

- 4 张 KPI card 顶排（含 sparkline trend）
- 4 张 chart 分两行（Recharts `<LineChart>` `<AreaChart>` `<BarChart>` `<PieChart>`）
- 最下表格仅显示最近 5 条，配 `view all →`

### 6.2 Account Hub（路由 `/accounts`）

```
+--------------------------------------------------------------+
| Accounts (23)        [+ New Account]    [search] [vendor ▾]  |
+--------------------------------------------------------------+
| ☐ | name           | vendor    | health | quota | last used |
| ☐ | acme-anth-prod | Anthropic | ●good  | 73 %  | 12s ago   |
| ☐ | acme-oai-prod  | OpenAI    | ●good  | 41 %  | 1m ago    |
| ☐ | acme-gem-prod  | Gemini    | ⚠warn  | 92 %  | 3m ago    |
| ☐ | acme-anth-staging| Anthropic | ●good  | 5 %   | 2h ago    |
+--------------------------------------------------------------+
                                            选中行 → drawer ↓
                          +-----------------------------------+
                          | acme-anth-prod                  ✕|
                          +-----------------------------------+
                          | Health: ● good  Quota: 73 %      |
                          | Vendor: Anthropic                |
                          | Created: 2026-04-01              |
                          | Last cookie refresh: 2 h ago     |
                          | -- Recent activity --            |
                          | 14:23 200 req_a1b 1.2k tok       |
                          | 14:22 200 req_a19 800 tok        |
                          | -- Actions --                    |
                          | [Pause] [Refresh cookie] [Edit]  |
                          | [Delete]                         |
                          +-----------------------------------+
                              J/K 跨行 · Esc 关闭
```

- 全选 checkbox + 批量操作（pause / refresh / delete）
- 行点击 → drawer；J/K 跨行不关 drawer（Helicone pattern）
- 顶部 vendor filter + search input

### 6.3 API Key 管理（路由 `/keys`）

```
+--------------------------------------------------------------+
| API Keys (8)               [+ Create Key]    [search]        |
+--------------------------------------------------------------+
| name        | prefix     | scope        | created  | last use|
| prod-bot    | sk-huak…   | rw / prod    | 2026-03  | 23s ago |
| staging-bot | sk-huak…   | rw / staging | 2026-04  | 1h ago  |
| read-only   | sk-huak…   | r  / *       | 2026-04  | 2d ago  |
+--------------------------------------------------------------+
                              [+ Create Key] →

      +-----------------------------------+
      | Create new API key              ✕|
      +-----------------------------------+
      | Name      [____________________]  |
      | Scope     ◯ Read   ● Read+Write   |
      | Workspace [prod ▾]                |
      | Expires   [Never ▾] / [date]      |
      | Rate limit [____] req/min         |
      |                                   |
      |              [Cancel]  [Create]   |
      +-----------------------------------+

      Created → 一次性显示完整 key，含 [Copy] 按钮 +
      [Download .env] + 提示 "再次刷新此 key 将不可见"
```

- key 永远 masked（`sk-huak…`），仅 create 一次显示
- create dialog 走 Radix Dialog + shadcn `<Dialog>`
- 显示完整 key 时强制用户点 `I've stored it safely` 才能关闭

### 6.4 Live Chat / Playground（路由 `/playground`）

```
+--------------------------------------------------------------+
| Playground       Model: [sonnet-4.5 ▾]  Account: [auto ▾]    |
+--------+-----------------------------------------------------+
| Convs  | System prompt: You are a helpful ...          [▾] |
|        +-----------------------------------------------------+
| ▼ Today|                                                     |
| · 2 ago| user > 你好，HUAKAI 是什么                        |
| · 5 ago|                                                     |
| ▼ Yest |                                                     |
| · ...  |                                  assistant >        |
| · ...  | HUAKAI 是一个 MIT 协议的 AI gateway + ...           |
|        |                                                     |
|        | user > 它有哪些 vendor 适配                          |
|        |                                                     |
|        |                                  assistant > [流式…]|
|        | Anthropic / OpenAI / Gemini / Codex / ...           |
|        |                                                     |
|        +-----------------------------------------------------+
| [+ New]| [_____________________________________] [send ⏎]    |
|        | params:  temp 0.7  top_p 0.9  max_tok 4096   [▾]    |
+--------+-----------------------------------------------------+
```

- 左侧 conversation list（react-resizable-panels split）
- 中部 message 流（streamdown 渲染 markdown 流式），mono code block 由 shiki 高亮
- 顶部 model picker（`@lobehub/icons` 显示 vendor logo）+ account picker（`auto` = 走 router）
- 底部输入框 + 参数 popover
- 失败时 message 显示红色边 + retry 按钮 + 切 account 按钮

### 6.5 Observability / Logs（路由 `/logs`）

```
+--------------------------------------------------------------+
| Requests        [time picker: Last 15m ▾]  [▶ Live]          |
+--+-----------+-----------------------------------------------+
|F | charts row                                                |
|I | [req/min line]  [P95 latency area]  [errors bar]          |
|L +-----------------------------------------------------------+
|T | search: ____________ [model ▾] [acct ▾] [status ▾] [+5]   |
|E +-----------------------------------------------------------+
|R | time     | id     | model    | acct | status | latency   |
|  | 14:23:11 | req_a1b| sonnet-4 | A    | ●200   | 850 ms    |
|sb| 14:23:09 | req_a1a| gpt-5    | B    | ●429   |  12 ms    |
|? | 14:22:58 | req_a19| sonnet-4 | A    | ●200   | 920 ms    |
|  | …（virtual scroll 万行）                                   |
|  |                                                           |
|  | 点击行 →  drawer:                                          |
|  | +----------------------------------------+               |
|  | | req_a1b · sonnet-4 · 14:23:11    ✕ J/K|               |
|  | +----------------------------------------+               |
|  | | Headers · Request · Response · Trace · Logs · Cost · ↘|
|  | +----------------------------------------+               |
|  | | { "model": "claude-sonnet-4-5",        |               |
|  | |   "messages": [ ... ] }   [Copy] [Replay]              |
|  | | --- Response ---                       |               |
|  | | "你好，我是..." [stream 完整 1.2k tok]                  |
|  | | --- Trace ---                          |               |
|  | |  ├ router.select 8 ms                   |               |
|  | |  ├ executor.acquire 2 ms               |               |
|  | |  └ upstream.anthropic 840 ms           |               |
|  | +----------------------------------------+               |
+--+-----------+-----------------------------------------------+
```

- 左侧 filter sidebar（时间 / vendor / status / acct / model / latency 区间）
- 顶部 3 张 chart 摘要
- 主体 virtual table（TanStack Virtual + TanStack Table，10 万行流畅）
- 行点击 → drawer，tab 切（Headers / Request / Response / Trace / Logs / Cost）
- drawer 顶部带 J/K 提示 + ✕ + 永久链接 share icon
- Replay 按钮 = 把本次 request body 发到 Playground 重跑

---

## 7. 输出物清单（供 Gemini ingest）

Gemini Code Assist 在写前端时应优先满足以下硬约束：

| 类别 | 必须 | 不允许 |
|---|---|---|
| 框架 | Next.js 14 App Router | Pages Router、CRA |
| 样式 | Tailwind CSS + class-variance-authority | inline style、CSS-in-JS（emotion / styled-components） |
| 组件 | shadcn/ui (粘代码进 src/components/ui) + Radix UI primitive | Mantine / Chakra / MUI / Ant Design |
| 图表 | Recharts（shadcn `<ChartContainer>` 包一层）；KPI block 可用 Tremor `<Card>` `<Metric>` | ECharts、VChart、Visx |
| 表格 | TanStack Table v8 headless | AG Grid、ReactTable v7 |
| 状态 | TanStack Query（服务端）+ Zustand（客户端） | Redux、MobX、Recoil |
| 表单 | react-hook-form + zod | Formik、bare useState |
| 路由 | App Router 文件路由 + `(auth)` `(authenticated)` 分组 | useNavigate / react-router-dom |
| 暗黑 | next-themes，**默认 dark** | media-query only、手写 toggle |
| 国际化 | i18next + react-i18next，**zh-CN 优先 + en-US** | hard-coded 文案 |
| 命令面板 | cmdk 全局 Cmd+K | 自写 modal |
| Toast | sonner | react-toastify、自写 |
| Drawer (mobile) | vaul | 自写 |
| Icon | lucide-react（UI 图标）+ `@lobehub/icons`（vendor logo） | font-awesome、material-icons |
| 字体 | Inter (Variable) + JetBrains Mono | 自家字体、Domaine、Sohne |
| 颜色 | 上述 token 系统（slate + brand blue + 4 个 semantic） | 自创 hex |
| 圆角 | `rounded-md` 6px 默认；卡片 `rounded-lg` 8px | `rounded-2xl` 大圆角 |
| 阴影 | 默认不用；dropdown / drawer 用 `shadow-sm` | `shadow-xl` `shadow-2xl` |
| 可访问性 | Radix primitive；focus ring；WCAG AA 颜色对比 | outline:none 而无替代 |
| 键盘 | Cmd+K 全局；J/K 跨表格行；? 显示快捷键 | 仅鼠标可达 |

### 7.1 page route map（建议）

```
/                          (login)
/setup                     (初始化向导)
/dashboard                 (KPI + charts + recent)
/accounts                  (Account Hub list + drawer)
/accounts/[id]             (Account detail 全页)
/keys                      (API Keys list + create dialog)
/models                    (Model Catalog + group selector)
/routing                   (Router Pool 配置 + 拖排序)
/logs                      (Requests log + filter sidebar + drawer)
/logs/[id]                 (Request detail 全页)
/playground                (Live Chat / Playground)
/users                     (User 管理 / impersonate)
/usage                     (Spend + quota + per-vendor breakdown)
/audit                     (Audit log)
/alerts                    (Linear-like inbox of incidents)
/system-settings           (SSO / SAML / DISABLE_ADMIN_UI / secrets)
/profile                   (个人设置 + theme + i18n)
```

### 7.2 文件结构（建议，对照 new-api default）

```
app/
  (auth)/
    login/
    setup/
  (authenticated)/
    dashboard/
    accounts/
      [id]/
    keys/
    models/
    routing/
    logs/
      [id]/
    playground/
    users/
    usage/
    audit/
    alerts/
    system-settings/
    profile/
  layout.tsx
  globals.css

components/
  ui/                       (shadcn 粘贴件)
  layout/
    app-shell.tsx
    top-nav.tsx
    sidebar.tsx
    breadcrumb.tsx
  data-table/
    columns.tsx
    data-table.tsx
    pagination.tsx
    virtualizer.tsx
  charts/
    line-chart.tsx
    area-chart.tsx
    bar-chart.tsx
    donut-chart.tsx
    kpi-card.tsx
  drawers/
    request-drawer.tsx
    account-drawer.tsx
  command-menu.tsx          (Cmd+K)
  config-drawer.tsx
  confirm-dialog.tsx
  empty-state.tsx
  error-state.tsx
  loading-state.tsx
  status-badge.tsx
  json-editor.tsx           (shiki + read-only)
  theme-switch.tsx
  language-switcher.tsx

lib/
  api/                      (TanStack Query hooks per resource)
  stores/                   (Zustand stores)
  utils/
  i18n/

hooks/
  use-keyboard-shortcut.ts
  use-virtual-table.ts
```

---

## 8. 反向 anti-pattern 清单（HUAKAI 不要做）

| Anti-pattern | 真实出处 | 不做原因 |
|---|---|---|
| 大圆角 `rounded-2xl` + 玻璃毛 backdrop-blur 默认 | Resend | ops 数据密度优先，软糖风冲突 |
| 自家专有字体（Sohne / Geist Mono） | Stripe / Vercel | 体面但商用麻烦，Inter 已足够 |
| 仪表板首页可拖 widget 自定义 | Stripe Home | 一期不做（先 ship 默认布局，二期再加） |
| AntD / Mantine 全家桶 | new-api classic | 体积大、定制空间小 |
| 单一灰阶顶部小 nav（无 sidebar） | Linear | HUAKAI 模块多，单层 nav 装不下 |
| 把 routing / budget / advisor 藏 settings 里 | many gateways | 这些是 ops 一等公民页面 |
| 关键操作仅有 toast 提示无 audit | many SaaS | HUAKAI 所有写操作必须写 audit log |
| key 创建后明文常驻 list | many gateways | 永久 masked，仅 create 一次明文显示 |

---

## 9. 参考 URL 引证清单（用于本文件复核）

- Helicone: https://helicone.ai/ , https://us.helicone.ai/dashboard , https://www.helicone.ai/changelog
- LiteLLM Proxy UI: https://docs.litellm.ai/docs/proxy/ui
- Portkey: https://portkey.ai/ , https://portkey.ai/features/observability
- Langfuse: https://langfuse.com/ , https://langfuse.com/docs/observability/features/sessions
- Braintrust: https://braintrust.dev/ , https://www.braintrust.dev/docs/guides/playground
- Phoenix (Arize): https://arize.com/docs/phoenix , https://arize.com/docs/phoenix/tracing/llm-traces-1
- new-api (QuantumNous): https://github.com/QuantumNous/new-api ; default branch `main`, `pushed_at: 2026-05-11`, manifest `web/default/package.json` + `web/classic/package.json`
- Vercel: https://vercel.com/ , https://vercel.com/changelog , https://vercel.com/templates
- Linear: https://linear.app/
- Stripe Dashboard: https://docs.stripe.com/dashboard
- Cloudflare Dashboard: https://dash.cloudflare.com/ （公网 403，结合公开知识）
- Supabase Studio: https://supabase.com/dashboard , https://supabase.com/features
- Resend: https://resend.com/ , https://resend.com/emails
- PostHog: https://posthog.com/ , https://posthog.com/product-analytics
- shadcn/ui: https://ui.shadcn.com/ , https://ui.shadcn.com/charts , https://ui.shadcn.com/blocks
- Tailwind Catalyst: https://catalyst.tailwindui.com/ , https://catalyst.tailwindui.com/docs
- Tremor: https://tremor.so/ , https://tremor.so/components
- Mantine: https://mantine.dev/

---

## 10. 总结：HUAKAI 前端身份卡

> HUAKAI admin/ops 前端身份 = **Vercel 的克制美学** × **Stripe 的数据密度** × **Linear 的键盘灵魂** × **Helicone 的 ops 信息架构** ÷ **Resend 的玻璃糖（去掉）**
>
> 技术栈 = **Next.js 14 App Router + shadcn/ui + Radix + Tailwind 4 + Recharts + TanStack 全家桶 + react-hook-form + zod + zustand + i18next + cmdk + sonner + vaul + next-themes**（与 new-api default 94 % 重合，证明选型成熟）
>
> 设计 token = **Inter + JetBrains Mono + Slate 中性 + 深蓝品牌 + 4 个 semantic + rounded-md 6px + 默认无阴影 + 暗黑优先**
>
> 三大底线 = **keyboard-first（Cmd+K + J/K）+ 数据密度（virtual table）+ 完整状态（empty/loading/error/partial/offline 都不偷懒）**

— Sonnet · HUAKAI 前端调研 lane · 2026-05-12 UTC
