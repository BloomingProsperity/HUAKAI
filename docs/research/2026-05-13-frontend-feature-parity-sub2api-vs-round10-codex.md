# 2026-05-13 HUAKAI Round 10 Dashboard vs sub2api Dashboard 功能展示差距清单

| 字段 | 内容 |
|---|---|
| Owner directive | 2026-05-13: "sub2api里面的功能，显示都不能少" |
| 本轮范围 | 只做功能展示项对比和 Round 11 清单；不研究色调；不改 backend；不改 mock schema；不实现 UI |
| sub2api 证据版本 | `Wei-Shaw/sub2api@dbc8ae658cfc1c012160752582925e45115e2f3a` |
| HUAKAI 证据 | Round 10 `frontend/app/dashboard/page.tsx`、`frontend/components/dashboard/*`、`frontend/components/layout/*`、`frontend/lib/dashboard-mock.ts` |
| Observed regions | 25 |
| Inferences | 9，集中在 HUAKAI operator 语义映射和优先级 |
| Open questions | 4 |

说明：sub2api 是用户自助 + 管理后台 + 运维监控混合控制台；HUAKAI Round 10 当前是 operator 总览单页。下面不把语义不一致当作删除理由；不适合直搬的项目会标成 `Safe Equivalent` / `Mandatory Roadmap`。

## 1. sub2api Dashboard 全部展示项（穷举）

### 1.1 User Dashboard

| ID | sub2api 项 | 文件:line | 数据语义 | 视觉形态 |
|---|---|---|---|---|
| S-1 | 页面加载态 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/user/DashboardView.vue:4` | Dashboard 初次加载 | 居中 spinner |
| S-2 | 用户 Dashboard 三段布局 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/user/DashboardView.vue:6-10` | 统计、图表、最近使用 + 快捷操作 | 分段 grid |
| S-3 | Balance | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:5-15` | 用户余额，仅非 simple 模式显示 | StatCard |
| S-4 | API Keys count + active count | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:21-30` | 用户 API key 总数和活跃数 | StatCard |
| S-5 | Today Requests + total requests | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:35-44` | 今日请求数和累计请求数 | StatCard |
| S-6 | Today Cost + total cost，actual / standard 双值 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:49-64` | 今日与累计成本；展示实际成本和标准成本 | StatCard |
| S-7 | Today Tokens，input / output 分解 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:73-82` | 今日 token 总量、输入、输出 | StatCard |
| S-8 | Total Tokens，input / output 分解 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:87-96` | 累计 token 总量、输入、输出 | StatCard |
| S-9 | Performance RPM/TPM | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:101-115` | 请求吞吐和 token 吞吐 | StatCard |
| S-10 | Avg Response Time | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardStats.vue:121-130` | 平均响应耗时 | StatCard |
| S-11 | Date Range Picker | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:4-9` | 图表查询起止日期 | 控件栏 |
| S-12 | Refresh | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:10-12` | 重新拉取全页数据 | Button |
| S-13 | Granularity Day/Hour | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:13-17` | 图表按日/小时聚合 | Select |
| S-14 | Model Distribution doughnut | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:23-34` | 模型 token 分布 | Doughnut chart |
| S-15 | Model Distribution side table | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:35-55` | Model、Requests、Tokens、Actual、Standard | 小表格 |
| S-16 | Model Distribution empty/loading | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:26-33` | 图表加载与无数据 | Overlay / empty text |
| S-17 | Token Usage Trend | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardCharts.vue:60-61` | token 趋势 | Line chart card |
| S-18 | Token trend 五线 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/TokenUsageTrend.vue:74-122` | Input、Output、Cache Creation、Cache Read、Cache Hit Rate | 多线折线图 |
| S-19 | Token trend 双 Y 轴 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/TokenUsageTrend.vue:177-203` | token 数值 + 百分比 | 双轴 chart |
| S-20 | Token trend tooltip 成本 footer | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/TokenUsageTrend.vue:146-160` | 某时点 actual / standard cost | Tooltip |
| S-21 | Recent Usage 标题 + Last 7 Days | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:2-6` | 最近使用记录范围 | Card header + badge |
| S-22 | Recent Usage loading / empty | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:8-13` | 使用记录加载与空态 | Spinner / EmptyState |
| S-23 | Recent Usage 列表项 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:14-31` | 模型、时间、actual/standard cost、tokens | 5 行卡片列表 |
| S-24 | View All Usage | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:34-37` | 跳转完整 usage 页 | Text link + arrow |
| S-25 | Quick Actions 标题 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardQuickActions.vue:2-5` | 快捷操作区 | Card header |
| S-26 | Create API Key | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardQuickActions.vue:7-20` | 跳转密钥页创建新 key | Action button |
| S-27 | View Usage | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardQuickActions.vue:22-35` | 跳转详细日志 | Action button |
| S-28 | Redeem Code | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/user/dashboard/UserDashboardQuickActions.vue:37-50` | 兑换码加余额 | Action button |

### 1.2 Admin Dashboard

| ID | sub2api 项 | 文件:line | 数据语义 | 视觉形态 |
|---|---|---|---|---|
| A-1 | Admin Dashboard 加载态 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:4-7` | 管理统计加载 | Spinner |
| A-2 | Total API Keys + active | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:11-30` | 全局 API key 总量和活跃数 | StatCard |
| A-3 | Service Accounts total / active / error | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:32-55` | 服务账号总量、正常、异常 | StatCard |
| A-4 | Today Requests + total | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:57-75` | 管理端今日请求和累计请求 | StatCard |
| A-5 | New Users Today + total users | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:77-96` | 今日新增用户和总用户 | StatCard |
| A-6 | Today Tokens + actual/account/standard cost | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:99-134` | 今日 token 和三类成本 | StatCard |
| A-7 | Total Tokens + actual/account/standard cost | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:136-170` | 累计 token 和三类成本 | StatCard |
| A-8 | RPM/TPM | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:172-196` | 全局吞吐 | StatCard |
| A-9 | Avg Response + active users | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:198-216` | 平均响应时间、活跃用户数 | StatCard |
| A-10 | Admin date range / refresh / granularity | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:219-250` | 管理图表过滤与刷新 | 控件栏 |
| A-11 | Admin Model Distribution | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:252-268` | 模型分布 | Doughnut + table |
| A-12 | Model source toggle | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/ModelDistributionChart.vue:10-44` | requested / upstream / mapping 模型口径 | Segmented control |
| A-13 | Distribution metric toggle | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/ModelDistributionChart.vue:45-69` | token / actual cost 分布口径 | Segmented control |
| A-14 | Model vs spending ranking toggle | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/ModelDistributionChart.vue:70-95` | 模型分布和用户消费排行切换 | Segmented control |
| A-15 | Admin model table | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/ModelDistributionChart.vue:103-164` | Model、Requests、Tokens、Actual、Account Cost、Standard；可展开用户分解 | Doughnut + expandable table |
| A-16 | Spending ranking doughnut/table | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/charts/ModelDistributionChart.vue:173-231` | 用户排行、请求、tokens、spend | Doughnut + table |
| A-17 | Token Usage Trend | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:268` | 管理端 token 趋势 | Line chart |
| A-18 | User Usage Trend Top 12 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:271-288` | Top 12 用户 token 趋势 | Full-width line chart |
| A-19 | Admin chart default range/granularity | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:368-386` | 默认最近 24h，小时粒度 | State + Select |
| A-20 | Ranking click to admin usage | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/DashboardView.vue:558-566` | 排行点击带用户和日期过滤跳转 usage | Drill-down link |

### 1.3 Admin Ops Monitoring

| ID | sub2api 项 | 文件:line | 数据语义 | 视觉形态 |
|---|---|---|---|---|
| O-1 | Ops 页面总布局 | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:1-40` | 运维监控页面、错误提示、骨架屏、头部 | Ops page |
| O-2 | Platform / group / time range / refresh / alert rules / settings / fullscreen | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:896-990` | 运维过滤、刷新、规则、配置、全屏 | Toolbar |
| O-3 | Last updated + auto refresh countdown | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:878-893` | 数据新鲜度和自动刷新 | Header metadata |
| O-4 | Health score + diagnosis popover | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:994-1101` | 总体健康分和诊断建议 | Gauge + popover |
| O-5 | Realtime QPS/TPS current/peak/avg | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1104-1180` | 实时流量窗口 | Realtime panel |
| O-6 | Requests card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1210-1245` | Requests、tokens、avg QPS、avg TPS | KPI card |
| O-7 | SLA card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1247-1276` | SLA 百分比和异常数 | KPI card + progress |
| O-8 | Duration latency card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1278-1327` | P99/P95/P90/P50/Avg/Max 响应耗时 | KPI card |
| O-9 | TTFT card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1329-1378` | P99/P95/P90/P50/Avg/Max 首 token 耗时 | KPI card |
| O-10 | Request Errors card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1380-1404` | 请求错误率、错误数、业务限流数 | KPI card |
| O-11 | Upstream Errors card | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1406-1430` | 上游错误率、非 429/529 错误、429/529 | KPI card |
| O-12 | System health cards | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1434-1543` | CPU、内存、DB、Redis、goroutines、jobs | 6-card grid |
| O-13 | Job details modal | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1545-1580` | 后台 job 心跳、耗时、成功/失败 | Modal |
| O-14 | Concurrency + account availability | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:42-68` | 并发、切换率、吞吐趋势 | Ops row |
| O-15 | Concurrency dimensions | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsConcurrencyCard.vue:36-48` | platform / group / account / user 维度 | Toggle-driven card |
| O-16 | Concurrency row data | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsConcurrencyCard.vue:50-99` | 账号可用、限流、错误、并发容量、队列、用户并发 | Table/card rows |
| O-17 | Latency / error distribution / error trend | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:70-85` | 延迟直方图、错误分布、错误趋势 | Chart grid |
| O-18 | OpenAI token stats | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:87-94` | 模型级 token 性能统计 | Optional card |
| O-19 | OpenAI token stats table | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/components/OpsOpenAITokenStatsCard.vue:156-248` | model、request count、avg tokens/sec、avg first token、output tokens、avg duration、first-token requests | Table + topN/pagination |
| O-20 | Alert events | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:96-97` | 告警事件 | Card |
| O-21 | System logs | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:99-104` | 系统日志和运行时日志配置 | Table |
| O-22 | Settings / alert rules / error details / request details modals | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/views/admin/ops/OpsDashboard.vue:106-134` | 运维配置、规则管理、错误详情、请求详情 | Dialogs |

### 1.4 Sidebar 与 Header

| ID | sub2api 项 | 文件:line | 数据语义 | 视觉形态 |
|---|---|---|---|---|
| N-1 | Brand logo/site/version | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:9-21` | 站点标识和版本 | Sidebar header |
| N-2 | User nav: Dashboard, API Keys, Usage, Available Channels, Channel Status, My Subscriptions, Buy Subscription, My Orders, Redeem, Affiliate, Profile, custom user menu | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:662-685` | 用户自助主导航 | Sidebar links |
| N-3 | Admin nav: Dashboard, Ops, Users, Groups | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:716-723` | 管理入口 | Sidebar links |
| N-4 | Admin channel group: pricing, monitor | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:723-733` | 渠道配置与监控 | Collapsible group |
| N-5 | Admin nav: subscriptions, accounts, announcements, proxies, risk control, redeem codes, promo codes | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:734-740` | 订阅、账号、公告、代理、风控、兑换码、优惠码 | Sidebar links |
| N-6 | Admin affiliate group | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:741-753` | 邀请、返佣、转账记录 | Collapsible group |
| N-7 | Admin order group | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:754-765` | 支付 Dashboard、订单、套餐 | Collapsible group |
| N-8 | Admin Usage + Settings + admin custom menu | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:767-787` | 用量、系统设置、自定义菜单 | Sidebar links |
| N-9 | Admin My Account section | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:96-118` | 管理员也能访问个人功能 | Sidebar section |
| N-10 | Feature-flag/simple-mode nav filtering | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:646-691` | 根据开关和 simple mode 隐藏菜单 | Nav rule |
| N-11 | Theme toggle | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:142-156` | 明暗主题切换 | Sidebar bottom button |
| N-12 | Collapse + mobile overlay | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppSidebar.vue:158-178` | 侧边栏折叠与移动端遮罩 | Button + overlay |
| N-13 | Header mobile menu + page title/description | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:4-21` | 当前页面标题与说明 | Header left |
| N-14 | Announcement bell | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:24-28` | 公告通知 | Header icon |
| N-15 | Docs link | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:29-39` | 文档入口 | Header link |
| N-16 | Language switcher | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:41-42` | 多语言切换 | Header widget |
| N-17 | Subscription progress mini | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:44-45` | 用户订阅用量/进度 | Header widget |
| N-18 | Balance display | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:47-68` | 用户余额 | Header pill |
| N-19 | User dropdown identity | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:70-106` | 头像、名称、角色、email | Dropdown trigger/menu |
| N-20 | User dropdown links/actions | `Wei-Shaw/sub2api@dbc8ae6:frontend/src/components/layout/AppHeader.vue:118-206` | Profile、API Keys、GitHub(admin)、contact support、restart tour、logout | Dropdown menu |

## 2. HUAKAI Round 10 当前展示项

| ID | Round 10 项 | 文件:line | 数据语义 | 视觉形态 |
|---|---|---|---|---|
| H-1 | Dashboard shell | `frontend/components/layout/AppLayout.tsx:11-25` | Sidebar + Header + main | App layout |
| H-2 | 页面总览头 | `frontend/app/dashboard/page.tsx:178-196` | P1 总览、运营总览、当前时间、数据更新时间 | Header section |
| H-3 | 今日 Token 用量 | `frontend/app/dashboard/page.tsx:127-135` | input + output + cache 合计；显示输入/输出明细 | StatCard |
| H-4 | 今日成本 | `frontend/app/dashboard/page.tsx:136-143` | USD 成本；显示 RMB 折算 | StatCard |
| H-5 | 请求数 | `frontend/app/dashboard/page.tsx:144-151` | 今日网关请求数；显示 P50 延迟 | StatCard |
| H-6 | P95 延迟 | `frontend/app/dashboard/page.tsx:152-159` | P95；显示 P99 | StatCard |
| H-7 | 并发数 | `frontend/app/dashboard/page.tsx:160-167` | 当前飞行中请求和并发容量 | StatCard |
| H-8 | 缓存命中率 | `frontend/app/dashboard/page.tsx:168-175` | cache hit ratio；显示缓存 token | StatCard |
| H-9 | 6-card metric grid | `frontend/app/dashboard/page.tsx:198-210` | 核心指标集合 | Responsive grid |
| H-10 | 24h 缓存命中率趋势 | `frontend/components/dashboard/TrendChart.tsx:23-58` | mock `hit_rate` 单线趋势 | Line chart |
| H-11 | Top 5 供应商账号 | `frontend/app/dashboard/page.tsx:214-256` | 账号、供应商、健康状态、并发、额度 | Table |
| H-12 | 异常告警条件 | `frontend/app/dashboard/page.tsx:74-113` | 非健康、额度耗尽、并发满载账号 | Card list |
| H-13 | 健康账号比例 | `frontend/app/dashboard/page.tsx:261-291` | 健康/总数、降级、失败、说明 | Progress card |
| H-14 | Sidebar nav | `frontend/components/layout/Sidebar.tsx:22-58` | 总览、账号池、密钥、用量、设置；除总览外 disabled | Sidebar links |
| H-15 | Sidebar brand/collapse/bottom badge | `frontend/components/layout/Sidebar.tsx:72-137` | HUAKAI 控制台、折叠按钮、本地开发 v0.1.0 | Sidebar chrome |
| H-16 | Header current time | `frontend/components/layout/Header.tsx:18-30` and `frontend/components/layout/Header.tsx:67-71` | 本地当前时间 | Header pill |
| H-17 | Header backend heartbeat | `frontend/components/layout/Header.tsx:32-64` and `frontend/components/layout/Header.tsx:73-92` | `/debug/vars` 探活状态与延迟 | Header status pill |
| H-18 | Header theme placeholder | `frontend/components/layout/Header.tsx:94-102` | 主题切换占位，当前 disabled | Disabled icon button |
| H-19 | Header user chip | `frontend/components/layout/Header.tsx:103-109` | 管理员身份占位 | Header chip |
| H-20 | Mock usage schema | `frontend/lib/dashboard-mock.ts:6-25` and `frontend/lib/dashboard-mock.ts:51-70` | input/output/cache/cost/request/latency/concurrency/cache hit/health stats | Mock data |
| H-21 | Mock provider account schema | `frontend/lib/dashboard-mock.ts:29-38` and `frontend/lib/dashboard-mock.ts:72-123` | provider account name/provider/health/concurrency/quota/last dispatch | Mock data |
| H-22 | Mock chart schema | `frontend/lib/dashboard-mock.ts:40-49` and `frontend/lib/dashboard-mock.ts:125-155` | hourly input/output/cache read/requests/hit rate/ratio/P95 latency | Mock data |

## 3. 差距表

| sub2api 项 | Round 10 状态 | 缺失类型 | HUAKAI 语义映射 | Round 11 优先级 |
|---|---|---|---|---|
| Balance | 没显示 | 数据 + UI 都缺 | 账户池剩余信用、供应商余额、预计可用天数 | P0 |
| API Keys count + active | 没显示；Sidebar 有 disabled 密钥入口 | 数据 + UI 都缺 | 租户/API 凭证总数、启用数、即将过期数 | P0 |
| Today Requests + total | 今日请求显示；累计请求未显示 | 数据有一部分但 UI 不完整 | 今日请求、累计请求、成功/失败分解 | P0 |
| Today Cost actual/standard | 只有今日 USD/RMB；没有 actual/standard/account cost | 数据缺但 UI 容易 | 实际供应商成本、内部计费成本、标准估算、毛差 | P0 |
| Total Cost | 没显示 | 数据 + UI 都缺 | 累计成本和本周期成本 | P0 |
| Today Tokens input/output | 今日 token 合计和 input/output 已显示；cache 未作为第三项完整展示 | 数据有但 UI 弱 | input/output/cache creation/cache read | P0 |
| Total Tokens | 没显示 | 数据 + UI 都缺 | 累计 token 与本周期 token | P0 |
| RPM/TPM | 没显示 | 数据 + UI 都缺 | Gateway RPM、TPM、按 provider/group 分解 | P0 |
| Avg Response Time | Round 10 有 P50/P95/P99，但无 Avg | 数据缺但 UI 容易 | 平均响应、P50/P95/P99 同卡 | P0 |
| Date Range Picker | 没显示 | 数据 + UI 都缺 | 时间窗口选择器，控制所有图表/表格 | P0 |
| Refresh | Header 有 backend heartbeat 自动探活；Dashboard 无刷新按钮 | UI 缺 | 手动刷新 mock/real dashboard data | P0 |
| Granularity day/hour | 没显示 | 数据 + UI 都缺 | 小时/日聚合切换 | P0 |
| Model Distribution doughnut | 没显示 | 数据 + UI 都缺 | 请求模型、上游模型、映射后模型分布 | P0 |
| Model Distribution table | 没显示；Top 5 provider account 不是等价项 | 数据 + UI 都缺 | Model / Requests / Tokens / Actual / Account / Standard | P0 |
| Token Usage Trend 五线 | 只有 cache hit rate 单线；mock 中已有 input/output/cache_read/requests/latency 未展示 | 数据有但 UI 没显示 | 多线 token 趋势 + cache hit rate | P0 |
| Token trend 双 Y 轴 | 没显示 | UI 组件缺 | token 左轴、百分比右轴 | P0 |
| Token trend tooltip 成本 | 没显示 | 数据 + UI 都缺 | 每点成本、请求数、延迟摘要 | P1 |
| Recent Usage 列表 | 没显示 | 数据 + UI 都缺 | 最近网关请求、模型、时间、成本、tokens、状态 | P0 |
| Recent Usage Last 7 Days badge | 没显示 | UI 缺 | 当前列表窗口标签 | P1 |
| Recent Usage loading/empty | 只有 TrendChart loading；主列表无 | UI 缺 | 所有数据块 skeleton/empty/error | P1 |
| View All Usage | Sidebar 用量 disabled；无 dashboard 链接 | UI 缺 | 跳转 Usage 详情页 | P0 |
| Quick Actions: Create API Key | 没显示 | UI 缺；目标页 disabled | 创建/导入 API key 或租户 key | P0 |
| Quick Actions: View Usage | 没显示 | UI 缺；目标页 disabled | 打开 usage ledger | P0 |
| Quick Actions: Redeem Code | 不适合直搬 | 语义不适合 HUAKAI | 改为手工加款/信用调整/优惠码管理；若启用账户中心则 Mandatory Roadmap | P2 / Roadmap |
| Admin Total API Keys | 没显示 | 数据 + UI 都缺 | 全局 key 资产总览 | P0 |
| Admin Service Accounts total/active/error | 部分通过 Top 5 账号表和健康比例显示；无总数卡 | 数据有一部分但 UI 不完整 | Provider account 总数、可用、错误、冷却、限流 | P0 |
| Admin Today/Total Requests | 今日请求有；累计缺 | 数据有一部分但 UI 不完整 | 全局请求总览 | P0 |
| Admin New Users Today | 没显示 | 数据 + UI 都缺 | 新租户/新用户/新 workspace；若无自助注册则可降级 | P1 |
| Admin Today/Total Tokens + 三类成本 | 今日 token/成本部分有；累计和三类成本缺 | 数据有一部分但 UI 不完整 | 运营成本核算卡 | P0 |
| Admin active users under avg response | 没显示 | 数据 + UI 都缺 | 活跃租户/活跃 key/活跃账号 | P1 |
| Admin Model source toggle | 没显示 | 数据 + UI 都缺 | requested/upstream/mapping 模型口径 | P0 |
| Admin metric toggle tokens/actual cost | 没显示 | 数据 + UI 都缺 | 按 token 或成本看模型分布 | P0 |
| Spending ranking | 没显示 | 数据 + UI 都缺 | 租户/用户/账号池成本排行 | P1 |
| Ranking drill-down to usage | 没显示 | UI 缺 | 点击排行进入 usage 并带筛选 | P1 |
| User Usage Trend Top 12 | 没显示 | 数据 + UI 都缺 | Top tenants/workspaces usage trend | P1 |
| Ops health score | 健康账号比例存在，但不是全局健康分/诊断 | 数据 + UI 都缺 | 网关健康分、诊断建议、runbook hint | P0 |
| Ops realtime QPS/TPS current/peak/avg | 没显示 | 数据 + UI 都缺 | 实时流量面板 | P0 |
| Ops SLA card | 没显示 | 数据 + UI 都缺 | 成功率/SLA、异常数 | P0 |
| Ops request/upstream error cards | 没显示 | 数据 + UI 都缺 | 请求错误率、上游错误率、429/529 单独显示 | P0 |
| Ops duration + TTFT percentile cards | P95/P99 有部分；TTFT 缺；percentile 集合缺 | 数据有一部分但 UI 不完整 | Duration/TTFT P50/P90/P95/P99/Avg/Max | P0 |
| Ops system health CPU/MEM/DB/Redis/jobs | 没显示 | 数据 + UI 都缺 | Runtime health；真实数据后接 observability API | P1 |
| Ops concurrency dimensions | Round 10 只有账号 in_flight/cap；无 platform/group/account/user 维度 | 数据有一部分但 UI 不完整 | account pool concurrency by provider/group/account/user | P0 |
| Ops throughput/switch trend charts | 没显示 | 数据 + UI 都缺 | 调度吞吐、账号切换率、fallback/switch rate | P1 |
| Ops latency histogram/error distribution/error trend | 没显示 | 数据 + UI 都缺 | 延迟分布、错误分类、错误趋势 | P1 |
| OpenAI token stats | 没显示 | 数据 + UI 都缺 | 按 provider/model 的流式性能表；不限 OpenAI，改名为 Model Performance | P1 |
| Alert events | Round 10 有异常告警条件，但无事件流 | 数据 + UI 都缺 | 告警事件列表 | P1 |
| System logs table | 没显示 | 数据 + UI 都缺 | 运维日志、审计日志入口 | P1 |
| Request/error details modals | 没显示 | 数据 + UI 都缺 | 请求详情、错误详情 drill-down | P1 |
| Sidebar user/admin nav 完整性 | 仅 5 项，且 4 项 disabled | UI 缺；信息架构缺 | Operator nav：总览、账号池、凭证、用量、路由、告警、日志、审计、设置、租户/计费 | P0 |
| Sidebar feature flag/simple mode 规则 | 没显示 | 数据 + UI 都缺 | Edition/feature flag 下的菜单可见性 | P1 |
| Sidebar theme toggle | Header 有 disabled placeholder；Sidebar 无可用 toggle | UI 缺 | 可用明暗主题切换 | P1 |
| Header announcement bell | 没显示 | 数据 + UI 都缺 | 公告/系统通知 | P1 |
| Header docs link | 没显示 | UI 缺 | 文档/Runbook 入口 | P1 |
| Header language switcher | 没显示 | UI 缺 | 中英文切换 | P2 |
| Header subscription progress | 不适合直搬 | 语义不适合 HUAKAI | 改为租户 quota / plan usage / budget burn | P1 |
| Header balance display | 没显示 | 数据 + UI 都缺 | 当前账号池余额/预算余额 | P0 |
| Header user dropdown | 只有静态管理员 chip，无 dropdown | UI 缺 | 当前 operator、角色、profile、logout、support/runbook | P1 |
| Header backend heartbeat | Round 10 已有，sub2api Header 无完全等价项 | Implemented Better | 保留；可并入 Ops health | 已有 |

## 4. HUAKAI 视角的语义映射建议

1. `Balance` 不应直译为用户余额；Round 11 应显示“账户池剩余信用 / 供应商余额 / 预计可用天数”，并支持按 provider/account pool 拆分。
2. `API Keys` 应映射为“网关凭证资产”：总 key、启用 key、即将过期 key、异常 key，而不是只显示导航入口。
3. `Today Cost` 的 actual / standard 在 HUAKAI 应扩成“实际供应商成本 / 内部计费成本 / 标准估算 / 差额”，这是运营控制台的成本核心。
4. `Model Distribution` 应保留三口径：请求模型、上游实际模型、映射后模型。HUAKAI 的 provider routing 更需要看“请求模型到实际账号/模型”的偏差。
5. `Recent Usage` 应改成“最近网关请求 ledger”：请求 ID、租户/key、模型、provider account、状态、tokens、成本、耗时。
6. `Quick Actions` 应替换为 operator 操作：新增 provider account、创建 API key、打开 usage logs、查看告警、导出报告。
7. `Redeem / Payment / Subscription / Affiliate` 不适合放在 operator 首页原样展示；应转成 Account Hub / Billing Ops 的 `Mandatory Roadmap`，功能不能删，但可从 Round 11 首页降级为入口或占位。
8. `Ops health score` 应成为 HUAKAI 首页第一屏指标之一，因为 HUAKAI 是 gateway/operator 产品，不只是用户自助控制台。
9. `Available Channels / Channel Status` 应映射为“可调度 provider/account pool 能力矩阵 + 健康状态”，并与账号池表、模型分布、错误率联动。
10. sub2api 的 `Admin Usage` 与 `User Usage` 在 HUAKAI 应合并成“Usage Ledger + Drill-down”，支持从排行、模型分布、错误卡片跳入详情。

## 5. Round 11 实施清单

### 5.1 P0 字段与数据来源

| P0 项 | Round 11 字段建议 | 当前 mock 状态 | 处理建议 |
|---|---|---|---|
| 8 个核心 stat cards | `account_pool_balance_usd`, `key_total`, `key_active`, `today_requests`, `total_requests`, `today_actual_cost`, `today_standard_cost`, `today_account_cost`, `today_input_tokens`, `today_output_tokens`, `today_cache_creation_tokens`, `today_cache_read_tokens`, `rpm`, `tpm`, `avg_response_ms` | 部分已有：tokens/cost/request/latency/concurrency/cache/health | 扩 mock；先 UI 展示 mock |
| Date range / refresh / granularity | `start_date`, `end_date`, `granularity`, `last_refreshed_at` | 无 | 前端状态即可先做 |
| Model distribution | `model`, `requests`, `total_tokens`, `actual_cost`, `account_cost`, `standard_cost`, `source` | 无 | 扩 mock；先用 doughnut + side table |
| Token trend 五线 | `input_tokens`, `output_tokens`, `cache_creation_tokens`, `cache_read_tokens`, `cache_hit_rate` | 已有 input/output/cache_read/hit_rate；缺 cache_creation | 先复用已有 mock，补一个 cache_creation 字段 |
| Recent usage | `id`, `created_at`, `tenant`, `key`, `model`, `provider_account`, `status`, `input_tokens`, `output_tokens`, `actual_cost`, `standard_cost`, `duration_ms` | 无 | 扩 mock；表格 + 移动卡片 |
| Quick actions | `action_id`, `label`, `href`, `disabled_reason?` | 无 | 静态配置即可 |
| Admin service account stat | `provider_accounts_total`, `available`, `degraded`, `failed`, `rate_limited`, `cooling_down` | health stats + provider accounts 部分已有 | 从现有 mock 聚合 |
| Ops realtime / SLA / errors | `qps_current`, `qps_peak`, `tps_current`, `sla_percent`, `request_error_rate`, `upstream_error_rate`, `duration_percentiles`, `ttft_percentiles` | latency 部分已有；QPS/TPS/SLA/errors/TTFT 缺 | 扩 mock；真实 API 后接 observability |
| Header balance/user dropdown | `operator_name`, `role`, `budget_balance`, `notifications_count`, `docs_url` | 静态管理员 chip；无 dropdown | 静态 mock + UI |
| Sidebar complete nav | nav item list with `enabled`, `href`, `badge?`, `feature_flag?` | 5 项，4 disabled | 先展示完整 IA，未实现页可标 disabled/coming soon |

### 5.2 推荐落地顺序

1. 先补 Dashboard 控件栏：时间范围、刷新、粒度、更新时间。它会成为所有图表和表格的共同状态。
2. 把 stat grid 从 6 张扩到 8-10 张，补 Balance/API Keys/RPM/TPM/Avg/Total 指标，保留 HUAKAI 现有成本、延迟、并发优势。
3. 把 `TrendChart` 改成双图区域：左侧 Model Distribution doughnut + table，右侧 Token Trend 五线 + 双 Y 轴。
4. 新增 Recent Usage 表格和 Quick Actions 卡片，形成 sub2api User Dashboard 的第三行等价结构。
5. 扩 Sidebar 到完整 operator IA；Round 11 可以把未实现页标为 disabled，但不能不显示。
6. Header 补 balance/budget pill、docs/runbook、notification bell、user dropdown；主题按钮从 disabled 改为可操作或标明确切占位。
7. 将 Ops P0 指标嵌入首页：Health Score、Realtime QPS/TPS、SLA、Request/Upstream Errors、Duration/TTFT。

### 5.3 估工程量

| 范围 | 估算 |
|---|---|
| 仅前端 mock + UI 展示，沿用 shadcn/recharts | 2-3 天 |
| 加完整响应式表格、loading/empty/error、drill-down query 状态 | 3-5 天 |
| 接真实 backend observability/API | 另计 4-8 天，取决于接口是否已有 |

## Open Questions

1. Round 11 是只做 `/dashboard` 单页吸收 User/Admin/Ops 展示，还是新增 `/admin/ops`、`/usage`、`/accounts` 等真实路由？
2. HUAKAI 是否已有 Account Hub / Billing 的 Owner-approved 范围？若没有，Payment/Subscription/Affiliate 应只做 `Mandatory Roadmap` 可见入口。
3. Round 11 是否允许扩 `frontend/lib/dashboard-mock.ts`？本文件按“不改 mock schema”只列建议，没有执行。
4. Header 是否必须支持真实登录/logout，还是继续以本地管理员占位直到 auth slice？

## Source Coverage Proof

| Region | 贡献 |
|---|---|
| `docs/research/2026-05-12-sub2api-frontend-decomposition.md:640-816` | 既有 User Dashboard 布局、stat、chart、recent usage、quick actions 概览 |
| `docs/research/2026-05-12-sub2api-frontend-decomposition.md:816-943` | Top-N、分页、响应式表格模式 |
| `docs/research/2026-05-12-sub2api-frontend-decomposition.md:942-1135` | Vue→React 映射和 Dashboard 组件层级 |
| `~/refs/sub2api/frontend/src/views/user/DashboardView.vue:1-37` | User Dashboard 页面组成、默认日期、数据加载关系 |
| `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardStats.vue:1-162` | User stat cards 全量字段 |
| `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardCharts.vue:1-103` | User chart controls、model distribution |
| `~/refs/sub2api/frontend/src/components/charts/TokenUsageTrend.vue:1-228` | token trend 五线、双轴、tooltip |
| `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue:1-57` | Recent usage 展示、loading/empty、View All |
| `~/refs/sub2api/frontend/src/components/user/dashboard/UserDashboardQuickActions.vue:1-61` | Quick actions 三个按钮 |
| `~/refs/sub2api/frontend/src/views/admin/DashboardView.vue:1-698` | Admin stat cards、filters、charts、Top 12、ranking loading |
| `~/refs/sub2api/frontend/src/components/charts/ModelDistributionChart.vue:1-320` | Admin model distribution 增强、ranking、toggle、breakdown |
| `~/refs/sub2api/frontend/src/views/admin/ops/OpsDashboard.vue:1-300` | Ops 页面组成和主要区域 |
| `~/refs/sub2api/frontend/src/views/admin/ops/components/OpsDashboardHeader.vue:1-1580` | Ops toolbar、health、realtime、KPI、system health |
| `~/refs/sub2api/frontend/src/views/admin/ops/components/OpsConcurrencyCard.vue:1-260` | 并发/账号可用性维度和字段 |
| `~/refs/sub2api/frontend/src/views/admin/ops/components/OpsOpenAITokenStatsCard.vue:150-260` | OpenAI token stats 控件和表列 |
| `~/refs/sub2api/frontend/src/components/layout/AppSidebar.vue:1-860` | Sidebar 全部用户/管理员导航、主题、折叠 |
| `~/refs/sub2api/frontend/src/components/layout/AppHeader.vue:1-320` | Header widgets 和 user dropdown |
| `~/refs/sub2api/frontend/src/router/index.ts:157-620` | 用户/管理路由存在性和 Dashboard/Ops/Usage 等入口关系 |
| `frontend/app/dashboard/page.tsx:1-296` | HUAKAI Round 10 dashboard 当前展示 |
| `frontend/components/dashboard/TrendChart.tsx:1-59` | HUAKAI 当前趋势图 |
| `frontend/components/dashboard/StatCard.tsx:1-52` | HUAKAI 当前 stat card 形态 |
| `frontend/components/layout/Sidebar.tsx:1-142` | HUAKAI 当前 sidebar |
| `frontend/components/layout/Header.tsx:1-115` | HUAKAI 当前 header |
| `frontend/components/layout/AppLayout.tsx:1-28` | HUAKAI 当前 layout |
| `frontend/lib/dashboard-mock.ts:1-155` | HUAKAI 当前 mock 字段和哪些数据未展示 |

Source files read: `frontend/src/views/user/DashboardView.vue`; `frontend/src/components/user/dashboard/UserDashboardStats.vue`; `frontend/src/components/user/dashboard/UserDashboardCharts.vue`; `frontend/src/components/charts/TokenUsageTrend.vue`; `frontend/src/components/user/dashboard/UserDashboardRecentUsage.vue`; `frontend/src/components/user/dashboard/UserDashboardQuickActions.vue`; `frontend/src/views/admin/DashboardView.vue`; `frontend/src/components/charts/ModelDistributionChart.vue`; `frontend/src/views/admin/ops/OpsDashboard.vue`; `frontend/src/views/admin/ops/components/OpsDashboardHeader.vue`; `frontend/src/views/admin/ops/components/OpsConcurrencyCard.vue`; `frontend/src/views/admin/ops/components/OpsOpenAITokenStatsCard.vue`; `frontend/src/components/layout/AppSidebar.vue`; `frontend/src/components/layout/AppHeader.vue`; `frontend/src/router/index.ts`; HUAKAI `frontend/app/dashboard/page.tsx`; `frontend/components/dashboard/TrendChart.tsx`; `frontend/components/dashboard/StatCard.tsx`; `frontend/components/layout/Sidebar.tsx`; `frontend/components/layout/Header.tsx`; `frontend/components/layout/AppLayout.tsx`; `frontend/lib/dashboard-mock.ts`.

Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-13T07:12:53Z

中文总结：本轮真实观察了 sub2api 用户 Dashboard、管理 Dashboard、运维 Ops、Sidebar、Header，以及 HUAKAI Round 10 的 dashboard/header/sidebar/mock；合理推断集中在 HUAKAI operator 语义映射和 Round 11 优先级；Open Questions 为 4 个。结论是 Round 10 已有运营总览雏形和若干 HUAKAI 特有指标，但相对 sub2api 仍缺 Balance/API Keys、完整 8-card stats、双图表区、Recent Usage、Quick Actions、完整 Sidebar/Header、Admin/Ops 监控指标等 P0/P1 展示面，不能视为功能展示 parity。
