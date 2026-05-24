# Auth Expired Outcome Schema Gate — Claude Lane Plan

- Lane: Claude PM-orchestrator (plan-only)
- UTC: 2026-05-24T11:18Z
- 互补 lane: docs/process/plans/2026-05-24-auth-expired-schema-gate-codex.md(codex 后台跑中)
- 前置:[decisions-locked.md](2026-05-24-decisions-locked.md) §1 TR-D1 (Owner 锁 schema 列路径)
- CLAUDE.md 条款:#10 / #11 / #12 / #13 / #14 / #15

## §1 目标范围

收尾 P-A R-A3 留下的 schema gate,把 refresh outcome 分类扩展到生产可用,落 TR-D1 schema 列。

### 1.1 当前 outcome enum 现状

`audit_outcome` 字段 (`oauth_refresh_audit_events.outcome` text 列) 现在用的值(P0-4 路径):
- `success` — refresh 成功
- `transient_error` — 临时 5xx / 网络错
- `non_retryable_error` — 其他

P-A R-A3 + 第三发 AI review 揭示**缺**:
- `auth_expired` — 401 invalid_grant / revoked_token(refresh token 失效)
- `rate_limit_exceeded` — 429(quota)
- `risk_control_triggered` — vendor 风控封禁
- `account_disabled` — vendor 远端 disable

### 1.2 health_state 字段(TR-D1 锁定加 schema)

- `provider_account.health_state` enum:`healthy` / `throttled` / `revoked` / `cooldown`
- `provider_account.health_changed_at` timestamp
- `provider_account.cooldown_until` timestamp(nullable;throttled/cooldown 状态使用)

## §2 现状缺口锚点

| 文件 / 位置 | 现状 | 缺口 |
|---|---|---|
| `backend/sql/queries/oauth_refresh_audit_events.sql` (sqlc) | outcome 是 text,不限 enum | 字段是 text → 应该 enum 提 type safety |
| `backend/internal/credentialworker/audit.go` (P0-4 commit f49867f) | recordAudit 路径接受任何 outcome 字符串 | 接入新 outcome 但 type 不知 |
| `backend/internal/provider/copilot/copilot_refresher.go` (P-A 9165551) | R-A3 注释 "auth_expired outcome safe equivalent" 留 TODO | 改成真 outcome + 真 audit |
| `backend/internal/anthropicoauth/refresher.go` (C2 0304d0d) | 401 invalid_grant → auth_expired classification 已写在代码但 outcome 字符串没 schema enum | 同上,需要 enum |
| `backend/sql/migrations/0054_*` | 是 device-code 列,**没动** outcome | 0055 加 outcome enum + 0056 加 health_state |

## §3 ref 项目对照(CLAUDE.md #15)

| 维度 | Wei-Shaw/sub2api@63b0631a5827 (LGPL paraphrase only) | BerriAI/litellm@414866767176 (Apache-2.0) | router-for-me/CLIProxyAPI@50d19e204fed (MIT) |
|---|---|---|---|
| outcome enum | `channel_monitor_service.go:269` history + 日 rollup,outcome 用 string;无 enum | `proxy/_experimental/health_check` 用 dict status `healthy/unhealthy/unknown` | `internal/auth/claude/anthropic_auth.go:355/396` 仅 success/failure 二值 |
| health state | `channel_monitor_runner.go:33` 长周期 monitor + 多状态 disable / restore | `proxy/_experimental` 实验性 health_check | 无(单机 CLI 无 health) |
| revoke detection | `channel_monitor_service.go:269` 401 → disable + 报警 | health_check 401 → unhealthy | 401 → 报错重 OAuth |
| 适合 HUAKAI | health state + cooldown 行为 paraphrase 主参考 | outcome dict 模式参考 | 二值不够,本切片需更精细 |

## §4 文件级范围

**新增**:
```
backend/sql/migrations/
  0055_audit_outcome_enum.up.sql          (新)
  0055_audit_outcome_enum.down.sql        (新)
  0056_provider_account_health_state.up.sql (新)
  0056_provider_account_health_state.down.sql (新)

backend/internal/credentialworker/outcome.go (新)
  type RefreshOutcome string + enum constants + Validate
  func Classify(err error, vendor string, statusCode int) RefreshOutcome

backend/internal/credentialworker/outcome_test.go (新,mutation)
```

**修改**:
- `backend/sql/queries/oauth_refresh_audit_events.sql`:outcome 列类型改 `audit_outcome` enum
- `backend/sql/queries/provider_accounts.sql`:加 health_state / health_changed_at / cooldown_until 列查询
- `backend/internal/credentialworker/audit.go`:outcome param type 改 `RefreshOutcome` (不再是 string)
- `backend/internal/credentialworker/scheduler.go`:refresh failed 路径调 `Classify(err, vendor, statusCode)` 拿 outcome
- `backend/internal/provider/copilot/copilot_refresher.go`:R-A3 升级,401 路径返 `RefreshOutcome.AuthExpired`
- `backend/internal/anthropicoauth/refresher.go`:同上,401 → AuthExpired,429 → RateLimitExceeded
- `backend/internal/db/billing/`:sqlc 重生成跟 schema 同步

**禁止新增**:
- ❌ `backend/internal/proto/` (冻结)
- ❌ `backend/internal/gateway/` (冻结)
- ❌ `backend/internal/gatewayhttp/` (冻结)

## §5 切片建议

### S1: outcome enum migration 0055 + sqlc 重生成

**Spec**:
- migration 0055:`CREATE TYPE audit_outcome AS ENUM ('success','transient_error','non_retryable_error','auth_expired','rate_limit_exceeded','risk_control_triggered','account_disabled');`
- ALTER TABLE oauth_refresh_audit_events ALTER COLUMN outcome TYPE audit_outcome USING outcome::audit_outcome;
- down:revert column to text + drop type
- sqlc 重生成 db/auth/oauth_refresh_audit_events.sql.go(outcome 字段改 enum 类型)

**风险测试** (CLAUDE.md #14):
- R-S1-A:up migration 跑过现有 audit row(text="success" 等)→ 必须 cast 成功;mutation:加一行 outcome="unknown" 在 migration 前 → up 必须红(cast 失败)
- R-S1-B:down + up round-trip → 同一行 outcome 完整保留;mutation:down 不 drop type → up 时 duplicate object → red

### S2: outcome.go 分类逻辑 + RefreshOutcome 类型

**Spec**:
- 定义 `RefreshOutcome` string type + constants
- `Classify(err error, vendor string, statusCode int) RefreshOutcome`:
  - statusCode==401 → AuthExpired
  - statusCode==429 → RateLimitExceeded
  - statusCode==403 + body match "risk_control" → RiskControlTriggered
  - statusCode==403 + body match "account_disabled" → AccountDisabled
  - 5xx → TransientError
  - other → NonRetryableError
- audit.go recordAudit 签名改 outcome RefreshOutcome

**mutation test**:
- 401 fixture → outcome 必须 AuthExpired;mutation:把 401 分类删 → 退到 NonRetryableError → test 必须红
- 429 + Retry-After body → outcome 必须 RateLimitExceeded;mutation:把 429 分类删 → test 必须红
- vendor=anthropic 401 invalid_grant body → AuthExpired(不是 RateLimited);mutation:vendor 路由错 → test 必须红

### S3: P-A R-A3 升级 + Anthropic C2 升级

**Spec**:
- copilot_refresher.go 401 → 调 Classify(err, "copilot", 401) → AuthExpired → audit 同事务记 outcome
- anthropic refresher.go 同上
- 测试:**真 PG integration test** R-A3 升级版,断 audit_ledger 同事务 row 含 outcome='auth_expired'

**mutation test**:
- 删 audit 同事务 → audit_ledger 无 sidecar row → R-A3 真挂

### S4: provider_account health_state 列 migration 0056

**Spec**:
- migration 0056:`CREATE TYPE health_state AS ENUM ('healthy','throttled','revoked','cooldown');`
- ALTER TABLE provider_account ADD health_state health_state NOT NULL DEFAULT 'healthy',ADD health_changed_at timestamptz NOT NULL DEFAULT NOW(),ADD cooldown_until timestamptz NULL;
- index on (health_state) for dispatcher 过滤
- down 反向

**mutation test** (round-trip):同 R-S1 的 round-trip 模式

### S5: dispatcher 接入 health_state 过滤(可拆后切片)

dispatcher 选号时 WHERE health_state='healthy' OR (health_state='throttled' AND cooldown_until < NOW()) → 自动 cooldown 解除。本切片或拆 L-Z 单独切片。

## §6 风险测试矩阵

| ID | 风险 | 真实损失 | mutation 自检 | 判别 fixture |
|---|---|---|---|---|
| R-S1-A | migration 0055 up cast fail | 历史 row 丢失 | mock 'unknown' outcome 在 migration 前 | up 必须报错 |
| R-S1-B | 0055 round-trip 不可逆 | down 后无法 re-up | down 不 drop type | up duplicate object red |
| R-S2-A | 401 分类丢失 | refresh failed 误归 NonRetryableError → backoff 一直试 | 删 401 → AuthExpired 映射 | mock backend 401 |
| R-S2-B | 429 + Retry-After 未提取 | 429 当 5xx 重试 → vendor 拉黑 | 删 429 → RateLimitExceeded | mock 返 Retry-After: 60 |
| R-S2-C | vendor 路由错 | anthropic 401 当 copilot 处理 | vendor 参数忽略 | 两 vendor 不同 body shape |
| R-S3 | audit 同事务断 | auth_expired 写入但 ledger 无 sidecar row | 删 BeginFunc tx | 真 PG integration test |
| R-S4 | 0056 round-trip | down 1 不能 drop index/column | 缺 drop index | up/down/up cycle |
| R-S5 | dispatcher 错过滤 unhealthy | unhealthy 账号继续被选 → 钱继续算 | filter clause 用错 OR 而非 AND | 多账号 mix healthy + revoked |

## §7 D 决策点(Owner pick)

### D-AE-1: outcome enum 完整度

| 选项 | 大白话 | ref 对照 |
|---|---|---|
| (A) **7 个 outcome**(success/transient/non_retryable/auth_expired/rate_limit/risk/account_disabled,Claude 默认推荐) | 全覆盖 | sub2api channel_monitor:LGPL paraphrase 多状态;Codex synthesis: D-D-001 ephemeral 反对 |
| (B) **4 个 outcome**(成功/瞬时/永久/auth) | 简化,risk/quota/disable 后续切片再加 | litellm 实验性 dict:`healthy/unhealthy/unknown` 三态参考 |
| (C) **per-vendor 自定义** outcome 字段 | 灵活,但 enum 不固定 | (HUAKAI 自研) |

**Claude 推荐**:(A) 7 个一次到位。**Why**:[[feedback_huakai_better_than_sub2api]] strict-better;补一次 outcome 比反复 migration 强。

### D-AE-2: outcome 字段 type

| 选项 | 大白话 |
|---|---|
| (A) PG enum type | 强类型,DB schema gate |
| (B) text + check constraint | 灵活,但弱类型 |
| (C) smallint + Go enum | 紧凑,但 ops 查 SQL 不直观 |

**Claude 推荐**:(A) PG enum。**Why**:Owner TR-D1 已锁 schema 路径,enum 是最强 type safety + 后续加列容易。

### D-AE-3: health_state cooldown 默认时长

承袭 [[decisions-locked.md]] §3 PH-D3/TR-D4:**配置可调,默认 3 连封 + 30min cooldown**。本切片不重新决,直接 lock。

### D-AE-4: dispatcher 接入 health_state 时机(S5)

| 选项 | 大白话 |
|---|---|
| (A) S5 跟 S1-S4 同切片做 | 一次到位 |
| (B) S5 独立切片(L-Z 压轴)| 解耦,先看 audit 数据再决过滤策略 |

**Claude 推荐**:(B) 独立。**Why**:dispatcher 在 frozen package(gateway / pool / router),触改要小心;先把 outcome enum + health_state 列 + audit 写入做完,看真生产数据再决过滤策略。

## §8 验证

- migration round-trip:`migrate up + down 1 + up + down 2 + up` on huakai_roundtrip DB(本机 PG ready,[[reference_local_pg_verification]])
- sqlc 重生成:`make sqlc-generate` 或对应命令;diff 不超预期
- 单元:`go test ./internal/credentialworker/... ./internal/provider/copilot/... ./internal/anthropicoauth/...`
- 集成:`go test -tags integration_pg ./internal/credentialworker/...`(真 PG audit 同事务测试)
- 全量:`go test ./...` + `go build ./...`

## §9 Source files read

**HUAKAI**:
- `backend/sql/queries/oauth_refresh_audit_events.sql` (上轮读 outcome 字段)
- `backend/internal/credentialworker/audit.go:1-120` (已读 P0-4 同事务路径)
- `backend/internal/provider/copilot/copilot_refresher.go` (9165551 R-A3 锚点)
- `backend/internal/anthropicoauth/refresher.go` (0304d0d 401 分类已写)
- `backend/sql/migrations/0054_*` (本日 latest migration 序号)

**Refs (paraphrase only)**:
- `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_service.go:269` (LGPL,health/disable/cooldown 行为)
- `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/channel_monitor_runner.go:33` (LGPL,长周期 monitor 思路)
- `BerriAI/litellm@414866767176:litellm/proxy/_experimental/health_check/`(Apache-2.0,outcome dict 模式)
- `router-for-me/CLIProxyAPI@50d19e204fed:internal/auth/claude/anthropic_auth.go:355,396`(MIT,success/failure 二值参考)

**Recency check**:全部 anchor 表 [docs/process/2026-05-24-ref-anchor.md](../2026-05-24-ref-anchor.md) 最新 SHA。

## §10 Lane attribution + UTC

- Agent: claude-opus-4-7
- UTC: 2026-05-24T11:18Z
- Lane: PM-orchestrator + specifier (plan-only,实施 lane 转 codex executor)
- Cross-discuss target: docs/process/plans/2026-05-24-auth-expired-schema-gate-codex.md (后台跑中)
- Synthesis 文件:docs/process/plans/2026-05-24-auth-expired-schema-gate-synthesis.md(codex 完成后)
