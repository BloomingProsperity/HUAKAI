# HUAKAI 文档—代码不一致与代码疑点台账

> 建档：2026-07-15（UTC）  
> 核验原则：每条“代码实际怎样”均来自亲读实现与必要调用链；搜索只用于找文件。  
> 处置原则：本表只记录、分类，不在文档归并波擅自改生产代码。`代码疑似缺陷` 需要
> Owner/Claude 决定是否开实现切片。

| 文档路径 | 文档怎么说 | 代码实际怎样（file:line） | 判定（文档过期/文档错/代码疑似缺陷） | 建议处置 |
| --- | --- | --- | --- | --- |
| `docs/process/decisions/DR-004-frontend-framework.md:14-20,70-83` | 当前栈锁为 React + Next.js App Router + Tailwind，且类型/客户端必须 OpenAPI codegen | 实际依赖只有 React、React Router、Vite、TypeScript，构建为 `tsc -b && vite build`；路由用 `createBrowserRouter`（`frontend/package.json:6-28`；`frontend/src/app/router.tsx:82-190`） | **文档过期** | 保留决策记录但尽快把状态改为被 2026-06-19 Owner 迁移决定取代，并修传播清单。 |
| `docs/RULES.md:100-108`（TS-002） | 前端必须 Next.js App Router + Tailwind | 当前是 Vite + React Router，依赖中无 Next/Tailwind（`frontend/package.json:6-28`） | **文档过期** | Owner 修订治理规则；修订前以本 SSOT 和 2026-06-19 授权决定解释现状。 |
| `docs/RULES.md:100-108`（TS-004）与 `docs/process/decisions/DR-004-frontend-framework.md:74-77` | OpenAPI 是前端类型唯一来源，不得手写共享 DTO | `ApiErrorShape`、账号目录/创建 DTO 等均手写（`frontend/src/lib/api.ts:20-42`；`frontend/src/features/accounts/createTypes.ts:25-91`） | **代码疑似缺陷** | 单开契约生成/迁移切片；迁移前禁止宣称已遵守 codegen 规则。 |
| `docs/process/plans/2026-06-19-frontend-spa-migration.md:5-23` | Owner 锁 Vite SPA，同时写明继续使用 Tailwind | Vite/React Router/embed 已落地，但依赖中没有 Tailwind（`frontend/package.json:6-28`；`backend/Dockerfile:18-45`） | **文档错**（部分落地） | 保留 Owner 栈迁移决定，给 Tailwind 语句加“未采用/待重新决定”标注。 |
| `docs/frontend/ASK-HERMES-DESIGN-v1.md:24` | 方案沿用 Next.js 15 + Tailwind 栈 | 当前为 Vite + React Router 且无 Tailwind（`frontend/package.json:6-28`） | **文档过期**（Owner-gated 设计局部） | 不删除 Owner-gated 方案；加现栈迁移注记，交互/诊断目标继续保留。 |
| `docs/frontend/ASK-HERMES-DESIGN-v1.md:143-150` | Ask Hermes 前置要求 ApiError 保存 request_id、retry、details 与响应头 | 当前 `ApiError` 只保存 status/code/message，解析时丢弃其他 body 字段与 headers（`frontend/src/lib/api.ts:20-33,57-69`） | **代码疑似缺陷** | 作为 Ask Hermes 前置小切片；未补前不得声称错误上下文信封可用。 |
| `docs/architecture/backend-feature-inventory-codex.md:190-200` | 无隐式 tenant scope 的全局平台管理员可能因 `tenant_id` 漏传被新建弹窗阻断 | 弹窗虽接收 `tenantId`，目录与创建调用均不传；后端强制无 scope 平台管理员携带正数 query（`frontend/src/features/accounts/CreateAccountModal.tsx:22-47,62-72`；`frontend/src/features/accounts/createApi.ts:17-84`；`backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-345`） | **代码疑似缺陷**（文档判断已证实） | 优先开 P0 前端修复并加 platform_admin 无 scope 判别测试；本波不改代码。 |
| `docs/architecture/backend-feature-inventory-codex.md:190-199` | 前端应按 Released/Experimental/Scaffold 与凭证可用性表达账号模式 | 后端目录返回 `serving_readiness`，前端 `AccountMode` 没有该字段，UI 无法消费闭合结论（`backend/internal/adminhttp/catalog.go:35-59`；`frontend/src/features/accounts/createTypes.ts:25-41`） | **代码疑似缺陷** | 在账号创建修复切片同步补 DTO、展示和判别测试。 |
| `docs/frontend/2026-06-24-源码梳理与前端编写方案.md:7-17` | `frontend/` 几乎没有业务源码，最大缺口是前端零暴露 | 当前路由表已经映射用户与运营页面，安全、用量、账号、告警等均有真实模块（`frontend/src/app/router.tsx:87-149`） | **文档过期** | 本批删除；有效目标由 frontend SSOT 和当前活跃设计承接。 |
| `docs/frontend/WIRING-COVERAGE-MATRIX.md:20-62` | 2FA、Passkey、OAuth 绑定、路由表、告警等仍未接线 | 2FA/Passkey/OAuth 调用已接入，路由规则 CRUD 已接入，告警页已映射（`frontend/src/features/profile/api.ts:64-145`；`frontend/src/features/routeadmin/api.ts:22-87`；`frontend/src/app/router.tsx:132-145`） | **文档过期** | 本批删除；若需要覆盖矩阵，应从真实路由/API 测试自动生成新工件。 |
| `docs/process/gap-designs/usage-dashboard.md:10-33,71-83` | 需要新增另一套 analytics 包、六个端点和迁移 0077 | 当前自助时间序列在 `usageanalyticshttp` 中直接聚合 `usage_records`，身份强制限定 tenant/API key；管理端使用既有 `/v1/admin/usage/*` 路由（`backend/internal/usageanalyticshttp/handler.go:1-25,86-145`；`backend/cmd/gateway/routes_usageadmin.go:11-36`） | **文档过期** | 本批删除；尚未完成的排名/RPM/TPM 只能作为明确 roadmap，不能复活旧架构。 |
| `docs/process/feature-tree/observability-analytics.md:3,29,45,63-81` | 无 Prometheus scrape、无 OTel SDK/告警，且建议可选 prompt 内容日志 | 已有默认关闭的 OTel metrics→Prometheus bridge 和 admin-gated `/metrics`，也有告警调度/投递；但没有分布式 tracing。可选 prompt 日志又违背当前隐私规范（`backend/internal/otelbridge/provider.go:18-44`；`backend/cmd/gateway/middleware.go:114-126`；`backend/internal/alerting/service.go:273-351`） | **文档过期**（混合真/假断言） | 本批删除；真实能力与剩余 tracing/retention 缺口已迁入 observability SSOT。 |
| `docs/specs/privacy-no-user-data-logs.md:38-49,87-128` | transit 应 forward-only zero-copy；三通道及所有外部 sink 统一经 Redactor，严禁 freeform message 旁路 | middleware 会整段缓冲 body；slog 门面只清 attrs、不扫 message；标准库 `log` 明确直写 stderr、绕开门面与 sink（`backend/internal/privacy/middleware.go:39-75`；`backend/internal/logfacade/logfacade.go:57-87`；`backend/cmd/gateway/main.go:66-74`） | **代码疑似缺陷** | 规范保留为 Mandatory Roadmap；优先审计 message/标准 log 旁路，明确 zero-copy 是目标还是需要修正文档。 |
| `docs/architecture/runtime-logic/runtime-log-sink.md:18-25` | DB 就绪后才启动 sink，且 slog 路径已脱敏 | 当前只在打开连接池后启动 sink，`HUAKAI_AUTO_MIGRATE` 在其后执行；空库早期 flush 可因表未建而丢弃。slog message 也未扫描（`backend/cmd/gateway/wiring.go:837-864`；`backend/internal/logfacade/logfacade.go:63-87`） | **代码疑似缺陷** + **文档错** | 重复说明本批删除；另开小切片把迁移提前到 sink Start 前，并审计 slog message。 |
| `docs/process/plans/2026-07-12-runtime-log-sink-claude.md:30-38,53-56` | 成功标准含 admin usage 按 request_id 单查与前端运行日志/请求记录联合检索 | 已挂运行日志列表/清理/健康，但没有计划中的 admin usage by-request-id 路由；前端只筛运行日志（`backend/cmd/gateway/routes.go:965-986`；`frontend/src/features/logsdiag/RuntimeLogsPanel.tsx:117-188`） | **文档过期**（计划未全落地） | 本批删除计划；未实现能力转为 observability SSOT 的 Mandatory Roadmap。 |
| `docs/deploy/go-live-readiness.md:19-27` 与 `docs/deploy/production-bootstrap.md:47-62` | 标题/导语称“三道 production 启动硬门” | 邮箱门默认只 warn，只有显式 `HUAKAI_REQUIRE_EMAIL_GATE=true` 才拒启（`backend/cmd/gateway/email_gate.go:8-30`；`backend/cmd/gateway/wiring.go:1037-1050`） | **文档错**（正文后段已自我纠正） | 保留 runbook，统一改为“两道默认硬门 + 一道可选严格门”。 |
| `docs/deploy/production-bootstrap.md:1-5,190-193` | 当前控制台未实现，生产只能 API-only | 镜像构建并内嵌 Vite SPA，网关提供 SPA 回退，真实页面路由已接入（`backend/Dockerfile:18-45`；`backend/cmd/gateway/middleware.go:128-133`；`frontend/src/app/router.tsx:87-190`） | **文档过期** | 保留部署步骤并尽快删去 API-only 断言；修订前由 deployment SSOT 纠偏。 |
| `docs/ops/remote-dev-setup.md:1-23` | 把单台主机/IP/用户/路径、开发 DB 口令、代理与 CLI 凭据路径写成当前共享环境 | 当前代码工具链为 Go 1.25 且迁移已到 0184；文档仍称 12 个 migration，并暴露环境特定连接信息（`backend/go.mod:1-24`；`backend/sql/migrations/0184_burst_value_comment.up.sql:1`） | **文档过期**（兼有安全风险） | 本批删除；若仍需远程接入，另写不含主机、口令和凭据路径的参数化 runbook。 |

## Owner/Claude 优先复核

1. **P0 候选：平台管理员新建账号漏传 tenant scope。** 这是直接阻断运营录入账号的真实调用链问题。
2. **隐私边界：slog message 与标准库 log 旁路。** 涉及“不记录用户内容”的核心承诺，应先确认风险面再开修复。
3. **空库自迁移：sink 在 migration 前启动。** 影响的是早期运行日志完整性，不阻断主业务，但会让启动故障证据丢失。
4. **治理分叉：正式规则仍锁 Next/Tailwind/codegen。** 其中栈声明是文档过期，codegen 则是当前代码未满足正式约束，二者不能混成同一类。
