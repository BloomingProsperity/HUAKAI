# Admin 端点 × 前端覆盖审计（2026-06-17）

> 多 agent（8 分簇 finder + 完整性 critic）对 `backend/cmd/gateway/routes.go` 全部 admin 路由对照 `frontend/lib/api` + `frontend/app` 覆盖。审计基线 `feat/frontend-portal` @ 9ab2d765。
> 复跑说明：首跑因本地主检出陈旧（停在 9b6f088d、缺已合并切片）失真，已 ff 更新后在新检出复跑，本表为复跑结果。

## 计数

| 类别 | 数量 | 含义 |
|---|---|---|
| ✅ 已覆盖 covered | 24 | 读+写工作流均已接线 |
| 🟡 部分 partial | 8 | 读已接、部分写操作缺 |
| ⬜ 零覆盖 uncovered | 6 | 后端有、前端零引用（=可做的活）|
| 🚧 避让 avoidance | 7 | provider/channel/代理区，让给并行 proxies 分支 |
| **合计** | **45** | critic 补漏 2 |

## ⬜ 零覆盖（真缺口 = 可做的活）

| surface | base_path | 端点 | risk | 缺口/备注 |
|---|---|---|---|---|
| **api-keys** | `/admin/v1/api-keys` | 3 | security | 后端缺口: No GET /{id} single-key detail and no PUT/PATCH update endpoint (only issue/list/revoke); a frontend admin key… |
| **cache-price-overrides** | `/v1/admin/cache-price-overrides` | 3 | money | paymenthttp.MountCacheOverrideAdminRoutes (cache_price_override_handler.go:49): GET / list, PUT /{scope} set, … |
| **exporthttp CSV exports (admin)** | `(root, mounted directly on admin router)` | 4 | money | exporthttp.MountRoutes (export.go:68) mounts 4 read-only admin CSV exports (payments/usage/orders/refunds) dir… |
| **disputes (admin)** | `/v1/admin/disputes` | 2 | billing | 后端缺口: No admin GET single dispute by id (only list + resolve); resolve is the only mutation (no reject/reopen/assign… |
| **routes-admin / routing wizard** | `/v1/admin/routes` | 4 | security | 后端缺口: Backend has NO update/PUT handler for routes — only Create, List, Get-by-id, Delete (controlhttp/routeadmin_ha… |
| **model capabilities + aliases admin (capability override, alias bulk-import, capability-binding inspect)** | `/v1/admin/models` | 3 | none | 后端缺口: No frontend admin surface at all to drive capability overrides or alias bulk-import; no GET to read current pe… |

## 🟡 部分覆盖（缺写操作）

| surface | base_path | 端点 | risk | 缺口/备注 |
|---|---|---|---|---|
| **users** | `/admin/v1/users` | 13 | security | 后端缺口: Backend exposes the capabilities; the gap is purely frontend. No frontend client for: POST /admin/v1/users (cr… |
| **billing settings** | `/admin/v1/billing` | 2 | billing | 后端缺口: Settings GET/PUT only expose a single key (billing.StreamInputOnlyInterruptedPolicyKey via d.Store.Get with bi… |
| **payments (orders/refunds admin)** | `/v1/admin/payments` | 14 | money | paymenthttp.MountPaymentAdminRoutes (handler.go:189) mounts 14 endpoints. Frontend wires ONLY 4: GET list, GET… |
| **usage analytics (mountUsageAdminRoutes leaderboard/perf/health)** | `/v1/admin/usage` | 7 | quota | 后端缺口: Two backend GETs have ZERO frontend client: /v1/admin/usage/perf-metrics/by-bucket (routes_usageadmin.go:30, N… |
| **alerting: alert-rules CRUD** | `/v1/admin/alert-rules` | 5 | security | 后端缺口: Frontend has no create/update/delete/get-by-id client for alert rules, and no page renders the rule list. Oper… |
| **moderation (content moderation admin control plane)** | `/admin/v1/moderation` | 13 | security | 后端缺口: POST /admin/v1/moderation/api-keys/{id}/unban (mount.go:51) has NO frontend client — deliberately deferred (mo… |
| **notifications** | `/v1/admin/notifications + /v1/admin/users/{user_id}/notifications (admin); /v1/notifications + /v1/users/me/notifications (user)` | 9 | none | 后端缺口: No admin-side broadcast/worker-stats/per-user-settings UI exists; backend exposes those 4 admin endpoints but … |
| **tls-fingerprint-profiles** | `/v1/admin/tls-fingerprint-profiles` | 6 | security | 后端缺口: Frontend exposes only the read-only list. The 5 write/detail endpoints (POST create, GET {id}, PUT {id} update… |

## 🚧 避让（provider/channel/代理，让 proxies 分支）

| surface | base_path | risk | 前端引用 / 缺口 |
|---|---|---|---|
| proxies (outbound proxy pool CRUD) | `/admin/v1/proxies` | security | ⚠后端缺口: No proxy health-check / connectivity-test endpoint (status is operator-set via PUT /{id}/s… |
| provider catalog | `/admin/v1/providers` | none | ⚠后端缺口: Frontend has no create/update/delete client for the provider catalog (only the list GET is… |
| channel catalog | `/admin/v1/channels` | none | ⚠后端缺口: Frontend has no create/update/delete client for the channel catalog (only the list GET is … |
| provider-accounts / pool-accounts (core account CRUD + actions) | `/admin/v1/provider-accounts (aliases: /v1/admin/provider-accounts, /v1/admin/pool-accounts)` | security | ⚠后端缺口: Frontend does NOT wire bulk-by-tag (mass tag ops), upstream-models (per-account model disc… |
| channel-health (read + per-account override) | `/v1/admin/channel-health (read) + /admin/v1/provider-accounts/{id}/channel-health/* (override, on all 3 provider-account base paths)` | quota | ⚠后端缺口: None material — read list/summary/detail and all 3 override actions are wired. (Pause/resu… |
| credentials (per-account credential CRUD + acquisition flow + import helpers + renew-status) | `/admin/v1/provider-accounts/{id}/credentials (per-account CRUD), /admin/v1/provider-accounts/{id}/credential-acquisitions (flow), /admin/v1/credentials/* (helpers + renew-status)` | security | ⚠后端缺口: Frontend wires ONLY the helper import routes (paste/cli/csv/json-import), oauth-init, and … |
| pools (pool-group CRUD) | `/admin/v1/pools` | quota | ⚠后端缺口: Backend NewAdminPoolsHandler exposes only GET list, POST create, GET {id}, PATCH {id} — th… |

## ✅ 已覆盖

| surface | base_path | risk | 前端引用 / 缺口 |
|---|---|---|---|
| email (admin SMTP settings) | `/v1/admin/email` | security | /home/ubuntu/HUAKAI/frontend/lib/api/adminSettings.ts (getEmailSettings :250, updateEmailS… |
| mountPlatformSettingsRoutes (allow-list platform settings) | `/v1/admin/platform-settings` | security | /home/ubuntu/HUAKAI/frontend/lib/api/adminSettings.ts (listPlatformSettings :100, updatePl… |
| mountSiteConfigRoute (public site bootstrap config) | `/v1/site/config` | none | /home/ubuntu/HUAKAI/frontend/lib/api/siteConfig.ts (fetchSiteConfig fetches '/v1/site/conf… |
| version + loglevel (admin build info + runtime log level) | `/admin/v1 and /v1/admin` | security | /home/ubuntu/HUAKAI/frontend/lib/api/adminSystem.ts (getVersion :158 -> '/admin/v1/version… |
| mountSystemHealthRoutes (ADMIN-042 read-only system health) | `/v1/admin/system/health and /admin/v1/system/health` | none | ⚠后端缺口: No per-component history/timeseries GET and no alerting-firing wiring: buildSystemHealthSo… |
| mountModuleRegistryRoutes (WAVE H2 module-knowledge spine) | `/admin/v1/modules and /v1/admin/modules` | none | ⚠后端缺口: Read-only registry: no POST/PUT to enable/disable or re-probe a module on demand; frontend… |
| account-modes | `/admin/v1/account-modes` | none | ⚠后端缺口: Read-only catalog by design (single GET; newAccountModeListHandler returns provider.Catalo… |
| quota-policies | `/admin/v1/quota-policies` | quota | ⚠后端缺口: No usage/consumption read endpoint per policy (no GET of current window usage / live count… |
| channel-test-templates | `/admin/v1/channel-test-templates` | security | ⚠后端缺口: Template defines a channel test request (method/path/headers) but there is no execute/run … |
| model-sync | `/admin/v1/model-sync` | money | ⚠后端缺口: POST-only trigger: no GET for last-sync status/history/result-of-record and no scheduler e… |
| pricing catalog (ratios) | `/admin/v1/pricing/ratios` | money | /home/ubuntu/HUAKAI/frontend/lib/api/pricingRatios.ts (BASE='/admin/v1/pricing/ratios', li… |
| balances (credit adjustments) | `/admin/v1/balances` | money | ⚠后端缺口: Only an incremental credit (amount, reason, idempotency_key) is exposed - no debit/deducti… |
| vouchers | `/v1/admin/vouchers` | money | ⚠后端缺口: No GET single voucher by id; no voucher-redemptions/usage history list on admin side. |
| subscriptions | `/v1/admin/subscriptions` | billing | ⚠后端缺口: No GET single-plan client (GET /plans/{id}) and no GET single-assignment client (GET /assi… |
| referrals | `/v1/admin/referrals` | money | ⚠后端缺口: Admin referrals surface is READ-ONLY in backend: only 3 GET handlers (NewAdminReferralsHan… |
| usage (admin usage record stream) | `/admin/v1/usage` | quota | /home/ubuntu/HUAKAI/frontend/lib/api/observability.ts:58 (listUsageRecords -> '/admin/v1/u… |
| billing/claims (ledger claims) | `/admin/v1/billing/claims` | money | ⚠后端缺口: Read-only: no mutate/settle claim write endpoint exposed (only the list GET). If operators… |
| audit-events | `/admin/v1/audit-events` | security | /home/ubuntu/HUAKAI/frontend/lib/api/adminOpsData.ts:161 (listAuditEvents -> '/admin/v1/au… |
| dlq list + replay (dead-letter queue) | `/admin/v1/dlq` | money | ⚠后端缺口: No DLQ purge/delete or bulk-replay endpoint; replay is one-at-a-time by id and non-idempot… |
| cache/l2 (L2 response cache admin) | `/admin/v1/cache/l2` | none | ⚠后端缺口: No flush-all / bulk purge endpoint (eviction is single-key only). Operators wanting a full… |
| alerting: alert-events | `/v1/admin/alert-events` | security | ⚠后端缺口: Platform-admin must pass tenant_id explicitly to read events (noted in ops/page.tsx:488); … |
| alerting: alert-silences | `/v1/admin/alert-silences` | security | ⚠后端缺口: No update/PUT silence endpoint in backend (only create/delete) — to extend a silence windo… |
| announcements | `/v1/admin/announcements` | none | /home/ubuntu/HUAKAI/frontend/lib/api/adminAnnouncements.ts (full CRUD client: listAnnounce… |
| model-pool-bindings (model -> pool binding admin CRUD) | `/admin/v1/model-pool-bindings` | none | ⚠后端缺口: No admin model-list endpoint, so the page forces operators to hand-type model_id (page com… |

## 后端缺口 roadmap（Feature-Preservation；前端无法接，待后端补端点）

| surface | 状态 | 后端缺口 |
|---|---|---|
| mountSystemHealthRoutes (ADMIN-042 read-only system health) | covered | No per-component history/timeseries GET and no alerting-firing wiring: buildSystemHealthSource (routes_systemhealth.go:42-51) leaves alertSvc unset so… |
| mountModuleRegistryRoutes (WAVE H2 module-knowledge spine) | covered | Read-only registry: no POST/PUT to enable/disable or re-probe a module on demand; frontend can only list+filter, cannot trigger a live re-probe or tog… |
| api-keys | uncovered | No GET /{id} single-key detail and no PUT/PATCH update endpoint (only issue/list/revoke); a frontend admin key-management page would want per-key deta… |
| users | partial | Backend exposes the capabilities; the gap is purely frontend. No frontend client for: POST /admin/v1/users (create user), DELETE /admin/v1/users/{id} … |
| account-modes | covered | Read-only catalog by design (single GET; newAccountModeListHandler returns provider.Catalog, account_modes_handler.go:46). No write/enable-disable end… |
| proxies (outbound proxy pool CRUD) | avoidance | No proxy health-check / connectivity-test endpoint (status is operator-set via PUT /{id}/status; LastCheckAt field exists in DTO but no backend route … |
| provider catalog | avoidance | Frontend has no create/update/delete client for the provider catalog (only the list GET is wired); CRUD writes exist in backend but are unreferenced. |
| channel catalog | avoidance | Frontend has no create/update/delete client for the channel catalog (only the list GET is wired); CRUD writes exist in backend but are unreferenced. |
| provider-accounts / pool-accounts (core account CRUD + actions) | avoidance | Frontend does NOT wire bulk-by-tag (mass tag ops), upstream-models (per-account model discovery), or the {id}/enabled PATCH toggle and DELETE — operat… |
| channel-health (read + per-account override) | avoidance | None material — read list/summary/detail and all 3 override actions are wired. (Pause/resume/force-active write to per-account base path, mounted via … |
| credentials (per-account credential CRUD + acquisition flow + import helpers + renew-status) | avoidance | Frontend wires ONLY the helper import routes (paste/cli/csv/json-import), oauth-init, and renew-status (GET). NOT wired: per-account credential CRUD (… |
| pools (pool-group CRUD) | avoidance | Backend NewAdminPoolsHandler exposes only GET list, POST create, GET {id}, PATCH {id} — there is NO DELETE pool-group endpoint, so pools cannot be rem… |
| quota-policies | covered | No usage/consumption read endpoint per policy (no GET of current window usage / live counters); frontend can only CRUD the policy definition, not obse… |
| channel-test-templates | covered | Template defines a channel test request (method/path/headers) but there is no execute/run endpoint here to actually fire a test against a channel usin… |
| model-sync | covered | POST-only trigger: no GET for last-sync status/history/result-of-record and no scheduler endpoint. Frontend shows the per-call result (added/updated/d… |
| billing settings | partial | Settings GET/PUT only expose a single key (billing.StreamInputOnlyInterruptedPolicyKey via d.Store.Get with billing.StreamInputOnlyInterruptedPolicyKe… |
| balances (credit adjustments) | covered | Only an incremental credit (amount, reason, idempotency_key) is exposed - no debit/deduction path, no GET to list prior adjustments/history (history m… |
| vouchers | covered | No GET single voucher by id; no voucher-redemptions/usage history list on admin side. |
| disputes (admin) | uncovered | No admin GET single dispute by id (only list + resolve); resolve is the only mutation (no reject/reopen/assign state transitions exposed as distinct e… |
| subscriptions | covered | No GET single-plan client (GET /plans/{id}) and no GET single-assignment client (GET /assignments/{id}) on the frontend — both read-by-id endpoints ex… |
| referrals | covered | Admin referrals surface is READ-ONLY in backend: only 3 GET handlers (NewAdminReferralsHandler, NewAdminReferralRewardsHandler, NewAdminReferralOvervi… |
| billing/claims (ledger claims) | covered | Read-only: no mutate/settle claim write endpoint exposed (only the list GET). If operators need to reconcile/void a claim from UI, backend would need … |
| dlq list + replay (dead-letter queue) | covered | No DLQ purge/delete or bulk-replay endpoint; replay is one-at-a-time by id and non-idempotent. Bulk operator recovery would need a backend addition. |
| cache/l2 (L2 response cache admin) | covered | No flush-all / bulk purge endpoint (eviction is single-key only). Operators wanting a full L2 flush from UI would need a backend addition. |
| usage analytics (mountUsageAdminRoutes leaderboard/perf/health) | partial | Two backend GETs have ZERO frontend client: /v1/admin/usage/perf-metrics/by-bucket (routes_usageadmin.go:30, NewPerfMetricsByBucketHandler) and /v1/ad… |
| alerting: alert-rules CRUD | partial | Frontend has no create/update/delete/get-by-id client for alert rules, and no page renders the rule list. Operators cannot create or edit alerting rul… |
| alerting: alert-events | covered | Platform-admin must pass tenant_id explicitly to read events (noted in ops/page.tsx:488); not a backend capability gap, an ergonomics note. |
| alerting: alert-silences | covered | No update/PUT silence endpoint in backend (only create/delete) — to extend a silence window operators delete+recreate. Matches backend Service interfa… |
| moderation (content moderation admin control plane) | partial | POST /admin/v1/moderation/api-keys/{id}/unban (mount.go:51) has NO frontend client — deliberately deferred (moderation.ts:7 'unban 后端刻意延后'; system/pag… |
| notifications | partial | No admin-side broadcast/worker-stats/per-user-settings UI exists; backend exposes those 4 admin endpoints but frontend never references them. Broadcas… |
| routes-admin / routing wizard | uncovered | Backend has NO update/PUT handler for routes — only Create, List, Get-by-id, Delete (controlhttp/routeadmin_handler.go:84-89). A routing wizard that e… |
| tls-fingerprint-profiles | partial | Frontend exposes only the read-only list. The 5 write/detail endpoints (POST create, GET {id}, PUT {id} update, POST {id}/status enable-disable, DELET… |
| model-pool-bindings (model -> pool binding admin CRUD) | covered | No admin model-list endpoint, so the page forces operators to hand-type model_id (page comment D3-B); priority_weighted selection_mode is exposed in U… |
| model capabilities + aliases admin (capability override, alias bulk-import, capability-binding inspect) | uncovered | No frontend admin surface at all to drive capability overrides or alias bulk-import; no GET to read current per-model capabilities back (only the PUT … |


## 推荐下一刀顺序（据本审计 + 风险分层）

### A. 可立即推进（低风险、自包含、不需 Owner 闸）
1. **alert-rules 写 CRUD** `/v1/admin/alert-rules`（收口 alerting 面；event/silence 已覆盖，rules 仅 list 接线。schema 复杂但是纯配置 CRUD）
2. **usage analytics 补 2 个 GET** `/v1/admin/usage/perf-metrics/by-bucket` + 另一个（只读、最小）
3. **exporthttp CSV 导出** 4 个只读下载（payments/usage/orders/refunds export.csv；money 标签但只读下载、低风险）
4. **notifications admin** 广播 / worker-stats / per-user settings（low risk）
5. **tls-fingerprint-profiles 写操作** 5 个（create/get/update/status/delete；安全姿态配置，运维闸控，做时知会 Owner 但非阻断）

### B. 需 Owner 确认（money / billing / auth 敏感 —— 风险规则要求）
- **payments admin 写**（退款执行 POST {id}/refund、退款审批、provider 凭证）——**payment logic 高风险**，必须 Owner 确认
- **cache-price-overrides** `/v1/admin/cache-price-overrides`（定价覆盖，money）
- **disputes admin**（争议 resolve，billing-risk）
- **api-keys admin**（管理员签发 key、明文一次性展示，security）
- **users 敏感写**（create/delete、2FA force-disable、passkey 清除——auth core 邻近）

### C. L 大件（留最后 / 需后端先补）
- **routes-admin / 路由可视化向导** `/v1/admin/routes`：**后端无 PUT update**（仅 create/list/get/delete）→ 要么先补后端 update、要么前端先接 create/list/get/delete 并登记 update roadmap
- **model capabilities + aliases admin**：后端无读当前能力的 GET（仅 PUT 覆盖）→ 编辑体验受限，部分 roadmap

### D. 避让（待 proxies 分支 `feat/frontend-admin-proxies` 合并解锁）
provider catalog CRUD 写、channel catalog CRUD 写、proxies、provider-accounts 写（bulk-by-tag/upstream-models/enabled toggle/delete）、pools、credentials 写、channel-health —— 这些面 proxies 分支正在做 IA 重排，合并后再补写操作。

> 勾选方式：每接一刀，把对应 surface 从「零覆盖/部分」移到「已覆盖」，并在此节划掉。
