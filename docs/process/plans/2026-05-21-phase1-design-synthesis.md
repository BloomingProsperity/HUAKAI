# Phase 1 详细实施设计 — 综合稿(Claude × codex × Owner 决策)

- 日期: 2026-05-21
- 范围: 方向 1 Phase 1 — 洞 ② 单请求内重试 + 账号 failover、洞 ⑥ 跨池多候选编排
- 输入: `2026-05-21-phase1-design-claude.md`(Claude 独立稿)+ `2026-05-21-phase1-design-codex.md`(codex 独立稿),按 CLAUDE.md #10 平行起草
- 状态: Owner 已就 2 个分歧拍板,本稿是 PR1-5 实施的权威输入

## 1. Owner 决策(2026-05-21 已拍板)

| # | 分歧 | Owner 决策 | 含义 |
|---|---|---|---|
| D1 | 失败 attempt 的记账机制 | **失败留作废记录** | 采用 codex 稿的 Abort→ReReserve 路径:失败 attempt 走 `Settler.Abort` 写零费用作废记录,下个 attempt 重新 `Reserve` 命中同一 claim。完整重试证据链,符合账务透明定位。 |
| D2 | 401(令牌失效)处理 | **换一次号** | 401 触发 failover,但有独立子限制:整个请求最多因 401 换 1 次号;且 401 不写 channelhealth degraded 惩罚信号。 |

## 2. 两稿裁决表

| 议题 | 裁决 | 依据 |
|---|---|---|
| 洞⑥ router 替换 | 两稿一致,采用 | 把 `DefaultRouter` 换真 planner,枚举全部 pool candidates |
| `RoutePlan`/`AttemptPlan` 数据模型 | 现成,不改 | 两稿一致 |
| 是否改表结构 | 否 | 两稿一致;`attempt_seq` 列已存在 |
| billing 跨 attempt 机制 | codex 稿(Abort→ReReserve) | Owner D1;复用已测试原子操作 |
| `ReReserveAbortedClaim` 不更新 `pooling_group_id` | 必修(codex 发现) | Claude 稿漏掉的真 bug |
| `Settle` 用 claim 行的 `attempt_seq` | 采纳 codex 观察 | 序号真相源在 DB,handler 要对齐 |
| 401/403 taxonomy | 综合(D2 覆盖 codex §8.2) | 见 §3 override-1 |
| `AttemptBudget` 默认值 | 3(env 可配),单 pool 时 2 | codex 论证更充分 |
| 请求体上限 | 保持 1 MiB | 两稿一致,不放大 |
| 5-PR 拆分(PR3 先关 retry) | 采用 codex 稿 | 风险摊薄优于 Claude 3-PR |
| 框架判断「Phase 1 是 L0→real 过渡」 | 保留 Claude 稿 §0 | 好的框架性总结 |

## 3. 最终设计指针 + 综合稿 override

主骨架以 `2026-05-21-phase1-design-codex.md` 为准(勘察 50 个 Go 文件、设计细节最全)。下列各点是综合稿对它的 **override / 补充**:

**override-1 — 401/403 taxonomy(覆盖 codex §8.2 的 401 行)**

- 401 → 可重试=是、换账号=是,但受独立子限制 `authFailoverBudget = 1`(整个请求最多因 401 换 1 次号)。
- 401 → **不写** channelhealth degraded 信号(令牌问题不是账号「不健康」)。
- 401 仍带 `RefreshIntent = RefreshOAuthHotPath`,为 Phase 2 洞④ 留接口。
- 403 → 维持 codex 稿:不 failover(平台策略/账号封禁,换号无用)。
- 实现:handler retry loop 维护 `authFailoverUsed bool`;401 失败时若已 used → terminal。
- 覆盖范围:本 override 覆盖 codex 稿中**所有** 401 条目 —— §8.2 taxonomy 表 401 行、§12.1 测试「non-stream 401 → no second attempt」、§13 风险表 401/403 行、§15 确认点 4。凡 codex 稿写「401 不可重试 / 无 second attempt」处一律以本 override 为准:401 = 换一次号(codex review P2)。

**补充-2 — 保留 Claude 稿 §0 框架判断**

Phase 1 不是从零设计,是完成代码早已预留的 L0→real 过渡(三层架构、多 attempt 数据模型、attempt-aware 账务层均已就位)。

**补充-3 — attempt 级 upstream model override(codex review P2)**

不同 pool 的 binding 可有不同 `provider_model_id_override`(见 `model_pool_bindings.provider_model_id_override` 列)。当前 registry 只把首个 binding 的 override 应用到单个 `resolved.ProviderModelID`,dispatch 对所有 attempt 用同一个 upstream model id。跨池 failover 必须让每个 attempt 用**自己那个 pool 的** override —— PR1 的 `AttemptPlan`(或 `PoolCandidateMeta`)必须携带 per-attempt upstream model id / override,dispatch 按 attempt 取用。PR1 实现多候选路由时一并处理。

其余设计(洞⑥ 候选生成、`runAttempt` 抽取、handler loop、delivery tracker、错误 taxonomy 其余行、流式硬规则、测试策略)全部按 codex 稿。

## 4. billing 改动清单(Owner 已确认 — D1 路径)

无表结构迁移。改动点:

1. `backend/sql/queries/` 的 `ReReserveAbortedClaim` — 改 SQL:(a) 增加更新 `pooling_group_id`;(b) 清空 `provider_account_id` / `acquisition_token`(置 NULL),让重开的 claim 回到「reserving 但未 acquire」的干净状态。否则下个 attempt 若在写新 acquisition 前再次失败,最终 abort 会拿 stale token 去释放一个已释放的 slot,导致 abort 失败 / claim 卡死在 reserving(codex review P1)。sqlc 重新生成。
2. `backend/internal/billing/claim_gate.go` — `Reserve` 重开分支传入当前 attempt 的 `PoolingGroupID`;`ComputeIdempotencyFingerprint` 保持不变(不加 `PoolingGroupID`)。
3. `backend/internal/gatewayhttp/` handler — 失败 attempt 调 `Settler.Abort` 释放 slot + 写零费用作废记录;下一 attempt 重新 `Reserve`。
4. `attempt_seq` 去硬编码 4 处(`chat_completions_handler_headers.go`、`chat_completions_dispatch.go`、`chat_completions_stream.go`、`chat_completions_billing.go`)→ 循环序号。
5. non-stream / stream settle — 用当前 attempt 的序号/账号/token;正向 settle 整个请求只一次。
6. 测试:attempt 1 失败释放 slot;attempt 2 re-reserve 同一 claim id;re-reserve 后 claim 的 `provider_account_id`/`acquisition_token` 已清空;attempt 2 在写新 acquisition 前再次失败 → 最终 abort 不重复释放 slot;最终只一条正费用成交;跨池后 `pooling_group_id` 正确;幂等 replay 仍命中。

**PR4 把关**:codex 写完 + codex review 通过后,真实 diff 再发 Owner 过目才提交。

## 5. 5-PR 拆分

| PR | 内容 | 风险 | 触 billing |
|---|---|---|---|
| PR1 | 洞⑥ router 多候选 planner | 低-中 | 否 |
| PR2 | 错误 taxonomy + attempt outcome 类型 | 中 | 否 |
| PR3 | handler attempt loop 骨架(budget=1,retry 关闭,行为不变) | 中 | 否(只抽函数) |
| PR4 | billing/claim retry 原子性 | **高 — Owner 确认门** | 是 |
| PR5 | 打开 retry/failover + 集成回归 | 中-高 | 否 |

估时合计约 7.5-11 天。PR1-3 可立即开工(不碰 billing)。PR4 实现前已 Owner 确认机制(D1);提交前再发 diff。

## 6. PR1 立即开工范围

文件:`backend/internal/router/{route_plan.go,default_router.go,router_test.go}`、`backend/internal/gatewayhttp/chat_completions_dispatch.go`(只映射 registry binding metadata)、`backend/cmd/gateway/wiring.go`(若 `routePlanner` 类型变化)。

内容:见 codex 稿 §4(候选生成)+ §11 PR1,并落实本稿 §3 补充-3(attempt 级 model override)。默认 budget 3、单 pool 时 2,Router 接口不变。单测覆盖:有序候选→多 attempt、metadata 缺失 fallback、单池 same-account-failover、snapshot 不变、不同 pool 的 model override 各自正确。

---
本稿 lane: synthesizer — Claude 综合两份 CLAUDE.md #10 平行稿 + Owner 2 决策。未读外部参考源码。agent: Claude (claude-opus-4-7)。UTC 2026-05-21。
