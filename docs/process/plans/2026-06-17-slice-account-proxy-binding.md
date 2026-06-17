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
