# 配额子系统实施计划 — Claude 独立稿 (2026-05-28)

> CLAUDE.md #10 平行计划法的 Claude 一侧。独立成文,未参考 codex 稿。分支 `work/quota-subsystem`(旧机临退役 2 天窗口,禁 push 到 `fix/hermes-phase-1-e33d940`)。

## 0. 对账结论(现状,带证据)

- **已有且真能用**:上游账号并发 cap —— `provider_accounts.cap_concurrency`(默认 4)+ `in_flight_count`,原子 `IncrementInFlightCount`(`WHERE in_flight_count < cap_concurrency`,rows=0 即满)+ `pool_slot_acquisitions` 幂等释放/孤儿清扫。仅 **per-upstream-account 并发**。
- **看着像配额其实不是**:`internal/billing/claim_gate.go` 的 `Reserve` 只做幂等指纹 + replay 检测 + 在 Serializable tx 插 `reserving` claim 行;`billing.go:22-24` 那句"reserves quota across 5 dimensions"是过时空头(无任何余额检查/计数扣减)。`SettleResult.APIKeyQuotaExhausted` 是**死 flag,无 producer**。
- **stub / 锁了没写**:`internal/rate/rate.go` 是 contract-only(接口 + enum,未接热路径);`0004_rate_limiting` 的上游冷却列(`rate_limited_at` / `overload_until` / `openai_403_counter` / 5h 窗口 / `model_rate_limits` jsonb)+ `rate_limit_audit_events` 表**已锁但无写入状态机**。
- **完全没有**:`api_keys` 表(`0007`)零配额列;入站 per-key/per-user **限速(RPM/TPM)**、**成本/预算强制**、**429→冷却强制引擎** 全缺。最高 migration **0059**。

## 1. 范围(2 天旧机窗口 —— 只闭合一块)

全配额子系统是多切片(限速 + 成本预算 + 并发 + 上游冷却)。2 天只闭合**最高价值 + 最贴现有架构**的一块:

**入站 per-key 成本预算强制(USD budget),复用现有 reserve/settle 账本(参考架构 approach 2)。**

理由:money-risk 最高;贴 HUAKAI 现有 Tx1/Tx2;**零新基础设施**(无需引入 Redis);**不动冻结包**(热路径已调 claim_gate)。

- **In**:`api_keys` 加预算列;Tx1 内原子扣减 + 超预算 fail-closed 拒绝;复活死 flag `APIKeyQuotaExhausted`;超预算 typed error 进审计;Settle 阶段按实际 cost reconcile。
- **Out(列 follow-up spec,不在本切片)**:RPM/TPM 滑窗(要 Redis 决策)、per-user/per-group 聚合、上游 429 冷却状态机、多窗口(5h/1d/7d)。

## 2. 包/文件布局(#13 职责分包,确认非冻结)

- **新包** `backend/internal/quota`(非冻结):`BudgetReserver` 接口 + Postgres 实现 + typed 错误。
- **编辑** `backend/internal/billing/claim_gate.go`(非冻结·既有文件):在 Tx1-Reserve 内调用 `quota.BudgetReserver`;Settle 内 reconcile。
- **新 migration** `0060_api_key_budget.up/down.sql`(见 §8 撞号风险)。
- 确认:`gatewayhttp / gateway / proto` 冻结,本计划**不在其内加任何新文件**(热路径已经调用 claim_gate,集成点天然在非冻结包内)。

## 3. 数据模型

`api_keys` 加列(或独立 `api_key_budget` 表 —— D3):`budget_usd_micros`(0=unlimited)、`used_usd_micros`。
核心扣减:`UPDATE api_keys SET used_usd_micros = used_usd_micros + :reserved WHERE id=:id AND (budget_usd_micros = 0 OR used_usd_micros + :reserved <= budget_usd_micros)` —— rows-affected=0 即超预算。

## 4. 算法(2-phase 复用 + 升级)

- **Tx1 (Reserve)**:用 `rate_table` 估算 cost → 在**现有 Serializable claim tx 内**原子扣 budget;超则整 tx 回滚(claim 不落)+ 返回 typed `QuotaExhausted`。
- **Tx2 (Settle)**:按实际 cost reconcile(`delta = actual - reserved`,补/退 `used_usd_micros`);Abort 全退。
- **升级点**:扣减与幂等 claim 在**同一个 Serializable tx**,消除 new-api/sub2api 那种 pre-consume 与 claim 之间的竞态窗口。

## 5. 集成点

`claim_gate.Reserve` 已被 `gatewayhttp` chat_completions 热路径调用 → 在其内插 budget 检查,**无需动冻结包**;`Settle` 同理走既有 Tx2。cost 估算复用既有 `rate_table`。

## 6. 成功标准

超预算请求被拒(typed error + 审计落账);预算内放行;Settle 后 `used` 反映真实 cost;并发耗尽边界时不超卖(Serializable 保证)。

## 7. 测试计划(mutation-discriminating #14,真 PG)

- **T1** key `budget=$1.00 used=$0.99`,估算 `$0.05` 请求 → **拒**。Mutation:删/反转 budget 检查 → 放行 → 红。(判别 fixture:余额刚好不够)
- **T2** 预算内 `$0.005` 请求 → 放行 + `used` 增。
- **T3** 并发 2 请求各踩边界 → 只 1 个过(Serializable)。Mutation:降隔离级别 → 两个都过超卖 → 红。
- **T4** Settle reconcile:reserved `$0.05` actual `$0.02` → `used` 回退 `$0.03`。
- **自证**:同测试内跑 within-budget(放行)+ over-budget(拒)断言结果不同。
- 真 PG(本地 `migrate up` 到 0060),不 mock —— money-risk 活在真依赖里(`feedback_risk_based_testing`)。

## 8. blast radius / what-could-go-wrong

- 改 `claim_gate.Reserve` = 热路径核心,改错波及**所有计费请求** → 严格测试 + 守 Serializable 不变量。
- **migration 0060 与新机撞号(重点)**:新机在 `fix/hermes-phase-1` 修 bug 可能也加 migration,合并时 0060 可能撞。缓解见 D4。
- **budget 扣减 DB 失败的 fail-policy**:沿用信任账本的 fail-closed(拒请求)而非 fail-open(放行记账)—— money-risk 默认 fail-closed(与 `project_trust_ledger_failclosed_policy` 一致)。
- **估算偏差**:reserve 阶段输入 token 估算偏保守可能误拒边界请求;Settle reconcile 兜底实际值。

## 9. Owner 决策点(带参考项目对照 #15)

- **D1 架构**:**A 复用 DB reserve/settle 账本(推荐)** / B 加 Redis 滑窗 / C 混合(DB 管 $,Redis 管 RPM/TPM)。
  - sub2api:DB 原子 `UpdateQuotaUsed`(`api_key_service.go:827`)+ typed USD/窗口错。
  - new-api:DB `quota_used` 预扣 + Redis 缓存(`service/pre_consume_quota.go:38,67`)。
  - litellm:Redis Lua server-time 滑窗(`parallel_request_limiter_v3.py:95`)。
  - portkey:无本地配额,只读上游 header 退避(`retryHandler.ts:108`)。
  - HUAKAI 现有 Tx1/Tx2 + 无 Redis → **A 最省且与钱账本 txn 一致**。
- **D2 维度**:**per-key USD(推荐 2 天)** / + per-user 聚合 / + 多窗口(5h/1d/7d)。参考:sub2api per-key USD + 3 窗口;new-api per-user。
- **D3 代码位置**:**新 `internal/quota` 包被 claim_gate 调(推荐,职责分离 #13)** / 直接改 billing in-place。
- **D4 migration 号**:**预留高号(如 0070)防撞新机(推荐)** / 合并时重排。需 Owner 拍(取决于新机这 2 天是否加 migration)。

## 10. fusion-upgrade delta(三维)

- **架构**:quota 独立包 + `BudgetReserver` 接口插入既有 Tx1,而非散落 counter(sub2api 在 api_key_service、new-api 在 middleware)—— HUAKAI 把入站预算预留与钱账本统一进一个 Serializable tx。
- **算法**:预留与幂等 claim 同 tx 原子,消除 new-api/sub2api 的 pre-consume↔claim 竞态窗口。
- **生态**:复活死 `APIKeyQuotaExhausted` flag 给真 producer;typed 超预算错走既有审计账本;per-key 预算列为后续 admin 可视化打底。

---
Source files read(经研究 agent,SPECIFIER lane):HUAKAI `internal/billing/{billing.go,claim_gate.go,rate_table_source.go,state.go}`、`internal/pool/binding/auth_credential_gate.go`、`internal/rate/rate.go`、`internal/db/billing/pool_accounts.sql.go`、`sql/migrations/{0001,0002,0004,0007,0023}` + 目录至 0059;参考 sub2api / new-api / litellm / portkey(见 §9 引用)。
