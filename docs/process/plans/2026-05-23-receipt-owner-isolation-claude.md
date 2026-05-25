# P0-1 Receipt 租户内 user 隔离 — Claude lane plan

Status: Draft (parallel with codex lane, prestudy pending)

## §1 问题陈述

`SessionIdentity` 已有 `UserID int64` ([backend/internal/auth/session_middleware.go:15-21](backend/internal/auth/session_middleware.go#L15-L21)),但 cost receipt 查询全链路只 tenant 隔离:

| 位置 | 当前 | 影响 |
|---|---|---|
| receipt_storage_pgx.go:114,146,222,251,279 | `WHERE request_id=$1 AND tenant_id=$2` | SQL 层无 owner |
| cost_receipt_handler.go:107 | `GetReceipt(ctx, requestID, ident.TenantID)` | 只传 tenant |
| cost_receipt_handler.go:120,222,232 | `receipt.TenantID != ident.TenantID` | 只 tenant 校验 |
| 0028_user_cost_receipts.up.sql | `tenant_id BIGINT NOT NULL` 无 user_id | schema 缺 owner 列 |

**Attack**: 同租户内 user A 拿到 user B request_id (URL 日志/邮件/截图泄漏) → user A 用自己 session 查/verify user B 的 cost receipt → 用户级信任链泄漏。

**违差异化承诺**: [[project_core_trust_chain_differentiator]] "用户消费透明,商家不能做假" — 但 multi-user 租户内不闭环 (企业租户多员工互看)。

## §2 Schema 选项

receipt 表有 `enforce_audit_append_only()` trigger (0028:24-40),不允许 UPDATE/DELETE。所以 backfill 不能 UPDATE 老行 user_id。

### Option A (推荐): ALTER ADD COLUMN nullable + handler-side enforce
- `ALTER TABLE user_cost_receipts ADD COLUMN user_id BIGINT NULL`
- 新 INDEX `(tenant_id, user_id, request_id) WHERE user_id IS NOT NULL`
- 新 receipt 写入强制填 user_id (handler 在 settle 时从 session 拿)
- 老 receipt user_id = NULL (legacy 标记)
- 查询: `WHERE request_id=$1 AND tenant_id=$2 AND user_id = $3` 严格隔离;legacy(NULL user)直接不可查
- **优**: 一次 ALTER 不动数据,append-only trigger 不阻塞
- **缺**: legacy receipt 用户/管理员都看不到(需 admin 入口单独查 by request_id no-owner)

### Option B: 新表 + view union
- 创建 user_cost_receipts_v2 with user_id NOT NULL
- 旧表 read-only
- 重风险

### Option C: 单独 mapping 表
- request_id → user_id 单独表
- LEFT JOIN 查
- 多一次表查

**选 A** — 最小变更 + 信任链层级清晰 + 与 W5 [[feedback_test_quality_discipline]] 兼容。

## §3 Handler 改造点

| 文件 | 当前 | 改造 |
|---|---|---|
| receipt_storage.go (接口) | `GetReceipt(ctx, requestID, tenantID)` | `GetReceipt(ctx, requestID, tenantID, userID)` (userID=0 表 admin 跨 owner) |
| receipt_storage_pgx.go:114 | `WHERE request_id=$1 AND tenant_id=$2` | `AND user_id = $3` (admin mode 时 $3=NULL skip) |
| receipt_storage_pgx.go:146,222,251,279 | 同 | 同 |
| cost_receipt_handler.go:107 | `GetReceipt(ctx, requestID, ident.TenantID)` | `GetReceipt(ctx, requestID, ident.TenantID, ident.UserID)` |
| cost_receipt_handler.go:120 | `receipt.TenantID != ident.TenantID` | + `receipt.UserID != 0 && receipt.UserID != ident.UserID` |
| cost_receipt_handler.go:222 | `userReceiptBelongsToTenant(req, ident.TenantID)` | + user_id 检验 |
| chat_completions settle 路径 | receipt insert 不带 user_id | receipt insert 带 session.UserID |
| audit.CostReceipt struct | 含 TenantID 无 UserID | 加 UserID int64 |

## §4 风险

| 风险 | 缓解 |
|---|---|
| Legacy receipt user_id=NULL 用户看不到自己历史 | 接受 (legacy 标记) + 后续切片可 reverse-lookup binding 表 backfill |
| Admin 复审跨 user receipt 需要 | admin endpoint 用 `userID=0` 走 admin path,绕 owner check |
| chat_completions settle 拿不到 session.UserID (API key 路径无 session 只有 binding) | binding 表已有 (user_id, api_key_id, account_id) mapping,改 binding lookup 时把 user_id 拉出来填 receipt |
| Append-only trigger 不阻塞 ALTER ADD COLUMN | 验证:trigger 只 `BEFORE UPDATE/DELETE ON user_cost_receipts FOR EACH ROW`,ALTER 是 DDL 不触发 row trigger ✅ |
| Race: 同一 request_id 两个 session 同时 settle (理论不可能 request_id UNIQUE,但要 mutation 验) | 测试用 UNIQUE constraint conflict 验证 |

## §5 测试矩阵 (mutation-checked)

| Test | Mutation 自检 |
|---|---|
| T1: User A 查 user B receipt → 404 | 移除 user_id check → 返回 receipt = 红 |
| T2: User A verify user B receipt → 404 | 同上 |
| T3: Admin endpoint 跨 user 查 OK (userID=0) | 把 admin 强制走 owner check → 红 |
| T4: Legacy receipt (user_id NULL) 普通 user 查 → 404 | NULL 视为 wildcard → 红 |
| T5: Legacy receipt admin 查 OK | 同 T3 |
| T6: chat_completions settle 写新 receipt 必含 user_id | settle 路径 user_id 缺 → DB CHECK 拒(handler-side 强制) |
| T7: SQL injection on user_id → 拒 | 用 prepared statement (现有 pgx 已 OK) |

## §6 Commit 切片 (一 commit 一模块)

1. **C1 schema**: migration 0052 ADD COLUMN user_id + INDEX + Owner schema gate 确认
2. **C2 storage 接口 + queries**: receipt_storage.go/pgx.go 加 userID 参数 + handler insert path
3. **C3 chat_completions settle wire user_id**: binding lookup pull user_id → receipt insert
4. **C4 cost_receipt_handler enforce + 测试**: handler 校验 + T1-T7
5. **C5 admin endpoint 留 admin path (or 不做): 等 P0-2 用户面 endpoint 一起设计**

## §7 D 决策点 (Owner)

- **D1 schema gate**: Option A 接受? (ALTER ADD nullable + handler enforce)
- **D2 legacy receipt 用户可见性**: 用户级 NULL 视为不可见 (强隔离) OR NULL 用户也可见 (软隔离)? 建议强隔离 (符合信任链承诺)
- **D3 admin endpoint 设计**: 把 admin override 做在 GetReceipt(userID=0) 还是独立 `/admin/v1/receipts/*` endpoint? 建议独立 endpoint (按职责组织,frozen package 约束 P-PROD 不可加新文件但 admin handler 独立 mount point 可在 routes.go 加)
- **D4 chat_completions settle 路径** 拿 session.UserID 还是 binding.UserID? Binding 更可靠(API key auth path 没 session)

## §8 时间估 + Ceremony

按 [[feedback_ceremony_tiered]] 高难度:
- prestudy + plan parallel + synthesis = 已在进行 (3 task 并行)
- 实施 5 commit 切片 ≈ 1-2 工作日 (Claude 决策 + codex 实施 + per-commit review)
- 双 verify (Claude + codex review) 每 commit

## §9 Lane attribution

- Claude lane plan timestamp: 2026-05-23T14:25:00Z
- 不读 codex lane plan (bnvdkan33 并行跑)
- 不读 prestudy (blj1dcczp 并行跑)

## §10 Source files read

- backend/internal/auth/session_middleware.go (SessionIdentity)
- backend/internal/gatewayhttp/cost_receipt_handler.go (handler 全文)
- backend/internal/audit/receipt_storage_pgx.go (SQL)
- backend/internal/audit/receipt_storage.go (接口 + 占位)
- backend/sql/migrations/0028_user_cost_receipts.up.sql (schema + trigger)
- backend/cmd/gateway/routes.go (mount point)
