# HUAKAI role 制单登录迁移计划(auth-core + schema)

> 状态:**草案,Owner 门控**。本文只做设计与分阶段拆解,不含实现代码。
> 作者:首席架构师 · 日期:2026-07-01
> 前置事实已亲核真码(见文末「源码核验」),四镜研究见附录 §7。

---

## ★ 决策定案(2026-07-01 Owner 拍板,覆盖下文草案中不一致的推荐)

1. **D1 = 硬切,砍掉 admin token 粘贴**(不采用草案的"双接受长期并存")。终态:部署者正常登录即管理员,登录页不再有令牌粘贴区,对标 new-api 纯 role 制。实现仍分阶段:P0 组合解析器(session 分支 knob 默认关)→ 灰度验证 → 翻 knob → 最后移除令牌粘贴通道。admin_tokens 后端可保留作纯 programmatic/内部(Hermes 等)通道,但不作运营者登录方式、不在 UI 暴露。
2. **D3 = 平台级全权超管**:admin = 部署者本人,拥有平台所有权限。
3. **首个 admin 引导 = 管理员邮箱 env**(方案 A):部署时填 `HUAKAI_ADMIN_BOOTSTRAP_EMAIL`,该邮箱账号登录即被认定全权 admin;复用现有 advisory-lock + 幂等 no-op + 陈旧 env 不崩契约;不做弱口令自动建号。
4. **D6 = 加 step-up 二次校验**:令牌通道砍掉后 session 是唯一 admin 凭据,充值/凭证 KEK/签发凭据等 A 级危险操作 session 通道强制二次输密码;只读不受影响。
5. **范围**:本 arc 只做顶层部署者 admin(users.role 二值)。多租户"二级管理员/代理商"是独立后续 arc(D2 多级角色本次不做)。

---

## ★ P1 scope 审计结果(2026-07-01,读真码,重塑 P2 设计)

已走查全部 `AdminIdentity` 消费点,session-admin 映射成 `{Role:platform_admin, TokenID:0, ScopeTenantID:0}` 的两个真隐患:

1. **审计误归属(必修)**:~15 处 handler 用 `ident.TokenID` 做审计归属(`ActorID: fmt(ident.TokenID)` / `admin_token:%d` / routeadmin `AdminID`,遍及 balance_credit/api_keys/provider_catalog/channel_catalog/provider_account_bulk/platformsettings/model_sync/routeadmin/notify 等)。session-admin TokenID=0 → 全记成"token 0",真实操作用户丢失。
2. **潜在 FK 破裂(必修)**:迁移 `0144_hermes_admin_actor` 的 actor 列带 `REFERENCES admin_tokens(id)` 外键;session-admin 插 `admin_token_id=0` → 外键违约、操作崩。(其余 `*_admin_id` 为纯 bigint 无 FK,只误归属不崩。)
3. **Role== 检查**:session-admin=platform_admin,全部 `!=RolePlatformAdmin`/`requirePlatformAdmin` 检查通过(符合 D3 全权);`provider_catalog:165` 要求 tenant_operator 的分支是"无 ?tenant_id 时用 ScopeTenantID",platform_admin 走显式 ?tenant_id,不受影响。**签发/吊销 admin token 走 requirePlatformAdmin → session-admin 能签发 → 属 P3 step-up 必盯的高危操作。**

**P2 设计据此调整**:`AdminIdentity` 增 `Source`(token/session)+ session 时的 `UserID`;审计归属改走统一方法(如 `ident.AuditActor()` 返 "admin_token:N" / "admin_user:N"),迁移 ~15 call site;带 FK 的 Hermes actor 列对 session-admin 走可空/另存用户列。**未处理这两点前,knob 绝不能翻开(会误归审计 + Hermes 动作 FK 崩)。**

---

## 0. 一句话背景与本计划的边界

HUAKAI 今天是**两套物理隔离的身份系统**:

- **用户系统**:`users` 表 + 签名 session(`usersession` + `auth.SessionMiddleware`),身份 `SessionIdentity{TenantID, UserID, FamilyID}`,**不带 role**,面向 `/v1/*` 用户端点。
- **运维系统**:`admin_tokens` 表 + bcrypt 校验(`hk_admin_` 前缀,`admin.AdminResolver.Resolve`),面向 ~170 个 `/admin/v1` + `/v1/admin` 端点,**每个 handler 顶部显式 `d.Auth.Resolve(...)`**(非中间件,唯二例外=Hermes 中间件 + 全局 ops 面 `adminGate`)。
- **第三维** `users.role`('admin'/'user',迁移 0076):当前**只决定登录后进哪个面板 UI**(`/v1/auth/me` → `panelauth.PanelForUser`),**完全不授予 admin API 权限**——role=admin 的 session 现在打 `/admin/v1` 仍被 `operator_auth.go:58-61` 以「非 hk_admin 前缀」拒掉。

Owner 选定方向 = 对齐 sub2api/new-api 的 **role 制单登录**:一次登录、admin 是用户账号上的一个角色、登录后按角色显示用户台或运营台,**并且 admin-role 的 session 能真正鉴权 admin 端点**。本计划就是把「第三维只管 UISkin」升格为「第三维管真授权」的 auth-core + schema 迁移。

**本计划刻意不改动的**:relay 热路径鉴权(`hk_live_`/`hk_test_` 客户 key)、CMB-1 契约(热路径 resolver 禁碰 admin_tokens)、支付/凭证 KEK 等 A 级 money 逻辑。

---

## 1. 目标模型(Target Model)

**单一账号 + 角色驱动授权**。一个人只有一个 `users` 账号、一次登录、一个 session。账号上的 `role` 列同时决定两件事:

1. **前端面板归属**(已有能力,不变):role=admin → 运营台壳,其余 → 用户台壳。
2. **admin 端点授权**(新增能力,本迁移的核心):role=admin 的**已验证 session** 被 admin 端点接受为合法 admin 身份,等价于今天持 `hk_admin_` token 的 `platform_admin`。

关键设计约束(全部照搬 HUAKAI 现有 fail-closed 语义):

- **deny-by-default**:`PanelForRole` 已经是「仅精确 =='admin' 才授管理面板,一切其他值(空/污染/未来新值/大小写不符)→ user」。授权层必须复用**同一个** `role == RoleAdmin` 精确比较,绝不用「!= user」这种反向判断。
- **admin_tokens 体系保留,不删**(见 §3)。programmatic/API/CI 访问继续用 `hk_admin_` token;session 授权是**并列新增的第二条通道**,不是替换。
- **身份仍取自服务端已验证凭据**,绝不信前端声明的 role(沿用 `panelauth_handler.go:94-95` 的既有铁律)。
- **tenant scope 语义保留**:HUAKAI 是多租户(`UserRole` 查询按 `tenant_id + user_id`)。新增的 session-admin 默认对齐 `platform_admin`(全租户)还是 `tenant_operator`(限本租户)是一个 **Owner 决策点**(见 §8-D3)。默认建议:session-admin 映射为**平台级 admin**,因为当前单运营者场景只有 tenant0 系统租户下的运营者;多级代理角色树是独立的后续 arc。

---

## 2. Schema 迁移:`users.role`

现状(迁移 0076,已上线)已经给了我们一个可用的列:

```sql
ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'user'
    CHECK (role IN ('admin', 'user'));
```

老数据自动是 `'user'`(不会误变 admin),这一步**已经做完且正确**,迁移时无需重建。

本迁移在 schema 层需要的**增量**:

**2.1 是否扩枚举?** 当前 CHECK 只有 `('admin','user')`。两个决策点:
- **保持二值**(推荐 MVP):对齐 sub2api 的极简双值模型,单运营者够用。admin=平台运营者,user=终端用户/员工。
- **若 Owner 要「平台超管 vs 租户运营者」两级 admin**:扩为 `CHECK (role IN ('platform_admin','tenant_operator','user'))` 或加独立的 `admin_scope` 语义,并把 0010 的 `scope_tenant_consistency` CHECK 思路(platform→scope NULL / operator→scope 非 NULL)平移到 users 层。**这是 Owner 决策点 D2**,不建议 MVP 阶段做,会显著放大爆炸半径。

**2.2 回填**:0076 的 `DEFAULT 'user'` 已保证现有用户全部是普通角色。迁移时**唯一要显式回填的**是「把当前运营者本人的 users 行置为 admin」——但这属于 §4 的 bootstrap 演进,不在 schema DDL 里硬编 UPDATE(硬编 email/id 是启动脆性来源,呼应记忆里 tenant0 卡启动的教训)。

**2.3 不动的**:`admin_tokens` 表及其 `scope_tenant_consistency` CHECK 原样保留(§3 决定保留 token 通道)。**本迁移在 schema 层是加法,不删任何列/表**——这是可回滚性的关键。

**2.4 审计列(建议)**:考虑加 `users.role_updated_at timestamptz` + `role_updated_by bigint`,让「谁在何时把某账号提成 admin」在 schema 层留痕(呼应爆炸半径 §6 的审计要求)。这是可选增强,可放到 §8 后期阶段。

---

## 3. 鉴权改造:170 admin 端点从「只认 token」到「也认 admin-role session」

### 3.1 过渡策略:**双接受(dual-accept),不硬切**

**强烈建议双接受,理由充分**:

- 硬切会瞬间作废所有现存 `hk_admin_` token、所有用 token 的脚本/CI/Hermes 中间件、以及 bootstrap 流程,任何一处遗漏 = 运营者把自己锁在门外(HUAKAI 有过「新库永久拒启」的惨痛先例)。
- 双接受让新旧通道并存,可以**先上线 session 通道、观察审计、再决定是否收敛 token**,每一步可独立回滚。

### 3.2 统一 admin 身份解析器(核心改造)

今天有两个不相通的解析器:`AdminResolver.Resolve`(认 token)和 `SessionMiddleware` + `panelauth`(认 session 但不授权 admin)。改造引入一个**组合解析器** `AdminAuthResolver`,按顺序尝试两条通道,**任一成功即得到统一的 `AdminIdentity`**:

```
AdminAuthResolver.Resolve(ctx, req) →
  1. 若 Authorization 是 hk_admin_ 前缀 → 走既有 AdminResolver.Resolve
       → 成功返回 AdminIdentity{Source: token, Role, ScopeTenantID}
  2. 否则若存在有效 session bearer → SessionMiddleware.Validate
       → 取 users.role(复用 panelauth.PostgresRoleStore.UserRole)
       → 仅当 role == RoleAdmin: 返回 AdminIdentity{Source: session, Role: platform_admin(映射), ScopeTenantID: 映射}
       → role != admin: ErrAdminUnauthorized(deny-by-default,反枚举)
  3. 两者皆无/皆败 → ErrAdminUnauthorized(统一,反枚举)
  4. 后端故障 → ErrAdminBackend → 503(不误报 401,沿用既有语义)
```

**关键设计点**:
- `AdminIdentity` 结构体加一个 `Source`(token/session)字段,供审计区分「这次 admin 操作是 token 还是 session 发起」。这对爆炸半径追责至关重要(§6)。
- **前缀第一道拒**继续有效:客户 key `hk_live_`/`hk_test_` 在 token 分支被拒,session 分支要求的是签名 session bearer(无 hk_ 前缀),两条通道各自的凭据空间隔离不变。
- session 分支**必须复用** `panelauth` 的 `role == RoleAdmin` 精确匹配 + deny-by-default,不新写判断逻辑(避免两处逻辑漂移,呼应记忆里「镜像对照三层」教训)。

### 3.3 注入点:改 `d.adminAuth` 而非逐个 handler

**这是本迁移最省事、爆炸半径最可控的接线方式**:170 个 handler 都是 `ident, err := d.Auth.Resolve(r.Context(), r)`,而 `d.Auth` 在 routes.go 里统一由 `d.adminAuth`(即 `*admin.AdminResolver`)注入 ~30 个路由模块。**只要把 `d.adminAuth` 从 `*admin.AdminResolver` 换成新的组合 `AdminAuthResolver`(实现同一个 `Resolve` 接口),170 个 handler 一行不改就同时获得双接受能力。**

- Hermes 中间件(`hermeshttp.AdminAuthMiddleware(d.adminAuth)`)和全局 ops 面 `adminGate(resolver, ...)` 也吃同一个 `d.adminAuth`,自动一并升级。
- **陷阱(必须逐个核对)**:很多 handler 在 `Resolve` 之后紧接着做 tenant scope 解析(如 `provider_account_tenant_resolve.go` 强制 platform_admin 必带 `?tenant_id=N`),以及 `adminGate:170` 强制 `Role == RolePlatformAdmin`。session-admin 映射成什么 Role 会直接影响这些下游判断——**§8 里必须有一个专门的「scope 一致性审计」切片**,逐个走查所有 `id.Role ==`/`CanIssueForTenant`/`?tenant_id` 消费点,确认 session-admin 的 Role 映射不会误放行或误拒。

### 3.4 `admin_tokens` 体系去留:**保留,作为 programmatic 通道**

- **保留理由**:①CI/脚本/外部集成用 Bearer token 远比模拟浏览器 session 干净(sub2api 也保留 x-api-key 后台密钥通道并列 JWT);②`hk_admin_` token 是 bootstrap 的载体(§4);③签发 token 本身是高权操作(`requirePlatformAdmin`),保留它等于保留一条不依赖浏览器 session 的运维逃生通道。
- **收敛(可选后期)**:上线 session 通道并观察一个稳定期后,Owner 可决定是否把 token 通道从「任何 admin 都能用」收敛为「仅 programmatic/服务账号用」。这是 §8-D4 决策点,**不在本迁移强制范围**。

---

## 4. 首个 admin 引导:`HUAKAI_ADMIN_BOOTSTRAP_TOKEN` 演进

现状 bootstrap(`bootstrap.go:MaybeBootstrap`)= 在 `admin_tokens` 表为空时、持 advisory lock、插一行 `bootstrap=true` 的 platform_admin token。这套「env 非空 **且** 表为空才插、表非空即 no-op、陈旧 env 不崩启动」的启动韧性契约**必须照搬**(HUAKAI 踩过启动门的坑,sub2api 的 `decideAdminBootstrap` 也是同款「空库才建、已有则拒」幂等哲学)。

演进目标 = 让 bootstrap 除了(或转为)**把某个 users 行置为 role=admin**,而不只是造 token:

**方案 A(推荐,增量最小):token 与 role 并行 bootstrap**。保留现有 token bootstrap 不动(逃生通道)。**新增**一个 `HUAKAI_ADMIN_BOOTSTRAP_EMAIL`(或 user_id)env:启动时若该 env 非空 **且** 当前 tenant 下无任何 role=admin 用户,则在同一 advisory-lock TX 内把该 email 对应 users 行 `UPDATE ... SET role='admin'`。幂等判定复用 sub2api 决策哲学:
- 无该 email 对应用户 → 记 warn 日志,**不崩启动**(呼应「陈旧 env 不崩」)。
- 已存在 role=admin 用户 → no-op(不覆盖,防重复初始化事故)。
- 命中且当前无 admin → 提升,写审计。

**方案 B(更激进,MVP 后再议):安装向导**。对齐 new-api/sub2api 的一次性 setup 页/CLI,首启无 admin 时引导创建首个 admin 账号。更友好但引入新的未认证启动态端点,爆炸半径更大,**建议 MVP 用方案 A,向导作为 §8 后期可选切片 + Owner 决策 D5**。

**红线**:绝不保留 new-api 那种「空库自动建 root/123456 弱口令」的遗留路径(该项目自己都已弃用不调用)。bootstrap 必须走 env 显式指定 + 幂等 no-op。

---

## 5. 前端:登录响应带角色 + AppShell 按角色切壳

现状:`/v1/auth/me` 已返回 `meResponse{Panel, UserID, TenantID, DisplayName}`,前端已能据 `Panel` 字段路由。本迁移前端侧主要是**补上此前推迟的「按身份切壳」并去掉 admin token 粘贴区**:

**5.1 登录响应带角色**:`/v1/auth/me` 的 `Panel` 字段已经就是角色驱动的产物,**无需改后端契约**。前端只需消费 `panel === "admin"` 决定加载运营台壳(对齐 sub2api 的 `requiresAdmin` meta + 全局路由守卫、new-api 的角色驱动侧栏)。

**5.2 AppShell 按角色切壳**(补此前推迟项):
- 单一登录入口(不做分离门户),登录成功 → 调 `/v1/auth/me` → 据 `panel` 落地:admin → 运营台,user → 用户台。
- 全局路由守卫:未认证访问受保护路由 → 跳登录存回跳地址;已认证访问 `/login` → 按 panel 分流(借鉴 sub2api `setupRedirect`);要求 admin 的路由但当前 panel=user → 打回用户台。
- **前端裁剪仅为体验,不是授权边界**(new-api、sub2api 两镜都明确强调):真正的安全边界是 §3 的后端 `AdminAuthResolver`。前端隐藏运营台入口 ≠ 授权,后端每个 admin 端点仍独立校验。

**5.3 去掉/弱化 admin token 粘贴区**:登录页当前那个「粘贴 hk_admin_ token」区块在 session 通道上线后对普通运营者是多余的(登录即得 admin session)。
- 主入口改为账号密码/社交登录。
- token 粘贴区**降级为「高级/programmatic 访问」折叠项**(不删,保留逃生通道的可用性),或移到运营台内的「API 访问凭据」页。
- 呼应记忆里的坑:`/v1/hermes/*` 与 `/admin/*` 前端鉴权路径不同(`tokenForPath` 只认 `/admin` 前缀,`/v1/hermes` 回落 session 恒 401 需显式带 admin Bearer)。session-admin 上线后要重新走查前端 `tokenForPath`,确保 admin session 能正确带凭据打通所有 admin 前缀——**这是前端切片必须包含的验证项**。

---

## 6. 爆炸半径与安全风险(本计划最重要的一节)

**核心风险陈述**:今天 admin 权限由一条窄通道(`hk_admin_` token,bcrypt,反枚举,前缀硬隔离)把守,凭据空间与用户 session **物理隔离**。本迁移**主动打通这道隔离**——让用户 session 在 role=admin 时也能鉴权 admin 端点。这意味着:

> **一旦 `AdminAuthResolver` 的 session 分支有任何 bug(role 读错/污染值被误判 admin/session 校验被绕过/tenant scope 映射错位),爆炸半径 = 170 个控制计费、凭证 KEK、配额、账号池、余额充值的端点全部暴露给一个本不该有 admin 权限的普通用户 session。** 这是把「session 层的任何漏洞」直接升级为「平台级 admin 越权」。

具体高危点:
- **role 读取污染**:`UserRole` 返回污染值时,若判断逻辑写成「!= 'user' 即 admin」就会误授。**缓解**:强制复用 `role == RoleAdmin` 精确匹配 + deny-by-default(§3.2)。
- **tenant scope 错位**:session-admin 映射成 platform_admin 后,`?tenant_id` 强制、`CanIssueForTenant`、`adminGate` 的 Role 判断可能被绕过或误放。**缓解**:§8 专设 scope 一致性审计切片,逐点走查。
- **session 生命周期弱于 token**:session 有 generation/drift/family 机制但通常比 programmatic token 更「长命」且暴露在浏览器(XSS/CSRF 面)。admin session 被盗 = admin token 被盗的同等后果,但攻击面更大。

**缓解措施(建议纳入,部分可 Owner 门控分级)**:

1. **审计强制区分 Source**:每个 admin 操作审计记录必须带 `Source: token|session` + user_id/token_id,让「谁用什么通道做了什么」可追溯(`AdminIdentity.Source` 字段,§3.2)。
2. **敏感操作二次校验(step-up)**:对 A 级 money/凭证操作(余额充值、凭证 KEK、签发 admin token、改配额)在 session 通道上要求二次确认(重输密码/2FA)。token 通道可豁免(programmatic)。**这是把爆炸半径从「170 全暴露」收敛到「170 可读 + 高危需 step-up」的关键护栏**。是否 MVP 就上 step-up 是 Owner 决策 D6。
3. **session 降权/短时效**:admin session 可用比普通 user session 更短的 TTL + 更严的 drift 检测。
4. **签发 admin token 仍限 token 通道或 step-up**:`requirePlatformAdmin` 守卫的高权操作(签发/轮换 admin token)不应被一个普通 admin session 无摩擦触发。
5. **灰度**:双接受让 session 通道可先对**只读 admin 端点**开放、观察审计、再逐步放开写端点(而非一次性 170 全开)。
6. **前端裁剪不当授权**:再次强调后端每端点独立校验,前端隐藏 ≠ 安全。

**审查门槛**:本迁移的每个鉴权切片必须过 §14 变异 + 对抗审查零 S0/S1(HUAKAI 既有标准),且鉴权相关切片建议做**完整矩阵测试**(role × source × endpoint-class 全遍历),不抽样。

---

## 7. §15 参考对照表(关键决策 × 三镜 file:line × HUAKAI delta)

| 决策维度 | sub2api | new-api | HUAKAI 现状 & 目标 delta |
|---|---|---|---|
| **角色字段设计** | `users` 上单字符串列(≤20),默认 'user',取值 admin/user;常量 `domain/constants.go:14-17`;判定 `service/user.go:66-68` | `users` 上单整型等级 0/1/10/100,常量 `common/constants.go:188-192`,校验 `194-196`,判定 `model/user.go:724-735` | 已有 `users.role text CHECK('admin','user') DEFAULT 'user'`(迁移 0076)。**Delta**:字段已存在且正确,MVP 保持二值(对齐 sub2api);扩多级 = Owner D2。绝不用整型等级(HUAKAI 是命名角色 + 多租户,阈值模型不适配)。 |
| **admin 端点放行** | 管理路由组统一挂一个 `admin_auth` 中间件,x-api-key 常量时间比对 **或** 角色化 JWT(带 token 版本防改密复用)`middleware/admin_auth.go:27-204`,角色闸门 `191-194` | 统一鉴权助手带「最低等级」入参 `middleware/auth.go:36-167`,门槛比较 `:131`,三档便捷入口 `:180-196`,审计内聚进鉴权链 `:159-166` | **per-handler** `d.Auth.Resolve`(~170 处,~30 路由模块),非中间件;`adminGate` 强制 platform_admin。**Delta**:不改 handler,改注入的 `d.adminAuth` 为组合 `AdminAuthResolver`(双接受 token+session);借鉴 sub2api「双通道并列」+ new-api「审计内聚鉴权链」;保留 HUAKAI 反枚举 `ErrAdminUnauthorized` + `ErrAdminBackend`→503 语义。 |
| **first-admin bootstrap** | 独立 setup 流程,`decideAdminBootstrap` 空库才建/已有则拒 `setup/setup.go:128-145`;env 自动安装 `AutoSetupFromEnv` 读 `ADMIN_EMAIL/PASSWORD` `:539-570`;常规注册永不建 admin | 一次性向导 `controller/setup.go:54-130`;遗留「空库自动建 root/123456」`model/main.go:68-89` **已弃用不调用** | env `HUAKAI_ADMIN_BOOTSTRAP_TOKEN` + advisory-lock + 表空才插 + 陈旧不崩 `bootstrap.go:48-116`。**Delta**:方案 A 新增 `BOOTSTRAP_EMAIL` 把某 users 置 admin,复用 sub2api「空库才建/已有则拒」幂等哲学 + HUAKAI 现有 lock/no-op 韧性;**照搬 new-api 的教训:绝不保留弱口令自动建号**。向导式 = Owner D5。 |
| **登录/UI 切壳** | 单入口 Vue SPA,`requiresAdmin` meta + 全局守卫按角色重定向 `router/index.ts:721+`,`setupRedirect.ts:1-7`;管理端 DTO 与用户端 DTO 严格分离(防字段越权读) | 单入口,角色写进 session 回前端 `controller/user.go:130-157`;后端下发按角色侧栏 `model/user.go:103-152` + 前端二次过滤 `use-sidebar-view.ts:48-57`(前端裁剪仅体验) | `/v1/auth/me` 已返回 `Panel`,前端已能路由。**Delta**:补「按 panel 切 AppShell 壳」+ 弱化 admin token 粘贴区;借鉴两镜「前端裁剪≠授权,后端每端点校验」;借鉴 sub2api「管理/用户 DTO 分离防 IDOR」(HUAKAI 已有 userView/adminView 之分,继续守)。 |
| **凭据隔离/前缀** | x-api-key(后台)/ JWT(用户)双空间 | session/access-token 单空间 + 等级 | `hk_admin_` / `hk_live_`/`hk_test_` / 签名 session 三空间硬隔离,前缀第一道拒 `operator_auth.go:58-61`。**Delta**:原样保留,session-admin 是**并列新增第二条 admin 通道**,不混用前缀。 |

---

## 8. 分阶段落地(每阶段一个可独立合并的切片 + 门禁)

原则:**每个切片 knob-default-off 或纯加法**,合并到主线时零生产行为变更,直到 Owner 拍板翻开关。每阶段过 §14 变异 + 对抗审查零 S0/S1 + 集成 PG 测试 + quality-gate。

| # | 切片 | 内容 | 独立可合并性 | 门禁/开关 | Owner 决策点 |
|---|---|---|---|---|---|
| **P0** | 组合解析器骨架 | 新增 `AdminAuthResolver`(实现 `Resolve` 接口),内部先只包一层现有 `AdminResolver`(session 分支存在但 **default-off**,恒走 token)。加 `AdminIdentity.Source` 字段 | 纯加法,行为不变 | `HUAKAI_ADMIN_SESSION_AUTH_ENABLED`(默认 off) | — |
| **P1** | scope 一致性审计 | 逐点走查所有 `id.Role ==` / `CanIssueForTenant` / `?tenant_id` / `adminGate` 消费点,确定 session-admin 的 Role/scope 映射,补测试 | 只读/测试,可独立 | — | **D3**:session-admin 映射 platform_admin 还是 tenant_operator |
| **P2** | session 分支接线(只读端点先行) | 打开 session 分支,但灰度**仅对只读 admin 端点**生效;写端点仍只认 token。审计带 Source | 加法,knob 控制范围 | 同 P0 knob + 只读白名单 | **D1**:硬切 vs 双接受(建议双接受,此处定案) |
| **P3** | 写端点放开 + step-up 护栏 | session 分支放开写端点;A 级 money/凭证操作加 step-up 二次校验 | 依赖 P2 | knob + step-up 开关 | **D6**:step-up 是否 MVP 必须 |
| **P4** | bootstrap 演进 | 新增 `BOOTSTRAP_EMAIL` 把某 users 置 admin(方案 A),复用 lock/no-op 韧性 | 加法 | env 非空才生效 | **D5**:方案 A vs 安装向导 |
| **P5** | 前端切壳 + 弱化 token 区 | AppShell 按 `panel` 切壳,全局守卫,token 粘贴区降级为高级项;走查 `tokenForPath` | 前端独立 | 可跟随后端 knob | — |
| **P6(可选)** | token 通道收敛 | 观察稳定期后,决定是否把 token 通道收敛为仅 programmatic | 加法/配置 | — | **D4**:是否收敛 token |
| **P7(可选/后期)** | 多级角色 | 若 Owner 要平台超管 vs 租户运营者两级 admin,扩 CHECK + scope 一致性 | schema 迁移 | — | **D2**:是否扩多级(不建议 MVP) |

**Owner 必须拍板的决策点汇总**:
- **D1 硬切 vs 双接受**(本计划强烈建议双接受)——最关键。
- **D3 session-admin 的 tenant scope 映射**(platform 级 / 租户级)——影响 §3.3 所有下游 scope 判断。
- **D6 step-up 二次校验是否 MVP 必须**——直接决定爆炸半径收敛程度。
- **D5 bootstrap 方案 A(env 提升)vs 向导**。
- **D4 token 通道是否收敛**(后期)。
- **D2 是否扩多级角色**(后期,不建议 MVP)。
- 翻 `HUAKAI_ADMIN_SESSION_AUTH_ENABLED` 激活(生产切换点)。

---

## 9. 诚实的工时/风险评估

**工时(纯后端 auth-core + schema,不含前端,单人 + 对抗审查)**:

- P0 组合解析器骨架:**~1 人日**(纯加法,接口包一层)。
- P1 scope 一致性审计:**~1.5~2 人日**(~30 路由模块逐点走查是主要成本,不能抽样)。
- P2 只读 session 分支:**~1.5 人日**(含灰度白名单 + 矩阵测试)。
- P3 写端点 + step-up:**~2~3 人日**(step-up 是新机制,含 2FA/密码二次校验接线)。
- P4 bootstrap 演进:**~1 人日**(复用现有 lock/no-op)。
- P5 前端切壳:**~1.5~2 人日**(含 `tokenForPath` 走查,踩过的坑)。

**合计后端 auth-core ~7~9.5 人日,加前端 ~1.5~2 人日,总 ~9~11.5 人日**(不含 D2 多级角色 / D6 若加重的 step-up UX)。

**风险评级**:

- **最高风险 = §6 爆炸半径**。这是本迁移与「加个只读工具」的本质区别:它**主动拆掉一道现存的物理凭据隔离**。任何 session 分支的鉴权 bug 都是 S0/S1 越权。**缓解 = 双接受灰度 + step-up + Source 审计 + 矩阵测试 + 对抗审查**,但残余风险不可能归零,session 层攻击面天然大于隔离的 token 层。
- **中风险 = scope 映射错位**(P1),多租户下 session-admin 的 Role 映射若与 `?tenant_id` 强制/`CanIssueForTenant` 不一致,会误放行或误拒。P1 单独成切片就是为压这个风险。
- **中风险 = bootstrap 启动脆性**。HUAKAI 有过「新库永久拒启」惨痛先例;P4 必须照搬「陈旧 env 不崩」契约,严禁在 DDL 里硬编 email UPDATE。
- **低风险 = 前端**(纯体验层,非授权边界),但 `tokenForPath` 有历史坑,需走查。

**建议**:P0/P1/P2(只读)可先做,给 Owner 一个**只读 session-admin 已验证 + 审计可观察**的稳态,再由 Owner 拍板 D6/D1 是否放开写端点。写端点 + step-up(P3)是真正动 money/凭证爆炸半径的一步,不应在 Owner 明确授权 step-up 策略前翻开关。

---

## 源码核验(本计划所依赖的 HUAKAI 事实,均本人亲核 file:line)

- `users.role`:`sql/migrations/0076_user_role.up.sql:7-9`,`text NOT NULL DEFAULT 'user' CHECK (role IN ('admin','user'))`,老数据自动 user。已确认。
- `admin_tokens.role` + `scope_tenant_consistency`:`sql/migrations/0010_admin_auth.up.sql:33-34, 46-48`。已确认。
- `PanelForRole` 精确匹配 + deny-by-default:`internal/panelauth/resolve.go`(仅 `role == RoleAdmin` → PanelAdmin,其余全 → PanelUser)。已确认。
- `AdminResolver.Resolve` 流水线(前缀第一道拒 / bcrypt / active / expires / 反枚举):`internal/admin/operator_auth.go:50-95`。已确认。
- bootstrap 契约(env 非空 + 表空 + advisory lock + 陈旧不崩 + no-op):`internal/admin/bootstrap.go:48-116`。已确认。
- `/v1/auth/me`(身份取自已验证 session,绝不信前端;软删 → 403):`internal/controlhttp/panelauth_handler.go:84-112`。已确认。
- per-handler `d.Auth.Resolve`:`internal/adminhttp/` 下 16 个文件命中(单文件多 call site,如 admin_tokens_handler 3 处),`Auth: d.adminAuth` 在 routes.go 10 个块 + ~30 个 admin 路由 Mount(`/admin/v1/*` + `/v1/admin/*`)。**修正**:研究稿里「40+ 处 Auth 引用 / 170 端点」中,注入块实为 ~10 个 `Auth: d.adminAuth`,端点数量级 ~170 成立但按路由模块计更准。已确认。
- `adminGate` 强制 `id.Role == RolePlatformAdmin` 否则 403:`cmd/gateway/middleware.go:151-177`。已确认。
- `SessionIdentity{TenantID, UserID, FamilyID}` **不含 role**;`UserRole` 查询按 `tenant_id + user_id + deleted_at IS NULL`(多租户):`internal/auth/session_middleware.go:15-17`、`internal/panelauth/store_postgres.go:25-40`。已确认——这是「session 今天拿不到 role、必须显式接线」的根据。

---

## ★ P2b-3 架构修正(2026-07-01,读真码后):写门并入 P3

**Owner 已定**:高危全部 token-only(钱/凭证/KEK/签 admin token/删账号/Hermes 写),session 写只放低危配置类。

**读真码发现**:admin 路由把安全与危险端点**混在同一前缀**下——`/admin/v1/provider-accounts/{id}/credentials`(配置 vs KEK 凭证同前缀)、`/admin/v1/users`(改配置 vs DELETE 删账号)。故在组合解析器里按 path 前缀做中央分类会 **fail-open**(危险子路径漏进安全前缀被误放行),不可取。

**修正**:P2b-3 的「session 可写白名单」与 P3 step-up 的「危险端点二次密码」是**同一个 per-endpoint 分类问题**,分开建 = 先建一个被 P3 替换的临时门(堆砌)。**合并**:P2b-3 写门并入 P3,一套 per-endpoint 策略、fail-closed(默认 token-only,显式标注端点为 session-safe / session-with-stepup)。承载点在**路由注册处**(端点风险已知),非解析器猜 path。money 端点因 schema 未迁,即便有 step-up 仍 token-only(待 money-via-login 切片)。

**修正后剩余序**:P3(per-endpoint 策略 + step-up + 放开 session 写,含 FamilyID anchor + header proof + ErrLocked→429)→ money-via-login(P2b-2 schema A + 钱 step-up)→ P5 前端切壳。

---

## ★ P2b-2/P2b-3 设计(2026-07-01,待 Owner 拍板 schema + 写端点分级)

**已合**:P2b-1(f512ee7b 字符串审计统一)+ balance_credit 回归修复(4bf3dddb)+ 测试加强(6f27073d,5复杂逻辑区真 PG 集成)。

**§16 三镜动钱归属对照**:sub2api(`sub2api@e34ad2b:payment_fulfillment.go:350,439 UserID/AssignedBy`)+ new-api(`new-api:model/topup.go:16 / redemption.go:16 UserId int`)动钱归属都是**单个 int user_id**——**三镜无 token/session 双身份**,故"双身份钱归属"无现成范式,HUAKAI 是真扩展。CLIProxyAPI 纯 relay 无动钱,无对应。

**HUAKAI int64 归属迁移面**(~9 列跨 6 表,全 bigint):payment `created_by_admin_id/confirmed_by_admin_id`(0071)+ `payment_audit_events.actor_id`(0071:102);voucher `created_by_admin_id/revoked_by_admin_id`(0023);subscription `assigned_by_admin_id`(0073)+ `actor_id`(0073/0101);notices `created_by`(0020);**Hermes `admin_actor_token_id`(0144/0145,唯一带外键→崩)**。

**关键判断:P2b-2 schema 可完全延后。** Hermes 写 + 钱写在 P2b-3 保持 token-only(Owner 已定),它们的崩(Hermes FK)/误记(钱 int64)在灰度期根本不触发。故本轮**不动 money schema**,只做 P2b-3 按危险度分级放开 session 写。

**P2b-3 写端点分级(核心设计)**:组合解析器只读 gate 从"纯 GET/HEAD"升级为"读 + 安全写白名单";session 写仅放行**低危配置类**(catalog/routing/pool/quota/moderation/告警/平台设置等,均 P2b-1 已字符串归属),**危险类一律 token-only**:
- 🔴 **钱**(payment/voucher/subscription/refund,int64 归属)→ token-only,待 P2b-2 schema + P3 step-up。
- 🔴 **凭证/KEK/签发 admin token/删账号**(高危爆炸半径)→ token-only,待 P3 step-up(即便 P2b-1 已字符串归属,step-up 未建前不放开)。
- 🔴 **Hermes 写**(FK 崩)→ token-only,待 P2b-2 Hermes FK 修 + P3。

**P2b-2 未来 money schema 三选项(交 Owner)**:
- **A 加字符串 actor 列**(`*_by_actor text` 存 AuditActor(),与中央 admin_audit_events.actor_id 统一)——最完整,~9 列迁移;delta:HUAKAI 钱归属能分程序化 token vs 人会话(三镜单 id 分不清)。
- **B 来源判别列**(`*_actor_source text` + 复用 int64)——int64 含义随列变,较脏。
- **C 延后**:钱端点保持 token-only,money-via-session 与 P3 step-up 打包成独立"money-via-login"切片再做 A。**推荐**(动钱最高危,本就需 step-up;三镜无双身份范式可抄;不堆砌)。

---

## ★ Owner 定案(2026-07-01)+ P2b 执行拆分

**Owner 三决策**:①**授权 P2b 先行**(接受审计格式统一、存量行不回填、取证工具兼容新旧);②**token 通道豁免 step-up**(hk_admin 程序化凭据持有即授权,对标 new-api;sub2api 亦双通道 `admin_auth.go:26-88` x-api-key + JWT);③**P5 跟在 P2b 后**。顺序锁死:**P2b → P3 → P5**。

**sub2api 印证方向(§16,`sub2api@e34ad2b:backend/internal/server/middleware/admin_auth.go:26-88`)**:sub2 的 admin 门同样双身份——Admin API Key(x-api-key,程序化,= HUAKAI 令牌管理员)+ JWT 登录 admin 角色(= 登录管理员),两者同门。sub2 的 JWT-admin 早已能调所有管理 API;P2b 就是把 HUAKAI 登录管理员追平这条路。HUAKAI delta:令牌侧是表(多命名令牌,比 sub2 单共享密钥细)、审计能分 `admin_token:N` vs `admin_user:N`(sub2 两路都归第一个 admin,分不清)。

**P2b 站点枚举(已读真码分类)**:
- **字符串 actor(~25 处,3 套格式并存 `%d`/`admin-token:`/`admin:`/`admin_token:`)** → 统一走 `ident.AuditActor()`/`req.Caller.AuditActor()`,无 schema。安全前提已核:**无任何处把 actor 解析回数字**(Atoi/ParseInt on actor 为空);仅 admin_audit.sql:36 / observability.sql:295,330 按 actor_id 精确过滤(取证查询,新格式查、老行不回填=已授权)。→ **P2b-1**。
- **int64 actor(~15 处,重灾=钱:paymenthttp/voucher/subscription/refund `ActorAdminID int64`/`AdminID int64`)** → 装不下 `admin_user:N`,需 schema(加字符串 actor 列或 source 判别列)。→ **P2b-2**(动钱+schema,子决策可能再回 Owner)。
- **Hermes FK(hermeshttp/admin_auth.go:94 `adminActor{TokenID}` → admin_actor_token_id 外键)** → session-admin 写 NULL + 另存 user 列。→ **P2b-2**。
- **放开 session 写方法**(adminsessionauth 只读 gate 放宽)→ 仅在 P2b-1/2 属性修对后。→ **P2b-3**。

**P2b-1 排除项(功能键非审计,不机械改,单独核)**:`paymenthttp/cache_price_override_handler.go:163 "admin:"+N`(疑为 override key 非审计);hermesconfirm/cache.go(confirm 绑定键,非审计)。

---

## ★ 深研设计结论(2026-07-01,25-agent Workflow wnbh0dm9n,P3/P5/P2b 三切片各 3-lens 对抗审查 + critic)

**硬顺序链:P2b(keystone)→ P3;P5 后端无阻塞但 UX 依赖 P2b。** 三切片各有 S1,均源码亲核非误报。

### P2b(写端点放开)——比原估更实,Owner-gated
- **现网审计 actor 已 3 套格式并存、根本没走 `AuditActor()`**:`modelbindingadminhttp/routes.go:324`=`admin-token:%d`(连字符)、`pricingcataloghttp/pricing_ratio_handler.go:140`=`admin_token:%d`(下划线)、`adminquotahttp/validate.go:389`=裸 `%d`。统一到 `AuditActor()`(`admin_token:N`/`admin_user:N`)= **持久化审计格式迁移**,存量行不回填、取证工具须兼容旧格式 → §2 auth-core+审计格式变更,Owner-gated。
- Hermes `admin_actor_token_id`(FK→admin_tokens)对 session 源(TokenID=0)必须写 NULL;15 site 里哪些列带此 FK 须逐列核。
- S1:observability.sql 的 actor 过滤 UNION **不含 admin_audit_events**(设计的"第0层"前提为假,须重定位下游);oauth-callback mutating-GET **不过 session resolver**(白名单是死守卫,该端点用 OAuth flow_id/state 自证,不调 d.Auth.Resolve)。

### P3(step-up)——扩展既有原语,非另造;3 个 S1
- **二次密码+2FA step-up 原语已存在**:`passkeyhttp/stepup.go:44-77`(`LocalStepUpVerifier`,密码 argon2id 常时比较 + 2FA VerifyLogin,已生产接线 routes.go:716)。P3 真正新增 = **窗口 + anchor(绑 session FamilyID)+ 按路由挂门 + TTL 运维开关**。方案改为「给既有 verifier 加窗口」,delta 按 #12 重写为精确形态。
- **S1-a anchor 数据不可达**:`AdminIdentity` 没带 session FamilyID,resolver 构造时丢弃 `validated.FamilyID` → 提权窗绑不到 session family。修:P2a 的 AdminIdentity 补 `FamilyID` + resolver 填充(加法,token 源留 0,零行为变)。
- **S1-b proof 载体撞 `DisallowUnknownFields`**:danger 端点解码器禁未知字段、请求结构体无 step_up 字段 → proof 放 body 必 400,且中间件读 body 会耗尽 r.Body 致下游 EOF。修:**proof 走 HTTP header**(如 `X-Step-Up-*`),中间件不碰 body。
- **S1-c `writeStepUpError` 缺 429**:`passkeyhttp/handler.go:297-308` 无 `twofa.ErrLocked`→429 分支,锁定后落 default→503(误导)。修:补 ErrLocked→429。
- P3 覆盖面成功审计走 `admin_user:N` **依赖 P2b 已把 15 site 改走 AuditActor**,否则 session-admin 充值记成 `admin_token:0`。

### P5(前端切壳)——唯一后端无阻塞(auth-core 审查 SOUND);4 个 S1
- 权威来源 `getMe()` 的 panel 字段链已存在(`panelauth_handler.go`),不依赖 P2b/P3 新后端 knob。
- **S1**:OAuth 登录落点 `OAuthCallbackPage.tsx`(setSessionTokens :62/:101)漏进 scope → OAuth admin 的 panel 恒 null;getMe() 须 **best-effort + knob 门控**(否则插进登录成功分支的 catch 会因瞬时 5xx 挡登录);getMe() 503 时的**降级壳未定义**(不能默认 admin 壳=提权,不能白屏)。
- **UX 耦合**:admin 若在 P2b 前被切到运营台,其写操作 503/403(安全但断裂)——admin 密码登录只拿 session token、无 hk_admin token,operator 壳的 /admin 写端点在 P2b 放开前够不到。

### ★ 给 Owner 的决策(§15,见下方 AskUserQuestion)
1. **顺序 + 审计格式**:授权 P2b 先行(接受审计格式统一、存量不回填)?
2. **token 通道 step-up 豁免**:程序化 hk_admin token 对高危操作豁免 step-up(持有即授权,对标 new-api)?
3. **P5 排序**:P5 跟在 P2b 后(壳一上即可用)/ 先上(只读可用写待 P2b)/ 只弱化 token 框不切壳?
- 工程默认(非 Owner-gated,除非否决):P3 扩展既有 verifier + header 载 proof + anchor 绑 FamilyID + ErrLocked→429;TTL 默认 300s、session 撤销/FamilyID 轮换连带失窗;P5 getMe best-effort+knob 门控、503 降级到最小只读壳、deny-by-default。

---

## ★ P4 已合(2026-07-01,commit dc646749,对抗审查零 S0/S1)

**已交付**:`panelauth.MaybeBootstrapAdminUser`——env `HUAKAI_ADMIN_BOOTSTRAP_EMAIL` 把默认工作
租户下已注册的匹配账号提升为 `role=admin`。env 未设 = no-op。advisory-lock + 幂等(role<>'admin'
跳过不重写 updated_at)+ 陈旧/软删/未注册均记日志不 crash。`tenancy.WorkingTenantIDFromEnv()` 抽
单一真相源共用(复用不重复,零行为变)。

**delta(§16)**:new-api 空库建 root/123456、sub2api 建号带 ADMIN_PASSWORD;HUAKAI 提升真实注册
账号、env 只放邮箱(非凭据、无弱密码、无泄密面)。CLIProxyAPI 无 user/role 无等价。

**验证**:单测(env-gate/nil-pool/tenant 守卫判别)+ 真 PG integration_pg 六场景(提升/幂等+updated_at/
跨租户隔离/软删不提升/大小写/陈旧不崩);§14 五处变异实测证红(tenant/deleted_at/lower 三谓词 +
tenant 守卫 + role<>'admin')。对抗审查零 S0/S1,两条 S3 测试判别缺口就地修复证红。

**运维流程**:先用正常注册流建账号 → 设 `HUAKAI_ADMIN_BOOTSTRAP_EMAIL` 重启 → 该账号 role=admin。
注:users.role 当前只驱动前端面板归属(0076);授予 admin API 访问要等 session-auth knob 翻开
(P2b 写端点放开后)。

---

## ★ P2a 已合(2026-07-01,commit 9349144e,对抗审查零 S0/S1)

**已交付**:`AdminIdentity` 增 `Source`(token/session)+ `UserID` + `AuditActor()`;组合解析器
session 通道「灰度只读端点先行」——仅放行 GET/HEAD,写方法一律拒。knob 默认关零行为变。
§14 双变异证红(gate 失效 / AuditActor 忽略 Source),对抗审查逐 7 攻击面证伪,零 S0/S1。

**核心安全属性(经审查坐实)**:即便翻开 knob,session-admin 灰度期只能读,P1 两处写隐患
物理上无法触发——每个用 `ident.TokenID` 做变更写的 handler 都是 POST/PUT/DELETE
(routeadmin/voucher/balance_credit 等实测全非 GET);Hermes 审计只在写 handler,读 handler
不写审计,故 FK 崩 + JSONB 误归零残留。

**遗留债(P2b 放开写端点前必处理,审查记录的 S2/S3)**:
1. **S2 — mutating-GET:`oauth-callback`**(`admin_credential_acquisition_handler.go:96` 注册
   `r.Get("/oauth-callback")`,handler 落库凭证 + 写审计)。「读方法≠只读操作」的既有设计债:
   knob 开后 session-admin 可经此 GET 触发一次真实凭证 finalize。**不触 P1 隐患**(该 handler 用
   预存 `session.ActorID` 归属、不调 `d.Auth.Resolve()`,不写 TokenID=0、不触外键)。放开写端点
   前把变更类端点从 GET 迁 POST,或把 gate 改「端点白名单」而非纯 HTTP 方法。
2. **S3 — 方法代理只读性脆弱**:未来若新增「GET 却变更且用 ident.TokenID 审计」的 handler,gate 会
   静默放行 session-admin(TokenID=0)触发 P1。加一条「变更端点必须非 GET」的结构约束/集成断言。
3. **S3 — 灰度期 session-admin 可读全租户进程指标**(`/debug/vars`、`/metrics`)——D3 意图内,仅知会。

**P2b(Owner-gated,改持久化审计格式=behavior)**:`AuditActor` 接入 ~15 审计 site(token-admin
输出从 `%d`/`admin-token:%d` 统一到 `admin_token:N` = 现网审计内容格式变更);Hermes `admin_actor_token_id`
对 session-admin 写 NULL(另存 user 列);放开写端点。翻 knob 激活 = Owner 最终拍板点。

---

## ★ P3 实现设计(2026-07-01,读 HUAKAI 真码 + 三镜 step-up 后定稿)

**三镜 step-up 证据(亲核 file:line):**
- **new-api**:窗口模型(`secure_verified_at` 时间戳存 Gin **服务端 session cookie**,TTL 300s,`SecureVerificationRequired` 中间件比对 `now-verified_at<300`;`middleware/secure_verification.go:16,64`)。仅 1 条路由用(`POST /api/channel/:id/key` 看渠道密钥,`router/api-router.go:236`)。无失败锁定,靠 `CriticalRateLimit`。
- **sub2api**(最近亲,同双令牌 admin 模型 `admin_auth.go:26-88`):**无统一 step-up 框架**,逐操作**内联再认证**(改密验 old_password + bump TokenVersion 废全 token;TOTP/邮箱绑定验 password/email_code,均 body 载);5 次失败→429(`VERIFY_CODE_MAX_ATTEMPTS`/`TOTP_TOO_MANY_ATTEMPTS`);常时比较。**显式无跨操作"近 N 分钟已验证"窗口**;admin 另有合规确认门(HTTP 423)。
- **HUAKAI 既有** `passkeyhttp/stepup.go:44-77`:已是**逐请求 proof**(密码 argon2id 常时比较 OR 2FA VerifyLogin),已生产接线(routes.go:716);`writeStepUpError` **已含** `twofa.ErrLocked→429`(handler.go:305,非缺口——原计划误记)。

**定稿决策(翻转 §315 line-340 的"加窗口"工程默认):走逐请求 proof,不做窗口/不加 FamilyID anchor。** 理由:窗口在 HUAKAI(无状态 bearer,无 new-api 那种服务端 session)= **净新增基建**(签名 token 或存储),既有原语与最近亲(sub2api)都不用;逐请求 proof 复用既有已接线 verifier,零新状态、无重放窗口(每次高危单独授权)。符合「镜像既有模式/不发明新抽象/不堆砌」。窗口是 UX 优化,待真有中危路由放开 + Owner 要更顺 UX 再做,YAGNI。

**HUAKAI 相对两镜的 delta(更优处)**:一套 **fail-closed 的 per-endpoint 写分级框架**(默认 token-only),两镜都没有——它们把再认证零散挂在个别路由/handler 里;HUAKAI 集中成「路由注册处显式标注、未标注=默认拒 session 写」,配合 P2b 已建的双身份(token vs session)审计归属。

**机制(全在既有默认关 knob 后):**
1. **写分级(opt-in,fail-closed)**:`AdminWriteClass ∈ {SessionSafe, SessionStepUp}`,经 per-route 中间件 `adminsessionauth.AllowSessionWrite(class)` 塞进 request context。**只给要放开的路由挂**;高危路由不挂 → 默认 = session 写被拒(= 今日 P2a 行为)。无 token-only 枚举值(它就是"不标注"的默认)。
2. **解析器改造**:把 P2a 的一刀切 `if !isReadOnlyMethod → 拒` 换成:只读方法(GET/HEAD)照放;写方法读 context 里的 class —— 缺失→拒(fail-closed);SessionSafe→放;SessionStepUp→验 step-up header proof。
3. **step-up proof 走 header**(`X-Admin-Step-Up-Password`/`X-Admin-Step-Up-2FA`),避开 danger 端点的 `DisallowUnknownFields` + 不碰 r.Body(否则耗尽致下游 EOF)。解析器注入 `StepUpVerifier` 接口(knob 关/未接线时为 nil → SessionStepUp 路由 fail-closed 503);wiring 用薄 adapter 包既有 `passkeyhttp.LocalStepUpVerifier`(避免 adminsessionauth→passkeyhttp 层级耦合,不泄露其 proof 类型)。
4. **错误映射**(admin 语境,反枚举一致):新 sentinel `ErrAdminStepUpRequired`(403)/`ErrAdminStepUpInvalid`(401)/locked→429/未配→503,并入 `writeAdminAuthError`。
5. **knob 关**:session 通道整体不走,零变更。**knob 开 + 无路由挂**:session 读照走、所有写默认拒 = 今日行为。**knob 开 + 挂 SessionSafe/StepUp**:才开对应能力。

**放开哪些真路由 = 血案边界,分两步**:先合**机制切片**(框架 + 解析器 enforcement + step-up header 验证 + 测试用合成路由跑通,不动真 admin 路由)→ 再产出全 admin 写端点风险分级表交 Owner 批,按批放开低危配置类为 SessionSafe。money/凭证/KEK/签发 admin token/删账号/Hermes 写永远不挂(token-only);money 即便日后有 step-up 仍 token-only(待 money-via-login 切片迁 schema)。

**⚠️ 路由放开切片的强制项(对抗审查确认的潜伏跨层缺口,balance_credit 同类)**:机制切片里 `admin.ErrAdminStepUp{Required,Invalid,Locked}` 尚无任何 handler 的 `writeAdminAuthError` 副本映射(全仓 ~15 副本只认 ErrAdminBackend,余走 default→401)。机制切片下这些错误生产不可达(无真路由挂 AllowSessionWrite + knob 默认关),且 default→401 是 fail-closed 兜底(更严不误授权),故非 S0/S1。**但放开真 SessionStepUp 路由的切片【必须】同时**:①在承载该路由的包的 `writeAdminAuthError` 补 Required→403 / Invalid→401 / Locked→429(+Retry-After);②加一条 handler 端到端测试验此链(否则 403/429 静默坍缩成 401,丢掉设计刻意区分的可操作信号 + 429 退避提示)。

### ★★ Owner 终审(2026-07-01):采用 new-api 模型,砍掉后端 step-up,改前端确认弹窗

Owner 定案:**「不需要[后端密码/2FA step-up],像 new-api 就行了。只是弹窗二次确认,说明无法撤销操作即可。」**

- **后端**:session-admin(登录 admin,role='admin')可直接写非高危端点(= SessionSafe),**不做后端二次密码/2FA**。这对标 new-api(登录 admin 经 session 全权操作,前端对危险操作弹确认框)。防的是**误操作**(accident),非攻击——单运营者本就是受信主体。
- **前端**:对不可逆/破坏性操作弹「此操作无法撤销,确认?」确认框(P5 做,纯 UX,可绕过但只为防手滑)。哪些不可逆 = 分级表里带 `irreversible` flag 的。
- **high-risk 仍 token-only**:money/凭证/KEK/签发吊销/建删账号(60 个)保持 token-only。理由不变(独立锁 + money-via-session 需 P2b-2 int64 schema 迁移才能做,技术阻塞)。

**据此简化机制(撤 1c7ed0d9 的 step-up 部分,保留 SessionSafe/fail-closed 框架)**:删 `internal/adminstepup` 包、`SessionStepUp` 档、header proof、`ErrAdminStepUp*` sentinel、resolver 的 step-up 分支与 stepUp 依赖。写分级收敛成二元:**token-only(默认 fail-closed)vs SessionSafe(显式放开)**。原 39 个「SessionStepUp」端点改归 SessionSafe(session 可直接写),危险者靠前端确认框。**已合 commit fe097f4e**(§14 反转写分级判定证红,测试全绿,默认关 knob 后零生产变)。

**放开路由的落地架构(2026-07-01,已定 + chi 探针实证)**:
- **否掉「中央 exact-pattern allowlist 中间件」**:探针证实 chi `RouteContext.RoutePattern()` 在【父路由 r.Use 中间件】里读到 `""`——父级中间件在 sub-router 匹配【之前】执行,拿不到叶子模式。故中央 allowlist 在 admin 父路由层不可靠。
- **定为「per-route 注解」**:在各包 Mount 函数里给 SessionSafe 路由挂 `r.With(adminsessionauth.AllowSessionWrite(adminsessionauth.SessionSafe))`。robust(不依赖路由内省)、class 紧贴路由(无漂移)、fail-closed(不挂即 token-only)。
- **⚠️ 碰撞区限制**:per-route 注解须编辑承载包,但本会话硬约束【不碰 `gateway*`/`proxy`/`tlsfp` 等碰撞区包】。故这些包里的 SessionSafe 路由(gatewayhttp 池账号/渠道健康/L2缓存、proxyadminhttp 代理、tlsfphttp 指纹)**无法由我放开,继续 token-only**(与 money/凭证同待遇——基础设施层走令牌,合理)。
- **可放开集(非碰撞区,~26 路由)**:adminuserhttp(用户 remark/状态/解锁/强关2FA/解绑,5)、controlhttp(分组路由增改停删,4)、modelbindingadminhttp(模型→pool 绑定增改删,3)、moderationhttp(关键词/哈希/解封,7)、adminquotahttp(配额策略增改,2)、announcementhttp(公告增改删,3)、adminhttp(loglevel/model-sync,2)。**这些是放开切片的落地目标**,每包 per-route 挂 SessionSafe + handler 端到端测试(证 knob 开时 session-admin 可写、token 仍可写、未挂路由仍拒)。

---

### ★ admin 写端点风险分级表(2026-07-01,Workflow wr97jggxm:102 端点枚举→逐条对抗验证非 token-only 提议)

**102 个写端点**。首轮 56 token-only,46 拟放开;对抗验证后 4 个被降回 token-only(passkey 删除=账号安全 / 改用户分组=耦合计费配额档 / 建 TLS 指纹 profile / 删配额策略)。**放开哪些=血案边界,Owner 拍板**;下表全在默认关 knob 后,翻 knob 才激活。

**① SessionSafe(3,低危可逆无钱无凭证,Owner 已授权"放开低危配置类")**:
- `PUT /admin/v1/users/{id}/remark`(adminuserhttp)——写用户 admin 备注自由文本。⚠️残留:回显未服务端消毒=存储型 XSS(admin-on-admin,前端渲染层问题,非"谁能写")。
- `PUT /admin/v1/loglevel`(adminhttp)——运行时日志级别(zap 原子变量,进程内即时可逆)。⚠️切 debug 放大日志信息暴露面。
- `DELETE /admin/v1/cache/l2/{key}`(gatewayhttp)——逐出一条 L2 缓存(可逆,下次请求回填)。

**② SessionStepUp(39,中危可逆但有真实爆炸半径,需 Owner 明确授权+带 step-up)**:池账号编辑/停启/清限流/软删/批量、指纹 profile 绑定、用户账号恢复(强制关 2FA/解绑社交/解锁/封启)、模型→pool 绑定增改删、分组路由增改删停、渠道健康暂停/恢复/强激活、代理删/状态/质检、TLS profile 改/状态/删、模型目录同步、公告增改删、配额策略增改、审核关键词/哈希/API key 封禁增删。

**③ token-only(56+4=60,永久,session 永远够不到)**:全部 money(充值/退款/发券/订阅/计费设置/价格/媒体对账)、凭证/KEK(provider 凭证增改删/轮换/采集流/粘贴导入/OAuth init/邮件 SMTP 密钥)、签发吊销(api-key/admin-token)、建/删账号、建代理(含凭证)、改用户分组、建 TLS profile、删配额策略。

**放开路径的工程注意(非 policy)**:①注解穿透——SessionSafe 的 remark 埋在 adminuserhttp.MountRoutes 多端点混合挂载里,不能整包 wrap(会 fail-open 同包的 status/unlock 等 StepUp 路由),须 per-route 挂 `AllowSessionWrite`(改 Mount 函数按路由标注,或在 routes.go 单独注册该路由)。loglevel/L2cache 若单一用途可整组 wrap。②放开任何 SessionStepUp 路由的切片必须同时补 `admin.ErrAdminStepUp*`→403/401/429 handler 映射+端到端测试(见上"强制项")。

### ★ SessionSafe 写路由放开进度(2026-07-01,per-route 注解 + 端到端测试 + §14 双向变异)

**已放开 5 包(均默认关 knob 后零生产变)**:
- adminuserhttp(8fa015b2):unlock/2fa-force-disable/remark/status/解绑社交 5 端点。
- controlhttp + moderationhttp(89f06d96):分组路由增改停删 4;审核关键词/哈希增删 + API key 解封 7(PUT /config 留 token-only)。
- announcementhttp + adminhttp:公告增改删 3;loglevel PUT + model-sync POST 2。
- adminquotahttp:配额策略 create/update 2(delete 留 token-only);顺带把 gateway 内联挂载重构成包函数
  `MountQuotaPolicyRoutes`(全路径内联、路径不变,写分级注解与路由收拢一处;删除死掉的导出 wrapper)。
- 共享测试脚手架 `internal/adminsessionauthtest`(89f06d96)。

**至此非碰撞区可自主放开的包全部开完(6 包)。**

**arc 收口全局审计(2026-07-01,Workflow wy5tl34mk,5 维 × 逐条对抗验证):零 CONFIRMED S0/S1。**
核心 knob-off 零变更不变量确认成立(knob 关时 session/写分级分支物理不可达、AllowSessionWrite 惰性)。
唯一 CONFIRMED = **S3(自愈、部署边界一次性)**:P2b-1(f512ee7b)把审计 actor_id 从 `"5"` 统一到
`"admin_token:5"`(token 通道恒活、非 knob 门控),而 API key 签发限流的 `CountIssuanceInWindow`
按 actor_id 精确串匹配统计 → P2b-1 部署瞬间,卡在 30/小时上限的 admin 其限流窗因旧行 actor_id 格式不符
被有效清零一次(≤1 小时自愈,无回填)。无越权/崩溃/money。属 Owner 已接受的「审计格式统一 + 存量不回填」
之副作用(Q1),留待 money-via-login 切片(届时本就重访审计归属)一并解决,不为 S3 硬改限流查询。

**暂缓/不碰(继续 token-only)**:
- `modelbindingadminhttp`:import 碰撞区 `registry` 类型,守 §6 不碰。
- **碰撞区 gateway*/proxy/tlsfp**(池账号/渠道健康/L2缓存/代理/TLS 指纹):守 §6 不碰,继续 token-only(与 money/凭证同待遇,基础设施层走令牌合理)。

**机制切片已合(2026-07-01,commit 1c7ed0d9)**:admin sentinels + `internal/adminstepup` 适配器(复用 passkeyhttp verifier,错误翻译)+ `adminsessionauth` 写分级(AllowSessionWrite 中间件 fail-closed / resolver enforcement / header proof)+ wiring 注入;全在默认关 knob 后零生产变;§14 变异(fail-closed default / step-up 错误传递 / 错译)均证红,对抗审查(3 镜头 × 逐条对抗验证)零 S0/S1。

---

## ★★ money-via-login 设计(2026-07-01,Owner 选定的下一切片;schema+money 双 Owner-gated,待拍板)

**目标**:让登录 admin(session,无 hk_admin 令牌、TokenID=0)也能做动钱操作(充值/余额调整/退款/发券/订阅指派),归属可追、守 new-api 模型(前端确认弹窗,无后端 step-up)。

**研究结论(Workflow wsknpa07f 亲核真码,file:line 见任务输出)**:动钱归属列**全是可空 bigint、无 NOT NULL、无指向 admin 身份的 FK**(只有指向 tenants/users 的租户 FK)→ session-admin 写入本身不违约。真阻断三层:
1. **归属丢**:所有动钱 handler 取 `ident.TokenID`(int64)→ session-admin=0 → `nullableInt64(0)`=NULL,归属整条丢(balance_credit 刻意传 "0" 也一样)。
2. **硬阻断**:退款申请 approve/reject 有 `adminActorID <= 0` 守卫(refund_request_admin.go:130,169 + refund_request_postgres.go:95,144)→ session-admin(0)被 ErrInvalidInput 直接拒。
3. **到达阻断**:无动钱路由标 SessionSafe → session-admin fail-closed 到不了(设计使然)。
- **格式裂缝**:`admin_audit_events.actor_id` 已是 **text**(`admin_token:N`,P2b-1)但 payment/voucher/subscription 的 actor 列是 **bigint 裸 id**,区分不了 admin_tokens.id vs users.id。审计表有 `actor_kind` CHECK(admin/user/system)。

**§15 三镜对照(动钱归属数据模型)**:
| 镜 | 归属列形态 | 是否区分 token vs session | 证据 |
|---|---|---|---|
| **new-api** | 单 int `admin_id`(恒 users.id)+ JSON `auth_method`("access_token"/"session") | **区分**(auth_method 字段) | new-api@HEAD:model/log.go:179-201、controller/audit.go:66-80 |
| **sub2api** | 单 varchar `operator`(如 "admin"/"user:123") | **不区分** | sub2api@HEAD:service/payment_stats.go:153-159、migrations/093_payment_audit_logs.sql |
| **CLIProxyAPI** | 无动钱模块(纯 relay) | 不适用 | README 确认无 .sql/无 payment 包 |
- **关键差异**:new-api 的 admin token = 用户的**个人** access token(admin_id 恒 users.id,auth_method 只记"怎么登的")→ 单一 id 空间。**HUAKAI 的 admin_tokens 是独立于用户的程序化凭据**(不绑任何 users.id)→ 天然两 id 空间(admin_tokens.id vs users.id),必须区分。HUAKAI 的 `AuditActor()` 串 `admin_token:N`/`admin_user:N`(id+来源编码进一串)正是为此,比 new-api「id+方法两列」更紧凑。

**schema 选项(→ Owner 拍板)**:
- **选项 A(推荐)——加 text `*_by_actor` 列存 AuditActor()**:给动钱表(payment_orders/payment_audit_events/voucher/voucher_batch/user_subscriptions/subscription 审计/payment_refunds/refund_requests)各加一个可空 text 列,存 `admin_token:N`/`admin_user:N`;既有 bigint 列保留(token-admin 双写、session-admin 只写 text 列)。**统一 admin_audit_events 既有 text 格式**、无歧义、取证友好、对标 new-api「id+来源」的紧凑版。改动面:一支迁移加~8 列 + handler 改写审计归属走 AuditActor()。
- **选项 B——扩 actor_kind + int actor_id 存来源相关 id**:审计表 actor_kind CHECK 扩 'admin_token'/'admin_session',actor_id 存对应 id(token→TokenID、session→UserID),靠 kind 消歧。更接近 new-api「两列」、actor_id 可 JOIN;但 order/assignment 类列(created_by_admin_id 等)无 actor_kind 兄弟列,session-admin 只能留 NULL(归属只落审计事件)或另加来源列 → 反而更碎。
- **推荐 A**:一列一格式、全表一致、与 P2b-1 已建的 admin_audit_events text 归属同源;单运营者场景取证可读 > JOIN 便利。

**配套修法**:
- **refund 守卫**:`adminActorID <= 0` 改判「有已认证 admin 身份」而非「int>0」(session-admin 合法);归属改走 text actor 列。
- **S3 限流(顺带解)**:给 admin_audit_events 加数值 `actor_token_id bigint`,API key 签发限流 `CountIssuanceInWindow` 改按数值稳定键分桶(而非审计展示串),跨部署边界免疫(研究选项 A2)。可与本切片同迁移或独立小迁移。
- **放开范围(blast-radius,→ Owner 拍板)**:哪些动钱端点发 SessionSafe 许可?**选项**:(a)全动钱端点放开(充值/退款/发券/订阅,最贴 new-api,前端危险确认弹窗);(b)只放开低危动钱(如手动充值,退款/发券留 token-only);(c)先只建归属基建、暂不放开任何动钱端点(session 仍够不到,但迁移到位、日后翻标即开)。

**执行计划(Owner 已授权"按我的回答继续"=按推荐 A + 放开动钱;coordination 看板已确认无人改 payment)**:①一支迁移(加 text actor 列 + S3 数值列 + 索引;非破坏,纯加列)②handler 动钱归属统一走 AuditActor() 写 text 列(sqlc 手改生成码不重生成,见 [[sqlc-codegen-out-of-sync]])③refund 守卫改判来源 ④按选定范围给动钱路由发 AllowSessionWrite(SessionSafe)⑤§14 变异 + 真 PG 集成测试(session-admin 动钱归属落 admin_user:N、token-admin 落 admin_token:N、refund 不再拒 session)⑥对抗审查零 S0/S1。全在默认关 knob 后零生产变。

**★ 分阶段执行序(每阶段=一子系统 vertical slice:迁移+handler+真 PG 测试+变异+审查后单独提交;避免半迁移态与 money 代码在长会话尾部的仓促出错):**
- **Stage 1 = payment/充值路径**(canonical):迁移加 payment_orders.created_by_actor/confirmed_by_actor + payment_audit_events.actor_ref(nullable text)。改动 **~10 个 ActorID 触点**已勘定:`admin_credit.go`(AdminBalanceAdjustmentInput 加 ActorRef 字段 :28 区、actorID 计算 :114、createOrderRecord :123、三条 auditInsert :135-137、confirmed :168)+ `types.go`(createOrderRecord :63 / auditInsert :122-123 加 ActorRef/ActorRefText)+ `store_postgres.go`(insertOrderTx/insertAuditTx 写新 text 列)。**关键:balance_credit_handler 保持传 int64 TokenID 给旧列(token 向后兼容),另传 ident.AuditActor() 给新 text 列**——token-admin 双写(int64+text)、session-admin int64 落 NULL + text 落 admin_user:N。
- **Stage 2 = subscription**(admin_ops + subscription/activation + audit,assigned_by_actor/actor_ref)。
- **Stage 3 = refund**(refund_request_admin/postgres 去 `adminActorID<=0` 硬守卫改判来源 + payment_refunds/refund_requests text 列)。
- **Stage 4 = S3 限流数值键**(admin_audit_events 加 actor_token_id bigint,CountIssuanceInWindow 改按数值分桶)。
- **Stage 5 = 放开动钱路由**发 SessionSafe(充值/退款/订阅;adminhttp/paymenthttp/subscriptionhttp 均非碰撞区;守 new-api 前端确认弹窗)。
- **⚠️ voucher 顺延(原 Stage 2)**:其 admin handler 在 `internal/gatewayhttp/voucher_handler.go` = §6 碰撞区(本会话一贯不碰,与池账号/渠道健康同待遇)。发券归属 text 列 + 放开留待碰撞约束解除后一小片(3 个传参点 + 迁移),届时 voucher 继续 token-only 不受影响。
- **收尾 = arc 级对抗审查零 S0/S1**。全程 coordination claim、默认关 knob 后零生产变。

**Stage 1 已合(5a240cdb)/ Stage 2 subscription 已合(f0fb4c66,含 changePlanActor session 误判修复)/
Stage 3 refund 已合(6f876398,守卫改判来源+decided_by 0→NULL,审查抓 S2 refund.go 漏接已就地补)/
Stage 4 S3 限流数值键已合(46b76c8c,零 schema 翻案:CountIssuanceInWindow 双键谓词 actor_id OR legacyActorID,免二次格式迁移)/
Stage 5 放开动钱路由 SessionSafe 已合(d1379b20)——五阶段均对抗审查零 S0/S1。**
**收口审计(14b4f039)**:arc 级 money 审计抓 S1——建单/取消/重试路径开了 SessionSafe 但未接 ActorRef,
致 session-admin 建单 created_by_actor 与 created_by_admin_id 双 NULL + actor_kind 误标 'system'(真人伪称系统单);
已全接线 + createOrderActorKind 守卫 + 2 真 PG 测试 + 3 变异证红。

**★ voucher 归属片已合(5001f3ab,Owner 解禁 voucher 后接入)**:迁移 0168(voucher +created_by_actor/revoked_by_actor、
voucher_batch +created_by_actor,nullable text)+ CreateInput/BatchCreateInput/RevokeInput 加 ActorRef + 三处守卫改判来源
`(AdminID<=0 && ActorRef=="")` + store 双写(token bigint+text / session 仅 text,bigint NULL 绝不误归 0)+ voucherAuditActor 辅助
(token 保留裸数字串向后兼容、session 用 ActorRef)+ gatewayhttp/voucher_handler.go(3 处)+ subscriptionhttp 订阅券全接线。
真 PG 测试 3 例 + §14 变异三处证红 + 对抗审查逐列核对**零 S0/S1**。审查门控观察:实际活路径是订阅券创建
(subscriptionhttp:271 标 SessionSafe);gatewayhttp 余额券 POST 未标 SessionSafe,session 写在 resolver 层即被拒,
那段归属码 inert(前瞻,knob 默认关)——均在 Owner-gated money-via-login 范围内。

**★ money-via-login 归属基建至此全部落地(payment/subscription/refund/voucher + S3 限流键),后端 arc 收官。**
早前 S3 观察均已消解或纳入:①ActorRef 未纳入 validate 必填——生产 handler 恒传非空,留观(voucher 片同款,审查确认无害);
②通用手工确认路径 confirmed_by_actor 已在 Stage 5 放开确认端点时接。

---

## ★★ P5 前端切壳已建(2026-07-01,Owner「前端改就完了」授权)

**P5a role 感知壳 + getMe best-effort(commit 待附)**:
- 新 `frontend/src/auth/me.ts`:模块 store + `useMe()`(仅订阅)+ 纯函数 `deriveShellAccess`/`nextMeState`/`visibleNavSections`。
- **权威来源** = 后端 `/v1/auth/me` 的 `panel`(取自 users.role,绝不信前端)。`panel==='admin'` 才启用运营台(导航 + Hermes 面板),其余仅用户门户。
- **deny-by-default**:唯有 `status==='ready' && panel==='admin'` 启用运营台;loading/degraded/user/none/null 一律仅用户壳——降级绝不默认 admin 壳(防提权)、绝不空壳(防白屏)。
- **getMe best-effort**:触发点在壳(AppShell,按 sessionToken 变化),非登录 handler,故邮箱密码与 **OAuth 回调**两条登录路径都会拉 panel(修 §315 的 OAuth admin panel 恒 null 之 S1)。首拉失败→degraded;重验失败(已 ready)→保留上次良好态(瞬时 5xx 不抖掉在用 admin 的壳)。登出 resetMe 清态。
- 接线:PipelineNav 按 `visibleNavSections` 过滤;AppShell 单一 getMe 触发点 + Hermes 面板 gate 由 operatorEnabled+operator 路由双守;TopBar 登出接 resetMe。
- 验证:vitest 14 例(deny-by-default/首拉降级/重验保留/OAuth 覆盖/换人不残留)+ §14 五处变异证红 + 全量 1315 绿 + build 绿。

**P5b 危险操作确认弹窗(new-api 模型,commit 待附)**:
- 新 `frontend/src/ui/confirmDanger.ts`:单一真相源——纯 `buildIrreversibleMessage`(恒「⚠️ 此操作无法撤销。」前缀)+ `confirmIrreversible` 薄包装(无 window 保守返 false)。
- 接入 money 敏感不可逆 admin 操作:退款争议裁决(DisputesAdminPage)、券吊销(VouchersAdminPage)。其余危险操作(删除/2FA 强关等)代码库已有 window.confirm 二次确认;**是否全站统一迁到本助手 + 是否做带样式模态框,作为可选扩展 surface Owner**。
- 弱化 token 粘贴框:登录页 admin token 输入已收在折叠 `<details>`(登录为主),现状已满足「弱化」。
- 验证:vitest 3 例 + build 绿。

**注**:前端切壳仅为体验、不是授权边界——真正边界在后端每端点独立鉴权(§3 resolver + P2a/P3 写分级)。knob `HUAKAI_ADMIN_SESSION_AUTH_ENABLED` 仍默认关;翻开=Owner 最终拍板点。

---

相关文件(绝对路径,供落盘参考):
- 计划落盘目标目录:`/home/ubuntu/HUAKAI/backend/docs/process/plans/`
- 核心改造文件:`/home/ubuntu/HUAKAI/backend/internal/admin/operator_auth.go`、`/home/ubuntu/HUAKAI/backend/internal/admin/bootstrap.go`、`/home/ubuntu/HUAKAI/backend/internal/panelauth/resolve.go`、`/home/ubuntu/HUAKAI/backend/internal/auth/session_middleware.go`、`/home/ubuntu/HUAKAI/backend/cmd/gateway/routes.go`、`/home/ubuntu/HUAKAI/backend/cmd/gateway/middleware.go`、`/home/ubuntu/HUAKAI/backend/internal/controlhttp/panelauth_handler.go`
- schema:`/home/ubuntu/HUAKAI/backend/sql/migrations/0076_user_role.up.sql`、`/home/ubuntu/HUAKAI/backend/sql/migrations/0010_admin_auth.up.sql`