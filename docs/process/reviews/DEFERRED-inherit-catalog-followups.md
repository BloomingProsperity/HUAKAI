# DEFERRED — inherit_global_catalog 写端点 (PR #43) 审查后续

对抗审查 (w3zkbhbo1, 6 agents) 判 `BLOCKERS_FOUND`: 1×S1 + 8×S3。本切片内**已修** S1 + 3 个可操作 S3;
其余为正向确认 / 文档化意图 / 与既有先例同档的覆盖限制, 按下记录, 不 block 合并。

## 已在切片内修复
- **S1 — 无路由级测试证明 adminGate 包裹两新端点 (#14 非判别覆盖, 载重安全声明)**: 加
  `cmd/gateway/models_route_test.go` 的 `TestAdminTenantPolicy{Get,Set}RouteMountedBehindAdminGate`,
  经真 `buildTestRouter`(nil resolver)断言 503 `admin_gate_not_configured`。gate-drop 重挂裸 handler → 红。
  亦关闭 S3 "handler 层测试绕过 gate" 的观察(裸挂回归现本地可抓, 非仅靠中心化 middleware_test)。
- **S3 — `invalid_json`(malformed body)分支无专测**: 加 `TestTenantPolicySet_RejectsMalformedJSON`,
  发 `{` 断言 400 + code=`invalid_json` + 不触达 store(区别于 `{}` 的 invalid_tenant_policy)。
- **S3 — 前端错误表缺 adminGate 两 503 码**: `TENANT_POLICY_ERROR_MESSAGES` 补
  `admin_gate_not_configured` / `admin_backend_error` 中文文案, 并入前端测试已知码列表锁定。
- **S3 — plan 的 sub2api 门禁引用不精**: 收紧到 `backend/internal/server/routes/admin.go:19-21,33`
  (v1.Group("/admin")+admin.Use(adminAuth)+AdminComplianceGuard 门 + registerGroupRoutes@33), 268-279 仅作
  group models-list config PUT 注册指针。

## 延后 (与既有先例同档, 非当前缺陷)
- **FU-1 — 集成测试验证版本递增但未验证 policy 写 + snapshot bump 的同 Tx 原子性(一起回滚)**: 当前断言版本到
  2/3/4(happy path bump 发出)但无故障注入, 把 bump 挪到 `tx.Commit` 后第二条 autocommit 语句仍会绿。先例
  `bindings_admin_integration_test.go` 共享同一空白(亦仅断言 mutation 后版本值)。属 DB 集成测试无故障注入的固有限制,
  与既有实践同档。**跟进**: 引入共享故障注入 harness(中途 abort Tx 后断言 policy 行与 snapshot bump 皆不持久),
  统一覆盖此原子性维度(连带 bindings_admin)。非本切片单独负担。
- **FU-2 — `isForeignKeyViolation`(23503)与同包 `isUniqueViolation`(23505)结构孪生但分文件**: 纯内聚/可发现性
  nit, 零正确性/安全/clean-room 影响, Go 同包跨文件无碍。**跟进**: 可将两 SQLSTATE 谓词合并入
  `registry/pgerrors.go`, 安全可延后。

## 正向确认 (审查显式核验, 非缺陷)
- snapshot 版本 bump 不变式**已被** `tenant_policy_admin_integration_test.go` 判别式覆盖(newFixture 不 seed snapshot 行,
  删 bump INSERT → 首次 readSnapshotVersion 命中 pgx.ErrNoRows → t.Fatalf 红)。
- GET 对不存在租户返 200+inherit=false 而 PUT 返 404 = 文档化意图(GET 无存在校验对齐 resolve 语义; 二者均在
  adminGate 后, 仅回显 caller 提供的 tenant_id + 常量 false, 无泄漏)。
- handler 层测试经 `tenantPolicyRouter`(无 gate)+ 直接注入身份是有意的中心化测试作用域; platform-admin-only 由
  adminGate 自有 middleware_test 覆盖 + 本切片新增路由级 mount 测试补齐"是否被 gate 包裹"。
