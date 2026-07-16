# HUAKAI 文档归并删除日志

> 本日志只记录通过亲读实现代码和调用链得出的删除判定。Owner 要求的 `git rm` 已尝试，但当前
> 沙箱把 `.git/index.lock` 挂成只读；因此改用补丁删除同一批 tracked 文件。工作树结果均为 `D`，
> 未 stage、未 commit、未 push，所有文件仍可从 git history 恢复。  
> 基线：`feat/ui-density-overview` / `0f7d6b69`；核验日期：2026-07-15（UTC）。

## 1. 本批统计

| 领域 | 删除数 | 主要原因 | 替代入口 |
| --- | ---: | --- | --- |
| frontend | 34 | 旧 Next/Tailwind 页面树、已实施切片、被综合稿取代的独立稿、失真的覆盖矩阵 | `docs/architecture/frontend-SSOT.md` |
| observability-logging | 21 | 已实施日志/指标/告警计划、错误 feature tree、被 `usage_records` 合并等价取代的另表方案 | `docs/architecture/observability-logging-SSOT.md` |
| deployment | 5 | 已实施启动门/部署选项计划、含敏感且过期环境细节的单机说明 | `docs/architecture/deployment-SSOT.md` |
| **合计** | **60** | — | `docs/architecture/PROJECT-SSOT-INDEX.md` |

> 边界修正：`docs/process/plans/2026-05-14-t6-frontend-audit-codex.md` 虽被 manifest 分到
> frontend，但内容属于 trust-chain 审计展示，依 Owner 保护边界保留，不计入本批。

## 2. frontend 删除明细（34）

| 文件 | 删除理由 | 核过的代码证据 |
| --- | --- | --- |
| `docs/frontend/2026-06-24-源码梳理与前端编写方案.md` | 把前端写成“几乎无业务源码”；现已与真实路由和页面覆盖严重相反 | `frontend/src/app/router.tsx:87-190` |
| `docs/frontend/2026-06-25-页面清单-三镜对齐.md` | 重建期页面草图已被真实路由表和当前导航取代 | `frontend/src/app/router.tsx:87-190`；`frontend/src/app/nav.ts:32-163` |
| `docs/frontend/BUILD-SPEC.md` | 旧 Next/Tailwind 构建规格；当前是 Vite build + Go embed | `frontend/package.json:6-28`；`backend/Dockerfile:18-45` |
| `docs/frontend/FUSION-LAYOUT-PLAN-v3.md` | 面向旧 App Router/旧组件树的布局计划，路径与当前 React Router 壳不符 | `frontend/src/app/router.tsx:82-190`；`frontend/src/app/nav.ts:32-163` |
| `docs/frontend/IA-PROPOSAL-v2-2026-06-14.md` | 旧技术栈和旧路由 IA 已被当前双壳导航实现取代 | `frontend/package.json:6-28`；`frontend/src/app/nav.ts:32-163` |
| `docs/frontend/PAGE-PROMPTS.md` | 逐页提示词仍要求 Next/Tailwind、`app/(admin)` 和生成客户端，不能指导当前代码 | `frontend/package.json:6-28`；`frontend/src/app/router.tsx:82-190`；`frontend/src/lib/api.ts:20-75` |
| `docs/frontend/SUB2API-FRONTEND-REUSE-DRILL-2026-06-15.md` | 旧“拿现成前端做底座”演习已被 Owner 授权的自建 Vite SPA 和现有代码取代 | `frontend/package.json:6-28`；`backend/Dockerfile:18-45` |
| `docs/frontend/WIRING-COVERAGE-MATRIX.md` | 把 2FA、Passkey、OAuth、路由、告警等已接线能力标成缺失 | `frontend/src/features/profile/api.ts:64-145`；`frontend/src/features/routeadmin/api.ts:22-87`；`frontend/src/app/router.tsx:132-145` |
| `docs/process/gap-designs/usage-dashboard.md` | 拟议包/迁移/端点形态未采用；现有实现直接聚合 `usage_records` 并按身份收敛 | `backend/internal/usageanalyticshttp/handler.go:1-25,86-145`；`backend/cmd/gateway/routes_usageadmin.go:11-36` |
| `docs/process/plans/2026-05-12-frontend-brief-market-codex.md` | 调研派工过程已结束；受保护的调研产物另存 `docs/research`，计划不再承担现状解释 | `frontend/package.json:6-28`；`frontend/src/app/router.tsx:82-190` |
| `docs/process/plans/2026-05-12-frontend-round9-codex-prompt.md` | 针对已删除的 Next/Tailwind 故障现场和旧 Dashboard | `frontend/package.json:6-28`；`frontend/src/pages/Dashboard.tsx:8` |
| `docs/process/plans/2026-05-13-frontend-feature-parity-sub2api-vs-round10-codex.md` | Round 10 对照派工已结束；当前能力须由真实路由/API 核验，研究证据另受保护 | `frontend/src/app/router.tsx:87-190` |
| `docs/process/plans/2026-05-13-frontend-round10-codex-prompt.md` | 引用已不存在的 `frontend/app/*`、Tailwind 与 Next layout | `frontend/package.json:6-28`；`frontend/src/app/router.tsx:82-190` |
| `docs/process/plans/2026-05-13-frontend-round9-codex-execution.md` | 旧 Next/Tailwind Dashboard 实施记录，代码树已整体重建 | `frontend/package.json:6-28`；`frontend/src/pages/Dashboard.tsx:8` |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-research-codex-brief.md` | 一次性调研派工；其研究输出受保护保留，旧 Tailwind token 现场不再存在 | `frontend/package.json:6-28`；`frontend/src/styles/tokens.css:1` |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-v2-codex-brief.md` | 一次性 v2 调研派工已结束；当前 token 由代码维护 | `frontend/src/styles/tokens.css:1` |
| `docs/process/plans/2026-05-13-frontend-ui-aesthetic-v3-codex.md` | 一次性 v3 调研派工已结束；当前 token 由代码维护 | `frontend/src/styles/tokens.css:1` |
| `docs/process/plans/2026-05-20-renew-page-fix-claude.md` | 旧 Next `/renew` mock 修复草案；当前凭证续期页已重建为 React Router 模块 | `frontend/src/app/router.tsx:134-136`；`frontend/src/features/credentialrenew/CredentialRenewPage.tsx:1` |
| `docs/process/plans/2026-05-20-renew-page-fix-codex.md` | 与上项并行的旧栈草案，路径、类型层和 mock 现状均已消失 | `frontend/src/app/router.tsx:134-136`；`frontend/src/features/credentialrenew/CredentialRenewPage.tsx:1` |
| `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-claude.md` | 独立计划已被同日 synthesis 指名取代，当前运营路由已落地 | `frontend/src/app/router.tsx:107-149` |
| `docs/process/plans/2026-05-25-ae-d3-admin-ui-detailed-cause-codex.md` | 独立计划已被同日 synthesis 指名取代，当前运营路由已落地 | `frontend/src/app/router.tsx:107-149` |
| `docs/process/plans/2026-06-02-frontend-dashboard-real-codex.md` | 针对旧 Next Dashboard 去 mock 的实施计划；当前 Dashboard 已是新 SPA 模块 | `frontend/src/pages/Dashboard.tsx:8`；`frontend/src/features/dashboard/api.ts:1` |
| `docs/process/plans/2026-06-16-frontend-fusion-ia-blueprint.md` | 仍锁 Next/Tailwind 并把大量当前页面列为缺失，已被真实路由和 SSOT 取代 | `frontend/package.json:6-28`；`frontend/src/app/router.tsx:87-149` |
| `docs/process/plans/2026-06-17-wave2-alert-rules-crud-frontend.md` | 告警规则 CRUD 切片已落到当前告警页面 | `frontend/src/app/router.tsx:144-145`；`frontend/src/features/alerting/RulesTab.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-alert-silences-crud-frontend.md` | 告警静默 CRUD 切片已落到当前告警页面 | `frontend/src/app/router.tsx:144-145`；`frontend/src/features/alerting/SilencesTab.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-announcement-crud-frontend.md` | 公告管理切片已落到当前公告页面 | `frontend/src/app/router.tsx:115-117`；`frontend/src/features/announcements/AnnouncementsPage.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-channel-test-template-crud-frontend.md` | 渠道测试模板 CRUD 切片已落到当前页面 | `frontend/src/app/router.tsx:122-124`；`frontend/src/features/channeltesttemplates/ChannelTestTemplatesPage.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-model-sync-trigger-frontend.md` | 模型同步触发切片已落到当前上游模型页面 | `frontend/src/app/router.tsx:120-123`；`frontend/src/features/upstreammodels/UpstreamModelsPage.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-ops-data-panel-frontend.md` | 运维数据面切片已落到 Ops/DLQ/cache 等真实页面 | `frontend/src/app/router.tsx:128-145`；`frontend/src/features/ops/OpsPage.tsx:1` |
| `docs/process/plans/2026-06-17-wave2-subscription-lifecycle-ops-frontend.md` | 订阅生命周期管理切片已落到运营订阅页面 | `frontend/src/app/router.tsx:112-114`；`frontend/src/features/subscriptionsadmin/SubscriptionsAdminPage.tsx:1` |
| `docs/process/plans/2026-06-24-frontend-spa-kickoff.md` | SPA 重建启动过程已完成；当前代码和 Owner 迁移决定足以说明结果 | `frontend/package.json:6-28`；`frontend/src/app/router.tsx:82-190` |
| `docs/process/plans/2026-06-25-frontend-embed-single-binary.md` | 单二进制 embed 切片已完整落地，过程计划被部署/前端 SSOT 取代 | `backend/Dockerfile:18-45`；`backend/cmd/gateway/middleware.go:128-133` |
| `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes-claude.md` | 独立稿已被无后缀综合稿指名取代；当前 Dashboard 代码保留 | `frontend/src/pages/Dashboard.tsx:8`；`frontend/src/features/dashboard/api.ts:1` |
| `docs/process/plans/2026-07-13-operator-dashboard-live-data-fixes-codex.md` | 独立稿已被无后缀综合稿指名取代；当前 Dashboard 代码保留 | `frontend/src/pages/Dashboard.tsx:8`；`frontend/src/features/dashboard/api.ts:1` |

## 3. observability-logging 删除明细（21）

| 文件 | 删除理由 | 核过的代码证据 |
| --- | --- | --- |
| `docs/architecture/runtime-logic/runtime-log-sink.md` | 与代码重复且把“连接池已开”等同“schema 已就绪”、把 slog message 视为已脱敏；由新 SSOT 纠偏 | `backend/cmd/gateway/wiring.go:837-864`；`backend/internal/logfacade/logfacade.go:63-87` |
| `docs/process/feature-tree/observability-analytics.md` | 把 Prometheus/告警写成缺失，并提出与隐私规范冲突的 prompt 内容日志；混合真伪不能继续作现状树 | `backend/internal/otelbridge/provider.go:18-44`；`backend/cmd/gateway/middleware.go:114-126`；`backend/internal/alerting/service.go:273-351` |
| `docs/process/gap-critiques/relay-log.md` | 针对另建 relay log 的旧方案评议；当前 `usage_records` 已形成更小、更安全的合并等价 | `backend/internal/meusagehttp/handler.go:58-88,105-163` |
| `docs/process/gap-critiques/ops-suite.md` | 只评议已删除的旧 ops-suite 设计，且其无租户 alert 表、拟议迁移与 runner 接线前提不等于当前实现 | `backend/sql/migrations/0103_alerting.up.sql:5-52`；`backend/internal/alerting/postgres_store.go:352-415`；`backend/internal/alerting/scheduler.go:94-160` |
| `docs/process/gap-designs/ops-suite.md` | 告警部分已由真实 scheduler/service 吸收；未落地的 synthetic/scheduled 能力已迁入 SSOT Mandatory Roadmap | `backend/internal/alerting/scheduler.go:94-160`；`backend/internal/alerting/service.go:273-351` |
| `docs/process/gap-designs/relay-log.md` | 拟议的独立表和旁路写链未采用；逐请求日志由 `usage_records` 安全等价承载 | `backend/internal/meusagehttp/handler.go:58-88,105-163` |
| `docs/process/plans/2026-05-14-m3-observability-admin-codex.md` | 三个只读管理查询端点的实施计划已落地，过程文档不再解释现状 | `backend/cmd/gateway/routes_usageadmin.go:11-36`；`backend/cmd/gateway/routes.go:955-986` |
| `docs/process/plans/2026-05-15-f-obs-005-dlq-priority-claude.md` | 并行草案中的 DLQ/优先级/replica 机制已进入迁移和生产代码；现状由代码与 SSOT 描述 | `backend/sql/migrations/0015_obs_dlq_extend.up.sql:1-109`；`backend/internal/billing/settler.go:907-939` |
| `docs/process/plans/2026-05-15-f-obs-005-dlq-priority-codex.md` | 同一已实施切片的另一份独立草案，避免双源 | `backend/sql/migrations/0015_obs_dlq_extend.up.sql:1-109`；`backend/internal/billing/settler.go:907-939` |
| `docs/process/plans/2026-06-01-s2-021-codex.md` | 日志脱敏实施计划已由 privacy/logfacade 代码吸收；剩余 message/标准 log 缺口已进入 DRIFT | `backend/internal/privacy/redactor.go:15-62`；`backend/internal/logfacade/logfacade.go:57-95` |
| `docs/process/plans/2026-06-03-obs002-otel-codex.md` | OTel metrics→Prometheus bridge 已实现，旧计划被现状 SSOT 取代 | `backend/internal/otelbridge/provider.go:18-44`；`backend/cmd/gateway/wiring.go:1540-1579` |
| `docs/process/plans/2026-06-06-alert-eval-loop-codex.md` | 告警评估循环已实现并带 leader lock | `backend/internal/alerting/scheduler.go:94-160` |
| `docs/process/plans/2026-06-06-alert-rules-codex.md` | 告警规则状态机和 CRUD 路由已实现 | `backend/internal/alerting/service.go:273-351`；`backend/cmd/gateway/routes_alerting.go:10-22` |
| `docs/process/plans/2026-06-07-w2-alert-delivery-codex.md` | 告警触发/恢复/投递已进入 service，过程计划不再作权威 | `backend/internal/alerting/service.go:273-351` |
| `docs/process/plans/2026-06-07-w2-composite-alertmetrics-codex.md` | 复合告警快照已实现用量与账号健康聚合 | `backend/internal/alertmetrics/composite.go:11-27,57-203` |
| `docs/process/plans/2026-06-19-bridge-l2-cache-metrics.md` | L2/cache 指标桥切片已进入 runtime metrics bridge | `backend/cmd/gateway/wiring.go:1540-1579`；`backend/internal/otelbridge/expvarbridge.go:26-126` |
| `docs/process/plans/2026-06-19-runtime-alert-metrics.md` | runtime 指标与告警 catalog/复合快照已实现 | `backend/internal/alertmetrics/catalog.go:13-31`；`backend/internal/alertmetrics/composite.go:94-203` |
| `docs/process/plans/2026-07-02-logging-observability-plan-claude.md` | 双日志栈融合升级已进入 main、logfacade 和 sink；残余风险改由 DRIFT 管理 | `backend/cmd/gateway/main.go:31-74`；`backend/internal/logsink/capture.go:14-159` |
| `docs/process/plans/2026-07-02-slog-facade-unification-claude.md` | slog 门面与共享 loglevel 已落地；过程计划不应遮蔽 message 仍未扫描的真实缺口 | `backend/internal/logfacade/logfacade.go:45-99`；`backend/cmd/gateway/main.go:56-74` |
| `docs/process/plans/2026-07-12-runtime-log-sink-claude.md` | 大部分运行日志切片已实现，但 admin usage by-request-id 未落地；缺口已转 Mandatory Roadmap | `backend/internal/logsink/store_postgres.go:67-125`；`backend/cmd/gateway/routes.go:965-986`；`frontend/src/features/logsdiag/RuntimeLogsPanel.tsx:117-188` |
| `docs/process/plans/2026-07-13-batch4-pages-spec-claude.md` | 第四批页面规格已落实为当前路由和运行日志/用量/健康等页面，旧页面级过程稿不再作现状依据 | `frontend/src/app/router.tsx:89-149`；`frontend/src/features/logsdiag/RuntimeLogsPanel.tsx:117-188` |

## 4. deployment 删除明细（5）

| 文件 | 删除理由 | 核过的代码证据 |
| --- | --- | --- |
| `docs/ops/remote-dev-setup.md` | 单台旧主机说明硬编码 IP、用户、路径、开发口令、代理和 CLI 凭据位置；迁移数也已从“12”增长到 0184，既过期又扩大安全暴露 | `backend/go.mod:1-24`；`backend/sql/migrations/0184_burst_value_comment.up.sql:1` |
| `docs/process/plans/2026-06-01-s1-019-release-mode-codex.md` | 显式 release mode 启动门已经实现 | `backend/cmd/gateway/config.go:63-81` |
| `docs/process/plans/2026-06-20-b0-email-gate-exclude-system-tenant.md` | active tenant 查询已经以 `id > 0` 排除系统哨兵 | `backend/internal/email/settings_store.go:112-140` |
| `docs/process/plans/2026-06-23-deploy-no-domain-direct-option.md` | 无域名/IP 直连 compose 已实现，风险与绑定选项已在代码配置中说明 | `backend/docker-compose.direct.yml:1-14,31-88` |
| `docs/process/plans/2026-06-23-soften-email-gate.md` | 邮箱门默认软化、显式开关恢复严格模式已经实现 | `backend/cmd/gateway/email_gate.go:8-30`；`backend/cmd/gateway/wiring.go:1037-1050` |

## 5. 未删除但重新归类

- `docs/process/plans/2026-05-14-t6-frontend-audit-codex.md`：trust-chain 保护族。
- `docs/process/plans/2026-05-14-l1-prod-wiring-codex.md`：触及 trust-chain 启动接线，保护。
- `docs/process/plans/2026-06-03-auth006-bootstrap-ttl-codex.md`：归 auth/session，等待该领域核验。
- `docs/ops/2026-05-08-bedrock-anthropic-cli-setup.md`、`docs/ops/anthropic-prompt-cache-ttl.md`：
  归 provider/credentials/cache，不在部署波误删。
- `docs/process/plans/2026-07-15-security-monitoring-module-claude.md`：同日活跃、Owner-gated。
- `docs/process/plans/2026-07-15-mvp-launch-blockers.md` 与上线前验证：当前发布输入。
