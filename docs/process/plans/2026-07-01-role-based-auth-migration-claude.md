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

相关文件(绝对路径,供落盘参考):
- 计划落盘目标目录:`/home/ubuntu/HUAKAI/backend/docs/process/plans/`
- 核心改造文件:`/home/ubuntu/HUAKAI/backend/internal/admin/operator_auth.go`、`/home/ubuntu/HUAKAI/backend/internal/admin/bootstrap.go`、`/home/ubuntu/HUAKAI/backend/internal/panelauth/resolve.go`、`/home/ubuntu/HUAKAI/backend/internal/auth/session_middleware.go`、`/home/ubuntu/HUAKAI/backend/cmd/gateway/routes.go`、`/home/ubuntu/HUAKAI/backend/cmd/gateway/middleware.go`、`/home/ubuntu/HUAKAI/backend/internal/controlhttp/panelauth_handler.go`
- schema:`/home/ubuntu/HUAKAI/backend/sql/migrations/0076_user_role.up.sql`、`/home/ubuntu/HUAKAI/backend/sql/migrations/0010_admin_auth.up.sql`