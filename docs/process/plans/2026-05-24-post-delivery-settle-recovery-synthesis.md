# 2026-05-24 P2/P3 流式 post-delivery settle 失败 durable 兜底 — Synthesis

## §0 Inputs

- Claude lane plan: [2026-05-24-post-delivery-settle-recovery-claude.md](2026-05-24-post-delivery-settle-recovery-claude.md)
- Codex lane plan: [2026-05-24-post-delivery-settle-recovery-codex.md](2026-05-24-post-delivery-settle-recovery-codex.md)(由 Claude 代写落档)
- Prestudy ref scan: codex bazn7km4v 输出(sub2api/new-api/litellm 三者无 durable outbox)

## §1 两 lane 共识 (agreement)

| 共识点 | Cite |
|---|---|
| **复用 `usage_record_dlq` 表,不新建表**;只加 CHECK constraint 取值 | Claude §3 / Codex §1 |
| Schema migration 是 Owner schema-gate(0053 ALTER CHECK) | Claude §3.2 / Codex §2 |
| Enqueue 必须另起事务(settle 失败时 tx 状态污染) | Claude D-004 / Codex 隐式(独立 enqueue 函数) |
| Worker 重 settle 走 **public** `Settler.Settle`,不重写底层 SQL | Claude D-006 / Codex D3 |
| 流式主路径 stream.go:247 + 非流式 direct settle billing.go:163-179 都要兜底 | 两 lane 一致 |
| 三个借鉴项目都无 durable outbox(架构 + 算法 + 生态三维升级) | prestudy b06c0srmp / Codex §8 |
| Mutation-discriminating 测试矩阵 ≥ 6 项 | 两 lane 一致 |

## §2 两 lane 差异(选 codex 方案 — 更严)

| 维度 | Claude 提案 | Codex 提案 | 决议 |
|---|---|---|---|
| **EventKind 名称** | `pending_settle` | `post_delivery_settlement` | **采纳 codex** — 名字精准突出"已交付" |
| **Payload 序列化** | JSON marshal `RequestCompletionEvent` | DTO(因 `SettleRequest.OutboxEmitter func() bool` 不可 JSON 化) | **采纳 codex** — Claude 漏了 func() field |
| **Eventbus billing handler 失败兜底** | 未提 | `observability/billing_persister_handler.go:56-83` 失败也走新 kind + 自定义 payload | **采纳 codex** — Claude 漏了这个入口 |
| **Already-committed 判定** | 默认信任 Settler 内部状态拦截 | 三证 proof:claim status='committed' + usage_records 行 + billing_events(claim_committed) | **采纳 codex** — money-path 不能假阳性 |
| **新包位置** | `internal/billing/post_delivery_recovery.go` | 独立新包 `internal/settlementrecovery/` | **采纳 codex** — 跨 billing/gatewayhttp/audit 多调用方,独立包更内聚 |
| **0053 down.sql** | 简单 DROP+ADD CHECK | 加 `DO $$ ... RAISE EXCEPTION` 如果有 post_delivery_settlement 行存在 | **采纳 codex** — 防回滚丢数据 |
| **D 决策点数** | 8 (D-001~D-008) | 5 (D1-D5) | 用 codex 5 个核心 + Claude D-008(quarantine 后 SOP)合到 runbook |

**结论**:codex plan 更严 + 更完整 + 关键点(SettleRequest.OutboxEmitter / eventbus billing handler 兜底)Claude 漏了。**主体采纳 codex 方案**,Claude 补 quarantine SOP 进 runbook(C6 切片)。

## §3 Schema gate(Owner 必批)

```sql
-- 0053_post_delivery_settlement_dlq_kind.up.sql
BEGIN;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'post_delivery_settlement'));
COMMIT;
```

`.down.sql`:codex 设计 — 反向时如果有 `event_kind='post_delivery_settlement'` 行存在,RAISE EXCEPTION 阻断回滚(防 DLQ 数据丢失);否则正常 DROP+ADD 回 0050 的 7-value set。

风险 MED:CHECK constraint 是 EXCLUSIVE ACCESS lock,但数据不重写;maintenance window 几秒。

## §4 D 决策点(全部采用 codex 推荐,Owner 批准开干)

| D | 决策 | Owner 拍 |
|---|---|---|
| **D1** EventKind 名称 = `post_delivery_settlement` | 采用 codex | Owner 批 ✓ / 改 |
| **D2** Enqueue site = `settleCompletion` postDelivery 模式 + eventbus billing handler 自定义 payload | 采用 codex | Owner 批 ✓ / 改 |
| **D3** Worker = 重调 public Settler.Settle + 三证 proof | 采用 codex | Owner 批 ✓ / 改 |
| **D4** DLQ persist 也失败兜底 = metric + alert + operator_review(本切片只做 A) | 采用 codex | Owner 批 ✓ / 加 RR-W6-X 跟踪 B(local disk spool)单独 gate |
| **D5** Already-committed proof = 三证(claim + usage + billing_event) | 采用 codex | Owner 批 ✓ / 改 |
| **D-cache-hit** L2 cache-hit settle 失败是否一并兜底 | **加** — 同入口 settleCompletion,自动覆盖(Claude D-003 余下确认) | Owner 批 ✓ |
| **D-runbook** Quarantine 后 SOP(dashboard + 手动 admin endpoint replay) | runbook 写明 | Owner 批 ✓ |

## §5 切片(综合 6 commit)

| C | 模块 | 范围 | 风险 |
|---|---|---|---|
| C1 | sql/migrations + dlq | 0053 ALTER CHECK + down.sql RAISE EXCEPTION + `EventKindPostDeliverySettlement` const + types.go ValidatePayload 加 case + dlq 包单测 | HIGH(schema gate Owner 批) |
| C2 | settlementrecovery | 新包 + DTO `payload.go` + `enqueue.go` + `handler.go` + 三证 proof + 单测(spy settler 验 Settle 调用 + mutation 红体) | MED |
| C3 | eventbus + observability | eventbus/types.go 加 handler-supplied payload interface + billing_persister_handler DLQKind() + 单测 | MED |
| C4 | gatewayhttp | chat_completions_handler 加 `SettleRecoveryDLQ` sink + settleCompletion 加 postDelivery 选项 + stream.go:247 + billing.go:163 接入 + 单测 | MED(冻结包 — 只改既有文件,符合 #13) |
| C5 | cmd/gateway | routes.go + lifecycle.go 注册 handler + wire `SettleRecoveryDLQ: d.dlqService` + 集成测试 | LOW |
| C6 | docs + runbook | runbook 加 P2/P3 章节 + Quarantine SOP + 全量 verify + RR-W5-008 关闭 + RR-W6-X 开(local disk spool 候选) | LOW |

每个 commit 按 [feedback_one_commit_one_module] 一模块;每 commit 按规则 #8 跑 codex per-commit review。

## §6 测试矩阵(8 项,全 mutation-discriminating)

采用 codex §5 完整列表 + Claude 补 cross-tenant 隔离 + idempotency:

| Test | File | Mutation 红体 |
|---|---|---|
| T1 Stream delivered + settle 失败 → 1 行 post_delivery_settlement DLQ | chat_completions_stream_test.go | 删 enqueue → 0 行红 |
| T2 Stream settle 成功不 enqueue | chat_completions_stream_test.go | 无条件 enqueue → 红 |
| T3 非流式 pre-delivery settle 失败不 enqueue | chat_completions_billing_test.go | 所有 settle 错都 enqueue → 红 |
| T4 nil bus / direct settle 路径覆盖 | stream / billing helper unit | 只 eventbus DLQ 路径 → 红 |
| T5 Eventbus billing handler 失败 payload 可重放 | eventbus/observability test | 留 usage_record kind 或 generic payload → 红 |
| T6 DLQ handler 调 Settler.Settle | settlementrecovery/handler_test.go | mark delivered 不调 settler → 红 |
| T7 Already-committed proof 严格 | settlementrecovery/handler_test.go | proof false 视 success → 红 |
| T8 Migration CHECK + down RAISE | migration integration | 漏 CHECK value → insert 失败;down 时有行 → RAISE EXCEPTION |
| T9(Claude 补)Cross-tenant 隔离 | settlementrecovery/handler_test.go | 删 tenant_id 字段 → 串租户红 |
| T10(Claude 补)Idempotency 同 claim_id 重复 enqueue 走 ON CONFLICT | enqueue_test.go | 删 idempotency_key → 行数 != 1 红 |

## §7 风险

采用 codex §7 表 + Claude 补 quarantine 人工 SOP 风险。

主 HIGH 三项:重复扣费(Settler 严格 proof 守)/ DB 同时不可写(D4 alert,B 选项另起)/ Schema migration 未先于代码上线(强行 schema-gate 阻断)。

## §8 时间估

- Schema gate Owner 批: ad-hoc
- C1-C6 实施: 1-1.5 working day(纯 Claude 主笔,因为 money path + [feedback_anti_detection_specs_claude_writes] 类敏感;codex 只用于 per-commit review)
- 全量 PG round-trip + race sweep: 30 min
- 总: ≈ 2 working day

## §9 Lane attribution

- Claude lane planner: 本对话 session
- Codex lane planner: bazn7km4v(独立跑,sandbox read-only,本文件由 Claude 把 stdout 落档)
- Prestudy ref scan: b06c0srmp(sub2api/new-api/litellm)
- Synthesis: 2026-05-24T04:15:00Z
