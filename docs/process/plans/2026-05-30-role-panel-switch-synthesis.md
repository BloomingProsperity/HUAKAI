# 登录按角色切面板 — 双草 synthesis(Claude × Codex)

日期 2026-05-30。Claude 稿 `-claude.md` + Codex 稿 `-codex.md` 独立成文后交叉(CLAUDE.md #10)。Owner 已定「按 sub2api 那样处理」。

## 一致(两稿 + source 都同意)
1. **地基不存在**:users 无 role(0007:18)、SessionIdentity 无 role(session_middleware.go:15)、admin 独立 token(0010:20)、无用户登录流(session_handler.go:32)、前端无 auth(api/client.ts 从 localStorage 取 admin bearer)。
2. **后端才是安全边界**:前端守卫只是体验层,绝不能当安全边界;localStorage 角色不可信;所有 admin API 后端强制 deny-by-default。
3. **判别测试抓真越权**:伪造前端角色→后端仍 403;tenant_operator scope A 访问 B→403;普通 user session 调 /admin/*→401/403;principal 类型分离(不用一个 bool/string 贯穿)。
4. **schema gate + 前端解封**:users 加登录基础 = Owner-gated migration;前端此前搁置(Rust 四波后),本板块要 Owner 显式解封。

## 冲突(需 Owner 裁)
**Owner 定 = sub2api 式 = 统一账号+role(codex 的 Option A)。但 codex 独立推荐 Option B(双凭据/一个入口自动切),理由是 Option A 冲 HUAKAI 的 CMB-1 隔离、易 confused-deputy。**

调和方案(Claude 综合):sub2api 式的「面板登录」可落地**而不塌 CMB-1**——
- users 加 `role`('admin'/'user',默认 user;sub2api `ent/schema/user.go:46` + `constants.go:15-16` RoleAdmin/RoleUser);一个用户登录,session 带 role,role 决定面板(满足 Owner「按sub2api」)。
- **既有 admin_tokens 不并入 users**:保留作 programmatic admin API 凭据(如 /v1/admin/routes 走 AdminResolver),Feature Preservation;面板 admin 会话(role=admin)是**独立 principal 类型**,admin API 加读 session-role 的中间件(镜像 sub2api `admin_auth.go:23` / `admin_only.go:20`)**同时**仍接受 hk_admin token。
- principal 在 context 类型分离(codex 的 confused-deputy mitigation),不用裸 bool。
→ 这样既是 sub2api 式统一账号+role(面板),又保住 CMB-1(API 凭据世界)。

## sub2api 落地事实(clean-room 行为级,已 source-read)
- 单 users 表带 role 字符串:`~/refs/sub2api@91da8159/backend/ent/schema/user.go:46`;值 `RoleAdmin="admin"`/`RoleUser="user"`(`constants.go:15-16`)。
- 登录发 JWT(`jwt_auth.go`);admin 路由先过 admin 中间件查 role==admin(`admin_auth.go:23` / `middleware/admin_only.go:20`);`IsAdmin()=Role==RoleAdmin`(`service/user.go:65`)。
- admin/user 路由分组分离(`routes/admin.go:17` / `routes/user.go:18`);前端路由把管理页标 requiresAdmin、非管理员重定向(`frontend/src/router/index.ts:720`、`stores/auth.ts:89`)。
- **sub2api 是纯二分(admin/user),无 tenant_operator 概念** —— 这是 HUAKAI 多出来的(admin_tokens 有 platform_admin/tenant_operator)。

## Owner 决策(2026-05-30 AskUserQuestion,已锁)
1. **员工语义 = 二分**(管理员 vs 其他所有人):员工=user role,看用户面板,无管理入口;tenant_operator 保持独立凭据(不重解释成员工)。最贴 sub2api(纯 admin/user)+ Owner 原话。
2. **admin 凭据 = 保留 + 加角色**:hk_admin token 继续作 programmatic API 凭据(不删,CMB-1 隔离不动);users 加 role 给面板登录;admin API 同时接受 role=admin session 与 hk_admin token。principal context 类型分离防 confused-deputy。
3. **登录凭据**:默认 sub2api 式账号密码;OAuth/第三方留路标(R-VEND 之外的人员登录 OAuth 作 future)。

## 需 Owner 拍的点(已答,保留存档)
1. **员工语义**:sub2api 二分(员工=user role 看普通面板,tenant_operator 仍是独立 operator 凭据)vs 三分(platform_admin 全局台 / tenant_operator 受限台 / 普通员工 user 台)。Owner 原话「员工看普通面板、无管理入口」⇒ 偏二分;codex 推荐三分(因 tenant_operator 已存在)。
2. **admin_tokens 调和**:保留独立(作 API 凭据)+ users 加 role 给面板(推荐,保 CMB-1)vs 完全并进 users(纯 sub2api,塌隔离)。
3. **用户登录凭据**:password(sub2api 式)vs OAuth/第三方 vs 两者。codex 警告别 silently invent。

## 切片(调和方案,以 Owner 拍后为准)
- **S1**:principal 契约 + `GET /auth/me` 合约(返 panel=admin|user|none + tenant scope + expiry,不返敏感)+ 后端测试骨架。新包 `internal/panelauth`(非冻结)。无 schema。
- **S2**:admin 面板会话(role=admin principal)+ admin API 加 session-role 中间件(仍兼容 hk_admin token)。
- **S3**:用户登录流(schema gate:users 加 role + 登录凭据表/列)+ /auth/me 返 user entitlement;复用既有 usersession revoke。
- **S4**(前端解封后):登录页 + 后端驱动的面板分流 + admin/user 导航分离 + 移除 localStorage 手填 token;Playwright 守卫测。
- **S5**:后端授权加固(admin API 拒 user session、user API 拒 admin bearer、tenant_operator 不串租户);判别 fixture 仅差 principal 类型+租户。
- **S6**:角色矩阵文档 + 登录审计事件 + 锁死管理员恢复路径。

每片:codex per-commit review ≤2 轮 + 全量 go test;schema migration 逐个 Owner 确认;钱路径不碰(新机领地);冻结包不加新文件。
