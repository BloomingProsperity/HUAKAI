# 登录按角色自动切面板 — Claude 独立计划草稿

日期 2026-05-30。Owner 选了「登录自动切面板」板块(AskUserQuestion)。本稿为 Claude 独立草稿,与 codex 草稿互不参照,后 synthesis。属 **auth core = HIGH risk**(Risk-Based Confirmation Rule),实现前需 Owner 确认认证模型。

## 现状(已 source-read,file:line)
- `users` 表无 role/is_admin 列:`backend/sql/migrations/0007_l0_inbound_auth.up.sql:18-28`(仅 id/tenant_id/email/display_name/status/时间戳)。
- 用户 session 身份无角色:`backend/internal/auth/session_middleware.go:15`(SessionIdentity = TenantID/UserID/FamilyID/TokenID/Generation)。
- admin 是独立凭据世界:`backend/sql/migrations/0010_admin_auth.up.sql:30-52`(admin_tokens,role platform_admin/tenant_operator,bcrypt hk_admin_* bearer,**无 user_id FK,不引用 users**;注释 CMB-1 刻意让 inbound resolver 远离此表)。
- **当前无用户登录流**:`backend/internal/gatewayhttp/session_handler.go:32-35` 只暴露 /refresh /revoke /list(均需已有 session);usersession 只有 CreateFamily,无 password/oauth 登录入口。
- 前端无 auth:`/home/codex/wt-quota/frontend` = Next.js 14 操作台(app/{accounts,observability,audit,mimicry,selection,bindings,dashboard,chat}),无 login 页、无角色门禁,dashboard 走 mock(`frontend/lib/dashboard-mock.ts`)。

**结论:Owner 设想的「统一登录 + 账号角色 → 切面板」地基不存在。** 这板块要先定认证模型,再建登录流 + 角色信号 + 面板分离 + 导航守卫。

## 核心岔路(必须 Owner 定)：认证模型
| 模型 | 做法 | 改动面 | 风险 | 参考对照 |
|---|---|---|---|---|
| **M1 统一账号+角色** | users 加 role 列(或 user_admins 桥),一个登录,session 带 role,前端按 role 切 | users schema + SessionIdentity + 新建用户登录流 + 前端 | 改 auth core + schema gate(HIGH);但单登录 UX 最干净、最贴 Owner 原话 | new-api@20d3e73:user.go:30 `Role int`(1/10/100);sub2api@91da8159:user.go:46 role 字符串 — **两主流都用 M1** |
| **M2 两套凭据各登录** | admin 维持 hk_admin token 登录;user 另建登录;面板=进哪个门 | 最小后端改(users 不动);两登录页 | 低 schema 风险;但 admin 仍用裸 token UX 差,两套门面 | CLIProxyAPI:MANAGEMENT_PASSWORD 单口令守 management(`cmd/server/main.go:253`)≈ 简化版 M2;无多角色 |
| **M3 桥接** | admin_tokens 仍独立,加可选 admin_token.user_id(或 user_admins 表);user session 可被识别为 admin → 统一登录 UX 不塌两世界 | users 不动,admin_tokens 加列/加桥表 + session 解析时查桥 | 中;保留 CMB-1 隔离同时给统一 UX | 无参考直接等价(new-api/sub2api 直接 M1 合并;HUAKAI 双世界是自研架构,M3 是其上的最小升级) |

## Claude 推荐：M3(桥接),理由
- HUAKAI 已有的双世界(CMB-1 隔离)是有意的信任链架构,M1 强行合并会冲掉 inbound-resolver 远离 admin_tokens 的安全隔离;
- M3 给 Owner 想要的「一个登录按角色切面板」UX,同时不塌掉隔离:user 登录拿 session → 解析时查 user_admins 桥 → 若该 user 绑定了 platform_admin 角色则 session 标 admin;
- 但 M3 比 M1 多一层桥查,且需明确「员工」语义(tenant_operator?还是仅 platform_admin/非admin 二分)。
- **若 Owner 只要最快见效**:M2 最小改(admin 已有 token,只缺前端两登录页 + 路由),但 admin UX 差、不贴原话。

## 切片路线(以 M3 为例,Owner 改选则重排)
1. **S1 后端角色信号(数据/服务层,休眠)**:user_admins 桥表 migration(Owner-gated schema)+ 解析:给定 user session 判定 is_platform_admin。判别测:绑定→admin、未绑定→普通、跨租户不串。
2. **S2 用户登录流**:用户凭据→session 签发 endpoint(当前缺);登录响应/`/v1/users/me` 返回 role 信号。
3. **S3 前端登录页 + 角色路由守卫**:login 页 → 按 role 重定向 admin 控制台 vs 用户面板;员工≠admin 零管理入口(导航守卫 + 后端 admin API 仍 platform_admin 强制,双层防越权)。
4. **S4 用户面板**(若无):普通用户的最小面板(用量/余额/订阅/兑换,接已 land 的 payment/subscription)。

## 风险/纪律
- auth core + schema = HIGH,每 schema migration Owner 确认;
- 越权是头号风险:前端守卫 + 后端 platform_admin 强制双层,判别测注入「员工访问 admin API → 403」「员工 session → 前端无 admin 入口」;
- 前端此前搁置,Owner 选本板块即解封前端(需确认);
- 实现走 codex per-commit review ≤2 轮 + 全量 go test。

## 待 Owner 决策点
1. 认证模型 M1/M2/M3?(Claude 倾向 M3)
2. 「员工」= tenant_operator 也算半管理(看部分管理页),还是只 platform_admin/普通用户二分?
3. 是否同时要建用户登录流(S2)+ 用户面板(S4),还是本板块只做「角色识别 + 面板路由切换」骨架?
