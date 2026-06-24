# 修正版切片计划:model_pool_bindings 写路径 + 前端绑定页(全栈纵切,闭 #1+#3)— 2026-06-16 REVISED

**PM:** Claude (Opus 4.8)。**来源:** 综合 7 角度深挖 + 2 份对抗复核 + 本人对当前分支 `feat/frontend-portal` 的真码二次核实。本文取代草案 `2026-06-16-slice-model-pool-bindings-fullstack.md`。

**裁决一句话:** 草案主干(snapshot/读路径/codebudget/默认零行为变化)理解正确,但有 **5 条 S1 级缺陷必须改才安全/可做**,且草案的两个"照抄对象"互相矛盾(经真码核实,二者是不同的鉴权门、不同的包家族、不同的路由前缀)。**不建议按原稿开工。** 本修正版把这些钉死。

---

## A. 草案假设逐条裁决【证实 / 修正 / 推翻】

| # | 草案假设 | 裁决 | 证据(当前分支 `feat/frontend-portal` 真码) |
|---|---|---|---|
| A1 | snapshot.version 写绑定必须同 Tx bump,机制已有可复用 | **证实** | `bumpAffectedSnapshots`(`backend/internal/registry/model_sync_writer.go:562-590`)CTE:`model_pool_bindings WHERE model_id=ANY AND deleted_at IS NULL`(:573-575)+ `ON CONFLICT DO UPDATE version=version+1`(:581);新 INSERT 行会被命中。schema 头注要求同 Tx bump(`0008_model_registry.up.sql:138-141`)。 |
| A2 | resolver 无缓存(L0),写经 version bump 可见,版本戳 `registry:<tid>:<v>` 如实 | **证实** | REPEATABLE READ + ReadOnly TX,`SnapshotVersion: registry:%d:%d`(`postgres_registry.go:131-157`)。L0 noopCache。 |
| A3 | model_pool_bindings 现状零 admin 写路径(纯 inert gap) | **证实** | `registry.sql` 只有 `ListModelPoolBindings`(:101);生产无 Insert/Update/Delete binding。 |
| A4 | controlhttp 加 1 文件不爆 codebudget | **证实** | controlhttp ≈ 2.5k 非测试行 / 13 文件(< 6000 / < 20),不在 `baseline.json`。registry 同安全。加 1 handler + 1 registry 文件均绿。 |
| A5 | 表+列已存在,无新迁移,低 schema 风险 | **证实** | `model_pool_bindings`(`0008:141-189`)全列就位;唯一索引 `uq_bindings_tenant_model_pool` 是 **partial**(`WHERE deleted_at IS NULL`,:182-184)→ 软删后重建同三元组不冲突。 |
| A6 | 默认零行为变化(不写=旧行为) | **证实(附带条件)** | 绑定只在 operator 真写时喂 resolver;无后台任务。**但前提是先修 A10 的 sqlc 漂移**,否则 sqlc generate 会改动既有行为(见推翻项)。 |
| A7 | tenant 交叉校验 = "binding.tenant == model.tenant(scope=tenant 模型)" | **修正(谓词写错)** | 全局模型 `tenant_id IS NULL`(`postgres_registry.go:93-94`),该谓词对全局模型恒假 → 合法的"全局模型 + 租户 pool_group"继承绑定被拒,而这正是 schema 钦定的继承主路径(`0008:133-138`)。正确谓词须照 `model_alias_import.go:149-150`:`(m.scope='tenant' AND m.tenant_id=$T) OR (m.scope='global' AND m.tenant_id IS NULL)`。 |
| A8 | GET "复用 ListModelPoolBindings" | **推翻** | 该查询是 **路由专用读**:`model_id` 必填(`registry.sql:101`)、`mpb.enabled=true`、effective 时间窗、`INNER JOIN pool_groups ... pg.enabled=true AND deleted_at IS NULL`,且额外 JOIN channels 算 body_param_strips/param_override(:120-149)。它会**隐藏** admin 正要管理的 disabled/未来生效/已过期/挂停用 pool 的绑定,且不回 enabled/disabled_reason/effective_* 编辑字段。必须新建 admin 专用 list 查询。 |
| A9 | handler "照抄 model_admin_aliases 范式" **且** "照抄 proxyadminhttp 双角色门" | **推翻(二者互斥,且都被误述)** | 真码:model admin handler 自身**无** Auth(`AdminModelAliasesDeps` 只有 `Store`,`model_admin_aliases_handler.go:16-18`),靠路由层 `adminGate` 包。而 `adminGate` 是 **platform_admin-ONLY**(`middleware.go:144`:`if id.Role != RolePlatformAdmin → 403`),**不是双角色门**。proxyadminhttp 不用 `adminGate`,用自己的 `Auth + resolveTenant` 双角色门(`routes.go:828-833` + `proxyadminhttp/routes.go:275-335`),是另一个包家族(`/admin/v1/`)。→ "对齐 alias 范式" = 拿到 platform-only 门、零租户 scope;"照抄双角色门" = 换包家族。**不能同时满足,必须做结构决策(见 D1)。** |
| A10 | "sqlc 生成到 db/registry/" 是安全机械步 | **推翻(头号隐蔽坑)** | 当前分支 `registry.sql` 源文件的 `ListModelPoolBindings` **没有** `sensitive_words` 列(:120-149 只有 body_param_strips/param_override),但**生成码** `db/registry/registry.sql.go:185,253,285` **有** sensitive_words 且被 resolver 解码(`postgres_registry.go:167`)。源与生成码**已提交漂移**。一旦本切片跑 `sqlc generate`,会按落后的 .sql 重生成、**删掉 sensitive_words 列** → resolver 编译失败 / 敏感词网关静默失效。**必须先把 sensitive_words 补回 registry.sql 源文件再生成。** |
| A11 | DELETE(软删)bump 自动覆盖该租户 | **修正(有覆盖漏洞)** | `bumpAffectedSnapshots` CTE 找受影响租户用 `WHERE ... AND deleted_at IS NULL`(:573-575)。软删后该行 deleted_at 非空 → 若 bump 跑在软删**之后**,CTE 找不到该行;若这是该租户该 model 的最后一条存活 binding 且 inherit_global=false,**该租户不被 bump**。删了绑定但版本不前进 → 版本戳一致性破坏(Slice5 缓存上线后读陈旧缓存)。 |
| A12 | UI 暴露 priority_weighted 但灰标 | **证实(诚实标注正确,附一条补充)** | schema 注释当前按 strict_priority 跑(`0008:138`),resolver L0 取序首。后端 CHECK 允许存 priority_weighted(存而不执行符合 Feature Preservation)。补充:weight 仅在 priority_weighted 下有意义,UI 须联动提示。 |

---

## B. 修正后的范围(Scope)

### 后端

**B1. `backend/sql/queries/registry.sql`(改) → sqlc 生成到 `db/registry/`** — 目标包:`backend/internal/db/registry`(生成)+ 源在 `backend/sql/queries/`

> **前置阻断步(必须先做,见 A10):** 在加任何新查询前,先把 `sensitive_words` 列补回 `registry.sql` 的 `ListModelPoolBindings`(对齐已提交的生成码 `registry.sql.go:185/208/214`),`sqlc generate` 后 `go build` 确认 resolver + 敏感词网关不被改动。这一步独立验证、独立小 commit,**不与新功能混提**。

新增写查询:
- `InsertModelPoolBinding`(回插入行)
- `UpdateModelPoolBinding` — **WHERE id=$ AND tenant_id=$**(强制 tenant-scoped,见 A-review R3)
- `SoftDeleteModelPoolBinding` — **WHERE id=$ AND tenant_id=$**,set deleted_at=now()
- `GetModelPoolBindingByID` — **WHERE id=$ AND tenant_id=$**
- `ListModelPoolBindingsAdmin`(**新建,不复用路由查询,见 A8**)— 无 enabled/时间窗/pg.enabled 过滤;`model_id`/`pool_group_id` 均**可选**(`sqlc.narg`);回全部可编辑字段(含 enabled/disabled_reason/effective_from/effective_until/selection_mode/provider_model_id_override/fallback_class/rpm/tpm/max_parallel/reason)。
- **bump 不新建查询:复用 `bumpAffectedSnapshots`**(见 D2)。

**B2. `backend/internal/registry/bindings_admin.go`(新)** — 目标包:`backend/internal/registry`

- 每个写方法把 **binding mutation + snapshot bump 包同一 Serializable Tx**。
- tenant 交叉校验**正确谓词**(A7):`(m.scope='tenant' AND m.tenant_id=$T) OR (m.scope='global' AND m.tenant_id IS NULL)`,在 upsert 的 model 存在性子句里实现,照 `model_alias_import.go:149-150`。
- pool_group 归属由复合 FK(`0041`)兜底,但写前显式查 `pool_groups WHERE id=$ AND tenant_id=$ AND deleted_at IS NULL` 给出 422 友好错(而非 DB FK 的 500)。
- **DELETE 的 bump 用按 tenant 直接 bump,不靠 model-id CTE**(A11):软删**前**先抓 tenant_id,软删后用单租户 `INSERT ... ON CONFLICT version=version+1`(照 `model_alias_import.go:219` 单租户分支);或在软删前调 CTE。二选一,**计划钉死用"软删前抓 tenant + 单租户直 bump"**(最稳,不依赖存活行)。
- INSERT/UPDATE 路径可调 `bumpAffectedSnapshots(ctx, tx, []int64{modelID}, reason, actor)`(CTE 命中新存活行,安全)。

**B3. handler — 包与门见 D1 决策。** 两个候选落点:
- **D1-A(推荐):** `backend/internal/controlhttp/model_admin_bindings_handler.go`(新) + 在 deps 里**自建 Auth + 双角色 resolveTenant**(不是照 alias 范式的 platform-only `adminGate`,因为 binding 要支持 tenant_operator 管自己租户的绑定)。路由 `/v1/admin/models/...`(对齐 model admin 家)。
- **D1-B:** 放 adminhttp/proxyadminhttp 家族(`/admin/v1/model-pool-bindings`),双角色门现成,但不对齐 model admin。

无论 A/B:
- 端点 4 个(GET list / POST / PATCH{id} / DELETE{id} 软删)。
- **GET 用 `ListModelPoolBindingsAdmin`**(A8)。
- **by-id PATCH/DELETE 的 tenant 防越权落在查询层 WHERE tenant_id=$**(A-review R3):门只验"caller 能操作 tenant_id",不验"id 属于该 tenant";若查询写成 `WHERE id=$` 单条件,门全绿仍可跨租户改删。
- POST 唯一冲突 → 映射 **409 `binding_already_exists`**(不是 500)。
- secret-free 读 DTO(provider_model_id_override 不是 secret,但 reason/disabled_reason 照常返回)。

**B4. `cmd/gateway/routes.go` + wiring** — 挂载 + 注入(adminAuth resolver、modelRegistry/bindings store)。

### 前端

**B5. `frontend/lib/api/modelBindings.ts`(新)** — 类型逐字段对齐 DTO(含 effective_from/until)+ list/create/update/delete。

**B6. model picker 数据源** — 见 D3。要么新增 admin model-list 端点,要么降级为手输 model_id(计划须明确)。

**B7. `frontend/app/admin/models/bindings/page.tsx`(新页)** — 按 model/pool 列绑定 + CRUD 弹窗。字段:model / pool_group / priority / weight / selection_mode / provider_model_id_override / fallback_class / rpm·tpm·max_parallel / **effective_from / effective_until** / enabled / disabled_reason。UI 校验:`effective_from < effective_until`;selection_mode=strict_priority 时 weight 输入框禁用/提示"当前 mode 下不生效"(A12)。

**B8. `Sidebar.tsx`** — admin nav 加「模型绑定」槽。孤儿 `app/bindings/page.tsx` 去留见 D4。

### 测试

**B9. 后端 handler 测试:**
- auth 门:无/错 token → 401/403。
- **tenant 越权(handler 层,断言 403,与 DB FK 的 500 区分,A-review C2):** platform_admin 带错 tenant_id → CanIssueForTenant 行为;tenant_operator(scope=A)`PATCH/DELETE` 属于 B 的 binding id → **403/404**(不是 200)。
- 全局模型继承绑定**写得进**(A7 正谓词回归):scope=global model + 租户 pool_group → 成功。
- **snapshot bump 变异验证(判别式钉死,见 §C 头号不变量)。**
- 唯一冲突 → 409;effective_from>=until → 422。
- SQL 写走 integration_pg(gated)。

**B10. 前端** `tsc --noEmit` 绿 + `frontend_wiring_test.go` 加真后端 E2E 断言(建/读绑定)。

---

## C. 头号不变量:snapshot.version bump 的确切机制(写进计划)

### C.1 机制(三条写路径)

1. **INSERT / UPDATE binding:** 在同一 Serializable Tx 内,写完 binding 行后调
   `bumpAffectedSnapshots(ctx, tx, []int64{modelID}, reason, actor)`。
   其 CTE(`model_sync_writer.go:565-577`)bump 两类租户:(a)`inherit_global_catalog=true` 的租户,(b)`model_pool_bindings WHERE model_id=ANY AND deleted_at IS NULL` 的租户。新存活行被命中 → 该租户 `version=version+1`(初次 INSERT 该租户 snapshot 时值=2,见 `:578`)。
   *附带语义:* 单租户 binding 写会顺带 bump 所有 inherit_global 租户(无害但偏宽)。若要精确单租户,改用 alias_import 的单租户分支(`model_alias_import.go:219`)。**本计划接受 CTE 的略宽语义用于 INSERT/UPDATE**(与既有 model-sync 一致),DELETE 例外见下。

2. **DELETE(软删):** **不**用 CTE(A11 覆盖漏洞)。步骤:同 Tx 内先 `SELECT tenant_id FROM model_pool_bindings WHERE id=$ AND tenant_id=$`,再软删(set deleted_at),**再用单租户直 bump**:
   ```
   INSERT INTO model_registry_snapshots (tenant_id, version, reason, updated_by_actor)
   VALUES ($tenant, 2, $reason, $actor)
   ON CONFLICT (tenant_id) DO UPDATE SET version = version+1, ...
   ```
   这样即使删的是该租户该 model 最后一条存活 binding,version 仍 +1。

3. **原子性:** 所有 mutation + bump 在同一 `BeginTx(Serializable)`,要么全 commit 要么全 rollback。resolver 在独立 REPEATABLE READ ReadOnly Tx 读,观察到完整 bump 或完全旧值,无中间态。

### C.2 变异测试的判别式(钉死,防 W3a 弱测试)

L0 **无缓存**(noopCache),resolver 每次直查 DB——所以**不能**测"删 bump → 路由读到旧绑定"(无缓存层,resolver 照样读到新行,该断言天然不会红,是假护栏,A-review C1/M2)。

**唯一可判别的护栏:断言 `SnapshotVersion` 字符串里的版本数字递增。**

判别式样例(写进测试 spec):
```
1. resolve(tenant=42, alias=X) → Resolved.SnapshotVersion == "registry:42:7"   (写前)
2. InsertModelPoolBinding(tenant=42, model=mX, pool=p) commit
3. 用独立连接 resolve(tenant=42, alias=X) → SnapshotVersion == "registry:42:8" (v+1)   ← 断言
4. 变异:注释掉 bindings_admin.go 里的 bump 调用,重跑
5. 步骤 3 断言 SnapshotVersion 仍为 "registry:42:7" → 测试转红              ← 判别
```
DELETE 路径同款判别式:删最后一条 binding → 断言 v 仍 +1;去掉 DELETE 的单租户 bump → 红。
**禁用非判别写法:** 同连接/同 Tx 内写完紧接读 version(可能因可见性巧合通过);必须**独立连接**读 `GetTenantSnapshotVersion` 或经 resolver 拿 SnapshotVersion 字符串。

---

## D. 决策点(待 Owner 拍)

> 每个决策带【参考项目对照】(CLAUDE.md #15),引用真码 file:line。

### D1【升级为头号结构决策】handler 包 + auth 门 + 路由前缀(三件套绑定)

草案把这当实现细节,真码核实证明它是结构决策(A9):model admin 用 platform-only `adminGate`,proxyadminhttp 用双角色门,两套前缀并存。

- **D1-A(推荐):** controlhttp + **自建双角色门** + `/v1/admin/models/{id}/pool-bindings`(嵌在 model 资源下,对齐 model admin 路由前缀,前端 findings 也已假设此路径)。代价:要给 controlhttp handler 加 `Auth` + `resolveTenant`(model admin 现无,需自写,不是照抄)。
- **D1-B:** adminhttp/proxyadminhttp 家族 + 现成双角色门 + `/admin/v1/model-pool-bindings`(顶层资源)。代价:不对齐 model admin 家,与 §草案"对齐 alias"自相矛盾。

**为何必须双角色门而非 platform-only `adminGate`:** binding 是 tenant-scoped 资源,tenant_operator 应能管自己租户的绑定;platform-only 门会把 tenant_operator 全挡(`middleware.go:144`),功能缩水。

**参考项目对照:**
- **sub2api** `@<sha>`:channel→ModelPricing 是**顶层 admin CRUD**(`backend/internal/handler/admin/channel_handler.go:342-459`,`POST/PUT /api/v1/admin/channels[/:id]`),非嵌在 model 下。倾向顶层资源。
- **new-api** `@<sha>`:channel 也是**顶层**(`controller/channel.go:589-1047`,`POST/PUT /api/channel[/:id]`)+ 写后 `InitChannelCache()` 全量重建。同样顶层。
- **CLIProxyAPI** `@21fad9db`:config-file-centric,无 binding CRUD API(`internal/api/handlers/management/config_basic.go` 整文件 YAML),**无等价概念**——relay 项目,模型绑定走 credential config,不适用。

> 注:两个有等价概念的成熟项目(sub2api/new-api)都把"channel/model 绑定"做成**顶层资源**而非嵌套。这倾向 D1-B 的顶层 `/...model-pool-bindings`,但 D1-B 换了包家族与前缀。**Owner 需在"对齐 HUAKAI model admin 家(嵌套 /v1/admin/models)"vs"对齐成熟项目惯例 + 现成双角色门(顶层 /admin/v1)"间拍板。** PM 倾向 D1-A(前端已假设嵌套路径 + 不引入第三处路由家),但门要自建。

### D2【建议直接拍,省一查询】bump 复用,不新建 `BumpTenantSnapshotVersion`

草案 §1.1 列了新建 `BumpTenantSnapshotVersion`。真码已有 `bumpAffectedSnapshots`(`model_sync_writer.go:562`,registry 包内私有,同包 bindings_admin.go 可直接调)。**建议:INSERT/UPDATE 复用它;DELETE 用 alias_import 的单租户 ON CONFLICT 分支(`model_alias_import.go:219`)。不新建查询、不造两套 bump。**

**参考项目对照:**
- **sub2api** 写后 `invalidateCache()` + 级联 `invalidateAuthCacheForGroups()`(`channel_service.go:772-773`)——版本无关,靠 TTL+主动失效。
- **new-api** 写后 `InitChannelCache()` 全量重建(`model/channel_cache.go:22-87`)——无版本号。
- HUAKAI delta(生态升级):单调 version 计数 + REPEATABLE READ 点一致性,比两者更细(无需全量重建、无 TTL 窗口期)。

### D3【须拍】model picker 数据源

UI 要选 model。现有 `/v1/models` 是 user-track(Bearer/user 可见性过滤,`routes.go:112`),**无 admin 维度 model-list**。
- **D3-A:** 新增 `GET /v1/admin/models?tenant_id=`(handler+query+wiring+前端 client)——多一摊后端。
- **D3-B(降级):** UI 手输 model_id(无下拉),写明降级。
PM 倾向 D3-A 但承认增量;若要控本刀体积可选 D3-B。**参考:** sub2api 有独立 model 列表给 channel 编辑用(`channel_handler.go`);new-api channel 编辑也有 model 多选。两者都有 model picker → 倾向 D3-A,但可 follow-up。

### D4【须拍】孤儿 `app/bindings/page.tsx` 去留

它管 pool group(+ mock binding),名字 `bindings` 与新页 `/admin/models/bindings` 概念撞。
- **D4-A:** 本刀不动,标注 follow-up 清理。
- **D4-B:** 顺手接进侧栏(它是 pool group 管理,确为另一功能)。
PM 倾向 D4-A(本刀聚焦)+ 在 plan 留 follow-up 条目,避免两个 binding 概念在 UI 并存的困惑。

### D5【诚实标注,记录即可】selection_mode=priority_weighted

UI 暴露但灰标"加权执行未启用,当前按优先级"(A12)。**后端接受存储、执行未启用**——spec 须明说(避免审查误判为"已支持加权"或"功能缩水")。weight 字段在 strict_priority 下 UI 禁用/提示。

---

## E. 影响面(Blast Radius)

- **无新迁移**(表+列已存在)→ 无 schema gate,低风险。**但 A10 的 registry.sql 漂移修复触碰生成码 + resolver**,虽不改 schema 却改既有读路径行为 → 需独立验证(go build + 敏感词网关回归)。
- **改路由解析输入:** 绑定喂 resolver,仅 operator 真写时生效;不写=零行为变化(前提:A10 已修)。
- **新 admin 端点:** 双角色门 + **by-id WHERE tenant_id**(双层防越权,A-review R3)。复合 FK(`0041`)是第三层兜底。
- **codebudget:** controlhttp +1 文件、registry +1 文件均绿(A4)。
- **`bumpAffectedSnapshots` 略宽语义:** INSERT/UPDATE 顺带 bump inherit_global 租户(无害)。

---

## F. 会出什么错(What Could Go Wrong)

1. **忘 bump / bump 不判别**(头号)→ 护栏:§C 判别式变异测试(断言 SnapshotVersion 数字 +1,**不**测路由读新行)。
2. **sqlc generate 砸坏 sensitive_words 网关**(A10)→ 护栏:先修 registry.sql 源、独立 commit、go build + 网关回归绿后再加新查询。
3. **全局模型继承绑定被错谓词拒**(A7)→ 护栏:正谓词 + 全局模型写入回归测试。
4. **GET 复用路由查询隐藏 disabled/未来生效绑定**(A8)→ 护栏:新建 `ListModelPoolBindingsAdmin`。
5. **by-id 跨租户改删**(A-review R3)→ 护栏:三条 by-id 查询全 `WHERE id=$ AND tenant_id=$` + 越权 403/404 测试。
6. **DELETE 最后一条 binding 不 bump**(A11)→ 护栏:DELETE 用软删前抓 tenant + 单租户直 bump + 测试覆盖。
7. **POST 重复三元组 500**(B4)→ 护栏:唯一冲突映射 409(partial 索引已确保软删后可重建)。
8. **platform-only 门挡掉 tenant_operator**(A9)→ 护栏:用双角色门,不用 `adminGate`。
9. **effective 窗白扔**(A-review B3)→ 护栏:DTO/表单收 effective_from/until + `from<until` 422。
10. controlhttp 超 codebudget(余量足,不预期)→ 拆 `modelbindingadminhttp` 子包(#13)。

---

## G. 测试(汇总,见 B9/B10 + §C 判别式)

| 测试 | 它抓的回归(一句话) | 判别注入 |
|---|---|---|
| snapshot bump 变异 | 写绑定未 bump → 版本戳不前进 | 注释 bump → SnapshotVersion 数字不变 → 红 |
| DELETE bump | 删最后一条 binding 未 bump | 去掉单租户直 bump → 版本不变 → 红 |
| 全局模型继承绑定写入 | 错谓词拒掉 global model 绑定 | 用旧"tenant==tenant"谓词 → 写入失败 → 红 |
| by-id 跨租户 | operator A 改 B 的 binding 成功 | 写成 `WHERE id=$` 单条件 → 越权成功(应 403/404)→ 红 |
| tenant 越权 403 vs DB FK 500 | handler 门缺失,靠 DB 兜底 | 去掉 CanIssueForTenant → 500 而非 403 → 红 |
| 唯一冲突 409 | 重复三元组返 500 | 不映射 → 500 而非 409 → 红 |
| effective 窗校验 | from>=until 入库 | 去掉校验 → 422 不触发 → 红 |
| admin list 不过滤 | 复用路由查询隐藏 disabled 绑定 | 用 ListModelPoolBindings → disabled 行不返回 → 红 |

---

## H. 时间 / 顺序

0. **(前置)** 修 `registry.sql` 补 sensitive_words → sqlc generate → go build + 敏感词网关回归 → **独立 commit**(A10)。
1. 后端:admin-list 查询 + 4 写查询(全 tenant-scoped)→ sqlc generate → **go build 验 querier 接口闭合 + 现有 sql_filters_test 绿**(A-review B5)。
2. registry `bindings_admin.go`(正谓词 + Tx 内 bump,DELETE 单租户 bump)。
3. handler(D1 拍板后)+ wiring。
4. 后端测试(§G 全表,含判别式变异)。
5. 前端 client + 页 + nav。
6. 接线测试断言(真后端 E2E)。
7. `go build`/`vet`/`codebudget`/`tsc` 绿。
8. commit + push(独立分支 `feat/slice-model-pool-bindings`)。

**分支起点元数据(须动工前确认,A-review):** 当前在 `feat/frontend-portal`。草案 §6 说"off fix/h-fixes 取后端写路径"——需确认 `fix/h-fixes` 的后端写基础是否已并入 `feat/frontend-portal`,否则分支起点假设不成立。建议:off `feat/frontend-portal`(当前已有前端栈),后端从零写(本就零写路径,无需 cherry-pick)。

> **诚实结论:** 草案可做,但**不可按原稿开工**。先解决 5 条 S1(A7 谓词 / A8+R3 admin-list+by-id-scope / A9 门归属决策 / A10 sqlc 漂移 / A11 DELETE bump),把 §C 判别式与 §D1 结构决策钉死,再动手。前置步 H0(修 sensitive_words 漂移)是隐蔽但优先级最高的,先做。
