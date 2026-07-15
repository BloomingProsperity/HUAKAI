# HUAKAI 分销商 Phase 1 独立实现计划（Codex）

日期：2026-07-15
状态：独立规划稿，待 Owner 与 Claude 交叉裁定后实施
交付边界：本稿只规划，不改生产代码；实施阶段禁止把 Phase 2、Phase 3 工作偷渡进来。

## 1. 结论先行

Phase 1 应被拆成“先建不可误用的数据底座，再收紧身份，再开放超管写口”三层：先用下一未占用迁移号 0185 增加租户父指针、四个模式列、批发倍率和专属账号分配关系；随后把 AdminIdentity 的租户判定统一为 CanActOnTenant，并先消除 session 管理员无条件变成平台超管的路径；最后才挂载超管创建、停用、调倍率、调模式和分账号的写接口。权威底稿把 Phase 1 锁定为子租户、scope 和超管建站三项，并把两层扣费与分销商自助 UI 放到 Phase 2、白牌域名放到 Phase 3，因此本稿不触碰计费结算、零售价、Host 路由或分销商业务 UI。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:15-19、docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-27、docs/process/plans/2026-07-15-reseller-arc-final-model.md:35-38。

安全顺序不能倒置。当前 token 身份已有 platform_admin 与 tenant_operator 的 scope 雏形，但 CanIssueForTenant 只支持“平台全通或精确租户”，而 session 分支把任意 role=admin 的用户直接映射为平台超管、ScopeTenantID=0；如果先挂写口，子租户管理员会继承全租户权限。证据：backend/sql/migrations/0010_admin_auth.up.sql:28-48、backend/internal/admin/operator_auth.go:34-47、backend/internal/admin/operator_auth.go:140-158、backend/internal/adminsessionauth/resolver.go:71-91。

迁移必须对既有平台租户零回归：既有行的 parent_tenant_id 与 wholesale_multiplier 均保持 NULL，四个模式列使用平台现状默认值；现有大量 INSERT INTO tenants (name) 依赖省略新增列，故新增列必须有可接受的 NULL/DEFAULT，不能要求一次性回填子租户语义。证据：backend/sql/migrations/0001_pool_routing.up.sql:15-24、backend/cmd/mvp-seed/main.go:58、backend/internal/setuphttp/setuphttp_integration_test.go:40、backend/internal/tenancy/bootstrap.go:114-121。

本稿建议 Phase 1 的“分账号”只交付控制面配置、约束、审计和写口，不在本期改运行时 selector。原因是本轮任务明确限定为 admin 写口，而权威底稿一处把“分账号”列在 Phase 1，另一处又把 dedicated 账号落在 Phase 3；该矛盾必须由 Owner 裁定，不能由实现者暗自扩大路由热路径。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:25、docs/process/plans/2026-07-15-reseller-arc-final-model.md:36-38。

## 2. 锁定范围与明确非目标

### 2.1 本期必须交付

1. tenants 增加 parent_tenant_id、upstream_mode、domain_mode、announcement_mode、transparency_mode、wholesale_multiplier，并为父子关系和专属账号分配建立数据库约束。当前 tenants 只有 id、name、status、时间戳与软删列，名称在未软删行上全局唯一。证据：backend/sql/migrations/0001_pool_routing.up.sql:15-24。
2. 将 CanIssueForTenant(tenantID) 删除并替换为 CanActOnTenant(tenantID)，语义为：真正的平台级身份全通；其他已认证身份只可操作 ScopeTenantID 本身及其后代。当前方法只允许 tenant_operator 精确命中，不能表达子树。证据：backend/internal/admin/operator_auth.go:140-158、docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-23。
3. 收口 session 身份：根租户 admin session 才映射为平台级；子租户 admin session 映射为 tenant_operator，ScopeTenantID=会话租户，并携带可信子树集合。当前逻辑在验证 role=admin 后无条件返回 platform_admin。证据：backend/internal/adminsessionauth/resolver.go:67-91。
4. 新增仅超管可写的分销商管理后端：创建子租户、停用/恢复、设置批发倍率、设置模式、配置共享池/专属账号；补路由接线、OpenAPI、审计和真 PostgreSQL 测试。现有管理路由集中在 gateway 的 admin 区域，quota 包已示范独立包挂载方式。证据：backend/cmd/gateway/routes.go:1033-1064、backend/internal/adminquotahttp/routes.go:52-73。
5. 保留 users.role 的 admin/user 二值 CHECK，能力判断叠加在该角色之上，绝不扩成第三角色或自定义权限引擎。证据：backend/sql/migrations/0076_user_role.up.sql:1-14、docs/process/plans/2026-07-15-reseller-arc-final-model.md:8-11、docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-24。

### 2.2 本期明确不做

- 不改 hold、settle、settlement_intents、余额或账本，不做两层扣费、批发余额、零售倍率、下级余额不足联停；这些被锁在 Phase 2。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:13-19、docs/process/plans/2026-07-15-reseller-arc-final-model.md:37。
- 不做分销商固定自助 UI、下级用户/key/额度管理、透明数据页或真实数据脱敏展示；这些被锁在 Phase 2。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:23-26、docs/process/plans/2026-07-15-reseller-arc-final-model.md:37。
- 不做自定义域名、TLS、Host→tenant 路由、白牌公告运行时或 isolated 透明运行时；这些被锁在 Phase 3。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:27、docs/process/plans/2026-07-15-reseller-arc-final-model.md:38。
- 不开放分销商自行创建孙代理。数据结构与 CanActOnTenant 必须支持后代，但 Phase 1 的建站写口仍要求超管；这保留未来多级而不提前开放产品能力。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-24、docs/process/plans/2026-07-15-reseller-arc-final-model.md:36。
- 不让 wholesale_multiplier 进入任何价格计算。现有 ratio 使用 numeric(20,8) 且 CHECK > 0，可复用精度与传输约定，但本期只存配置。证据：backend/sql/migrations/0078_pool_group_pricing_ratios.up.sql:3-25、docs/process/plans/2026-07-15-reseller-arc-final-model.md:18、docs/process/plans/2026-07-15-reseller-arc-final-model.md:37。

## 3. 目标不变量

1. 平台级身份定义为 RolePlatformAdmin 且 ScopeTenantID=0；仅有 platform_admin 字符串但携带正 scope 的畸形身份不得被当成全局身份，而应按有限 scope 处理。当前数据库约束正常情况下不产生这种组合，但防御性定义能让 handler 的 role gate 与 scope gate 各自可被判别测试击穿。证据：backend/sql/migrations/0010_admin_auth.up.sql:33-48、backend/internal/admin/operator_auth.go:34-47。
2. 非平台身份必须有正 ScopeTenantID，并且 CanActOnTenant 只读取 resolver 装载的可信子树，不读取 URL/body 中自称的 parent 或 ancestry。权威底稿要求 handler 级显式 scope，不能把客户端租户字段当授权依据。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:29-32、backend/internal/adminquotahttp/routes.go:90-153。
3. parent_tenant_id 写入后不可变；新父行必须已存在，配合自引用外键即可从数据库层阻止自环和后续换父形成环。Phase 1 不提供 reparent API。当前租户主键与所有业务表的 tenant_id 是隔离基础，随意换父会改变整棵授权边界。证据：backend/sql/migrations/0001_pool_routing.up.sql:12-24、backend/sql/migrations/0041_tenant_composite_foreign_keys.up.sql:1-9。
4. 根租户保持平台形态；子租户必须有正批发倍率。倍率使用 decimal string 入出站和 numeric(20,8) 存储，禁止 float64。现有价格倍率已采用 numeric(20,8) 与大于零约束。证据：backend/sql/migrations/0078_pool_group_pricing_ratios.up.sql:3-18。
5. 停用子租户后，其用户 API key 已会被现有 inbound resolver 因 tenant.status 非 active 而拒绝；新 status 写口只允许 active 与 suspended，不扩充现有 tenants.status 的全表 CHECK。证据：backend/sql/queries/auth_inbound.sql:11-41、backend/internal/auth/api_key_resolver.go:142-164、backend/sql/migrations/0001_pool_routing.up.sql:15-23。
6. 子租户永远不能读取、创建、轮换、停用、删除或借测试接口使用上游凭证，也不能修改 provider account。现有 provider account 创建请求可携带 credentials，且现有 tenant 范围 resolver 允许 tenant_operator；这一面必须与 session scope 收口同片加固。证据：backend/internal/gatewayhttp/poolaccountadmin/contract.go:130-150、backend/internal/gatewayhttp/admin_pool_accounts_handler.go:67-153、backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-349。
7. 所有分销商配置写入与 admin_audit_events 同事务提交；现有 quota store 已有 pgx.BeginFunc 内同时改业务表与写审计的范式。证据：backend/internal/adminquotahttp/store.go:82-170。

## 4. 数据库与迁移设计

### 4.1 迁移号与文件

建议使用：

- backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql
- backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.down.sql

当前磁盘最高已发号为 0184；迁移目录要求 append-only、不得复用已发编号，所以实施开工前必须再次扫描并在冲突时顺延，不可硬抢 0185。证据：backend/sql/migrations/0184_burst_value_comment.up.sql:1、backend/sql/migrations/README.md:1-13。

迁移会自动被 go:embed 收入二进制，无需改 embed.go；该文件已匹配 migrations/*.sql。证据：backend/sql/embed.go:1-13。

### 4.2 tenants 建议列

| 列 | 类型与默认 | 约束 | Phase 1 语义 |
|---|---|---|---|
| parent_tenant_id | bigint NULL | REFERENCES tenants(id) ON DELETE RESTRICT；CHECK 为 NULL 或不等于 id | NULL=根；非 NULL=子租户 |
| upstream_mode | text NOT NULL DEFAULT 'shared_pool' | CHECK IN ('shared_pool','dedicated_accounts') | 控制面目标模式 |
| domain_mode | text NOT NULL DEFAULT 'platform_domain' | CHECK IN ('platform_domain','custom_domain') | 本期只存，不启用 Host 路由 |
| announcement_mode | text NOT NULL DEFAULT 'platform' | CHECK IN ('platform','tenant') | 本期只存，不启用独立公告 |
| transparency_mode | text NOT NULL DEFAULT 'masked' | CHECK IN ('masked','isolated') | 本期只存，不开放透明页 |
| wholesale_multiplier | numeric(20,8) NULL | 根必须 NULL；子必须非 NULL 且 > 0 | 本期只存，不参与扣费 |

四个默认值刻画当前平台形态：共享平台池、平台域名、平台公告、脱敏可见性；已有租户迁移后仍是根租户且行为不变。现有 tenants 没有这些列，provider account 默认由租户拥有并参加池选择。证据：backend/sql/migrations/0001_pool_routing.up.sql:15-24、backend/sql/migrations/0001_pool_routing.up.sql:108-164。

建议增加：

- idx_tenants_parent_active：以 parent_tenant_id、id 为键，过滤 deleted_at IS NULL，服务直接子级与递归 CTE。现有租户名称只在未软删行上唯一，可沿用该风格。证据：backend/sql/migrations/0001_pool_routing.up.sql:23。
- parent_tenant_id 不可变触发器：INSERT 时父必须已存在；UPDATE 只要 OLD.parent_tenant_id IS DISTINCT FROM NEW.parent_tenant_id 就拒绝。外键加写入后不可变，使 Phase 1 无需复杂循环检测也不会形成环。证据：backend/sql/migrations/0001_pool_routing.up.sql:15-24。
- 租户形态 CHECK：parent_tenant_id IS NULL 与 wholesale_multiplier IS NULL 同时成立，或者 parent_tenant_id IS NOT NULL、wholesale_multiplier IS NOT NULL 且大于零同时成立。该 CHECK 让实现者不能绕过 handler 建“无批发底价”的分销商。权威底稿把批发倍率定义为 Owner 专属地板价。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:15-16。

不建议给 tenants.status 新增全表 CHECK。当前列没有 CHECK，而现有授权热路径只认 active；贸然收紧历史值会扩大迁移爆炸半径。新 handler 自己只接受 active/suspended，数据库查询仍把非 active 视为停用。证据：backend/sql/migrations/0001_pool_routing.up.sql:15-23、backend/internal/auth/api_key_resolver.go:142-164。

### 4.3 专属账号关系表

建议新建 tenant_provider_account_allocations，而不是改写 provider_accounts.tenant_id 或复制 credentials：

| 列 | 建议 |
|---|---|
| consumer_tenant_id | bigint NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT |
| owner_tenant_id | bigint NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT |
| provider_account_id | bigint NOT NULL |
| assigned_by_actor | text NOT NULL |
| created_at / updated_at | timestamptz NOT NULL DEFAULT now() |

约束建议：

1. PRIMARY KEY (consumer_tenant_id, provider_account_id)。
2. UNIQUE (provider_account_id)，保证一个专属账号不会同时划给两个分销商。
3. FOREIGN KEY (owner_tenant_id, provider_account_id) REFERENCES provider_accounts(tenant_id, id) ON DELETE RESTRICT，复用既有复合隔离方向，禁止把账号 ID 与错误 owner 拼接。0041 已用同类复合外键保护凭证到账户的租户一致性。证据：backend/sql/migrations/0041_tenant_composite_foreign_keys.up.sql:13-16、backend/sql/migrations/0041_tenant_composite_foreign_keys.up.sql:39-44。
4. CHECK (consumer_tenant_id <> owner_tenant_id)。
5. 约束触发器或同事务校验 owner_tenant_id 是 consumer_tenant_id 的祖先，且 consumer 是非根、账号未软删；parent 不可变后祖先关系不会在校验后漂移。provider_accounts 当前带 tenant_id、enabled、deleted_at 与凭证材料，保留原 owner 可避免破坏现有路由和复合外键。证据：backend/sql/migrations/0001_pool_routing.up.sql:108-164。

跨表不变量由一条事务服务维护：shared_pool 必须清空分配行；dedicated_accounts 必须至少有一条分配行。PostgreSQL CHECK 不应查询另一张表，所以不要伪造跨表 CHECK；store 在 SELECT ... FOR UPDATE 锁住 consumer tenant 后替换分配、更新 upstream_mode、写审计，并用真 PG 并发测试守住。现有 provider account 的并发与租户复合键已依赖行级事务语义。证据：backend/sql/migrations/0001_pool_routing.up.sql:127-165、backend/internal/adminquotahttp/store.go:82-170。

### 4.4 审计白名单

0185 同步扩展 admin_audit_events_action_check，建议动作：

- create_reseller_tenant
- set_reseller_status
- set_reseller_wholesale_multiplier
- set_reseller_modes
- set_reseller_upstream_allocation

target_type 沿用已有 tenant 与 provider_account，不新增类型。当前审计表对 action 和 target_type 都是显式 CHECK，tenant/provider_account 已在白名单。证据：backend/sql/migrations/0181_admin_audit_runtime_logs_cleanup.up.sql:5-35。

审计 payload 只放 tenant_id、父 tenant_id、模式、倍率变更前后值、账号 ID 列表和结果，不放密码、admin token、credentials、完整上游返回或连接串。provider account 当前把 credentials 标记为敏感材料。证据：backend/sql/migrations/0001_pool_routing.up.sql:123-126、backend/internal/gatewayhttp/admin_pool_accounts_handler.go:116-188。

### 4.5 零回归与回滚

迁移验收必须证明：

- 迁移前的所有 tenant 行在迁移后 parent_tenant_id=NULL、wholesale_multiplier=NULL，四模式为平台默认。
- INSERT INTO tenants (name) 与 INSERT INTO tenants (id,name,status,created_at,updated_at) 仍成功；仓库的 seed、smoke 与大量真 PG 测试都依赖这些列清单。证据：backend/cmd/mvp-seed/main.go:58、backend/cmd/smoke-setup/main.go:181、backend/cmd/gateway/smoke_test.go:135。
- 根行带倍率、子行无倍率、非法模式、自父、缺失父、reparent、重复专属账号、非祖先 owner 全部失败。
- down 只用于开发/测试：先删 allocation 表、触发器/索引/约束，再删 tenants 新列并恢复审计 CHECK。生产回滚采用旧二进制忽略新列或前滚修复，不在已有子租户数据上直接 down。Makefile 也把 migrate-down 标成 dev/test only。证据：backend/Makefile:45-49。

## 5. scope 与身份收口设计

### 5.1 AdminIdentity 的可信作用域

在 backend/internal/admin/operator_auth.go 中：

1. 删除 CanIssueForTenant，新增 CanActOnTenant(tenantID int64) error；不要保留兼容别名。删除旧方法能让编译器强迫所有调用点迁移，避免某些 handler 继续只做精确匹配。当前方法定义和语义集中在该文件。证据：backend/internal/admin/operator_auth.go:140-158。
2. 增加 IsPlatformWide()：只有 RolePlatformAdmin 且 ScopeTenantID==0 返回 true。未知角色返回 ErrAdminUnauthorized；正 scope 但子树数据缺失时只允许 ScopeTenantID 自身，绝不 fail-open。
3. AdminIdentity 内增加不可由 handler/body 赋值的私有 scope 快照，至少含根 scope、后代 ID 集合和 scope 根是否为子租户。adminsessionauth 通过 admin 包导出的受控构造函数取得身份，避免公开可任意填充的 AllowedTenantIDs。
4. CanActOnTenant 判定顺序：tenantID<=0 拒绝；平台级放行；非平台要求 ScopeTenantID>0；目标等于 scope 放行；目标存在于可信后代集合放行；其他一律 ErrAdminForbidden。

CanActOnTenant 没有 context 参数，故不能在方法内部临时查库；子树必须由 resolver 在认证成功后一次装载。当前 AdminIdentity 是轻量值对象，CanIssueForTenant 也是无 I/O 方法。证据：backend/internal/admin/operator_auth.go:34-47、backend/internal/admin/operator_auth.go:140-158。

### 5.2 子树查询

在 backend/sql/queries/admin_resellers.sql 增加固定递归 CTE：

- 输入 root_tenant_id 为 bigint 参数。
- anchor 精确取 id=$1、deleted_at IS NULL、status='active'。
- recursive 只沿 tenants.parent_tenant_id 向下，过滤 deleted_at IS NULL；scope 授权建议同时要求后代本身 active。
- 返回 root 与所有后代 ID，使用 UNION 而不是拼接客户端 ID 列表；parent 不可变后结果稳定。

查询必须加入 backend/sqlc.yaml 的 admin queries 列表并由 sqlc generate 生成，不能手写或手改 generated 文件。当前 admin sqlc 配置只列出显式 query 文件，输出到 internal/db/admin。证据：backend/sqlc.yaml:3-25、backend/Makefile:42-43。

token resolver 在 bcrypt 验证成功后才查子树：platform token 直接返回全局身份；tenant_operator token 先依现有 SQL 确认 scoped tenant active，再装载子树。现有查询已把 scope tenant 被停用/软删的 token 过滤掉，应保留该性质。证据：backend/sql/queries/admin_tokens.sql:7-38、backend/internal/admin/operator_auth.go:95-120。

### 5.3 session resolver 收口

在 backend/internal/adminsessionauth 中把 RoleStore.ActiveUserRole 替换为更窄的 AdminIdentityStore.ResolveActiveAdminIdentity(tenantID,userID)，其 PostgreSQL 实现必须在同一次参数化读取中确认：

- users.id 与 users.tenant_id 精确同时命中；
- users.deleted_at IS NULL、users.status='active'、users.role='admin'；
- tenants.deleted_at IS NULL、tenants.status='active'；
- 根 tenant 的 admin 返回 RolePlatformAdmin、ScopeTenantID=0；
- 子 tenant 的 admin 返回 RoleTenantOperator、ScopeTenantID=validated.TenantID，并装载该 tenant 的 active 子树。

当前 RoleStore 只查 users 的精确 tenant/user 与 active 状态，未读取 tenants.parent_tenant_id；当前 resolver 在此之后无条件返回平台身份。证据：backend/internal/panelauth/store_postgres.go:23-61、backend/internal/adminsessionauth/resolver.go:27-43、backend/internal/adminsessionauth/resolver.go:67-91。

新实现建议放在 backend/internal/adminsessionauth/store_postgres.go，不修改 panelauth 对用户面板的通用角色语义；gateway wiring 把现有 panelauth role store 替换为该专用 store。当前接线点在 backend/cmd/gateway/wiring.go。证据：backend/cmd/gateway/wiring.go:1491-1496。

现有根租户 admin session 仍是平台级，保持零回归；只有 parent_tenant_id 非 NULL 的新子租户 admin 被收窄。现有测试明确断言所有 admin session 都是平台级，必须改成根/子两类而不是简单删除断言。证据：backend/internal/adminsessionauth/resolver_test.go:84-109、backend/internal/adminsessionauth/resolver_integration_pg_test.go:81-124。

写方法仍沿用“未标注即 token-only”的 fail-closed 机制。新分销商写口默认不挂 SessionSafe；若 Phase 1 尾部 UI 必须用登录 session 写，须单独 Owner-gated 决定哪些端点挂 SessionSafe，批发倍率与专属账号分配建议继续 token-only。证据：backend/internal/adminsessionauth/writeclass.go:8-21、frontend/src/auth/tokenForPath.ts:41-50。

### 5.4 全调用面迁移

删除旧方法后，至少下列现有生产调用点必须改为 CanActOnTenant；每个 handler 仍保留显式调用，不把 scope 完全藏进 store：

- backend/internal/adminquotahttp/routes.go:148
- backend/internal/moderationhttp/helpers.go:35
- backend/internal/controlhttp/dispute_handler.go:196、299
- backend/internal/modelbindingadminhttp/routes.go:367
- backend/internal/modelroutingadminhttp/routes.go:208
- backend/internal/accountfphttp/fingerprint_handler.go:149
- backend/internal/riskoverviewhttp/handler.go:108
- backend/internal/usernoticehttp/handlers.go:252
- backend/internal/announcementhttp/handlers.go:373
- backend/internal/pricingcataloghttp/admin_helpers.go:68
- backend/internal/adminhttp/provider_account_tenant_resolve.go:34
- backend/internal/adminhttp/provider_catalog_handler.go:184
- backend/internal/adminhttp/api_keys_handler.go:233
- backend/internal/alertinghttp/helpers.go:61
- backend/internal/gatewayhttp/poolaccountadmin/contract.go:342
- backend/internal/proxyadminhttp/routes.go:349
- backend/internal/admin/revoker.go:54
- backend/internal/proxyadminhttp/tenant_default_routes.go:98
- backend/internal/admin/issuer.go:101
- backend/internal/adminuserhttp/tenant_scope.go:37
- backend/internal/orphanreconcilehttp/reconcile.go:113
- backend/internal/hermeshttp/admin_auth.go:82

其中任何以“tenant_operator 直接取 ScopeTenantID”绕过目标判定的 helper 都要改成：先解析目标 tenant，再显式 CanActOnTenant；当前 provider account resolver 就有直接返回 scope 的分支和平台分支。证据：backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-349。

### 5.5 创建首个子租户管理员

Phase 1 要让子租户 admin session 可被验证，就需要安全地创建首个 users.role='admin' 用户。现有通用 admin 用户创建接口刻意只接受 role=user，不能为本需求放宽；其拒绝测试也守住这一点。证据：backend/internal/adminuserhttp/user_create.go:55-105、backend/internal/adminuserhttp/user_crud_test.go:100-113。

建议 POST /admin/v1/resellers 在同一事务内完成 tenant、初始 admin user 和审计：

- 请求必须包含 initial_admin.email、display_name、password，不接受 role 字段。
- 密码在进入事务前用现有用户密码哈希策略处理；事务内用固定 SQL 字面量写 role='admin'，不把 role 作为客户端参数。
- tenant 或 user 唯一冲突时全部回滚；响应只返回 user id/email，不返回密码或哈希。
- 不新增“任意改用户为 admin”的通用 API，后续多管理员能力留待独立安全设计。

现有 user store 接受 DBTX 抽象，说明可在调用者事务中复用；users.role 仍由二值 CHECK 保护。证据：backend/internal/userauth/store.go:22-32、backend/internal/userauth/store.go:69-101、backend/sql/migrations/0076_user_role.up.sql:7-14。

## 6. 超管 API、包结构与接线

### 6.1 新包

新增 backend/internal/adminresellerhttp，避免继续把 handler 塞进 adminhttp 或 gatewayhttp 大包。建议生产文件预算：

- routes.go：路由与写分级；
- handlers.go：七个窄 handler；
- contract.go：请求/响应 DTO、严格解码与错误映射；
- store.go：事务适配器；
- authz.go：platform role + CanActOnTenant 组合 guard；
- 最多再拆一个 list_store.go，超出前先运行 codebudget 检查。

测试文件不与生产实现混放逻辑：routes_test.go、authz_mutation_test.go、store_integration_pg_test.go、concurrency_integration_pg_test.go。仓库已有独立 adminquotahttp 包及 handler 级 scope 范式可照 HUAKAI 自身行为复用。证据：backend/internal/adminquotahttp/routes.go:52-73、backend/internal/adminquotahttp/routes.go:90-153。

### 6.2 路由契约

所有七个操作都先验证 admin 身份，再检查 RolePlatformAdmin，最后显式调用 CanActOnTenant；scope 在 store 之前失败。平台角色与 scope 检查必须是两个独立分支，便于判别性变异测试。

| 方法与路径 | 目标 scope | 主要请求 | 成功语义 |
|---|---|---|---|
| GET /admin/v1/resellers | parent_tenant_id 查询参数 | cursor、limit、parent_tenant_id | 列出直接子级，不默认扫全树 |
| POST /admin/v1/resellers | body.parent_tenant_id | name、wholesale_multiplier、四模式、initial_admin | 原子创建子租户与首个 admin |
| GET /admin/v1/resellers/{tenant_id} | 路径 tenant_id | 无 | 返回配置与脱敏账号摘要 |
| PATCH /admin/v1/resellers/{tenant_id}/status | 路径 tenant_id | status、reason | 只允许 active/suspended |
| PUT /admin/v1/resellers/{tenant_id}/wholesale-multiplier | 路径 tenant_id | wholesale_multiplier、reason | 仅改批发倍率 |
| PUT /admin/v1/resellers/{tenant_id}/modes | 路径 tenant_id | domain_mode、announcement_mode、transparency_mode、reason | 不在此接口独立改 upstream_mode |
| PUT /admin/v1/resellers/{tenant_id}/upstream-allocation | 路径 tenant_id | mode、provider_account_ids、reason | 共享清空；专属原子替换 |

创建 body 的 parent_tenant_id 可指向任意 active tenant，因此数据库结构可生成孙级；但调用者仍必须是平台超管，分销商不能自建下级。目标或 parent 是根/子、active/停用、软删等判断都在事务里再次校验，防授权后状态竞争。根租户不能被这些 reseller 路由改倍率、模式或停用。权威模型把分销商定义为带 parent 的 child tenant。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:15、docs/process/plans/2026-07-15-reseller-arc-final-model.md:22。

请求解码统一使用 64 KiB 上限、单个 JSON 值、DisallowUnknownFields、禁止尾随 token；tenant/account ID 必须为正 int64，倍率按字符串解析成定点数，模式使用服务端 allowlist。现有 provider account 解码已有 MaxBytesReader，但 json.Unmarshal 默认不拒绝未知字段，新包应主动更严。证据：backend/internal/gatewayhttp/admin_pool_accounts_handler.go:467-489。

响应与错误建议：

- 401：无效凭据；503：admin auth/store 瞬时故障，沿用现有映射。证据：backend/internal/adminquotahttp/routes.go:195-207。
- 403：角色不符、scope 不符、试图操作根或非本授权树。
- 404：已经通过 scope 后目标不存在/软删；不以 404 泄露别树存在性。
- 409：tenant 名称冲突、账号已专属分配、并发版本冲突。
- 400：非法枚举、倍率、状态、ID、未知字段或 shared/dedicated 形状不一致。

### 6.3 store 与事务

新增 backend/sql/queries/admin_resellers.sql，所有 CRUD 通过 sqlc.arg/sqlc.narg 或 pgx 的 $1...$n 参数；列表固定 ORDER BY id 与 cursor，不接受任意 sort 列。admin SQLC 只消费 yaml 列出的查询，所以要显式加入并重新生成。证据：backend/sqlc.yaml:3-25。

事务边界：

1. 创建：锁父租户/确认 active → INSERT tenant → INSERT 固定 role='admin' 用户 → INSERT 审计 → COMMIT。
2. 状态：SELECT target FOR UPDATE 且确认 parent 非 NULL → 更新 status → 审计 → COMMIT。
3. 倍率/模式：锁 target → 确认 child → 更新单一列族 → 审计前后值 → COMMIT。
4. 分账号：锁 target → 校验 child 与账号集合 → 验证每个账号的 owner 是祖先、未软删且未被他租户专属 → 删除旧映射/插入新映射 → 更新 upstream_mode → 审计 → COMMIT。

任何审计插入失败都必须回滚配置，不能沿用“业务先写、审计后写失败只返回 503”的非原子形态。quota store 已提供事务内改动加审计的正确样例。证据：backend/internal/adminquotahttp/store.go:82-170。

### 6.4 gateway 与 OpenAPI

在 backend/cmd/gateway/routes.go 的 quota 管理包附近挂 adminresellerhttp.MountRoutes，依赖为 d.adminAuth 与基于 d.pgPool 的 store；该区域已按独立包挂载管理功能。证据：backend/cmd/gateway/routes.go:1058-1070。

在 backend/cmd/gateway/wiring.go 把 adminsessionauth 的第三个依赖从通用 role store 换为专用身份 store。证据：backend/cmd/gateway/wiring.go:1491-1496。

七个新 operation、DTO、枚举、decimal string、错误响应和安全方案必须先/同片加入 docs/openapi/openapi.yaml；前后端类型只从统一契约生成。当前运行时有 path/method parity 测试，漏写契约会造成漂移。证据：docs/openapi/openapi.yaml:11、backend/cmd/gateway/openapi_method_parity_test.go:19-28、backend/cmd/gateway/openapi_consistency_test.go:157-167。

### 6.5 可选 Phase 1 尾部前端

默认建议 Phase 1 只交后端与契约。若 Owner 要求超管 UI 同期落地，仅增加超管的“分销商”页与确认弹窗，不做分销商自助 UI；新增 route/nav 时仍由后端授权兜底。当前前端 admin 路由只做登录 guard，operator 页面导航也未按 tenant scope 区分，不能把隐藏菜单当安全边界。证据：frontend/src/app/router.tsx:87-149、frontend/src/app/router.tsx:175-187、frontend/src/auth/RequireAuth.tsx:4-14。

若子租户 admin 在 Phase 1 就能登录现有 admin shell，应在 /v1/auth/me 增加服务端派生的 admin_scope_kind/capabilities 并隐藏平台专属导航；但后端仍必须 403。当前面板归属只根据 users.role 导出 admin shell。证据：frontend/src/auth/me.ts:168-184、backend/sql/migrations/0076_user_role.up.sql:1-14。

## 7. 安全专章：判别性变异测试

### 7.1 SQL 全参数化与注入

实施规则：

- admin_resellers.sql 中所有字符串、ID、倍率、模式、cursor、reason 都使用 sqlc 参数；手写递归 CTE 也只用 $1，不拼 fmt.Sprintf。现有 panelauth store 是 $1/$2 的合格最低范式。证据：backend/internal/panelauth/store_postgres.go:29-33、backend/internal/panelauth/store_postgres.go:51-54。
- provider_account_ids 先逐项解析成 int64，再作为 bigint[]/unnest 参数；绝不把逗号字符串拼进 IN (...)。
- ORDER BY、列名、动作名全部是服务端常量；客户端不能提供 SQL 标识符。
- 名称、邮箱、reason 可含引号、分号和注释符，应被存为数据或被业务校验拒绝，不得影响其他行。

判别测试：

1. 真 PG 事务内创建哨兵 tenant B；向 name、email、reason 分别发送 x'); UPDATE tenants SET status='suspended' WHERE id=<B>; --。期望请求只是成功存储普通文本或 400，B 始终 active，tenant 数量只发生预期变化，测试结束回滚。
2. 变异：把创建查询的 name 参数替换为字符串拼接。上述 payload 会改动 B 或产生多语句错误，断言立即变红；这比只 grep “是否有 sqlc.arg”更有判别力。
3. provider_account_ids 发送 1) OR TRUE -- 必须在 int64 解析层 400，store 调用计数为零；变异为把原始字符串直拼 IN 子句后，哨兵账号会被错误选中，测试变红。
4. 模式与倍率使用恶意字符串时只能命中解析/CHECK，不能改变 schema 或其他行；把参数绑定变异成拼接后，状态哨兵断言变红。

权威底稿要求所有分销商输入都禁止拼 SQL。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:29-31。

### 7.2 每个新 handler 的 IDOR 拒绝

测试夹具建立 root A、child A1、grandchild A1a、root B、child B1。另造一个“角色字符串为 platform_admin、但正 scope=A1、可信成员仅 A1/A1a”的畸形已认证身份；正常数据库不会产出它，但它可证明 role gate 与 scope gate 都真实生效。

每个 handler 做一正一负：

| handler | 正例 | 负例 | 删除 scope 校验后的红点 |
|---|---|---|---|
| 列表 | parent=A1，200 | parent=B1，403 | fake store 收到 B1，调用计数从 0 变 1 |
| 创建 | parent=A1，201 | parent=B1，403 | B1 下出现 sentinel child |
| 详情 | target=A1a，200 | target=B1，403 | 返回 sentinel 详情 |
| 状态 | target=A1a，200 | target=B1，403 | B1 status 被改 |
| 批发倍率 | target=A1a，200 | target=B1，403 | B1 multiplier 被改 |
| 模式 | target=A1a，200 | target=B1，403 | B1 modes 被改 |
| 分账号 | target=A1a，200 | target=B1，403 | B1 映射被替换 |

每个测试的 fake store 在被调用时返回可辨认成功值，而不是也返回 403；这样从对应 handler 删除 CanActOnTenant 后必红，不能被 store 偶然拒绝遮蔽。真实 store 的 UPDATE/SELECT 仍同时带 tenant/account owner 谓词，形成第二层隔离。handler 级显式校验是权威硬门，现有 quota handler 已展示先解析目标再 CanIssue 的模式。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:29-32、backend/internal/adminquotahttp/routes.go:90-153。

### 7.3 提权防线

#### A. 分销商不能改批发倍率、模式、状态、分账号或建站

对七个路由分别注入 RoleTenantOperator、scope=A1 的真实形态身份：即使 target=A1/A1a 且 CanActOnTenant 为 true，也必须因 platform role gate 返回 403、store 零调用。

判别变异：逐个删除 platform role gate。own/subtree 请求会通过 CanActOnTenant 并触达 store，断言必红。反过来删除 scope gate则由 7.2 的畸形平台身份测试变红。两个 guard 不能合并成一个无法区分的 mock。

批发倍率 handler DTO 不接受 retail_multiplier、role、scope、parent_tenant_id；modes DTO 不接受 wholesale_multiplier；未知字段 400。这样分销商不能借 mass assignment 自我加权或换父。批发倍率必须超管专属是权威锁定项。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:15-16、docs/process/plans/2026-07-15-reseller-arc-final-model.md:24、docs/process/plans/2026-07-15-reseller-arc-final-model.md:32。

#### B. 分销商不能碰凭证或上游账号控制面

scope 收口同片必须盘点并锁住现有敏感面：

- provider account POST/PATCH/PATCH enabled/clear-rate-limit/DELETE 目前共用允许 tenant_operator 的 resolver，且创建可携带原始 credentials。证据：backend/internal/gatewayhttp/admin_pool_accounts_handler.go:45-52、backend/internal/gatewayhttp/admin_pool_accounts_handler.go:67-153、backend/internal/gatewayhttp/admin_pool_accounts_handler.go:281-417、backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-349。
- credential CRUD 已经走 platform resolver，但其中四个写路由显式 SessionSafe；要补 child session 与 child token 的 403 锁定测试，防未来 guard 回退。证据：backend/internal/gatewayhttp/admin_credentials_handler.go:72-80、backend/internal/gatewayhttp/admin_credentials_handler.go:313-334。
- renew-status 当前显式允许 tenant_operator；provider account test 与上游模型探测也允许 tenant_operator 并会在服务端触碰凭证，Phase 1 要改为平台专属或至少显式拒绝 parent 非 NULL 的 reseller scope。安全优先建议平台专属。证据：backend/internal/gatewayhttp/admin_credentials_handler.go:263-310、backend/internal/adminhttp/provider_account_test_handler.go:113-142、backend/internal/adminhttp/provider_account_upstream_models_handler.go:166-196。
- credential acquisition 已有平台 guard，保持并补回归测试。证据：backend/internal/gatewayhttp/admin_credential_acquisition_handler.go:425-443。

判别测试：用 child session 与 child tenant_operator token 遍历上述 route matrix，全部 403，credential/account/test store 调用均为零；从每个敏感 resolver 删除 platform/child guard 后，对应请求触达会返回 sentinel，测试必红。分账号响应只返回账号 ID、名称和运行状态摘要，绝不返回 credentials、credential ID/版本或上游原始错误。

该加固可能收窄既有根租户 tenant_operator 的账号管理能力，属于 [Owner-gated: auth-core]。推荐最安全默认是所有秘密/账号写面只给 platform-wide；若 Owner 要保留 legacy root operator，必须用 resolver 提供的“scope 根是否有 parent”可信标志只豁免 parent=NULL 的旧 scope，绝不能按请求 tenant 自称根。当前 provider account 路由有三组别名，所有别名必须共用同一 guard。证据：backend/cmd/gateway/routes.go:1082-1128、backend/cmd/gateway/routes.go:1143-1152。

#### C. 分销商不能操作别家子树或超管根

CanActOnTenant 单测矩阵覆盖：A1→A1/A1a 允许；A1→A/root A/B1 拒绝；未知/零/负 target 拒绝；未知 role 拒绝；scope 缺失拒绝。把 descendant membership 判断变异为“任意正 ID”后，B1 断言红；把方向反写为“祖先可见”后，A root 断言红。

真实 PG handler 测试再覆盖 A1 session 对 B1、root A 的 403，避免只有值对象测试而 wiring 仍传错 scope。权威底稿要求分销商不能操作别的分销商或超管。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:31-32。

#### D. 分销商不能给自己加平台权

- users.role CHECK 保持 admin/user，不新增 platform_admin 值；平台级来自租户层级与 resolver，不来自客户端 role 字段。证据：backend/sql/migrations/0076_user_role.up.sql:7-14。
- 通用 create-user 继续只创建 user；initial admin 只能随超管 create-reseller 事务写固定 role='admin'。证据：backend/internal/adminuserhttp/user_create.go:55-105。
- admin token 签发继续要求平台管理员，子租户 admin 不能给自己签 platform token。现有 issuer 已有平台角色分支，应加 child session/token 403 回归。证据：backend/internal/admin/token_issuer.go:117-125。

判别变异：删除 create-reseller 的 platform gate，或把 initial_admin.role 改为客户端可控时，发送 role=platform_admin/admin 的请求应从 403/400 变成落库，测试必红；删除 token issuer 平台 guard 时，child 请求会返回新 token，测试必红。

### 7.4 resolver 全权收口判别测试

真 PG 建 root A→child A1→grandchild A1a 与 root B→child B1，并分别建 role=admin 用户：

1. root A admin session：RolePlatformAdmin、ScopeTenantID=0，可作用 A/B 所有 tenant。
2. child A1 admin session：RoleTenantOperator、ScopeTenantID=A1，可作用 A1/A1a，拒绝 root A、root B、B1。
3. suspended/deleted child 或 suspended/deleted admin user：统一 ErrAdminUnauthorized。
4. session tenant A1 + user id 属 B1：精确 tenant/user 谓词拒绝。
5. tenant_operator admin token scope=A1：与 session 相同的子树矩阵。

判别变异：把 resolver 返回段恢复成当前 backend/internal/adminsessionauth/resolver.go:85-91 的无条件 RolePlatformAdmin/Scope=0。此时“child identity 精确字段”断言先红，随后用该真实 resolver 打 B1 详情/状态的 403 端到端断言也红；即使实现者删除字段断言，跨树行为断言仍能抓住回退。现有真 PG 测试只断言 admin session 平台全权，必须改写而不是删除。证据：backend/internal/adminsessionauth/resolver.go:85-91、backend/internal/adminsessionauth/resolver_integration_pg_test.go:81-161。

再加一条 wiring 测试：如果 gateway 仍注入旧 RoleStore 而非新 IdentityStore，child session 无法得到子树或错误变平台，路由 403/200 矩阵必须红。接线位置唯一明确。证据：backend/cmd/gateway/wiring.go:1491-1496。

### 7.5 并发与原子性

- 两个事务同时把同一 provider_account_id 专属给 A1/B1：唯一约束保证仅一个成功，另一方 409；最终只有一行映射。
- 同一 child 同时切 shared/dedicated：tenant 行锁串行化，最终 upstream_mode 与 allocation 行满足一致性，不出现 shared+rows 或 dedicated+empty。
- 同名 tenant 并发创建：现有未软删名称唯一索引保证一个 201、一个 409；失败事务不能遗留 admin user 或审计。证据：backend/sql/migrations/0001_pool_routing.up.sql:23。
- 人为令审计 INSERT 违反 action CHECK：配置、tenant、initial admin、allocation 全部回滚，证明审计与业务同事务。当前审计 action 有显式 CHECK，可作为故障注入点。证据：backend/sql/migrations/0181_admin_audit_runtime_logs_cleanup.up.sql:5-24。

本期不写任何两层扣费并发测试，因为钱账不在 Phase 1；不得用“顺手准备”修改 settlement 热路径。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:33、docs/process/plans/2026-07-15-reseller-arc-final-model.md:37。

## 8. 实施切片、判别门与爆炸半径

### Slice 0：Owner 决策与基线锁定

改动：不改代码；确认 0185 未冲突、枚举字面量、倍率上限、分账号是否本期只配置、敏感账号面 legacy 策略、session 写分级、首 admin 创建方式。

判别门：把决策写进实施 PR 描述和测试名；任何未裁定项不得由默认值悄悄落地。

爆炸半径：无运行时影响。

标注：[Owner-gated: schema] [Owner-gated: auth-core]。

### Slice 1：只落 dormant schema

改动：0185 up/down、租户列、父指针不可变、allocation 表、审计白名单；不让任何运行时读取新模式。

判别测试：迁移 up/down/up；旧 INSERT 兼容；非法父/倍率/模式/分配约束；旧租户默认值；迁移版本嵌入测试。迁移版本测试已有固定检查位置。证据：backend/internal/db/pgconn_integration_test.go:93-106、backend/sql/embed.go:9-13。

爆炸半径：高，涉及核心 tenants 与审计 CHECK；但新增列均为 nullable/default，旧代码不读，部署可先于应用。

标注：[Owner-gated: schema]。Owner 批准后才能合并/部署。

### Slice 2：auth-core 先收口

改动：AdminIdentity 私有 scope、CanActOnTenant、递归查询、token resolver、session IdentityStore、wiring、删除旧方法并迁移全部调用点；同步封死 reseller 对 provider account/credential 敏感面。

判别测试：CanAct 矩阵、根/子 session 真 PG、token 子树、resolver 恢复全权变异、所有旧调用点编译、credential route matrix。

爆炸半径：最高，覆盖所有 admin handler 与 session 管理面；不能与 handler 开放同一不可回滚发布动作。先部署并观察现有根 admin、tenant_operator token 的 401/403 指标。

标注：[Owner-gated: auth-core]。Owner 批准后才能继续 Slice 3/4。

### Slice 3：store 与契约，暂不挂路由

改动：admin_resellers.sql、sqlc 配置/生成、adminresellerhttp store/DTO、OpenAPI 草案；不在 gateway 暴露路径。

判别测试：真 PG CRUD、审计回滚、SQL 注入、同名/专属账号并发、祖先验证、shared/dedicated 原子不变量。

爆炸半径：中；代码不可达，主要风险是生成码和迁移语义。

标注：倍率格式与 API shape 若未在 Slice 0 锁定，仍 [Owner-gated]。

### Slice 4：七个 handler 与接线

改动：routes/handlers/authz、gateway mount、method/path parity；默认所有写口 token-only。

判别测试：每个 handler 的 own/subtree 正例、别树 403、删除 scope gate 必红、删除 platform gate 必红、store 零调用、OpenAPI parity。

爆炸半径：中高；首次公开写面。可用 feature flag 或仅内部 admin token 灰度，但 flag 默认关闭、关闭时 404/明确 unavailable，不得绕过鉴权。

标注：[Owner-gated: expose admin write surface]。

### Slice 5：可选超管前端尾片

改动：只给平台超管增加 reseller 列表/创建/停用/倍率/模式/分账号页面；从 OpenAPI 生成类型；危险操作确认。

判别测试：非平台 capability 不渲染；直接调用仍由后端 403；倍率不走 JS number；错误码本地化；无 credential 字段。

爆炸半径：低到中；不改变后端安全边界。

标注：[Owner-gated: Phase 1 UI]。不批准则整体延期，不影响后端 Phase 1 验收。

### Slice 6：发布门

改动：无新功能；跑全量质量门、迁移演练、回滚演练、权限对抗审查。

判别测试：下节全部通过；S0/S1 安全问题为零，Owner 签字后才启用写路由。

爆炸半径：发布级；先 schema、再 auth、最后 routes，禁止倒序。

## 9. 验证命令与验收矩阵

实施时至少执行：

1. cd backend && sqlc generate
   证明 admin_resellers.sql 与 schema 可生成；生成差异必须纳入同片。证据：backend/Makefile:42-43。
2. cd backend && go test ./internal/admin/... ./internal/adminsessionauth/... ./internal/adminresellerhttp/...
   覆盖纯单测与 handler 判别测试。
3. cd backend && HUAKAI_DATABASE_URL=... go test -tags=integration_pg -race -count=1 ./internal/adminsessionauth ./internal/adminresellerhttp
   覆盖真 PG、并发与 resolver 行为；仓库集成测试约定使用该环境变量与 tag。证据：backend/Makefile:87-88、backend/internal/adminsessionauth/resolver_integration_pg_test.go:28-30。
4. cd backend && go test ./cmd/gateway -run 'OpenAPI|Reseller|AdminSession' -count=1
   覆盖路由、method/path 与安全契约。现有一致性测试从统一 OpenAPI 读路径/方法。证据：backend/cmd/gateway/openapi_method_parity_test.go:19-28、backend/cmd/gateway/openapi_consistency_test.go:157-167。
5. cd backend && go test ./...
   删除 CanIssueForTenant 后用全量编译抓漏迁移调用点。
6. cd backend && go test ./internal/codebudget -count=1
   确认新包没有突破生产文件/行数预算。
7. cd backend && make quality-gate
   运行静态质量 ratchet。证据：backend/Makefile:90-91。
8. 若做前端：从 docs/openapi/openapi.yaml 重生成 schema，再运行前端 typecheck、unit test 与 build；前端规范要求类型来自统一契约。证据：docs/frontend/BUILD-SPEC.md:31-37、docs/frontend/BUILD-SPEC.md:169-201。

最终验收：

- 既有根租户与旧 seed 行为不变。
- child admin session/token 只能自己与后代，不能祖先、兄弟、别根。
- 所有七个新 handler 都有删除 scope 校验必红的测试。
- 所有七个新 handler 都有删除 platform gate 必红的测试；其中 2 个读 handler、5 个写 handler。
- 恢复 resolver.go:85-91 的全权返回后，跨树端到端测试必红。
- 子租户对 provider account/credential 敏感面全 403。
- SQL 注入 payload 不改变哨兵行，拼接变异后必红。
- 写入与审计同事务；并发分配无双占。
- users.role CHECK 未改，billing/settlement 文件无差异。
- docs/openapi/openapi.yaml 与 runtime method/path 一致。

## 10. REFERENCE PROJECTS IN SCOPE（清洁室行为对照）

### 10.1 sub2api

固定版本为 sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e。可借鉴的行为只有“全局用户/分组、分组倍率、分组与账号的映射、全局管理员路由”：基础迁移把用户、分组、账号和映射放在全局模型中，后续迁移再给用户/分组增加倍率覆盖；管理路由由单一全局管理员门保护。证据：sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/001_init.sql:22-115、sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/047_add_user_group_rate_multipliers.sql:1-19、sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/middleware/admin_only.go:9-26、sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/routes/admin.go:12-95。

无等价：在所读核心路径中没有 tenant 父指针、租户子树 scope、根/子 session 身份或 child reseller；因此只把“独立分配关系表”和“定点倍率”作为行为参照，不复制其全局权限模型，也不复制标识符。证据：sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/001_init.sql:22-115、sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/middleware/admin_only.go:9-26。

### 10.2 new-api

固定版本为 new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd。可借鉴的行为是全局角色叠加资源/动作校验，最高角色可以短路全局授权；路由按资源动作挂权限。证据：new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/user.go:23-56、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:common/constants.go:202-210、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:middleware/auth.go:37-213、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/authz/resolver.go:5-35、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:router/channel-router.go:31-79。

无等价：在所读核心路径中没有 tenant 树或 target tenant 子树判定；其自定义权限引擎也与 Owner 锁定的固定分销商能力集冲突，不能移植。HUAKAI 保持 users.role 二值并在 CanActOnTenant 上叠加 scope。参考证据：new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/user.go:23-56、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/authz/resolver.go:5-35；权威依据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-24。

### 10.3 CLIProxyAPI

固定版本为 CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465。可观察到的是全局客户端 key 列表、单一远程管理密钥和整组管理路由共用一个管理中间件；认证结果只有 provider/principal/metadata 级信息。证据：CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/config/sdk_config.go:35-46、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/config/config.go:303-317、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/access/types.go:3-24、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/server.go:679-735、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/handlers/management/handler.go:262-295。

无等价：在所读核心路径中没有用户子账号、租户、多级分销商、目标 tenant scope 或专属账号按租户分配；不能为本 Phase 1 提供可复用授权模型。证据：CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/access/types.go:3-24、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/server.go:679-735。

### 10.4 三镜形态库存与取舍

| 参考镜 | 入口/路径形态 | 模式与状态 | actor/scope 形态 | HUAKAI Phase 1 取舍 |
|---|---|---|---|---|
| sub2api | 一个全局管理路由组覆盖用户、分组、账号与设置 | 用户为全局二类角色；分组可表达倍率、独占与账号映射 | 全局管理员，无目标租户树 | 借鉴“倍率独立列、账号独立映射”；拒绝全局权限模型。证据：sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/001_init.sql:22-115、sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/routes/admin.go:12-95。 |
| new-api | 管理路由按资源/动作挂全局授权 | 最高全局身份可短路；其他身份按资源动作集合 | 用户 ID + 全局系统角色，无目标租户树 | 不引入自定义权限引擎，只保留平台全通与固定子树 scope。证据：new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:middleware/auth.go:37-213、new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/authz/resolver.go:5-35。 |
| CLIProxyAPI | 整组管理入口共用单一管理密钥门；客户端 key 为全局列表 | 管理开/关与全局 key 校验，没有子账号状态机 | 单一管理者与认证主体，无 tenant target | 仅把“管理入口集中防护”作为审查提醒；授权与层级均无可复用等价。证据：CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/config/config.go:303-317、CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/server.go:679-735。 |

该库存覆盖入口、模式、状态与 actor 四维；三镜都没有 HUAKAI 所需的 parent tenant + subtree scope 组合，因此本稿的层级授权来自 HUAKAI 权威模型与现有 tenant 隔离，不从参考项目做逐行翻译。权威依据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:22-24；HUAKAI 现状：backend/sql/migrations/0001_pool_routing.up.sql:12-24、backend/internal/admin/operator_auth.go:140-158。

## 11. 与权威底座交叉后的分歧与空白点

以下不是擅自改模，而是交给 Claude/Owner 交叉裁定：

1. 分账号生效期矛盾：权威底稿在 Phase 1 写“分账号”，又在 Phase 3 写“dedicated 账号”。本稿按本轮任务只做 Phase 1 控制面写口、数据完整性与审计，不改 selector；Owner 若要求本期真正按 dedicated 路由，必须另开 routing-core 设计与并发容量测试，不能隐含塞入本片。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:36-38。
2. 首个子租户 admin 的创建未锁定：不放宽通用 user create 的前提下，本稿建议随 create-reseller 原子创建固定 role='admin' 用户；Owner 需确认 initial_admin 是必填、邀请制还是另设一次性 bootstrap。现有通用接口明确不允许 admin。证据：backend/internal/adminuserhttp/user_create.go:55-105。
3. 根租户即平台的兼容语义未显式锁定：本稿为零回归把 parent=NULL 的 admin session 视为平台级；若数据库允许多个独立根租户，这些根 admin 都会是平台级，与“系统部署者唯一”产品语义需要 Owner 核实。当前旧 resolver 本来就给所有 admin session 平台全权，所以本稿没有扩大权限。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:8-11、backend/internal/adminsessionauth/resolver.go:85-91。
4. 模式枚举字面量未锁定：本稿建议 shared_pool/dedicated_accounts、platform_domain/custom_domain、platform/tenant、masked/isolated；Owner 需在迁移前冻结，后续改 CHECK 会增加迁移成本。权威只锁了业务形态，没有锁 DB 值。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:25-27。
5. 批发倍率上限未锁定：本稿只设 >0，不擅自设 1、100 或其他上限；numeric(20,8) 与 decimal string 沿用现有倍率精度。Owner 需确认是否有商业上限。证据：backend/sql/migrations/0078_pool_group_pricing_ratios.up.sql:3-18、docs/process/plans/2026-07-15-reseller-arc-final-model.md:15-16。
6. session 写权限未锁定到具体端点：本稿默认新写口 token-only；若超管前端必须靠 session 写，建议只把建站/停用/模式标 SessionSafe，倍率与分账号仍 token-only，最终由 Owner 逐端点批准。现有机制明确是 opt-in。证据：backend/internal/adminsessionauth/writeclass.go:8-28。
7. 既有 tenant_operator 的凭证能力兼容策略未锁定：安全默认应把秘密与账号写面收成 platform-wide；若要保留历史根 scope operator，必须按可信 parent=NULL 例外，不能让 child 继承。当前 provider account resolver 确实允许 tenant_operator。证据：backend/internal/gatewayhttp/poolaccountadmin/contract.go:316-349。
8. 父停用是否级联孙级未锁定：Phase 1 不开放分销商自建孙代理，本稿只保证目标 child 的 API key/session/token 停用；未来开放多级前，要裁定祖先 suspended 是动态封锁整树还是物化级联状态。当前 inbound key 查询只检查目标 tenant 状态。证据：backend/sql/queries/auth_inbound.sql:11-41、backend/internal/auth/api_key_resolver.go:142-164。
9. Phase 1 前端边界未锁定：权威 Phase 1 说“超管 UI”，本轮任务允许前端放在尾部或并入；本稿建议后端先成片，超管页单独 Owner-gated，分销商 UI 严格留 Phase 2。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:36-37。

## 12. 预计实施文件清单

必须改：

- backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.up.sql
- backend/sql/migrations/0185_reseller_phase1_tenant_hierarchy.down.sql
- backend/sql/queries/admin_resellers.sql
- backend/sqlc.yaml
- backend/internal/db/admin/ 下 sqlc 生成差异
- backend/internal/admin/operator_auth.go 及其测试
- backend/internal/adminsessionauth/resolver.go、专用 store、单测与真 PG 测试
- 第 5.4 节列出的全部 CanIssueForTenant 调用点
- backend/internal/adminresellerhttp/ 新包与测试
- backend/cmd/gateway/routes.go
- backend/cmd/gateway/wiring.go
- docs/openapi/openapi.yaml
- backend/cmd/gateway 的 OpenAPI/路由一致性测试
- provider account/credential/test/upstream-model 敏感入口及其权限测试

可选改：

- frontend 的超管 reseller feature、路由、导航、OpenAPI 生成类型
- /v1/auth/me 的 admin scope/capability 展示字段，仅在 Phase 1 同期允许子 admin 进入现有 shell 时需要

明确不得改：

- backend/sql/migrations/0076_user_role.up.sql 的 users.role CHECK。证据：backend/sql/migrations/0076_user_role.up.sql:7-14。
- billing、settlement、hold、ledger 的扣费算法。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:18-19、docs/process/plans/2026-07-15-reseller-arc-final-model.md:37。
- Host/TLS/自定义域名运行时。证据：docs/process/plans/2026-07-15-reseller-arc-final-model.md:27、docs/process/plans/2026-07-15-reseller-arc-final-model.md:38。

## 13. 源码读取证明

本稿只读取了唯一允许的 reseller 权威底稿；未读取任何其他 reseller 计划稿。HUAKAI 现状与参考项目均按行为级清洁室方式对照。

Source files read:
- docs/process/plans/2026-07-15-reseller-arc-final-model.md
- AGENTS.md
- backend/internal/admin/operator_auth.go
- backend/internal/adminsessionauth/resolver.go
- backend/internal/adminsessionauth/writeclass.go
- backend/internal/adminsessionauth/resolver_test.go
- backend/internal/adminsessionauth/resolver_integration_pg_test.go
- backend/internal/panelauth/store_postgres.go
- backend/sql/migrations/0001_pool_routing.up.sql
- backend/sql/migrations/0010_admin_auth.up.sql
- backend/sql/migrations/0041_tenant_composite_foreign_keys.up.sql
- backend/sql/migrations/0076_user_role.up.sql
- backend/sql/migrations/0078_pool_group_pricing_ratios.up.sql
- backend/sql/migrations/0181_admin_audit_runtime_logs_cleanup.up.sql
- backend/sql/migrations/0184_burst_value_comment.up.sql
- backend/sql/migrations/README.md
- backend/sql/queries/admin_tokens.sql
- backend/sql/queries/auth_inbound.sql
- backend/sql/embed.go
- backend/sqlc.yaml
- backend/Makefile
- backend/internal/adminquotahttp/routes.go
- backend/internal/adminquotahttp/store.go
- backend/internal/auth/api_key_resolver.go
- backend/internal/userauth/store.go
- backend/internal/adminuserhttp/user_create.go
- backend/internal/adminuserhttp/user_crud_test.go
- backend/internal/adminuserhttp/tenant_scope.go
- backend/internal/admin/token_issuer.go
- backend/internal/gatewayhttp/poolaccountadmin/contract.go
- backend/internal/gatewayhttp/admin_pool_accounts_handler.go
- backend/internal/gatewayhttp/admin_credentials_handler.go
- backend/internal/gatewayhttp/admin_credential_acquisition_handler.go
- backend/internal/adminhttp/provider_account_test_handler.go
- backend/internal/adminhttp/provider_account_upstream_models_handler.go
- backend/cmd/gateway/routes.go
- backend/cmd/gateway/smoke_test.go
- backend/cmd/gateway/wiring.go
- backend/cmd/gateway/openapi_method_parity_test.go
- backend/cmd/gateway/openapi_consistency_test.go
- backend/cmd/mvp-seed/main.go
- backend/cmd/smoke-setup/main.go
- backend/internal/accountfphttp/fingerprint_handler.go
- backend/internal/admin/issuer.go
- backend/internal/admin/revoker.go
- backend/internal/adminhttp/api_keys_handler.go
- backend/internal/adminhttp/provider_account_tenant_resolve.go
- backend/internal/adminhttp/provider_catalog_handler.go
- backend/internal/alertinghttp/helpers.go
- backend/internal/announcementhttp/handlers.go
- backend/internal/controlhttp/dispute_handler.go
- backend/internal/db/pgconn_integration_test.go
- backend/internal/hermeshttp/admin_auth.go
- backend/internal/modelbindingadminhttp/routes.go
- backend/internal/modelroutingadminhttp/routes.go
- backend/internal/moderationhttp/helpers.go
- backend/internal/orphanreconcilehttp/reconcile.go
- backend/internal/pricingcataloghttp/admin_helpers.go
- backend/internal/proxyadminhttp/routes.go
- backend/internal/proxyadminhttp/tenant_default_routes.go
- backend/internal/riskoverviewhttp/handler.go
- backend/internal/setuphttp/setuphttp_integration_test.go
- backend/internal/tenancy/bootstrap.go
- backend/internal/usernoticehttp/handlers.go
- docs/openapi/openapi.yaml
- frontend/src/app/router.tsx
- frontend/src/auth/RequireAuth.tsx
- frontend/src/auth/me.ts
- frontend/src/auth/tokenForPath.ts
- docs/frontend/BUILD-SPEC.md
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/001_init.sql
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/047_add_user_group_rate_multipliers.sql
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/migrations/127_add_user_group_rpm_override.sql
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/middleware/admin_only.go
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/server/routes/admin.go
- sub2api@12d811bd76572836d6df6e1fa8aa5ff91be3b12e:backend/internal/service/admin_user.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/user.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:common/constants.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:middleware/auth.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/authz/resolver.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:service/authz/assignment.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:router/channel-router.go
- new-api@246d62aa5ed3ba2a4728322c269c180a016dc9cd:model/authz_role.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/config/sdk_config.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/config/config.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/access/types.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:sdk/access/registry.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/access/config_access/provider.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/server.go
- CLIProxyAPI@26d45fd46a2d2911adef14772465131066dae465:internal/api/handlers/management/handler.go

Lane: specifier
Agent: GPT-5 Codex /root
UTC timestamp: 2026-07-15T07:01:14Z
