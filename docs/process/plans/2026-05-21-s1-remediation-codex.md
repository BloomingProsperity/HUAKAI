# 2026-05-21 §1 用户与权限 remediation plan - Codex parallel draft

| Owner directive | "Independently draft the §1 (用户与权限 / Users & Permissions) remediation plan for HUAKAI." |
| Scope | 只规划 §1 用户与权限的 remediation；包括用户组/权限组、多租户管理面、注册增强、API key 自助与令牌级策略。 |
| Out of scope | 不写生产代码、不新增 migration、不修改 OpenAPI、不改测试；本文件不读取 Claude parallel draft。 |
| Success criteria | Owner 能用本计划和 Claude draft 交叉比较，并形成 synthesized plan；每个已知 §1 gap 都有依赖顺序、文件范围、风险和确认点。 |
| Time estimate | 计划撰写：1 个 Codex session；未来执行合计约 12-20 个工程日，取决于 Owner 对 schema、2FA、令牌配额策略的决策。 |
| Blast radius | 未来执行会触碰 auth hot path、admin handlers、schema/sqlc、OpenAPI、用户登录体验、gateway request admission。 |
| Failure modes | schema 过早固化导致 §4 pool binding 返工；token policy 读路径增加 hot path latency；2FA 破坏现有 session refresh；默认权益初始化误发权限。 |
| Decision points | `user_groups` schema、是否启用 2FA、captcha provider、是否注册即发默认 API key、token quota 是否进入 billing/claim gate。 |
| Pre-execution checklist | Owner 批准 synthesized plan；确认 new-api license 分类冲突；先写 acceptance tests；schema 先走 migration review；auth/billing/quota 变更单独高风险确认。 |

## Clean-room / truth note

- 本 draft 没有读取 `docs/process/plans/2026-05-21-s1-remediation-claude.md`；只看到了该文件名存在，未打开内容。
- sub2api 按任务约束没有重新读源码，只使用 `docs/process/research/2026-05-21-audit-A.md` 中已经 paraphrase 的 §1 证据；该 audit 说明 sub2api 的 group 覆盖用户、令牌、账号、模型路由和限制策略，见 `docs/process/research/2026-05-21-audit-A.md:51`。
- 任务把 CLIProxyAPI + new-api 标成 MIT；本地观察到 CLIProxyAPI `LICENSE` 是 MIT，但 `/home/codex/refs/new-api/LICENSE:1` 是 AGPL。以下 new-api 证据只作为 specifier-lane 行为证据，不能作为 implementer 可复制设计来源。
- Metadata: Observed regions: 34 / Inferences: 9 / Open questions: 6.

## 1. 当前状态表

| leaf | status | gap |
| --- | --- | --- |
| 用户注册 / 登录 | 🟡 | HUAKAI 已有 tenant-scoped user、密码摘要、邮箱验证、邀请、社交登录、失败计数和 session 签发；注册接口只接收 tenant/email/display/password/invite，登录接口只接收 tenant/email/password，未观察到 second-factor challenge、captcha/anti-abuse challenge、注册后默认 entitlement 初始化。证据：`backend/sql/migrations/0020_user_authentication.up.sql:9`, `backend/sql/migrations/0020_user_authentication.up.sql:29`, `backend/sql/migrations/0020_user_authentication.up.sql:66`, `backend/sql/migrations/0020_user_authentication.up.sql:99`, `backend/internal/userauth/types.go:132`, `backend/internal/userauth/types.go:145`, `backend/internal/userauth/service.go:65`, `backend/internal/gatewayhttp/auth_handler.go:109`. |
| Session 会话 | ✅ / 🟡 | session family、session token、refresh token 都是 tenant/user scoped，支持 hash、generation、refresh replay 防御和设备/IP drift；但 2FA pending-login state 不存在，不能承接注册增强里的 second factor。证据：`backend/sql/migrations/0021_session_management.up.sql:9`, `backend/sql/migrations/0021_session_management.up.sql:33`, `backend/sql/migrations/0021_session_management.up.sql:56`, `backend/internal/usersession/rotation.go:76`, `backend/internal/auth/session_middleware.go:36`. |
| API key 管理 | 🟡 | 管理员签发/list/revoke 已完成，key hash/prefix/tenant/user/status/expiry 完整；但用户不能 self-CRUD 自己的 keys，`api_keys` 没有 per-token quota/IP/model/group policy，resolver 只返回 tenant/api_key/user identity。证据：`backend/sql/migrations/0007_l0_inbound_auth.up.sql:51`, `backend/internal/adminhttp/api_keys_handler.go:60`, `backend/internal/adminhttp/api_keys_handler.go:72`, `backend/internal/admin/issuer.go:92`, `backend/sql/queries/admin_api_keys.sql:16`, `backend/internal/auth/api_key_resolver.go:38`, `backend/sql/queries/auth_inbound.sql:18`. |
| 用户组 / 权限组 | ❌ | 只观察到 `routes` 上有 string matcher，API key 只绑定 user_id；未观察到 group entity、membership table、group CRUD、user-to-group lifecycle 或 API-key-to-group binding。证据：`backend/sql/migrations/0001_pool_routing.up.sql:222`, `backend/sql/migrations/0001_pool_routing.up.sql:227`, `backend/sql/migrations/0007_l0_inbound_auth.up.sql:51`, `backend/internal/registry/postgres_registry.go:118`. |
| 管理员权限 | 🟡 | `admin_tokens` 有 platform/tenant operator 分层和 tenant scope check；缺细粒度 permission group，当前只能用 coarse role 控制 admin capability。证据：`backend/sql/migrations/0010_admin_auth.up.sql:30`, `backend/sql/migrations/0010_admin_auth.up.sql:68`, `backend/internal/admin/operator_auth.go:52`, `backend/internal/admin/operator_auth.go:113`. |
| 多租户隔离 | 🟡 | schema 侧 tenant_id 很强，API key composite FK 防跨租户绑定，provider-account admin 会解析 operator scope；但 pool admin handler 对 list/create/get/update 都使用固定 tenant id，阻塞 SaaS 多租户运营。证据：`backend/sql/migrations/0001_pool_routing.up.sql:15`, `backend/sql/migrations/0007_l0_inbound_auth.up.sql:67`, `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:490`, `backend/internal/gatewayhttp/admin_pools_handler.go:20`, `backend/internal/gatewayhttp/admin_pools_handler.go:84`, `backend/internal/gatewayhttp/admin_pools_handler.go:112`, `backend/internal/gatewayhttp/admin_pools_handler.go:137`, `backend/internal/gatewayhttp/admin_pools_handler.go:186`. |

## 2. Schema recommendation

Recommendation: first remediation should use one canonical `user_groups` authority with `policy jsonb`, plus the necessary membership relation table. Do not introduce a separate `permission_groups` authority in §1.

解释：这里的 "single table" 是指 group/policy 的权威模型只有 `user_groups`；因为用户归属仍然需要关系表，所以执行时还需要 `user_group_memberships`。要避免的是同时引入 `user_groups + permission_groups` 两套可授权实体，导致 pool routing、API key policy、registration default entitlement 三处各认一套权限来源。

Why this pick:

1. CLIProxyAPI 没有 SaaS user-group/RBAC 模型；它更像本地代理，入站 key、管理 key、模型别名、client-key 到 upstream-key 映射和管理端配置项都来自配置/管理面。它能证明轻量配置策略有价值，但不能证明 HUAKAI 需要单独 `permission_groups`。证据：`router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/access/types.go:3`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/access/config_access/provider.go:55`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/handler.go:145`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:224`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:279`, `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:599`.
2. new-api 的行为证据显示，用户、令牌、渠道、模型-渠道可用性共享一个 group-like 维度，角色检查是另一条粗粒度 admin chain；它没有给出 "user group" 和 "permission group" 分离才更好的证据。注意：本地 license 为 AGPL，此处只取行为轮廓。证据：`QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/user.go:24`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/token.go:14`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/channel.go:23`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/ability.go:16`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/channel_satisfy.go:8`, `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/auth.go:36`.
3. HUAKAI 已经有 JSON-policy precedent：provider credentials、session device metadata、admin audit payload、model capability params 都使用 `jsonb` 承载扩展策略或审计上下文。`user_groups.policy jsonb` 可以在第一版覆盖 model allow/deny、pool binding hints、default token policy、registration entitlement，而 stable columns 只保留 tenant/code/status/default flags。证据：`backend/sql/migrations/0001_pool_routing.up.sql:126`, `backend/sql/migrations/0021_session_management.up.sql:18`, `backend/sql/migrations/0010_admin_auth.up.sql:87`, `backend/sql/migrations/0008_model_registry.up.sql:224`.
4. 两表方案的主要风险是过度建模：`permission_groups` 会和 `admin_tokens.role/scope_tenant_id`、API key policy、group route policy 形成三套授权系统。§1 的真实 blocker 是 "没有 group entity / membership / CRUD / pool binding"，不是缺一个独立权限模板系统。

Open schema questions for Owner:

- `user_groups.policy` 是否允许 tenant operator 修改，还是 platform admin only？
- 默认 group 是否每个 tenant 必须有且不可删除？
- 用户是否允许多组 membership，还是 primary group + optional extra entitlements？
- API key 是否默认继承 user primary group，还是每个 key 必须显式绑定 group？
- 默认注册权益是否包含默认 API key；如果包含，plaintext one-time display 如何交付？
- new-api license 分类应更正为 AGPL 后是否继续纳入 specifier evidence。

## 3. Remediation subtasks

| order | subtask | scope | files touched | blast radius | additive-or-destructive | estimate |
| --- | --- | --- | --- | --- | --- | --- |
| P0 | Owner decision gate + synthesized plan | 合并 Claude/Codex draft，确认 schema、2FA、captcha、默认 API key 和 token quota 边界。 | `docs/process/plans/2026-05-21-s1-remediation.md` or amended approved plan; maybe `docs/10_RISK_REGISTER.md`. | 无 runtime blast radius；决定错误会导致后续返工。 | Additive docs. | 0.5-1 day Owner/PM time |
| P1 | Acceptance tests before implementation | 为四个 verified gaps 写场景测试：group CRUD/membership、pool admin tenant scope、registration default entitlement/anti-abuse、user API key self-CRUD/policy deny。 | `backend/internal/gatewayhttp/*_test.go`, `backend/internal/userauth/*_test.go`, maybe `backend/internal/tokenpolicy/*_test.go`, docs acceptance matrix. | 测试失败会暴露现状，不改生产行为。 | Additive tests. | 1-2 days |
| P2 | Pool admin tenant-scope fix | 去掉 `admin_pools_handler.go` 的 fixed tenant id；复用 provider-account admin 的 scope resolution 语义，platform admin 必须显式 tenant query/body 或安全 default rule 由 Owner 决定。 | `backend/internal/gatewayhttp/admin_pools_handler.go`, `backend/internal/gatewayhttp/admin_pools_handler_test.go`, maybe `backend/cmd/gateway/routes.go`. | Admin pool CRUD；若 scope 解析错误会跨租户读写 pool group。 | Additive behavior fix, no schema. | 0.5-1.5 days |
| P3 | User group schema + sqlc | 新增 canonical group table + membership relation；每张表 tenant-scoped、soft delete、default group invariant、policy JSON check、composite FK 防跨租户 membership。 | `backend/sql/migrations/00xx_user_groups.up.sql`, `.down.sql`, `backend/sql/queries/user_groups.sql`, generated `backend/internal/db/*`, maybe `backend/sqlc.yaml`. | Schema high risk；错误 FK/unique 会阻塞 registration 和 API key issue。 | Additive schema, not destructive. HIGH confirmation required. | 1.5-3 days |
| P4 | Group domain service + admin CRUD | 提供 group list/create/update/disable、membership assign/remove/list；审计 admin 操作；禁止删除 default group，禁用前检查活跃 membership。 | new `backend/internal/usergroup/*`, new `backend/internal/gatewayhttp/admin_user_groups_handler.go`, tests, `backend/cmd/gateway/routes.go`, `docs/openapi/openapi.yaml`. | Admin Ops UI/API；错误权限会让 tenant operator 管到别的 tenant。 | Additive API. | 2-3 days |
| P5 | Registration default entitlement | 注册成功后在同一 transaction 分配 tenant default group；可选初始化 default entitlement record。默认 API key 是否自动创建先等 Owner 决策，建议第一版不自动发 plaintext key，只创建 group membership。 | `backend/internal/userauth/service.go`, `backend/internal/userauth/store.go`, `backend/internal/userauth/types.go`, `backend/internal/gatewayhttp/auth_handler.go`, tests. | Auth core medium/high；错误会让新用户无权限或过度授权。 | Additive behavior; no destructive migration. | 1-2 days |
| P6 | Registration anti-abuse | 在注册/login 前接入抽象 challenge verifier，支持 feature flag、tenant policy、remote IP、failure audit；先用 stdlib HTTP client，不新增 runtime dependency。 | `backend/internal/userauth/*`, `backend/internal/gatewayhttp/auth_handler.go`, config/wiring files, tests, maybe `docs/openapi/openapi.yaml`. | User signup/login；provider outage 可能误封注册。 | Additive feature flag. | 1.5-2.5 days |
| P7 | 2FA / MFA login step | 新增 user second-factor secret + backup code storage，secret 用 existing credential encryption pattern 或 dedicated encrypted payload；login 返回 pending challenge，不签发 session，验证通过后才 `usersession.Create`。 | new migration, `backend/internal/userauth/*`, `backend/internal/usersession/*` only if pending state stored there, `backend/internal/gatewayhttp/auth_handler.go`, tests, OpenAPI. | Auth core HIGH；错误会锁用户或绕过 second factor。 | Additive schema + behavior. HIGH confirmation required. | 3-5 days |
| P8 | API key self-service | 新增 session-authenticated `/v1/users/me/api-keys` CRUD；复用 issuance hash/one-time plaintext rules，但 caller 是 current session user，tenant/user 不从 body 信任。 | new `backend/internal/apikey/*` or refactor `backend/internal/admin/issuer.go`, new `backend/internal/gatewayhttp/user_api_keys_handler.go`, `backend/cmd/gateway/routes.go`, tests, OpenAPI. | API key lifecycle；错误会泄漏 plaintext or cross-user keys。 | Additive API; refactor risk medium. | 2-3 days |
| P9 | Token-level policy enforcement | 为 API key 增加 policy source，先 enforce IP/CIDR + model allow/deny + group inheritance；per-token quota 要接 billing/claim gate 或独立 reservation，不能只做 best-effort counter。 | schema/query files, new `backend/internal/tokenpolicy/*`, `backend/internal/auth/api_key_resolver.go` if identity expands, `backend/internal/gatewayhttp/chat_completions_dispatch.go`, `backend/internal/billing/claim_gate.go` if quota included, tests. | Gateway hot path + quota/billing high risk；错误会误放行、误拒绝或双扣 quota。 | Additive but hot-path behavior change. HIGH if quota enforcement included. | 3-5 days |
| P10 | Docs / parity / risk closure | 更新 §1 state-tree disposition、risk register、OpenAPI contracts、admin/user scenario docs；标明每个 gap closure 是 Implemented / Implemented Better / Safe Equivalent。 | `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/10_RISK_REGISTER.md`, `docs/08_REAL_WORLD_SCENARIOS.md`, `docs/openapi/openapi.yaml`, release gate notes. | Docs only；错误会误导 release gate。 | Additive docs. | 0.5-1 day |

## 4. Sequencing

First subtask: P0 Owner decision gate must happen before code because P3 is schema HIGH risk and P7 touches auth-core HIGH risk. After P0, P1 tests should be written before implementation so the current gaps stay visible and future workers do not silently shrink scope.

Execution order I recommend:

1. P0 -> P1.
2. P2 can run immediately after P1 because it is isolated, closes the known tenant hardcode, and does not depend on group schema.
3. P3 must precede P4/P5/P8/P9 because group id/policy semantics become the shared contract.
4. P4 follows P3 to make groups operable.
5. P5 follows P3/P4 because registration needs a default group lifecycle.
6. P8 follows P3/P5 because user API keys should inherit or explicitly bind group policy.
7. P9 follows P8 and should split model/IP enforcement from quota enforcement; quota enforcement should be its own review slice if it touches billing/claim.
8. P6 and P7 can run parallel to P4/P8 only if they own disjoint files; in practice both touch `auth_handler.go` and `userauth`, so run them sequentially or assign one worker only.
9. P10 runs after each subtask incrementally, with final closure at the end.

Parallelizable after P1:

- P2 tenant-scope fix can run in parallel with P3 schema drafting because write sets are mostly disjoint.
- P6 anti-abuse design can be drafted in docs while P3/P4 execute, but code should wait if P5/P7 also edit auth files.
- OpenAPI/docs updates can be staged alongside each implementation slice, but final parity closure should wait for tests.

## 5. Decision points needing Owner sign-off

| decision | why Owner sign-off is needed | recommendation |
| --- | --- | --- |
| New schema for groups and memberships | AGENTS marks database schema as HIGH risk. | Approve `user_groups + user_group_memberships`, reject separate `permission_groups` for §1. |
| Group policy mutability | Policy JSON can grant models/pools/quotas; tenant operator edit rights may become privilege escalation. | Platform admin can define templates; tenant operator can assign memberships and edit low-risk display fields first. |
| Default registration entitlement | Wrong default can overgrant paid capacity or block all new users. | Default group membership yes; default API key no until Owner explicitly wants one-time plaintext delivery. |
| 2FA / MFA | Touches auth core, session issuance, account recovery. | Implement after group schema and self-service baseline; require recovery-code and admin unlock plan before enabling by default. |
| Captcha provider | External verifier affects signup reliability and stores provider secret. | Abstract verifier + feature flag; fail-closed only when provider is configured and healthy policy says so. |
| Per-token quota enforcement | Touches quota/billing/claim gate, marked high-risk by AGENTS. | Implement model/IP policy first; quota in a separate billing-reviewed slice. |
| new-api license classification | Local source license conflicts with task label. | Treat new-api as AGPL clean-room evidence unless Owner supplies a MIT fork/commit. |

## 6. Risk register

| risk | severity | what could go wrong | mitigation |
| --- | --- | --- | --- |
| R-S1-SCHEMA-001 group schema mismatch | HIGH | §4 pool binding later needs different group cardinality or policy shape. | Use single canonical group table with flexible JSON policy; keep stable constraints minimal; write migration rollback. |
| R-S1-TENANT-001 pool admin cross-tenant write | HIGH | Platform/tenant operator creates or updates pool in wrong tenant. | Reuse scoped admin identity helper; require explicit tenant when platform admin has no scope; tests for wrong tenant 403/404. |
| R-S1-AUTH-001 2FA bypass or lockout | HIGH | Login path signs session before second factor or users cannot recover. | Pending-login state must be short-lived and cannot call `usersession.Create`; backup codes; admin recovery flow; feature flag rollout. |
| R-S1-KEY-001 plaintext API key leak | HIGH | Self-service endpoint logs or replays plaintext. | Reuse one-time response semantics, redacted Stringer, audit without secret, tests that plaintext absent from audit/error. |
| R-S1-POLICY-001 token policy not enforced in all protocols | MED/HIGH | Chat completions enforces policy but messages/responses path bypasses it. | Put token policy gate in shared chat execution path used by all compatible endpoints; tests for `/v1/chat/completions`, `/v1/responses`, `/v1/messages`. |
| R-S1-QUOTA-001 double count / missed count | HIGH | Per-token quota decrements outside billing claim, causing drift. | Do not implement quota as ad hoc counter; integrate with claim gate in separate slice. |
| R-S1-CLEANROOM-001 AGPL leakage | HIGH | new-api or sub2api schema/source names leak into HUAKAI implementation. | Use behavior-only spec language; implement from HUAKAI schema vocabulary; reviewer-lane checks CL-001..010 before code. |
| R-S1-PERF-001 hot path extra DB read | MED | API key policy lookup adds latency per request. | Start with bounded indexed lookup by api_key/tenant; later cache policy snapshot by key id + updated_at. |
| R-S1-OPS-001 captcha provider outage blocks signup | MED | External challenge verifier downtime prevents user acquisition. | Feature flag, health-aware fail policy, admin override, metrics/audit. |

## 7. Implementation guardrails for future workers

- 不要把 new-api/sub2api 的 schema、字段名、handler shape 或文件结构搬进 HUAKAI；只保留用户 outcome：group entitlement、token policy、self-service key lifecycle、2FA/captcha/default entitlement。
- 不要让 auth resolver 承担 billing/quota 写入；`auth` 现在是 read-only layer，现有注释把 last_used 写入都推迟了，见 `backend/sql/queries/auth_inbound.sql:1`.
- 不要把 `tenant_id` 从 user body 作为 truth；self-service path 必须从 session identity 取 tenant/user。
- 不要自动给注册用户发 plaintext API key，除非 Owner 明确确认交付方式和风控默认值。
- 每个 subtask 完成后更新 parity/risk/docs，不要等最后一次性补。

## 8. Source coverage proof

HUAKAI regions read and contribution:

- `docs/RULES.md:22` - Owner start gate / clean-room / parity / tech-stack rules.
- `docs/process/research/2026-05-21-audit-A.md:22` - verified §1 gap inventory and paraphrased sub2api evidence.
- `backend/sql/migrations/0001_pool_routing.up.sql:15` - tenant table and route/pool schema, including string group match.
- `backend/sql/migrations/0007_l0_inbound_auth.up.sql:18` - users/api_keys tenant/user binding baseline.
- `backend/sql/migrations/0010_admin_auth.up.sql:30` - admin token roles and audit table.
- `backend/sql/migrations/0020_user_authentication.up.sql:9` - user auth fields and signup support tables.
- `backend/sql/migrations/0021_session_management.up.sql:9` - session family/token/refresh schema.
- `backend/sql/migrations/0008_model_registry.up.sql:160` - model-to-pool binding and JSON policy precedent.
- `backend/sql/queries/admin_api_keys.sql:8` - admin key issue/list/revoke SQL shape.
- `backend/sql/queries/auth_inbound.sql:8` - API key resolver lookup shape.
- `backend/sql/queries/pools.sql:3` - pool CRUD requires tenant id but handler hardcodes it.
- `backend/internal/gatewayhttp/admin_pools_handler.go:20` - fixed tenant id in pool admin.
- `backend/internal/gatewayhttp/admin_pool_accounts_handler.go:490` - scoped provider-account admin helper.
- `backend/internal/adminhttp/api_keys_handler.go:60` - admin-only key endpoints.
- `backend/internal/admin/issuer.go:92` - admin issue pipeline.
- `backend/internal/admin/revoker.go:45` - admin revoke pipeline.
- `backend/internal/admin/operator_auth.go:52` - admin resolver and tenant permission check.
- `backend/internal/auth/api_key_resolver.go:38` - resolved API key identity has no policy.
- `backend/internal/auth/session_middleware.go:36` - session middleware.
- `backend/internal/userauth/service.go:65` - register/authenticate behavior.
- `backend/internal/userauth/types.go:132` - register/login request shapes.
- `backend/internal/userauth/store.go:69` - user creation store shape.
- `backend/internal/usersession/rotation.go:76` - refresh/replay behavior.
- `backend/internal/gatewayhttp/auth_handler.go:109` - auth route surface.
- `backend/cmd/gateway/routes.go:67` - mounted auth/session/user/admin routes.
- `backend/internal/registry/postgres_registry.go:118` - tenant model binding resolution.

Reference regions read and contribution:

- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:sdk/access/types.go:3` - config-shaped request authentication.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/access/config_access/provider.go:55` - multiple inbound key sources.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/handlers/management/handler.go:145` - management key model.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:224` - routing/session-affinity config surface.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/config/config.go:279` - client-key to upstream-key mapping surface.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:599` - management API key routes.
- `router-for-me/CLIProxyAPI@21fad9dbb447a2ab70d51d0ac3e3d032525a6054:internal/api/server.go:682` - model exclusion/alias management routes.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/user.go:24` - user carries quota/role/status/group-like entitlement.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/token.go:14` - token carries quota/model/IP/group-like policy.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/channel.go:23` - channel carries group/model routing surface.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/ability.go:16` - group+model+channel eligibility relation.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/channel_satisfy.go:8` - group/model/channel eligibility check.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/auth.go:36` - coarse role/session authorization.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/auth.go:351` - token IP restriction behavior.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/token.go:167` - user-side token create/update/delete behavior.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/user.go:68` - second-factor pending login branch.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/user.go:200` - default token initialization after registration.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:middleware/turnstile-check.go:17` - captcha-like challenge middleware.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:model/twofa.go:13` - second-factor persistence shape.
- `QuantumNous/new-api@20d3e73734527cded251aff23202dfbf5a2584ca:controller/twofa.go:408` - pending-login second-factor verification.

Open questions: 6, listed in §2.

Source files read: docs/RULES.md; docs/process/research/2026-05-21-audit-A.md; backend/sql/migrations/0001_pool_routing.up.sql; backend/sql/migrations/0007_l0_inbound_auth.up.sql; backend/sql/migrations/0010_admin_auth.up.sql; backend/sql/migrations/0020_user_authentication.up.sql; backend/sql/migrations/0021_session_management.up.sql; backend/sql/migrations/0008_model_registry.up.sql; backend/sql/queries/admin_api_keys.sql; backend/sql/queries/auth_inbound.sql; backend/sql/queries/pools.sql; backend/internal/gatewayhttp/admin_pools_handler.go; backend/internal/gatewayhttp/admin_pool_accounts_handler.go; backend/internal/gatewayhttp/admin_credentials_handler.go; backend/internal/adminhttp/api_keys_handler.go; backend/internal/admin/issuer.go; backend/internal/admin/revoker.go; backend/internal/admin/keygen.go; backend/internal/admin/operator_auth.go; backend/internal/auth/api_key_resolver.go; backend/internal/auth/session_middleware.go; backend/internal/userauth/service.go; backend/internal/userauth/types.go; backend/internal/userauth/store.go; backend/internal/userauth/email_verify.go; backend/internal/userauth/invite.go; backend/internal/userauth/social_login.go; backend/internal/userauth/password.go; backend/internal/usersession/rotation.go; backend/internal/gatewayhttp/auth_handler.go; backend/internal/gatewayhttp/session_handler.go; backend/cmd/gateway/routes.go; backend/cmd/gateway/wiring.go; backend/internal/router/route_plan.go; backend/internal/router/default_router.go; backend/internal/gatewayhttp/chat_completions_dispatch.go; backend/internal/registry/postgres_registry.go; /home/codex/refs/CLIProxyAPI/LICENSE; /home/codex/refs/CLIProxyAPI/internal/config/config.go; /home/codex/refs/CLIProxyAPI/internal/api/server.go; /home/codex/refs/CLIProxyAPI/internal/api/handlers/management/handler.go; /home/codex/refs/CLIProxyAPI/sdk/access/types.go; /home/codex/refs/CLIProxyAPI/internal/access/config_access/provider.go; /home/codex/refs/new-api/LICENSE; /home/codex/refs/new-api/model/user.go; /home/codex/refs/new-api/model/token.go; /home/codex/refs/new-api/model/channel.go; /home/codex/refs/new-api/model/ability.go; /home/codex/refs/new-api/model/channel_satisfy.go; /home/codex/refs/new-api/middleware/auth.go; /home/codex/refs/new-api/controller/token.go; /home/codex/refs/new-api/controller/user.go; /home/codex/refs/new-api/middleware/turnstile-check.go; /home/codex/refs/new-api/model/twofa.go; /home/codex/refs/new-api/controller/twofa.go.
Lane: specifier
Agent: GPT-5 Codex (codex-cli session)
UTC timestamp: 2026-05-22T00:21:45Z
