# 2026-05-21 §1 用户与权限 remediation synthesized plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用 `subagent-driven-development` 或 `executing-plans` 逐片执行。本计划是 CLAUDE.md rule #10 的最终 synthesis，不再是平行草案。实现前必须从本文件执行，不得回到 `-claude` / `-codex` 草案自行择取。

**Goal:** 用 10 个可执行 remediation slice 收口 HUAKAI §1 用户与权限缺口：用户组、租户管理面、注册增强、anti-abuse、email OTP 2FA、用户 API key 自助、token policy 与 per-token quota。

**Architecture:** 以单一 `user_groups` authority 加 `policy jsonb` 承载可变策略，用 `user_group_memberships` 承载成员关系；API key policy 和注册默认权益都从该 authority 派生。认证增强采用 email OTP pending-login flow，不采用 TOTP/authenticator app。per-token quota 必须接入 billing/claim gate，不允许做 ad-hoc counter。

**Tech Stack:** Go backend, PostgreSQL migrations, sqlc, existing `usersession` flow, existing SMTP email backend, existing admin audit, existing billing/claim gate, `go test ./...`, `codex exec review --uncommitted --full-auto`.

---

## 0. 计划元数据

| 字段 | 内容 |
| --- | --- |
| Owner directive | `TASK: Produce the synthesized, executable §1 (用户与权限) remediation plan for HUAKAI.` |
| Owner approval provenance | 2026-05-22 当前 Claude session 中，Owner 对 native AskUserQuestion 三项裁决：§1 范围 = 「一次全补齐」(FULL scope，P1-P10 全部进入，包括 `2FA`、anti-abuse/captcha、per-token quota)；用户组表结构 = 「一张表+灵活字段」(SINGLE `user_groups` table + `policy jsonb`，不建单独 `permission_groups`)；登录两步验证 = 「邮箱验证码」(EMAIL OTP pending-login flow，不采用 TOTP)。 |
| Scope | FULL。P1-P10 全部进入 §1 remediation 范围，包括 `2FA`, anti-abuse/captcha, per-token quota enforcement。 |
| Out of scope for this file | 本次只写计划文档；不写生产代码、不写 migration、不改 OpenAPI、不改测试、不运行 git command。 |
| Source inputs | `docs/process/plans/2026-05-21-s1-remediation-codex.md`; `docs/process/plans/2026-05-21-s1-remediation-claude.md`; `docs/process/research/2026-05-21-audit-A.md`. |
| Success criteria | 每个 §1 gap 都有 slice、文件范围、TDD 顺序、风险、Owner surface 点和 release closure 输出；P0 Owner gate 已反映到计划内。 |
| Time estimate | 未来执行约 12-20 个工程日；P7 email OTP 与 P9 quota/billing 是主要不确定项。 |
| Blast radius | 后续执行会触碰 auth hot path、admin handlers、schema/sqlc、OpenAPI、SMTP 邮件、gateway request admission、billing/claim gate。 |
| Failure modes | group schema 返工影响 §4 binding；email OTP 绕过或锁死登录；captcha provider outage 阻断注册；token policy 增加 hot path latency；quota 若绕过 billing/claim gate 会造成漏扣或双扣。 |
| Decision points still open | `user_groups.policy` mutability、默认 API key 是否自动发放、captcha provider 选择、email OTP 默认开启范围、P9 quota reservation 细节。 |
| Pre-execution checklist | P0 已完成；实现前先写/更新 acceptance tests；P3/P7/P9 diff 在 commit 前向 Owner surface；每 slice 都跑 `go test ./...` 并记录真实 exit code；每 commit 前跑 Codex review。 |

## 1. P0 Gate 状态: DONE

P0 Owner decision gate 已关闭，本计划直接内化以下三项决定：

| 决定 | 最终口径 |
| --- | --- |
| §1 scope | FULL。P1-P10 全做，不能以风险为理由删除 2FA、anti-abuse/captcha 或 per-token quota。 |
| 用户组 schema | SINGLE authority。只建 `user_groups` + `user_group_memberships`，`user_groups.policy jsonb` 承载可变策略；不得引入单独 `permission_groups` 表。 |
| 2FA mechanism | EMAIL OTP。登录先验证 password，然后创建短期 pending-login challenge 并通过现有 SMTP backend 发送数字 code；第二个请求提交 `challenge_id + code`，成功后才走现有 `usersession` session issuance。不得实现 TOTP/authenticator secret。 |

澄清：FULL scope 授权 P1-P10 的实现工作可以推进并启动，但它不是 P3 `user_groups` schema / P7 email OTP auth-core / P9 per-token quota-billing 这三个 HIGH-risk surface 的 blanket pre-approval；这三片各自的 migration / auth / billing diff 仍必须在该 slice 的 commit landing 之前单独向 Owner surface 并取得确认，与计划 §4、P3、P7、P9 已写明的 Owner-surface 要求一致。

依据：Codex 草案已推荐 single `user_groups` authority，并指出当前缺 group entity、membership、CRUD、API-key-to-group binding（`docs/process/plans/2026-05-21-s1-remediation-codex.md:31`、`:33`、`:35`、`:57`）；Claude 草案同样把 group schema 作为 §4 binding 前置，并建议单表策略 JSON（`docs/process/plans/2026-05-21-s1-remediation-claude.md:19`、`:24`）。audit-A 证明 §1 当前缺口集中在注册增强、API key 自助与策略、用户组、pool admin tenant scope（`docs/process/research/2026-05-21-audit-A.md:31`、`:49`、`:58`、`:72`）。

## 2. Clean-room / Truth Posture

- 本 synthesis 没有读取任何 reference project source，只读取 Owner 指定的两个 parallel drafts、audit-A，以及 HUAKAI governance docs。
- `new-api` license 分类修正为 AGPL。后续只能使用已批准 specifier-lane 的 behavior paraphrase，不得 vendoring，不得复制 schema、字段名、handler shape、测试、注释或文件结构。
- reference evidence 只证明用户 outcome：group entitlement、token policy、self-service key lifecycle、email/pending-login 类 second factor、captcha/anti-abuse、default entitlement。HUAKAI 实现必须用本地 vocabulary 和本地架构。
- 本计划中的 upstream 行为主张均来自 audit-A 或草案引用；没有新增 source claim。
- Metadata: Observed regions: 8 docs regions / Inferences: 6 / Open questions: 5.

## 3. 当前 §1 状态和收口目标

| leaf | current status | remediation target |
| --- | --- | --- |
| 用户注册 / 登录 | 现有 email/password、email verify、invite、social login、失败计数和 session issuance；缺 second factor、captcha/anti-abuse、默认权益初始化。audit-A: `docs/process/research/2026-05-21-audit-A.md:27`-`:31`。 | `Implemented Better`: 默认 group entitlement、anti-abuse gate、email OTP opt-in、pending-login 不提前发 session。 |
| Session 会话 | session family、hash token、refresh rotation、防重放较强；但没有 2FA pending-login state。audit-A: `docs/process/research/2026-05-21-audit-A.md:35`-`:40`。 | 保持现有 `usersession` 强项；P7 只在 password 后、session 前增加 pending-login challenge。 |
| API Key 管理 | admin issue/list/revoke、安全存储较强；缺用户 self-CRUD、per-token quota/IP/model/group policy。audit-A: `docs/process/research/2026-05-21-audit-A.md:45`-`:49`。 | `Implemented Better`: session-authenticated user self-service + one-time plaintext + group-inherited token policy + claim-gate quota。 |
| 用户组 / 权限组 | 未找到独立 group table、membership table 或 CRUD；routes 只有 `user_group_match` string。audit-A: `docs/process/research/2026-05-21-audit-A.md:53`-`:58`。 | `Implemented`: `user_groups` + `user_group_memberships` + admin CRUD + group resolution into routes/API keys。 |
| 管理员权限 | platform/tenant operator 与 audit 已有；细粒度 RBAC 不作为本 slice 的独立表扩张。audit-A: `docs/process/research/2026-05-21-audit-A.md:62`-`:67`。 | 保持 coarse admin role；group management 通过 existing admin scope gate 控制。 |
| 多租户隔离 | schema tenant-aware 强；pool admin handler 仍 default tenant hardcode。audit-A: `docs/process/research/2026-05-21-audit-A.md:71`-`:76`。 | `Implemented Better`: pool admin 全部从 admin identity / explicit tenant scope resolve，跨租户错误返回 403/404。 |

## 4. 全局执行纪律

以下纪律适用于 P1-P10，每个 slice 的小节也会重复列出：

- Codex implements。Owner 已在本任务中要求每 implementation slice 由 Codex 实现、写 tests、运行 full tests、执行 per-commit review。
- TDD first。每个实现 slice 的第一步是写失败测试或 acceptance row，再做实现；不能先实现后补测。
- Full test。每个 slice 必须运行 `go test ./...`，在 slice log / commit body 记录真实 exit code。若 P1 只产生 red tests，要明确记录失败测试和 exit code，且不得把该状态伪装成 passing。
- Review before commit。每个 slice commit 前运行 `codex exec review --uncommitted --full-auto`，HIGH 必修，MED 修或记录延期理由，LOW 可记录后提交。
- One commit one module。每个 commit 只承载一个模块意图；若一个 slice 内部自然拆成 `P9a tokenpolicy` 和 `P9b tokenquota`，仍保留在 P9 下，但 commit 要拆开、review 要分别跑。
- 参照再对照收尾闸门。每个 implementation slice P2-P9 都必须在本片 commit 之后、下一片启动之前完成强制 closing gate：以 fresh HEAD 重新读取对应 reference-project module（sub2api / CLIProxyAPI / new-api，按本片 analog 选择）；执行查缺补漏，逐 feature 对照刚完成的 HUAKAI module 与 reference module，reference 有而本片缺的能力必须小项当片补齐，较大项写入 `docs/10_RISK_REGISTER.md` 的 `Mandatory Roadmap` row，绝不 silently drop，遵守 Feature Preservation Rule；执行升级点识别，按 CLAUDE.md #12 分为 架构 / 算法 / 生态 三维记录 HUAKAI 已超过或可超过 reference 的点；输出短 compare note，可追加到 slice commit/notes，或写入 `docs/process/research/2026-05-22-s1-<slice>-recompare.md`；reference citation 使用 `<repo>@<sha>:<file>:<line>`，读取 AGPL source（sub2api、new-api）时必须套用 clean-room lane guard，只能 paraphrase，不 vendoring，不复制 verbatim identifiers。本闸门与计划已隐含的 pre-slice compare 成对存在：每个 module 都要前置规划对照，也要收尾再对照。
- P3 schema、P7 email OTP auth、P9 per-token quota/billing 都是 HIGH-risk surface。Owner FULL scope 授权可以推进，但 migration/auth/billing diff 必须在 commit landing 前向 Owner surface。
- 不引入新的 runtime dependency，除非 Owner 单独确认。P7 必须复用 existing SMTP backend。

## 5. Slice Plan

### P1: Acceptance Tests Before Implementation

**Status:** Planned。P0 已完成后立即执行。

**Goal:** 先把 §1 缺口转成 acceptance contract，覆盖 normal、failure、operator recovery，不让后续实现缩功能。

**Files:**
- Modify: `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- Modify: `docs/08_REAL_WORLD_SCENARIOS.md`
- Test candidates: `backend/internal/gatewayhttp/*_test.go`, `backend/internal/userauth/*_test.go`, `backend/internal/auth/*_test.go`, `backend/internal/tokenpolicy/*_test.go`

**Acceptance rows to add or update:**
- `AT-S1-GROUP-001`: admin creates group, assigns user, route/API key policy resolves group.
- `AT-S1-TENANT-001`: tenant operator cannot list/create/update pool in another tenant; platform admin must specify tenant.
- `AT-S1-REG-001`: registration assigns default group entitlement and emits audit/log evidence without issuing unintended plaintext key.
- `AT-S1-ABUSE-001`: anti-abuse challenge and IP/email throttling block abuse before provider spend.
- `AT-S1-OTP-001`: password success creates pending-login challenge, no session yet; code success issues session; expiry/lockout/rate-limit paths fail safely.
- `AT-S1-KEY-001`: user self-creates/lists/revokes own API keys; cross-user key access is impossible.
- `AT-S1-POLICY-001`: token IP/model/group deny blocks before upstream dispatch across compatible endpoints.
- `AT-S1-QUOTA-001`: per-token quota reserves through billing/claim gate and never ad-hoc decrements outside claim path.

**TDD steps:**
- Write acceptance rows first.
- For each implementation slice P2-P9, create the first failing targeted test before implementation.
- Avoid permanent `t.Skip` coverage holes. If a temporary known-gap marker is necessary, it must name the owning slice and be removed in that slice.

**本片纪律:** Codex writes acceptance rows/tests using TDD, runs targeted tests and full `go test ./...` with real exit code, runs `codex exec review --uncommitted --full-auto`, and commits only the acceptance/test-contract module unless Owner accepts a red-test commit.

**Owner surface:** If P1 would land red tests on the main branch, Owner must explicitly accept that red-test checkpoint; otherwise pair each red test with its implementation slice before landing.

### P2: Pool Admin Tenant-Scope Fix

**Goal:** 去掉 `admin_pools_handler.go` default tenant hardcode，让 pool admin list/create/get/update 使用 admin identity scope 或 explicit tenant。

**Files:**
- Modify: `backend/internal/gatewayhttp/admin_pools_handler.go`
- Modify: `backend/internal/gatewayhttp/admin_pools_handler_test.go`
- Maybe modify: `backend/cmd/gateway/routes.go`
- Maybe modify: `docs/openapi/openapi.yaml`

**Acceptance tests:**
- Tenant operator lists only scoped tenant pools.
- Tenant operator create/update against another tenant returns 403/404.
- Platform admin without explicit tenant gets deterministic safe error or Owner-approved default behavior.
- Platform admin with explicit tenant succeeds and audit includes tenant.

**Implementation notes:** Reuse the tenant-scope semantics already present in provider-account admin handlers; do not trust request body `tenant_id` as truth without admin scope validation. Claude draft independently called this small but important fix (`docs/process/plans/2026-05-21-s1-remediation-claude.md:26`-`:28`).

**本片纪律:** Codex implements, writes failing tests first, runs targeted tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the pool-admin tenant-scope module.

**收尾对照:** re-compare new-api 的 user/token/channel group-based operational isolation 行为，以及 sub2api 单实例隔离模型；HUAKAI 的 tenant 模型本就强于两者，重点确认管理面没有漏掉隔离项。

**Risk:** R-S1-TENANT-001。错误 scope resolution 会造成跨租户 pool read/write。

### P3: User Group Schema + sqlc

**Goal:** 新增 canonical group schema：`user_groups` 是唯一 group/policy authority，`user_group_memberships` 是成员关系表。不得新增 `permission_groups`。

**Files:**
- Create: `backend/sql/migrations/00xx_user_groups.up.sql`
- Create: `backend/sql/migrations/00xx_user_groups.down.sql`
- Create: `backend/sql/queries/user_groups.sql`
- Modify/generated: `backend/internal/db/*`
- Maybe modify: `backend/sqlc.yaml`

**Schema contract:**
- `user_groups`: tenant-scoped, stable local identifier, display name, status/enabled, default flag, `policy jsonb`, timestamps, soft-delete marker where consistent with local patterns.
- `user_group_memberships`: tenant-scoped relation from user to group, composite FK preventing cross-tenant membership, uniqueness preventing duplicate active membership.
- Default group invariant: at most one default active group per tenant; disable/delete default group blocked while it is the fallback entitlement.
- `policy jsonb` may carry model allow/deny, pool binding hints, default token policy, registration entitlement, and future quota strategy. Stable columns stay minimal.

**Acceptance tests:**
- Migration up/down works in existing test harness.
- Cross-tenant membership FK is rejected.
- Duplicate membership is rejected.
- Default group invariant is enforced.
- Policy JSON accepts expected minimal shapes and rejects invalid top-level non-object values if local SQL conventions support that check.

**HIGH-risk Owner surface:** Before commit lands, surface the migration up/down, query file, generated sqlc diff, and rollback behavior to Owner. FULL scope authorizes work, but DB schema remains HIGH risk.

**本片纪律:** Codex implements, writes migration/schema tests first, runs targeted migration/sqlc tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the group schema/sqlc module.

**收尾对照:** re-compare sub2api 的 group 实体表与 group 管理 handler，以及 new-api 的 group 字符串 + group 配置/service。

**Risk:** R-S1-SCHEMA-001。Schema 过早固化会影响 §4 pool binding 和 P9 quota policy。

### P4: Group Domain Service + Admin CRUD

**Goal:** 让 group 可运维：list/create/update/disable、membership assign/remove/list，并接入 admin audit。

**Files:**
- Create: `backend/internal/usergroup/*`
- Create: `backend/internal/gatewayhttp/admin_user_groups_handler.go`
- Create/modify tests: `backend/internal/gatewayhttp/admin_user_groups_handler_test.go`, `backend/internal/usergroup/*_test.go`
- Modify: `backend/cmd/gateway/routes.go`
- Modify: `docs/openapi/openapi.yaml`

**Behavior contract:**
- Platform admin can manage groups for explicit tenant.
- Tenant operator can manage only scoped tenant groups under allowed policy.
- Disable/delete default group is blocked.
- Disable group with active membership is blocked unless an explicit reassignment strategy is provided.
- Every mutation writes audit event without leaking policy secrets if policy later carries sensitive config.

**Acceptance tests:**
- CRUD normal path.
- Cross-tenant denial.
- Default group disable denial.
- Active membership disable denial and recovery by reassignment.
- Audit event on create/update/membership change.

**本片纪律:** Codex implements, writes failing CRUD/audit tests first, runs targeted tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the group service/admin CRUD module.

**收尾对照:** re-compare sub2api 的 group 实体表与 group 管理 handler，以及 new-api 的 group 字符串 + group 配置/service。

**Risk:** R-S1-OPS-001 and R-S1-CLEANROOM-001。Admin policy shape must be HUAKAI-owned, not upstream-shaped.

### P5: Registration Default Entitlement

**Goal:** 注册成功后给用户分配 tenant default group 和默认 entitlement，不 silently omit default onboarding outcome。

**Files:**
- Modify: `backend/internal/userauth/service.go`
- Modify: `backend/internal/userauth/store.go`
- Modify: `backend/internal/userauth/types.go`
- Modify: `backend/internal/gatewayhttp/auth_handler.go`
- Modify tests: `backend/internal/userauth/*_test.go`, `backend/internal/gatewayhttp/auth_session_handler_test.go`
- Maybe modify: `docs/openapi/openapi.yaml`

**Behavior contract:**
- Registration transaction creates user and default group membership atomically.
- If no default group exists, registration returns actionable operator error rather than creating over-permissive access.
- Default entitlement includes group membership and policy-inherited baseline quota/routing rights.
- Default API key auto-issuance is not silently dropped: mark as `Feature Flag` / `Manual First` until Owner approves plaintext delivery and abuse defaults. P8 self-service provides the non-automatic path.

**Acceptance tests:**
- New user receives default group membership.
- Missing default group fails safely.
- Cross-tenant default group is impossible.
- No plaintext API key is emitted unless feature flag and Owner-approved flow exist.

**本片纪律:** Codex implements, writes failing registration entitlement tests first, runs targeted tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the registration entitlement module.

**收尾对照:** re-compare sub2api 注册服务，以及 new-api 注册 controller 的默认配额 / 默认组 / 邀请返利行为。

**Risk:** Wrong defaults either block all new users or grant paid capacity too widely.

### P6: Registration Anti-Abuse / Captcha

**Goal:** 在注册和 login abuse-sensitive path 前接入 anti-abuse challenge 与 rate-limiting，不增加 runtime dependency。

**Files:**
- Modify/create: `backend/internal/userauth/*`
- Modify: `backend/internal/gatewayhttp/auth_handler.go`
- Modify wiring/config files as needed
- Modify tests: `backend/internal/userauth/*_test.go`, `backend/internal/gatewayhttp/auth_session_handler_test.go`
- Maybe modify: `docs/openapi/openapi.yaml`

**Behavior contract:**
- Challenge verifier is interface-based and feature-flagged.
- Remote IP, email/tenant, and failure counters participate in throttle decisions.
- Provider outage behavior is explicit: disabled verifier bypasses; configured verifier follows Owner-approved fail-open/fail-closed policy.
- Abuse blocks happen before session issuance and before provider spend.
- Audit/log evidence records safe enums and counts, not raw challenge secrets.

**Acceptance tests:**
- Below threshold succeeds.
- Burst registration/login from same IP/email is blocked.
- A second IP/user is not globally blocked by first IP.
- Challenge failure produces safe error and no session.
- Provider outage follows configured policy.

**本片纪律:** Codex implements, writes failing anti-abuse tests first, runs targeted tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the anti-abuse module.

**收尾对照:** re-compare new-api 的 turnstile / captcha 中间件。

**Risk:** R-S1-OPS-001。Captcha/challenge outage can block legitimate signup if defaults are wrong.

### P7: Email OTP 2FA Login Step

**Goal:** 实现 Owner-approved email OTP 2FA：password 成功后不发 session，先发 pending-login numeric code；code 验证成功后才通过 existing `usersession` flow 签发 session。

**Files:**
- Create migration: `backend/sql/migrations/00xx_pending_login_challenges.up.sql`
- Create migration: `backend/sql/migrations/00xx_pending_login_challenges.down.sql`
- Modify/create queries: `backend/sql/queries/*pending_login*.sql` or local naming consistent with existing auth queries
- Modify: `backend/internal/userauth/*`
- Modify: `backend/internal/gatewayhttp/auth_handler.go`
- Modify: `backend/cmd/gateway/wiring.go`
- Reuse existing SMTP/email backend; no new dependency.
- Modify tests: `backend/internal/userauth/*_test.go`, `backend/internal/gatewayhttp/auth_session_handler_test.go`
- Modify: `docs/openapi/openapi.yaml`

**Behavior contract:**
- Per-user opt-in toggle controls whether email OTP is required.
- Password success with OTP enabled creates a short-lived pending-login challenge and sends one numeric code via existing SMTP backend.
- Password success with OTP enabled does not call `usersession.Create` and does not return session/refresh tokens.
- Verify request accepts `challenge_id + code`; success consumes challenge and then issues session via existing usersession path.
- Expired challenge, wrong code, replayed code, wrong user/tenant, and too many failed attempts fail safely.
- OTP send and verify are rate-limited per user/email/IP.
- No authenticator secret, no TOTP enrollment, no backup codes unless nearly free from existing email recovery flow.

**Known security posture:** Email OTP is weaker than authenticator-app 2FA because email account compromise can defeat the second factor. Owner explicitly accepted this tradeoff. Treat it as `Safe Equivalent` / `Feature Flag` posture, not as stronger-than-TOTP.

**Acceptance tests:**
- OTP opt-out user logs in through existing session flow.
- OTP opt-in user gets pending challenge and no session after password.
- Correct code issues session and consumes challenge.
- Wrong/expired/replayed code does not issue session.
- Failed-attempt lockout and OTP rate-limit activate.
- SMTP send failure does not create usable session.
- Existing session refresh tests remain green.

**HIGH-risk Owner surface:** Before commit lands, surface migration, auth handler changes, pending-login state machine, email template/copy, lockout/rate-limit behavior, and OpenAPI diff to Owner. FULL scope authorizes work, but auth core remains HIGH risk.

**本片纪律:** Codex implements, writes failing email OTP tests first, runs targeted auth/session tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the email OTP auth module.

**收尾对照:** re-compare sub2api 的 pending auth session，以及 new-api 的 2FA / secure-verification 行为；这里只做行为参照，HUAKAI 使用 email OTP，不实现 TOTP。

**Risk:** R-S1-AUTH-001 and R-S1-AUTH-EMAILOTP-001。Main failures are bypass, account lockout, email dependency outage, and weaker factor posture.

### P8: API Key Self-Service

**Goal:** 新增 session-authenticated user API key CRUD，让用户只能管理自己的 keys，并复用 one-time plaintext issuance semantics。

**Files:**
- Create: `backend/internal/apikey/*` or refactor existing `backend/internal/admin/issuer.go` into shared issuance service without weakening admin path
- Create: `backend/internal/gatewayhttp/user_api_keys_handler.go`
- Modify: `backend/cmd/gateway/routes.go`
- Modify tests: `backend/internal/gatewayhttp/user_api_keys_handler_test.go`, `backend/internal/auth/api_key_resolver_integration_test.go`
- Modify: `docs/openapi/openapi.yaml`

**Behavior contract:**
- Session identity is the only source of tenant/user truth.
- User can create/list/revoke own API keys.
- User cannot specify another `tenant_id` or `user_id` in body to escape scope.
- Plaintext key appears exactly once in create response, never in logs/audit/list.
- New keys inherit default group policy unless explicit allowed override is provided by P9 policy rules.

**Acceptance tests:**
- User creates key and receives one-time plaintext.
- List returns prefix/status/expiry/policy summary, not hash/plaintext.
- Revoke makes resolver reject key.
- Cross-user and cross-tenant access is impossible.
- Audit excludes plaintext.

**本片纪律:** Codex implements, writes failing self-service API key tests first, runs targeted tests, runs full `go test ./...` with real exit code, runs per-commit Codex review, and lands one commit for the user API key self-service module.

**收尾对照:** re-compare sub2api 的用户 API key handler，以及 new-api 的用户 token controller。

**Risk:** R-S1-KEY-001。Plaintext leakage or cross-user key management would be critical.

### P9: Token-Level Policy + Per-Token Quota Enforcement

**Goal:** 为 API key 增加 enforceable policy：IP/CIDR、model allow/deny、group inheritance、per-token quota。Quota 必须接 billing/claim gate 或等价 reservation，不得做 best-effort counter。

**Files:**
- Create/modify schema/query files for API key policy source if not fully covered by P3 `policy jsonb`
- Create: `backend/internal/tokenpolicy/*`
- Modify: `backend/internal/auth/api_key_resolver.go` only to enrich identity/read policy; do not perform billing writes in resolver
- Modify shared gateway admission path used by compatible endpoints, e.g. `backend/internal/gatewayhttp/chat_completions_dispatch.go`
- Modify billing/claim path only inside reviewed quota sub-slice, e.g. `backend/internal/billing/claim_gate.go`
- Modify tests: `backend/internal/tokenpolicy/*_test.go`, `backend/internal/auth/*_test.go`, compatible endpoint tests
- Modify: `docs/openapi/openapi.yaml`

**Internal split inside P9:**
- `P9a tokenpolicy`: IP/CIDR + model allow/deny + group inheritance + shared admission gate. This can be reviewed as hot-path auth/gateway behavior.
- `P9b tokenquota`: per-token quota reservation/settlement integrated with billing/claim gate. This is its own billing-reviewed slice and may be a separate commit under P9.

**Behavior contract:**
- Policy is resolved from API key explicit policy plus user group inherited policy using deterministic precedence documented in tests.
- Deny happens before upstream dispatch and before quota spend.
- Compatible endpoints cannot bypass the policy; test at least `/v1/chat/completions`, and include `/v1/responses` / `/v1/messages` if present in current code.
- Per-token quota uses claim/reservation semantics so concurrent requests cannot overspend.
- No quota decrement inside `auth` resolver.

**Acceptance tests:**
- IP deny blocks request before provider use.
- Model deny blocks request before provider use.
- Group policy inherited by default key.
- Explicit key policy can only narrow or Owner-approved widen group policy.
- Quota exhaustion rejects before upstream dispatch.
- Concurrent quota requests cannot overspend.
- Failed/retried requests do not double count quota.

**HIGH-risk Owner surface:** Before commit lands, surface billing/claim diff, quota data model, concurrency behavior, and failure/retry settlement behavior to Owner. FULL scope authorizes implementation, but quota enforcement and billing ledger surfaces remain HIGH risk.

**本片纪律:** Codex implements, writes failing token policy/quota tests first, runs targeted tokenpolicy/auth/billing tests, runs full `go test ./...` with real exit code, runs per-commit Codex review for `P9a` and `P9b` separately if split, and lands one module per commit.

**收尾对照:** re-compare sub2api 的 api_key 策略字段（IP / quota / 滑窗限流），以及 new-api 的 Token 策略字段（quota / model / IP / group）。

**Risk:** R-S1-POLICY-001 and R-S1-QUOTA-001。Main failures are endpoint bypass, latency regression, double count, missed count, and billing drift.

### P10: Docs / Parity / Risk Closure

**Goal:** 把 §1 remediation 结果收口到 parity、risk、scenario、acceptance、OpenAPI/release notes，不让实现状态和治理文档脱节。

**Files:**
- Modify: `docs/03_FEATURE_PARITY_MATRIX.md`
- Modify: `docs/10_RISK_REGISTER.md`
- Modify: `docs/08_REAL_WORLD_SCENARIOS.md`
- Modify: `docs/11_ACCEPTANCE_TEST_MATRIX.md`
- Modify: `docs/openapi/openapi.yaml`
- Modify release gate notes under `docs/process/` as appropriate

**Closure contract:**
- 每个 §1 gap 映射到 `Implemented`, `Implemented Better`, `Merged Equivalent`, `Safe Equivalent`, `Feature Flag`, or `Mandatory Roadmap`。
- Default API key auto-issuance若未启用，必须明确为 `Feature Flag` / `Manual First`，并给出 enablement path。
- Email OTP 2FA 明确标为 Owner-accepted weaker posture，不伪称等同 authenticator-app 2FA。
- new-api AGPL classification 写入 clean-room risk note。
- P3/P7/P9 HIGH-risk surfaces 的 Owner surface 记录进入 plan/review notes。
- P10 必须汇总 P2-P9 每片「参照再对照收尾闸门」产生的查缺补漏 findings，并确认每一项已在原 slice 修复，或已作为 `Mandatory Roadmap` row 写入 `docs/10_RISK_REGISTER.md`。

**本片纪律:** Codex updates docs/tests, runs docs-relevant checks plus full `go test ./...` with real exit code after code slices are integrated, runs per-commit Codex review, and lands one commit for governance closure. If P10 only touches docs, still run review per project rule.

**Risk:** Docs drift can falsely pass release gates.

## 6. Execution Order

1. P0: DONE.
2. P1: acceptance/test contract first.
3. P2 can run immediately after P1 because it is isolated and closes known tenant hardcode.
4. P3 must precede P4/P5/P8/P9 because group id/policy semantics become the shared contract.
5. P4 follows P3 to make groups operable.
6. P5 follows P3/P4 because registration needs default group lifecycle.
7. P6 and P7 both touch `userauth` / `auth_handler.go`; run sequentially unless workers have disjoint write ownership. Recommended order: P6 then P7, so OTP lockout/rate-limit can reuse anti-abuse primitives.
8. P8 follows P3/P5 because user keys inherit group/default policy.
9. P9 follows P8. Split `P9a tokenpolicy` and `P9b tokenquota` internally if needed; quota remains billing-reviewed and Owner-surfaced.
10. P10 updates incrementally after each slice and closes finally after P9.

Parallelizable after P1:

- P2 can run while P3 migration design is being prepared.
- P10 docs updates can be staged per slice, but final parity closure waits for full tests.
- P6 design can be drafted while P3/P4 execute, but code should wait if P5/P7 are active in the same files.

## 7. Owner Decision Points Remaining

| decision | why it matters | recommendation |
| --- | --- | --- |
| `user_groups.policy` mutability | Tenant operator editing model/pool/quota policy can become privilege escalation. | Platform admin controls templates and high-risk policy keys; tenant operator can assign memberships and edit low-risk display fields first. |
| Default group lifecycle | Registration depends on a safe fallback entitlement. | Every tenant must have one active default group; disabling/deleting it requires replacement. |
| Default API key auto-issuance | Plaintext delivery and abuse risk are high. | Do not auto-issue by default; provide self-service P8 and mark auto-issue as Feature Flag / Manual First until Owner approves. |
| Captcha provider | External challenge affects signup reliability and may require secrets. | Interface + feature flag; no new runtime dependency in first pass; fail policy explicit. |
| Email OTP rollout | Auth core and SMTP dependency can lock users out. | Per-user opt-in first; admin/operator can disable for recovery under audit; no default-on until tests and ops path are proven. |
| P9 quota reservation details | Incorrect reservation causes revenue or user-balance drift. | Use billing/claim gate; review P9b separately with Owner before commit. |

## 8. Risk Register For This Plan

| risk | severity | what could go wrong | mitigation |
| --- | --- | --- | --- |
| R-S1-SCHEMA-001 group schema mismatch | HIGH | §4 pool binding later needs different group cardinality or policy shape. | Single canonical `user_groups` with flexible `policy jsonb`; minimal stable columns; migration up/down review before commit. |
| R-S1-TENANT-001 pool admin cross-tenant write | HIGH | Platform/tenant operator creates or updates pool in wrong tenant. | Reuse scoped admin identity helper; explicit tenant for platform admin; tests for wrong-tenant 403/404. |
| R-S1-AUTH-001 2FA bypass or lockout | HIGH | Login signs session before second factor or valid users cannot recover. | Pending-login challenge cannot call `usersession.Create`; short expiry; lockout/rate-limit tests; feature-flag rollout. |
| R-S1-AUTH-EMAILOTP-001 weaker factor posture | MED/HIGH | Email compromise defeats OTP; SMTP outage prevents login. | Owner-accepted posture recorded; per-user opt-in; audited admin recovery; SMTP failure no-session test; no claim of authenticator-app equivalence. |
| R-S1-KEY-001 plaintext API key leak | HIGH | Self-service endpoint logs or replays plaintext. | One-time response only; audit redaction; tests that list/audit/error never include plaintext. |
| R-S1-POLICY-001 token policy bypass | MED/HIGH | One protocol endpoint enforces policy while another bypasses it. | Shared admission gate; tests across compatible endpoints present in codebase. |
| R-S1-QUOTA-001 double count / missed count | HIGH | Per-token quota decrements outside billing claim, causing drift. | Quota only through claim/reservation path; P9b billing-reviewed slice; concurrency tests. |
| R-S1-CLEANROOM-001 AGPL leakage | HIGH | new-api/sub2api schema or source structure leaks into HUAKAI. | Behavior-only plan; local vocabulary; no vendoring; per-commit review and clean-room checklist. |
| R-S1-PERF-001 hot-path policy read latency | MED | API key policy lookup adds DB latency per request. | Start with indexed bounded lookup; later cache by key id + policy version if measured. |
| R-S1-OPS-001 anti-abuse provider outage | MED | Captcha/challenge outage blocks signup/login. | Feature flag, explicit fail policy, metrics/audit, admin override. |

## 9. Implementation Guardrails

- Do not introduce `permission_groups` in §1. The only group authority is `user_groups`; memberships live in `user_group_memberships`.
- Do not implement TOTP, authenticator secret, QR enrollment, or backup-code flows in P7. The Owner chose email OTP.
- Do not issue a session after password when OTP is required. Session issuance must happen only after successful `challenge_id + code` verification.
- Do not add a new email dependency. P7 reuses existing SMTP backend.
- Do not let `auth` resolver write billing/quota counters. Resolver may read policy/enrich identity; quota belongs in billing/claim gate.
- Do not let request body `tenant_id` override session/admin identity in user self-service or tenant-scoped admin paths.
- Do not auto-issue plaintext API keys at registration unless Owner separately approves delivery, feature flag, and abuse defaults.
- Do not copy AGPL/LGPL/GPL source, schema, comments, tests, file structures, handler names, or distinctive identifiers from reference projects.
- Do not use risk as deletion. Risky features become `Safe Equivalent`, `Feature Flag`, `Manual First`, or `Mandatory Roadmap` with enablement path.
- Update parity/risk/docs incrementally after each slice; do not defer all governance closure to P10.

## 10. Source Coverage Proof

Documents read for this synthesis:

- `docs/process/plans/2026-05-21-s1-remediation-codex.md`: 10-slice backbone, schema recommendation, TDD sequencing, risk register, implementation guardrails.
- `docs/process/plans/2026-05-21-s1-remediation-claude.md`: independent confirmation of group-first ordering, pool tenant-scope fix, registration/API key remediation, one-commit-one-module discipline.
- `docs/process/research/2026-05-21-audit-A.md`: §1 gap evidence and reference behavior paraphrase.
- `docs/01_PROJECT_BRIEF.md`: commercial and parity goals.
- `docs/02_CAPABILITY_CONTRACT.md`: capability preservation requirements.
- `docs/03_FEATURE_PARITY_MATRIX.md`: valid dispositions and relevant `F-AUTH`, `F-KEY`, `F-GROUP`, `F-SEC`, `F-TENANT` obligations.
- `docs/08_REAL_WORLD_SCENARIOS.md`: scenario coverage expectations.
- `docs/09_BUG_PATTERN_LIBRARY.md`: quota/security/admin bug patterns.
- `docs/10_RISK_REGISTER.md`: clean-room, security, billing risk rules.
- `docs/11_ACCEPTANCE_TEST_MATRIX.md`: acceptance test structure and existing auth/key/security rows.
- `docs/12_AGENT_WORKFLOW.md`: role split, clean-room lanes, risk-based confirmation.
- `docs/15_RELEASE_GATES.md`: parity, clean-room, acceptance, security, billing gates.

Observed from input drafts:

- Codex draft marked P0-P10 and explicitly required acceptance tests before implementation, group schema, pool tenant-scope, registration entitlement, anti-abuse, 2FA, user API key self-service, token policy/quota, and docs closure (`docs/process/plans/2026-05-21-s1-remediation-codex.md:57`-`:67`).
- Codex draft required quota to use billing/claim gate rather than ad-hoc counter (`docs/process/plans/2026-05-21-s1-remediation-codex.md:66`, `:100`, `:112`).
- Claude draft independently required group schema first, pool tenant fix, registration enhancement, API key self-service, TDD/full tests/review/one-commit discipline (`docs/process/plans/2026-05-21-s1-remediation-claude.md:19`-`:38`, `:45`-`:48`).
- audit-A observed the §1 source gaps that drive this plan: registration productization, API key self-service/policy, user groups, tenant-scope pool admin (`docs/process/research/2026-05-21-audit-A.md:31`, `:49`, `:58`, `:72`).

Source files read: docs/process/plans/2026-05-21-s1-remediation-codex.md; docs/process/plans/2026-05-21-s1-remediation-claude.md; docs/process/research/2026-05-21-audit-A.md; docs/01_PROJECT_BRIEF.md; docs/02_CAPABILITY_CONTRACT.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/08_REAL_WORLD_SCENARIOS.md; docs/09_BUG_PATTERN_LIBRARY.md; docs/10_RISK_REGISTER.md; docs/11_ACCEPTANCE_TEST_MATRIX.md; docs/12_AGENT_WORKFLOW.md; docs/15_RELEASE_GATES.md.

Lane: synthesis / planning only
Agent: GPT-5 Codex
UTC timestamp: 2026-05-22
