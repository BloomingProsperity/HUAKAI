# Deferred follow-ups — 跨切片整合审计 (burst PR #33–39)

- 审计日期: 2026-06-18 (多 agent workflow + 对抗 verify, 12 agents)
- 基线: feat/frontend-portal @ 33e36720
- 结论: **整合健康, 零确认 S0/S1**。下列 S2 项记入 follow-up, 按 CLAUDE.md #8 **不 block** 当前/后续 commit。

## 被驳回的 "S1" (severity beats tool wording, 证据反驳)

审计 dependency-seams 维度报了一条 S1: "routes admin (#33) 未走 adminGate 注入身份, 仍依赖独立
`d.Auth.Resolve()`, 与 #39 model-admin 设计不一致, 身份来源不统一"。

**裁决: 降级/驳回, 非 S0/S1。** DI 接线证据(读真码):
- `cmd/gateway/routes.go:790` model-admin: `adminResolver = d.adminAuth` → `adminGate(adminResolver, …)`。
- `cmd/gateway/routes.go:1057` routes: `RouteAdminDeps{Auth: d.adminAuth}` → `routeAdminResolveAdmin` 调
  `d.Auth.Resolve()` = **同一个 `d.adminAuth`** resolver。
- 故审计核心论据「两条独立认证路径、Auth.Resolve 有漏洞则 routes 暴露而 model-admin 受保护」**事实错误**:
  两者调同一 resolver, 漏洞同等命中, 无差异暴露。
- routes admin 每个 handler 都先 `routeAdminResolveAdmin` (认证 + `Role != RolePlatformAdmin` 拒 + 审计 ID 取自
  `ident.TokenID` 非 body) → 完整认证+RBAC+审计安全。fail-closed nil 检查 + 集中 role gate, List/Get 也走同一
  `routeAdminResolveAdmin`。(以下行号锚定审计基线 **@33e36720** —— 之后 routes-enable 切片在 routeadmin_handler.go
  插入 SetEnabled handler 致行号下移; 论据依赖的是 routes.go 装配 `Auth: d.adminAuth`, routes.go **未被该切片改动**,
  故驳回结论不受影响。)
- 且 handler-local 自认证(`Auth: d.adminAuth`)是 admin 面**多数派**: announcements(routes.go:1052)、
  tls-fp(:1063)、api-keys(:803) 皆然; adminGate 是少数派(给 debug-vars/metrics 这种无自有 handler 的裸
  expvar/prometheus, 及 #39 为注入 context 的 model-caps)。所谓「不一致」方向反了。

→ 不开 worktree churn auth-core 去统一(零安全收益 + 高风险面)。仅留 S3 一致性观察(下)。

## S2 follow-up (登记, 不 block)

### S2-1 前端 admin client 重复代码 (DRY)
14+ 个 `admin*.ts` 模块各自定义 `adminToken/adminHeaders/parse/adminPut/adminDelete/tenantQuery`
(~450–600 行重复)。#35/#37 已显式标注 defer。
- **跟进**: 抽 `frontend/lib/api/adminClient.ts` 共享助手, 逐模块迁移。无功能回归, 纯维护负担。
- 注: 本切片 (#routes-enable) 新增的 `setRouteEnabled/enableRoute/disableRoute` 复用 adminRoutes.ts 既有
  `adminPut/tenantQuery`, 未新增重复面, 但仍属上述待抽取集合。

### S2-2 routes 与 model_admin 错误码命名不对齐
routes 出 `admin_forbidden`; adminGate 出 `admin_forbidden_scope`; 两侧前端错误 map 覆盖码集不同。
信封格式 `{error:{code,message}}` 已统一, fallback UI 可显示泛文案带 code, 无功能破。
- **跟进**: 建 `docs/admin-error-codes.md` 统一注册 + 前端 map 对齐。运维可读性改进。

### S2-4 routeadmin 生产 AuditSink 接 nil — Route 启停/增改删审计归属不持久化 (routes-enable 审查带出)
`cmd/gateway/wiring.go:1133` 用 `routeadmin.NewService(NewPostgresStore(pgPool), nil)` 注入 **nil** AuditSink;
service 层 `if s.audit != nil` 守卫 → Create/Update/Delete/**SetEnabled** 都正确把 `adminID=ident.TokenID` 传入
`audit.RouteUpdated` 等, 但生产无 sink → 无持久审计记录。**整包既有条件, 非任一切片回归**(routes-enable 仅把
启停的 adminID 正确串进既有 RouteUpdated 通道)。
- **跟进**(跨包独立切片): 接一个具体 `routeadmin.AuditSink` 实现(镜像 voucher service 在 wiring.go:1126 的接法/
  或 platformsettings.AdminAuditSink 形态)进 wiring.go:1133, 让 routeadmin 全部写操作落审计。非本切片 block(不回归既有行为)。

### S2-3 routes ↔ subscriptionenforce 缺端到端一致性测试
两侧各有单测(routeadmin service/store; subscriptionenforce arbitration), 但无 E2E 链:
Create(match_priority≠100) → GroupRoutes() → 验证收窄。
- **跟进**: 加跨切片 PG 集成测试(需 live PG infra)。两侧 schema/类型已对齐, 风险仅场景驱动(迁移/默认漂移)。

## S3 观察 (非 block, 可选)

- routes 与 model-admin 两套有效的 admin 认证注入模式(handler-local resolve vs adminGate context inject)
  并存。若未来要统一为单一边界, 方向应是把少数派 adminGate 模式作为标准(因其支持 context 注入身份), 但当前
  两套都正确且安全, 不构成缺陷。
