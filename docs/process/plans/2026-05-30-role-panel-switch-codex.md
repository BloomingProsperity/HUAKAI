# 2026-05-30 Role Panel Switch Codex Plan

| Owner directive | "管理员账号登录→自动管理员面板;员工/普通用户登录→普通用户面板;员工≠admin 不给任何管理入口。" |
| Scope | 独立规划「登录按角色自动切面板」。只规划认证模型、越权防线、切片、测试和 Owner gates；不执行代码。 |
| Success criteria | Owner 能选择认证模型；每个切片可独立验证；普通/员工身份不能看到或调用管理能力；管理员身份能进入正确面板；tenant scoping 不串租户。 |
| Time estimate | 规划 0.5 天；执行按模型不同约 4-9 个小切片，每片 0.5-2 天。 |
| Blast radius | 认证、前端路由、admin API 授权、session 生命周期。高风险触点是 schema、auth core、admin token 隔离；money path 不应触碰。 |
| Failure modes | 前端只藏菜单但后端放行；本地存储伪造角色；tenant_operator 语义误判；admin token 与 user session 混用；用户登录补丁绕开现有 session revocation。 |
| Decision points | 认证模型、员工语义、前端是否解封、登录凭据类型、tenant_operator 是否有面板、是否接受 schema gate。 |
| Pre-execution checklist | 先拿 Owner 模型选择；确认前端解封；确认 schema gate；列出目标包且不向 `backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 新增文件；先写判别测试。 |

## Clean-Room Lane Metadata

- Lane: specifier.
- Reference scope: CLIProxyAPI + sub2api + new-api.
- Observed regions: 20.
- Inferences: 8.
- Open questions: 7.
- Clean-room rule: 本计划只记录行为和风险；不复制非 MIT 参考项目源码、字段设计、路由结构、UI 源码、注释或测试。参考项目标识符只出现在 file:line 证据锚和 `Source files read` 尾注。

## Ground Truth

HUAKAI 当前不是「统一登录 + 账号角色」体系。`users` 只有 tenant/user 基础身份，没有角色、管理员位、密码列或 OAuth 绑定列（backend/sql/migrations/0007_l0_inbound_auth.up.sql:18）。用户 session identity 只携带 tenant/user/session family/token/generation，没有角色（backend/internal/auth/session_middleware.go:15）。admin 凭据是独立表，包含 platform-wide 与 tenant-scoped 两类 operator，且注释明确把 inbound resolver 与 admin credential 隔离（backend/sql/migrations/0010_admin_auth.up.sql:20）。现有 session handler 只有 refresh/revoke/list，没有 password/OAuth login（backend/internal/gatewayhttp/session_handler.go:32）。

前端也不是登录产品。现在是 Next.js 操作台页面集合，API client 从 localStorage 取 admin bearer（frontend/lib/api/client.ts:4），首页提示手工写入 admin/customer 两类 token（frontend/app/page.tsx:134），Header 也直接读 admin bearer 访问后端心跳（frontend/components/layout/Header.tsx:39）。因此本板块不是「补一个 redirect」；先要决定 HUAKAI 是否继续双认证世界，还是建立统一账号角色体系。

## Authentication Model Options

### Option A: 统一账号 + 角色合并

做法：把登录主体统一到 `users`，为用户身份增加权限等级或独立 membership/role 表；登录成功后 `/auth/me` 返回 panel entitlement；前端只根据后端返回的 entitlement 进入 admin/user 面板；后端所有管理 API 仍单独执行授权，不信前端路由。

改动面：schema gate 必触；auth core 必触；现有 admin token 要迁移、桥接或废弃；`SessionIdentity` 需要携带可验证的权限版本或通过后端 introspection 查询；前端要新增 login、guard、admin/user layout split。禁止向 frozen packages 新增文件；如需 mount route，只改既有 mount 文件，核心能力放新包。

参考对照：New API 观察到同一登录体系把用户身份、权限等级和会话结果放在一个登录闭环里，并用权限等级保护后端管理路由与前端受限页面（QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/user.go:33；QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/auth.go:36；QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/routes/_authenticated/channels/index.tsx:35）。这只能作为行为证据，不能借用其 schema、路由组织、UI 源码或字段命名。

风险：最贴近 Owner 的「账号角色」表述，但与 HUAKAI 当前 CMB-1 admin/inbound 隔离相冲突，容易把 admin bearer、customer API key、user session 三者混淆。若直接把 admin 权限塞进 `users`，会把 schema、auth core、admin 审计、session revocation 一次性放大。

### Option B: 维持两套凭据，各自登录，同一入口自动切面板

做法：保留现有 admin token 世界；新增普通用户登录世界。登录页可以是一个入口，但认证后端先判定 credential class：admin credential 成功则返回 admin principal；user credential 成功则返回 user principal。前端按后端 principal 自动跳转。员工/普通用户没有 admin entitlement；tenant_operator 是否进入 scoped admin 面板由 Owner 单独决定。

改动面：普通用户登录仍需 schema gate，因为现有 `users` 没有密码或 OAuth 登录基础；admin 侧可先用现有 admin token 做 web login/introspection，后续再换成 httpOnly session。后端管理 API 继续只接受 admin principal；用户 API 只接受 user session。最小化对现有 admin isolation 的破坏。

参考对照：CLIProxyAPI 本次未观察到「账号角色切面板」等价物；它更像 CLI/provider login 与服务模式入口，而不是多角色运营面板产品（CLIProxyAPI-local-no-git:cmd/server/main.go:56）。因此它是无等价对照：不能证明统一面板模型，反而提醒 HUAKAI 不应把 provider OAuth 登录误当成人员权限登录。

风险：产品上不是严格「一个账号表多角色」，但最符合 HUAKAI 现状，能先关闭越权风险。主要风险是 UX 需要解释 admin/user credential 差异；如果 Owner 坚持一个人员账号登录后既可能是 admin 又可能是普通用户，应转 Option A 或 C。

### Option C: 桥接 admin 与 user，但保留隔离

做法：保留 admin token 独立凭据表，同时建立 admin principal 与 user identity 的桥接关系。登录后统一返回 principal；admin 权限仍从 admin credential/bridge policy 解析，不直接由普通 user session 自行升级。普通用户仍不能进入 admin；tenant_operator 可在 scoped admin 面板与普通用户面板之间按策略切换。

改动面：需要 schema gate 增加桥接关系或 entitlement 表；需要严格审计 bridge 创建/撤销；需要 session generation/entitlement version，避免撤权后旧 session 继续保留 admin 面板。前端要支持一个身份多面板但默认落到最高安全默认值。

参考对照：sub2api 观察到管理入口可接受两类管理证明：一种是独立管理密钥，一种是登录用户令牌通过后端管理员检查；所有 admin routes 先经过 admin middleware，再进入管理资源（Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/middleware/admin_auth.go:23；Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/routes/admin.go:17）。它也有普通用户路由与管理路由分组分离（Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/routes/user.go:18），前端路由会把管理页面标为需要管理员并在导航时重定向非管理员（Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:frontend/src/router/index.ts:720）。

风险：最灵活，但最容易产生 confused-deputy：普通用户 session、admin credential、tenant scope 的关系若定义不严，会出现「员工误变 operator」或「tenant_operator 串租户」。需要比 Option B 更多测试和审计。

## Recommendation

推荐先走 Option B，明确作为「安全等价第一阶段」：同一登录入口可以自动切面板，但后端仍保留 admin/user 两类 principal，不急于合并身份表。理由：

1. HUAKAI 当前已刻意隔离 admin credential 与 inbound/customer identity，Option B 不破坏这条安全边界。
2. Owner 的头号风险是员工误见/误用管理面板；Option B 的默认态最容易做到 deny-by-default。
3. 普通用户登录地基不存在，任何模型都要补 user login。Option B 让 user login 与 admin isolation 分别闭合，不把 schema、auth core、admin migration 合成一个大爆炸。
4. 若 Owner 后续要求「同一人员账号既是普通用户又可能有管理权限」，再把 Option C 作为第二阶段桥接，而不是直接跳到 Option A。

## Authorization Defense

前端只是体验层，不能是安全边界。登录后前端必须调用后端 `current principal` 合约拿到 `panel=user|admin|none`、tenant scope、operator scope、session expiry；禁止从 localStorage 自造角色。普通/员工身份下不渲染管理导航，直接访问 `/admin/*` 必须重定向到用户面板或 403 页面；已登录普通用户访问 login 不应被错误带进 admin。

后端才是强制边界。所有 `/admin/v1/*`、`/debug/*`、ops dashboard、provider account、quota/admin observability 入口继续使用 admin authorization；普通 user session 或 customer API key 调用必须 401/403。所有用户面板 API 只允许读取当前 tenant/current user 的数据；tenant_operator 如果被 Owner 定义为半管理，只能访问 scope_tenant_id 内资源。admin principal 与 user principal 在 request context 中必须类型分离，不能用一个 boolean 或字符串角色贯穿所有路径。

判别测试必须能抓住真实越权：伪造前端存储为 admin 但后端 principal 是普通用户时，前端不出现管理入口，后端 admin API 仍 403；tenant_operator scoped to tenant A 请求 tenant B 管理资源时 403；platform_admin 能访问跨租户只读管理资源；普通用户直接请求 `/admin/v1/provider-accounts` 不能因为有有效 user session 而成功；admin token 不能被 customer chat/user API 当成普通 key。

## Employee Semantics

Owner 需要明确「员工」是不是 HUAKAI 现有 `tenant_operator`。

- Semantics 1: 员工 = 普通 tenant member。员工与普通用户一样进入 user panel，不给任何管理入口。`tenant_operator` 仍是单独的 operator 概念。
- Semantics 2: 员工 = tenant_operator。员工进入 scoped admin panel，可管理本 tenant 的账号/观测/低风险配置，但不能平台级管理。
- Semantics 3: 三分法。platform_admin 进全局 admin；tenant_operator 进 scoped admin；employee/user 进普通 user panel。

推荐 Semantics 3。理由是现有 admin_tokens 已经把 platform_admin 与 tenant_operator 写成管理凭据，不应把 tenant_operator 重新解释成「普通员工」。如果 Owner 口中的员工明确是「不能管理的人」，则员工应是 member/user，不是 tenant_operator。

## Slice Route

### Slice 0: Owner Gates And Contract Freeze

- Files: docs only.
- Gates: Owner must choose Option A/B/C, employee semantics, login credential type, frontend unblock.
- Checks: no code. Verify plan has no feature shrinkage and no clean-room leakage.
- Risk: if skipped, later slices会把「员工」和「tenant_operator」混成同一类。

### Slice 1: Principal Contract And Backend Test Harness

- Target packages: new package outside frozen packages, e.g. `backend/internal/principal` or `backend/internal/panelauth`; existing route mount may need a small edit, but no new file under `gatewayhttp`, `gateway`, or `proto`.
- Schema gate: no.
- Money path: no touch.
- Behavior: define backend principal categories: anonymous, user, platform admin, tenant-scoped operator. Define `GET /auth/me` or equivalent contract returning only entitlement, identity ids, tenant scope, expiry, and audit-safe metadata.
- Tests: table tests for anonymous/user/admin/operator; forged role input ignored; expired/revoked admin token yields no admin panel.
- Verification: `go test` for new package plus focused handler tests.

### Slice 2: Admin Web Login / Introspection Without User Merge

- Target packages: admin auth helper package plus existing admin route wiring only if needed.
- Schema gate: no if using current admin bearer introspection; yes if adding server-side admin web sessions.
- Frozen package: no new files in frozen packages.
- Behavior: admin enters admin credential, backend validates through existing admin credential resolver, returns admin principal; tenant_operator result includes tenant scope. Do not expose admin token to normal user APIs.
- Tests: platform_admin success; tenant_operator success with scope; disabled/revoked/expired admin credential fail; admin credential cannot be consumed by user panel APIs.

### Slice 3: User Login Foundation

- Target packages: new user auth package and usersession integration.
- Schema gate: yes. Current `users` table has no login secret, so Owner must choose password login, OAuth-only, or external IdP. Prefer a separate user credential/identity table over overloading admin_tokens.
- Frozen package: route mount may modify existing file only; no new frozen-package files.
- Behavior: successful user login creates existing usersession family; `/auth/me` returns user panel entitlement; user logout/revoke uses existing session revocation semantics.
- Tests: ordinary user login -> user panel; disabled/deleted user cannot login; password/OAuth failure does not create session; user session has no admin entitlement.

### Slice 4: Frontend Auth Shell And Route Guards

- Frontend unblock: requires explicit Owner confirmation because prior frontend work was postponed until Rust four waves.
- Target files: `frontend/app/login/page.tsx`, `frontend/lib/auth/*`, `frontend/components/layout/*`, route guard/middleware if adopted. No backend core.
- Behavior: one entry screen, backend-driven panel selection, admin and user nav separated. localStorage role is not trusted; admin token manual setup copy is removed or hidden behind dev-only flow after backend login exists.
- Tests: Playwright route tests for admin login -> admin dashboard, user login -> user dashboard, direct `/admin/*` as user -> no admin UI, forged localStorage role -> still user panel.

### Slice 5: Backend Authorization Hardening

- Target packages: existing admin gate tests plus new package tests; no schema unless earlier slices need it.
- Frozen package: modifying existing tests allowed; no new frozen package files unless the test already belongs there and Owner accepts structure risk. Prefer new package tests around authorization helpers.
- Behavior: admin API rejects user session; user API rejects admin bearer where current CMB-1 expects separation; tenant_operator cannot cross tenant; platform_admin paths are explicit and audited.
- Tests: discriminating fixtures where the only difference is principal kind and tenant scope. Avoid weak `not bad` assertions; assert exact 401/403 and exact success path.

### Slice 6: UX Closeout And Audit Notes

- Target files: docs + frontend copy.
- Schema gate: no.
- Behavior: document final role matrix, login states, and recovery path for locked-out admin. Add admin audit event for login if backend session login is added.
- Tests: e2e smoke with screenshots or DOM assertions; backend audit assertion if login audit exists.

## Frontend Unblock Confirmation

This board cannot honestly finish without frontend work. Owner previously postponed frontend until Rust four waves; this plan needs explicit Owner confirmation to unblock frontend for auth shell, route guards, login page, and panel-specific nav. If Owner does not unblock frontend, only backend contract/tests can proceed and the user-visible requirement remains Mandatory Roadmap, not Implemented.

## Owner Decision List

1. Choose authentication model: A unified account roles, B dual credential worlds with one entry, or C bridge with isolation. Recommendation: B first, C later if one-account operator UX becomes mandatory.
2. Define employee: ordinary user, tenant_operator, or separate member role. Recommendation: three-way split: platform_admin / tenant_operator / user-or-employee.
3. Decide tenant_operator panel: no panel, scoped admin panel, or user panel plus limited delegated actions. Recommendation: scoped admin only if existing tenant_operator semantics remain valid.
4. Approve schema gate for user login. Required unless Owner accepts admin-only panel switching first.
5. Choose user login credential type: password, OAuth/OIDC, or external IdP. Recommendation: OIDC/password decision should be separate security review; do not invent silently.
6. Approve frontend unblock for login/route guard work.
7. Decide whether admin web login may initially validate existing admin bearer, or must immediately move to httpOnly session/cookie.

## Assumptions And Risks

- Assumption: Owner wants no feature shrinkage; if unified roles are deferred, the plan must label dual-login as Safe Equivalent, not deletion.
- Assumption: admin panel means operational management surface, not customer chat/API key usage surface.
- Risk: Option A copied from reference behavior would create clean-room and license risk if implementation follows upstream structure. Mitigation: design HUAKAI-native principal model and cite references only as behavior evidence.
- Risk: schema changes can destabilize auth/session tests. Mitigation: slice schema separately and require Owner gate.
- Risk: tenant_operator ambiguity can ship a privilege bug. Mitigation: no implementation until Owner chooses semantics.
- Risk: frontend-only guard can be bypassed. Mitigation: backend authorization tests are blocking.

## Source files read

HUAKAI:
- backend/sql/migrations/0007_l0_inbound_auth.up.sql
- backend/internal/auth/session_middleware.go
- backend/sql/migrations/0010_admin_auth.up.sql
- backend/internal/gatewayhttp/session_handler.go
- frontend/lib/api/client.ts
- frontend/app/page.tsx
- frontend/components/layout/Header.tsx

Reference projects:
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/auth.go
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/user.go
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:router/api-router.go
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/routes/_authenticated/channels/index.tsx
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/routes/_authenticated/system-settings/route.tsx
- QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:web/default/src/lib/roles.ts
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/middleware/jwt_auth.go
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/middleware/admin_auth.go
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/routes/admin.go
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/internal/server/routes/user.go
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:backend/ent/schema/user.go
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:frontend/src/router/index.ts
- Wei-Shaw/sub2api@91da815993732e6536be8c702168822e482cd850:frontend/src/stores/auth.ts
- CLIProxyAPI-local-no-git:cmd/server/main.go

Lane: specifier
Agent: GPT-5 Codex
UTC timestamp: 2026-05-30T16:35:12Z

中文摘要：本计划基于 HUAKAI 当前「普通用户 session 无角色、admin token 独立、前端无登录」的真实观察，提出统一账号、双凭据安全等价、桥接隔离三种模型；合理推断是先走双凭据模型最少破坏现有安全边界，再按 Owner 决策升级桥接；open questions 共 7 个，最高优先级是认证模型、员工语义、schema gate 与前端解封。没有功能缩水：未删除统一登录目标，只把高风险合并列为 Owner-gated 后续；clean-room 风险已通过行为化转述和 file:line 证据控制；安全风险集中在越权、串租户和角色误判，计划要求前后端双层防线与判别测试。Owner 需要先确认模型、员工定义、tenant_operator 权限、用户登录凭据类型、schema gate、前端解封和 admin web session 策略。
