# S1-015-fu — 纯缓存流结算闸门 + lease-sweep 测试隔离

**作者**: Claude PM  **日期**: 2026-05-29  **分支**: fix/s1015-cache-stream-fu (off fix/hermes 410a6c9)
**来源**: docs/process/reviews/DEFERRED-S1-015-cache-tier-pricing.md:8（piece A，[S2]）+ :16（piece B，test-hygiene 债，非 review finding）

## Context（为什么做）

S1-015 缓存分档计价（commit 3bd7302）已让 draft 的 cache 字段被填充且可定价，但
`billing.AttemptFromGatewayDraft`（state.go:110-145）的流式 chargeable 闸门只看
`delivered / TokensInput / TokensOutput`，完全无视 cache 桶。结果：一个**成功结束
（StreamEndGraceful）但 usage 仅含 cache 创建/读取 token、零 fresh input/output** 的
流，被判 `StreamStateFailed` → `CostForAttempt` 把含 cache 的成本一并归零 → 既不计费、
也不写 usage_record。这是**漏计合法 cache 成本 + 丢审计行**的双重缺陷。

Owner 已决策（2026-05-29 AskUserQuestion）：**计 cache 成本 + 写 usage 行**。

### 参考项目对照（#15）
| 选项 | 参考对照 | 一句话 |
|---|---|---|
| 计 cache + 写行（采纳） | litellm@79b4578:litellm/litellm_core_utils/llm_cost_calc/utils.py:544-617 | cache-read/write 成本无视 output、无条件计入，无 completion-only 闸 |
| | helicone@094b210:packages/cost/models/calculate-cost.ts:210-229 + valhalla/jawn/src/utils/cacheTokenAdjustments.ts:1-98 | cache 成本仅看 cache 桶；漏计 cache 被其定性为**欠费 bug** 做发票回溯 |
| 写行但零input不扣费（未采纳） | new-api@20d3e73:service/text_quota.go:303-307,462-475 | fresh-prompt+completion 全零则跳过扣费，但**仍写 log 行** |
| 维持现状（未采纳） | 三参考无一在 completion-only 设闸、无一 suppress usage 行 | 现状比所有参考更激进 |

**delta / 维度**：HUAKAI 的 stream-state chargeable 闸门是自研架构（架构升级：3-ID + attempt
state machine），本修复把 cache 桶纳入「可计费信号」，与多数派参考行为对齐，消除过度激进的 drop。

## Scope

**piece (A) — 纯缓存流结算闸门（money-path，Owner 已确认）**
- `backend/internal/billing/state.go`（非冻结包 billing）：
  - 新增 helper `streamDraftHasCacheTokens(draft)`：任一 cache 桶 (`CacheCreationTokens` /
    `CacheCreation5mTokens` / `CacheCreation1hTokens` / `CacheReadTokens`) > 0 → true。
  - 改 `AttemptFromGatewayDraft` 流式 `StreamEndGraceful && TokensInput==0 && TokensOutput==0`
    分支（state.go:131-132）：有 cache token → `StreamStatePartial`（可计费）；否则维持 `Failed`。
- 边界保证：`delivered>0` 已在前置 case 处理；`AmbiguousUsage`（line 123）与非 graceful
  （line 129-130 empty/Unknown）已短路为 Failed，cache token 不会复活它们。仅 graceful 受影响。
- 自动生效：chargeable 后 `CostForAttempt`（state.go:172）+ settler.go:160-161 让 cache 成本落账。

**piece (B) — lease-sweep 测试隔离（test-only，零钱险）**
- `backend/internal/billing/balancehold_settle_integration_test.go`（`//go:build integration_pg`）：
  `TestSettler_LeaseSweepAbortsExpiredClaims` 在共享 huakai_dev 库上靠全局 sweep batch(limit 10)
  回收 seeded claim；其他无 cleanup 测试留下的孤儿 reserving claim 会挤占 batch → seeded claim
  未被扫到 → flaky red。durable fix：setup 阶段预清理 stale reserving claim（或把断言 scope 到本
  测试 seeded claim），消除 order-dependence。

## Success criteria
- piece (A)：判别单测（state_test.go）：graceful + 零 in/out + 非零 cache → `StreamStatePartial`
  且 `CostForAttempt(cacheCost, attempt)` 非零；mutation（移除 cache 分支 / 闸门忽略 cache 桶）→ RED。
- piece (B)：lease-sweep 测试在有孤儿 claim 的共享库上稳定通过（预清理后 seeded claim 必被回收）。
- `go build ./...` + `go test ./internal/billing/`（非集成）全绿；集成测试（integration_pg）本地跑过。
- #8 codex review：无未结 S0/S1。

## Blast radius
- piece (A)：仅改 billing 包一函数的一个 case + 一 helper。影响面 = graceful 流且 input==output==0
  且 cache token>0（罕见但合法的纯缓存命中）。**方向是「开始计合法 cache 成本」（之前漏计），
  非「多收」**；不改 schema、不改 money 数学、不改 Tx1/Tx2 不变量、无迁移。
- piece (B)：纯测试文件，零生产影响。

## 什么会出错
- 误把 failed/ambiguous 流当 chargeable → 已用「仅 graceful 分支 + 前置短路」防住，并加 mutation 证明。
- cache 成本字段为零但 token 非零（定价缺失）→ CostForAttempt 返回的是 settler 算好的 actualCost，
  定价缺失是 S1-015 已解决的上游问题，不在本切片范围。
- piece (B) 预清理误删别的测试数据 → 仅清理 expired reserving（lease 已过期）孤儿，不动 active。

## Decision points for Owner
- piece (A) 计费方向：**已确认（计 cache + 写行）**。无其他未决点。

## 执行结果（2026-05-29）
- **piece (A) 落地**：state.go + state_test.go 实现完成；判别单测 self-proving（cache-only=Partial /
  no-cache=Failed 两路相异），mutation（cache 分支退回无条件 Failed）→ RED 已证；
  `go build ./...` + billing/gateway/gatewayhttp/settlementrecovery 非集成测试全绿。
- **piece (B) 延后**：循环 sweep 隔离修复已实现且 `go vet -tags=integration_pg` 通过，但本地 dev DB
  落后一个迁移（0060 user_balance_holds 未应用，`user_balances` 表缺失），集成测试无法运行；
  应用迁移命中 standing migration gate（共享库需显式授权）。按 #14「测试须经变异证明」不落未验证的
  集成测试改动 → 本切片仅落 piece (A)，piece (B) 回退，待 Owner 决定是否放行 dev DB 迁移后再验证落地。
  piece (B) 是 deferred 文档明示的 test-hygiene 债（非 review finding），延后不影响 piece (A) 正确性。

## 提交
- 单 commit（commit-naming-v2）：`billing 纯缓存流结算闸门补全（cache token 视为可计费信号）`
- #8 review → 无 S0/S1 → 落 fix/hermes（ff）→ push（非 force、非 main）。无迁移、不动 prod。
