# 核心中转链路逻辑级地图 + 测试地基(链路①~⑤)

> 日期:2026-06-20 · 基线:`feat/frontend-portal` @ b59abd6e · 真码读取:`backend/internal/**` + 三家镜像 `~/refs/{sub2api,new-api,CLIProxyAPI}`
>
> **为什么写这份**:核心中转功能(relay→计费→配额→选号→流式)逻辑链很长很纠缠,不把"端到端逻辑链路 + 必守不变量 + 最易错热点"搞清楚,将来写测试会很被动(测了不知道在守什么、漏了不知道漏在哪)。这份是把五条核心链路逐条拆到 `file:line` 级 + 三家对照 + 不变量 + 测试必覆盖分支 + 复杂度热点的**测试地图**。写测试、改核心、查 bug 前先查这份。
>
> **clean-room 声明**:本文对三家(sub2api / new-api / CLIProxyAPI)的描述均为读源码后的**机制意译**,不含任何上游标识符/字段名/注释原文。三家对照仅出现在本类 `docs/process` 分析文档,**禁止**出现在生产或测试代码注释里。

---

## 0. 五条链全景

| 链路 | 脊柱文件(`backend/internal/`) | money 锚点 | 三家是否有等价 |
|---|---|---|---|
| ① 请求主链 | `gatewayhttp/chat_completions_{handler,attempt,dispatch,stream,billing}.go` + `router/` | `reserveRes.ClaimID` 贯穿 | 全有(结构各异) |
| ② Tx1/Tx2 资金链 | `billing/{claim_gate,settler,balancehold,state,reconciliation_worker,lease_sweep}.go` + `settlementrecovery/` | claim 行 status + acquisition_token | sub2api 单相后付 / new-api 内存 flag 两相 / CLIProxy **无** |
| ③ 账号池选择 | `pool/router/{default_selector,pasr,gates,hrw_ring}.go` + `pool/dispatcher/` + `pool/binding/` | acquisition_token(同 Tx 写回 claim) | sub2api 调度器 / new-api channel 选择 / CLIProxy 轮换 |
| ④ 配额/限流 | `quota/**` + `budget/**` + `quotaenforce/` + `budgetenforce/` | claim 级 reservation | sub2api post-hoc / new-api 单维+中间件 / CLIProxy **无** |
| ⑤ 流式+协议转换 | `gateway/{forwarder,forwarder_types,stream_scanner}.go` + `proto/**` + `protosse/` | usage 捕获 → SettleRequest | 三家 usage 不抹零不变量**同构**,转换结构各异 |

**贯穿全链的总不变量(任一条链违反都是钱账/安全事故):**
1. **一个 claim 恰一次终结** —— Settle(成功)/ Abort(失败零成本)/ CommitCacheHit(缓存零成本),三选一且仅一次。每条失败路径都必须配对一次 Abort。
2. **claim 是 money + 幂等 + 审计三合一锚点** —— `(tenant, api_key, idempotency_key)` 唯一;`acquisition_token` 把"哪次 pool slot 获取"与"哪笔结算"硬绑定(跨 attempt 不串号)。
3. **交付前/后单向不可逆** —— `deliveryTracker.started()` 一旦 true:不换模型、不换号重试、不写终局 JSON 错误;已交付的流 settle 失败必走 DLQ 绝不 abort。
4. **身份只来自认证上下文** —— TenantID/UserID/APIKeyID/AllowedModels 绝不从 request body 派生。
5. **租户隔离** —— 所有 claim/hold/refund/slot/quota 查询带 `tenant_id` 谓词;L2 cache serve 前校验 TenantID+ScopeID,mismatch 删条目当 miss。
6. **fail-open 只松不紧** —— 基础设施抖动让限制失效(放行)而非误挡健康账号/误拒用户;但绝不能 fail-open 路径误扣用量。
7. **钱账不丢** —— 已交付未结算的灰区必经 DLQ 持久兜底;pending/DLQ 必被 worker 终结,无永久挂起。

---

## 链路① 请求主链(鉴权→路由/计划→attempt→上游转发→响应回写)

### 流程骨架(真码)
`/v1/chat/completions` 是脊柱,`/v1/messages`、`/v1/responses`、Gemini native 复用同 pipeline(仅 EndpointFamily 不同)。

- **S1 入口+配置门** `chat_completions_handler.go:304` `NewChatCompletionsHandler`;`:308` `chatHandlerConfigured` 检 8 核心 dep,未配 503。
- **S2 鉴权** `:314` `Auth.Resolve` → `auth.Identity`(身份全来自认证上下文,绝不读 body)。错误分级:ErrAuthMisconfigured/ErrAuthBackend→503、ErrForbidden→403、其它→401。
- **S3 校验+模型 ACL** `:332` `validateChatCompletionsRequest`(读 body/校验 JSON/解析 model+stream/推断协议/生成 RequestID);`:336` `apikeymodelallow.AllowsCSV` per-key 模型 ACL,违则 403,**在路由前**。
- **S4 执行体** `:343` `newChatExecution`(`dispatch.go:62`)装配 `chatExecution`,预解析 clientSessionID + 两条 billing policy;`:344` `runWithModelFallback(newDeliveryTracker(w))`。`deliveryTracker`(`attempt.go:450`)是"是否已写出"的**单一事实源**。
- **S5 model-fallback 外层循环** `handler.go:460`。每轮 `:468` `runSingleModel`;成功返回;若 DeliveryStarted/`w.started()` 立即返回(已交付不可换模型);失败且允许 fallback 且未超 MaxDepth → `prepareNextModelFallback` 重置后重入。
- **S6 单模型** `runSingleModel(:500)`:S6a 预热拦截(默认关);S6b `prepareRoute`(`dispatch.go:287`)→ `registry.Resolve` + `bodyfeatures.Detect` + `Router.Plan`(有界 AttemptPlan:单池2/多池3)+ `refreshRequestSessionHashes`;S6c 输入审核(默认跳);**S6d `reserveClaim`(`dispatch.go:459`)= Tx1 预扣**;S6e 非流式 L2 cache 命中校验 tenant/scope;S6f attempt 内层循环 `:549`。
- **S7 runAttempt** `attempt.go:373`:`prepareClaimAndAccount`(补 reserve + `selectPoolAccount`)→ `resolveCredential` → 分流非流式 `executeNonStreamingAttempt`(`billing.go:79`)/ 流式 `executeStreamingAttempt`(`stream.go:130`)。
- **S8a 非流式** `billing.go:79`:dispatch → submitAuditLedger → 翻回客户端格式 → actualCost → **settleCompletion(Tx2)** → recordIdempotencyReplay + L2 cache.Set(用实际成功 attempt 的 upstreamModelID)。
- **S8b 流式** `stream.go:130`:`forwardSSEAndSettle(:220)` 逐事件转发累加 usage;settle 三选一(`Chargeable || DeliveredTokenCount>0 || AmbiguousUsage`)→ `settleCompletionWithRecovery`(失败转 DLQ),否则 Abort;`ledgerFailClosed` 强制 abort;`fwdErr && !deliveryStarted` 才返回可重试。
- **S9 attempt 收口** `handler.go:561-593`:DeliveryStarted/DeliveredToClient → 终止;`shouldRetryAttemptFailure`(`attempt.go:204`)**双通道**:401 auth-failover 子预算(`!authFailoverUsed` 至多一次,`:587` attemptCap++)独立于普通 attempt 预算。

### 三家对照(一句话)
- **sub2api**:handler 内联一个 `for{}` failover 循环(选号+并发槽+转发+failover 全在一个函数体);计费是预检 eligibility + 事后异步 RecordUsage(无两段式原子)。
- **new-api**:controller 单层 `for retry` + per-channel adaptor;PreConsume 预扣 + defer Refund + PostConsume 三段(非单事务原子)。
- **CLIProxyAPI**:`conductor.Execute` 单层 `for attempt` + selector;**无任何 money 锚点**(无 reserve/settle/quota)。

### 必守不变量
见全景总不变量 1/2/3/4/5,外加:**⑦ attempt 预算双子预算独立**(普通有界 + 401 至多一次额外换号,即使落在最后一次普通 attempt);**⑧ protocolLoss 每步翻译后刷新且随 abort/settle 携带**;**⑨ abort 自身失败必降级为不可重试 + `abort_failed=1`**(防 claim 卡 reserving 还被同键重试双扣);**⑩ L2 cache 写键用实际成功 attempt 的 upstreamModelID**(禁把 fallback 响应写进 primary 键)。

### 测试必覆盖分支(P0 优先)
1. **[P0]** 流式已交付 + settle 失败 → DLQ 不 abort 不重试。
2. **[P0]** 流式未交付 + fwdErr → 返回可重试换号。
3. **[P0]** 401 auth-failover 子预算落在**最后一次普通 attempt** → 额外换号且至多一次。
4. **[P0]** 幂等命中但重放表无数据 → 409 `replay_without_cache` 绝不重扣。
5. **[P0]** L2 cache 跨租户/principal → 删条目当 miss 绝不 serve(S0 级泄露)。
6. **[P1]** 配额 denied vs fail-open(infra error)二分。
7. **[P1]** abort 自身失败 → 降级不可重试 + `abort_failed`。
8. **[P1]** 池无容量 → 503 + 精确 Retry-After(向上取整≥1,无恢复时刻退化 5)。
9. **[P1]** model-fallback 已交付后不再切模型。
10. **[P2]** 上游 2xx 超 1MiB → 终止不重试,502 而非截断喂翻译。

### 复杂度热点(最易错,优先强测试)
- `chat_completions_handler.go:500-595` attempt 内层循环 + 双子预算(attemptCap 动态 ++、planIdx 越界复用最后 pool plan、`shouldRetryAttemptFailure` 双 bool 组合)—— off-by-one 高发,**必须 mutation 证 RED**。
- `chat_completions_attempt.go:204-223` `shouldRetryAttemptFailure` 三布尔(DeliveredToClient/replayableBody/finalAttempt)+ 两类预算交织 —— 非歧视性测试高发区。
- `chat_completions_stream.go:220-333` `forwardSSEAndSettle` settle/abort 三选一 + ledgerFailClosed + DLQ + `fwdErr&&!deliveryStarted` 四判定纠缠(流式 money 最深处)。
- `chat_completions_stream.go:680-775` 缺 usage 估算多层降级(no-usage 定稿 SQL 只认全零的隐式约束)。
- `chat_completions_dispatch.go:727-840` 上游错误分类 + protocolLoss 刷新时机。
- `chat_completions_handler.go:950-981` `classifyPoolSelectFailure` 5 类错误→HTTP+abort+Retry-After 映射。

---

## 链路② Tx1/Tx2 资金链(reserve/hold→转发→settle/退款/对账)

### 流程骨架(真码)
- **Tx1 reserve/hold** `completionshttp/billing.go:20`(chat 同形 `chat_completions_dispatch.go:467`)→ `billing/claim_gate.go:71` `Reserve`(Serializable 事务):算幂等指纹(8 字段 sha256,显式排除 PoolingGroupID/客户端 Idempotency 头)→ `GetClaimByIdempotency FOR UPDATE`(committed→IdempotencyHit / reserving→ErrClaimRace / aborted→`ReReserveAbortedClaim` 复活)→ 重放检测(同 logical_request_id 异指纹→409)→ `InsertClaim`(23505→ErrClaimRace)→ 同 Tx 挂 hold `balancehold.go:49`(原子 `(balance-held)>=cost` 才扣)→ commit。随后 `reserveQuota` 配额维度预扣。
- **转发期挂 hold** `pool/dispatcher/slot_manager.go:70` `Acquire`(Serializable:IncrementInFlightCount cap 满→ErrNoSlotAvailable + token=uuid + InsertSlotAcquisition)→ `WriteAcquisitionToken`(`SET provider_account_id, acquisition_token WHERE status='reserving'`)—— **这是 Tx1 与 Tx2 的连接键**。
- **Tx2 settle 成功** 流结束构造脱钩 ctx `chat_completions_stream.go:279` `context.WithTimeout(context.WithoutCancel(ex.ctx),30s)`(**关键:客户端断连不取消 Tx2**)→ settle 三选一(`:291`)→ `settleCompletionWithRecovery` → `billing/settler.go:77` `Settle`(Serializable):`GetClaimForSettle`(四键 `id+tenant+acquisition_token+status='reserving' FOR UPDATE`)→ 权威列以 claim 为准(防写错归属)→ `CostForAttempt`(只 Partial 收费)→ **单 Tx 七效果**:billing_event + 可选 replica + usage_record(SAVEPOINT 失败转 DLQ)+ 可选 scheduler_outbox + ReleaseSlotAndDecrementInFlight + UpdateClaimCommitted(`WHERE status='reserving'` 单翻转)+ Capture hold(按 actual 实扣释放差额)→ commit。配额二级补账 fail-open。
- **Tx2 abort 退款** `settler.go:271`:`FOR UPDATE` 锁 claim,非 reserving→ErrClaimNotReserving;UpdateClaimAborted + Release hold 全额 + 写 claim_aborted 零成本 billing_event + 可选零成本 abort usage_record + ReleaseSlot。
- **Tx2 CommitCacheHit** `settler.go:425`:缓存命中**必须 committed 而非 aborted**(否则审计把成功记中止);provider-less usage_record + Capture(zero)。
- **对账 + DLQ**:`PendingReconciliationWorker`(`reconciliation_worker.go:56`,`finalizePendingNoUsageSQL:156` grace 期外零 token 零成本行 `FOR UPDATE SKIP LOCKED + NOT EXISTS` 去重终结)+ `LeaseSweeper`(`lease_sweep.go:78` 扫过期 reserving claim 逐个 Abort + 回收孤儿 slot)+ `settlementrecovery/handler.go:39`(DLQ 重放走完整 Tx2 幂等,ErrClaimNotReserving→查三证 CommittedProof 视幂等成功)。
- **审计 mismatch 退款** `settler.go:557` `Refund`(append-only):**先 FOR UPDATE 锁 claim 再查幂等**(锁前置防并发双退)→ 同 audit_request_id 已退返存储额 → 累计 SUM ≤ original → 写 reconciliation_appended 负事件 + 回补 user_balances。原 claim/usage 行不可变。

### 三家对照
- **sub2api**:单相后付。请求跑完一次性 `Apply`,`usage_billing_dedup(request_id,api_key_id)` 表保 at-most-once;失败请求=不 Apply=天然免双扣;**无预扣/hold/退款**。
- **new-api**:两相但锚点是**内存 session bool flag**(`fundingSettled/settled/refunded`);进程崩溃即丢、无 DB 锚、无 settle-后-DLQ-对账。
- **CLIProxyAPI**:no-equivalent(纯 relay 无计费)。

### 必守不变量
全景总不变量 1/2/5/7,外加:**Tx1 hold 唯一**(uq_claims_idempotency)、**Tx2 settle 单翻转**(`WHERE status='reserving'` 二次→ErrClaimNotReserving)、**settle 四键全中**(token 不符→mismatch)、**退款幂等 + 锁前置 + 累计 SUM≤original**、**settle 后不可 abort/refund 要求 committed**(状态机单向 `reserving→{committed|aborted}`)、**每次 Tx2(含 abort/cache-hit)必写 usage_record**(失败转 DLQ 不静默丢)、**hold 与实扣守恒**(held=predicted,Capture 按 actual,abort 全额释放)、**in_flight 守恒**(acquire+1/释放各-1 + GREATEST 防负)。

### 测试必覆盖分支(P0 优先)
1. **[P0]** 流式部分交付(DeliveredTokenCount>0)客户端断连 → 必 settle 收已交付(脱钩 ctx)。**mutation:去掉 `WithoutCancel` 应 RED**(本会话审计 S1-2 原型)。
2. **[P0]** 流式零交付断开 → abort 零扣(hold 全额 Release/claim=aborted/usage actual_cost=0/in_flight-1)。
3. **[P0]** settle 失败 → DLQ 重放幂等(claim 已 committed → 三证 CommittedProof 视幂等成功,不二次扣)。
4. **[P0]** 并发同 claim 两个 settle → 一成功一 ErrClaimNotReserving,只扣一次。
5. **[P0]** 退款累计上限 & 并发双退 → 总退≤原扣、同键只退一次。**mutation:把锁移到幂等查之后应 RED**。
6. **[P1]** claim 竞争(并发 Reserve 同幂等键)→ 只一行 claim 只一个 hold。
7. **[P1]** settle 后再 abort/refund → 被拒不双退。
8. **[P1]** usage_record 插入失败 → SAVEPOINT 隔离转 DLQ 不回滚整 Tx2。
9. **[P1]** 租约丢失/孤儿 → LeaseSweeper Abort + 回收 slot + in_flight-1。
10. **[P2]** cache-hit 记 committed 非 aborted;opt-in 无余额行 reserve 放行 / refund no-op。

### 复杂度热点
- `settler.go:77-269` Settle 主体(单事务 7+ 副作用,顺序/漏 rollback 即 money 不一致;权威列 coalesce vs claim 列历史出过 bug)——**最该全分支 + mutation**。
- `settler.go:557-736` Refund(锁前置/幂等查/累计上限/opt-in no-op 四处任一退化即倒贴钱;并发双退 + 不同金额重放最难测)。
- `state.go:110-154` `AttemptFromGatewayDraft`(流式状态判定分支迷宫,直接决定收不收费,反复出过 bug)。
- `chat_completions_stream.go:279-317` settle/abort 三选一 + 脱钩 ctx(本会话审计修复点)。
- `claim_gate.go:91-153` Reserve 三态 + 复活 + 指纹冲突 + re-reserve 后 AttemptSeq 兼容(隐藏耦合)。
- `settlementrecovery/handler.go:62-85` + `reconciliation_worker.go:156`(DLQ 重放幂等 + NOT EXISTS/SKIP LOCKED 去重,多副本下最易重复终结/永不终结)。
- `quotaenforce/settler.go:141-222` fail-open 二级补账(故意不一致边界,误改 fail-close 让成功请求回滚)。

---

## 链路③ 账号池选择(gate 过滤→sticky→分层 routing→slot 获取→claim 写回→fallback/WaitPlan)

> ⚠️ **`pool/*` 在碰撞写面**(`pool/registry/proxy/channel/gateway*/rate/admin/gatewayhttp/tlsfp`):只读不限;若需改 → 纯 additive 不改既有语义,独立 worktree/PR。

### 流程骨架(真码)
两条 selector 并存,dispatcher 按 mode flag 路由(默认 DefaultSelector;PASRSelector 是自有 cache-aware,经 shadow/canary/primary/strict 灰度),共享 AccountSource/GateChain/SlotManager/ClaimGate seam。

- **入口** `chat_completions_dispatch.go:597` `Selector.Select(SelectionRequest{...})`,ClaimID=`reserveRes.ClaimID`(billing 预留先于 select)。
- **DefaultSelector** `default_selector.go:77`:① `ListAccounts`(`account_source.go:36` DBAccountSource,SQL 端做 model_allow/capability 过滤,返 AccountSnapshot)② policy ③ `gates.ForSelection`(一次准备,SelectionGatePreparer 查库一次后逐候选复用)④ filter 逐 account 跑 `gates.Allow` 全链(ordered:Tenant→Lifecycle→Channel→Protocol→Model→Capability→Credential→Health→GroupPolicy→Exclusion→Pinned→WindowCost→SessionCount→ContextWindow→RatePrecheck;大量 gate fail-open)⑤ 空 eligible → `earliestPoolRecovery(:386)` 估最早恢复时刻包进 NoCapacityError;全因 health→ErrAllChannelsDegraded ⑥ 分层 routing(routed/sticky)⑦ fresh 层 `rankFresh(:247)`(Priority↑→LoadRate↑→LastUsedAt 早)+ `topK(:308)` 等价前缀 + `weightedReservoirIndex(:279)` 或 Shuffle(randMu 串行)⑧ `trySticky/tryLayer(:206)` 逐候选:**二次 gates.Allow**(防漂移)→ `slots.Acquire` → claim writeback ⑨ slot `slot_manager.go:50`(跨表 tenant 校验 + Serializable 重试 + IncrementInFlightCount + InsertSlotAcquisition lease 90s)⑩ claim 写回 `binding/claim_gate.go:35`(0 行→ErrClaimRace)⑪ fallback `fallbackPlan(:344)` WaitPlan ⑫ 调用方收尾 `dispatch.go:619`(WaitPlan→abort+429;AccountID==0→abort+503;成功→Register session)。
- **PASRSelector** `pasr.go:132`:HRW ring(splitmix64+SHA256,账号增减仅 1/N 段重排)+ K=3 段 + score `Blend(blend.go:9)`=LocalityBonus(hasCache?1.0)+headroom*0.3-Degraded*2.0 + `acquireAndReturn(:432)`(pre-mutation 错可 fallback / post-mutation 用 WithoutCancel+2s release + fail-closed)+ cache 反馈闭环(连 2 次 miss→Demote 清 hasCache)。

### 三家对照
- **sub2api**(最成熟,与 HUAKAI 高度同构,HUAKAI 明显借鉴它):三层 sticky(PreviousResponseID 锚点→SessionHash→fresh)+ EWMA 多信号打分(priority+load+queue+errorRate+ttft)+ 加权水库 + **sticky-escape**(质量差主动逃离粘滞,HUAKAI 无此);Redis 并发槽(无 acquisition_token 锚定 billing)。
- **new-api**:粒度是 channel 非 account;内存缓存两级 priority→weighted-random(纯静态,无 LoadRate/health-EWMA);auto-ban 布尔 enabled 持久翻转(粗粒度);**选择期无 slot/claim**。
- **CLIProxyAPI**:最轻量,按 priority 取最高层 + round-robin/fill-first 游标 + session 亲和缓存(内存 TTL);modelCooldownError earliest 机制≈HUAKAI earliestPoolRecovery;**无 slot/claim/billing**。

### 必守不变量
**① 选出账号必过 filter 全链 + tryLayer 内二次 gates.Allow**(filter 后到 acquire 前账号可能被改 health)**② WriteAcquisitionToken 0 行→ErrClaimRace 必 bubble**(绝不映射成 no-eligible)**③ 并发不超 slot**(IncrementInFlightCount 0 行→ErrNoSlotAvailable)**④ slot 与 claim 同租户 ⑤ StickyState 如实标 hit/miss**(responses 链据此剥 previous_response_id)**⑥ 已 mutate 的 slot 必 release**(PASR post-mutation 用 WithoutCancel+2s,release 幂等)**⑦ select 任何失败必 Settler.Abort** ⑧ fail-open gate 只松不紧 **⑨ PASR pre-mutation 可 fallback / post-mutation 必 fail-closed**(否则双 claim race)⑩ rand 持 randMu ⑪ earliestPoolRecovery 取键与 modelRateLimitGate 一致。

### 测试必覆盖分支(P0 优先)
1. **[P0]** claim 竞争(ErrClaimRace)→ 仅一个拿到 AccountID,另一个 bubble + slot release。**mutation:把 `claim_gate.go:48` 的 ErrClaimRace 改返 nil 应 RED**。
2. **[P0]** PASR post-mutation release 在 ctx-cancel 下 → 用 WithoutCancel 必执行 + fail-closed。**mutation:换回原 ctx,ctx-cancel 场景应发现 slot 未 release**。
3. **[P0]** sticky miss(绑定账号被 gate 挡)→ fall-through 选新号 + StickyState=miss(供 responses 剥链)。
4. **[P1]** slot 满逐候选 continue(非整体失败);全满进 WaitPlan。
5. **[P1]** 全限流耗尽 → NoCapacityError + earliestPoolRecovery(两账号不同恢复时刻断言取较早);全 health→ErrAllChannelsDegraded。
6. **[P1]** 重试排除集(AttemptSeq>0 排已试,全试过→no-capacity 不死循环)。
7. **[P1]** 租户隔离(slot 跨租户拒;PASR 段表 key 含 tenant 不共段)。
8. **[P2]** priority_weighted/strict/Shuffle 公平性(统计断言);cache demote(连 2 miss 清 bit)。

### 复杂度热点
- `default_selector.go:206` `tryLayer`(32 行:二次 gate + acquire + writeback + release + 错误分类全挤一处,每条 money-coupled)。
- `pasr.go:432` `acquireAndReturn`(pre/post-mutation 状态机最难,多个 HIGH/MEDIUM fix 叠加,"返 err 时表状态完全还原"极难证)。
- `default_selector.go:247/308/279` rankFresh+topK+weightedReservoir(等价前缀边界 + 两种打散 + randMu,公平性只能统计断言)。
- `default_selector.go:386` earliestPoolRecovery(时间型边界 + modelCooldownKey 回退)。
- `pasr.go:132` Select 段路径分叉(readOnly×段命中×全 unhealthy 接力×SessionHash 空退化×Excluded 五维交叉)。
- `dispatcher.go:210` + `retry.go:12`(mode × PASR 错误类型决定 fallback/fail-closed,PASR 安全最后一道闸)。

---

## 链路④ 配额/限流(quota 多窗口 reserve/settle + budget rpm/tpm 双闸)

> 装配分层 `cmd/gateway/wiring.go:429-443`:reserve 侧 budget 套在 quota 外(**budget 先判、quota 后判**);settle 侧 budget→quota→billing 由外向内包(最外层 budget settler 先跑)。`quota/budget/budgetenforce/quotaenforce` **不在碰撞写面**(可自由修)。

### 流程骨架(真码)
- **热路径入口** `chat_completions_dispatch.go:518` `reserveQuota`:billing.Reserve 出 ClaimID 后 `BuildReserveRequest` 喂 ClaimID/RequestFingerprint/ReservedTokens/PredictedCost → `QuotaReserver.Reserve`(实为 budget 外层);denied→Settler.Abort billing claim + 429;infra error→fail-open 放行 + WARN。
- **budget 子闸(rpm/tpm,先判)** `budgetenforce/enforce.go:38`:budget.Reserve → 拒则构造 quota.Decision(Counter==TPM→MetricTokensEstimated)回 DenyError;infra err→fail-open;放行后才调内层 quota;quota 反而 deny→回滚 budget 预留。`budget/service.go:183` reserveWithStore 逐 scope CheckAndIncrement(任一拒→逆序退已加)。窗口=分钟定窗(memory_store 或 redis Lua 原子)。
- **quota 子闸(多窗口,后判)** `quota/service.go:68` Reserve(Serializable,40001/40P01 重试 3 次):① `GetReservationByClaimForUpdate` 幂等(重放异身份→replay_conflict;released/expired→reactivate;reserved→reused;settled→settled_replay)② `ResolvePolicies`(`policy.go:33` SQL FOR UPDATE + 内存防御过滤 + 按 scopeLockOrder→metric→priority→id 稳定排序防死锁 + 解析窗口)③ `evaluatePolicies`→`assessPolicy`(current=Reserved+Settled,exceeded=current+amount>limit;Enforce 超→deny / Observe/ManualFirst 超→只记审计)④ deny→审计返回;通过→InsertReservation(含 PolicySnapshot 固化窗口边界)⑤ `applyEnforceReservations(:311)` 真占用(`IncrementWindowReserved` SQL 原子 `WHERE reserved+settled+delta<=limit`;ErrNoRows→rollbackDenyError 整笔回滚;Concurrency→AcquireConcurrencySlot)。
- **拒绝 429 构造** `quotaenforce/settler.go:105` DenyRetryAfter/DenyWindowKind → `chat_completions_error.go:44`(429 + Retry-After 秒 ceil + window_resets_at RFC3339 + windowKind 非空加 quota_window)。
- **settle/release/cache-hit** `quotaenforce/settler.go:141`:inner billing.Settle 提交后 quota.Settle 作 **post-commit 次级补账**(失败 fail-open);`service_settle.go:79` 三 finalizer 按当前状态分支幂等(applySettlementWindows 释放 Reserved/累加 Settled;marginalBudgetOverage 算边际超额)。失败→enqueueFinalizationReconciliation。`reconciler.go:78` 后台消费(settle 无真 actual_cost 用 PredictedCost 保守代理,指数退避 maxAttempts=10)。

### 三家对照
- **sub2api**:订阅多窗口 USD(daily/weekly/monthly)+ 速率窗口,但是 **post-hoc check**(读缓存用量→比较→放行/拒→异步累加);**有意接受有界 TOCTOU 超支**(超支压在并发 in-flight 内而非随时间无限累积)。是 HUAKAI 多窗口语义来源,HUAKAI 升级为 reserve/settle 账本强一致。
- **new-api**:配额=单一累计整数余额(无窗口);速率=独立中间件(Redis LIST 滑窗)。额度与频率完全分两套各单维。
- **CLIProxyAPI**:无消费者侧配额(只反射上游厂商 429/Retry-After 作账号冷却)。no-equivalent。

### 必守不变量
**① enforce 超限必拒 + SQL 原子上限二次把关**(评估与占用间 TOCTOU,只有 SQL WHERE 上限真防越限)**② observe/ManualFirst 只审计不拒**(运营开关核心)**③ fail 语义分层**(入口 fail-closed / 热路径 fail-open 不扣用量 / post-commit fail-open+对账)**④ reserve/settle 守恒**(幂等命中不重复)**⑤ 窗口边界 UTC + PolicySnapshot 跨窗一致**(settle 不重算到新窗漏退旧窗)**⑥ budget 拒→不留 quota 占用,budget 过 quota 拒→必 Release budget ⑦ 拒绝时不留占用 ⑧ 锁顺序 scopeLockOrder 一致 ⑨ none/manual 的 RetryAfter 返 0**(不吐天文数字)。

### 测试必覆盖分支(P0 优先)
1. **[P0]** 多 policy 命中顺序/优先级 → 最严先命中拒,Decision.Scope/Metric/WindowKind 指向真正命中那条。
2. **[P0]** 窗口翻转(day/week 周一/month 跨年/fixed floorUnix)→ 新窗从 0、reserve 旧窗 settle 新窗靠 PolicySnapshot 退旧窗(否则旧窗 reserved 永久泄漏)。
3. **[P0]** observe vs enforce 自证:同超限输入 observe Allowed=true / enforce Allowed=false。
4. **[P0]** fail-open/fail-closed 矩阵(注入真错误断言放行/拒绝方向,绝不把 infra 抖动变误拒或 fail-open 误扣)。
5. **[P1]** budget 与 quota 叠加 + 顺序(budget 拒不调 quota;budget 过 quota 拒必 Release budget)。
6. **[P1]** 中途断开 release 释放 ReservedValue + 并发槽不留占用。
7. **[P1]** 并发竞争/lease 丢失(同 claim 并发 reserve→复用;抢同窗最后一格 SQL 原子只一个成功)。
8. **[P1]** 重放冲突(同 claim 异 fingerprint→replay_conflict 不复用旧 reservation)。
9. **[P1]** settle 幂等与非法迁移(Settled 再 settle→IdempotencyHit;Released 再 settle→ReconciliationNeeded)。
10. **[P2]** ReservedTokens<=0 token policy 跳过(防死配额)。

### 复杂度热点
- `service.go:311-405` `applyEnforceReservations`(评估读与占用写之间 TOCTOU,只有这里 SQL WHERE 上限真防越限,任一分支漏回滚=越限/泄漏)。
- `service_settle.go:394-447` + `:593-599`(按 PolicySnapshot 精确释放/累加 + 边际 overage,跨窗/缓存命中/预测>实际组合最易错)。
- `service.go:68-194` Reserve 幂等×重试×回滚三重嵌套(错误分类决定重试/拒绝/复用,归类错=死循环或误放行)。
- `budgetenforce/enforce.go:38-60` budget↔quota 跨子系统编排(两套幂等锚底层存储不同,竞态最易漏退)。
- `policy.go:33-98` + `service_assess.go:25-91`(多 scope×metric×window×三 Mode 笛卡尔,排序错位=该拒没拒)。
- `rate_window.go:13-46` ComputeWindow(周一为界/月跨年/fixed 负数取模差一高发;manual/none 9999 哨兵 retryAfter 必返 0)。

---

## 链路⑤ 流式 + 协议转换(SSE 切帧→provider→canonical→client + usage 捕获)

> ⚠️ `gateway/*` 与 `gatewayhttp/*` 在碰撞写面;`proto/*` 与 `protosse/*` **不在**(可自由修)。

### 流程骨架(真码)
- **A 入口装配** `chat_completions_stream.go:126` → `streamForwarder.Forward(:265)`(forwardReq 必带 ProtocolFamily)。`forwarder.go:103` 校验四件套(ProtocolAdapters/Scanners 非 nil **fail-loud 禁 fallback 把 binary 切碎** / ProtocolFamily 非空 / 按 family 解析 adapter+scanner,都不 fallback)。
- **B wire 切帧** `forwarder.go:153` `go scanInto`;scanner 注册 `stream_scanner.go:175`(32 family→SSE / bedrock_invoke→binary EventStream / ollama_native→NDJSON);`event_scanner.go:27` 有界缓冲(默认 1MB,上限 64MB,超→ErrScannerOverflow)。
- **C 主循环** `forwarder.go:178` for-select 6 路(ctx.Done/totalTimer/firstTimer/interTimer/keepaliveTimer 心跳/events)。
- **D 单事件** `forwarder.go:307` `handleEventWithAdapter`:error 帧→脱敏日志 + 按 ClientProtocol 合成 terminalErrorFrame;正常→`ProviderEventToCanonicalEvents`(providerLosses 必在 err 早返前累积进 acc)→ 逐 canonical:usage→`acc.Update(Reported)` / 估算累加(visible+reasoning 分开)/ terminal→`acc.Freeze()` / `clientChunks`→`CanonicalEventToClientChunk`→writeAndFlush。
- **E adapter 内 usage 累计**:Anthropic `anthropic/sse.go:188` message_start 存 usage + `:225` message_delta `mergeUsage`(非零才覆盖保住 input/cache);OpenAI `:482` 整段替换(只末 chunk 给 usage 无零覆盖风险);Gemini 同。
- **F 累加器合并** `forwarder_types.go:198` Update:TerminalLocked 丢弃;token 非零才覆盖/取最新;tool-call 计数 +=;TotalTokens 缺则回填。
- **G 截断收口**:客户端断开→EndClass=ClientDisconnect + `drainWithAdapter(:426)` 有界 drain(MaxSeconds/MaxBytes/MaxEstimatedCost 三护栏,drain 期 usage 用 Partial 更新)；上游 EOF 无 terminal→EndClass=UpstreamEOFNoTerminal + PendingReconciliation + Inferred + `emitFinalUpstreamEvents`；已提交 200 后出错→补 terminalErrorFrame(不发 [DONE])。
- **H 缓冲 SSE 兜底** `protosse/reconstruct.go:22`(非流式但上游回 SSE):折叠成 CanonicalResponse,message_delta usage 用 `mergeNonZeroUsage(:248,本会话审计修复)` + total 取 max(stale, input+output)。
- **I 计费交接** `chat_completions_stream.go:680` streamingCompletionEvent:缺 usage→DeliveredTokenCount 绝不当 token(走 estimatedStreamingCost inferred)；crossCheckAudit 审计-only;`mergeProtocolLossWithEntries` 把流式逐事件 loss 合进 SettleRequest(本会话审计修复点)。

### 三家对照
- **sub2api**:passthrough relay(上游字节几乎原样转发),usage 是 sideband gjson 解析;message_delta "v>0 才覆盖"逐字段合并(与 HUAKAI 同不变量);主 Anthropic→Anthropic 不转换。
- **new-api**:`StreamResponseClaude2OpenAI` 单函数真 N→1 跨协议转换;message_delta 缺字段时用 message_start 持久化的 usage 回填(同不变量,有专门 patch 测试守);ResponseText2Usage 文本估算兜底。
- **CLIProxyAPI**:N×N 翻译矩阵;有状态累加器 `.Merge` 每字段 `.Exists()` 守(与 HUAKAI mergeNonZeroUsage / sub2api set-if-nonzero / new-api patch **完全同构**的不抹零不变量);无计费故无 settle/crossCheck/Delivered。

### 必守不变量
**① usage 不抹零**(message_delta 只带 output 不覆盖 input/cache,三路径:活流 Update / anthropic mergeUsage / 缓冲 mergeNonZeroUsage)**② cache token 并列维度不二次扣减**(creation 5m/1h 不进 TotalTokens)**③ tool-call 计数累加而 token 取最新**(只 server_tool_use 计费)**④ ReasoningTokens 已含 OutputTokens 内不单独计费 ⑤ DeliveredChunkCount 是帧数不当 token 计费**(缺 usage 只作 inferred 弱信号)**⑥ 每种结束映射明确 EndClass**(UpstreamEOFNoTerminal 必挂 PendingReconciliation)**⑦ 已提交 200 后出错必按 ClientProtocol 发正确终止帧**(不发 [DONE])**⑧ 协议损失不静默丢且在 err 前累积进 acc 合进 settle ⑨ tool call_id 缺失合成+记 loss 不丢空串 ⑩ 缓冲重组陈旧 total 取 max ⑪ scanner 有界 64MB ⑫ binary 流不静默走 SSE**。

### 测试必覆盖分支(P0 优先)
1. **[P0]** message_delta 合并不抹零(message_start input=1000/cache_read=5000 → message_delta 只带 output=50 → 最终仍 input=1000/cache=5000)。覆盖三路径,**broken 代码产 input=0 必 RED**。
2. **[P0]** 中途客户端断开 + drain(EndClass=ClientDisconnect + drain 期 usage Partial 累加,测三护栏各退出)。
3. **[P0]** 上游 EOF 无 terminal → PendingReconciliation + Inferred + emitFinal 合成 stop。
4. **[P0]** DeliveredChunkCount 不计费(缺 usage 有交付帧 → 走估算/pending 绝不按帧×单价)。
5. **[P1]** cache token 5m/1h 拆分 + 不二次扣减。
6. **[P1]** 非法/未知事件(loss 在 err 前累积 + 不 panic 不污染后续)。
7. **[P1]** 跨协议字段映射(Claude thinking→OpenAI reasoning_content 或记 loss;tool 参数估算不双算)。
8. **[P1]** 已提交 200 后出错补**协议正确**终止帧(openai 裸 data: error 不带 [DONE] / anthropic event:error / gemini / responses)。
9. **[P2]** scanner overflow 64MB;bedrock binary exception 帧;Freeze 后迟到 usage 被拒;reasoning folding 不可知跳过校验。

### 复杂度热点
- `forwarder.go:178-286` 主 for-select(6 路定时器 + 终态收口 + 补帧,状态变量交织;补帧条件 `(keepaliveCommitted||firstEmitted)&&!terminalSeen&&!terminalFrameWritten` 极易因新分支漏改双写/漏发)。
- `forwarder.go:307-401` handleEventWithAdapter(单事件串 error 帧 + 解析 + usage 捕获 + 双重估算 + Freeze + 转换 + 写 socket + loss 双段累积,顺序错即丢证据)。
- `protosse/reconstruct.go:182-281`(折叠状态机 + mergeNonZeroUsage + 陈旧 total 重算 + content-before-message_start 判废,usage 抹零 bug 就藏这)。
- `anthropic/sse.go:558-589` mergeUsage(三参合并双层逻辑,边界只 a/只 b/都有/都零极易漏)。
- `forwarder_types.go:198-238` Update(token set-to-latest 与 tool-call additive 混一个方法)。
- `chat_completions_stream.go:680-775` streamingCompletionEvent(缺 usage→估算→inferred→pending 多层 if + no-usage 定稿 SQL 只认全零的隐式约束)。

---

## 测试地基策略(怎么测才不"很麻烦")

### 分层
1. **单元(纯逻辑)** —— 不变量里跟 DB/网络无关的判定(merge usage、ComputeWindow、shouldRetry 双通道、topK 等价前缀、状态机迁移、Blend 打分)优先做表驱动单元测,快、稳、易 mutation。
2. **integration_pg(`//go:build integration_pg` + dev DB)** —— 一切涉及 Serializable Tx / FOR UPDATE / 唯一约束 / 跨表守恒(claim 单翻转、退款累计上限、slot in_flight 守恒、quota 窗口翻转、reserve/settle 守恒)必须真 PG 测,内存 stub 测不出锁/约束语义。dev DB:`postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable`。
3. **对抗审查** —— 核心 money/路由切片落地后并行多 agent 对抗审查零 S0/S1 + 干净基线(变异 agent 会污染 live worktree+dev DB,审后必核 `git status` 干净 + 重跑 fail-0 + 清 DB 残留)。

### 优先级排序(价值 = money-coupling × 复杂度 × 三家无对照)
**第一梯队(money-coupled + 三家无等价 + 反复出过 bug)**:
- 链路②全部(Tx1/Tx2 守恒、退款幂等、DLQ 重放、脱钩 ctx)—— 最高价值,且包不在碰撞面可自由修。
- 链路⑤ usage 不抹零三路径 + DeliveredChunkCount 不计费 + drain 期 usage(proto/protosse 不在碰撞面)。
- 链路④ 窗口翻转 PolicySnapshot 跨窗一致 + budget↔quota 回滚 + observe/enforce 切换(不在碰撞面)。

**第二梯队(路由正确性,但在碰撞写面需独立 PR)**:
- 链路③ claim 竞争 bubble + PASR post-mutation release + sticky miss 剥链。
- 链路① attempt 双子预算边界 + 已交付不可逆(gatewayhttp 在碰撞面)。

### mutation 清单(每个 P0 必须"注入缺陷→RED"才算强测试)
每条 P0 旁已标"mutation:..."的就是把缺陷重新注入后测试必须变红的判据。**没有 mutation 证 RED 的测试一律当草案**(非歧视性测试无价值)。重点 mutation:
- ② 去 `WithoutCancel` → 断连不计费应 RED;退款锁移到幂等查后 → 并发双退应 RED;UpdateClaimCommitted 去 `WHERE status='reserving'` → 双 settle 应 RED。
- ⑤ mergeUsage/mergeNonZeroUsage 改成整段覆盖 → input 抹 0 应 RED;DeliveredChunkCount 当 token 计费 → 帧数多收应 RED。
- ③ `claim_gate.go:48` ErrClaimRace 改返 nil → 双 claim 写应 RED;PASR release 换回原 ctx → ctx-cancel 下 slot 泄漏应 RED。
- ④ IncrementWindowReserved 去 SQL 上限 WHERE → 越限应 RED;ComputeWindow 月进位改错 → 跨年窗口应 RED。
- ① shouldRetryAttemptFailure 末位 401 不额外换号 / authFailoverUsed 不置位 → 401 被吞或无限换号应 RED。

### 落地节奏(配合 loop cadence)
每个测试/修复切片走全 cadence:worktree(base origin/feat/frontend-portal 最新)→ #16 真读三家对照(本文已备料)→ plan → build/vet(`GOFLAGS=-mod=mod`,codebudget≤600)→ integration_pg 变异测试(`-count=1`)→ 对抗审查零 S0/S1 + 干净基线 → commit(中文)→ PR(base feat/frontend-portal)→ squash → ff 清理。碰撞面(③①)切片纯 additive 独立 worktree/PR。

---

## 附:本文与其它过程文档的关系
- 链路级"逻辑链路"理解 → 本文(测试地图 + 不变量 + 热点)。
- 字段级 parity 缺口 → `docs/process/plans/` 各 parity 计划。
- 已修审计阻断项 → 记忆 `audit-blockers-2026-06-19-wy94u3tn9`(9 个 S0/S1 全闭合)。
- 功能树后端闭环 → `docs/process/feature-tree/feature-tree.json`。
