# Wave2 — model-sync 触发 admin 前端（计划留痕）

- 日期：2026-06-17
- 切片：Slice 5（Wave2 admin 后台补全）
- 分支：`feat/admin-model-sync`（base `origin/feat/frontend-portal` @ f13296b0）
- 协调锁：`claude-modelsync`

## 范围（Scope）

把已就绪的后端「全局模型目录同步」触发端点接到 admin 前端（前端零覆盖）。纯前端接线测功能，不追设计，**不动 Sidebar.tsx**（避让并行 proxies 分支；导航入口让其收口，本页先 URL 直达 `/admin/model-sync`）。

## 后端权威契约（读真码，禁止凭记忆）

- `backend/internal/adminhttp/model_sync_handler.go` + `backend/cmd/gateway/routes.go:975-980`
- 端点：`POST /admin/v1/model-sync`（`r.Route("/admin/v1/model-sync")` + `r.Post("/")`）
- 鉴权：platform_admin only（`ident.Role != admin.RolePlatformAdmin → forbidden`）；**无 tenant_id**（全局目录，影响所有继承全局目录的租户）
- 请求体：`{reason?}`，trim 后 `utf8.RuneCountInString > 200 → 400 invalid_reason`；空 → 后端自填 `admin_manual`
- 响应 200：`{object:"admin_model_sync_result", completed_at(RFC3339), total_added, total_updated, total_disabled, results:[{vendor, added, updated, reactivated, disabled, unchanged, snapshot_bumps}]}`
- 错误：503 gateway_not_configured / model_sync_failed、400 invalid_json / invalid_reason、401/403
- **无**同步历史 / 状态 GET、**无**调度端点 → Feature-Preservation roadmap（页内 Info 提示，不伪造）

## 借鉴对照（CLEAN-ROOM §11/§12/§16，仅功能形态，未抄码；源经 reviewer-lane 核实，file:line 如下）

| 维度 | new-api@1ac0f58(AGPL) | sub2api@e34ad2b(LGPL) | CLIProxyAPI@2a050dc(2026-06-14) | HUAKAI delta · 维度 |
|---|---|---|---|---|
| 上游模型同步触发 | **有**跨渠道聚合 apply-all 管理端点（遍历全部启用渠道，返回聚合 added/removed + 逐渠道明细）`controller/channel_upstream_update.go:850-932`、路由 `router/api-router.go:263,265` 受 `AdminAuth()`；**有**默认开启定时 ticker `:652-680`；但逐渠道 Models CSV 仅 add/remove `:808`，无 reactivate/disable/snapshot、非单一全局目录 | **有**按账号实时拉取上游可用模型端点 `admin.go:320,322,615`，返回扁平 `{models:[]}` `account_handler.go:2146,2196` / `upstream_models.go:76,127`，**无** add/update/disable/reactivate 差量结算 | **无**管理员触发的上游账号目录拉取（仅 GET 读模型 `internal/api/server.go:723-724`）；有内部 config/CDN 模型刷新+hash-diff 回环 `sdk/cliproxy/service.go:374-423` / `internal/registry/model_updater.go:77-138`（拉 maintainer 静态 models.json + 本地 config 热载差异，非账号上游发现） | 单 platform_admin **全局目录**一次触发 + 逐厂商【新增/更新/复活/停用/未变/快照递增】差量结算 + reason 审计 actor（`backend/internal/adminhttp/model_sync_handler.go:35-52,106-131`；`modelsync/service.go:62-129`）· **生态升级**：动词更全（reactivate/disable/snapshot）+ 收敛为单一全局目录差量结算 |

注：`modelsync/service.go:127` 的 `TotalAdded += Added + Reactivated` → 顶部 `total_added` 含复活，前端用「新增/复活」标签与逐厂商两列对账。

## 文件（每个文件标注落点）

1. `frontend/lib/api/model-sync-form.ts`（新，零依赖纯逻辑）：`MAX_SYNC_REASON_LEN`、`validateModelSyncReason`（trim + 码点计长 ≤200）、`buildModelSyncBody`（空省略键）、`vendorChangeCount`、`syncHadChanges` + 结果形状类型。
2. `frontend/lib/api/adminModelSync.ts`（新，客户端）：`ModelSyncResult`/`Item` 类型 + `triggerModelSync`（复用 client.ts `apiPost`，无 PUT/DELETE/tenantQuery）。
3. `frontend/app/admin/model-sync/page.tsx`（新）：reason 输入 + 码点计数器 + 触发按钮（loading）+ 本次结果表（合计徽章 + 逐厂商行）+ 友好错误 + roadmap 提示。
4. `frontend/lib/api/model-sync-form.test.ts`（新）：纯逻辑单测 + 接线源文断言，全部变异验证。
5. `frontend/package.json`：加 `test:model-sync` 脚本。

## 成功标准

- tsc exit 0；`test:model-sync` 全绿；邻测不破。
- 测试判别性经变异实测：边界 `>200`、码点 vs UTF-16、trim、空省略键、变更数排除 unchanged/snapshot、syncHadChanges 三项、端点路径/动词/builder/无 tenant_id —— 每个变异转红、还原绿。
- 对抗审查无未结 S0/S1。

## 爆炸半径 / 风险

- 纯前端新增文件，不改后端、不改共享文件、不动 Sidebar.tsx → 撞车与回归面极小。
- 触发会真同步全局模型目录（写动作），但仅 platform_admin、由后端鉴权强制；前端只是触发入口。

## 已知缺口（登记 roadmap，不伪造）

- 后端无同步历史 / 状态查询 GET → 页面仅展示「本次」结果。
- 后端无定时 / 自动调度端点 → 仅手动触发。
