# 2026-05-20 l2-cache-hit-usage-settlement-codex

> CLAUDE.md #10 平行交叉:本文件是 codex 的独立草案,由后台任务 `bbv2h5c5r`
> 产出。codex 在产出时**未看到** Claude 的 A/B/C 草案——只拿到了 3 个硬约束
> 与 v15 的两个未修发现,独立给出方向。Claude 的草案见同目录 `-claude.md`。

| 项 | 内容 |
|---|---|
| Lane | reviewer/specifier(只读 HUAKAI 内部代码 + SQL,未改任何文件) |
| 触发 | Owner "你和 codex 一起讨论"——L2 缓存命中路径连续 3 轮 review 出问题 |
| 给定输入 | 3 硬约束(C1 幂等先于缓存 / C2 缓存命中不占 slot / C3 usage_records.provider_account_id NOT NULL);v15 两个未修发现 P2-1 幂等重试 409、P2-2 receipt 产不出 |
| clean-room | 无风险——仅读 HUAKAI 内部代码 |

## codex 独立结论

把 L2 cache hit 收敛为统一的 **provider-less usage settlement**,不要让它继续是
"committed claim + billing_event 但没有 usage fact" 的特殊孤岛。cache hit 仍
先 Reserve、仍不 acquire pool slot,但 settlement 要写一条 usage_records 语义
的 provider-less 记录,供 receipt / admin usage / 审计一致消费。

### (a) 推荐终结模型

cache hit 首次命中在写 200 body 前完成一次专门结清:

1. `billing_ledger_claims`:`status=committed`、`actual_cost=0`、
   `provider_account_id=NULL`、`acquisition_token=NULL`。
2. `billing_events`:写 `claim_committed`、`actual_cost=0`、
   `audit_request_id=<本次 request_id>`,保留 idempotency fingerprint。
3. `usage_records`:写 provider-less cache-hit row,`actual_cost=0`,
   tokens/model 从缓存的 HCSF envelope 派生,`routing_reason.cache_hit=true`。
4. `user_cost_receipts`:继续从持久的 usage/billing/audit facts 派生,
   不从易失的 L2 cache 里补事实。
5. 不释放 slot、不改 provider in-flight(因为没有 acquire)。

### (b) NOT NULL 还是可空

选择:改为**受约束可空**,不是无条件放松。新增来源判别(如
`settlement_source='provider_upstream'|'response_cache_l2'`);source 为
provider 时 `provider_account_id` 与 `acquisition_token` 必须非空,source 为
L2 cache 时必须为空,用 CHECK 约束保住 provider 路径原不变量。

只改 `provider_account_id` 不够——cache hit 同样没有 pool slot token,
`acquisition_token` 也要可空。

### (c) P2-1 409 的性质

是全局 Phase E 幂等重放未实现的暴露,不是 cache 特有回归。建议现在修一个
**窄版**:`IdempotencyHit` 后(fingerprint 已校验)再查 L2;cache entry 还在
就直接 replay 200、不再 CommitCacheHit、不新增 billing/usage/receipt;entry
不在才回 `409 replay_without_cache`。完整的"无 cache 也能 replay"持久 replay
store 留 Phase E。

### (d) v15 未发现、依赖"committed claim 必有 usage_record"的下游(grep 实证)

- receipt 派生 `LEFT JOIN usage_records`,usageRecordID 空即 `ErrReceiptUnavailable`
  —— `receipt_formatter.go:454/475`、`receipt_storage_pgx.go:337`
- GET receipt 把该错转 202 `receipt_unavailable` —— `cost_receipt_handler.go:107`
- receipt 校验 / 错配退款依赖 DeriveReceipt —— `cost_receipt_handler.go:226`、
  `refund_worker.go:499`
- admin 用量列表 + 计数只从 usage_records 出发 —— `observability.sql:4`、
  `admin_observability_handler.go:194`
- 旧 obs ListUsage 同样 —— `obs_queries.sql:9`、`repository.go:133`
- DLQ usage replay payload 假设 provider/account 非空 —— `dlq/handlers.go:13`
- 烟测假设 committed claim 必有 usage_record / released slot —— `smoke_test.go:403`

## codex 风险提示

- schema 可空属高风险变更,必须 Owner 确认。
- 必须用 CHECK 约束保住 provider 路径"两者非空"不变量,不能裸放松。
- 下一步建议:先写方案文档 + 针对 cache-hit receipt/idempotency/admin usage
  的 acceptance tests,再实施。

Source: codex task bbv2h5c5r,model_reasoning_effort=xhigh,2026-05-20 UTC。
读过 HUAKAI 内部:chat_completions_handler*.go / dispatch / stream、
settler.go、claim_gate.go、receipt_*.go、billing/obs SQL。
