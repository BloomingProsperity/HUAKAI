# 前端领域唯一权威文档（SSOT）

> 建档：2026-07-15（UTC）  
> 核验基线：分支 `feat/ui-density-overview`，代码基线 `0f7d6b69`。  
> 权威顺序：实现代码 > 本 SSOT > 保留的契约、决策和计划。发现不一致时先登记到
> `docs/architecture/DOC-CODE-DRIFT.md`，不得用旧文档反推代码现状。

## 1. 当前技术栈与交付形态

- 当前前端是 React 18 + TypeScript + Vite 5 + React Router 6；构建命令是
  `tsc -b && vite build`。依赖清单没有 Next.js、Tailwind 或 OpenAPI 代码生成器
  （`frontend/package.json:6-28`）。
- 路由由 `createBrowserRouter` 驱动；公开入口与 `RequireAuth` 保护的应用壳明确分开，
  已实现页通过 `BUILT_PAGES` 映射接入，未知路径回到首页
  （`frontend/src/app/router.tsx:82-190`）。
- 生产镜像先构建 Vite 产物，再用 `-tags embed` 编进 Go 二进制
  （`backend/Dockerfile:18-45`）；网关未命中 API 路由时交给内嵌 SPA
  （`backend/cmd/gateway/middleware.go:128-133`）。
- 因此，仍把前端描述为 Next.js App Router、Tailwind 页面树或独立前端服务的文档，
  不能作为当前实现依据。

## 2. 路由、鉴权与 API 访问

- 用户门户和运营台共享一个受保护应用壳；`/setup`、登录、OAuth 回调、找回/重置密码、
  邮箱验证、设备确认及公开页面在壳外
  （`frontend/src/app/router.tsx:162-190`）。
- API 基础层使用同源相对路径，按 URL 选择 session/admin/API-key Bearer；请求类型与响应
  DTO 目前由前端手写，而不是从 OpenAPI 生成
  （`frontend/src/lib/api.ts:17-75`）。
- 账户安全页已真实接入 2FA、Passkey 和 OAuth 绑定，不再是旧覆盖矩阵所说的“未接线”
  （`frontend/src/features/profile/api.ts:64-145`）。
- 路由规则管理页已经接入列表、创建、更新、启停和删除，不再是旧矩阵所说的“无前端”
  （`frontend/src/features/routeadmin/api.ts:22-87`）。

## 3. 能力边界：页面存在不等于能力闭环

| 能力 | 当前真实状态 | 代码证据 |
| --- | --- | --- |
| 主要用户/运营页面 | 已有真实路由映射；未映射项才落占位页 | `frontend/src/app/router.tsx:87-156` |
| 运行日志 | 可筛选、翻页、手动刷新及 3 秒轮询；前端没有清理动作 | `frontend/src/features/logsdiag/RuntimeLogsPanel.tsx:43-188` |
| 备份 | 只展示 manifest；后端也只有只读 GET，没有备份/恢复写操作 | `frontend/src/features/backup/BackupPage.tsx:8-45`；`backend/cmd/gateway/routes_backup.go:11-28` |
| 模块注册表 | 只读知识与运行状态；后端只有 GET，没有“开关”写口 | `frontend/src/features/moduleregistry/ModuleRegistryPage.tsx:16-22`；`backend/cmd/gateway/routes_modules.go:11-32` |
| Ask Hermes | 仍是 Owner-gated 设计，尚未形成全局抽屉和错误上下文信封 | `docs/frontend/ASK-HERMES-DESIGN-v1.md:5-11`；`frontend/src/lib/api.ts:20-33` |
| SPA 单二进制 | 已落地 | `backend/Dockerfile:18-45`；`backend/cmd/gateway/middleware.go:128-133` |

## 4. 已确认的实现风险与未闭环项

以下不是本次文档归并要修的代码；统一进入 DRIFT，等 Owner/Claude 决定切片。

1. **平台管理员新建账号的租户作用域可能断链。** 弹窗接收 `tenantId`，但目录请求和创建
   请求都没有携带它（`frontend/src/features/accounts/CreateAccountModal.tsx:22-47,62-72`；
   `frontend/src/features/accounts/createApi.ts:17-84`）；后端对无固定租户的平台管理员强制要求
   正数 `tenant_id` query（`backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-345`）。
2. **账号模式服务就绪信息被前端 DTO 丢弃。** 后端返回 `serving_readiness`
   （`backend/internal/adminhttp/catalog.go:35-59`），前端 `AccountMode` 未声明该字段
   （`frontend/src/features/accounts/createTypes.ts:25-41`）。
3. **ApiError 丢上下文。** 当前只保留 status/code/message
   （`frontend/src/lib/api.ts:20-33,57-69`），无法满足 Ask Hermes 设计要求的 request ID、
   retry、details 和响应头上下文。
4. **导航文案超出真实能力。** “模块开关”“备份与恢复”对应的实现目前都是只读面
   （`frontend/src/app/nav.ts:152-156`；`backend/cmd/gateway/routes_modules.go:11-32`；
   `backend/cmd/gateway/routes_backup.go:11-28`）。
5. **正式治理规则与真实栈分叉。** `docs/RULES.md` 和 DR-004 仍锁 Next.js/Tailwind，
   且要求 OpenAPI codegen；代码已迁到 Vite/React Router但仍手写 DTO。详见 DRIFT。

## 5. 当前保留的领域文档

- `docs/14_UI_CONTRACTS.md`：通用 UI 契约，保留。
- `docs/process/plans/2026-06-19-frontend-spa-migration.md`：Owner 授权的 Vite SPA
  迁移决定；正文后半历史方案不能当当前实现，但决策本身保留。
- `docs/frontend/ASK-HERMES-DESIGN-v1.md`：Owner 明确“先深化、暂不写代码”的设计；保留为
  Mandatory Roadmap，不伪装成已实现。
- `docs/architecture/backend-feature-inventory-codex.md`：近期跨后端/前端盘点；它不是本领域
  SSOT，但其已核实的问题仍可作为审计输入。
- `docs/process/decisions/DR-004-frontend-framework.md` 与 `docs/RULES.md`：治理文件不能在本波
  擅删；其过期栈声明已在 DRIFT 标出，等待 Owner 修订。
- 2026-07-12 至 2026-07-15 的安全、UI、schema ghost、功能树及 Owner 决策文档：仍可能是
  活跃切片或 Owner-gated 输入，本波保留。

## 6. 后续更新规则

- 新增或移除真实页面、切换框架、改变鉴权方式、补齐代码生成、补 Ask Hermes 或改变只读
  能力边界时，必须同步本 SSOT。
- 历史计划不得重新成为现状依据；需要复盘时从 git history 恢复。
- 本领域被删除的散文档及逐项代码依据见
  `docs/architecture/DOC-CONSOLIDATION-DELETION-LOG.md`。
