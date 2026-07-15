# 模型主体(model registry)Admin CRUD 补口 · C③ · Claude 规划稿

日期:2026-07-15。主线 C 系列第三块(承 C①默认出口 / C②模型→账号强制pin)。性质=**激活补口**:表 0008 既有,消费端只读面既有,补运维 CRUD 写口,**不触 schema/钱/auth-core,主线自主推进**(codex 实现、Claude 亲检)。

## 0. 缺口(Explore ad3b75a 已核 file:line)
- `models` 主体正表(0008_model_registry.up.sql:54-90)**只能靠厂商同步 upsert 生成**(model_sync_writer.go:302),运维**无法手工新建/改名/改协议族/改上下文窗口/软删**一个模型主体。
- 无带**数字 DB id** 的 admin 模型清单——公开 `/v1/models` 只回字符串别名,前端 ModelRegistryPage 只能**手输数字 id** 定位模型(最大 UX 痛点)。
- 现有写口只覆盖附属属性(capabilities / capability-bindings / alias bulk-import / tenant-policy),主体本身无 CRUD。

## 1. 范围(补以下运维面,针对 `models` 表)
1. **List(admin)**:带数字 id + 全字段的分租户模型清单(消除手输 id)。
2. **Get(admin)**:按 id 取单模型主体全字段。
3. **Create**:手工登记模型主体(非厂商同步来源)。
4. **Update**:改 default_provider_model_id / default_context_window / request_timeout / pricing_class(仅 tag,不动真实定价表)/ protocol_family / model_owner。
5. **Delete/状态流转**:软删(status→deleted + deleted_at)、disable/enable(active↔disabled);schema 已预留字段。
6. **每写操作同 TX 递增 `model_registry_snapshots.version`**(0008:29-30 硬约束,目录缓存一致性命脉;照 bindings_admin.go 范式)。

## 2. 权限边界(防提权——本切片安全命脉,codex 必须焊死)
模型主体 scope 分 **global(平台级)** 与 **tenant**:
- **platform_admin**:可 CRUD global scope 及任意 tenant scope 模型(后者须显式带 tenant_id)。
- **tenant_operator**:**只能在自己 `ScopeTenantID` 内 CRUD tenant scope 模型**;对 global 模型**只读继承、禁写**;**禁 Create global**(否则=提权)。
- 强制点:handler 从鉴权上下文取身份(**非请求体**)+ `CanIssueForTenant(tenantID)`(IDOR 第一道);service 层按 `models.scope + tenant_id` 归属校验(第二道)——tenant scope 模型跨租户读/写返 403/404,写 global 非 platform_admin 返 403。

## 3. 落地(照 modelroutingadminhttp 骨架,遵 §13 包纪律)
- **新包 `backend/internal/modeladminhttp/`**(勿塞臃肿的 controlhttp/gatewayhttp):REST `GET/POST /v1/admin/models`、`GET/PATCH/DELETE /v1/admin/models/{id}`;`resolveTenant` 复用 C② 范式(身份+角色+CanIssueForTenant);`decodeJSON`(DisallowUnknownFields+MaxBytesReader)+ `parsePathID` 正整数校验;错误归一 writeServiceError。
- **registry 包新增 `models_admin.go`**:List/Get/Create/Update/SoftDelete;每写操作同 TX bump snapshot + `isUniqueViolation`(canonical_id 冲突→ErrConflict)+ scope 归属校验(照 bindings_admin.go checkModelBindable/checkPoolGroupOwned:303/321 范式)。
- **sqlc**:新 queries 手改生成码(禁 sqlc generate 全量重生,照 [[sqlc-codegen-out-of-sync]]——新参数追末位/struct 末尾加字段)。
- **前端**:ModelRegistryPage.tsx 顶部补「模型主体列表卡 + 新建/编辑/停用」,用 admin List 回填数字 id;类型/api 扩展。
- **OpenAPI**:新路由补 docs/openapi/openapi.yaml + `go test ./cmd/gateway/`(openapi_consistency)。

## 4. 判别测试(真 PG,亲手变异必红)
- CRUD 回读:Create→Get 全字段一致→Update 改子集→SoftDelete 后 List 不返(deleted 过滤)。
- **跨租户拒绝(IDOR)**:tenant scope 模型属租户 A,租户 B 读/改/删返 403/404;**变异**:去 scope 归属校验→测试转红。
- **提权拒绝**:tenant_operator 写 global 模型 / Create global 返 403;**变异**:放开 global 写守卫→转红。
- **snapshot 递增**:每写操作后 `model_registry_snapshots.version` +1;**变异**:去掉 bump→版本不变断言转红(目录一致性命脉)。
- **唯一冲突**:重复 canonical_id Create→ErrConflict。
- 接线:cmd/gateway 注入 typed-nil service→接线测试转红。

## 5. 三镜对照(§16,codex dispatch 须带)
- sub2api:`~/refs/sub2api/backend/internal/service/ops_models.go`、`ops_settings_models.go`(ops 运维侧模型管理,默认 tiebreaker)。
- new-api:`~/refs/new-api/controller/model.go`、`model_meta.go`、`model/model_extra.go`。
- CLIProxyAPI:纯 relay 无模型主体 CRUD 面→出具源码 cited no-equivalent 说明。

## 6. 执行
codex 主实现(先出实现计划核对本稿→落地),中文注释+中文报告+判别变异+禁 commit;产出 Claude 本机真 PG 亲检(含上述变异逐把证红)后提交。不触 schema/钱/auth-core=主线自主,不需 Owner 逐个点头。
