<!-- 经验文档 · sub2api 前端做底座的可行性演习(throwaway spike,演习场已删)· 2026-06-15 -->
<!-- 来源: 3-agent 真码侦察 sub2api@e34ad2b(frontend/src) + HUAKAI 真码契约(backend/internal/gatewayhttp, cmd/gateway/routes.go) -->
<!-- Clean-room: 全文为行为/HTTP契约的 paraphrase 分析 + repo@sha:path 引用,未拷贝任何 sub2api 源码标识符/代码块。LGPL 禁止 fork/vendoring(DR-002),本演习场已删除。 -->

# 经验:拿 sub2api 前端做底座 + 补缺失 —— 可行性演习记录

> **演习性质**:Owner 指令"只做这次验证,搞完删掉,经验保留,后续前端我还会重写"。本文是**保留下来的经验**;演习场(`/tmp/sub2api-spike`,sub2api frontend 的一次性拷贝)已删除,**未写入 HUAKAI 仓库**。
>
> **一句话结论**:sub2api 前端能立起来,但"直接做底座"有三条硬伤(框架/许可证/数据层全绑在它自己的后端上);而**反向的好消息**是 —— HUAKAI 后端早已覆盖 sub2api 前端要的绝大部分,真正缺的只有窄窄几块。所以真前端应当**用 HUAKAI 自己的 React/Next 栈重写,借鉴 sub2api 的信息架构,对接 HUAKAI 已做完的端点**(即 `docs/frontend/IA-PROPOSAL-v2-2026-06-14.md` 的路线),而不是 fork 它的代码。

---

## 1. 亲手演习的硬数据(install + build)

把 `sub2api@e34ad2b:frontend/` 拷到 `/tmp` 隔离场,真装真构建:

| 步骤 | 结果 |
|---|---|
| 前端规模 | **507 个 .vue/.ts 文件 / ~169,000 行**(`sub2api@e34ad2b:frontend/src/`) |
| `npm install` | ✅ 成功,**512 包 / node_modules 258MB / 2 分钟** |
| `npx vite build` | ❌ 42s 后倒在 `src/components/admin/AdminComplianceDialog.vue` 的 `?raw` 导入 `../../../../docs/legal/admin-compliance.zh.md` —— 该文件在仓库根 `docs/legal/`,我只拷了 `frontend/`。**这是拷贝范围坑,不是真失败**,但本身是个经验点:它前端**编译期硬依赖仓库根的 legal markdown**(`sub2api@e34ad2b:frontend/src/components/admin/AdminComplianceDialog.vue`)。 |

**经验①**:它能 install、能 bundle,是个成熟可运行的产品级前端。但"立起来"≠"能服务 HUAKAI"。

---

## 2. 三条硬伤(为什么不直接 fork 做底座)

### 硬伤 A — 框架是 Vue3,不是 React → 等于扔掉现有前端
- sub2api 前端 = **Vue 3 + Vite + Pinia + vue-router + vue-i18n + Tailwind 3**(`sub2api@e34ad2b:frontend/package.json`)。
- HUAKAI 现有前端 = **Next.js 15 + React 18 + shadcn/radix + Tailwind 4 + recharts**(`frontend/package.json`)。
- 两框架**组件零互通**。用它做底座 = 现有 React 前端整套作废,从此维护一个 17 万行的 Vue app。

### 硬伤 B — 许可证 LGPL-v3 → 与 clean-room MIT 定位冲突(宪章 DR-002)
- 根 LICENSE = **GNU LGPL v3** + 带 CLA.md(`sub2api@e34ad2b:LICENSE`)。
- fork 它的代码进 HUAKAI,HUAKAI 前端就变成 LGPL 衍生作品,**不能再是 MIT 干净房**,且封死 SaaS 商用路径。
- 这正是 `CLAUDE.md §12` 写死的红线:**"For LGPL/AGPL projects (sub2api/new-api): vendoring is forbidden. Only paraphrased mechanism extraction allowed."** 本演习的结论与宪章一致 —— **只借鉴行为/IA,不搬代码**。

### 硬伤 C — 它的数据层全绑在 sub2api 自己的后端契约上
- 它一个共享 axios 实例,`baseURL = VITE_API_BASE_URL || '/api/v1'`,响应假定信封 `{code,message,data}`,Bearer 取自 `localStorage['auth_token']`,401 走 `/auth/refresh` 单飞刷新(`sub2api@e34ad2b:frontend/src/api/client.ts`)。
- HUAKAI 后端**说的是另一套话**:登录返回 `{user, session{session_token, refresh_token, ...}}`(**不是** `access_token`),错误信封是 `{"error":{"code","message"}}`(成功裸 JSON,无 `{code,data}` 包裹),刷新走 `/v1/sessions/refresh`,注册要 `tenant_id`(`backend/internal/gatewayhttp/auth_handler.go`, `cmd/gateway/routes.go`)。
- 所以"补缺失"是**反的**:不是 sub2api 缺 HUAKAI 的功能,而是它一身功能绑在别的后端上。真要 fork,得**把它整个数据层(2357 行 `api/*` + stores + types)重写去接 HUAKAI**。

---

## 3. 契约对接图:sub2api 前端功能 → HUAKAI 已做完端点

**重大发现**:HUAKAI 后端 ≠ "瘦调试台"。它 `/v1/*` + `/v1/admin/*` + `/admin/v1/*` 已覆盖 sub2api 绝大部分。状态:✅ 直接可接(字段适配)· ⚠️ 形状/概念需适配 · ❌ HUAKAI 缺(进缺口清单)。

### 3.1 用户面(对接优先级最高)

| sub2api 前端能力(`frontend/src/api/`) | HUAKAI 端点(`backend`) | 状态 |
|---|---|---|
| 登录/注册/me/登出 `auth.ts` | `POST /v1/auth/login·register·logout` + `GET /v1/auth/me` | ✅ 字段适配(`access_token`↔`session_token`;注册需 `tenant_id`) |
| Token 刷新 `auth.ts` `/auth/refresh` | `POST /v1/sessions/refresh`(轮换+重放检测 409) | ✅ 路径+形状适配 |
| 2FA/TOTP `totp.ts` | `/v1/auth/2fa/*`(setup/enable/disable/status/backup-codes)+ 登录挑战 202→`/login/2fa` | ✅ HUAKAI 全活 |
| API Key CRUD `keys.ts` | `/v1/api-keys/*`(配额/分组/IP白黑名单/模型白名单/一次性明文) | ✅ 直接 |
| 用量日志/仪表盘/图表 `usage.ts` | `/v1/me/usage`、`/v1/me/analytics/time-series`、`/v1/me/keys/{id}/usage-summary`、CSV/JSON 导出 | ⚠️ HUAKAI 有底层数据;sub2api 的 `dashboard/trend·models` 富图需用 time-series 自行聚合 |
| 配额 `user.ts`/`platform-quotas` | `/v1/me/quota` | ✅ |
| 分组 `groups.ts` | `/v1/me/groups`、pool-group ratios | ✅ |
| 兑换码 `redeem.ts` | `/v1/users/me/vouchers/redeem` + redemptions | ✅ 概念对齐(voucher) |
| 订阅 `subscriptions.ts` | `/v1/users/me/subscriptions` + progress | ✅ |
| 公告 `announcements.ts` | `/v1/announcements`(+ 已读) | ✅ |
| 余额通知/notify-email `user.ts` | 每用户通知设置(webhook/email/Bark/Gotify) | ✅ 概念对齐 |
| 推广/分销 `user.ts`/aff | `/v1/me/referrals`(+ reward 账本) | ✅ |
| OAuth 登录/绑定 `auth.ts`(LinuxDo/WeChat/DingTalk/OIDC) | `/v1/auth/oauth-init·oauth-callback` + `/v1/users/me/oauth-bindings/*` + telegram | ⚠️ 通用 OAuth + github/google/telegram 有;**LinuxDo/微信/钉钉这些 sub2api 专属 provider 是缺口** |
| 频道监控(用户只读)`channelMonitor.ts` | channel-health(偏管理) | ⚠️ 无用户面"频道状态页" |
| 支付/下单 `payment.ts` | `/v1/users/me/payments·recharges`、`/v1/payment/webhooks` | ⚠️ **按 Owner 指令跳过真实支付 SDK 对接**(端点骨架在) |
| 首次安装向导 `setup.ts` `/setup/*` | —— | ❌ **缺口** |

### 3.2 管理面(`frontend/src/api/admin/`)

| sub2api 管理能力 | HUAKAI 端点 | 状态 |
|---|---|---|
| 账号(provider account)CRUD+批量 `accounts.ts` | `/admin/v1/provider-accounts·pools·credentials` | ✅ |
| 频道+定价管理 `channels.ts` | `/admin/v1/channels`、`pricing/ratios`、cache-price-overrides、models capabilities | ✅ |
| 仪表盘/统计 `dashboard.ts` | `/v1/admin/usage/overview·leaderboard·performance·perf-metrics·health-score` | ✅ |
| 告警(规则/事件/静默)`ops.ts` | `/v1/admin/alert-rules·alert-events·alert-silences` | ✅ |
| 用户管理 `users.ts` | `/admin/v1/users/*`(余额/历史/解锁/通知) | ✅ |
| 订阅管理 `subscriptions.ts` | `/v1/admin/subscriptions·assignments` | ✅ |
| 兑换码生成/导出 `redeem.ts` | `/v1/admin/vouchers·vouchers/batch` | ✅ 概念对齐 |
| 代理 `proxies.ts` | `/admin/v1/proxies` | ✅ |
| 内容审核/风控 `riskControl.ts` | `/admin/v1/moderation` | ✅ |
| TLS 指纹 `tlsFingerprintProfile.ts` | `/v1/admin/tls-fingerprint-profiles` | ✅ |
| 计划测试 `scheduledTests.ts` | `/admin/v1/channel-test-templates` | ⚠️ 概念对齐,形状异 |
| 分销/返利 `affiliates.ts` | `/v1/admin/referrals·overview·rewards` | ✅ |
| 平台设置 `settings.ts`(SMTP/OAuth provider/邮件模板) | `/v1/admin/platform-settings`、`/v1/admin/email·email/settings·email/test` | ⚠️ 基础在;深层项缺(见缺口) |

---

## 4. "对接"演习的具体经验:三处适配器(Adapter)是关键

真把它接到 HUAKAI,**不改 UI 也得在数据层加一层适配器**,差异集中在三点:

1. **鉴权模型**:sub2api 期望登录直接回 `access_token`+`refresh_token` 存 localStorage,Bearer 头;HUAKAI 回 `session{session_token,...}`(键名不同),且**注册不发 token(需邮箱验证 `verification_required`)**、登录 2FA 走 **202 + challenge_id**。→ 适配器要把 `session.session_token`→当作 `access_token` 存,刷新指向 `/v1/sessions/refresh`。
2. **响应信封**:sub2api 拦截器假定 `{code,message,data}` 并 unwrap `data`;HUAKAI 成功**裸返 JSON**、失败是 `{"error":{code,message}}`。→ 适配器要改写 `client.ts` 的 response 拦截器(成功直接用 body,失败读 `error.code`)。
3. **多租户**:HUAKAI 注册/登录要 `tenant_id`;sub2api 前端无此概念。→ 需在 bootstrap(`/v1/site/config`)注入默认 tenant。

**经验②**:这层适配器是机械但成规模的工作(每个 `api/*` 模块都要改路径+形状)。在"重写真前端"时,这等于直接照 HUAKAI 契约写 client,**不必经过 sub2api 那层**。

---

## 5. ⭐ 缺口清单 —— sub2api 前端有、HUAKAI 后端缺(已排除真实支付)

> 这是 Owner 要的核心"经验"。每条标注建议:🔴 真缺口该补 · 🟡 概念不同/低优先 · ⚪ 不同部署模型/建议不抄。

| # | 缺口能力 | sub2api 出处 | HUAKAI 现状 | 建议 |
|---|---|---|---|---|
| 1 | **首次安装向导**(测 DB/Redis/建管理员/写配置) | `frontend/src/api/setup.ts` `/setup/{status,test-db,test-redis,install}` | 无任何 setup 端点 | 🔴 补:`/v1/setup/*`,首跑体验关键 |
| 2 | **备份 + 数据管理**(S3 备份配置/计划/恢复、PG/Redis/S3 profile、备份任务) | `admin/backup.ts`、`admin/dataManagement.ts` `/admin/backups/*`、`/admin/data-management/*` | 无 | 🔴 补(运维刚需):备份计划+S3+恢复 |
| 3 | **富 Ops 监控面板**(延迟直方图、吞吐趋势、错误分布、实时 QPS WebSocket、并发/可用性统计、请求/上游错误分流 triage+resolve) | `admin/ops.ts` `/admin/ops/dashboard/*`、`/admin/ops/{request,upstream}-errors/*`、`WS /admin/ops/ws/qps` | 有 alerting + perf-metrics + system/health,**无**完整 ops 面板/错误 triage/实时 QPS | 🔴 补(部分):错误 triage + 延迟/吞吐趋势最有价值 |
| 4 | **用户自定义属性**(属性定义 CRUD + 每用户值 + 批量) | `admin/userAttributes.ts` `/admin/user-attributes/*` | 无 | 🟡 补:运营分群有用,非 MVP |
| 5 | **错误透传规则**(按状态码/关键词匹配,定制上游错误透传行为) | `admin/errorPassthrough.ts` `/admin/error-passthrough-rules/*` | 无 | 🟡 网关精细化,排路线图 |
| 6 | **系统日志列表/清理/sink 健康** | `admin/ops.ts` `/admin/ops/system-logs/*` | 有 loglevel/modules,无日志列表/清理 | 🟡 可观测补充 |
| 7 | **设置深层项**:rectifier、beta-policy、web-search 模拟、429/529/overload 冷却、stream-timeout、邮件模板编辑器 | `admin/settings.ts` `/admin/settings/*` | platform-settings + 邮件基础在,深层项缺 | 🟡 逐项排,多为高级调优 |
| 8 | **promo 码**(独立于 voucher) | `admin/promo.ts` `/admin/promo-codes/*` | 有 voucher,无独立 promo | 🟡 概念可并入 voucher |
| 9 | **provider 专属 onboarding**:CRS 同步、Codex-session 导入、Antigravity/Gemini code_assist 分层 OAuth | `admin/accounts.ts`、`admin/antigravity.ts`、`admin/gemini.ts` | 有通用 credential 导入(cli/csv/json/paste/oauth-init),无这些专属流 | ⚪ sub2api 专属上游,按需补,非通用 |
| 10 | **系统自更新**(version/check-updates/update/rollback/restart) | `admin/system.ts` `/admin/system/*` | 无 | ⚪ 部署模型不同(容器化),**建议不抄** |
| 11 | **频道合成探测监控**(主动探活+模板+历史+用户状态页) | `admin/channelMonitor.ts`、用户 `channelMonitor.ts` | 有 channel-health(被动),无主动探活+用户状态页 | 🟡 用户面状态页有价值,排路线图 |

---

## 6. 别忘了:HUAKAI 反超 sub2api 的地方(护城河,重写前端时要做出来)

侦察中确认 HUAKAI 后端在这些上**领先** sub2api 前端覆盖面(`backend/internal/gatewayhttp`, `cmd/gateway/routes.go`):

- **Passkey/WebAuthn**(`/v1/auth/passkey/*` + `/v1/me/passkeys/*`)—— sub2api 用户面只有 TOTP,无 passkey。
- **密码学透明度**:签名回执(`/v1/receipts/*` get/verify)、Ed25519 审计账本 + Merkle 证明(`/v1/audit/*`)、trust-verify —— sub2api 无对应。
- **争议系统**(`/v1/me/disputes` + admin resolve)。
- **媒体生成任务**(Midjourney/Suno/视频 + 通用异步任务带成本 hold)。
- **Hermes 运维助手**(`/v1/hermes/*`)。
- **多协议推理广度**:OpenAI Chat/Completions/Responses(+Codex)、Anthropic Messages(+count_tokens)、Gemini v1beta、embeddings/rerank/images/audio。

→ 这些必须在真前端**单独成面**(已在 `IA-PROPOSAL-v2` 列为"前端护城河")。

---

## 7. 结论与对真前端的指导

1. **不 fork sub2api 前端**(框架冲突 + LGPL 封死 MIT/SaaS + 数据层全绑它后端)。符合 `CLAUDE.md §12` / DR-002。
2. **真前端 = HUAKAI 自有 React/Next 栈**,**借鉴 sub2api 的 IA/页面形态/交互**(即 `IA-PROPOSAL-v2-2026-06-14.md` 三家融合路线),client 直接照 HUAKAI 契约写 —— 无需 sub2api 适配器层。
3. **先对接 HUAKAI 已做完端点**(§3 的 ✅ 项:auth+2FA、api-keys、me/usage/quota、groups、vouchers、subscriptions、announcements、referrals、admin 全套),真实支付按 Owner 指令暂跳过 SDK。
4. **缺口按 §5 优先级排路线图**:🔴 setup 向导 / 备份 / ops 错误 triage 先补;🟡 排后;⚪ 自更新不抄。
5. 本演习场已删除,零代码进仓库,clean-room 不受污染。

> **演习场清理**:`/tmp/sub2api-spike` 已 `rm -rf`(本文写完即删)。本文是唯一保留产物。
