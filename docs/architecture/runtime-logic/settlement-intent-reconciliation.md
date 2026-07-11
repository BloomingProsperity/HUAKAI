# settlement_intents 持久结算意图 运行逻辑

> 与主账本 billing_ledger_claims **平行**的「意图→交付→结算」证据链,采 fail-open 旁路
> (默认 env-gate 关 `HUAKAI_SETTLEMENT_INTENT_ENABLED`)。本文记它与 billing/交付/quota/sweeper
> 各模块**怎么配合**,不重复功能清单。相关 [relay-forwarding.md](relay-forwarding.md)(主结算链)。

## 1. 请求/操作生命周期(数据流)

**阶段 1 — 正向意图生命周期(与主结算链同步、旁路记录)**:

1. dispatch 阶段 `ClaimGate.Reserve` 拿到权威 claim(含 `attempt_seq`)→ 紧接 `InsertPending`
   把意图行落 `pending`,**attempt_seq 取自 ReserveResult**(绑创建时的权威尝试,非自行推断)。
2. 首个业务帧完整写出并 flush → 转发器 `AfterFirstBusinessFrame` 回调 → `MarkDelivering`
   (写 first_byte_at)。这是**客户端交付证据**,与财务 committed 分离——cache 短写/断连不推进。
3. 主结算 `Settler.Settle` 成功/失败 → `MarkSettlementResult(actualCost, settleErr, recoveryEnqueued)`
   → `settling`/`settled` 或 `failed`。金额取主账本权威值。

**阶段 2 — 后台 sweeper 兜底(追平 fail-open 漏标)**:

4. sweeper Ticker(默认 60s)→ `ListStaleNonTerminal`(status∈{pending,delivering,settling} 且
   updated_at<now−staleAfter 且 created_at<now−createdGrace)→ 逐条 `GetClaimByID` 查权威 claim →
   按 claim 状态守卫式 CAS 追平。

## 2. 关键配合点表

| from→to | 传什么 | 配合关系 | 配合错的后果 | file:line |
|---|---|---|---|---|
| ClaimGate→意图 Insert | attempt_seq(权威) | 意图绑创建时的权威尝试 | 绑错 attempt→sweeper 拿新尝试金额冒充旧证据 | chat_completions_dispatch.go InsertPending 处 |
| 转发器→意图 | AfterFirstBusinessFrame 回调 | 首帧交付才 MarkDelivering | 无回调→delivering 永不出现,或短写误标交付 | forwarder.go:74 / chat_completions_stream.go |
| Settler→意图 | actualCost/settleErr | 结算结果同步意图终态 | 漏调→意图停 settling(靠 sweeper 兜) | chat_completions_billing.go MarkSettlementResult |
| sweeper→claim | tenant_id/claim_id | 只读权威状态,不推断金额 | 推断金额→与主账本漂移 | sweeper.go GetClaim / obs_queries.sql.go:106 |
| sweeper→意图 | version+status IN 守卫 | 守卫式 CAS 单胜者 | 缺守卫→已终态被反向改写/多副本重复终态 | sweeper.go applyCAS / settlement_intents.sql |

## 3. 失败协作

| 场景 | 涉及模块 | 怎么协作补偿 | file:line |
|---|---|---|---|
| 意图写失败/超时/panic | 意图 Tracker↔主结算 | **fail-open**:绝不阻塞主交付/主结算;三层 panic recover(定时轮用 slog.Default 防注入 logger 自身 panic 二次触发) | tracker.go / sweeper.go:188-214,261-267 |
| fail-open 漏标终态 | sweeper↔claim | sweeper 扫 stale 按权威 claim 追平:committed→settled(复制权威 actual_cost)/aborted→aborted | sweeper.go:284-300 |
| claim 已复活到更高 attempt | sweeper↔claim | attempt proof:旧意图只能 superseded,不拿新尝试终态/金额冒充 | sweeper.go:276-279 |
| claim 仍 reserving(在途) | sweeper | 跳过不动,勿误杀在途请求(LeaseSweeper 管 claim 层) | sweeper.go:296 |
| 多副本 sweeper × 正向 hook 并发 | sweeper↔意图 | 守卫式 CAS(status IN 悬挂态 + version),ErrNoRows 让位、天然幂等 | sweeper.go:303-316 |

## 4. 三镜对照

| 镜 | 同款做法 | HUAKAI delta |
|---|---|---|
| sub2api | 支付订单周期对账 = leader 选主 + 外部网关权威源 + `WHERE status=pending` 守卫式 UPDATE 单胜者 + 置过期前再查一次防误杀;outbox 10s 宽限期 | 等价采守卫式 CAS 单胜者 + 权威源=主账本 + createdGrace 10s;leader 选主留后续(靠 CAS 保正确性,对齐 LeaseSweeper 现状) |
| new-api | 超时清扫 sweeper(100/轮)+ per-task CAS(WHERE status=旧值)+ DB 租约 fencing;真金差额补偿写账本 | 等价 batch+CAS;**delta:只追平不推断金额**(new-api 会差额补扣/退,HUAKAI 金额取主账本权威值不自行动钱) |
| CLIProxyAPI | 纯 relay,无结算/账本/对账等价物 | 不适用 |

## 5. 已知配合缺口(非阻塞,Owner-gated 后续)

- **在途续期未做**:delivering/settling 期间不刷新 updated_at,长请求靠 staleAfter 10min 兜底;
  更强防线(处理协程周期 bump)增复杂度,留后续。判据:staleAfter 须长于部署允许的最长请求生命周期。
- **leader 选主未做**:多副本正确性靠守卫式 CAS(必需且已证),leader 选主仅减重复扫描(优化),留后续。
- **运维人工裁决 UI 未做**:sweeper 自动追平能对上主账本的意图;主账本本身异常(如 claim 缺失)的
  意图记 warning,人工裁决面板留后续强制切片。

## 6. 配合点测试清单(真 PG + -race,已实现并变异证)

| 测哪个配合 | 构造条件 | 判别断言 | 变异证 |
|---|---|---|---|
| committed 追平 | claim committed 同 attempt,意图卡 delivering | 意图→settled 且 actual_cost=claim 权威值 | 删 attempt 一致判断→误标 settled 红 |
| aborted 追平 | claim aborted,意图卡 pending | 意图→aborted | — |
| attempt 复活 | claim.attempt=2,意图.attempt=1 | 意图→superseded(非 settled) | 删 attempt 比较→红 |
| 在途保护 | claim reserving | 意图 status/version 不变 | — |
| 多副本单胜者 | 12 goroutine(sweeper×正向 hook×另副本)抢同一悬挂意图 | 恰一方成功、version+1、无重复终态 | 删任一 IfStale 的 status IN 守卫→对应终态被反向改写红(三守卫各自锁定) |
| 创建宽限 | created_at 在宽限窗内的新意图 | 不被 ListStale 扫到 | 去 grace→红 |

> 实测:真上游 E2E 账目零漂移(7=7=7=7);真 PG + -race 并发/故障注入全通过。
> 见 docs/process/reviews/2026-07-11-settlement-intent-sweeper-result.md、
> 2026-07-11-B-class-phase1-real-upstream-result.md。
