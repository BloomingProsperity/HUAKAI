<!-- 三家融合前端 IA 方案 v2 · sub2api 布局为基 · 非抄袭(repo@sha:path 引用)· 2026-06-14 · 生成: 8-agent 真码调研 sub2api@e34ad2b/new-api@1ac0f58/CLIProxyAPI@2a050dc -->

# HUAKAI 前端信息架构（IA）融合提案 v2 — “深度版”

> 本提案推翻并取代 `docs/frontend/BUILD-SPEC.md`（旧版仅 214 行、信息架构过于扁平,Owner 已明确不可信)。本版以 **sub2api 为布局/形态默认基座**(最成熟),融入 **new-api 的深度**(深层 Settings 树、ratio/定价可视化编辑器、多主题、可配置导航模块、dashboard/log 富度),把 **CLIProxyAPI** 仅作为 ops/config 管理模型折叠进运维能力,并把 **HUAKAI 后端独有能力**(Ed25519 可验证审计账本 + 收据、Hermes 运维助手、provider 账号池 + 凭证保险库、channel-health 冷却状态机、DLQ/告警/可观测、模型同步、L2 缓存、内容审核、多租户)作为前端护城河独立成面。
>
> 证据来源:HUAKAI 真实契约 `docs/openapi/openapi.yaml`(40 tags,行号已核对)。参考项目引用形如 `repo@sha:path`。

---

## 1. 三套界面外壳（Three Shells）

HUAKAI 与三个参考项目一致采用 **public / user-portal / admin-console** 三受众,但外壳数比旧 spec 多一层:**借鉴 sub2api 的“三视觉上下文”而非一个 SPA**(`sub2api@e34ad2b:frontend/src/components/layout/AuthLayout.vue` + `AppLayout.vue` + 独立全屏 `SetupWizardView.vue`)。

### 1.1 外壳清单(4 个外壳,不是 1 个)

| 外壳 | 用途 | 灵感来源 | 进入条件 |
|---|---|---|---|
| **SetupShell**(无 chrome 全屏向导) | 首次安装:数据库/Redis/首管理员/平台密钥 | sub2api `SetupWizardView.vue` + new-api `setup-wizard.tsx`(`new-api@1ac0f58:web/default/src/features/setup/setup-wizard.tsx`) + CLIProxyAPI 的 management secret/bcrypt 引导(`CLIProxyAPI@2a050dc:internal/api/handlers/management/handler.go:200-332`) | `needs_setup=true` 时 boot 重定向 |
| **AuthShell**(居中品牌卡 + 动态渐变球/网格背景) | 登录/注册/找回/passkey/2FA/OAuth 回调/法务/匿名 key 查询/支付落地页 | sub2api `AuthLayout.vue` | `requiresAuth:false` 路由 |
| **PortalShell / AdminShell**(可折叠侧栏 + 粘性玻璃 header + mesh 背景) | 用户门户 + 管理控制台共用一套壳,侧栏按角色重塑 | sub2api `AppLayout.vue`/`AppSidebar.vue` + new-api `authenticated-layout.tsx`(`new-api@1ac0f58:web/default/src/components/layout/components/authenticated-layout.tsx`) | `user` / `admin` guard |
| **PublicSiteShell**(独立 header/footer/主题,不挂 AppLayout) | 落地页、定价市场、排行榜、信任中心 | sub2api `HomeView.vue`/`KeyUsageView.vue` 自带 chrome + new-api 公开 root group | 无 auth |

### 1.2 导航模型与守卫(多层正交门,比旧 spec 的“三 guard”深得多)

借鉴 sub2api 的**正交访问模式分层**(`sub2api@e34ad2b:frontend/src/router/index.ts`):

- **基础 RBAC**:`public` / `user`(session+JWT 或 `hk_` key) / `admin`(platform-admin)。路由组 `app/(public)` / `app/(portal)` / `app/(admin)`。
- **角色再细分**:`admin` 还需区分 `operator` vs `root`(借 new-api `isAdmin()`/`isRoot()`,`new-api@1ac0f58:web/classic/src/hooks/common/useSidebar.js`)——平台 Settings/凭证保险库/审计私钥管理 = root-only。
- **能力门(feature flags)**:仿 sub2api 的“宽松 undefined→显示”策略避免菜单闪烁,从缓存的 public settings 驱动:`flagPayment` / `flagModeration` / `flagAffiliate` / `flagChannelHealth` / `flagHermes` / `flagTrust` / `flagDLQ`——**既隐藏侧栏项,又拦截直接导航**。
- **simple mode**:隐藏一组高级页(subscriptions/vouchers/部分 admin 组),仿 sub2api。
- **backend mode**:整个前端锁给 admin,非 admin 仅留白名单(login / key-usage / setup / payment-result / trust-verify / legal / OAuth callbacks)。
- **合规确认门(HUAKAI 强化)**:仿 sub2api 的 `ADMIN_COMPLIANCE_ACK_REQUIRED` 423 阻塞对话框(`sub2api@e34ad2b:frontend/src/components/admin/AdminComplianceDialog.vue`)——但 HUAKAI 额外把它接到**凭证保险库**和**审计私钥轮换**这类高危操作前。

### 1.3 可配置导航模块系统(直接采纳 new-api 的两层权限模型 — 旧 spec 完全没有)

旧 spec 的侧栏是硬编码。本版采用 new-api 的**两层 AND 模型**(`new-api@1ac0f58:web/classic/src/pages/Setting/Operation/SettingsSidebarModulesAdmin.jsx` + `SettingsSidebarModulesUser.jsx`):

- **`SidebarModulesAdmin`(部署级允许清单)**:JSON 树,section(chat/console/personal/admin)各带 `enabled` + 逐模块 boolean,定义“本部署允许什么”。
- **`sidebar_modules`(用户级隐藏)**:每用户可隐藏任意被允许的模块(从 Profile 编辑)。
- **`HeaderNavModules`(公开顶栏)**:控制 public 顶栏链接(home/console/pricing/docs/about),其中 pricing 带 `requireAuth` 子开关。
- **有效侧栏 = admin-allowed AND user-allowed AND role**。
- **drill-in 上下文侧栏**(new-api default 独有,`new-api@1ac0f58:web/default/src/components/layout/lib/sidebar-view-registry.ts`):进入 `/admin/settings/*` 时根侧栏替换为“系统管理”分类侧栏 + “返回 Dashboard”。HUAKAI 把此模式同样用于 **凭证保险库**和 **Hermes 控制台**(它们各自子导航很深)。

### 1.4 主题 / i18n(融合 new-api 的 9 轴外观抽屉,超越旧 spec 的单一暗色切换)

- **server-selected 主题**(可选,roadmap):仿 new-api `theme.frontend` 双主题机制——HUAKAI 暂只做一套,但**预留 server option 钩子**。
- **per-user 外观抽屉**(采纳 new-api `config-drawer.tsx`,`new-api@1ac0f58:web/default/src/components/config-drawer.tsx`):light/dark/system、色彩预设(default/anthropic/...)、字体、圆角档、密度、侧栏变体(inset/floating)、布局折叠模式、内容宽度、LTR/RTL。
- **i18n**:zh-CN(默认)+ en,惰性加载、浏览器语言检测、`html[lang]` 同步、文档标题由路由 i18n key + 站名构成(仿 sub2api `title.ts`)。
- **路由器运维细节**(全采纳 sub2api):导航进度条、idle 预取、scroll restoration、chunk-load 失败自动 reload。

---

## 2. 完整页面树（每页含内部 sections/tabs）

> 标注:`[S]`=sub2api 启发 / `[N]`=new-api 启发 / `[C]`=CLIProxyAPI 运维模型折叠 / `[H]`=**HUAKAI 独有护城河**。endpoint 行号来自真实契约。

### 2A. PublicSiteShell — `app/(public)`

1. **`/`(落地页)** `[S][N]`
   - Hero + CTA;动画 faux-terminal(curl → `/v1/messages` 逐行揭示,3D tilt)`[S]`;provider/model icon marquee `[N]`;统计项(总请求/账号池数);三卡特性栅格;supported-providers chips(Claude/GPT/Gemini/Antigravity = Supported,More = Coming Soon)`[S]`;footer。
   - **custom-content 模式**:admin 可注入 raw HTML 或全页 iframe(`HomePageContent`)`[N]`。

2. **`/pricing`(模型定价市场)** `[N]` — `GET /v1/pricing/page`、`/v1/pricing/rate-table`
   - 左 facet 过滤栏(vendor/group/quota-type/tag/endpoint-type,各带 live count)`[N]`;card-view ↔ table-view 切换 + “显示 ratio”开关;模型详情 side-sheet(基本信息、支持端点、完整定价表、**tiered/expression 动态定价拆解** = HUAKAI 的 micro-USD/1M token + 图像 `image_base_micro_usd` 公式)`[N]`。

3. **`/rankings`(公开排行)** `[N]` — `GET /v1/public/rankings`
   - Rankings hero、market-share、models section、pulse/activity、增长指标。

4. **`/trust`(信任中心 — 验真入口)** `[H]` — `trust` tag、`/v1/audit/pubkey`(`:3500`)、`/v1/audit/pubkeys`(`:3537`)、`POST /v1/trust/verify`(`:3466`)、`/v1/audit/merkle-tree.json`(`:3678`)
   - **这是 HUAKAI 前端最大护城河之一,任何参考项目都没有。** sections:公钥展示(fingerprint + `/.well-known/huakai-pubkey.json`)、**粘贴收据/证明 JSON → 一键验真**(本地校验 Ed25519 签名 + Merkle 包含证明,显示 ✓/✗ 与链路可视化)、Merkle 树 root 与最近批次、透明度日志说明。

5. **`/key-usage`(匿名 key 查询)** `[S]` — 直连 `/v1/usage`(bearer = key)
   - masked/visible key 输入、日期范围(Today/7d/30d/Custom)、动画 SVG 进度环(总额度/速率窗口 5h-1d-7d/订阅 daily-weekly-monthly/钱包余额)`[S]`、token-stats 16 格、按日用量表、按模型表、skeleton + staggered 入场动画。

6. **`/legal/:documentId`** `[S]` — 动态法务文档(用户协议/隐私)。

### 2B. AuthShell — `app/(auth)`

7. **`/login`** `[S][N]` — `auth`、`user-passkeys`、2FA tags
   - 邮箱/密码;OAuth 矩阵(GitHub/Google/Discord/LinuxDO/DingTalk/WeChat/OIDC 各自 callback)`[S]`;**passkey/WebAuthn 登录** `[N]`;TOTP 2FA modal;Turnstile;登录协议 gate。
8. **`/register`** `[S]` — 邮箱/用户名/密码、邮箱验证、邀请/affiliate code、Turnstile。
9. **辅助页** `[S]`:`/email-verify`、`/forgot-password`、`/reset-password`、OAuth 各回调、DingTalk email-completion、WeChat login/payment callback。
10. **支付落地页(public-capable)** `[S]`:`/payment/result`、`/payment/qrcode`、`/payment/stripe`、`/payment/stripe-popup`、`/payment/airwallex`——**全部按 Owner 冻结要求做清晰标注的 stub handoff,但流程 UI 完整**。

### 2C. PortalShell — `app/(portal)`

11. **`/dashboard`(用户总览)** `[S][N]` — `user-quota`(`/v1/me/quota`)、`GET /v1/me/usage`、analytics
    - 第 1 排 KPI:余额(simple mode 隐藏)、API Keys(总/活跃)、今日请求(+终生)、今日成本(**actual vs standard 双成本** `[S]`);第 2 排:今日 tokens(in/out 拆分)、总 tokens、性能(RPM+TPM)、平均响应。
    - **per-platform 额度可视化卡** `[S]`:每平台 mini-card(actual/today cost/req/tokens)+ daily/weekly/monthly 进度条(按利用率配色,limit=0 disabled 态,窗口重置时间戳,合成 `__other__` 卡)。
    - charts:date-range + day/hour 粒度 + refresh;Model Distribution doughnut + 滚动 per-model 表;Token Usage Trend。
    - Recent Usage(近 7 天 5 条)+ Quick Actions(建 key/看用量/兑换码)`[S]`。

12. **`/playground`(聊天测试台)** `[N]` — `gateway`:`/v1/chat/completions`、`/v1/messages`、`/v1/responses`、`GET /v1/models`
    - **采纳 new-api 的近-IDE 富度**(`new-api@1ac0f58:web/classic/src/components/playground/SettingsPanel.jsx`):group/model/streaming 选择;**每采样参数独立启停开关**(temperature/top_p/penalties/max_tokens/seed...);**raw 自定义 request-body JSON 模式**(与消息列表双向同步);多模态图像输入(URL + 粘贴 base64);per-message copy/edit/regenerate/delete + role 切换 + reasoning/thinking 折叠;stop-generation;**SSE/debug 面板**(请求 payload 实时预览 + 响应/timing tab);import/export/autosave config。
    - **HUAKAI 强化** `[H]`:playground 让用户选自己的某个 `hk_` key 发起;流式渲染需处理 `content_block_start/delta/stop`、tool calls、**image 输出块**(图像模型)。

13. **`/keys`(API Key 管理)** `[S][N]` — `/v1/api-keys`、`user-api-key-controls`、`GET /v1/me/keys/{id}/usage-summary`
    - 可搜索 DataTable;每 key today/total/quota stats;inline group 选择器 + cross-group“smart fusing”retry 标注 `[N]`;endpoint popover(base URLs);Use-Key modal(用法说明 + 连接串/CcSwitch 导入)`[S]`;copy/enable-disable/reset-rate-limit/edit/delete。
    - Create/Edit 三卡侧抽屉 `[N]`:基本(name/group/expiry 快捷预设/批量建 N 个随机后缀)、额度(currency 6 位精度 + 可折叠 raw-quota + unlimited 开关)、访问限制(**model allow/deny 多选 + IP 白名单 CIDR**)`[N]`。

14. **`/usage`(用量记录)** `[S][N]` — `GET /v1/me/usage`、`/v1/me/analytics/time-series`、`GET /v1/generation`
    - tab:Usage / Errors;date-range + 过滤;**token 经济学列**(requested-vs-actual model、prompt/completion + cache read/write 拆分、first-token 延迟、stream/非 stream、soft-error、channel retry chain、订阅扣减 tooltip)`[N]`;error-detail modal;balance history modal。

15. **`/billing`(钱包/充值/账单)** `[S][N]` — `user-recharges`、`/v1/users/me/payments`、`user-vouchers`、`pricing/snapshots`
    - **采纳 sub2api 的完整 recharge/order 流程**(`sub2api@e34ad2b:frontend/src/views/user/PaymentView.vue`)+ new-api 的多支付轨:tab Top-up / Subscribe;AmountInput(预设 chips + 校验自由输入 + 折扣“save X”徽标)`[N]`;PaymentMethodSelector(品牌色 + 费率 + per-method min/max);**live fee/credited-balance 预览**(recharge 倍率 + 百分比费率);**PaymentStatusPanel 内嵌支付状态机**(QR/popup/redirect + 3s 轮询 + 倒计时 + 终态)`[S]`;**全部 PSP handoff = 清晰 stub**(Owner 冻结)`[H-frozen]`。
    - 账单历史/发票 modal(order no/method/quota/paid/status,admin re-complete)。

16. **`/subscriptions`** `[S]` — `/v1/users/me/subscriptions`
    - 空态;per-subscription 卡(到期、daily/weekly/monthly 限额、unlimited 指示、各窗口用量进度)。

17. **`/referrals`(推荐/分销)** `[S]` — `GET /v1/me/invitations`、`/v1/me/referrals`、`/v1/me/referrals/rewards`
    - stat 卡(有效返佣率/邀请数/可用-总-冻结额度);affiliate code + invite link(copy);tips 面板;**quota→balance 转账卡**(compliance gate)`[S]`;invitee 表(email/username/累计返佣/加入时间)。

18. **`/checkin`(每日签到)** `[N]` — `user-checkin` tag
    - 签到日历(streak、本月/累计奖励、per-day reward cell、Turnstile)`[N]`。

19. **`/notifications`** `[S][N]` — `user-notifications`、`announcements`
    - 公告/通知列表 + read-status;announcement bell 在 header。

20. **`/account`(账户/安全)** `[S][N]` — `auth`、`user-passkeys`、2FA、`sessions`
    - profile + avatar 卡;改密;**TOTP 2FA 卡**(setup modal + QR + backup codes + disable)`[N]`;**passkey/WebAuthn 注册/解绑**(secure-verification gate)`[N]`;**身份绑定 tab**(8 OAuth provider + 动态 custom-OAuth bind/unbind)`[N]`;**通知渠道 tab**(email/Webhook/Bark/Gotify + webhook payload 样例)`[N]`;sessions 列表 + 撤销;余额通知阈值;danger-zone 删号(输用户名确认 + Turnstile)`[N]`。

21. **`/audit`(我的可验证收据)** `[H]` — `user-audit`、`/v1/receipts/{request_id}`(`:2815`)、`/v1/receipts/{request_id}/verify`(`:2844`)、`/v1/receipts/{request_id}/disputes`(`:2875`)
    - **护城河:用户端信任 UX。** 每条请求的可验证收据列表;点开 → 收据详情(Ed25519 签名、cost breakdown、模型/账号脱敏)+ **一键 verify**(✓ 链上包含);**发起 dispute**(争议流程)。无任何参考项目有此面。

### 2D. AdminShell — `app/(admin)`

22. **`/admin`(运维驾驶舱)** `[S][N][H]` — `admin-usage`、`admin-billing`、`admin-alerting`、system-health 聚合
    - **三重成本模型 KPI**(actual / account-side / standard,per-token)`[N]`;RPM/TPM;today/total req、accounts(normal vs error)、users、tokens;`[H]` 额外顶排:**池健康摘要、DLQ 深度、告警状态、channel-health 冷却中账号数、收入快照**(`14_UI_CONTRACTS.md` §Dashboard 要求)。
    - charts toolbar(date-range 默认 24h + 粒度);Model Distribution ↔ **user-spending ranking**(top-N,click-through 到 Usage 预填过滤)`[N]`;per-user usage trend(Top 12)`[N]`。

23. **`/admin/providers`(Provider 目录)** `[N]` — `admin-models`/providers
    - CRUD;vendor type tabs(live count + icon)`[N]`。

24. **`/admin/channels`(渠道 + 测试模板)** `[S]` — `GET /admin/v1/channels`、`/admin/v1/channel-test-templates`
    - DataTable;create/edit dialog(Basic tab);定价/映射;test-template manager。

25. **`/admin/accounts`(上游 provider 账号 — 核心) `[S][N][H]`** — `admin-accounts`(`/admin/v1/provider-accounts` `:9485`)、`admin-channel-health`
    - **采纳 sub2api/new-api 最深页**(`sub2api@e34ad2b:.../AccountsView.vue` + `new-api@1ac0f58:.../channels/modals/EditChannelModal.jsx`):auto-refresh 开关(5/10/15/30s)+ tools menu(sync-from-CRS/import/export/error-passthrough/TLS-fingerprint profiles)+ column-visibility;bulk bar(选中 OR filter-snapshot);宽表(capacity/quota 徽标、schedulable 开关、billing rate multiplier、proxy 绑定 + revert、priority、windows);row actions(test/stats/re-auth/refresh-token/recover-state/reset-quota/set-privacy);create/edit modal(per-platform stepped OAuth + gemini 子类型 + model whitelist + quota cards)。
    - **HUAKAI 独有强化** `[H]`:`/admin/v1/provider-accounts/{id}/clear-rate-limit`(`:9612`)、`/test`(`:9638`)、`/health`(`:9670`)、`/channel-health/pause|resume|force-active`(`:9871-9921`)——**channel-health 冷却状态机**直接成行内动作 + 状态可视化(active/cooling/paused/forced)。

26. **`/admin/pools`(账号池 + 路由) `[H]`** — `admin-pools`(`/admin/v1/pools` `:9373`)、`/v1/admin/routes`
    - **护城河:relay-station 身份级 Pool**(`14_UI_CONTRACTS.md` §Pooling Groups)。sections:Pool 列表 + 详情、增删成员 Account、per-Pool 健康 + 余额视图、per-Account hot-spot 诊断、**Route 解析可视化**(显示某请求为何落到某 Account、sticky-session 分布)。参考项目均无 Pool 概念。

27. **`/admin/credentials`(凭证保险库 + 获取) `[H][C]`** — `admin-credential-acquisition`、`/admin/v1/credentials`(paste `:10255`/renew-status `:10271`/cli-import `:10304`)、`/admin/v1/provider-accounts/{id}/credentials/*`(`:9726-9836`)、`credential-acquisitions/*`(`:10032-10171`)
    - **护城河 + CLIProxyAPI 运维模型折叠。** sections:凭证列表(per-credential rotate `:9807` / state `:9836`)、**OAuth 获取向导**(start URL → 浏览器打开 → **粘贴 callback URL headless 模式 + 状态轮询** — 直接来自 `CLIProxyAPI@2a050dc:internal/tui/oauth_tab.go` 与 `internal/api/handlers/management/auth_files.go` 的六-provider 模型)、**CLI import / paste-config**(`CLIProxyAPI@2a050dc:.../config_lists.go`)、renew-status 预轮换窗口。root-only,合规 gate。

28. **`/admin/users`** `[S][N]` — `admin-users`(`/admin/v1/users`)
    - 宽配置表(~20 列,记忆列布局)`[S]`;role/status/group 过滤 + 动态属性过滤;row menu(查 keys、allowed groups、deposit/withdraw、platform quota、balance history、reset 2FA/passkey、delete)`[N]`;create/edit/balance/history modals。

29. **`/admin/keys`** `[S]` — `admin-api-keys`。平台级 key 管理表 + CRUD。

30. **`/admin/billing`** `[S][N]` — `admin-billing`、`/admin/v1/balances`、`GET /admin/v1/usage`
    - claims、balances、usage records(pool-aware 对账视图,`14_UI_CONTRACTS.md` §Billing);usage 表(~16 列)+ 分布 charts + export/cleanup。

31. **`/admin/pricing`(费率/定价 — 最深 Settings 子树) `[N]`** — `admin-pricing`、`/v1/admin/cache-price-overrides`、`pricing/snapshots`
    - **直接采纳 new-api 三级深定价树**(`new-api@1ac0f58:web/classic/src/components/settings/RatioSetting.jsx` + `pages/Setting/Ratio/*`):inner card-Tabs(Model Pricing / Group / Unset-price Models / Upstream Price Sync / Tool-call Pricing)。
    - **Model Pricing 可视化编辑器**(`ModelPricingEditor.jsx`):左可搜索模型表(billing-method 列 = per-request/per-token/tiered/expression);右详情(per-token base/input/completion/cache-read/cache-creation + 扩展图像/音频价;per-request 固定价;**tiered/expression 编辑器** `TieredPricingEditor.jsx` = 条件构建器 time/header/body-field + 乘性附加费 + **live evaluator**)。
    - **Manual JSON 编辑器**(raw ratio maps);**Upstream Price Sync**(拉上游费率 diff + conflict-confirm);**Group ratio rules** + **Tool-call pricing**($/1K calls + model-prefix override)。

32. **`/admin/payments`** `[S]` — `admin-vouchers`、`/v1/admin/payments`、`/v1/admin/disputes`、`/v1/admin/subscriptions`
    - 支付 dashboard(order stats / daily revenue chart / method chart / top-users leaderboard)`[S]`;order 管理 + refund 流程(provider-aware 退款资格)`[S]`;vouchers;disputes;subscription plans 编辑。

33. **`/admin/referrals`** `[S]` — `GET /v1/admin/referrals*`。invites/rebates/transfers 记录视图。

34. **`/admin/observability`(可观测) `[S][H]`** — `/debug/vars`、`admin-usage`
    - **采纳 sub2api Ops 全观测控制台**(`sub2api@e34ad2b:frontend/src/views/admin/ops/OpsDashboard.vue`):concurrency 卡、switch-rate/throughput/latency/error trend + distribution charts、alert events + rules、system/error log 表、3 个 drill-down modal。

35. **`/admin/alerting`** `[S]` — `admin-alerting`。alert rules / silences / events。

36. **`/admin/audit`(审计 + Merkle 树 + 信任) `[H]`** — `admin-audit`、`/admin/v1/audit-events`(`:11228`)、`/v1/audit/merkle-tree.json`(`:3678`)、`/v1/audit/export`(`:3633`)、`/v1/audit/pubkeys`(`:3537`)
    - **护城河顶配。** sections:审计事件流(可过滤)、**Merkle 树可视化**(root/批次/叶子包含证明)、**Ed25519 公钥管理 + 轮换**(root-only + 合规 gate)、proof 导出(`/v1/audit/proof/{request_id}.json` `:3603`)、audit export。

37. **`/admin/dlq`(死信队列) `[H]`** — `admin-dlq`(`/admin/v1/dlq/{handler}` `:11262`、replay `:11290`)
    - **护城河。** per-handler DLQ 列表、深度、replay 单条/批量、错误详情。

38. **`/admin/cache`(L2 缓存) `[H]`** — `admin-cache`(`/admin/v1/cache/l2/stats` `:10404`、`/{key}` `:10421`)
    - **护城河。** L2 stats(hit/miss/size)、key 查看/失效。

39. **`/admin/model-sync`(模型同步) `[H]`** — `admin-model-sync`(`/admin/v1/model-sync` `:10693`)、`/admin/v1/provider-accounts/{id}/upstream-models`(`:10171`)
    - 上游模型 drift 检测/应用(借 new-api upstream-update 模式 `[N]`)+ HUAKAI 多 provider sync 调度。

40. **`/admin/moderation`(内容审核) `[S][H]`** — `admin-moderation`(keywords `:10857`、hashes `:10968`、config `:11079`、logs `:11128`、banned `:11164`、unban `:11196`)
    - **采纳 sub2api 完整审核子系统**(`sub2api@e34ad2b:frontend/src/views/admin/RiskControlView.vue`):worker-pool telemetry、per-key health、**in-dialog audit tester**(提交 text+image → 复合 flagged score)、records 表 + 过滤、per-user unban、flagged-hash 管理、keyword/api/hybrid 模式、scope/threshold/retention 多 tab。

41. **`/admin/notifications`** `[S]` — `admin-notifications`、`admin-announcements`、`admin-email`。公告作者 + targeting editor + read-status + SMTP/邮件模板编辑器。

42. **`/admin/settings`(平台设置 — 9-tab mega-console) `[N][C]`** — `admin-platform-settings`、`/v1/admin/tls-fingerprint-profiles`
    - **采纳 new-api 7 类/35+ 子节 + sub2api 9-tab 融合 + CLIProxyAPI 每字段 GET/PUT/PATCH 模型**(`CLIProxyAPI@2a050dc:internal/api/handlers/management/config_basic.go`,~90 路由的“full settings engine”而非几个表单)。drill-in 侧栏(§1.3)。tabs:General(站名/logo/custom-menu builder + SVG)、Auth federation(OIDC/LinuxDO/WeChat/DingTalk/Turnstile/passkey)、Features(feature-flag 表 + affiliate 经济)、Users defaults、Gateway(scheduling/forwarding/routing-strategy/force-model-prefix/quota-exceeded switch-project)`[C]`、Payment provider config、Email(SMTP + 模板)、Backup(S3/R2 + cron + 双保留 + 密码恢复)`[S]`、Header/Sidebar 导航模块编辑器 `[N]`。
    - **Hermes API-profiles 配置** `[H]`:`/v1/hermes/api-profiles`(`:2722`)、settings(`:2313`)。

43. **`/admin/hermes`(Hermes 运维助手控制台) `[H]`** — `hermes` tag:`/v1/hermes/chat`(`:2376`)、`/tools`(`:2400`)、`/tool-execute`(`:2418`)、`/context`(`:2451`)、`/conversations`(`:2470`)、`/settings`(`:2313`)
    - **护城河,任何参考项目都没有 — 对话式运维。** sections:Hermes 聊天面板(流式)、tool inventory(可执行运维工具列表 + tool-execute 确认 gate)、context 面板、对话历史/检索、settings(enable/disable + api-profiles 绑定)。drill-in 侧栏。**高危工具执行前接合规确认门**(§1.2)。

44. **`/setup`(首次安装向导) `[S][N][C]`** — SetupShell
    - 多步 stepper:Database(host/port/user/pwd/db/SSL mode + test-connection)→ Redis(test)→ 管理员账号 → **平台密钥/管理 secret**(借 CLIProxyAPI bcrypt management-secret 引导 `[C]`)→ Complete。

45. **`/admin/proxies`** `[S]` — `admin-proxies`。proxy pool + 质量分级引擎(quality report score+grade + 多目标可达表)`[S]` + batch test/quality/delete + bound-accounts。

46. **404 / 错误页** `[N]`:401/403/404/500/503,access-denied(凭证保险库/审计私钥的 root gate 拦截态)。

---

## 3. 比旧 BUILD-SPEC 多出来的深度（显式缺口清单）

旧 spec(214 行)只有一张“page ↔ tag 映射表”,**没有任何页面内部 sections/tabs**。本版补足以下深度,均为旧 spec 缺失:

| # | 旧 spec 缺失项 | 本版补足 | 来源证据 |
|---|---|---|---|
| 1 | **深层 Settings 层级** | `/admin/settings` 升级为 9-tab/35+ 子节 + drill-in 侧栏 + 每字段 GET/PUT/PATCH 模型 | `new-api@1ac0f58:web/classic/src/pages/Setting/index.jsx`;`CLIProxyAPI@2a050dc:.../config_basic.go` |
| 2 | **ratio/定价可视化编辑器** | Model Pricing 可视化 + tiered/expression + live evaluator + manual JSON + upstream sync + tool-call pricing | `new-api@1ac0f58:.../Ratio/components/TieredPricingEditor.jsx` |
| 3 | **内容审核子系统**(旧只 1 行) | worker telemetry + audit tester + records + unban + 多模式多 tab | `sub2api@e34ad2b:.../RiskControlView.vue` |
| 4 | **channel-health / 冷却状态机 UI** | accounts 页行内 pause/resume/force-active + 状态可视化 | `:9871-9921` |
| 5 | **backup/DR** | S3/R2 + cron + 双保留 + 密码恢复 + R2 向导 | `sub2api@e34ad2b:.../BackupView.vue` |
| 6 | **多主题 / 9 轴外观抽屉** | per-user config drawer + server-theme 钩子 | `new-api@1ac0f58:.../config-drawer.tsx` |
| 7 | **可配置导航模块**(两层 AND) | SidebarModulesAdmin × user × role + HeaderNavModules + drill-in | `new-api@1ac0f58:.../useSidebar.js` |
| 8 | **支付流程富度** | tab Top-up/Subscribe + PaymentStatusPanel 状态机 + 多 PSP 轨 + 退款资格 + recovery snapshot | `sub2api@e34ad2b:.../PaymentView.vue`、`paymentFlow.ts` |
| 9 | **log/analytics 深度** | token 经济学列、三重成本、user-spending ranking deep-link、三类 log surface | `new-api@1ac0f58:.../UsageLogsColumnDefs.jsx`;`sub2api@e34ad2b:.../DashboardView.vue` |
| 10 | **affiliate/promo 深度** | 返佣率/冻结额度/quota 转账/invitee ledger/promo register-link | `sub2api@e34ad2b:.../AffiliateView.vue` |
| 11 | **playground 近-IDE** | 每参数启停 + raw JSON 双向同步 + 多模态 + SSE debug | `new-api@1ac0f58:.../playground/SettingsPanel.jsx` |
| 12 | **Ops 全观测控制台** | concurrency/latency/throughput/switch-rate/error charts + alert rules/events + log 表 | `sub2api@e34ad2b:.../ops/OpsDashboard.vue` |
| 13 | **匿名 key 查询页**(动画环) | `/key-usage` 独立 public 动画分析面 | `sub2api@e34ad2b:.../KeyUsageView.vue` |
| 14 | **首次安装向导外壳** | 独立无 chrome SetupShell | `sub2api@e34ad2b:.../SetupWizardView.vue` |
| 15 | **正交访问模式**(simple/backend mode + 合规门) | 见 §1.2 | `sub2api@e34ad2b:.../router/index.ts` |
| 16 | **凭证 OAuth headless 获取** | paste-callback + 状态轮询(六 provider) | `CLIProxyAPI@2a050dc:internal/tui/oauth_tab.go` |

---

## 4. 组件清单（可复用,比旧版更富）

旧 spec §8 只列了 ~12 个组件。本版需要(标注新增者 `+`):

**布局/外壳**:RoleAwareSidebar(可折叠 72/256 + mobile drawer + 自定义 SVG icon + 折叠组)`+`、DrillInSidebar(path-pattern registry)`+`、GlassHeader(标题/描述 i18n + 公告铃 + locale switch + 余额 pill + 订阅 mini-widget + user dropdown)`+`、AuthShell、SetupStepper `+`、ConfigDrawer(9 轴外观)`+`、OnboardingTour(driver.js 风,per-role,header restart)`+`。

**数据展示**:DataTable(服务端排序/分页/过滤 + 选择 + 列可见性持久化 + compact + mobile card fallback)、TablePageLayout、StatCard/KPI(triple-cost 变体)`+`、Chart 套件(model/group/endpoint distribution、token trend、latency/throughput/error/switch-rate、revenue/method)、EmptyState、Skeleton/shimmer `+`、AnimatedProgressRing(eased count-up + gradient)`+`、Pagination。

**表单/输入**:Input/Select/TextArea/Toggle/SearchInput、DateRangePicker + 粒度、ImageUpload、AmountInput(预设 chips + 校验)`+`、CIDR/IP-whitelist 输入 `+`、PaymentMethodSelector `+`、GroupSelector/GroupBadge/CapacityBadge/QuotaBadge、ParameterControl(每参数启停)`+`。

**Modal/编辑器**:BaseDialog/ConfirmDialog(money/destructive danger 态)、多 tab dialog 模式 `+`、TieredPricingEditor(条件构建 + live evaluator)`+`、ModelPricingEditor(visual/manual 切换)`+`、ParamOverrideEditor(rule-navigator + JSON fallback)`+`、EmailTemplateEditor、TargetingEditor、StepOAuthFlow(浏览器打开 + paste-callback + 轮询)`+`。

**支付**:PaymentStatusPanel(QR/popup/redirect 状态机 + 轮询)`+`、SubscriptionPlanCard(platform-themed + 折扣徽标)`+`、QRCanvas(品牌 logo overlay)`+`、OrderTable + OrderStatusBadge。

**护城河专属** `[H]`:**ReceiptVerifier**(粘贴收据 → Ed25519 + Merkle 本地校验 → ✓/✗ 链路可视)`+`、**MerkleTreeViz**(root/批次/包含证明)`+`、**TrustChainBadge**(请求 → 收据信任徽标)`+`、**HermesChatConsole**(流式 + tool-execute 确认 gate)`+`、**PoolHealthCard / HotSpotDiagnostic**`+`、**RouteResolutionViz**(为何落到某 Account)`+`、**ChannelHealthStateMachine**(active/cooling/paused/forced 切换)`+`、**CredentialVaultRow**(rotate/state + acquisition 向导)`+`、**DLQReplayPanel**`+`、**AuditPubkeyManager**(轮换 + 合规 gate)`+`。

**通用**:LocaleSwitcher、ThemeToggle、AnnouncementBell + Popup、Toast(映射后端错误码)、CodeViewer/JSONViewer/SSEViewer `+`、MaskedSecretField(reveal-copy)、StatusBadge/StatusDot、HelpTooltip、ExportProgressDialog、AutoRefreshButton、NavigationProgress、ComplianceDialog `+`、SecureVerificationModal(2FA/passkey gate)`+`。

---

## 5. HUAKAI 前端护城河（参考项目全无的页面）

> 这些是 HUAKAI 后端比三个参考项目更富的能力的 UI 表达。三个参考项目均**无对应物**(已对 source 核实):sub2api/new-api 无 Pool/凭证保险库/Ed25519 审计/Hermes;CLIProxyAPI 是纯 relay,无 payment/billing/审计账本/Pool(`~/refs/CLIProxyAPI/internal/` 无 payment 包)。

| 护城河页面 | 后端能力 | endpoint 证据 | 如何呈现 |
|---|---|---|---|
| **`/trust` + `/audit`(用户)+ `/admin/audit`** | Ed25519 可验证审计账本 + 收据 + Merkle 证明 | `:3466/:3500/:3603/:3678`、`:2815/:2844` | 公开验真台 + 用户收据“一键 verify”+ admin Merkle 树可视 + 公钥轮换。**ReceiptVerifier / MerkleTreeViz** 组件;信任徽标贯穿 usage/billing 行,把“可验证”做成产品卖点。 |
| **`/admin/hermes`** | Hermes 对话式运维助手 + tool-execute | `:2376/:2418/:2470/:2722` | 嵌入式流式聊天控制台 + 运维工具清单 + 执行前合规确认门;drill-in 侧栏。运维从“点表单”升级为“对话运维”。 |
| **`/admin/pools`** | provider 账号 **池** + 路由解析 | `:9373`、`/v1/admin/routes` | Pool 列表/详情 + 成员账号 + per-Pool 健康/余额 + **RouteResolutionViz**(为何落某 Account)+ sticky-session 分布。 |
| **`/admin/credentials`** | 凭证保险库 + headless OAuth 获取 + rotate | `:9726/:9807/:9836/:10032/:10255` | 保险库表(rotate/state)+ OAuth 获取向导(paste-callback + 轮询)+ CLI import + 预轮换窗口;root-only + 合规门。 |
| **`/admin/accounts` channel-health** | 冷却/暂停状态机 | `:9612/:9871/:9896/:9921` | 行内 pause/resume/force-active + active/cooling/paused/forced 状态机可视化。 |
| **`/admin/dlq` / `/admin/cache` / `/admin/model-sync` / `/admin/moderation`** | DLQ replay / L2 缓存 / 模型同步 / 审核 | `:11262/:11290`、`:10404`、`:10693`、`:10857+` | 各自独立运维面;DLQ replay 单/批、L2 key 失效、多 provider sync 调度。 |

**呈现策略**:把护城河收敛为侧栏一个 **“信任 & 运维”分组**(Trust / Audit / Hermes / Pools / Credentials / DLQ / Cache / Model-Sync / Moderation),用 `[H]` 视觉强调(独立 accent),并在公开 `/trust` 页对外讲“可验证 AI 网关”的故事——这是 HUAKAI 相对 sub2api/new-api 的 **生态升级**(可验证审计)+ **架构升级**(Pool/凭证保险库)。

---

## 6. 建议构建顺序（修订的垂直切片）

每片可独立 ship,先打通“浏览器可交互核心流”,再到全产品。**强制前置**:`openapi-typescript` 生成 `lib/api/schema.d.ts`,零手写 shape(旧 spec §1,保留)。

### (a) 浏览器可交互核心流（MVP — 6 片）
1. **Auth + 三外壳骨架**:SetupShell/AuthShell/PortalShell/AdminShell + RoleAwareSidebar + GlassHeader + 三 guard + i18n + ConfigDrawer 暗色。login/register/passkey/2FA/sessions。
2. **用户核心日用环**:`/dashboard`(双成本 + per-platform 额度卡)+ `/keys`(三卡抽屉 + CIDR)+ `/playground`(流式 + 每参数启停 + 多模态)。
3. **money 环**:`/billing`(Top-up/Subscribe + PaymentStatusPanel,PSP = stub)+ `/usage`(token 经济学列)+ `/subscriptions`。
4. **护城河首秀(对外卖点)**:`/trust`(ReceiptVerifier + MerkleTreeViz)+ 用户 `/audit`(收据 verify)。— **优先级提前**,因这是 HUAKAI 唯一无法被参考项目复制的差异化。
5. **admin 运维核心**:`/admin`(驾驶舱 triple-cost + 池/DLQ/告警摘要)+ `/admin/accounts`(channel-health 状态机)+ `/admin/pools`(RouteResolutionViz)。
6. **凭证 + Hermes**:`/admin/credentials`(headless OAuth 获取)+ `/admin/hermes`(对话运维)。

### (b) 全产品（深度 — 5 片）
7. **ops 深度**:`/admin/billing`、`/admin/pricing`(ratio 可视化 + tiered/expression + live evaluator)、`/admin/payments`(revenue dashboard + 退款)。
8. **审计/告警/队列**:`/admin/audit`(Merkle + 公钥轮换)、`/admin/alerting`、`/admin/dlq`、`/admin/cache`、`/admin/model-sync`。
9. **审核 + 通知 + 用户管理**:`/admin/moderation`(audit tester)、`/admin/notifications`(SMTP + 模板)、`/admin/users`(宽表)、`/admin/proxies`(质量分级)。
10. **平台设置 mega-console**:`/admin/settings`(9-tab + drill-in + 每字段 PATCH)+ backup/DR + 可配置导航模块编辑器 + `/setup` 向导收尾。
11. **public site + 收尾**:`/`(faux-terminal)、`/pricing`(facet 市场 + tiered 拆解)、`/rankings`、`/key-usage`(动画环)、`/referrals`/`/checkin`/`/notifications`/`/account` + simple/backend mode + onboarding tour + 错误页。

---

## 关键文件路径(供后续 Claude Design 逐页 prompt 使用)

- 本提案应取代:`/home/ubuntu/HUAKAI/docs/frontend/BUILD-SPEC.md`(旧版 214 行,过简)
- 契约源(唯一真相):`/home/ubuntu/HUAKAI/docs/openapi/openapi.yaml`(40 tags,行号已核对)
- 现有薄前端(扩展非重写):`/home/ubuntu/HUAKAI/frontend/`(`app/`、`components/`、`lib/api/`;已有 `app/chat`/`app/observability`/`app/accounts`/`app/audit`/`app/mimicry`/`app/bindings` 可重构进路由组)
- UI 契约(管理面义务):`/home/ubuntu/HUAKAI/docs/14_UI_CONTRACTS.md`(Pooling Groups / Route 解析 / pool-aware 对账等强制项)
- 逐页 prompt 模板:`/home/ubuntu/HUAKAI/docs/frontend/PAGE-PROMPTS.md`(1127 行,沿用其 per-page prompt 模式)

**核心论点**:本版相对旧 spec 的不“简单”体现在三处——(1) 每页都展开了内部 sections/tabs(旧版完全没有);(2) 引入了 sub2api 的正交访问模式 + new-api 的两层可配置导航/深 Settings/ratio 可视化编辑器/9 轴外观;(3) 把 HUAKAI 后端独有的 **Trust/Audit/Hermes/Pools/Credentials/channel-health/DLQ** 显式成面,作为参考项目无法复制的前端护城河,并在构建顺序中**提前**护城河首秀(切片 4)。