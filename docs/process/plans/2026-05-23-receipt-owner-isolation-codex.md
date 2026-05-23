# P0-1 Receipt 租户内 User 隔离 — Codex 独立 Plan

> **For agentic workers:** 执行本计划前必须先取得 Owner 对 §7 D 决策点的确认。本文件是 Codex lane 独立计划；未读取 Claude plan，未读取并行 prestudy，未实施代码。

**Owner directive:** “P0-1 receipt 租户内 user 隔离 — Codex lane 独立 plan”

**Goal:** receipt 的读取、验证、退款补偿 receipt 派生链路必须在 `tenant_id + user_id` 边界内 fail-closed，不能只靠 `tenant_id`。

**Architecture:** 先把 receipt owner 从账务事实中显式化，再让所有用户可达 receipt HTTP 路径按 session `TenantID + UserID` 查询/校验。生产历史数据用可审计 backfill，不靠请求时猜测。推荐优先采用 sidecar owner 表，避免 UPDATE append-only receipt 行。

**Tech Stack:** Go backend, PostgreSQL migrations, pgx/database.sql receipt storage, chi HTTP handlers, existing Go tests.

---

## Plan Metadata

| Field | Value |
|---|---|
| Scope | 只规划 P0-1 receipt 租户内 user 隔离；不实施代码、不 commit、不 push。 |
| In scope | schema/options, handler改造点, storage/derive/refund影响面, test matrix, commit slicing, Owner decision points. |
| Out of scope | 公开 receipt JSON 增加 `user_id`、改 `LICENSE`、读参考项目源码、读 Claude plan/prestudy。 |
| Success criteria | Owner 能基于本 plan 选择 schema/backfill 路径；后续实现有明确 fail-closed 行为和 mutation 自检测试。 |
| Blast radius | receipt 表/owner 映射、receipt storage 接口、GET/verify handler、mismatch refund receipt sequence/idempotency lookup。 |
| Primary risk | DB migration 锁表或 append-only 语义被破坏；legacy owner 无法回填时误放行。 |

## §1 问题陈述 + Impact

当前 receipt 持久化表没有 `user_id`。初始 schema 只有 `tenant_id`、`request_id`、费用、签名、时间等字段（`backend/sql/migrations/0028_user_cost_receipts.up.sql:3`-`backend/sql/migrations/0028_user_cost_receipts.up.sql:16`），后续 sequence 唯一索引也只是 `(tenant_id, request_id, receipt_sequence)`（`backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:10`-`backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql:11`）。与此同时，session 已经有 `UserID` 字段（`backend/internal/auth/session_middleware.go:15`-`backend/internal/auth/session_middleware.go:21`），middleware 也会从 validated session 填入 `TenantID` 和 `UserID`（`backend/internal/auth/session_middleware.go:60`-`backend/internal/auth/session_middleware.go:63`）。

用户可达的 GET receipt handler 只把 `ident.TenantID` 传给 storage（`backend/internal/gatewayhttp/cost_receipt_handler.go:98`-`backend/internal/gatewayhttp/cost_receipt_handler.go:107`），返回前也只比较 `receipt.TenantID != ident.TenantID`（`backend/internal/gatewayhttp/cost_receipt_handler.go:120`-`backend/internal/gatewayhttp/cost_receipt_handler.go:123`）。PGX storage 的最新 receipt 查询同样只按 `request_id + tenant_id` 过滤（`backend/internal/audit/receipt_storage_pgx.go:101`-`backend/internal/audit/receipt_storage_pgx.go:118`），指定 sequence、refund idempotency、refunded、max sequence 也分别只含 tenant 边界（`backend/internal/audit/receipt_storage_pgx.go:130`-`backend/internal/audit/receipt_storage_pgx.go:150`, `backend/internal/audit/receipt_storage_pgx.go:202`-`backend/internal/audit/receipt_storage_pgx.go:227`, `backend/internal/audit/receipt_storage_pgx.go:239`-`backend/internal/audit/receipt_storage_pgx.go:256`, `backend/internal/audit/receipt_storage_pgx.go:268`-`backend/internal/audit/receipt_storage_pgx.go:281`）。

Verify path 也只验证 tenant。它要求 session 存在（`backend/internal/gatewayhttp/cost_receipt_handler.go:139`-`backend/internal/gatewayhttp/cost_receipt_handler.go:143`），但有效签名后只调用 `userReceiptBelongsToTenant(req, ident.TenantID)`（`backend/internal/gatewayhttp/cost_receipt_handler.go:222`-`backend/internal/gatewayhttp/cost_receipt_handler.go:224`）。派生 receipt 分支也只比较 derived tenant 和 submitted tenant（`backend/internal/gatewayhttp/cost_receipt_handler.go:231`-`backend/internal/gatewayhttp/cost_receipt_handler.go:234`）。`userReceiptBelongsToTenant` 本身只比较 raw `tenant_id` 或 `tenant_scope_ref`（`backend/internal/gatewayhttp/cost_receipt_handler.go:429`-`backend/internal/gatewayhttp/cost_receipt_handler.go:435`）。

Impact: 同租户内任一用户如果知道或猜到另一个用户的 `request_id`，可以读取该 receipt 的 model、token、micro-USD cost、rate snapshot、状态、签名等字段；这些字段在 user response 中明确输出（`backend/internal/gatewayhttp/cost_receipt_handler.go:54`-`backend/internal/gatewayhttp/cost_receipt_handler.go:76`, `backend/internal/gatewayhttp/cost_receipt_handler.go:382`-`backend/internal/gatewayhttp/cost_receipt_handler.go:400`）。Verify path 还可能让同租户非 owner 对别人的 receipt 做有效性探测，甚至在 mismatch 分支触发 refund enqueue，当前保护只覆盖跨租户（`backend/internal/gatewayhttp/cost_receipt_handler.go:243`-`backend/internal/gatewayhttp/cost_receipt_handler.go:247`）。

## §2 Schema 选项（含 backfill 策略）

### Option A — 推荐：新增 `user_cost_receipt_owners` sidecar owner 表

新增 owner 映射表，不 UPDATE `user_cost_receipts`。建议字段：`tenant_id`, `request_id`, `receipt_sequence`, `user_id`, `claim_id`, `owner_source`, `created_at`；主键 `(tenant_id, request_id, receipt_sequence)`；查询索引 `(tenant_id, user_id, request_id, receipt_sequence DESC)`。可以 FK 到 `users(tenant_id,id)`，因为 users 已有 `(tenant_id,id)` unique index（`backend/sql/migrations/0007_l0_inbound_auth.up.sql:18`-`backend/sql/migrations/0007_l0_inbound_auth.up.sql:32`）。

Backfill: 批量 `INSERT INTO user_cost_receipt_owners ... SELECT`，从 `user_cost_receipts` 通过 `billing_events(tenant_id, audit_request_id)` 找 claim，再取 `billing_ledger_claims.user_id`。`billing_events.audit_request_id` 已有列和索引（`backend/sql/migrations/0029_billing_events_audit_request_id.up.sql:3`-`backend/sql/migrations/0029_billing_events_audit_request_id.up.sql:8`），claim 表有 `user_id`（`backend/sql/migrations/0002_observability_billing.up.sql:19`-`backend/sql/migrations/0002_observability_billing.up.sql:31`）。几十万行按 receipt `id` range 分批；每批记录 inserted/missing/ambiguous 计数。未匹配 legacy row 不生成 owner 映射，用户路径 fail-closed。

优点：不破坏 receipt append-only 物理行；历史回填是 INSERT-only；锁表风险最低。缺点：写入 receipt 后必须同事务写 owner 映射，storage 需要保证 receipt 与 owner 原子性。

### Option B — 直接给 `user_cost_receipts` 加 `user_id`

新增 nullable `user_id BIGINT`，新 receipt INSERT 必填；回填完成后加 NOT NULL 和 `(tenant_id,user_id,request_id,receipt_sequence)` 索引，所有 storage WHERE 加 `user_id`。

Backfill: 和 Option A 一样从 `billing_events.audit_request_id -> billing_ledger_claims.user_id` 推导。问题是 receipt 表有 append-only UPDATE/DELETE trigger（`backend/sql/migrations/0028_user_cost_receipts.up.sql:24`-`backend/sql/migrations/0028_user_cost_receipts.up.sql:40`），直接 UPDATE legacy rows 会被挡住。若选此方案，Owner 必须明确批准临时 trigger bypass 或改 trigger 放行一次性 owner metadata backfill。

优点：查询最简单。缺点：触碰 append-only 语义，migration 锁和回滚风险最高。

### Option C — `user_id + claim_id` 一起持久化到 receipt

在 receipt 行或 sidecar 中同时保存 `claim_id` 和 `user_id`。`CostReceipt` 已有 `ClaimID`（`backend/internal/audit/receipt_formatter.go:61`-`backend/internal/audit/receipt_formatter.go:79`），但当前 INSERT 没有持久化 claim/user（`backend/internal/audit/receipt_storage_pgx.go:70`-`backend/internal/audit/receipt_storage_pgx.go:90`）。`ReceiptInputs` 现在也只有 tenant、claim、cost/token/time，没有 user（`backend/internal/audit/receipt_formatter.go:81`-`backend/internal/audit/receipt_formatter.go:92`）。

Backfill: 和 Option A 相同，但把 matched claim_id 一并写入 owner 表或 receipt 列。未匹配 legacy 行不猜测。优点是后续 refund/审计排障更清楚；缺点是比 P0 所需多一个 schema 字段，需确认不会扩大本 slice。

### Option D — 不存 owner，读时 join billing owner

不新增 owner 存储；GET/verify 时通过 `request_id + tenant_id` join `billing_events` 和 `billing_ledger_claims` 得到 owner。当前 receipt input SQL 已经按 audit request join billing facts（`backend/internal/audit/receipt_formatter.go:475`-`backend/internal/audit/receipt_formatter.go:501`），可以复用思想。

Backfill: 无。上线前加必要索引并用 production-like 数据压测。优点是实现快；缺点是每次读取依赖 billing retention/可用性，legacy billing 缺失时必须 fail-closed，且用户可见 receipt availability 会被账务表状态耦合。

### Option E — 只在 handler 用 tenant 校验，不改 schema

拒绝作为 P0 方案。它无法证明同租户 owner，mutation “删除 user check” 不会变红，也不能覆盖 legacy 数据。

## §3 Handler 改造点逐个

### GetReceipt (`GET /v1/receipts/{request_id}`)

改 `CostReceiptReader` 合约，从 `GetReceipt(ctx, requestID, tenantID)`（`backend/internal/gatewayhttp/cost_receipt_handler.go:27`-`backend/internal/gatewayhttp/cost_receipt_handler.go:29`）升级为带 `userID` 的 owner-aware 方法，或新增 `GetReceiptForUser(ctx, requestID, tenantID, userID)` 避免混淆内部 server-only lookup。handler 从 session 读取 `ident.UserID`，若 `TenantID <= 0` 或 `UserID <= 0`，fail-closed，不调用 storage。

storage 层按 schema 决策实现：Option A 使用 owner sidecar join；Option B 直接 `WHERE request_id=$1 AND tenant_id=$2 AND user_id=$3`。返回后保留 defense-in-depth：`receipt.TenantID == ident.TenantID` 且 `receipt.UserID == ident.UserID`，否则统一 404。公开 JSON 不新增 `user_id`，避免改变签名 payload 或泄露内部 ID。

### VerifyReceipt (`POST /v1/receipts/{request_id}/verify`)

Verify 当前是 session-protected endpoint，但 detached verify 在只有 signer 时可返回 valid（`backend/internal/gatewayhttp/cost_receipt_handler_test.go:75`-`backend/internal/gatewayhttp/cost_receipt_handler_test.go:90`）。P0 后要明确改成 account-bound verify：签名有效只是必要条件；还必须证明 receipt owner 是当前 `TenantID + UserID`。

建议新增 owner check dependency，例如 `ReceiptOwnerVerifier` 或复用 owner-aware receipt reader。流程：先做 path/body/schema/signature 校验；当 `valid == true` 时，用 `(requestID, receipt_sequence, tenantID, userID)` 查 owner。owner 不存在、legacy 未回填、依赖未配置、user mismatch，均不能返回 `valid=true`；对用户统一 404 或明确 Owner 决策的 fail-closed error。进入 mismatch/refund enqueue 前再次确认 derived receipt owner 与 session user 一致，不能只做 derived tenant 比较。

### DerivedReceipts / refund enqueue 分支

`CostReceiptDeriver` 现在只按 requestID derive（`backend/internal/gatewayhttp/cost_receipt_handler.go:31`-`backend/internal/gatewayhttp/cost_receipt_handler.go:33`）。`DeriveReceipt` 从 ledger entry 取得 tenant，再查 receipt inputs（`backend/internal/audit/receipt_formatter.go:242`-`backend/internal/audit/receipt_formatter.go:250`），但 `CostReceipt` 当前没有 user。需要让 `ReceiptInputs` 读取 claim/user owner，并把 `UserID` 写入 `CostReceipt`。

`billing_ledger_claims.user_id` 是 settle/usage 的权威来源：settle 已拒绝 req.UserID 与 claim.UserID 不一致（`backend/internal/billing/settler.go:113`-`backend/internal/billing/settler.go:120`），并用 claim.UserID 写 usage record（`backend/internal/billing/settler.go:137`-`backend/internal/billing/settler.go:145`）。cache-hit path 也从 claim 取 userID 写 usage record（`backend/internal/billing/settler.go:417`-`backend/internal/billing/settler.go:420`, `backend/internal/billing/settler.go:478`-`backend/internal/billing/settler.go:483`）。

### 任何 receipt 查 endpoint / 内部查路径

当前 HTTP receipt 路由只有 GET 和 POST verify 这组用户可达路径（测试路由使用 `/v1/receipts/*`，`backend/internal/gatewayhttp/cost_receipt_handler_test.go:594`-`backend/internal/gatewayhttp/cost_receipt_handler_test.go:595`）。此外，refund worker 内部会通过 `GetReceipt`, `GetReceiptBySequence`, `GetByRefundIdempotency`, `MaxReceiptSequence` 查 receipt（`backend/internal/audit/refund_worker.go:212`-`backend/internal/audit/refund_worker.go:229`, `backend/internal/audit/refund_worker.go:641`-`backend/internal/audit/refund_worker.go:650`, `backend/internal/audit/refund_worker.go:675`-`backend/internal/audit/refund_worker.go:708`）。这些是 server-internal，不直接接收 session user，但也要携带 `UserID` 到 refund payload 或 receipt owner lookup，避免后续 append 的 refunded receipt 失去 owner。

`MismatchRefundPayload` 当前没有 user（`backend/internal/audit/refund_worker.go:23`-`backend/internal/audit/refund_worker.go:30`），`NewMismatchRefundEvent` 只写 tenant/claim/request/delta（`backend/internal/audit/refund_worker.go:75`-`backend/internal/audit/refund_worker.go:82`）。后续实现应把 `UserID` 从 mismatched `CostReceipt` 写入 payload，worker append refunded receipt 时保留同一 owner。

## §4 风险

1. **Legacy 数据 owner 不可判定。** 历史 receipt 可能几十万行，且并非每行都能从 `billing_events.audit_request_id` 找回 claim/user。未匹配不能猜测，也不能退回 tenant-only；必须 fail-closed 或进入 Owner 批准的 manual recovery。
2. **Append-only 语义冲突。** `user_cost_receipts` 有 BEFORE UPDATE/DELETE trigger 直接 raise（`backend/sql/migrations/0028_user_cost_receipts.up.sql:30`-`backend/sql/migrations/0028_user_cost_receipts.up.sql:40`）。直接 backfill receipt row 的 `user_id` 需要高风险 Owner 确认；推荐 sidecar 避免 UPDATE。
3. **跨租户边界不能回退。** 新 user check 不能削弱现有 tenant check。所有用户路径仍要保持 `tenant_id` 条件，错误响应保持 404，避免 request_id oracle。
4. **同租户跨用户边界要 fail-closed。** 任何 owner dependency missing、`UserID==0`、legacy owner missing、derived owner mismatch，均不能返回别人的 receipt 或 `valid=true`。
5. **Migration 锁表。** `ALTER TABLE`、FK、普通 `CREATE INDEX` 可能阻塞 writes；`CREATE INDEX CONCURRENTLY` 不能包在现有 `BEGIN/COMMIT` 风格 migration 里，需要 Owner 决策 migration runner 是否支持 non-transactional migration。
6. **原子写风险。** Sidecar 方案要求 receipt snapshot 与 owner mapping 同事务写入；普通 `AppendReceipt` 不能先插 receipt 成功再 owner 失败。实现需改为 transaction wrapper 或单 storage API 保证两个 INSERT 同成败。
7. **Frozen package 约束。** `backend/internal/gatewayhttp` 是冻结包；后续只能改现有文件，不能新增 handler/test/helper 文件。`backend/internal/gateway` 和 `backend/internal/proto` 同样不得新增文件。

## §5 测试矩阵（含 mutation 自检）

| Test ID | Area | Fixture / action | Expected | Mutation self-check |
|---|---|---|---|---|
| T1 | storage GET | 同 tenant，receipt owner=user100；用 user200 查同 request_id | `ErrReceiptNotFound` | 删除 SQL/user owner 条件会返回 user100 receipt，测试变红 |
| T2 | storage latest sequence | user100 有 sequence 0/1；user200 查 latest | not found | `ORDER BY` 仍在但 user 条件删除会泄露 sequence 1 |
| T3 | refund idempotency lookup | user100 receipt adjustment_refs 命中；user200 同 tenant 同 request_id 查 | not found/no enqueue | 删除 owner join 会命中 user100 refund receipt |
| T4 | handler GET | session `{TenantID:7,UserID:200}` 请求 user100 receipt | 404，body 不含 receipt cost/model | handler 不传 UserID 或只检查 tenant 时返回 200 |
| T5 | handler missing user | session TenantID 有值但 UserID=0 | fail-closed，且 storage stub 未被调用 | 去掉 UserID precondition 后 storage call count 变 1 |
| T6 | verify detached | user200 提交 user100 的 valid signed receipt，同 tenant | 404 或 Owner 决定的 fail-closed error；`valid=true` 不允许 | 只保留 tenant check 会返回 OK valid |
| T7 | verify mismatch refund | user200 提交 user100 receipt 且 derived mismatch | 不 enqueue refund | 删除 owner check 会触发 queue.calls=1 |
| T8 | derived owner | `DeriveReceipt` 从 claim user_id 派生 `CostReceipt.UserID` | returned receipt.UserID == claim.UserID | 不 select/assign user_id 时为 0，测试变红 |
| T9 | backfill happy path | receipt + billing_event.audit_request_id + claim.user_id | owner row inserted with expected user | join 错字段或漏 tenant 条件会 count/owner mismatch |
| T10 | backfill ambiguous | 同 receipt join 到多个不同 user claim | 不写 owner，记录 ambiguous | 使用 `LIMIT 1` 随便选会写 owner，测试变红 |
| T11 | legacy missing owner | receipt 无 owner mapping | user endpoint 404/fail-closed | fallback tenant-only 会返回 200 |
| T12 | cross-tenant regression | tenant8 receipt，tenant7 user 查 | 404 | 删除 tenant 条件但保留 user 条件不能通过 |
| T13 | public JSON | owner-aware GET 成功 | response 仍不含 raw `user_id` | accidentally exposing user_id makes body assertion fail |
| T14 | migration locking rehearsal | production-like rows, online migration dry-run | bounded lock time, backfill batch metrics | single giant update/locking index violates time budget |

测试落点：`backend/internal/audit` storage/derive/backfill tests 可新增或改现有测试文件；`backend/internal/gatewayhttp` 冻结包内只能改现有 `cost_receipt_handler_test.go` / `refund_receipt_flow_test.go`，不能新增文件。所有新测试必须说明 killed risk，并用“去掉 user 条件就变红”的判别性 fixture，不能只测“能跑”。

## §6 Commit 切片（一 commit 一模块）

1. **Commit 1 — schema + migration rehearsal assets.** 新增 owner schema/backfill migration 或 chosen schema migration；包含 dry-run SQL/test harness。若选择 `CREATE INDEX CONCURRENTLY`，先确认 migration runner 支持 non-transactional file。必须 codex review before commit。
2. **Commit 2 — audit domain owner propagation.** `CostReceipt` / `ReceiptInputs` 增 `UserID`，receipt input SQL 从 claim/usage 读取 user，validation 要求正数。storage 写入 owner mapping，并提供 owner-aware read methods。测试覆盖 T1/T2/T8/T9/T10/T11。
3. **Commit 3 — refund worker owner preservation.** mismatch refund payload 携带 `UserID`，sequence/idempotency/existing receipt lookup owner-aware 或通过 payload owner 校验。测试覆盖 T3/T7。
4. **Commit 4 — gatewayhttp existing-file patch.** 改 `CostReceiptReader` handler 合约、GET fail-closed、Verify owner check。只修改现有 frozen package 文件，不新增文件。测试覆盖 T4/T5/T6/T12/T13。
5. **Commit 5 — integration/docs/release gate.** 加 production migration runbook、legacy unmatched handling、metrics/check queries、rollback/fail-forward notes。跑全量相关 tests，stage 后执行 `codex exec review --uncommitted --full-auto`。

## §7 D 决策点列表（Owner 必决）

- **D-001 Schema path:** 选 Option A sidecar（推荐）还是 Option B 直接 receipt.user_id，或 Option C/D。
- **D-002 Legacy unmatched policy:** owner 无法回填的历史 receipt 是永久 404、管理员手工认领、还是重新签发新 receipt。
- **D-003 Append-only exception:** 若选 direct column backfill，是否允许临时绕过或修改 append-only trigger。
- **D-004 Verify semantics:** `/v1/receipts/{id}/verify` 是否正式定义为“session user owner-bound verify”。推荐是；否则它和 P0 user 隔离冲突。
- **D-005 Fail-closed status code:** owner missing/user mismatch 对外统一 404，还是 dependency missing 用 503；推荐 user mismatch/legacy missing 404，dependency misconfigured 503。
- **D-006 Migration mode:** 是否允许 non-transactional concurrent index migration；是否需要 maintenance window；批大小和 lock timeout。
- **D-007 Public receipt schema:** 是否保持 user_id 只在 DB/internal，不进入 user JSON/canonical signature。推荐保持隐藏，避免签名兼容风险。
- **D-008 FK strictness:** owner table 是否立刻 FK 到 `users(tenant_id,id)`；严格 FK 更安全，但可能被历史 synthetic fixtures 或旧数据挡住。
- **D-009 Rollout flag:** 是否需要短期 feature flag `receipt_owner_enforcement`。推荐只用于 staged rollout，默认目标必须 enforce；不能作为长期绕过。

## §8 实施时间估

估为**高难度，多切片**，不是中难度单切片。原因：涉及 schema/backfill、append-only trigger、storage interface、HTTP handler、verify/refund side effects、legacy 数据策略和 migration 锁表风险。

预计 5 个 commit 切片。代码和测试约 1.5-2.5 agent-days；生产数据 dry-run、migration rehearsal、Owner 决策等待另计。若 Owner 选择 Option D 读时 join，可缩到 1 个高风险快速切片，但长期可维护性和 availability 风险更差。

## Pre-execution Checklist

- [ ] Owner 确认 §7 D-001 到 D-009。
- [ ] 不新增任何文件到 `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto`。
- [ ] 确认 migration runner 是否支持 `CREATE INDEX CONCURRENTLY` / non-transactional migration。
- [ ] 准备 production-like backfill dry-run：总行数、matched、missing、ambiguous、batch latency、lock wait。
- [ ] 每个测试都有 mutation 自检说明：删除 user condition / 只保留 tenant 时必须变红。
- [ ] 每个 commit stage 后跑 `codex exec review --uncommitted --full-auto`，高风险 schema slice 需要 Owner gate。

## 5 行中文总结

1. 观察到 receipt 读写链路当前只有 tenant 边界，session 虽有 UserID 但 GET/verify/storage 没使用。
2. 推荐用 owner sidecar 表做历史 backfill，避免 UPDATE append-only receipt 行。
3. 后续实现必须让 GET、verify、refund receipt 内部查找全部带 owner 语义并 fail-closed。
4. 没有功能缩水：receipt 仍可读、可验、可退款补偿，但只对 owner 用户可见。
5. 需要 Owner 先决定 schema path、legacy unmatched 策略、verify 语义、migration 锁表方式。

Source files read:
- `/home/codex/.codex/skills/using-superpowers/SKILL.md`
- `/home/codex/.codex/skills/writing-plans/SKILL.md`
- `/home/codex/.codex/skills/brainstorming/SKILL.md`
- `.agents/skills/acceptance-test-writer/SKILL.md`
- `backend/internal/audit/receipt_storage_pgx.go`
- `backend/internal/auth/session_middleware.go`
- `backend/internal/gatewayhttp/cost_receipt_handler.go`
- `backend/internal/audit/receipt_storage.go`
- `backend/internal/audit/receipt_formatter.go`
- `backend/internal/audit/refund_worker.go`
- `backend/internal/billing/billing.go`
- `backend/internal/billing/settler.go`
- `backend/internal/gatewayhttp/cost_receipt_handler_test.go`
- `backend/internal/gatewayhttp/refund_receipt_flow_test.go`
- `backend/sql/migrations/0002_observability_billing.up.sql`
- `backend/sql/migrations/0007_l0_inbound_auth.up.sql`
- `backend/sql/migrations/0013_trust_chain_audit_ledger.up.sql`
- `backend/sql/migrations/0028_user_cost_receipts.up.sql`
- `backend/sql/migrations/0029_billing_events_audit_request_id.up.sql`
- `backend/sql/migrations/0032_audit_mismatch_refund_pending.up.sql`
- `backend/sql/migrations/0033_user_cost_receipts_sequence.up.sql`
- `backend/sql/migrations/0036_user_cost_receipts_unknown_state.up.sql`

Lane: codex-plan
Agent: GPT-5 Codex / codex-plan-2026-05-23-receipt-owner-isolation
UTC timestamp: 2026-05-23T14:25:54Z
