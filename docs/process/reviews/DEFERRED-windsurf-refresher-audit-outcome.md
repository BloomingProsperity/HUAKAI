# DEFERRED — Windsurf refresher 不带 RefreshAuditOutcome wrapper

- **Severity**: S2 (audit outcome 误报,Windsurf 未接通生产即 no-impact)
- **来源 codex review**: 2026-05-24T16:08Z, S2 切片 (commit 648ceb5 后) Round 1 P2 finding
- **Affected files**:
  - `backend/internal/provider/windsurf/refresher.go:142-145` (Windsurf 401/429 错误返回 bare error)
- **问题描述**:
  - S2 切片让 scheduler 用 `RefreshAuditOutcome` 接口写新 4 类 outcome (auth_expired / rate_limit_exceeded / risk_control_triggered / account_disabled)
  - 接口契约:refresher 返回的 error 必须实现 RefreshAuditOutcome,scheduler 才能拿到 outcome 落 audit row
  - Windsurf refresher (windsurf/refresher.go:142-145) 内部 save failure 时 outcome 正确,但**返回的 bare error 不带 wrapper** → scheduler fallback 写 'permanent_disable'
  - 现有 anthropic / copilot refresher 用 `auth.WrapRefreshError(err, outcome)` 包装,Windsurf 漏了同样包装
- **不 block 当前 commit 的原因**:
  - Windsurf 还**没接通生产**(scheduler 也没注 Windsurf refresher)
  - 当前没有 caller,bare error 不会真触发误报 audit
  - Windsurf 真接通切片同步补 wrapping
- **修复模板**(Windsurf 接通切片实施时参考):
  ```go
  // windsurf/refresher.go:142-145 类似位置
  if outcome != auth.OutcomeSuccess {
      return auth.WrapRefreshError(err, outcome)  // 包装 outcome 让 scheduler 读到
  }
  ```
  比照 backend/internal/provider/copilot/copilot_refresher.go 同样模式。
- **应在哪个切片修**: Windsurf 真接通切片(等 Owner 提供 windsurf OAuth endpoint 抓包后)
- **Tracker**: 跟 [[DEFERRED-windsurf-storage-handler]] 合并 — 同一切片修

[CLOSED 2026-05-24 by Cursor SSRF + outcome wrap 修复 (codex bk8yblst2): Windsurf refresher 已包 auth.WithRefreshAuditOutcome(err, failureClass), 配合 Cursor 同步修。Windsurf storage handler 缺失另行 DEFERRED-windsurf-storage-handler.md tracker]
