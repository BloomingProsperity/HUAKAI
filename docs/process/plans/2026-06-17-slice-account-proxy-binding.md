# 切片计划草案:账号↔代理绑定写路径(#2+#4)— 2026-06-17

**触发:** Owner 最早点出的缺口("代理该绑账号")。跨模块接线审计 #2≡#4(同一 proxy_id 写路径缺口)。当前:列(proxy_id@0038 / proxy_group_id@0124)+ resolver(postgres_proxy_resolver:账号单绑 > 组轮换 > 租户默认 > 直连)都在,但**账号 admin API/SQL/前端三处都不能设绑定**(inert gap)。
**分支:** feat/slice-account-proxy-binding(off feat/frontend-portal)。**风险:中**(加性、默认直连、不设=零行为变化;但触账号管理核心写路径,须谨慎)。
**状态:草案,待多角度硬化后再开工(账号核心敏感,照 bindings 刀的硬化范式)。**

## 0. 现状(已读真码核实)
- `sql/queries/admin_provider_account_mutations.sql`:`InsertProviderAccount`(~20 列,无 proxy_id/proxy_group_id)+ `UpdateAdminProviderAccount`(COALESCE-narg partial update)。走 **sqlc** → `db/admin`。
- `internal/gatewayhttp/admin_pool_accounts_handler.go`:create/update 请求 DTO 无 proxy 字段。
- 前端 `app/admin/accounts/page.tsx`:无 proxy 引用/选择器。
- 前端代理数据源:`adminCredentials.ts` 有 `listProxies`(proxies CRUD client);**代理组无专门 list 端点**(proxies 表有 group_id)。

## 1. 范围(Scope)
**后端:**
1. `admin_provider_account_mutations.sql`:Insert 加 `proxy_id`/`proxy_group_id`(`sqlc.narg`,可空);Update 加这两列。→ sqlc generate → **只留 db/admin,revert 其它包注释漂移**(已知 dance)。
2. handler create/update DTO 加 `proxy_id *int64` / `proxy_group_id *string`(指针区分 省略 vs 显式);param 映射;**proxy/组 归属校验**(属本租户,照 bindings 的 checkPoolGroupOwned 范式,给 422 而非 FK 500)。
3. resolver 已读这两列,不动。

**前端:**
4. 账号编辑/新建弹窗加"出站代理"**三档选择器**:直连 / 指定代理(proxy_id,下拉=listProxies)/ 代理组(proxy_group_id,见 D1 数据源)。
5. 账号列表加"代理"列(显示绑定)。

**测试(强,禁止假绿):** handler 测试(DTO 传参/归属校验 403-422/清除语义);registry/store 集成测试 gated;前端 tsc 真跑。

## 2. ⚠️ 已浮现的真坑(待硬化重点)
1. **清除绑定回直连的语义**:Update 用 COALESCE-narg "省略=保留"。要把账号从"绑代理"改回"直连",**不能靠省略 proxy_id**(会保留旧值)。需显式 clear 信号(如 DTO 带 `proxy_id: null` 显式 vs 省略,或一个 `clear_proxy` 标志,或前端选"直连"时发哨兵)。**这是头号设计点。**
2. **单绑 vs 组 互斥**:resolver 优先级 account proxy_id > group。选择器选"指定代理"应清 proxy_group_id,反之亦然 → 写时两列联动(设一个、清另一个)。
3. **sqlc regen-dance**:Insert/Update 改后 generate 会重写多个 db/* 包注释 → 只留 db/admin。
4. **归属校验**:proxy_id/proxy_group_id 必须属本租户(跨租户绑会泄漏 IP 隔离)。

## 3. 决策点(待硬化 + Owner)
- **D1 代理组数据源**:前端"代理组"下拉怎么来?(a)从 listProxies 取 distinct group_id 派生(无新端点);(b)新增 proxy-groups list 端点。倾向 (a) 省后端。
- **D2 清除语义**:三选一(显式 null / clear 标志 / 前端"直连"哨兵)——硬化定。
- **D3 列表"代理"列**:显示 proxy_id/组名 还是仅图标。

## 4. 参考对照(真 sha,本会话深挖)
- sub2api@e34ad2b:账号编辑表单 ProxySelector(`frontend/.../ProxySelector.vue` + account update 的 ProxyID 字段);account.proxy_id 单绑 1:1。
- new-api@1ac0f58:per-channel 单 proxy(channel.setting JSON)。
- CLIProxyAPI@21fad9db:per-credential proxy-url(config)。
- **三家共识:绑定在账号/渠道侧的编辑表单设。HUAKAI delta:proxy_group_id 按账号确定性轮换(住宅 IP 隔离),三家皆无。**

## 5. 顺序
0. 多角度硬化本草案(找 S1)。1. SQL+regen+revert。2. handler DTO+校验+清除语义。3. 后端测试。4. 前端选择器+列。5. tsc。6. commit+push。

---

## 6. 硬化修正(对抗 agent 1·正确性·安全,真码 file:line)

**S1-1 清除语义:草案"0-哨兵"推翻 → 改 Set-flag + nullable 指针。**
- HTTP JSON 层 `null`/省略都映射 Go `nil`,指针单独无法区分"省略=保留"vs"清空=NULL"。
- 正解照 HUAKAI 现有 `SetProbeModel` 范式(`admin_provider_accounts.go:276`):
  `proxy_id = CASE WHEN $SetProxyID::boolean THEN $ProxyID::bigint ELSE proxy_id END`
  Go params:`SetProxyID bool; ProxyID *int64; SetProxyGroupID bool; ProxyGroupID *string`。

**S1-2 Update 是手写 SQL(非 sqlc)。** `admin_provider_accounts.go:269-324` 的 `UpdateAdminProviderAccount` 是手写 SQL 常量 + params 结构;`mutations.sql` 只有 Insert/Delete。→ **Update 直接改手写常量+params,无 sqlc regen**;**只有 Insert 走 sqlc dance**(mutations.sql 加 narg + generate + revert 非 db/admin 漂移)。

**S1-3 互斥:resolver 靠应用层。** `postgres_proxy_resolver.go:168-185`:`!hasProxyBound && proxyGroupID!=""` 才走组。→ handler:选"指定代理"=SetProxyID(N)+SetProxyGroupID(nil);选"代理组"=SetProxyGroupID(x)+SetProxyID(nil);选"直连"=两者 Set 为 nil。

**S1-3b proxy_group_id 无 FK(text 标签)。** proxy_id 有 DB 触发器 `enforce_provider_account_tenant_alignment`(`0038:194-225`)保证同租户;proxy_group_id 无 → handler 必须显式校验 `SELECT 1 FROM proxies WHERE tenant_id=$ AND group_id=$ AND deleted_at IS NULL`,查不到 422(防跨租户/空组)。

**确认(无需改):** Insert 用 narg → 不传=NULL=直连,零回归;Update Set-flag=false → 保留旧值,零回归。sqlc dance 是已知工作流(提交前 `git diff backend/internal/db/` 查漂移)。

**D2 决策定:** 清除语义 = Set-flag(不用 sub2api 的 0-哨兵,因 HUAKAI JSON 层不适配)。

## 7. 硬化修正(对抗 agent 2·完整性·前端)—— 揭示切片远比草案大 + S0 安全洞

**S0-A 前端账号页【无编辑表单】。** `app/admin/accounts/page.tsx` 行动作只有 test/启停/清冷却/健康快照,**无编辑入口、无弹窗、无代理列**。→ 本刀要**从零建账号编辑弹窗**(三档代理选择器)+ 列表加"代理"列 + 编辑按钮。工作量远超"加选择器"。

**S0-B 互斥失守 = IP 隔离泄漏。** resolver(`postgres_proxy_resolver.go:96-202`)两列都有值时按优先级选 proxy_id 忽略组 → 破坏三档语义。DB 无互斥 CHECK。→ (1) handler 强制互斥(设一列清另一列 + 拒同时显式设两列 400);(2) **建议加 migration CHECK 约束**(= schema 变更,**高危需 Owner gate**);(3) 前端选择器单选联动。

**S1-C account list 不含 proxy 列。** `AdminProviderAccountRow`(`admin_provider_accounts.go:9-47`)+ 列选择器 + scan 均无 proxy_id/proxy_group_id → 列表显示代理列会 N+1。→ Row 加两列 + adminProviderAccountColumns 加 + scanAdminProviderAccount 加。

**S1-D 代理组无 list 端点。** proxies 有 group_id 列但无 list-groups 端点 → 前端从 listProxies 派生 distinct(memo 防闪)。D1 定:派生(a),代理多时承认成本(可后续加 ListProxyGroups 端点优化)。

**测试判别式(禁止假绿,给例):** handler:SetProxyID→另列清空;清除→直连;同时设两列→400;跨租户 proxy_id→trigger 500/或 handler 403;跨租户 group→422。resolver 集成:绑定 proxy healthy→用、unhealthy→ErrProxyUnhealthy(不降级!)、组确定性轮换(同账号粘同 IP)、空组→fail-closed、租户默认 fallback、无绑定→直连。

## 8. ⛔ 待 Owner 决策(本刀升级为 S0-安全 + schema 高危,建议 Owner 先批)
- **D2 清除语义**:Set-flag(params 层,前端发显式 null/哨兵)—— 定哪种请求表示。
- **D-schema**:是否加 `0125 account_proxy_mutual` CHECK 约束(schema 变更高危)?还是仅 handler 层互斥?
- **D-scope**:账号编辑弹窗从零建(本刀只做代理三档,还是顺带把账号其它可编辑字段也补进弹窗)?
- 风险定级:本刀触**账号管理核心写 + IP 隔离安全 + 可能 schema 迁移** → 按 Risk-Based Confirmation Rule 属高危,**Owner 确认后再开工**。
