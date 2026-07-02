# Relay 转发链 运行逻辑 / 模块间配合

> 本文档实现 CLAUDE.md §17:记录终端 `hk_key` 请求 `POST /v1/chat/completions` 流过 relay 链时,
> **模块交界处**的数据/状态传递与失败协作;对照 sub2api / new-api,标出配合缝,给出配合点测试用例。
> 调研方法:3 路只读读码(sub2 @0b8e5eec / new-api @52858ad1 / HUAKAI)+ 综合,所有载荷锚点对当前分支真码核验(2026-07-02)。
> **实测背书**:2026-07-02 relay 细粒度 E2E(真 Grok 上游)已在部分配合点抓到真实缺口,见文末 §7。
>
> 核验过的关键锚点:settler acquisition_token 结算归属锚、claim_gate 的 aborted 分支复活、balancehold 预扣/实扣/释放、
> lease_sweep 30 分钟窗口回收、channelhealth 冷却+渐进放量准入、上游 401 返空健康信号、流式 deliveryTracker + 独立 ctx DLQ、
> attempt 循环的 failedAccounts/authFailoverUsed/attemptCap。测试环境:`//go:build integration_pg` + `scripts/integration-pg.sh`(每包克隆纯净库)。

---

# HUAKAI Relay 链模块**配合**测试架构

> 范围:一个终端 `hk_key` 请求 `POST /v1/chat/completions` 流过 relay 链时,**模块交界处**的数据/状态传递与失败协作。所有断言针对"两个及以上模块配合错"才暴露的缺陷(漏钱/冻钱/重复扣/换号失败/健康不更新/串号),单模块单测测不到。
> 代码锚点全部对当前分支 `feat/fe-wire-users-mod` 真码核验(路径相对 `/home/ubuntu/HUAKAI/backend`)。

---

## 1. Relay 链模块协作图(按请求生命周期)

图例:`──▶` 同步数据传递 · `⇢` 异步/回流 · `【】` 交界处传递的载荷 · `★` 该载荷是跨模块唯一锚点。

```
 客户端 hk_key
    │  Authorization: Bearer hk_...
    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. auth.APIKeyResolver.Resolve            api_key_resolver.go:126             │
│    prefix→LookupAPIKeysByPrefix(≤5)→bcrypt→status/expiry/tenant/IP           │
└─────────────────────────────────────────────────────────────────────────────┘
    │  【auth.Identity{TenantID,APIKeyID,UserID,UserGroup,AllowedModels}】★ 身份唯一真相源(§4:绝不读 body)
    ▼
┌───────────────────────────┐   ┌───────────────────────────┐   ┌───────────────────────────┐
│ 2. 模型 allowlist          │   │ 3. prepareRoute            │   │  sessionHash(prompt 亲和键) │
│ apikeymodelallow.AllowsCSV │──▶│ registry→Router.Plan       │──▶│  dispatch.go:287-327        │
│ dispatch.go 337            │   │ RequestContext{Tenant,User,│   │  plan.Attempts[]            │
│  ident.AllowedModels 卡权限 │   │  APIKeyID,RequestID}(非body)│   │  (PoolGroupID/UpstreamModel)│
└───────────────────────────┘   └───────────────────────────┘   └───────────────────────────┘
    │  attempt 循环开始(runSingleModel handler.go:548,维护 failedAccounts / authFailoverUsed / attemptCap)
    ▼
╔═══════════════════════════════════════════════════════════════════════════════════════╗
║ 4. 计费预扣 Tx1  ClaimGate.Reserve            claim_gate.go:84  (Serializable Tx)        ║
║    仅当 ex.reserveRes==nil 才跑一次(dispatch.go:426)                                     ║
║    幂等三元: payloadHash=SHA256(body) · logicalRequestID(Idempotency-Key|uuid)          ║
║      ├ committed→重放 / reserving→ErrClaimRace / aborted→ReReserveAbortedClaim(seq+1)   ║
║      └ INSERT reserving claim + balancehold.Reserve(hold=PredictedCost)                 ║
║         mandatory 且余额<cost → ErrBalanceHoldInsufficientBalance → 402(整 Tx rollback)  ║
╚═══════════════════════════════════════════════════════════════════════════════════════╝
    │  【reserveRes.ClaimID】★ hold 锚点 + claim 归属锚点
    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│ 5. 配额预扣 reserveQuota   dispatch.go:494                                    │
│    QuotaReserver.Reserve(ClaimID+ident+PredictedCost+ReservedTokens)         │
│    拒 → Settler.Abort(claimID,"quota_denied")→回滚 hold→429                    │
│    基础设施错 → fail-open 放行(dispatch.go:525)                               │
│  (非流式先查 L2: serveL2CacheIfAvailable→命中 CommitCacheHit 零成本终结 claim)  │
└─────────────────────────────────────────────────────────────────────────────┘
    │  ClaimID
    ▼
┌───────────────────────────────────────────────────────────────────────────────────────┐
│ 6. 选号 selectPoolAccount  dispatch.go:555   DefaultSelector.Select default_selector:77 │
│    ListAccounts→policy→gate 链(PoolGate.Allow 健康门)→sticky/route/fresh 三层            │
│    →slots.Acquire 抢 pool slot→claims.WriteAcquisition(tenant,claimID,accountID,token)   │
│                                            (Pattern B 回写到 reserving claim 行)          │
└───────────────────────────────────────────────────────────────────────────────────────┘
    │  【selRes{AccountID, AcquisitionToken}】★★ acquisition_token = "选号→结算" 唯一锚点
    │  → SessionCapRegistry.Register(accountID, sessionHash) dispatch.go:628 (下次选号并发 cap 视图)
    ▼
┌───────────────────────────┐        ┌───────────────────────────────────────┐
│ resolveCredential          │        │ 7. Dispatcher.Dispatch / DispatchHCSF  │
│ CredentialVault.Resolve    │───────▶│  executeNonStreamingAttempt billing:79 │
│ (tenantID, accountID)      │        │  executeStreamingAttempt     stream:153│
│ + channelHealthKey         │        └───────────────────────────────────────┘
└───────────────────────────┘                       │
                                                     ▼
        ┌───────────────────── 上游响应分叉 ─────────────────────┐
        │                                                        │
   非 2xx (classify)                                        2xx 成功
        │                                                        │
        ▼                                                        ▼
┌───────────────────────────────────────────┐   ╔═══════════════════════════════════════════════╗
│ ClassifyAttemptHTTPError attempt_error:191 │   ║ 8. 结算 Tx2  Settler.Settle  settler.go:77      ║
│  decision{SwitchAccount/SwitchPool/        │   ║  GetClaimForSettle(tenant+claim+acq_token 锁行) ║
│   CountsAgainstAuthFailoverBudget/AbortRsn}│   ║  →InsertBillingEvent(claim_committed)           ║
│  classification→signalFromClassification   │   ║  →InsertUsageRecord                             ║
│    ├ 401 TokenRevoked/OAuthInvalidGrant     │   ║  →ReleaseSlotAndDecrementInFlight(acq_token)    ║
│    │   → 返回 "" 空健康信号(error.go:181)    │   ║  →UpdateClaimCommitted                          ║
│    │   → triggerCredentialHotRefresh(异步)   │   ║  →Capture(claim, actualCost)  多退少不补按 actual║
│    └ 5xx/429/timeout → SignalUpstream5xx/   │   ╚═══════════════════════════════════════════════╝
│        RateLimit/Timeout                    │            │ 流式: 边收边写客户端,
│  429/529→ForceCooldown(dispatch.go:841)     │            │ 结束后 forwardSSEAndSettle 独立 30s ctx
└───────────────────────────────────────────┘            │ (WithoutCancel) → settleCompletionWithRecovery
        │  Settler.Abort(claimID,reason)                   │ 失败→settlementrecovery DLQ 持久化→worker 重放
        │  → balancehold.Release(整额退 hold)               ▼
        │  → ReleaseSlotAndDecrementInFlight              return 200
        ▼
   ⇢ channelhealth.ApplySignal(service.go:66) → evaluate → UpsertRecord
   ⇢ failedAccounts[accountID]=struct{}         (下轮 ExcludedAccounts)
   ⇢ prepareNextAttemptAfterAbort: ex.reserveRes=nil (attempt.go:342)
        │        └──强制下轮重跑 Reserve → 命中 aborted 分支 → ReReserveAbortedClaim 复活同 claim + seq+1 + 重建 hold
        ▼
   下一轮 attempt (回到 6. 选号,健康门读回 ApplySignal 落库的状态排除坏账号)

──────────────────────── 后台回收/补偿(异步,与请求生命周期解耦)────────────────────────
 billing.LeaseSweeper(每 30s)  lease_sweep.go:78
   SelectExpiredReservingClaims(lease_expires_at<now,30min 窗口)→逐条 Settler.Abort(lease_expired)
   →释放 hold+slot+回退 in_flight;SweepOrphanedSlotAcquisitions 扫孤儿 slot
 settlementrecovery.Handler(worker) handler.go:63
   走 public Settler.Settle 单入口重放(Tx2 幂等)→ErrClaimNotReserving 用 CommittedProof 三证判已 committed
   →多次失败转 quarantine 待 operator
```

**三条贯穿全链的锚点(配合的"焊点",测试必须盯住):**

| 锚点 | 产出方 | 消费方 | 配合断掉的后果 |
|---|---|---|---|
| `auth.Identity` | APIKeyResolver(1) | Reserve / Quota / Router.Plan / Selector(4/5/3/6) | 从 body 取 tenant/user → 越权计费、跨租户串号、配额穿透 |
| `ClaimID`(=hold_id) | ClaimGate.Reserve(4) | Selector.WriteAcquisition(6) / Settler.Capture·Release(8) | claim 悬空、hold 永不 capture/release → 冻钱或漏扣 |
| `acquisition_token` | Selector.Select(6) | Settler.GetClaimForSettle / ReleaseSlot(8) | token 对不上 → `ErrAcquisitionTokenMismatch`,用量记错账号 / slot 泄漏 |

---

## 2. 关键配合点对照表(HUAKAI vs sub2api / new-api)

评级:**等价** = 目标一致做法相当 · **更强** = HUAKAI 结构更严 · **有隐患** = HUAKAI 侧存在配合缝或需测试兜住。

| # | 配合点 | HUAKAI 做法(真码锚点) | sub2api 做法 | new-api 做法 | 评级 & 说明 |
|---|---|---|---|---|---|
| C1 | **预扣 hold ↔ 结算 capture** | Tx1 建 `balancehold.Reserve(=PredictedCost)`;Tx2 `Capture(claim,actualCost)` 落实扣、差额回补;失败 `Release` 整退(`settler.go:252`/`balancehold.go:111`) | **无 per-request hold**,入口只 gate `balance>0`,转发后 post-hoc 实扣,不足则强扣成负 + `BalanceOverdrafted`(`usage_billing_repo.go:177`) | PreConsume 预扣估算额度,Settle 按 `delta=实际-预扣` 补/退(`billing.go:34`) | **更强(=最严)**:HUAKAI 是三家唯一"真预扣 hold + Serializable Tx 原子"的。sub2 允许有限超支,new-api 无金额 hold 只有额度记账。HUAKAI 的代价=Capture/Release 必须成对,漏一个就冻钱/漏扣 → 见 T1/T2 |
| C2 | **选号 ↔ 结算锚点** | `acquisition_token` 回写 reserving claim(Pattern B),Settle 按 `(tenant,claim,acq_token)` 锁行取权威归属;不符 `ErrAcquisitionTokenMismatch`(`settler.go:89,777`) | `ReleaseFunc` 闭包持有账号,无跨请求持久锚点(`gateway_service.go:1700`) | context `channel_key` **快照**进 `ChannelError`,异步 DisableChannel 用快照防封错 key(`relay.go:357`) | **更强**:HUAKAI 用 DB 行级 token 做结算归属锚,能跨进程崩溃恢复;sub2/new-api 是进程内闭包/快照,进程死了锚点即丢。测:token 错配必须拒结算 → 见 T3 |
| C3 | **failover ↔ hold 释放/复活** | 每轮失败 `Abort` 释放 hold+slot;`prepareNextAttemptAfterAbort` 置 `reserveRes=nil`→下轮命中 `aborted` 分支 `ReReserveAbortedClaim` 复活同 claim + `attempt_seq+1` + 重建 hold(`attempt.go:342`,`claim_gate.go:114`) | `ReleaseFunc` 立即释放槽,坏账号入 `FailedAccountIDs`;**无金额 hold**,无释放/复活问题(`failover_loop.go:65`) | 预扣额度**全程只挂 relayInfo.Billing 一份,重试间不退不重扣**,循环最终失败才 defer 一次性 Refund(`relay.go:172`) | **有隐患(最需测)**:HUAKAI 每轮 Abort→重建 hold,若某轮 Abort 失败,`degradeFailureIfAbortFailed` 必须清 retry 标志停止重试(`attempt.go:163`),否则同 claim 双 hold/双 settle。new-api 的"一份 hold 不动"反而更简单——HUAKAI 换来的是每轮账目干净但配合面更大 → 见 T4/T5/T13 |
| C4 | **上游 401 ↔ 健康不降级 + 凭据热刷新** | `signalFromClassification` 对 TokenRevoked/OAuthInvalidGrant 返回 `""` 空信号(`error.go:181`),只 `triggerCredentialHotRefresh`(`handler.go:567`)+ 换号;auth-failover 独立子预算 `attemptCap++`(`handler.go:584`) | OAuth 有 refresh_token→`SetTempUnschedulable` 冷却但**刻意保 status=active** 让后台带锁刷新,**刻意不整列回写 credentials JSONB** 防回滚新 token(`ratelimit_service.go:278`) | `processChannelError` 判 `ShouldDisableChannel&&AutoBan` → **DisableChannel**(`channel.go:17`) | **等价 sub2 / 强于 new-api**:HUAKAI 与 sub2 都"401 不写健康降级"防误伤好账号;new-api 直接禁渠道更粗暴。HUAKAI 用空信号实现,sub2 用冷却+保 active。测:401 后该账号健康态**不得**变 degraded/cooling → 见 T7 |
| C5 | **选号 ↔ 健康回流(cooling/ramp)** | `ApplySignal` 累积窗口→`evaluate` 迁移状态→`UpsertRecord` 落库;下次 `PoolGate.Allow`→`IsEligible`:cooling_down 未到期拒/到期放行,ramping 按 `RampStagePct` 哈希准入(`failover.go:68,103`);`disable_cooling` 只豁免 cooling/ramping 不豁免 disabled/manual_paused | `HandleUpstreamError` 写账号字段 `RateLimitResetAt/OverloadUntil/TempUnschedulableUntil`,`IsSchedulable()` 读回排除(`account.go:137`) | `DisableChannel`→`UpdateChannelStatus`→同步 `ability` 表 `enabled=false`,选路 `GetRandomSatisfiedChannel` 不再选(`channel.go:641`) | **更强**:HUAKAI 有独立 cooling→ramping 渐进放量状态机(三家唯一),回流是**异步 UpsertRecord**;隐患=写读不一致或 ramp 永不推进会让账号永久卡死或过早满量放回。测:冷却窗口读写闭环 + ramp 推进 → 见 T8/T9 |
| C6 | **429/529 ↔ 强制冷却** | `forceCooldownFromUpstreamRateLimit`→`RateService.HandleUpstreamError` 算截止→`ChannelHealth.ForceCooldown`;**刻意不因缺 Retry-After 早退**(`dispatch.go:841`) | `handle429/handle529` 只设冷却不 disable,Anthropic 5h/7d 硬窗口优先于用户 429 规则(`ratelimit_service.go:184`) | 状态码映射 + 关键词 + AutoBan 双条件才禁(`model/channel.go:641`) | **等价**:三家都"429 只冷却不禁用"。HUAKAI 与 sub2 都刻意处理"429 不带 Retry-After"的 provider,防限流账号被反复命中。测:无 Retry-After 头仍须冷却 → 见 T10 |
| C7 | **流式已交付字节 ↔ 禁 failover** | `deliveryTracker.started()==true`→`forwardSSEAndSettle` 即使上游断也返回 `(deliveryStarted=true,nil)`,handler 判 `DeliveryStarted` 直接终止不 failover(`stream.go:342`,`handler.go:563`) | Forward 前记 `writerSizeBeforeForward`,返回后 `c.Writer.Size()` 变化则**禁止 failover** 且**不 RecordUsage**(`gateway_handler.go:791`) | 断流后 `OaiStreamHandler` 仍返回 `(partialUsage,nil)` 当**成功**,不换渠道,按部分 token 结算(`relay-openai.go:178`) | **更强**:三家都"已发字节不重试"防重复交付。差异在计费:sub2 **已发部分不计费**(平台吃成本);new-api **按部分 token 计费**;HUAKAI **已交付>0 就 settle 计费**(`stream.go:315`),对齐"上游已付成本必须计费"。测:已交付且上游断,必须 settle 且不 failover → 见 T6 |
| C8 | **结算故障 ↔ DLQ 补偿** | post-delivery settle 失败→独立 ctx(`WithoutCancel`+10s)enqueue `settlementrecovery` DLQ→worker 走 public Settle 单入口幂等重放,`ErrClaimNotReserving` 用 `CommittedProof` 三证判已提交;enqueue 自身失败只 P0 alert 不 disk spool(`billing.go:302`,`handler.go:63`) | DB 事务是真相源,balance cache 走 `QueueDeductBalance` 异步,队列满同步回退防丢;billing 不可用→circuitBreaker 拒新请求(`billing_cache_service.go:379`) | Settle 令牌调整失败**只记日志不回滚资金**,`s.settled=true` 防二次退款,**接受令牌/资金一次性漂移**;请求照返 200(`billing_session.go:45`) | **更强**:HUAKAI 是三家唯一"结算失败进持久 DLQ + worker 重放 + quarantine"的闭环;sub2 是异步队列 + 熔断,new-api 直接接受账目漂移。隐患=DLQ enqueue 若复用过期 ctx 会落不了盘=永久漏钱(已用 WithoutCancel 兜住)。测:settle 失败必入 DLQ 且可重放到 committed → 见 T11 |
| C9 | **孤儿 claim ↔ LeaseSweeper 回收** | reserve 时 `lease_expires_at=now+30min`,生命周期内不续租;Sweeper 每 30s Abort 过期 reserving claim + 扫孤儿 slot(`claim_gate.go:52`,`lease_sweep.go:83`) | `wrapReleaseOnDone` 在 ctx 取消时用独立 background ctx 兜底释放槽防泄漏(`gateway_helper.go:160`) | 无等价持久 claim 回收(额度只在内存 relayInfo) | **更强但有陷阱**:HUAKAI 30min 窗口**刻意远大于 600s 流上限**,否则跑得久的合法流式请求被 Sweeper 误 Abort→已交付内容永不计费(漏钱)+ in_flight 提前减低致账号超并发。测:长流式请求不被 sweeper 误伤 → 见 T12 |
| C10 | **幂等 ↔ 防重复扣** | claim 幂等键 `(tenant,api_key,logicalRequestID)` + `payloadHash` 指纹 + `attempt_seq`;replay 指纹检查(`claim_gate.go:77`) | `usage_billing_dedup (request_id,api_key_id)` INSERT ON CONFLICT + fingerprint 比对 + archive 表二次校验;不一致→`ErrUsageBillingRequestConflict`(`usage_billing_repo.go:45`) | `relayInfo.Billing` 单份,重试间不重扣,无 DB 级跨请求去重 | **等价 sub2 / 强于 new-api**:HUAKAI 与 sub2 都用 `(request_id, api_key_id)` 双维度 + payload 指纹防"不同内容顶替同 id 漏扣";new-api 靠内存单份。测:同 Idempotency-Key 重放不双扣、指纹变更不误当重复 → 见 T14 |
| C11 | **选号并发 ↔ 账号级 cap 门** | 选号成功即 `SessionCapRegistry.Register(accountID,sessionHash)`,下次选号 `SessionCountGate` 有最新会话视图(SUB2-EGRESS-02);nil 时安全跳过(`dispatch.go:628`) | 账号级并发 cap `AcquireAccountSlotWithWaitTimeout` + 等待队列 `IncrementAccountWaitCount`(`concurrency_service.go:165`) | 无账号级会话并发 cap(渠道 weight 选路) | **等价 sub2**(HUAKAI 的 SUB2-EGRESS-02 即移植 sub2 账号级并发)。测:注册后下次选号 cap 视图生效 → 见 T15 |
| C12 | **账号配额跨限 ↔ 快照失效** | (HUAKAI:配额跨限走 QuotaReserver/结算,选号读 PoolGate;需核实是否有等价 outbox 使快照即时反映跨限) | 结算事务内 `enqueueSchedulerOutbox(AccountChanged)`,快照服务消费刷新,防旧 used 值继续选中该账号(`usage_billing_repo.go:337`) | ability 表 `enabled=false` 同步 | **有隐患/需专测**:sub2 有"配额跨限→同事务发 outbox→快照刷新"的强闭环;HUAKAI 需验证账号配额从"未超"跨到"已超"后,下次选号是否**同一事务内**让选号视图排除它,否则配额上限形同虚设 → 见 T16(标注为需先核实的探针测试) |

---

## 3. 配合点测试用例清单(核心交付)

> 每条 = ①测哪些模块的配合 ②触发条件(如何让配合被真实触发,而非单模块 mock) ③判别断言(配合错了怎么咬住:漏钱/冻钱/重复扣/换号失败/健康不更新)。
> **优先级**:`[M]` money(漏钱/冻钱/重复扣,最高)· `[S]` 安全(串号/越权/泄漏)· `[A]` 可用性(换号/健康/回收)。按 M→S→A 排序。
> **运行环境**:`//go:build integration_pg` 标签 + 真 Postgres(`scripts/integration-pg.sh` 每包克隆纯净库串行跑,勿裸 `-tags=integration_pg ./...` 共享库假阳);上游用可注入错误码的 fake dispatcher 挂进 `ChatHandlerDeps.Dispatcher`;docker 栈用 `docker-compose.dev.yml` 起 PG + gateway。**每条测试须过 §14 变异**:把断言里描述的"配合错"真注入代码,测试必须转红。

### 3.1 [M] Money 类(漏钱 / 冻钱 / 重复扣)—— 最高优先

---

**T1 [M] · 预扣 hold ↔ 结算 Capture:成功结算必须把 hold 落成实扣(不漏扣、多退少不补)**
- **配合模块**:`ClaimGate.Reserve`(Tx1 建 hold) ↔ `Settler.Settle→Capture`(Tx2 实扣)· `balancehold`
- **触发条件**:真 PG 起一条余额行(如 $10)。请求 `PredictedCost=$1`(建 hold),上游返回 usage 使 `actualCost=$0.4`。走完整非流式 `executeNonStreamingAttempt→settleCompletion`。
- **断言**:①`billing_ledger_claims` 该 claim `status=committed`;②`balance_holds` 该 hold **不再是 active**(已 Capture 落地);③账户余额 = `10 - 0.4 = $9.6`(**按 actual 扣,不是按 predicted $1**);④`usage_records` 有一行且 `provider_account_id`/`acquisition_token` 非空。
- **咬死点**:Settle 忘调 Capture(或按 predicted 扣)→ 余额变 $9(多扣 $0.6)或 hold 永挂(冻 $1)。变异:注释掉 `settler.go:252` 的 `Capture` 调用,测试必红(余额不变或 hold 仍 active)。

---

**T2 [M] · 预扣 hold ↔ Abort Release:任何失败路径必须整额退 hold(不冻钱)**
- **配合模块**:`ClaimGate.Reserve` ↔ `Settler.Abort→balancehold.Release`
- **触发条件**:建 hold=$1 后,让选号成功但上游返回 `500`(不可重试到 finalAttempt)或 `quota_denied`。跑到 `Settler.Abort`。
- **断言**:①claim `status=aborted`;②hold 已 Release,账户余额恢复原值(**$0 净变动**);③无 `usage_records`(未成功不计费);④`ReleaseSlotAndDecrementInFlight` 已执行,该账号 `in_flight` 回退。
- **咬死点**:Abort 忘 Release → hold 永挂,余额被冻 $1 直到 LeaseSweeper 30min 后才回收(用户短期少 $1 额度)。变异:让 `Abort` 跳过 `Release(claim_gate)`(`settler.go:317`),测试断言余额恢复必红。

---

**T3 [M/S] · 选号 acquisition_token ↔ 结算归属:token 错配必须拒结算(用量不记错账号)**
- **配合模块**:`DefaultSelector.WriteAcquisition` ↔ `Settler.GetClaimForSettle`
- **触发条件**:正常 Reserve+选号拿到 `acquisition_token=A`。构造 Settle 请求时传入**错误的** `acquisition_token=B`(模拟并发/串号/回写丢失)。
- **断言**:`Settle` 返回 `ErrAcquisitionTokenMismatch`(`settler.go:777`),**不写** `usage_records`、**不 Capture**、**不 ReleaseSlot**;原 claim 仍 reserving(等 LeaseSweeper 或正确 token 结算)。
- **咬死点**:若 GetClaimForSettle 不校验 token(只按 claimID),用量会记到错账号头上 = 跨账号计费污染 + slot 被错误账号释放。变异:把 `GetClaimForSettle` 的 WHERE 去掉 `acquisition_token` 条件,测试必红(错 token 竟结算成功)。

---

**T4 [M] · failover ↔ hold 复活:换号时前一轮 hold 已释放、本轮重新预扣(不双扣、不泄漏)**
- **配合模块**:attempt 循环 `prepareNextAttemptAfterAbort` ↔ `ClaimGate.Reserve` 的 `aborted→ReReserveAbortedClaim` 分支 ↔ `balancehold`
- **触发条件**:2 个可用账号。账号 A 上游返回可重试 `503`(SwitchAccount);账号 B 成功。观察两轮 claim/hold 生命周期。
- **断言**:①全程**同一条 claim 行**(唯一幂等键),`attempt_seq` 从 0→1;②第一轮 Abort 后 A 的 hold 已 Release、A 的 slot 已释放;③第二轮 ReReserveAbortedClaim 重建 hold(不是新建第二条 claim);④最终只 Capture 一次,账户净扣 = B 的 actualCost;⑤余额**从未被同时扣两份 predicted**。
- **咬死点**:若换号不置 `reserveRes=nil`(`attempt.go:342`)→复用旧 ClaimID 但 hold 已释放 → 结算找不到 hold;若新建第二条 claim → 双 hold 双扣。变异:删掉 `prepareNextAttemptAfterAbort` 里 `ex.reserveRes=nil`,测试断言 `attempt_seq` 递增 + 单次 Capture 必红。

---

**T5 [M] · Abort 失败 ↔ 降级停重试:Abort 本身失败时禁止同 claim 继续 retry(防双 hold/双 settle)**
- **配合模块**:`Settler.Abort`(注入失败) ↔ `degradeFailureIfAbortFailed` ↔ attempt 循环 retry 门
- **触发条件**:账号 A 上游 `503`,但让 `Settler.Abort` 返回错误(注入 DB 错/mock Abort 返 err)。
- **断言**:①`degradeFailureIfAbortFailed` 把 failure 的 `RetryableBeforeDelivery/SwitchAccount/CountsAgainstAuthFailoverBudget` 全清、`AbortReason` 追加 `abort_failed=1`(`attempt.go:180`);②循环**不再进入下一轮**(因 claim 可能仍 reserving,再 retry 会撞 ReReserveAbortedClaim 状态或双预扣);③请求以终局错误返回;④后续由 LeaseSweeper 回收该 reserving claim。
- **咬死点**:不降级继续 retry → 同 claim 可能被当 aborted 复活(实际仍 reserving)→ 双 hold。变异:让 `degradeFailureIfAbortFailed` 直接返回原 failure(不清标志),测试断言"不再有第二轮选号"必红。

---

**T6 [M] · 流式已交付 ↔ settle + 禁 failover:上游中途断但已发字节,必须计费且不重试**
- **配合模块**:`StreamForwarder.deliveryTracker` ↔ `forwardSSEAndSettle` ↔ attempt 循环 failover 门 ↔ `Settler.Settle`
- **触发条件**:流式请求,fake 上游先发若干 SSE token(触发 `tracker.started()=true`)后**中途断开/报错**。
- **断言**:①`forwardSSEAndSettle` 返回 `(deliveryStarted=true, failure=nil)`(`stream.go:342`);②handler **不进入下一轮 failover**(`handler.go:563` 直接终止);③走 `settleCompletionWithRecovery`,`usage_records` 记录**已交付 token 的成本**(已交付>0 触发 settle,`stream.go:315`);④客户端只收到**一段**响应(无两上游拼接)。
- **咬死点**:已交付却 failover → 客户端收到两段拼接 + 重复扣;已交付却 abort → 内容白发漏钱。变异:把 `tracker.started()` 强制返回 false,测试断言"不 failover + 有计费"必红(会去 failover 或 abort)。

---

**T7 [M/A] · 上游 401 ↔ 健康不降级 + 换号 + 热刷新:令牌失效不误伤好账号**
- **配合模块**:`classify(401)` ↔ `signalFromClassification`(空信号) ↔ `channelhealth.ApplySignal` ↔ `triggerCredentialHotRefresh` ↔ auth-failover 子预算
- **触发条件**:账号 A 上游返回 `401`(OAuthInvalidGrant/TokenRevoked)。账号 B 可用。
- **断言**:①A 的 channelhealth 记录**保持 active/未 degraded/未 cooling**(`error.go:181` 空信号,不 UpsertRecord 降级态);②`triggerCredentialHotRefresh(A)` 被异步触发;③换号到 B 成功;④若 401 落在本应最后 slot,`attemptCap++` 给了额外一次换号(`handler.go:584`),且 `authFailoverUsed` 置 true(只额外给一次);⑤A 的 hold 已 Abort 释放。
- **咬死点**:若 401 也写健康 degrade → 只是令牌过期的好账号被误踢进冷却,后续请求可用账号变少。变异:把 `error.go:181` 的 401 分支改成返回 `SignalUpstream5xx`,测试断言"A 健康态不变"必红。

---

**T8 [M/A] · 选号 ↔ 健康冷却回流:上游 5xx 使账号进 cooling,下次选号必须排除它**
- **配合模块**:`classify(5xx)→signalFromClassification` ↔ `ChannelHealth.ApplySignal→evaluate→UpsertRecord` ↔ `PoolGate.Allow→IsEligible`
- **触发条件**:单账号 A 连续触发足够多 `500` 让 `evaluate` 迁移到 `cooling_down`(累积窗口阈值)。随后立即发新请求。
- **断言**:①A 的 channelhealth `state=cooling_down` 且 `cooling_until>now`;②新请求选号时 `PoolGate.Allow` 对 A 返回 false(`IsEligible` 在冷却窗口内拒),选号排除 A;③冷却窗口**到期后**,`IsEligible` 放行 A(`failover.go:110` 到期自动恢复,防永久卡死)。
- **咬死点**:异步 UpsertRecord 与选号读不一致 → 坏账号被反复选中连环 502;或到期不放行 → 账号永久卡死。变异:让 `IsEligible` 对 cooling_down 恒返 true(删到期判断),测试断言"冷却期内被排除"必红。

---

**T9 [A] · 健康 ramping ↔ 选号哈希准入:冷却结束进 ramping 后必须渐进放量(不一次满量)**
- **配合模块**:`channelhealth.evaluate`(cooling→ramping 迁移) ↔ `PoolGate.Allow→AdmitRamp(RampStagePct)`
- **触发条件**:账号 A 冷却到期进入 `ramping` 状态(`RampStagePct` 从低起步)。用固定 `RampAdmissionKey`(基于 req+accountID 哈希)发多个请求。
- **断言**:①`RampStagePct` 较低时,只有哈希落在准入区间的请求被放行(部分请求选中 A,部分排除);②随着成功累积 `RampStagePct` 递增,准入比例上升;③最终 ramp 推进到 100% 回到 active。
- **咬死点**:ramp 永不推进 → 账号永久半量;或 ramp 直接满量 → 刚恢复就被打爆再次 cooling(震荡)。变异:让 `AdmitRamp` 恒返 true,测试断言"低 RampStagePct 时部分请求被排除"必红。

---

**T10 [A] · 429/529 ↔ 强制冷却(无 Retry-After 也冷却)**
- **配合模块**:`classify(429)` ↔ `forceCooldownFromUpstreamRateLimit→RateService.HandleUpstreamError` ↔ `ChannelHealth.ForceCooldown`
- **触发条件**:账号 A 上游返回 `429` **不带 `Retry-After` 头**(很多 provider 如此)。
- **断言**:①A 仍被 `ForceCooldown`(用默认冷却时长,`dispatch.go:841` 不因缺头早退);②冷却期内 `IsEligible` 排除 A;③换号继续。
- **咬死点**:若缺 Retry-After 就早退不冷却 → 被限流账号永不冷却被持续命中撞墙。变异:在 `forceCooldownFromUpstreamRateLimit` 加"无 Retry-After 就 return",测试断言"A 被冷却"必红。

---

**T11 [M] · 结算故障 ↔ DLQ 补偿:流式 post-delivery settle 失败必须入 DLQ 且可重放到 committed**
- **配合模块**:`Settler.Settle`(注入 DB 失败) ↔ `settleCompletionWithRecovery` ↔ `settlementrecovery` DLQ ↔ recovery worker ↔ `CommittedProof`
- **触发条件**:流式请求内容已全部发客户端;让 Tx2 `Settle` 首次失败(注入 DB 错/超时)。随后跑 recovery worker。
- **断言**:①settle 失败后一条 `settlementrecovery` DLQ 记录被 enqueue(用独立 `WithoutCancel`+10s ctx,`billing.go:316`——即使原 settleCtx 已过期也能落盘);②worker 走 public `Settler.Settle` 重放,最终 claim `status=committed` + `usage_records` 有一行(**钱最终扣到,不漏**);③重放遇 `ErrClaimNotReserving` 时用 `CommittedProof` 三证判定已提交=幂等成功,不二次扣。
- **咬死点**:若 enqueue 复用过期 ctx → DLQ 落不了盘 = 永久漏钱(内容白发)。变异:把 `billing.go:316` 的 `WithoutCancel` 去掉、直接用传入 ctx,并让传入 ctx 已 cancel,测试断言"DLQ 有记录"必红。

---

**T12 [M] · 长流式请求 ↔ LeaseSweeper 不误伤:合法长请求不被 30min 租约误 Abort**
- **配合模块**:`ClaimGate.Reserve`(lease=30min) ↔ `LeaseSweeper.sweepOnce` ↔ `Settler.Abort`
- **触发条件**:一条 reserving claim,`lease_expires_at` 设为 `now+30min`。跑 `SweepOnce`,验证**未到期**的 claim 不被 Abort;再把 `lease_expires_at` 手动置为过去,跑 `SweepOnce` 验证**过期孤儿**被回收。
- **断言**:①未过期 claim:`SweepOnce` 返回 0,claim 仍 reserving、hold 仍 active;②过期孤儿 claim:被 `Abort(lease_expired)`,hold Release + slot 释放 + in_flight 回退;③`SweepOrphanedSlotAcquisitions` 清掉无主 slot。
- **咬死点**:若租约窗口 < 流上限(如误设 90s)→ 跑得久的合法流式请求被 sweeper 误 Abort → 已交付内容永不计费(漏钱)+ in_flight 提前减低致账号超并发。变异:把 `DefaultClaimLeaseWindow` 改成 90s + 模拟一条运行中的长 claim,测试断言"运行中 claim 不被 sweep"必红。(现有 `lease_sweep_nil_test.go` 可扩展)

---

**T13 [M] · 换号预算耗尽 ↔ 最终失败全额退 hold(多轮不累积泄漏)**
- **配合模块**:attempt 循环(`failedAccounts`/`attemptCap`) ↔ 每轮 `Abort→Release` ↔ 最终失败响应
- **触发条件**:所有账号(如 3 个)全部上游 `503`,耗尽换号预算 + RetryBudget。
- **断言**:①每一轮失败都执行了 `Abort→Release`(不是只在最后退一次);②最终 hold 全部释放,账户**净扣 $0**;③`balance_holds` 无残留 active hold;④返回 503 + 精确 `Retry-After`(`classifyPoolSelectFailure`)。
- **咬死点**:多轮中间某轮漏 Abort → hold 累积泄漏,余额被冻 N×predicted。对比 new-api:new-api 全程一份 hold 最后退一次,HUAKAI 每轮退——所以 HUAKAI 特别要测"每轮都退干净"。变异:让循环中间轮跳过 Abort(只最后 abort),测试断言"每轮 hold 数=0"必红。

---

**T14 [M] · 幂等 ↔ 防重复扣:同 Idempotency-Key 重放不双扣;指纹变更不误当重复漏扣**
- **配合模块**:`ClaimGate.Reserve` 的幂等三元(`logicalRequestID`+`payloadHash`+`committed` 分支) ↔ replay 指纹检查
- **触发条件**:(a) 同 `Idempotency-Key` + 同 body 发两次(第一次已 committed);(b) 同 `Idempotency-Key` + **不同 body**(payloadHash 变)发第二次。
- **断言**:(a) 第二次命中 `committed` 分支→**重放已有结果**,不新建 claim、不二次扣款(余额只扣一次);(b) 不同 payloadHash → replay 指纹冲突被识别(不能用同 key 顶替不同内容;按实现返回冲突错误或新 claim),**绝不静默当重复而漏扣第二笔真实用量**。
- **咬死点**:幂等键漏 payloadHash 维度 → 不同内容被误当重复漏扣;或 committed 分支重新扣一次 → 双扣。变异:把 Reserve 的幂等键去掉 payloadHash 只留 logicalRequestID,测试断言"不同 body 不被当重复"必红。

---

### 3.2 [S] 安全类(串号 / 越权 / 泄漏)

---

**T15 [S] · identity ↔ 全下游主体:计费/配额/路由/选号主体全取自鉴权上下文,绝不读 body**
- **配合模块**:`APIKeyResolver.Resolve` ↔ `ClaimGate.Reserve` / `QuotaReserver.Reserve` / `Router.Plan` / `Selector.Select`
- **触发条件**:用租户 T1 的 hk_key 鉴权,但请求 body 里**塞入伪造字段**(如 `"user_id": <T2 用户>`、`"tenant_id": <T2>`)。
- **断言**:①`billing_ledger_claims` 该 claim 的 `tenant_id/api_key_id/user_id` **全部 = T1**(来自 Identity,`dispatch.go:443`),与 body 伪造值无关;②配额记在 T1;③Router.Plan 的 `RequestContext` = T1;④选号 `UserGroup` = T1 的组。
- **咬死点**:若任一下游从 body 取主体 → 越权计费(记到别人头上)/ 跨租户串号 / 配额穿透。变异:把 `dispatch.go:443` 的 `ident.TenantID` 换成从 body 解析的 tenant,测试断言"claim.tenant_id==T1"必红。这是 §4 铁律的守门测试。

---

**T16 [S/A] · 账号配额跨限 ↔ 选号快照即时排除(探针测试,需先核实 HUAKAI 是否有等价 outbox)**
- **配合模块**:配额结算(账号 quota_used 递增跨限) ↔ 下次选号视图
- **触发条件**:账号 A 配额剩 1 次。发请求 N 使其"未超→已超"跨限,紧接着发请求 N+1。
- **断言**:①请求 N 结算后 A 的账号配额标记为已超;②**请求 N+1 的选号必须排除 A**(配额已满)。若 HUAKAI 无同事务 outbox/即时失效机制,则验证是否存在"选号视图滞后仍选中 A 导致 used 大幅超 limit"的窗口。
- **咬死点**:对照 sub2(`enqueueSchedulerOutbox(AccountChanged)` 同事务发 outbox 刷新快照)——若 HUAKAI 依赖异步刷新且窗口过大,配额上限形同虚设(超卖上游账号)。**本条先作为探针**:跑通后确认 HUAKAI 的等价机制,再定成回归断言。这是对照表 C12 标注的需核实缝。

---

### 3.3 [A] 可用性类(换号 / 回收 / 竞争)

---

**T17 [A] · 选号 ClaimID 回写竞争 ↔ slot 不泄漏:两请求抢同一 claim,WriteAcquisition 竞争必须释放已抢 slot**
- **配合模块**:`DefaultSelector.tryLayer→slots.Acquire` ↔ `binding.DBClaimGate.WriteAcquisition`(竞争) ↔ slot 回滚
- **触发条件**:并发让两个 Select 对同一 reserving claim 抢 slot;一个 WriteAcquisition 成功,另一个 `WHERE status=reserving` 匹配 0 行 → `ErrClaimRace`。
- **断言**:①失败方立即 `acquired.release(ctx)` 释放刚抢的 slot(`default_selector.go:234`);②失败方上抛 `ErrClaimRace`(区分"无候选" vs "被抢占");③账号并发数无虚占(slot 计数回到正确值)。
- **咬死点**:WriteAcquisition 失败却不释放 slot → slot 泄漏,账号并发被虚占,后续请求全排队。变异:删掉竞争失败分支的 `acquired.release`,测试断言"slot 计数不虚高"必红。

---

**T18 [A] · 选号失败分类 ↔ hold 释放 + 精确 Retry-After:各类选号失败都必须 Abort 释放 hold**
- **配合模块**:`pool.Selector`(各类失败) ↔ `classifyPoolSelectFailure` ↔ `Settler.Abort` ↔ 客户端响应
- **触发条件**:分别构造 `ErrNoEligibleAccount`/`ErrNoSlotAvailable`/`ErrAllChannelsDegraded`(→503+Retry-After)、`ErrClaimRace`(→409)、`ErrKeyRateLimited`(→429)、`WaitPlan` 队列等待(→429)、空账号(→503)。
- **断言**:①每种失败都调用了 `Settler.Abort` 释放 hold(`dispatch.go:609/616`,`handler.go:949`);②HTTP 码与 `Retry-After` 正确(503 从 `NoCapacityError.EarliestRecoveryAt` 算);③无残留 active hold。
- **咬死点**:任一选号失败分支漏 Abort → hold 泄漏冻钱。变异:删掉 `WaitPlan` 分支的 `Abort(queue_wait)`,测试断言"队列等待失败后无残留 hold"必红。

---

**T19 [A] · 换号排除集 ↔ 下轮选号:失败账号加入 ExcludedAccounts 后不再被选中**
- **配合模块**:attempt 循环 `failedAccounts` ↔ 下轮 `SelectionRequest.ExcludedAccounts` ↔ `Selector.Select`
- **触发条件**:账号 A 失败(SwitchAccount)→ `failedAccounts[A]`。下轮选号在 A、B 都健康时,验证只可能选 B。
- **断言**:①下轮 `SelectionRequest.ExcludedAccounts` 含 A(`handler.go:571`,`dispatch.go:562`);②Selector 过滤掉 A,选中 B;③即使 A 健康态正常(仅本请求失败)也在本请求内被排除,避免同请求内死循环打 A。
- **咬死点**:排除集未回传 → 坏账号被同一请求反复选中,换号预算空耗在同一账号,连环 502。变异:让下轮 `ExcludedAccounts` 传空集,测试断言"第二轮不选 A"必红。

---

**T20 [A] · L2 缓存命中 ↔ 零成本终结 claim:命中缓存必须 CommitCacheHit 而非留悬空 claim**
- **配合模块**:`reserveQuota` 前的 `serveL2CacheIfAvailable` ↔ `ClaimGate`(CommitCacheHit 路径) ↔ `Settler` provider-less usage 记录
- **触发条件**:非流式请求命中 L2 响应缓存。
- **断言**:①命中后走 `CommitCacheHit` 零成本终结该 claim(`settler.go:463` Capture zero);②写一条 provider-less `usage_records`(无 `provider_account_id`/`acquisition_token`,`settlement_source=response_cache_l2`);③**不选号、不抢 slot、不发上游**;④hold 被 capture 成 $0(缓存命中不扣费或按缓存策略),无残留 active hold。
- **咬死点**:命中缓存却不终结 claim → claim 悬空等 LeaseSweeper(冻 hold 30min);或走了选号+上游(缓存白命中,浪费上游成本)。变异:让缓存命中路径跳过 CommitCacheHit,测试断言"命中后 claim 已终结无残留 hold"必红。

---

### 3.4 测试落地要点(直接据以在 docker 栈上跑)

1. **分层**:C1/C2/C10(纯 Tx1/Tx2 账目)可复用 `internal/billing/*_integration_test.go` 现有骨架(`settler_integration_test.go`、`claim_gate_integration_test.go`、`balancehold_settle_integration_test.go`、`settler_refund_idempotency_integration_test.go`)扩展断言;C3–C9/C11(跨 handler↔billing↔pool↔channelhealth)在 `internal/gatewayhttp` 层用注入式 fake `Dispatcher`/`Selector` + 真 PG 的端到端配合测试。
2. **上游可控**:fake dispatcher 需能按脚本返回 `200(带 usage)` / `401` / `429(有/无 Retry-After)` / `500` / `流式先发 N token 再断`,以真实触发 classify→signal→health→failover→settle 全链。
3. **变异即验收**:每条 T# 的"咬死点"段就是变异脚本——先把该缺陷注入生产码跑一遍确认测试转红(§14),再还原确认转绿;还原用 `cp` 备份或先 commit 再变异(勿 `git checkout` 抹实现,见记忆 footgun)。
4. **DB 隔离**:走 `scripts/integration-pg.sh`(每包克隆纯净模板库串行),勿裸 `go test -tags=integration_pg ./...`(共享库假阳)。
5. **docker 栈冒烟**:`docker-compose.dev.yml` 起 PG+gateway,用真 hk_key 打 `/v1/chat/completions`,对 T7(401 健康不降级)、T8(5xx cooling)、T11(DLQ 补偿)做端到端观察——这三条是 Owner 反复关心的失败协作,最值得在真栈上跑。

---

**覆盖 Owner 点名的失败协作**:上游错→换号 hold 不泄漏(T4/T13/T18/T19)· 流式中途断(T6/T12)· 余额不足全链回滚(T2 + Reserve 侧 402 原子回滚)· 结算故障 DLQ 补偿(T11)· 401 换号不误伤(T7)· 健康回流(T8/T9/T10)。
---

## 7. 实测发现(2026-07-02 relay 细粒度 E2E,真 Grok 上游)

对照本文档的配合点(C#/T#)在 docker 栈(huakai-direct)上真实触发验证。总消耗约 $0.001(最便宜模型 grok-code-fast-1)。

### 7.1 已验证 PASS 的配合点

| 配合点 | 验证结论 | 判别证据 |
|---|---|---|
| C1 预扣 hold↔结算 capture | ✅ 精确扣费,失败释放 | committed 扣费=预测微调后实扣;停用账号→hold 施加即回滚 held=0 |
| C10 幂等↔防重复扣 | ✅ 单扣费单重放 | 同 Idempotency-Key 两发→响应 id 相同、仅 1 committed、只扣 1 次 |
| C11 选号并发↔账号级 cap | ✅ 打满触发 | 账号 cap_concurrency=1、6 并发→唯一槽占用后 #4 得 429 queue_wait,零扣费无双扣 |
| per-key RPM 拒绝 | ✅ 429+Retry-After 不扣钱 | requests/60s/limit=3,第 4 次 429、超限 claim aborted/quota_denied/actual_cost=NULL |
| per-key quota 并发 cap | ✅ 打满触发 | concurrency/limit=3、12 并发→恰 3×200(=cap)+2×429(零扣费) |
| 分组倍率入账 | ✅ 精确翻倍 | ratio=2.0→actual_cost 恰为 1x 的两倍(唯一 prompt 避 L2 缓存) |
| 账号停用↔释放 hold(C 类) | ✅ 钱未冻结 | 停用→503,held 保持 0、claim aborted/pool_no_capacity |

### 7.2 抓到的配合缺口(surface Owner)

- **🔴 [并发健壮性] billing reserve 用 Serializable 无重试,单用户并发大量 500**
  - 配合点:`ClaimGate.Reserve`(C1)争抢同一 `user_balances` 行。根因:`claim_gate.go` 用 `pgx.Serializable`,`reserveClaim` 只调一次无重试循环;序列化失败(40001)非 `ErrClaimRace` 直接映射 `500 reserve_error`(dispatch 侧)。
  - 现象:同用户并发发 N 请求,多数在 ~0.08s 秒级 500(远早于上游),跨三轮稳定复现(6/12、4/6、5/8 并发)。billing reserve 是第一瓶颈,掩盖下游限流。
  - money-safety:500 事务干净回滚,**不扣费不泄漏 hold**(已验)。缺口是**可用性**:并发请求得 500 而非干净 429/排队/重试。
  - 三镜对照:new-api `relayInfo.Billing` 单份内存不走 DB 行锁并发;sub2 balance cache 走异步 `QueueDeductBalance` 非同步 Serializable。HUAKAI「真预扣 hold + Serializable 原子」最严,代价=并发争用需重试兜住(当前缺重试)。修法方向:Reserve 对 40001 序列化失败加有限退避重试(非 money 改动,是健壮性)。

- **🟠 [C9 孤儿 claim 回收] 并发上游超时 abort 可孤儿化 reserving claim → hold 冻结最长 30 分钟**
  - 并发下上游超时的 abort 在争用下可能未释放 hold,留 `reserving` 孤儿 claim(两次复现)。**恢复有效**:LeaseSweeper(30s tick)会 abort lease 过期 claim 释放 hold——强制过期验证 t+10s 内 held→0。但自然 claim lease=30 分钟,即孤儿 hold 最长冻 30 分钟才自动回收。非永久亏损。对应 C9「刻意 30min 窗口」的已知取舍,但并发超时路径的即时释放值得加强。

- **🟠 [billing↔quota reconciler 配合断裂] quota reconciliation 在此部署未工作**
  - `quota_reconciliation_jobs` 全卡 `queued`(0 成功,报 no rows)、`quota_reservations` 停 `reserved`、0 个 concurrency 槽经 settled 释放(全靠 lease_expired)。判别性:cap=2 下**串行**连发 3 个→200,429,429(完成的槽未即时释放)→ concurrency 退化为「90s 窗口内请求启动数上限」而非「真在途并发数」。**待定性**:是此 dev 部署未起 reconciler worker(配置)还是真 bug——P1 批正在核。非 money bug(RPM 与金额均正确)。

- **🟡 [observability] usage_records 成本分项列系统性为 0**
  - `input_cost`/`output_cost` 全 0(聚合 `actual_cost` 正确)。对账分项报表会空。非 money bug。

