# DEFERRED — audit outcome severity mapping for new 4-class outcomes

- **Severity**: S2 (operator UI polish, 非 production-impact data correctness)
- **来源 codex review**: 2026-05-24T15:32Z, S1 切片 (commit b1fe8c30f→修正后) Round 1 P2 finding
- **Affected files**:
  - `backend/sql/queries/observability.sql:114-117` (audit listing severity case)
  - `backend/sql/queries/observability.sql:155-157` (audit counting severity case)
  - 配套 sqlc generated Go (`backend/internal/db/observability/*.sql.go`)
- **问题描述**:
  - 0055 migration 加了 4 新 outcome (`auth_expired` / `rate_limit_exceeded` / `risk_control_triggered` / `account_disabled`)
  - 当前 observability.sql severity case 只 special-case 旧 oauth outcomes,新 4 类落入 `info` default
  - 后果:operator 在 admin UI 按 warning / error 过滤时,看不到 new auth_expired / risk_control / account_disabled refresh 失败 → ops 漏报
- **不 block 当前 commit 的原因**:
  - 仅影响 UI 严重度显示分类,**不影响数据写入 / 不影响 production refresh 行为**
  - audit row 仍正确落库 (CHECK 通过),只是 admin UI 默认严重度归 info
  - operator 可临时按 outcome 字面值过滤工作绕过 (filter outcome IN (...))
- **应在哪个切片修**:**S2 (refresher outcome 升级切片) 同期**,因为 S2 会把 ClassifyRefreshError 真的接入 audit append,真实落库新 outcome。届时配套更新 observability.sql 严重度映射。
- **修复模板** (S2 切片实施时参考):
  ```sql
  -- backend/sql/queries/observability.sql:114-117 类似 case
  CASE
      WHEN outcome IN ('refresh_succeeded', 'refresh_token_rotated', 'cache_hit') THEN 'info'
      WHEN outcome IN ('refresh_lock_held', 'db_version_conflict', 'invalid_grant_race_recovered') THEN 'info'
      WHEN outcome IN ('storm_budget_exhausted', 'cas_lost', 'token_malformed') THEN 'warning'
      WHEN outcome IN ('oauth_401_force_refresh', 'permanent_disable', 'mimicry_applied') THEN 'warning'
      WHEN outcome = 'rate_limit_exceeded' THEN 'warning'
      WHEN outcome IN ('auth_expired', 'risk_control_triggered', 'account_disabled') THEN 'error'
      ELSE 'info'
  END AS severity
  ```
  S2 实施时按真实 ops 需求确认每行映射。
- **Tracker**: 跟 [[2026-05-24-auth-expired-schema-gate-synthesis]] S2 切片合并

[CLOSED 2026-05-24 by S2 切片实施]
