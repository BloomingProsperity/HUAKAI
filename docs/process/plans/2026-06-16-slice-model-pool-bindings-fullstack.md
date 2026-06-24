# 切片计划:model_pool_bindings 写路径 + 前端绑定页(全栈纵切,闭 #1+#3)— 2026-06-16

**PM:** Claude (Opus 4.8)。**Owner 指令:** 「开始吧,先定计划」。这是前端+后端一起闭环的第一刀(主线"补 inert gap"按全栈纵切),一次清掉跨模块接线审计的 **#1(模型映射/override 写路径)** + **#3(model→pool 绑定写 API+UI)**——两者是同一张表 `model_pool_bindings` 的同一个写缺口,合并做。

## 0. 现状(已读真码核实,fix/h-fixes + feat/frontend-portal)

- **表已存在且很全** `model_pool_bindings`(`0008_model_registry.up.sql:140-205`):tenant_id / model_id(FK models)/ pool_group_id(复合 FK pool_groups,防跨租户)/ priority / weight / selection_mode(strict_priority|priority_weighted)/ **provider_model_id_override** / rpm_limit / tpm_limit / max_parallel_requests / **fallback_class**(normal|context_window|safety|quota|manual)/ enabled / disabled_reason / effective_from / effective_until / reason / 软删。唯一索引 (tenant,model,pool)。字段已是参照对照产物(LiteLLM rpm/tpm/max_parallel + Portkey selection_mode + one-api override + envoy weight/priority + LiteLLM fallback_class,schema 注释已 cite)。
- **读路径在**:`registry.sql` 有 `ListModelPoolBindings`(:101)+ resolver 消费;**写查询零**(无 Insert/Update/Delete binding)。→ inert:能配的全配好了,就是没法配。
- **schema 硬约束**(`0008` 头注):写绑定**必须同事务 bump `model_registry_snapshots.version`**,否则路由读旧快照缓存。**当前无 bump 写查询** → 本切片要新建。
- **前端**:`app/bindings/page.tsx` 是个**没进侧栏的孤儿 pool-group 页**(管 pool group + mock `accountsForPool`),**model→pool 绑定 UI 不存在**。
- **后端家**:`controlhttp` 已是 model admin 的家(`model_admin_aliases_handler.go` / `model_admin_capabilities_handler.go`)。

## 1. 范围(Scope)

**后端:**
1. `backend/sql/queries/registry.sql` 加写查询 → sqlc 生成到 `db/registry/`:`InsertModelPoolBinding` / `UpdateModelPoolBinding` / `SoftDeleteModelPoolBinding` / `GetModelPoolBindingByID` / `BumpTenantSnapshotVersion`(若 model_sync_writer 已有等价 bump 机制则复用,不重复造)。
2. `backend/internal/registry/bindings_admin.go`(新文件,registry 包):每个写方法把 **binding mutation + snapshot.version bump 包在同一个 Tx**;写时做 **tenant 交叉校验**(binding.tenant == model.tenant,scope=tenant 模型;schema 注释要求 admin-write 时校验)。
3. `backend/internal/controlhttp/model_admin_bindings_handler.go`(新文件,controlhttp 包,对齐 `model_admin_aliases_handler.go` 范式)。端点:
   - `GET  /admin/v1/model-pool-bindings?tenant_id=&model_id=&pool_group_id=`(复用 ListModelPoolBindings)
   - `POST /admin/v1/model-pool-bindings`
   - `PATCH /admin/v1/model-pool-bindings/{id}`
   - `DELETE /admin/v1/model-pool-bindings/{id}`(软删)
   admin gate **照抄 proxyadminhttp 的双角色门**(tenant_operator 自 scope;platform_admin 必带 ?tenant_id + CanIssueForTenant)。secret-free 读 DTO。
4. `cmd/gateway/routes.go` + `wiring.go`:挂载 + 注入依赖。

**前端:**
5. `frontend/lib/api/modelBindings.ts`(新):类型逐字段对齐 DTO + list/create/update/delete。
6. `frontend/app/admin/models/bindings/page.tsx`(新页):按 model 或按 pool 列绑定 + 新建/编辑/删除弹窗,字段:model / pool_group / priority / weight / selection_mode / provider_model_id_override / fallback_class / rpm·tpm·max_parallel / enabled。
7. `Sidebar.tsx` admin nav 加「模型绑定」槽。

**测试:**
8. 后端 handler 测试:auth 门(无/错 token→401/403)、tenant 交叉校验、**snapshot.version bump 变异验证**(去掉 bump→测试转红——头号不变量)。SQL 写走 integration_pg(gated)。
9. 前端 `tsc --noEmit` 绿 + `frontend_wiring_test.go` 加断言(真后端 E2E 建/读绑定)。

## 2. 成功标准(Success Criteria)

- admin 能从 UI 建/改/删一条 model→pool 绑定,持久化成功。
- `provider_model_id_override` 可设(闭 #1 映射半边);priority/weight/fallback_class 可设(闭 #3)。
- **每次写都原子 bump snapshot.version**(变异验证过)→ resolver 读到新绑定,不读旧缓存。
- tenant 交叉校验挡住跨租户误绑。
- `go build`/`vet`/`codebudget` 绿;`tsc` 绿。

## 3. 影响面(Blast Radius)

- **无新迁移**(表+列已存在)→ 无 schema gate,低风险。
- **改路由解析的输入**:绑定喂 resolver,但**只在 operator 真写绑定时**生效;不写=零行为变化。
- **新 admin 端点**:auth 门必须正确(跨租户泄漏风险)→ 照抄 proxyadminhttp 已验证的门,不自创。
- `controlhttp` 包体积:加 1 文件,需 `codebudget` 仍绿(若 controlhttp 超预算则拆 `modelbindingadminhttp` 子包,#13)。

## 4. 会出什么错(What Could Go Wrong)

1. **忘了 bump snapshot.version**(头号坑)→ 路由读旧绑定缓存,改了不生效。**护栏:写方法 Tx 内 bump + 变异测试(删 bump 必红)。**
2. **跨租户误绑** pool_group(复合 FK 已防 DB 层,但 admin-write 还要查 model.tenant==binding.tenant)。
3. **selection_mode='priority_weighted' 暂未启用执行**(schema 注释:当前按 strict_priority 跑)→ UI 允许设但要标注"加权执行未启用",别给运维错觉。
4. controlhttp 超 codebudget → 拆子包。
5. 前端孤儿 bindings 页混淆:**新建独立 `/admin/models/bindings`,不动那个孤儿 pool 页**(它管 pool group,是另一回事)。

## 5. 决策点(待 Owner 拍)

- **D1 路由形态**:推荐**顶层资源** `/admin/v1/model-pool-bindings` + `?model_id/?pool_group_id` 过滤(对齐 proxies 的顶层 CRUD),而非嵌 `/pools/{id}/bindings`。
- **D2 handler 包**:推荐放 `controlhttp`(与 model_admin_aliases/capabilities 一致);仅当 codebudget 红才拆 `modelbindingadminhttp`。
- **D3 本切片边界**:**只做绑定写路径 + UI**;#1 的"定价倍率前端面板"(调已有 `PUT /pricing-ratio/{pool_group_id}`)是**独立的纯前端小切片,下一刀做**,不塞进本刀。
- **D4 selection_mode**:UI 是否暴露 priority_weighted?推荐**暴露但灰标"加权执行未启用,当前按优先级"**(不缩功能,如实标注)。

## 6. 时间/顺序

后端(查询+registry 写方法+handler+测试)→ 前端(client+页+nav)→ 接线测试断言 → `go build`/`vet`/`codebudget`/`tsc` 绿 → 变异验证 snapshot bump → commit+push(独立分支 `feat/slice-model-pool-bindings`,off fix/h-fixes 取后端写路径 + 前端)。**一刀做完整功能,不并行别的页。**

> 闭环账:本刀关 审计 #1 + #3 两条 high。下一刀候选:proxy 绑定写路径(#2+#4,同样全栈纵切)。
