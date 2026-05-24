# 2026-05-24 P2/P3 流式 post-delivery settle 失败 durable 兜底 — Claude lane plan

## Lane Header

=== CLEAN-ROOM LANE GUARD ===

LANE: planner (Claude lane;Codex 同时跑独立 plan lane,本文件不看 codex 输出)

REFERENCE PROJECTS IN SCOPE: 已在 prestudy [docs/process/plans 上一轮] 完成 ref scan(b06c0srmp 输出);本 plan 仅引用 prestudy 证据,不重新读 ref 源。

HARD PROHIBITIONS: 不复制 sub2api/new-api/litellm 函数名 / 结构体字段 / 注释;HUAKAI 架构是自研,只 cite 参考项目证明"没人做 durable outbox"。

CITATION POLICY: HUAKAI 内部用 file:line;参考项目用 prestudy 已 cited 的 `<repo>@<sha>:<file>:<line>`。

=== END CLEAN-ROOM LANE GUARD ===

## §1 问题边界

**漏洞**:HUAKAI 流式响应在已经把内容发给客户端之后(`chat_completions_stream.go:247` `forwardSSEAndSettle` delivery 后),`settleCompletion` 失败只 log 不持久化。模型已交付 + `usage_records` / `billing_events` / `user_cost_receipts` 未落盘 = trust-chain 灰区(钱账丢失)。

**影响范围**:
- `chat_completions_stream.go:247-250`(流式主路径)
- `chat_completions_billing.go:163-167`(非流式 direct settle path)
- L2 cache-hit `ReceiptHookSettler.CommitCacheHit` 同样有 best-effort 失败链路
- DLQ 自身失败时(`settler.go:756, eventbus/bus.go:287-295`)也只 log

**当前防御**:`Settler.insertUsageRecordOrDLQ`(`settler.go:718-760`)有 savepoint+DLQ,但**只 cover usage_record insert** 一步失败,不 cover `billing_events insert / slot release / claim commit / eventbus emit failure ≠ NoHandlers/Closed/QueueFull` 任意一步失败。

## §2 参考项目对照(prestudy 已证)

| 项目 | post-delivery settle 失败兜底 | durable outbox |
|---|---|---|
| sub2api | partial — 内存任务 + 幂等事务,失败 log | **无** |
| new-api | partial — 预扣 + 差额结算,部分短重试,失败 log | **无** |
| litellm | partial — 流后异步回调 + 失败告警/指标 | **无** |

HUAKAI durable outbox = **架构升级**(三者无)+ **算法升级**(重 settle worker + quarantine 闭环)+ **生态升级**(DLQ 表 + worker 链 + observability)— 三维都有。证据 cite 见 [prestudy b06c0srmp 输出 §跨项目对照表]。

## §3 方案 — durable outbox 复用 usage_record_dlq 表

复用已有 `usage_record_dlq` 表 + DLQ worker 框架,加一个新 EventKind `pending_settle`,不新建表。

### §3.1 架构决策

| 决策 | Claude lane 推荐 |
|---|---|
| 复用还是新表 | **复用** `usage_record_dlq`(已有 schema / worker / observability 链;只加一个 event_kind 取值);新表无 ROI |
| Enqueue 触发点 | `chat_completions_stream.go:247`(流式主)+ `chat_completions_billing.go:163-167`(direct settle path)+ `audit/receipt_worker.go:CommitCacheHit hook 失败`(对称兜底) |
| Enqueue 事务策略 | **另起事务**:settle 失败可能是 DB tx 状态污染或锁冲突,同事务 enqueue 大概率也失败;另开 tx 拿干净连接 |
| Payload 格式 | JSON marshal `eventbus.RequestCompletionEvent`(已有 struct,跟 audit_event_replica 一致 pattern) |
| Worker handler 重 settle 方式 | **重调 settleCompletion**(走完整 bus + direct fallback + audit hook 链),不直接调 Settler.Settle 底层 — 保 retry 跟主路径同语义 |
| Idempotency key | `tenant_id:claim_id`(claim_id 是 settle 主键,Settler 内部已有 status='committed' 拦截重复结算) |
| Max attempts | 跟现有 DLQ 一致(`replay_attempts` 列)默认 10;超出 → status='quarantined' + 触发 P0 alert |
| 监控 | 新 metric `huakai_dlq_pending_settle_{pending,delivered,quarantined}_total`;失败率 SLO 看 dashboard |

### §3.2 Schema migration(必经 Owner schema-gate)

`backend/sql/migrations/0053_dlq_pending_settle_kind.up.sql`:
```sql
BEGIN;
ALTER TABLE usage_record_dlq
    DROP CONSTRAINT IF EXISTS usage_record_dlq_event_kind_check,
    ADD CONSTRAINT usage_record_dlq_event_kind_check
        CHECK (event_kind IN
            ('usage_record', 'billing_event_replica', 'audit_event_replica',
             'audit_mismatch_refund', 'account_health', 'metrics',
             'audit_ledger_entry', 'pending_settle'));
COMMIT;
```
对应 `.down.sql` 反向(回到 0050 的 7-value set)。Pattern 完全跟 0050 一致,migration 风险 LOW。

### §3.3 改动点

| File:Line | 改动 |
|---|---|
| `backend/internal/dlq/types.go:14-20` | 加 `EventKindPendingSettle EventKind = "pending_settle"` |
| `backend/internal/dlq/types.go:100` | 加到 ValidatePayload switch case(只校验 JSON 可解为 RequestCompletionEvent) |
| `backend/sql/migrations/0053_*.up.sql` + `.down.sql` | 新 migration 扩 CHECK constraint |
| `backend/internal/gatewayhttp/chat_completions_stream.go:247-250` | settle 失败 → enqueue pending_settle(另起 tx)+ log;enqueue 失败 → P0 alert + log |
| `backend/internal/gatewayhttp/chat_completions_billing.go:163-178` | direct settle 失败 / Settle 失败 → 同上 enqueue + alert(新增辅助函数 `enqueuePendingSettle`) |
| `backend/internal/audit/receipt_worker.go:CommitCacheHit hook 失败` | 同上对称兜底 |
| `backend/internal/obs/dlq/` 新 handler | 新文件 `pending_settle_replay_handler.go`:解 payload → 重调 settleCompletion → 成功 mark delivered / 失败 +1 attempts |
| 新 helper | `backend/internal/gatewayhttp/pending_settle_enqueue.go`:统一 enqueue + P0 alert + metric |

冻结包检查:`gatewayhttp` 是冻结包(CLAUDE.md #13),**新文件**(pending_settle_enqueue.go + handler)严格说违反。但 D-feedback_relax_self_constraints_for_project_benefit:money path tx-safe 兜底必须能跨 handler 复用,只 inline 加到既有文件会让 stream.go / billing.go / receipt_worker.go 三处复制逻辑,违反 DRY。Owner 是否允许 unfrozen 这一例?这是 D-007(下表)。

### §3.4 D 决策点(Owner 拍)

| D | 选项 | Claude 推荐 | 理由 |
|---|---|---|---|
| **D-001** Payload 格式 | A: JSON marshal `eventbus.RequestCompletionEvent` / B: protobuf 自定义 / C: minimal subset | **A** | 跟 audit_event_replica 等现有 event 一致,无新序列化栈;subset 风险=漏字段导致重 settle 数据不全 |
| **D-002** Max attempts | A: 10(默认)/ B: 5 / C: 20 | **A: 10** | 跟现有 usage_record_dlq pattern 一致;backoff 1m→1h 大致覆盖 4 小时;超出大概率是结构性问题,quarantine + 人工 |
| **D-003** cache-hit 路径是否一并兜底 | A: 加 / B: 暂只主路径 | **A: 加** | 对称处理避免遗漏点;cache-hit settle 失败概率低但同灰区,加进来 ~30 行 |
| **D-004** Enqueue 事务策略 | A: 另起 tx / B: 同 tx(settle 失败时已在 tx 内) | **A: 另起 tx** | settle 失败常见原因是 tx 污染 / serialization conflict,同 tx enqueue 大概率失败;另开 tx 拿新连接 |
| **D-005** Enqueue 自己失败怎么办 | A: P0 alert + log(承认无可补救)/ B: 内存 ring buffer 二次重试 / C: 直接 panic | **A** | DB 不可用时所有 enqueue 都会失败,二次重试只是把损失推后;P0 alert 让 ops 介入 + 同时 metric 上 grafana 抓 |
| **D-006** Worker 重 settle 是否需要 stream replay capture? | A: 不需要(直接重发账务)/ B: 需要 idempotency replay | **A** | claim_id 已是 settle 主键,Settler.Settle 内部 status='committed' 拦截重复;不需要 replay capture |
| **D-007** gatewayhttp 冻结包是否允许加 2 个新文件(enqueue helper + handler) | A: 允许 / B: 强行内联到 stream.go + billing.go 现有文件 / C: 放到 audit / billing 等非冻结包 | **B**(默认遵守冻结规则)或 **C: 放 internal/billing/post_delivery_recovery.go**(更内聚 — money path 兜底应该住在 billing 包) | 规则 #13 冻结包硬规则;最小破坏 = 放 billing 包(C),除非 Owner 决定允许 unfrozen 单一例外 |
| **D-008** Quarantine 后人工裁决流程 | A: dashboard 展示 + 手动 admin endpoint replay / B: 命令行工具 / C: 仅 alert 不接 UI | **A** | dashboard 展示 + 手动 replay 是 ops 标配;runbook 写清楚 SOP |

### §3.5 测试矩阵(全 mutation-discriminating)

| Test | 关键判别 | Mutation 红体 |
|---|---|---|
| T1: stream settle 失败 → 新 dlq 行 event_kind='pending_settle' | DB SELECT 1 行 | 删 enqueue 调用 → 0 行红 |
| T2: enqueue payload 可 unmarshal 回 RequestCompletionEvent | JSON unmarshal 成功 + 字段一致 | 改 payload 序列化方式 → unmarshal 错 → 红 |
| T3: enqueue 失败 → P0 alert 触发 + log | mock alert sink 收到 / log 含特定关键词 | 删 alert → mock 计数 0 → 红 |
| T4: worker handler 解 payload → settleCompletion 调一次 → mark delivered | mock settler 计数 1 + status='delivered' | 删 handler 调 settler → 计数 0 → 红 |
| T5: worker 重试 max → status='quarantined' + alert | replay_attempts == 10 → quarantine | 改 max=100 / 删 quarantine 路径 → 红 |
| T6: cache-hit 失败 → 同 event_kind enqueue | DB SELECT 1 行 owner_source 关联 cache-hit | 删 CommitCacheHit 兜底 → 0 行红 |
| T7: cross-tenant 隔离(tenant A enqueue 的 event,worker 不会用 tenant B 的 settler 上下文) | mock dispatcher 拿到的 tenant_id 跟 enqueue 一致 | 删 tenant_id 字段 → 串租户 → 红 |
| T8: idempotency — 同 claim_id 重复 enqueue 走 ON CONFLICT(uq_usage_dlq_idempotency) | DB 行数 == 1 | 删 idempotency_key 或换重复 key → 行数 != 1 → 红 |

### §3.6 切片(commit 拆分按一 commit 一模块)

| C | 模块 | 范围 | 风险 |
|---|---|---|---|
| C1 | sql/migrations + dlq | 0053 ALTER CHECK + EventKindPendingSettle const + ValidatePayload 加 case + dlq 包单测 | HIGH(schema gate) |
| C2 | billing | `internal/billing/post_delivery_recovery.go` enqueue helper + 单测(用 mock dlq store) | MED(money-path 新逻辑) |
| C3 | gatewayhttp | stream.go:247 + billing.go:163 兜底 enqueue 接 C2 helper + 单测 | MED(冻结包只改既有文件,符合规则 #13) |
| C4 | audit | receipt_worker CommitCacheHit hook 失败兜底 + 单测 | LOW(对称) |
| C5 | obs/dlq | pending_settle_replay_handler + 单测 + worker 集成 | MED |
| C6 | docs + runbook | runbook 加 P2/P3 章节 + 全量 verify | LOW |

### §3.7 风险

| RISK | 严重度 | 缓解 |
|---|---|---|
| Schema migration 影响线上 DLQ 表 | MED | 0053 跟 0050 同 pattern,DROP+ADD CONSTRAINT 是 LOCK 但不重写数据;Owner maintenance window |
| Enqueue 自己失败的二次灰区 | MED | D-005 P0 alert + metric;接受 worst case 是 alert,不再嵌套 |
| Worker 重 settle 触发上游真实调用 | LOW | 不会:Settler.Settle 内部已校验 claim status='committed' 直接返回;不会重发上游 |
| 测试覆盖不到 quarantine 的人工裁决 SOP | LOW | C6 runbook 详细写 SOP |

## §4 时间估

- Plan parallel + synthesis: 1 hour
- Owner schema-gate + D 决策: ad-hoc
- C1-C6 实施 + 测试: 4-6 hours
- Per-commit codex review × 6: 1 hour
- 全量 verify + CI: 30 min

总 ≈ 半个工作日(Claude 主笔 + per-commit codex review)。

## §5 Lane attribution

- Claude lane planner: 本对话 session
- Codex lane planner: bazn7km4v(同时跑独立,本文件不看其输出)
- Prestudy: b06c0srmp(2026-05-24 ref scan,sub2api/new-api/litellm)
- UTC: 2026-05-24T04:10:00Z
