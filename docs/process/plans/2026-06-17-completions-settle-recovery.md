# Plan：/v1/completions 交付后结算恢复（S1-2 + S1-3 修复）

- **日期**：2026-06-17
- **来源**：`docs/process/reviews/2026-06-17-account-to-api-deep-audit.md` 确认 S1-2 + S1-3
- **风险级别**：HIGH（billing ledger / settlement 路径）—— Owner 已授权（"全部一起修"）
- **类型**：把 HUAKAI **自有**的交付后结算恢复机制（已存在于 gateway chat 路径）扩展到 `/v1/completions` 流式路径

## 缺陷（深审确认）

`completionshttp` 流式 relay 在**可取消的请求 ctx** 上交付后结算（[attempt.go:171](../../backend/internal/completionshttp/attempt.go)）：
- **S1-2**：内容已全部交付客户端，客户端在流末断连 → `ex.ctx` 取消 → settle 的 `BeginTx` 立即失败 → 已交付 token 永不计费（计费泄漏）。
- **S1-3**：completionshttp 缺 chat 路径的两层保护——`context.WithoutCancel`+30s 脱钩 与 `settlementrecovery` DLQ 持久重结算（chat 路径 [chat_completions_stream.go:279/300](../../backend/internal/gatewayhttp/chat_completions_stream.go)）。任何瞬态结算失败=不可恢复钱丢失，唯一兜底 LeaseSweeper 只 Abort 不重结算。

## 参考研究（#16）

- 本切片**不是新功能**：交付后结算恢复机制 HUAKAI 已有，原始设计见 `docs/process/plans/2026-05-24-post-delivery-settle-recovery-synthesis.md`（含三镜像研究 + 三证 proof 防重复扣费）。本切片仅把它从 chat handler 扩展到 completions handler，镜像源是**本仓内部 chat 路径**（#12 HUAKAI-internal 豁免外部镜像复研）。
- **CLIProxyAPI**：纯 relay account→API 代理，**无 billing/settlement 模块**（no-equivalent，#16 已记录在案）——故无交付后结算概念。
- **sub2api / new-api**：账号枢纽有计费，但其架构无 HUAKAI 的 Tx1/Tx2 claim/settle 分离与 post-delivery DLQ 三证 proof 设计；HUAKAI 此机制是自有架构升级（生态：DLQ + 三证幂等防重扣），非镜像照搬。

## 方案（镜像本仓 chat 路径，最小 delta）

仅修真泄漏点（流式交付后 settle）。**不动** JSON 非流式 settle（[attempt.go:117](../../backend/internal/completionshttp/attempt.go)）——它 settle-before-deliver，若也 detach ctx 反而会"charge 未交付"，审计已确认非泄漏。

1. **settlementrecovery/payload.go**：加导出构造器 `FromSettleRequest(src, requestID, req billing.SettleRequest) Payload`（`settleRequestPersisted` 未导出，包外无法构造，必须在包内加）。`FromCompletionEvent` 重构为委托它（DRY，行为不变，现有 payload_test 守）。
2. **completionshttp/handler.go**：`Deps` 加 `SettleRecoveryDLQ settlementrecovery.Enqueuer`（可选；nil 时退回原行为，不破坏现有 wiring_test）。
3. **completionshttp/billing.go**：加 helper `settleStreamWithRecovery(ctx, req, requestID) error`——调 `Settler.Settle`，失败时经 `FromSettleRequest` + `EnqueuePayload(SourceStream)` 落 DLQ，enqueue 自身失败时 P0 log（镜像 chat 路径 `privacy.LogSystem`）。
4. **completionshttp/attempt.go:171**：用 `context.WithTimeout(context.WithoutCancel(ex.ctx), 30s)` 脱钩 + 改调 `settleStreamWithRecovery`。
5. **cmd/gateway/routes.go**：`completionsHandlerDeps` 加 `SettleRecoveryDLQ: d.dlqService`（与 chat 路径 [routes.go:662](../../backend/cmd/gateway/routes.go) 同一注入）。

## Blast radius

- 改 5 文件。最敏感是 settlementrecovery（共享 billing 包）——但只**加**导出构造器 + 行为不变重构（payload_test 守）。
- completionshttp settle 行为变化**仅限流式交付后失败分支**：从"丢账"变"脱钩重试 + DLQ 持久"。成功路径、JSON 路径、abort 路径、count_tokens 路径全不变。
- routes.go 注入 nil-safe（DLQ==nil 退回原行为），不破坏现有测试。

## 可能出错点

- **重复扣费**：DLQ worker 重结算时靠三证 proof（claim committed + usage_records + billing_events）幂等——这是既有机制，本切片不改 worker，只复用 enqueue。payload Validate 要求 ClaimID+TenantID 非空（completionshttp settleRequest 已填，[billing.go:88/91](../../backend/internal/completionshttp/billing.go)）。
- **detach ctx 误用到 JSON 路径** → charge-未交付。规避：只改 line 171 流式分支，不碰 117。
- **enqueue 失败静默** → 镜像 chat：P0 log alert，不阻塞（响应已发不能反悔）。
- **FromCompletionEvent 重构回归** → 现有 payload_test 守；我会跑该包测试确认绿。

## 成功标准

- 流式交付后 settle 失败 → DLQ enqueue 发生（判别性测试：注入 settleErr + mock Enqueuer，断言 Enqueue 被调；变异删 enqueue → RED）。
- detach：settle 用脱钩 ctx（断连不取消结算）。
- 现有 completionshttp + settlementrecovery + wiring 测试全绿；go build relay-core 通过；codebudget 绿。
- 对抗审查零 S0/S1。

## 决策点（Owner）

- 这是 billing ledger 改动（HIGH）。Owner 已授权"全部一起修"，后续追加「全权授权给你」→ 实施 + 强测试 + 对抗审查（零 S0/S1）后**自主合并**。

## 审查后增补（对抗审查 wa52al89r：零 S0/S1，4 条 S2 全部提交前修复）

1. **#1/#3 非判别 detach 测试**（我引入）：原测试仅断言 `Deadline()` 存在，单删 WithoutCancel 仍绿（#14 违规）。**已修**：改用已取消的父 ctx 注入 + 断言 settle 调用时刻 `ctx.Err()==nil`（只有 WithoutCancel 脱钩才成立）。变异删 WithoutCancel → RED 实测。
2. **#2 DLQ enqueue 复用过期 settleCtx**（money-path 真改进，chat 路径同构）：settle 因 deadline 超时失败时复用同一过期 ctx → enqueue 立即失败 → recovery intent 落不了盘（DB 受压时兜底失效）。**已修两路**：completionshttp + gatewayhttp chat 路径均改为 `context.WithTimeout(context.WithoutCancel(ctx), N)` 派生独立 enqueue ctx。各配判别性测试（已取消 ctx → 断言 enqueue 收到未取消 ctx；变异复用过期 ctx → RED）。**scope 增补**：reviewer 明示"两路同改"，chat 是高流量路径，连带修复。
3. **#4 enqueue-失败 P0 分支零覆盖**（我引入死脚手架）：**已修**，加测试 set retErr，断言（a）不 panic/不改 HTTP（b）enqueue 仍尝试一次（c）DLQ failure_reason 只记错误类别不泄漏 raw 文本。变异 failureClass 用 err.Error() → RED。

合并文件增至 8（+gatewayhttp/chat_completions_billing.go + post_delivery_recovery_test.go）。全包测试 + codebudget + cmd/gateway wiring + vet 全绿。
