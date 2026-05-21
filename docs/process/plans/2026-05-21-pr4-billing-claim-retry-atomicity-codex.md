# 2026-05-21 PR4 billing/claim retry atomicity

| Owner directive | "现在执行 PR4 —— billing/claim retry 原子性"；"PR4 写完后由 Claude 把真实 diff 发 Owner 过目、Owner 确认后才提交 —— 所以你绝对不能做任何 git 操作。" |
| Scope | 仅执行综合稿 §4 的 PR4 清单：`ReReserveAbortedClaim` SQL/sqlc、`ClaimGate.Reserve` re-reserve 参数、gateway handler 失败 attempt 的 Abort→ReReserve 管道、billing integration 覆盖。 |
| Out of scope | 不改 `ComputeIdempotencyFingerprint`；不打开 PR5 retry budget；不改 schema/migration；不加 runtime dependency；不做任何 git 操作。 |
| Success criteria | re-reserve 同一 claim 时 attempt_seq 递增，pooling_group_id 更新为当前 attempt pool，provider_account_id/acquisition_token 清空；失败 attempt 只走 `Settler.Abort` 写 zero-cost evidence；最终成功只产生一条正费用 committed usage record；跨 pool replay 不产生 fingerprint conflict，幂等 replay 命中同 claim id。 |
| Time estimate | 1.5-3 小时 agent time；全量 Go test 可能额外耗时。 |
| Blast radius | billing ledger claim Tx1/Tx2、pool slot release、chat handler pre-delivery failure path、sqlc generated billing DB package。 |
| Failure modes | stale acquisition token 未清导致二次 abort 释放已释放 slot；pooling_group_id 停在第一次 attempt；handler 在 retryable failure 后直接写 client 导致无法 re-reserve；正向 settle 被多次调用；integration 环境缺 PG 时 integration tests 会按现有 build tag/env skip。 |
| Mitigation | 先写失败测试再改生产代码；只通过 `Settler.Abort` 释放；re-reserve 后主动 reset execution acquisition state；最终跑指定 build/full test/billing race。 |
| Decision points | 若实现需要 schema/migration、billing ledger 新协议、runtime dependency、auth/quota/payment 核心外扩，停止等待 Owner；当前 Owner 已确认 D1 Abort→ReReserve 机制。 |
| Pre-execution checklist | 已读 `docs/RULES.md` Owner Start Gate；已读综合稿 §4；已读 Codex 稿 §9；确认不运行 git；确认 PR3 attempt_seq 已在 handler settle 路径使用当前 attempt seq。 |

## Concrete execution order

- [ ] Add billing integration regression proving `Abort -> Reserve` reopens the same claim, increments attempt_seq, updates pooling_group_id, and clears stale provider acquisition fields.
- [ ] Run targeted billing test to watch the new regression fail before SQL/claim code changes.
- [ ] Update `backend/sql/queries/billing_claims.sql` `ReReserveAbortedClaim` to set `pooling_group_id`, clear `provider_account_id`, and clear `acquisition_token`.
- [ ] Regenerate sqlc output for `backend/internal/db/billing/billing_claims.sql.go`.
- [ ] Update `backend/internal/billing/claim_gate.go` to pass current `PoolingGroupID` into `ReReserveAbortedClaimParams`, without changing `ComputeIdempotencyFingerprint`.
- [ ] Add/adjust billing integration regression proving final positive settle occurs once after an aborted failed attempt and re-reserve.
- [ ] Add handler-level retry plumbing so pre-delivery retryable failures are abort-only on non-final attempts and leave the next attempt able to re-reserve the same claim. Keep PR4 budget at 1.
- [ ] Run targeted package tests, then required build/full tests and billing race test.

## Assumptions

- The repository already contains PR1-PR3 attempt loop scaffolding and keeps `pr3EffectiveAttemptBudget = 1`; PR4 prepares the retry billing path but does not enable multi-attempt runtime behavior.
- Existing integration tests use `integration_pg` and skip without `HUAKAI_DATABASE_URL`; required full `go test ./...` will not run those tagged tests unless the tag is supplied.
