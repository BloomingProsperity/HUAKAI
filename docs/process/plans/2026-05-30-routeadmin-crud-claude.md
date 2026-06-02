# routes admin CRUD — 补全 S1b 可运维性(Claude 自选板块)

状态:实现中。日期 2026-05-30。Owner 授权"选一个未完成板块进行"(单次决策授权,本切片不另起双草;实现走 codex per-commit review)。协调服务已 claim:`server2-claude [routes-admin-crud]`。

## 为什么
S1b 激活了「routes 表 → 分组路由白名单强制」,但**全仓库无 routes 写入的生产代码**(仅集成测里 seed),管理员只能手塞 SQL → 分组路由不可运维。Pool Groups 早有 CRUD(F-POOL-001,handler 在冻结 `gatewayhttp`)。routes 需对等的 admin CRUD。

## 领地 / 约束
- 属我地盘:admin 配置 + router 配置,**非钱、非 `chat_completions_*` 热路径**。
- 冻结 `gatewayhttp` 禁加新文件 → handler 进**新非冻结包**(镜像 subscriptionhttp/paymenthttp)。
- 写 routes ≠ 只读 routes:与只读的 `subscriptionenforce`(热路径 gate)**按职责分包**(#13)→ 新包 `internal/routeadmin`(写)。
- 裸 pgx(镜像 subscriptionenforce + voucher store),免 sqlc 配置/drift。

## 切分(每 commit 走 codex per-commit review)
- **Commit 1(本次,休眠安全)**:`internal/routeadmin` 包 — types/store/store_postgres/store_memory/service + mid-string 通配校验 + 单元 + integration_pg 测。无 handler/wiring → 零行为变化。
- **Commit 2**:`internal/routeadminhttp` handler(MountRouteAdminRoutes)+ OpenAPI 声明(`/v1/admin/routes`)+ `cmd/gateway/routes.go` 挂载(SHARED — 单独 claim)。激活。

## Commit 1 设计
- **CreateInput**:TenantID/Name/UserGroupMatch/ModelPatternMatch/PoolGroupID + MatchPriority(*int, nil→默认100)+ AdminID(审计)。富表的 override/weight 列首版留默认(非缩水:列在且有合理默认;高级项作 follow-up 暴露)。
- **ValidateModelPattern(retro S3)**:只允许 `''`/`'*'`(全)、`'prefix*'`(单个尾通配)、纯精确串(无 `*`);**中段/多个 `*`(如 `a*b`、`*x`)拒**(`ErrInvalidModelPattern`)——因 `subscriptionenforce.ModelPatternMatches` 把它们当精确静默失配,写入时拒避免管理员困惑。语义与 ModelPatternMatches 单一来源对齐。
- **Store**:Create / List(tenant 限,enabled+未软删,按 match_priority 排)/ Get / SoftDelete(deleted_at=now)。
- **错误映射**:unique(tenant_id,name) 违反→ErrDuplicateName;FK pool_group 违反→ErrPoolGroupNotFound(无需额外查询)。
- **审计**:可 nil 的 AuditSink(RouteCreated/RouteDeleted),带 adminID。

## 判别性测试(#14)
- 单元:`TestCreate_RejectsMidStringWildcard`(seed `a*b` 期望 ErrInvalidModelPattern;**mutation: 删 ValidateModelPattern 调用 → 接受 → 红**);`TestCreate_AcceptsValidPatterns`(table `''/'*'/'claude-*'/'gpt-4o'`);`TestCreate_RejectsEmptyNameZeroPoolGroup`;`TestList_TenantScoped`(A/B 隔离);`TestSoftDelete_RemovesFromList`。
- integration_pg:create→list→get→softdelete 真 PG round-trip;tenant 隔离(A 单不入 B 列);unique-name 二次创建→ErrDuplicateName;坏 pool_group_id→ErrPoolGroupNotFound;软删后不在 List。判别:漏 tenant 谓词→串租户列出→红;软删谓词错→已删仍列出→红。

## 风险
- R1 富表多列:首版只暴露核心 + match_priority,override/weight 默认。**显式记为 follow-up,非缩水**(Feature Preservation)。
- R2 Commit 2 碰 SHARED routes.go + OpenAPI 一致性闸:接线时单独 claim + 跑全量 go test。
- R3 ModelPatternMatch 校验须与 subscriptionenforce 语义恒等:用同一规则表测两边。
