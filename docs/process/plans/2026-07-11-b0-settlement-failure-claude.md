# B0 结算失败四缺口修复 — 实施计划(Claude 独立车道)— 2026-07-11

> 本文件为 Claude/PM 独立起草的实施计划,与 codex 车道并行(规则 #10)。撰写时**未读取** codex 车道计划,
> 独立结论。证据全部来自 HUAKAI 内部当前分支代码 / SQL / 测试(file:line 见正文),implementer clean-room:
> **未读取** `/home/ubuntu/refs` 或任何参考项目源码。

## Owner directive(依据)

- 定稿方案见 `docs/process/reviews/2026-07-10-B0-settlement-failure-design.md` §「2026-07-11 Owner 裁决 + 官方计费模型调研 → 定稿方案」。
- Owner 裁决原话:「结算兜底这个东西,你看一下官方是怎么做的……我们就按官方那个来好了呀。」
- 定稿一条原则:**「先交付、后按权威用量计量、已交付永不反悔」**。
- 本车道被授权实施四个缺口,且**明确约束**:不改 schema / 迁移、不动 Reserve/hold、不动 usage/actualCost 计量口径、
  不动 SSRF/auth、**不建第二持久环**(官方也没有第二环,D4「只 alert 不 disk spool」维持不推翻)。

## REFERENCE PROJECTS IN SCOPE

- **CLIProxyAPI** — 纯 relay account→API,**无计费/订单/结算模块**,结算失败补偿无等价物(定稿已 cite:`manager.go:304-311` fire-and-forget)。
- **sub2api** — 后扣制;同步主计费路径失败**仅日志、不重试、白吃全额**;唯一完整持久补偿环在异步批量图片(定稿已 cite:`gateway_handler.go:539-548` / `batch_image_settlement.go` / `batch_image_billing_recovery.go`)。
- **new-api** — 预扣制;差额结算失败仅日志但主体已锁,失败只丢差额;三态守卫防双退(定稿已 cite:`text_quota.go:387-389` / `billing_session.go:82-145`)。

> 三镜的运行逻辑调研与失败补偿逐行复核**由定稿设计 `2026-07-10-B0-settlement-failure-design.md` 承接**
> (specifier lane 已在 sub2api@12d811bd / new-api@246d62aa / CLIProxyAPI@26d45fd4 完成)。**本 implementer 车道
> 不重复读源码**(规则 #11 lane 隔离:同一 artifact 上 specifier 与 implementer 必须不同 session;定稿已是 specifier 产出)。
> 三镜结论(改写取舍依据):三家同步主链路失败补偿都只有日志、无第二持久环——HUAKAI 在现有 schema 内做「已交付永不反悔 +
> post-delivery 恢复 + sweeper 排除 + 封顶退避」= 补三家共同短板,非抄某一家;图片子域照 sub2api「预冻结-捕获-释放」范本的
> 语义(本次仅复用现有三证 recovery worker,不新建冻结环)。

---

## 一、范围内(In-Scope)

四个缺口,均在**现有 schema 内**、money-path 高风险、按安全网(对抗审 0 S0/S1 + 变异证 + clean baseline)实施:

1. **缺口1 非流式** — chat 非流式**先完整写响应体给客户端 → 再 settle**;settle 失败入 post-delivery 恢复(用户已拿内容,补偿方向正确);**零/部分写失败才 Abort**。
   - 触点:`gatewayhttp/chat_completions_billing.go:79-171` `executeNonStreamingAttempt`(现 settle 在 `:129`,写体在 `handleNonStreamingResponse`→`writeAttemptSuccess`,即 **settle 先于写**)。
2. **缺口2 流式** — 已交付业务帧的流**永不 Abort**;ledger+DLQ 双失败 → 审计 + 结算 bundle recovery,trailer 标 deferred/recovery,**不把已交付流写成 failed**;**零帧交付仍 Abort**。
   - 触点:`gatewayhttp/chat_completions_stream.go:299-367` `forwardSSEAndSettle`(现 `ledgerFailClosed`(`:315-320`)强制 `StreamStateFailed` 并走 else 分支 `:346-350` Abort;`:325-327` 的 `settle` 判定已含「DeliveredTokenCount>0」但被 `!ledgerFailClosed` 门控挡掉)。
3. **缺口3 双失败兜底(PG 环内止血,不建第二环)** —
   - a) claim lease sweeper **排除所有未解决 `post_delivery_settlement` DLQ 行**对应的 claim(杜绝「已交付被零成本 Abort」);
   - b) `post_delivery_settlement` 事件种类**封顶退避持续重试**(不因 MaxAttempts/DLQAfter 转终态停);
   - c) 增加 **`delivered_unsettled_age`** 观测(告警未决已交付结算的滞留时长)。
   - 触点:`billing/lease_sweep.go:110-145` + `sql/queries/balance_holds.sql:81-88` `SelectExpiredReservingClaims`;`dlq/retry.go` `NextFailure`;`dlq/service.go:162-167` 策略入口;新增观测文件。
4. **缺口4 图片** — 与缺口1同构:图片**先完整写业务响应 → 再 settle**;settle 失败入恢复(**新 `SourceImagesDelivered`**,复用现有三证 recovery worker);**零/部分写失败立即 Abort**。
   - 触点:`imageshttp/attempt.go:129-177` `settleSuccessfulResponse`(现 settle 在 `:166`,写体在 `:174-175`,即 settle 先于写;失败 `:167` 返 500 **不调 abort**)。abort helper 已存在:`imageshttp/billing.go:205-219`。

## 二、范围外(Out-of-Scope,硬边界)

- **不改 schema / 不写迁移**:缺口3 sweeper 排除靠现有 `usage_record_dlq.claim_id`(已存在列,`0015_obs_dlq_extend.up.sql:16-63` 放宽为 nullable + `source_id=COALESCE(source_id,claim_id)`)+ `event_kind` + `status`,**无需新列/新表/新索引**(若为性能加索引=schema,须 Owner 门,本计划默认不加,见风险 R7)。
- **不建第二持久环 / 不新增外部队列 / 不推翻 D4**:终极双失败仍 = ERROR 告警(维持 Owner D4)。
- **不动 Reserve/hold 准入闸**(`dispatch.go:342`)、不动 Capture 权威用量扣费口径(`balancehold.go:111`)、不动 usage/actualCost/proto 解析。
- **不动 SSRF/auth/身份派生**。
- **不动 `Sidebar.tsx`**(历史条款;虽已解冻仍不在本切片范围)。

## 三、成功标准(Success Criteria)

1. 四缺口修复后,**「已交付内容的 claim 在任何结算/记账失败下都不会被零成本 Abort / 释放 hold」**——用注入式故障测试证明(见 §十 判别测试 A–G)。
2. **零/部分交付仍 Abort**——保留现有正确的零帧 abort 语义(不因修复引入「白放行」反向缺陷)。
3. 缺口3 sweeper 在有未决 `post_delivery_settlement` 行时**跳过**该 claim;`post_delivery_settlement` 事件**封顶退避持续重试**不转终态。
4. 全部新增测试通过**变异红点**验证(规则 #14):把修复逻辑还原成缺陷,对应测试必须变红。
5. 并发测试(规则 #17):per-key 并发 cap / 账号级并发槽 / claim lease sweep 竞态下,已交付 claim 不被误 abort、槽正确释放。
6. 门禁:`go build` / `go vet` / `codebudget` gate 绿;`go test -count=1` clean baseline;codex 逐提交审查零未结 S0/S1。
7. clean-room 无污染(未复制三镜代码/标识符/结构/测试)。

## 四、时间估算

| 阶段 | 估算 |
|---|---|
| 反转/新增测试(先写,锁定预期终局) | 2.0h |
| 缺口3(sweeper 排除 SQL + 封顶退避策略 + 观测) | 2.5h |
| 缺口2(已交付流禁 Abort + bundle recovery + trailer) | 2.5h |
| 缺口1(非流式写后结算 + 写错误捕获) | 2.0h |
| 缺口4(图片写后结算 + SourceImagesDelivered) | 1.5h |
| 并发 + 真故障注入测试 + 变异校验 | 2.5h |
| codebudget 拆分(若超预算) + 逐提交审查 + 修复 | 1.5h |
| **合计** | **~14.5h**(约 4–5 个 commit 切片) |

## 五、Blast Radius(影响面)

- **直接改动包**:`gatewayhttp`(chat 非流/流式)、`imageshttp`、`billing`(lease sweep)、`dlq`(retry 策略)、`settlementrecovery`(新 Source)、`sql/queries`(1 条 SELECT)+ 观测新文件。
- **money-path 全触**:所有 chat 非流式 / chat 流式 / 同步图片 family(dall-e / replicate 等)的成功响应结算路径。**任何回归都直接是钱账正确性问题**(白吃 / 冻钱 / 双扣 / hold 泄漏)。
- **跨模块配合面(§17)**:①结算↔lease sweeper(排除逻辑)②ledger-fail-closed↔money-path-audit-ref 守卫↔已交付禁 Abort(见 R3,关键交界)③DLQ retry 策略↔recovery worker↔claim 状态机(reserving/committed)④HTTP 写体结果↔结算触发时序(缺口1/4 的重排)。
- **不触面**:auth / SSRF / Reserve/hold 准入 / Capture 扣费口径 / schema。

## 六、失败模式及缓解(Failure Modes & Mitigations)

- **R1 缺口1/4 重排后「部分写」误判为交付** → 部分/零写却 settle=用户没拿全内容却被扣。缓解:捕获 `w.Write` 的 `(n, err)`,**仅当 err==nil 且 n==len(body)** 才视为交付走 settle-recovery;否则 Abort。判别测试 B/G。
- **R2 重排破坏 retry/failover**:非流式现架构「先 attempt 再 write」是为跨 attempt 重试;写体前置会让「写后不可重试」。缓解:settle 是成功响应的最后一步,此刻**重试已结束**(缺口1 只在最终成功 attempt 内重排,不影响 failover 逻辑);计划中明确 write 只在 `outcome.Success != nil` 的终态发生。判别测试 A。
- **R3【关键交界】ledger-fail-closed × money-path-audit-ref 守卫 × 已交付禁 Abort**:缺口2 已交付流遇 ledger append+DLQ 双失败时,recovery/settle 会走 `validateMoneyPathAuditRefForSource`(`chat_completions_billing.go:345/352`);此刻 `missingMoneyPathAuditRef`=true(无 LedgerID 也无 DLQRef),production enforce 下 `rejectMoneyPathDirectSettle`→`detachedAbort`(`:397`)**会 Abort 已交付 claim = 又一次白吃**。缓解:已交付路径的 recovery 入队**必须绕过 reject-and-abort**——落 DLQ 持久兜底 + ERROR 告警,**绝不 Abort 已交付 claim**;deferred trailer 表达未决。**此点需实现时专门守护 + 可能需 Owner 微决策(见决策点 D2)**。
- **R4 sweeper 排除子查询漏字段**:若 `post_delivery_settlement` DLQ 行的 `claim_id` 未落库,NOT EXISTS 会漏掉→仍误 Abort。缓解:已核实 enqueue 落 `ClaimID: p.Settle.ClaimID` 且 `SourceID` 同值(`settlementrecovery/enqueue.go:45,52`),`claim_id` 列可直接 join,无需 JSON 提取。判别测试 E。
- **R5 封顶退避持续重试 → DLQ 无限增长**:不转终态=行永不离开 pending。缓解:仅对 `post_delivery_settlement` 一种 kind 生效;配 `delivered_unsettled_age` 告警,达阈值 ERROR 通知运营人工介入(维持 D4「alert 不 spool」),不是静默无限堆积。见决策点 D3。
- **R6 committed claim 被盲目 Abort(缺口4 紧急止血分支)**:若图片只止血不重排,settle 失败盲目 Abort 可能 Abort 已 committed 的 claim=双扣/错放。缓解:**本计划采用完整重排(写后 settle),不采用盲目 Abort 分支**;recovery worker 已有三证 proof 区分 committed/未提交(`settlementrecovery/handler.go:67-84`),重放幂等。判别测试 G。
- **R7 sweeper 子查询性能**:每轮 sweep 对每个过期 claim 跑 NOT EXISTS。缓解:sweep 批量小(batch=100,30s tick)、`usage_record_dlq` 未决行量级低;暂不加索引(加索引=schema 门)。若压测显示慢,记 follow-up,不本切片加。
- **R8 反转测试掩盖真实回归**:反转 `stream_test:626` / `images handler_test:256` 若只改断言不改行为,变异测试会假绿。缓解:每个反转测试必须先证明**旧代码下新断言变红、新代码下变绿**(自证式)。判别测试 C/G。
- **R9 并发:lease sweep 与真实请求路径竞态**:sweep 选中 reserving claim 与请求路径推进出 reserving 并发。现有 `ErrClaimNotReserving` 良性跳过(`lease_sweep.go:129`)。缓解:排除子查询 + `FOR UPDATE SKIP LOCKED` 保持;并发测试覆盖「recovery 行在途 + lease 过期同时」。

## 七、Owner 决策点

### 无需 Owner 决策(定稿 2026-07-11 裁决已授权)
- 「完整业务体写成功才算交付 / 部分写与 keepalive 裸换行不算交付」政策 — 定稿 §6 已由 Owner 确认。
- 已交付流永不 Abort、非流式/图片写后结算、sweep 排除现有 recovery 行、按事件种类区分重试策略 — 定稿「可在现有 schema 内实现」清单已授权执行。
- 反转两个锁错终局测试(`stream_test:626`、`images handler_test:256`)— 定稿决策点已确认当前行为非有意,授权反转。

### 需 Owner 确认(建议 + 理由,均为 S2 级微决策,不阻塞主线)
- **D1 未解决状态定义**:sweeper 排除条件建议 = DLQ 行 `event_kind='post_delivery_settlement'` 且 `status <> 'delivered'`(即 pending/inflight/operator_review/dlq/**quarantined 均视为未解决、都不 Abort**)。理由:quarantined=毒消息待运营,Abort 它=零成本释放已交付 claim=白吃;交给运营手动决断更安全。**请 Owner 确认「quarantined 也排除」**。
- **D2 已交付流 recovery 的 audit-ref 放行**(见 R3):production enforce 下已交付流 ledger 双失败时,recovery 入队须绕过 money-path-audit-ref reject-and-abort。建议:**已交付=交付事实优先,落 DLQ + ERROR 告警,永不 Abort**;deferred trailer 表达。**请 Owner 确认「已交付内容的记账兜底可在缺 audit-ref 时仍入队而不 Abort」**(与「已交付永不反悔」一致,但触碰 audit-ref 强制策略边界)。
- **D3 封顶退避是否设 age 告警阈值 + 是否保留 operator_review 可见性**:建议 `post_delivery_settlement` 不转终态、封顶退避续跑,但达 `delivered_unsettled_age` 阈值发 ERROR 告警(不 spool,维持 D4)。**请 Owner 确认阈值语义**(建议默认 15min 首告警,之后周期性再告警)。

## 八、预执行清单(Pre-Exec Checklist)

- [ ] worktree off latest base + `.coordination/claim.sh` 认领代码文件(本计划文件已认领 `claude-b0-plan`,实现切片另起 agent 名认领代码文件)。
- [ ] 确认 `usage_record_dlq.claim_id` 在 post_delivery 行必落(已核实 `enqueue.go:45,52`)。
- [ ] 确认 `dbbilling.SelectExpiredReservingClaims` 由 sqlc 生成(改 `.sql` 后须 `sqlc generate`,生成码 `*.sql.go` 属排除项不手改)。
- [ ] 跑一次 `codebudget` gate 取当前基线,规划新增行落点(见 §九)。
- [ ] 先写/反转全部判别测试(§十),证明旧代码红、新代码绿,再动生产码。
- [ ] 选最便宜模型 + 压小 max_tokens 做真 PG 故障注入 + 并发压测(规则 #17)。

## 九、预计文件与包预算(Files & Code Budget)

> `codebudget`:单文件 ≤600 行、包 ≤6000 非测试行/≤20 文件;grandfathered 违规按 baseline.json + 5% 允量。
> **风险**:`gatewayhttp` 是 grandfathered god-package,`chat_completions_billing.go` baseline=711、`chat_completions_stream.go` baseline=824,均已超 600 且吃允量。**新增逻辑优先落新文件/新子包,避免撑爆允量**(规则 #13)。

| 文件/包 | 变更 | 预算处置 |
|---|---|---|
| `gatewayhttp/chat_completions_billing.go` (711) | 缺口1 写后结算重排 | **净增最小化**;若超 5% 允量,把「写体+结算触发」helper 抽到**新文件** `chat_completions_settle_delivery.go`(同包)。 |
| `gatewayhttp/chat_completions_stream.go` (824) | 缺口2 已交付禁 Abort + deferred trailer | 净增最小化;deferred-recovery helper 可入上条新文件复用。 |
| `gatewayhttp`(包总量 + 文件数≤20) | 可能新增 1 文件 | **须核实包总非测试行 + 文件数**;超则拆子包(如 `gatewayhttp/settledelivery/`)。列为 pre-exec 硬检查。 |
| `imageshttp/attempt.go` (177) / `billing.go` (235) | 缺口4 写后结算 + 写错误捕获 | 远低于 600,直接改。 |
| `billing/lease_sweep.go` (165) | 缺口3 sweeper 传入排除后的查询结果 | 低,直接改;新增最小。 |
| `sql/queries/balance_holds.sql` | `SelectExpiredReservingClaims` 加 NOT EXISTS 排除 | 非 Go,不计预算;须 `sqlc generate`。 |
| `dlq/retry.go` (81) 或新增 | 缺口3 per-kind 封顶退避策略 | 建议**新增小文件** `dlq/policy_kinds.go` 承载 per-kind 覆盖,避免改 `retry.go` 通用逻辑语义。 |
| `dlq/service.go` | 按 event_kind 选策略 | 净增小。 |
| `settlementrecovery/payload.go` (209) | 缺口4 新 `SourceImagesDelivered` 常量 + `Validate` case | 低,直接改。 |
| 观测:新文件 `billing/delivered_unsettled_age.go`(或 obs 包) | 缺口3 `delivered_unsettled_age` gauge/告警 | **新文件**,budget 友好;落点包待定(倾向 `obs` 或 `billing`)。 |
| 测试文件 | 7 判别 + 并发 | 测试行不计包预算。 |

## 十、具体执行顺序(严格分 commit,每 commit 过门 + 逐提交审查)

**Commit 1 — 测试先行(锁定预期终局 + 反转两个锁错测试)**
- 新写判别测试 A–G(§十一)+ 并发测试骨架。
- 反转 `chat_completions_stream_test.go:626`(已交付+ledger双失败→aborts=0 + recovery enqueued + trailer=deferred,非 failed/非 abort)。
- 反转 `imageshttp/handler_test.go:256`(settle 失败但已写体→200+body+recovery+aborts=0)。
- 此 commit 测试**应红**(生产码未改),故作为 WIP 基线;或与对应生产切片同 commit 落地(推荐后者,保持 baseline 绿)。实操:测试与其对应生产修复**同 commit**,分 4 个功能 commit。

**Commit 2 — 缺口3(sweeper 排除 + 封顶退避 + 观测)**(先做,因它是「止血网」,后续三缺口的 recovery 行都依赖它不被误 Abort)
1. `balance_holds.sql`:`SelectExpiredReservingClaims` 加 `AND NOT EXISTS (SELECT 1 FROM usage_record_dlq d WHERE d.claim_id = billing_ledger_claims.id AND d.event_kind = 'post_delivery_settlement' AND d.status <> 'delivered')` → `sqlc generate`。
2. `dlq/policy_kinds.go`(新)+ `dlq/service.go`:`post_delivery_settlement` 用封顶退避、不转终态的策略。
3. `billing/delivered_unsettled_age.go`(新):观测未决已交付结算滞留时长 + 告警。
4. 测试 E、F + 并发(sweep 竞态)。

**Commit 3 — 缺口2(已交付流永不 Abort)**
1. `chat_completions_stream.go`:`ledgerFailClosed && delivered` 时**不走 abort 分支**,改走 recovery + deferred trailer;严守 R3(绕过 reject-and-abort)。零帧交付保持 Abort。
2. 测试 C(反转 626)、D(零帧仍 abort)+ 真故障注入(ledger append+DLQ 双失败 + 已交付)。

**Commit 4 — 缺口1(非流式写后结算)**
1. `chat_completions_billing.go`:重排为**先写完整 body(捕获 write err)→ 成功再 settle-recovery;写失败 Abort**。
2. 测试 A(settle 失败已写体→200+recovery+不 abort)、B(写失败→abort)。

**Commit 5 — 缺口4(图片写后结算)**
1. `settlementrecovery/payload.go`:加 `SourceImagesDelivered`。
2. `imageshttp/attempt.go` / `billing.go`:重排为写后 settle;失败经 `FromSettleRequest(SourceImagesDelivered,...)` 入 DLQ;写失败 Abort。
3. 测试 G(反转 256)+ 图片并发。

> 每 commit:`go build`/`vet`/`codebudget`/`go test -count=1` 绿 + `codex exec review --uncommitted` 零未结 S0/S1 才落。

## 十一、7 条判别测试与变异红点(规则 #14)

> 每条:一句话说清它守的回归 + 变异红点(把修复还原成缺陷,该测试必红)。判别 fixture 保证「坏代码产出 ≠ 期望」。

- **测试 A — 非流式:settle 失败但已完整写体 → 交付且不 Abort**
  - 断言:HTTP 200 + body==完整 clientBody;settler.settles==1(尝试);settler.aborts==0;post_delivery DLQ 入队 1 条(SourceDirectSettle)。
  - 变异红点:把 settle 移回写体**之前**(现状 `:129`)→ settle 失败返 500 无 body → 「body 完整」断言红。

- **测试 B — 非流式:写体失败(部分/零写)→ Abort,不 settle**
  - fixture:注入一个在写到一半返回 error 的 ResponseWriter(n<len(body))。
  - 断言:settler.aborts==1;不进 post_delivery recovery;不产生成功结算。
  - 变异红点:去掉 write err 检查、无条件 settle → aborts==0 → 红。

- **测试 C — 流式:已交付帧 + ledger append+DLQ 双失败 → 永不 Abort + recovery + deferred trailer**(反转 `stream_test:626`)
  - 断言:200 + body 含已交付内容;settler.aborts==0(**旧断言是 aborts==1 audit_ledger_error**);settle-recovery 入队(SourceStream);trailer `X-HUAKAI-Stream-State` == `deferred`/recovery,**非 `failed`**。
  - 变异红点:恢复旧 `ledgerFailClosed`→abort 分支 → aborts==1 → 「aborts==0」红;trailer 回 failed → 「deferred」断言红。

- **测试 D — 流式:零帧交付 + 不可计费 → 仍 Abort**(保护正确的零帧语义,防修复引入反向缺陷)
  - fixture:上游即刻 EOF,DeliveredTokenCount==0,非 AmbiguousUsage。
  - 断言:settler.aborts==1(reason 非空,如 `stream_no_billable_delivery`);无 recovery 入队。
  - 变异红点:把「零帧也不 abort」误扩到全部 → aborts==0、hold 泄漏 → 红。

- **测试 E — sweeper:claim 有未决 post_delivery_settlement DLQ 行 + lease 过期 → 不 Abort 该 claim**
  - fixture:插一条 reserving 且 lease 过期的 claim + 一条 `usage_record_dlq`(claim_id 同、event_kind=post_delivery_settlement、status='pending')。
  - 断言:`SelectExpiredReservingClaims` 结果**不含**该 claim;sweep 后 settler.aborts 不含该 claim。
  - 变异红点:移除 NOT EXISTS 排除 → 该 claim 被选中并 Abort → 「不含」断言红。
  - 补充判别:另插一条 status='delivered' 的 DLQ 行对应另一 claim → 该 claim **应**被选中(证明排除只针对未解决,不误伤已解决)。

- **测试 F — retry:post_delivery_settlement 达 MaxAttempts/DLQAfter → 仍 StatusPending(封顶退避),不转 OperatorReview**
  - fixture:previousAttempts >= DefaultRetryPolicy.MaxAttempts 或 now > firstFailureAt+DLQAfter,event_kind=post_delivery_settlement。
  - 断言:RetryDecision.Status==StatusPending 且 Delay==CapBackoff;**对照组**:同样条件下 usage_record kind → StatusOperatorReview(证明只对该 kind 改变)。
  - 变异红点:该 kind 仍走 DefaultRetryPolicy → 转 OperatorReview → 「StatusPending」红。

- **测试 G — 图片:settle 失败但已完整写响应 → 交付且不 Abort + recovery**(反转 `images handler_test:256`)
  - 断言:200 + body==上游 raw;settler.settles==1;settler.aborts==0(**旧断言 aborts==0 但 status==500 无 body**);post_delivery DLQ 入队(SourceImagesDelivered)。
  - 变异红点:恢复旧「settle 先于写、失败返 500 不 abort、不 recovery」→ 「200+body」红 且 「recovery 入队」红。

**并发测试(规则 #17,非上述 7 条,另列)**
- C1:N 个已交付 claim 同时进 post_delivery recovery + lease sweep 同时跑 → 断言无一被误 Abort、槽按结算释放(非仅靠 lease 过期)。
- C2:per-key 并发 cap / 账号级并发槽打满真实触发排队/拒绝 → 结算失败入 recovery 后并发槽仍能释放。

## 十二、专项风险说明(prompt 点名项)

- **header 已提交(committed-header)**:缺口1/4 重排为「先写 body」意味着一旦 `WriteHeader`+`Write` 开始,HTTP 状态码已定,**后续 settle 失败不能再改 5xx**——这正是「已交付永不反悔」的正确表达。测试 A/G 断言 200 已发出。风险:现非流式在 settle 后才 `WriteHeader`(`:160-168`),重排需保证 header(含 `WriteHuakaiHeaders`/cache/content-type)在写 body 前一次性写全,不遗漏。列为实现硬检查。
- **committed claim Abort 保护**:缺口4 若走「盲目 Abort」会误伤已 committed claim(R6)。本计划**不采用盲目 Abort**,采用完整重排 + recovery worker 三证幂等(`handler.go:67-84` 用 `ErrClaimNotReserving` + CommittedProof 区分 committed/未提交,committed 视 idempotent success 不重扣)。
- **未决状态表达**:靠现有 `usage_record_dlq.status`(pending/inflight/operator_review/dlq/quarantined/delivered)+ `event_kind='post_delivery_settlement'` + `claim_id` 列,**无需新 schema**;sweeper NOT EXISTS 以 `status<>'delivered'` 表达「未解决」(D1 待确认 quarantined 处置)。
- **持续重试**:`post_delivery_settlement` 专用封顶退避策略,达 MaxAttempts/DLQAfter **不转 OperatorReview 终态**,而是 `StatusPending` + `CapBackoff` 续跑;配 age 告警(D3)。范围仅此一 kind,其余 kind 行为不变(测试 F 对照组证明)。
- **观测风险**:`delivered_unsettled_age` 是**新增观测面**,非计费逻辑;须保证观测本身失败不影响结算路径(fire-and-forget + 独立 ctx);告警不含 prompt/响应/凭据(仅 tenant_id/claim_id/age 等脱敏元数据,遵守 secret-mask 硬规则)。

## 十三、clean-room 声明

- implementer lane;**未读取** `/home/ubuntu/refs` 或任何参考项目源码。三镜运行逻辑/失败补偿调研由定稿设计承接。
- 仅提取行为与失败策略(定稿已 paraphrase),未复制三镜代码/标识符/结构/测试。
- 本计划所述未来代码注释一律用中文(Go 生产码 + 测试);英文技术标识符保留。派任何 subagent 时显式要求「代码注释用中文、返回报告用中文」。

---

**门禁复述**:worktree(off latest base,claim lock)→ 定稿承接三镜 → 本计划 → Go build/vet/codebudget → 变异证 → 对抗审(0 S0/S1)→ clean baseline(`-count=1`)→ commit → push → PR → squash → ff main → surface Owner。
