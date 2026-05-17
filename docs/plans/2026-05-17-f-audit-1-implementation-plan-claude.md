# 2026-05-17 F-AUDIT-1 实施 Plan — Claude

| 字段 | 内容 |
|---|---|
| Spec 锚 | docs/specs/user-consumption-transparency.md |
| 前置 | F-TRUST-1-B (commit 44d6bde) + F-BILL-001 settler + F-BILL-002 voucher (commit 158c421) + F-PRIV-1 (commit e961e5c + d8996c4 + 8c2acdc) + F-OBS-005 DLQ (commit aa60cab) 全闭环 |
| 闭环目标 | 5 sub-phase 实施 F-AUDIT-001 (HUAKAI 6 大差异化 6: 用户消费透明) — per-request user-verifiable cost receipt + mismatch 自动退款 + public pricing + user dispute API |
| 派工 | Claude plan (商业 PM 决策); codex 实施 Go backend |
| 估时 | 7-11 天 codex (5 sub-phase) |

---

## Sub-phase 拆分

### F-AUDIT-1-A (2-3 天): Migration 0028 + Receipt Formatter

- 新 migration `backend/sql/migrations/0028_user_cost_receipts.up.sql`:
  - `user_cost_receipts` table (append-only audit cache per spec §2 derived-first):
    - id / tenant_id / request_id / model / input_tokens / output_tokens / cached_tokens / cost_usd / rate_table_snapshot_id / signer_fingerprint / signed_hash / created_at
  - 不引第二套 billing source of truth (per spec §2 OCAW)
  - FK 弱约束: 只 reference 已存 audit_ledger_entries + billing_events (per spec §2)
- 新 `backend/internal/audit/receipt_formatter.go`:
  - DeriveReceipt(ctx, request_id) → CostReceipt (从 audit_ledger_entries JOIN billing_events JOIN usage_record JOIN pricing_rate_tables)
  - SignReceipt(receipt) → ed25519 sign (复用 F-TRUST-1-B signer)
  - 不引新 dep (复用现有 sign/auditledger/billing 接口)
- AT-AUDIT-001-001..003 (基础 derive + sign + 一致性)

### F-AUDIT-1-B (2 天): 5 user-facing endpoint

- 新 `backend/internal/audit/http_handlers.go`:
  - GET /v1/receipt/{request_id} (user-facing, tenant scoped, returns CostReceipt + verify signature)
  - GET /v1/receipt/{request_id}/verify (re-derive from raw + compare to signed snapshot, returns match/mismatch)
  - POST /v1/receipt/{request_id}/dispute (user 提 dispute, 写 audit_ledger DISPUTE_INITIATED)
  - GET /v1/pricing/public (public rate tables, 无 auth)
  - GET /v1/refund/{request_id} (user 查 refund 状态)
- 5 endpoint 全经 F-PRIV redactor (per [[feedback_huakai_better_than_sub2api]])
- AT-AUDIT-001-004..010

### F-AUDIT-1-C (1-2 天): Refund Worker + DLQ

- 改 `backend/internal/billing/refund_worker.go` (复用 F-OBS-005 DLQ):
  - 周期扫描 user_cost_receipts 找 mismatch (derived view ≠ signed snapshot, 比例 > 0.5%)
  - mismatch 自动 issue refund (写 billing_events REFUND row)
  - retry 经 DLQ 兜底 (per F-OBS-005 priority lane high)
- 不引新表 (refund 走现有 billing_events)
- AT-AUDIT-001-011..013

### F-AUDIT-1-D (1-2 天): Operator + User Dashboard

- 改 frontend dashboard (Owner 冻结期内只占位 API)? 或留 codex 后期写? 留 backend API 完整即可, dashboard 渲染等 frontend 解冻
- 改 admin endpoints:
  - GET /admin/v1/audit/mismatches (operator 看 mismatch 列表)
  - GET /admin/v1/audit/disputes (operator 看 user dispute, 路由到 ops)
- AT-AUDIT-001-014..016

### F-AUDIT-1-E (1-2 天): AT 全测试 + cross-spec 联动

- AT-AUDIT-001-001..016 全 PASS
- cross-spec: F-TRUST ledger (receipt 签 ed25519 from F-TRUST-1-B signer) + F-PRIV redactor (5 endpoint all sanitized) + F-OBS DLQ (refund retry) + F-BILL settler (receipt cost derived from billing)
- integration_test 验证 sentinel injection (user 真发请求 → receipt 真出 → verify PASS)
- matrix sync AT-AUDIT-001 全行

---

## 不动

- frontend (Owner 冻结, backend API 完整即可, dashboard 渲染等解冻)
- Rust core_gateway (并行 wave R-2-B/R-3-A-fix 在跑)
- LICENSE / 计费核心 (F-AUDIT 跟 F-BILL settler 解耦)
- F-PAY-001 真 payment provider (留独立 wave, Owner provider 选型 OCAW)

## Risks

| 编号 | 类型 | 严重度 | Mitigation |
|---|---|---|---|
| R-AUDIT-001 | algorithm | MED | derived view 重算性能 (every receipt request 重 JOIN 4 表) | 添 user_cost_receipts physical snapshot 兜底; 缓存 hot request_id 受最 5 min |
| R-AUDIT-002 | reliability | MED | mismatch > 0.5% threshold 选错可能 false-positive refund | 默认 0.5%, operator 可调; AT-013 双向 case (true mismatch + false positive) |

## Verify Gate

- backend test `go test ./internal/audit/... -race -count=1 -timeout 120s` PASS
- AT-AUDIT-001-001..016 全 PASS
- backend build PASS (`go build ./...`)
- codex per-commit review HIGH 清零
- 不引非 stdlib + 现有 dep

---

Plan: Claude Opus 4.7 直写 (商业 backend, PM 决策)
UTC: 2026-05-17T~13:35:00Z
