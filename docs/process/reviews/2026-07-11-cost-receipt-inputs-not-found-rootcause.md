# cost-receipt「receipt inputs not found」根因定位 — 2026-07-11

两次真上游 E2E(B 类阶段 1 + codex live)结算后稳定打 warning
`audit: derive receipt after settle: receipt inputs not found`。本次追到根因、定级、
排后续。

## 根因:E2E 跑在「无 audit signer」的 fail-open 模式,audit_ledger_entries 表恒空

`ReceiptFormatter.DeriveReceipt` 的 `receiptInputsSQL` 从
`FROM audit_ledger_entries ale` 起 JOIN billing_events / billing_ledger_claims /
usage_records。JOIN 起点表若无行,直接 `sql.ErrNoRows` → `ErrReceiptInputsNotFound`。

查上次 codex live E2E 库 huakai_e2e_live 坐实:

| 表 | 行数 |
|---|---|
| billing_ledger_claims (committed) | 7 |
| billing_events (claim_committed) | 7 |
| **audit_ledger_entries** | **0** |
| **user_cost_receipts** | **0** |
| receiptInputsSQL 的 ale JOIN 命中 | 0 |

账本侧(claims/events/usage)全部正确(§B 类实测 7=7=7=7),唯独 **ale 表空**。

原因在 `submitAuditLedgerEntry`(chat_completions_billing.go:551):当 `d.Signer == nil`
走 fail-open D-8 分支——审计条目**不写 ale 表,只 EnqueuePreparedEntryToDLQ**。E2E
未配 audit signer,于是每笔审计条目都进 DLQ、ale 恒空,receipt 派生 JOIN 从起点断,
receipt DLQ recovery 重试时 ale 仍空、同样永久失败,`user_cost_receipts` 恒 0。

## 定级:S3(E2E 环境保真度),非生产 / 非 money 缺陷

- **不影响钱**:receipt hook 失败只 `report`,不阻塞结算(receipt_worker.go:386-388);
  账目零漂移已由 7=7=7=7 证。cost receipt 是审计辅助派生数据,非账本。
- **生产不受影响**:audit signer 是 **production 硬启动门**——无
  `HUAKAI_AUDIT_PRIVATE_KEY_PATH` 直接拒启(config.go:494、wiring.go:1531
  audit fail-closed gate)。生产 `d.Signer != nil` → fail-open 分支不触发 → ale 正常
  写表 → receipt 链可用。
- **生产 receipt 链接线完整**:middleware.go:390-401 已接 formatter + store + recovery
  enqueuer + DLQ 消费者(kind=CostReceiptAppend)+ NewReceiptHookSettler 包 settler。

结论:warning 是 E2E 跑在无 signer fail-open 模式的**预期产物**,不是生产或 money 缺陷。

## 后续改进(follow-up,非上线 blocker)

1. **E2E 保真度补齐**:E2E 至今未真正覆盖 cost receipt 端到端链(因无 signer)。给
   codex live / B 类 E2E 注入 `HUAKAI_AUDIT_PRIVATE_KEY_PATH`(如同注入 settlement
   flag),让 ale 写表、receipt 真派生、断言 user_cost_receipts 落齐,补上这段盲区。
2. **dev/无 signer 短路(可选,轻)**:无 signer 环境每笔结算的 receipt 同步派生注定
   失败并入 DLQ、recovery 又注定失败 → DLQ 堆积永久失败记录。可在 signer 未配置时
   skip receipt 同步派生(反正 ale 不会有),避免 dev DLQ 死记录堆积与噪音 warning。
