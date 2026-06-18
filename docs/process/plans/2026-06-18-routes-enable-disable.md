# Plan — routes.enabled 启用/停用 admin 写路径 (inert-gap 切片)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner 已授全权自主实现+合并)
- 基线: origin/feat/frontend-portal @ 33e36720
- 分支: feat/frontend-admin-routes-enable

## 背景 / 动机

跨切片 inert-gap 猎取(多 agent + 对抗 verify)确认: `routes.enabled` 布尔列已存在、热路径已 honor
(`internal/subscriptionenforce/routes_repo_postgres.go:46` 的 `AND r.enabled = true` 把停用路由排除出
`GroupRoutes` 仲裁), 列被扫描并出现在 DTO(`routeadmin/types.go:38`, `controlhttp/routeadmin_handler.go:73`),
**但没有任何 admin 写端点能翻转它**。CreateInput/UpdateInput 都不含 Enabled 字段, PUT 全替换刻意排除它
(`store_postgres.go:69` 注释 "enabled/created_at 不动")。当前唯一退出 live 集的方式是 SoftDelete —— 无法
**临时**停用一条路由(改配置排障/灰度回滚)而不软删它。这是一个真实运营缺口。

非 money(目录可见性/选路准入, 不碰 balances/billing/pricing/settlement), 非避让(pool_GROUP 路由目标,
非 provider/channel/proxy/pool-accounts/credentials)。无 schema 变更(列已存在) → 无 DB schema gate。

## #16 三镜像研究 (clean-room specifier lane, 已读真源)

调研「启用/停用一条 channel/route/能力」的 admin 写法:

- **new-api@1ac0f58**: 三态 status 枚举(on/手动off/自动off, `common/constants.go:254-256`); 单项翻转走整体
  update 端点带 status 字段, 分组翻转用专用 action 端点(`router/api-router.go:244-245`); **手动停用与
  失败驱动的自动停用写不同 code**(`model/channel.go:791` vs `service/channel.go:28`), 自动 re-enable 只
  撤销自动 off 不碰运营 off; 热路径经 denormalized 候选表布尔过滤 → 翻转需 fan-out 重写投影 + 重建缓存
  (`model/ability.go:36/263`)。审计每次翻转记管理审计条目; 状态写幂等(已是目标态则 no-op 不报错)。
- **sub2api@e34ad2b (默认 tiebreaker)**: account 有专用窄动作端点 `POST /accounts/:id/schedulable`
  (`backend/internal/server/routes/admin.go:319`), path-id 键 + 单字段布尔 body; 与生命周期 status 枚举
  **正交**(运营手动 schedulable 闸 vs status=error 自动 + 时间盒自动挂起); 热路径**实时查询过滤**
  (`repository/account_repo.go:926-938` 一次性 status=active AND schedulable=true), **无投影/缓存刷新**步骤,
  下次选号即生效。
- **CLIProxyAPI@2a050dc**: 专用 `PATCH /auth-files/status` 动作; disabled 布尔 + 镜像 status 枚举, 手动 off
  与 error off 分开; in-memory scheduler 即时剔除(`sdk/cliproxy/auth/scheduler.go:529-531`)。无 per-operator
  审计(单一共享管理密钥)。

### shape inventory + 取舍

完整启停 path/mode: { 手动 enable, 手动 disable, [自动 disable on health-fail], [自动 re-enable], 多态 status }。
三家共识: **启停是独立动作**(不塞 omnibus update), 且内部多用**多态 status** 区分运营 off vs 系统自动 off。

**HUAKAI 本切片决策**(sub2api 默认 tiebreaker):
- 写面用**专用单一动作端点、path-id 键、单字段布尔 body** —— `PUT /v1/admin/routes/{id}/enabled`
  body `{"enabled": bool}`。与 sub2api `POST /accounts/:id/schedulable {schedulable:bool}` 同形。
- 与 HUAKAI 现有设计一致(PUT 全替换刻意排除 enabled, 启停是独立意图, 防 read-omit-write 静默翻转)。
- 热路径 filter 实时(同 sub2api), **无需** projection/cache 刷新, 翻转下次仲裁即生效。
- **delta vs 三镜像**: 本切片只建运营手动布尔翻转(HUAKAI 目前**无** auto-disable 子系统)。三家的「多态
  status 分运营/自动 off」是为 auto-disable 共存防 auto-reenable 覆盖运营 off —— HUAKAI 暂无该需求, 布尔够用。
  - **Feature Preservation roadmap 条目**: 若将来落地健康检查 auto-disable, `routes.enabled` 须扩成小 enum
    (enabled / manual-disabled / auto-disabled) 防自动 re-enable 覆盖运营手动 off。本切片在代码注释 + 此 plan
    标注, 不删能力只标演进路径。

## 实现范围 (success criteria)

后端(routeadmin 包 + controlhttp handler, 均在预算内, 无新包):
1. `Store.SetEnabled(ctx, tenantID, id int64, enabled bool) (Route, error)` 接口 + PG 实现(UPDATE
   enabled+updated_at WHERE tenant+id+未软删 RETURNING; 0 行→ErrRouteNotFound) + Memory 实现。
2. `Service.SetEnabled(ctx, tenantID, id int64, enabled bool, adminID int64) (Route, error)`: 校验
   tenant>0/id>0 → 调 store → 审计 RouteUpdated(post-change 快照含新 enabled)。
3. handler: `setRouteEnabledRequest{ Enabled *bool }`(用 *bool 强制显式存在, 防空 body 静默停用; 复用
   match_priority 的 *T 存在性纪律); DisallowUnknownFields 拒 tenant_id 走私; tenant 取 query, id 取 path,
   adminID 取认证身份; 挂 `r.Put("/{id}/enabled", ...)`。

前端(只接线测功能):
4. `buildSetEnabledBody(enabled)` 精确 key-set `{enabled}`(routes-form.ts, 可测纯函数)。
5. `setRouteEnabled(id, tenantId, enabled)` + `enableRoute/disableRoute` 便捷封装(adminRoutes.ts, 薄 fetch)。
6. routes-form.test.ts 扩展: buildSetEnabledBody 精确 key-set + 布尔保真(变异: 漏键/多键/字符串化→红)。

强测试(变异验证): 后端 service+store(翻转/幂等/not-found/跨租户) + handler(enabled 取 body 而非走私 tenant、
缺 enabled→400、非 platform_admin→403、adminID 取认证身份、ErrRouteNotFound→404)。每条变异转红再还原。

## blast radius

- routeadmin 包(写侧, 不在热路径) + 一个新 handler + 一个新 chi 子路由。subscriptionenforce 只读热路径**不改**
  (已 honor enabled)。routes.go 主装配**不改**(MountRouteAdminRoutes 内部加子路由)。
- 风险点: chi `/{id}/enabled` 与 `/{id}` 路由不冲突(段深不同, 已核 chi 行为)。无 schema/migration。无 money。

## 可能出错 & 缓解

- 空 body 静默停用 → 用 *bool + nil→400 显式拒。
- tenant 经 body 走私 → DisallowUnknownFields(复用 routeAdminDecodeJSON)+ tenant 仅取 query。
- 启用一条 pool_group 已软删的路由 → 无害(热路径 JOIN 仍过滤掉), 故 SetEnabled 不做 pool_group EXISTS 检查(窄动作)。
- 幂等: 把 enabled 设成当前值仍 UPDATE 命中该行返回快照(200), 不报错(与 sub2api 一致)。

## 门禁

codex 401 → ultracode 多 agent 对抗审查(refute-by-default)为 #8 替代门禁; 提交门 = 无未结 S0/S1。
合并: squash 入 feat/frontend-portal, 清理 worktree/锁, ff main。本 PR 附带把 #33-39 跨切片审计的 3 条真 S2
follow-up 记入 docs/process/reviews/DEFERRED-burst-33-39.md(非 block, 仅登记)。
