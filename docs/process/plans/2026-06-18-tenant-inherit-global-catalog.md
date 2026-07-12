# Plan — 租户全局目录继承(inherit_global_catalog)写端点 (inert-gap 切片)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner 全权自主实现+合并)
- 基线: origin/feat/frontend-portal @ b64eba13
- 分支: feat/frontend-admin-inherit-catalog

## 背景 + 真 inert gap 核实 (禁止凭记忆)

`GET/PUT /v1/admin/model-registry-policy?tenant_id` —— 给 model_registry_tenant_policies.inherit_global_catalog
补 admin 写路径。确认真 inert gap (非死开关), 三要素齐全:
- **存储✓**: model_registry_tenant_policies(tenant_id PK→tenants, inherit_global_catalog bool, updated_at, updated_by_actor)。
- **消费者✓ (真行为效果)**: resolve 时 GetTenantInheritGlobal (postgres_registry.go:276, tenant 别名 miss → 若 inherit
  回落 scope=global 别名) + /v1/models discovery live JOIN (models_list.go:71-73, AND inherit_global_catalog=true 决定
  租户看见哪些 global 模型)。改它直接改租户可见目录。
- **缺口**: 仅测试 INSERT 写过该表, 无任何 admin 写端点; GetTenantInheritGlobal 只读。

**纠正先前"需物化 snapshot 重同步"的担忧**(禁止凭记忆): model_registry_snapshots 是 per-tenant 单调**版本计数器**
(client ETag, resolve 读入 SnapshotVersion=registry:<tid>:<v>), 非物化目录; discovery 是 live JOIN。故写 = upsert
policy 行 + 单租户 snapshot version+1, 一个 Tx, 无 fan-out 无物化, blast 有界。

非 money(目录可见性, 三镜像独立确认不碰计价/扣费), 非避让。

## #16 三镜像研究 (clean-room specifier lane)

「租户/分组是否继承全局模型目录」开关:
- **sub2api@e34ad2b (默认 tiebreaker, 三家唯一有真继承/override 开关 = 直接先例)**: per-group `{enabled, models[]}`
  config(domain/models_list_config.go:4-7, group 字段 ent/schema/group.go:159-162), 默认 off=继承; 整体 group 对象 PUT
  带该字段(handler/admin/group_handler.go:320-374); **platform-admin only 无租户自管**(分组路由经
  backend/internal/server/routes/admin.go:19-21 的 v1.Group("/admin")+admin.Use(adminAuth)+AdminComplianceGuard 门
  + registerGroupRoutes(admin,h)@33 挂入; group models-list config PUT 注册@268-279, 非租户自服务路径);
  query-time 过滤 + 写后失效 per-key auth 快照(因它缓存 policy 进快照); 非 money(grep 证实不在 pricing/billing service)。
- **new-api@1ac0f58**: 模式(b) 由 channel→group 绑定派生可见性(model/ability.go), 无继承开关; token 级 allow-list(deny-by-default);
  in-mem cache 重建; 非 money(定价在独立 ratio 轴)。
- **CLIProxyAPI@2a050dc**: 扁平单目录 relay, 无每租户目录隔离 = **no equivalent**(只有上游凭据级 excluded-models)。

### 取舍 (sub2api 默认 tiebreaker — 它是唯一真先例)
- **门 = platform-admin only**(经 adminGate, 非 modelbindingadminhttp 双角色门): 租户自行授予全局目录继承 = 提权
  (自己给自己开通所有 global 模型), 该决策属平台; 且 sub2api 先例正是 platform-admin only。tenant_id 是【目标租户】(admin 供 query)。
- **写面 = 字段挂整体 tenant-policy PUT**(mirror sub2api "整体对象带字段")。HUAKAI 的 policy 对象当前只 1 字段
  (inherit_global_catalog), 故 body{inherit_global_catalog}; *bool 必填(防省略静默改不继承)。
- **保留 snapshot version bump**(单租户): model_registry_snapshots 表注释明令"凡改租户可见目录的 admin 写必须同 Tx version+1",
  mirror model-pool-bindings(bindings_admin.go)这个 registry-写 house 模式。HUAKAI 有 client ETag, 改目录须失效它(比
  capability-binding 的 no-bump 更对 —— 那是 FU-1 跟进)。
- delta vs 镜像: HUAKAI 已有读侧(resolve 回落 + discovery live JOIN), 唯一缺写路径 = 本切片。**架构升级**: 独立
  per-tenant policy 表 + 单租户 snapshot 版本计数器(client ETag), 写时同 Tx bump。

## 实现范围 (success criteria)
- 后端 store internal/registry/tenant_policy_admin.go(新): TenantPolicy 类型 + GetTenantPolicy(无行→默认 inherit=false 不报错,
  对齐 resolve 语义) + SetTenantInheritGlobal(Tx Serializable: upsert policy ON CONFLICT + 单租户 snapshot version bump,
  Commit; FK 违反→ErrUnknownTenant 映射 4xx 非 503) + ErrUnknownTenant 哨兵。
- 后端 handler internal/controlhttp/model_registry_policy_handler.go(新): GET+SET; tenant 取 query(routeAdminParsePositiveQuery
  复用); inherit *bool 必填 nil→400; actor 取 admin.IdentityFromContext(adminGate 注入); DisallowUnknownFields;
  ErrUnknownTenant→404。routes.go: GET+PUT /v1/admin/model-registry-policy 经 adminGate(platform-admin), Store: d.modelRegistry。
- 前端 model-registry-policy-form.ts(validate/build 无 tenant_id 防走私/inherit 永远带 + 错误码映射) +
  adminModelRegistryPolicy.ts(getTenantPolicy apiGet/setTenantInheritGlobal adminPut, tenant 走 query) + test + package.json 脚本。

强测试(变异验证): 后端 handler(tenant 取 query/inherit 取 body/actor 取身份/inherit 必填/DisallowUnknownFields/ErrUnknownTenant→404)
+ store 集成(integration_pg: set+bump 快照版本+读回+翻转+幂等+FK→ErrUnknownTenant) + 前端(validate/builder no-tenant_id/接线)。

## blast radius
- 新 store 文件 + 新 handler 文件 + routes.go 2 行 + 前端 3 文件。resolve/discovery 读侧**不改**(已 live 消费)。
  schema **不改**(列已存在)。写仅触 1 policy 行 + 1 snapshot 行(目标租户), 无 fan-out。无 money 无避让。

## 门禁
codex 401 → ultracode 对抗审查(#8 替代门禁) 零 S0/S1 → squash 合并 → ff main。
