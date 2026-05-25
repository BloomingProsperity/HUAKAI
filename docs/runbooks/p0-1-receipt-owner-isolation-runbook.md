# P0-1 Receipt Owner Isolation — Migration Runbook

## Lane Header

=== CLEAN-ROOM LANE GUARD ===

LANE: IMPLEMENTER

REFERENCE PROJECTS IN SCOPE: none. 引用 prestudy/synthesis plan 已完成的 sub2api / new-api / one-api / CLIProxyAPI 证据;本 runbook 不重新读 ref 源码。

HARD PROHIBITIONS: 不复制借鉴项目源码;不暴露 user_id 进 receipt canonical signed body;不绕开 sidecar INNER JOIN 强 fail-closed。

CITATION POLICY: 仅引用 HUAKAI 内部文件。

=== END CLEAN-ROOM LANE GUARD ===

## §1 目的

把 receipt 信任链漏洞(同租户内 User A 通过共享 request_id 看到 User B receipt)关掉。Sidecar 表 `user_cost_receipt_owners` 携带 `(tenant_id, request_id, receipt_sequence, user_id, claim_id, owner_source)`,所有用户向 read path INNER JOIN sidecar;新 receipt 在同事务里写两边,老 receipt 没 sidecar 行 → 用户端天然 404 强隔离;admin 走独立 `GetReceiptForAdmin` path(同 RR-W5-006 admin 权限切片到位前后端无 admin endpoint 暴露)。

决策依据见 [docs/process/plans/2026-05-23-receipt-owner-isolation-synthesis.md](../process/plans/2026-05-23-receipt-owner-isolation-synthesis.md) §6 (D-001~D-010 Owner 批准)。

## §2 上线前 checklist

- [ ] CI 全绿:`backend-ci` workflow run 26340121342 或更新一次 push;`go vet` / migration round-trip / `go test -race -count=1` 三项全 success
- [ ] `0052_user_cost_receipt_owners.up.sql` 和 `0052_user_cost_receipt_owners.down.sql` 已合入 `claude/phase-1` 或目标发布分支
- [ ] Owner 协调 maintenance window(预计停服 5-10 分钟,migration 0052 加 `(tenant_id, id, user_id)` superset UNIQUE 不走 CONCURRENT,见 D-006 决议)
- [ ] `backend/internal/audit/receipt_storage.go` / `receipt_storage_pgx.go` / `receipt_formatter.go` (DeriveReceipt 已 select `blc.user_id`)三者部署对齐
- [ ] `backend/internal/gatewayhttp/cost_receipt_handler.go` GET/Verify 路径已经 wire `ident.UserID` 传 `GetReceiptForUser` (cost_receipt_handler.go:112 / cost_receipt_handler.go:238)
- [ ] Owner 通知客服:发布前的老 receipt 用户端 GET 会返 404(D-002 全 404 强隔离);admin 后台仍可读

## §3 Migration 步骤

按 D-006 maintenance window 停服几分钟,**不**用 CONCURRENT 索引,直接 BEGIN/COMMIT migration runner 兼容。

```bash
# 1. 确认目标库连接
export MIGRATE_DSN="postgres://huakai:<PROD_PASSWORD>@<PROD_HOST>:5432/<PROD_DB>?sslmode=require"

# 2. 上 migration 前快照(只读;预计 ~30s 取决于库大小)
pg_dump --schema-only --no-owner --no-acl \
  --table=billing_ledger_claims \
  --table=user_cost_receipts \
  --table=users \
  -d "$MIGRATE_DSN" > /tmp/pre-0052-schema.sql

# 3. 把网关 inbound 流量摘掉(LB 摘除 / 蓝绿切流量)
# 4. 跑 0052
migrate -path backend/sql/migrations -database "$MIGRATE_DSN" up 1
# 看到 "52/u user_cost_receipt_owners (...ms)" 即成功

# 5. 验:三个新对象都在
psql "$MIGRATE_DSN" -c "\d user_cost_receipt_owners"
psql "$MIGRATE_DSN" -c "SELECT indexname FROM pg_indexes WHERE tablename='billing_ledger_claims' AND indexname='uq_billing_ledger_claims_tenant_id_id_user_id';"
psql "$MIGRATE_DSN" -c "SELECT tgname FROM pg_trigger WHERE tgrelid='user_cost_receipt_owners'::regclass;"
# 期望:trigger 名带 enforce_user_cost_receipt_owners_append_only_{update,delete}

# 6. 恢复 inbound 流量
```

## §4 Rollback 步骤

仅在 §3 步骤 5 验失败 或 部署后立即出 incident 时使用。

```bash
# 1. 关 inbound 流量
# 2. 跑 down
migrate -path backend/sql/migrations -database "$MIGRATE_DSN" down 1
# 看到 "52/d user_cost_receipt_owners" 即成功

# 3. 验:sidecar 表 + (id,user_id) UNIQUE 索引都消失
psql "$MIGRATE_DSN" -c "\d user_cost_receipt_owners"
# 期望 "Did not find any relation named ..."
psql "$MIGRATE_DSN" -c "SELECT indexname FROM pg_indexes WHERE indexname='uq_billing_ledger_claims_tenant_id_id_user_id';"
# 期望 0 行

# 4. 部署回退到 commit 1ddaeb2 之前 (不含 sidecar handler 调用)
git checkout <pre-1ddaeb2-deploy-tag>
# 然后重启 backend
# 5. 重开 inbound 流量
```

**Rollback 注意**:0052.down.sql 会 `DROP TABLE user_cost_receipt_owners`,sidecar 行全部丢失。回滚后新写入的 receipt 也回到无 owner 状态。Rollback **只**应在上线后立即出现严重 incident 时使用;一旦客户已经依赖 sidecar 隔离(>24h),应改走前向修补而非 rollback。

## §5 部署后验证

```bash
# A. 应用层冒烟
# A1. 新 receipt 写入路径 (settle hook)
curl -X POST https://<gateway>/v1/messages -H "Authorization: Bearer <user-A-api-key>" -d @sample.json
# 等结算完成后:
psql "$MIGRATE_DSN" -c "SELECT request_id, receipt_sequence, owner_source FROM user_cost_receipt_owners ORDER BY created_at DESC LIMIT 5;"
# 期望:能看到 owner_source IN ('settle','cache_hit') 的新行

# A2. user A 拿到自己 receipt
curl https://<gateway>/v1/audit/cost-receipts/<request-id> -H "Authorization: Bearer <user-A-api-key>"
# 期望 200 + receipt body

# A3. user B 想拿 user A receipt
curl https://<gateway>/v1/audit/cost-receipts/<request-id> -H "Authorization: Bearer <user-B-api-key>"
# 期望 404 (storage INNER JOIN 自动过滤;handler 不泄 receipt 存在)

# A4. 老 receipt (deployment 前的) user A 自己拿
curl https://<gateway>/v1/audit/cost-receipts/<legacy-request-id> -H "Authorization: Bearer <user-A-api-key>"
# 期望 404 (D-002 强隔离;sidecar 无行 = INNER JOIN fail)
# 这是预期行为,需 RR-W5-003 后续 backfill 切片才会恢复用户端可见

# B. 完整性验证
psql "$MIGRATE_DSN" -c "
SELECT count(*) FROM user_cost_receipts r
LEFT JOIN user_cost_receipt_owners o
  ON o.tenant_id=r.tenant_id AND o.request_id=r.request_id AND o.receipt_sequence=r.receipt_sequence
WHERE o.user_id IS NULL AND r.created_at > NOW() - INTERVAL '1 hour';"
# 期望 0;非 0 说明新 receipt 漏写 sidecar 是 P0 incident,立即触发 §6
```

## §6 Incident 响应

| 信号 | 可能原因 | 应对 |
|---|---|---|
| 新 receipt 无 sidecar 行 (A4 验证 left join 非零) | settle hook 失败或 ReceiptHookSettler 没装上 | 立刻摘流量;`audit.AppendReceipt` ROLLBACK 是原子的,理论上不会有 receipt 无 sidecar 的状态,出现一定是 wire 漏了;查 commit 1ddaeb2 / 507dd63 的 wire 是否在部署里 |
| 老 receipt 大量 404 投诉 | D-002 预期 | 通知客服按 RR-W5-003 后续 backfill 切片;backfill 可后台异步从 billing_ledger_claims.user_id join 写 sidecar(owner_source='backfill_join') |
| admin endpoint 暴露 | RR-W5-006 admin 权限切片未到位 | 当前**没有** admin endpoint(NewAdminCostReceiptGetHandler 已删,不接路由);若 main.go 误装上立刻摘 |
| user A 看到 user B receipt | sidecar 写错 user_id | 立刻 P0;查 `audit.AppendReceipt` 入参 `receipt.UserID` 是否 == `claim.user_id`(composite FK D-010 兜底,但应用层也要查);看 [backend/internal/audit/receipt_formatter.go:490-491](../../backend/internal/audit/receipt_formatter.go#L490-L491) SQL `blc.user_id::bigint AS user_id` |
| Verify 接口对 cross-user request_id 返 valid | handler 未 wire ident.UserID | 看 [backend/internal/gatewayhttp/cost_receipt_handler.go:238](../../backend/internal/gatewayhttp/cost_receipt_handler.go#L238);测试 `TestReceiptStorageGetReceiptForUserRejectsCrossUser` 应该挂红 |

## §7 后续切片 (RR 票)

| RR | 范围 | 触发条件 |
|---|---|---|
| [RR-W5-002](../10_RISK_REGISTER.md) | antigravity refresh fail-closed 接 `credentialworker.Scheduler.recordAudit` | P0-1 上线稳后做 |
| [RR-W5-003](../10_RISK_REGISTER.md) | Legacy receipt backfill — 从 `billing_events.audit_request_id` JOIN `billing_ledger_claims.user_id` 异步补 sidecar 行,owner_source='backfill_join' | 客服收老 receipt 404 投诉积压时 |
| [RR-W5-005](../10_RISK_REGISTER.md) | request-claim DB-enforce LOW | application invariant 已守,DB 层 follow-up |
| [RR-W5-006](../10_RISK_REGISTER.md) | Admin endpoint with `admin.OperatorAuth` | 需要 admin 后台读 receipt 时 |
| [RR-W5-007](../10_RISK_REGISTER.md) | Verify owner-bound 严格 enforce | detached verify (request without ident) 需 owner 强校验时 |

## §8 验证记录 (本次发布)

| 项 | 状态 | 证据 |
|---|---|---|
| Migration round-trip | ✅ | 本地 PG15 跑通 52 → down -all → up → down -all,public BASE TABLE 残留 = 0 (2026-05-23) |
| CI backend-ci | ✅ | run 26340121342 全绿(go vet + migration + go test -race) |
| Codex per-commit review (read-only) | ✅ | 7 对 0001-0007 up/down 逐对核对 miss=∅ extra=∅ 无 HIGH/MEDIUM |
| 单测覆盖 | ✅ | `TestReceiptStorageGetReceiptForUserRejectsCrossUser` / `RejectsLegacyReceiptWithoutOwner` / `RejectsMissingUserID` / `AppendReceiptRollsBackReceiptWhenOwnerInsertFails` / `ReceiptHookSettlerCacheHitAppendsCacheHitOwnerSource` 全绿 |
| 全量 go test ./... | (见 §8 commit 评注) | claude/phase-1 fc97e63 |
