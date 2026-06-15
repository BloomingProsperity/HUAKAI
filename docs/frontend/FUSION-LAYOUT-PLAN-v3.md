<!-- 前端融合布局具体方案 v3「实测版」· 2026-06-15 · PM: Claude -->
<!-- 证据基: 已自研建成并对真后端接线测试 11 模块(frontend_wiring_test.go 全 PASS) + 三家源码对照 -->
<!-- 参考: sub2api@e34ad2b(Vue) · new-api@1ac0f58(web/ React,one-api 血统) · CLIProxyAPI@2a050dc(无前端) -->
<!-- Clean-room: 全文为行为/IA/字段对照的 paraphrase + repo@sha:path 引用,未抄三家任何源码标识符/代码块 -->

# 前端融合布局具体方案 v3 —— 实测版

> **与 `IA-PROPOSAL-v2-2026-06-14.md` 的区别**:v2 是调研推导的纸面 IA;**v3 是实测沉淀**——HUAKAI 已用自研 React/Next 栈建成 11 个模块并对真后端逐一接线测试(`backend/cmd/gateway/frontend_wiring_test.go`,11 子测试全 PASS),每条布局决策都有运行证据。本方案给出可直接照建的**外壳 / 导航 / 每页 IA / 设计系统 / 实测约束**。
>
> **栈定论(见 [[sub2api-frontend-reuse-verdict]])**:不 fork(sub2api Vue + LGPL / new-api React + copyleft 均禁 vendoring,§12 DR-002),用 HUAKAI 自有 Next.js 15 + React 18 + Tailwind v4 + shadcn,布局借鉴三家。

---

## 1. 三套外壳(Shells)—— 已落地

| 外壳 | 受众 | 路由 | 三家对照 | HUAKAI 实现 |
|---|---|---|---|---|
| **public** | 访客 | `/login`(注册同页 tab)、定价、排行 | sub2api `frontend/src/views/auth/` + 独立 `SetupWizardView`;new-api `web/src` 登录页 | `app/login/page.tsx` 全屏(`AppLayout` BARE_ROUTES 绕过外壳)✅ |
| **user-portal** | 终端用户 | 左栏控制台,见 §3 | sub2api `AppLayout.vue` 左栏;new-api 左栏 + 顶栏 | `components/layout/{AppLayout,Sidebar,Header}.tsx` ✅ |
| **admin-console** | 操作员 | 左栏控制台(独立导航树) | sub2api `views/admin/`;new-api 管理面 | 批次3-4(现 `/accounts /dashboard /observability /audit` 为雏形) |

**delta(架构升级)**:三家是「一个 SPA 内切上下文」;HUAKAI 用 Next App Router 的**路由级外壳切换**(`BARE_ROUTES` 常量 + `usePathname`),public 页天然 SSR 静态化(实测 `/login` 6.83kB 静态预渲染),无需 SPA 全量 hydrate。

---

## 2. 鉴权三轨 —— 实测逼出的核心布局约束 ⭐

接线测试最重要的产出:HUAKAI 是**三条独立凭证轨道**,布局必须分清(三家都是单一 bearer,这是 HUAKAI 的结构差异):

| 轨道 | 凭证 | 覆盖端点 | 布局含义 |
|---|---|---|---|
| **会话** | session_token(登录得,localStorage) | auth/me · api-keys · me/quota · groups · vouchers · subscriptions · notifications · account · export.csv | portal 管理面默认轨,`userClient` 自动带 + 401 单飞刷新 + 踢回登录 |
| **API Key** | hk_ key(用户自建,localStorage) | `/v1/models` · `/v1/chat/completions` · `/v1/me/usage` · `/v1/me/analytics/time-series` | **Playground 与「用量明细」页必须有 key 输入位**;两页共用 `huakai_api_key` |
| **管理** | admin token(`hk_admin_` 前缀) | `/admin/v1/*` | admin-console 专轨,与 portal 互不影响 |

**delta(架构升级)**:`sub2api@e34ad2b:frontend/src/api/client.ts` 是单 axios 实例 + 单 `auth_token` 拦截器;`new-api@1ac0f58:web/` 同为单 token。HUAKAI 把**会话态(管理你的账户)**与**调用凭证(hk_ key 打推理)**在 UI 层分离 —— 这直接来自后端 `d.inboundAuth`(API-key)vs `auth.SessionMiddleware`(session)的双鉴权设计(`backend/cmd/gateway/routes.go`)。维度:架构(凭证轨道分离)+ 生态(key 输入位贯穿 Playground/用量)。

---

## 3. 用户门户导航 IA —— 已建(实测可点)+ 三家对照

左栏顺序(`components/layout/Sidebar.tsx`,均已接真端点):

| 项 | 路由 | 状态 | sub2api 对照 | new-api 对照 | HUAKAI delta / 维度 |
|---|---|---|---|---|---|
| 概览 | `/dashboard` | 雏形(待批次2 用户化) | `views/user/DashboardView.vue` | `web/src/features/dashboard` | 聚合 quota+usage+keys 一屏;算法:今日/窗口花费前端聚合 |
| Playground | `/chat` | ✅ 已测 | 仅 `AccountTestModal` 单发连通测试 | `web/src` playground 多轮 | **多轮+SSE+token面板+模型能力**;融合 new-api 多轮 + 自研 markdown 零依赖 |
| API Keys | `/api-keys` | ✅ 已测 | `api/keys.ts` | Token 管理页 | 一次性明文弹窗 + 每 key 展开 usage-summary;生态:key 维度成本 |
| 用量 | `/usage` | ✅ 已测 | `views/user/UsageView` | `features/usage-logs` | 日期范围+粒度+模型 Top+CSV;混合鉴权(额度 session/明细 key) |
| 账户 | `/account` | ✅ 已测 | `api/{groups,user}.ts` | 个人中心 | 分组+邀请码+签到+推荐账本一页(cents/decimal 统一格式化) |
| 兑换 | `/redeem` | ✅ 已测 | `api/redeem.ts` | redemption | 余额券/订阅券分流文案 + 幂等回显 |
| 订阅 | `/subscriptions` | ✅ 已测 | `api/subscriptions.ts` | 订阅页 | 日/周/月窗口配额进度(sub2api 窗口模型,§16 默认)+ progress 503 容错 |
| 通知 | `/notifications` | ✅ 已测 | `api/announcements.ts` | 通知中心 | 收件箱+公告双 tab + 每用户多渠道设置(email/webhook/bark/gotify) |
| 审计 | `/audit` | 雏形(待批次2) | 无对应 | 无对应 | **HUAKAI 独有**:见 §5 |
| 管理后台 | `/accounts` | 雏形(批次3) | `views/admin/` | 管理面 | admin-console 入口 |

> CLIProxyAPI(`@2a050dc`):纯中继账号→API 代理,`~/refs/CLIProxyAPI/` 仅 `internal/`,**无 web/前端**(documented no-equivalent,§16)。其 ops/config 模型概念在 admin-console 借鉴,前端无可对照页。

---

## 4. HUAKAI 护城河面 —— 三家无对应,独立成面 ⭐

接线测试覆盖的后端里,这些是三家前端都没有的能力(`backend/cmd/gateway/routes.go`),布局上**单独开面、不混进通用 portal**:

- **密码学回执 + 可验证审计**:`/v1/receipts/*`(签名成本回执 get/verify)、`/v1/audit/*`(Ed25519 + Merkle 证明)、`/v1/trust/verify` → 「审计/信任」面:每笔调用可下载签名回执、验 Merkle 链。维度:生态(可验证计费透明度)。
- **争议**:`/v1/me/disputes` → 回执旁「发起争议」。
- **Passkey/WebAuthn**:`/v1/me/passkeys`、`/v1/auth/passkey` → 账户安全区(批次2)。
- **Hermes 运维助手**:`/v1/hermes/*` → admin-console 末。
- **provider 账号池 + 凭证保险库 + channel-health 冷却态机**:admin-console 核心。

三家对照:`sub2api@e34ad2b` 有 compliance ack 但无密码学回执;`new-api@1ac0f58` 无可验证审计账本;均为 HUAKAI 架构独有(回执/审计是后端 `auditLedger`/`receiptStore` 的前端面)。

---

## 5. 每页 IA(实测字段级,已建页)

各页已建 + 后端支持字段 + gap,详见 `WIRING-COVERAGE-MATRIX.md` 与各 `lib/api/*.ts` 顶部契约注释。要点:
- **空态优先**:接线测试证明新用户所有端点返空但 200 + 形状 → 每页先设计空态(已落地)。
- **per-section 503 容错**:dev 装配里 2FA / invitation / referral 服务为 nil → 结构化 503;布局上每个 section 独立降级显示「暂不可用」,不拖垮整页(账户页的邀请码/推荐已这样做)。
- **金额单位统一**:后端混用 cents(签到/兑换/邀请)与 decimal 字符串(用量/额度/订阅 USD)→ 前端 `formatCents` / `formatUsd` 两个格式化器分流(已落地)。

---

## 6. admin-console IA(批次3-4 蓝图)

左栏(独立树,对照 `sub2api@e34ad2b:frontend/src/views/admin/` + `new-api@1ac0f58:web/` 管理面):总览 · 用户 · 账号池(CRUD+批量+健康+测试) · 渠道/定价 · 计费/订单 · 兑换/订阅/推荐运营 · 运维监控(告警/错误 triage) · 内容审核 · 系统设置(平台/邮件/路由/TLS指纹) · 系统(版本/日志/模块)。每模块走 admin token 轨,接 `/admin/v1/*` + `/v1/admin/*`。

---

## 7. 设计系统(已落地,照用)

`tailwind.config.ts` + shadcn:**primary=teal**(#14b8a6,500/600/700)· accent 灰阶(CSS vars)· 暗色默认 · `shadow-card`/`shadow-glow` · 圆角 lg · lucide 图标 · 中文文案 · `Button(cva variant)`/`Card*`/`Badge`/`Table*` 套件 · recharts 图表。新页一律 `'use client'` + 复用 `userClient`/`friendlyMessage`。

---

## 8. 实测踩坑 → 布局约束(经验沉淀,给后续批次)

1. **三轨鉴权**(§2):Playground/用量明细要 key 位;portal 其余用登录态;admin 独立 → 不要把三者混进一个 client。
2. **dev 装配 nil 服务**(2FA/invitation/referral):每 section 独立 503 容错。
3. **平台设置门控**:登录受 `two_factor_enabled`(默认 true!)、注册受 `registration_enabled`(默认 false)门控 → public 页须读 `/v1/site/config` 感知开关(登录页已做)。
4. **/v1/auth/me 形状**:`{panel,user_id,tenant_id,display_name}`(无 email,user_id 非 id)→ `fetchMe` 映射 + email 从登录保留。
5. **空态/金额单位/cursor 分页**:全站统一处理器。

> 完整逐模块测试经验见 [[huakai-frontend-wiring-test-setup]];覆盖进度见 `WIRING-COVERAGE-MATRIX.md`。本方案随批次推进持续校准。
