# 2026-05-23 P0-1 Receipt 租户内 user 隔离 — Synthesis (3 lane 合并)

## §0 Inputs

- Claude lane plan: [docs/process/plans/2026-05-23-receipt-owner-isolation-claude.md](2026-05-23-receipt-owner-isolation-claude.md)
- Codex lane plan: [docs/process/plans/2026-05-23-receipt-owner-isolation-codex.md](2026-05-23-receipt-owner-isolation-codex.md) (196 行,5 schema options + 9 D 决策点)
- Prestudy: [docs/process/research/2026-05-23-receipt-owner-isolation-prestudy.md](../research/2026-05-23-receipt-owner-isolation-prestudy.md) (82 行,4 ref 项目证据)

## §1 3 Lane 收敛 (agreement)

所有 3 lane 同意:

| 共识点 | Cite |
|---|---|
| receipt 必须加 user 维度隔离 | Prestudy §A 3 ref 项目都做 (sub2api/new-api/one-api),CLIProxyAPI 单用户例外不适用 HUAKAI |
| Source of truth = billing claim/usage 已有 user_id,不取 user 输入 | Prestudy §C 推荐,codex plan §3 验证,settler.go:101,117 已 reject mismatch |
| Append-only trigger 不能 UPDATE legacy | 3 lane 共识 |
| Handler 必须 fail-closed,不能 fallback tenant-only | Codex plan §4,prestudy §C |
| signed receipt canonical 不含 user_id(避免签名兼容) | Codex D-007 |
| frozen package 约束 — gatewayhttp 不加新文件 | CLAUDE.md #13,codex §4.7 |

## §2 3 Lane disagreement (schema 选择)

| Lane | 推荐 schema |
|---|---|
| Claude lane | Option B: ALTER ADD COLUMN user_id nullable + handler-side enforce |
| Codex lane | Option A: Sidecar `user_cost_receipt_owners` 表 (避免 UPDATE append-only) |
| Prestudy | **Column add 新数据 + 可选 sidecar 老数据 混合** |

**Synthesis 决策 (Owner 2026-05-23T15:00 已批) = Sidecar 方案 (codex lane Option A)**:
- 新表 `user_cost_receipt_owners`:`(tenant_id, request_id, receipt_sequence, user_id, claim_id, owner_source, created_at)` PK `(tenant_id, request_id, receipt_sequence)`
- FK `(tenant_id, user_id) REFERENCES users(tenant_id, id)` 立即加(sidecar 新表 100% NOT NULL)
- 新 receipt 写入: receipt INSERT + sidecar INSERT 同事务原子;两个都失败整 tx rollback
- 老 receipt: 没 sidecar row → 用户端 404 强隔离;后续切片(RR-W5-003)可从 billing_ledger_claims join backfill
- receipt 表 0028 完全不动 (保 append-only 纯粹)

**为什么选 Sidecar (Owner 决定)**:
- receipt 表 append-only trigger 不动 (信任链承诺保留)
- 新数据 + 老数据 backfill 同一 mapping 表 (sidecar 后续扩展简洁)
- 原子写: receipt insert + sidecar insert 同事务,保 owner mapping 一致
- 老数据天然 404 (没 sidecar row),不需要 flag 切换 enforce 时机

## §3 选定方案 (Sidecar — Owner 2026-05-23 已批)

### Schema (Commit 1):

```sql
-- backend/sql/migrations/0052_user_cost_receipt_owners.up.sql
BEGIN;

-- D-010 加固: billing_ledger_claims (tenant_id, id, user_id) superset UNIQUE
-- 让 sidecar 能用 composite FK 强制 sidecar.user_id == claim.user_id。
CREATE UNIQUE INDEX IF NOT EXISTS uq_billing_ledger_claims_tenant_id_id_user_id
    ON billing_ledger_claims (tenant_id, id, user_id);

CREATE TABLE IF NOT EXISTS user_cost_receipt_owners (
    tenant_id        BIGINT NOT NULL,
    request_id       TEXT NOT NULL,
    receipt_sequence INTEGER NOT NULL CHECK (receipt_sequence >= 0),
    user_id          BIGINT NOT NULL,
    claim_id         BIGINT NOT NULL,             -- D-010 收紧 NOT NULL
    owner_source     TEXT NOT NULL CHECK (owner_source IN ('settle', 'cache_hit', 'backfill_join')),
    created_at       TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, request_id, receipt_sequence),
    FOREIGN KEY (tenant_id, user_id) REFERENCES users(tenant_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (tenant_id, request_id, receipt_sequence)
        REFERENCES user_cost_receipts(tenant_id, request_id, receipt_sequence) ON DELETE RESTRICT,
    -- D-010 composite FK 强制 sidecar.user_id == claim.user_id。
    FOREIGN KEY (tenant_id, claim_id, user_id)
        REFERENCES billing_ledger_claims(tenant_id, id, user_id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_user_cost_receipt_owners_user_lookup
    ON user_cost_receipt_owners(tenant_id, user_id, request_id, receipt_sequence DESC);

-- append-only enforce (跟 0028 trigger 一致语义)
DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_update ON user_cost_receipt_owners;
CREATE TRIGGER enforce_user_cost_receipt_owners_append_only_update
    BEFORE UPDATE ON user_cost_receipt_owners
    FOR EACH ROW EXECUTE FUNCTION enforce_audit_append_only();

DROP TRIGGER IF EXISTS enforce_user_cost_receipt_owners_append_only_delete ON user_cost_receipt_owners;
CREATE TRIGGER enforce_user_cost_receipt_owners_append_only_delete
    BEFORE DELETE ON user_cost_receipt_owners
    FOR EACH ROW EXECUTE FUNCTION enforce_audit_append_only();

COMMIT;
```

Owner 已批 maintenance window 停服几分钟,**不**用 CREATE INDEX CONCURRENTLY,纯 BEGIN/COMMIT migration runner 兼容。

### Storage 接口 (Commit 2):

```go
// backend/internal/audit/receipt_storage.go
type CostReceiptStore interface {
    AppendReceipt(ctx context.Context, receipt *CostReceipt, owner ReceiptOwner) error  // owner 一起写,同事务
    GetReceiptForUser(ctx context.Context, requestID string, tenantID, userID int64) (*CostReceipt, error)
    GetReceiptForAdmin(ctx context.Context, requestID string, tenantID int64) (*CostReceipt, error)  // 不带 owner 校验,admin only
    // ... 其它 method 同样分 ForUser / ForAdmin
}

type ReceiptOwner struct {
    UserID      int64
    ClaimID     int64
    OwnerSource string  // "settle" / "cache_hit"
}
```

SQL (ForUser):
```sql
SELECT r.* FROM user_cost_receipts r
INNER JOIN user_cost_receipt_owners o
  ON o.tenant_id=r.tenant_id AND o.request_id=r.request_id
WHERE r.request_id=$1 AND r.tenant_id=$2 AND o.user_id=$3
```

INNER JOIN = 老 receipt 无 sidecar row 自动 not found,fail-closed 天然 (Owner 选老数据 404 强隔离)。

`AppendReceipt` 一个 tx 内: INSERT receipt + INSERT owner;任一失败 ROLLBACK。

### Handler (Commit 4, frozen package gatewayhttp 只改既有文件):

`backend/internal/gatewayhttp/cost_receipt_handler.go`:
- L107: `d.Receipts.GetReceiptForUser(r.Context(), requestID, ident.TenantID, ident.UserID)` (handler 默认 user path)
- L120/222/232: owner 已 INNER JOIN sidecar,storage layer 强 fail-closed,handler 只多 defense-in-depth check (UserID > 0)
- 新加 admin handler 函数 `NewAdminCostReceiptGetHandler` 在**同一文件**(避 frozen package 新文件)

### CostReceipt struct + Settle wire (Commit 3):

`backend/internal/audit/receipt_formatter.go`:
```go
type CostReceipt struct {
    // ... 原字段
    TenantID int64
    UserID   int64  // 从 sidecar JOIN 出来 (read path) 或 settle 时填 (write path)
    ClaimID  int64  // 已有,确认 wire
}
```

`backend/internal/billing/settler.go` settle path:
- L113-120 已 reject `req.UserID != claim.UserID`,拿 `claim.UserID + claim.ID` 传给 `AppendReceipt(receipt, ReceiptOwner{UserID, ClaimID, "settle"})`
- L417-420, L478-483 cache-hit path 同方式拿 claim.UserID + claim.ID,owner_source="cache_hit"

## §4 5 Commit 切片 (按 codex §6 + 一 commit 一模块)

| C | 模块 | 范围 | 风险 |
|---|---|---|---|
| C1 | sql/migrations | 0052 ALTER ADD COLUMN + CONCURRENT INDEX (2 文件) | Schema gate Owner 确认 (HIGH) |
| C2 | audit storage | receipt_storage.go + receipt_storage_pgx.go + CostReceipt struct + ReceiptInputs 加 UserID | 中 (5 SQL queries 改) |
| C3 | billing settler | settle 路径 wire claim.UserID → receipt insert + cache-hit path | 中 (settle 是 money path) |
| C4 | gatewayhttp + 测试 | cost_receipt_handler GET/Verify handler 加 owner check + T1-T13 + admin override path | 中 (frozen package 只改既有文件) |
| C5 | runbook + 全量 verify | migration runbook + production-like dry-run + 全量 go test + codex review | 低 |

**sidecar 暂缓 → RR-W5-003** (legacy backfill 后续切片,需 Owner separate D 决策)

## §5 测试矩阵 (取 codex T1-T14 + 删去 T9/T10 sidecar 部分)

| Test | 关键判别 | Mutation |
|---|---|---|
| T1 storage GET user A 查 user B receipt | not found | 删 user_id WHERE → 返回 = 红 |
| T2 storage latest sequence cross-user | not found | 同 |
| T3 refund idempotency cross-user | no enqueue | 删 owner join → 红 |
| T4 handler GET cross-user | 404 + body 无 cost | handler 不传 UserID → 红 |
| T5 handler UserID=0 (session 损坏) | fail-closed,storage 未调 | 删 precondition → storage 被调 = 红 |
| T6 verify detached cross-user | 不 valid (404 / 显式 err) | 只 tenant check → 返 valid = 红 |
| T7 verify mismatch refund cross-user | 不 enqueue | 删 owner check → enqueue.count=1 |
| T8 DeriveReceipt 从 claim.UserID 填 CostReceipt.UserID | UserID == claim.UserID | 不 select user_id → 0 = 红 |
| T11 legacy NULL receipt user 查 | 404 | NULL 视通配 → 200 = 红 |
| T12 cross-tenant regression | 404 | 删 tenant 条件 → 红 |
| T13 public JSON 不含 raw user_id | response body 不含 | accidentally expose → assertion 红 |

T9/T10 sidecar backfill 测试 → 移到后续 sidecar 切片。

## §6 D 决策点 (Owner 2026-05-23 决议)

| D | Owner 决议 | 备注 |
|---|---|---|
| **D-001** Schema path | **Sidecar 映射表** (codex lane Option A) | 新表 user_cost_receipt_owners,receipt 0028 不动 |
| **D-002** Legacy unmatched | **全 404 强隔离** | 老 receipt 无 sidecar row → INNER JOIN 自动 not found;admin 走独立 path |
| **D-003** Append-only exception | **不需要** | Sidecar 新表,receipt 表不动,无 trigger bypass |
| **D-004** Verify semantics | **session user owner-bound** (synthesis 默认采纳) | Verify 必须 owner 校验 |
| **D-005** Fail-closed status | **user mismatch 404,dependency missing 503** (synthesis 默认) | 避免 request_id oracle |
| **D-006** Migration mode | **Maintenance window 停服几分钟** | 不用 CONCURRENT,纯 BEGIN/COMMIT migration runner;Owner ops 协调下线时间 |
| **D-007** Public receipt schema | **隐藏 user_id 不进 canonical** (synthesis 默认) | 向后兼容签名 |
| **D-008** FK strictness | **立即 FK to users(tenant_id, id)** | Sidecar NOT NULL,FK 直接生效 |
| **D-009** Rollout flag | **不需要 flag** | Sidecar 模式下老数据天然 fail-closed,直接上线 enforce |
| **D-010** DB-enforce user-claim 一致性 (2026-05-23 C1 review 追加) | **同意加** billing_ledger_claims (tenant_id, id, user_id) superset UNIQUE + sidecar composite FK | 0009 (tenant_id, id) UNIQUE 零碰撞 superset;**HUAKAI DB-enforce 升级 = 信任链差异化**,详见 [prestudy §A 4 参考项目 token/log/usage owner 模式逐条](../research/2026-05-23-receipt-owner-isolation-prestudy.md#A-Reference-Observations);batch 进 C1 0052;closes RR-W5-004 |

## §7 时间估 + Ceremony

- ceremony = 高难度 ([[feedback_ceremony_tiered]])
- plan parallel ✅ (Claude lane + codex lane + prestudy 3 路)
- synthesis ✅ (本文)
- Owner schema gate D-001~D-009 = 必批
- 实施 = 5 commit ≈ 1.5-2.5 agent-days (codex 估)
- 每 commit per-commit review + Claude 自审
- C1 migration 高风险 Owner gate 单独批

## §8 Source files read

合并 3 lane (~80 个文件):
- HUAKAI Go: 同 Claude lane §10 + codex plan tail Source files + prestudy HUAKAI 列表
- ~/refs: 同 prestudy §Source files read (sub2api 10 / new-api 9 / one-api 8 / CLIProxyAPI 9)
- 关键 cite: SessionIdentity (auth/session_middleware.go:15-21) / receipt_storage_pgx.go:114,146,222,251,279 / settler.go:113-120,137-145,417-420,478-483 / billing_ledger_claims (0002:19-31) / append-only trigger (0028:24-40)

## §9 Lane attribution + timestamp

- Claude lane synthesizer: 本对话 session 81fec8f5-b3e1-465a-95c3-26d6efee9c90
- Codex plan lane: bnvdkan33, GPT-5 Codex / codex-plan-2026-05-23-receipt-owner-isolation, 2026-05-23T14:25:54Z
- Prestudy lane: blovkszyo, Codex GPT-5, 2026-05-23T14:44:28Z
- Synthesis timestamp: 2026-05-23T14:55:00Z
