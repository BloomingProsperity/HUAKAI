# queue_wait 排队执行层补建——Claude 草案(2026-07-05)

## 背景与拍板依据(三镜调研,证据存 tasks/a008e81ddf36b402e + a7af07b2d44462df2)

**HUAKAI 现状(亲核)**:池并发槽全满时 `fallbackPlan`(pool/router/default_selector.go:344)按 schema 配置(0001 迁移:sticky_wait_timeout_ms=5000/fallback_wait_timeout_ms=30000/sticky_wait_max_waiting=2/fallback_wait_max_waiting=8,binding 级还有 override 列)产出 `WaitPlan{AccountID,TimeoutMS,MaxWaiting}`——但 **TimeoutMS/MaxWaiting 在 router 之外零消费者**。dispatch(chat_completions_dispatch.go:497)拿到 WaitPlan 直接 abort claim + retryable 429 queue_wait,attempt 循环无间隔立即重试(UpstreamRateLimit 在 retryablePreDeliveryEndClasses,default_router.go:35),撞同一满窗,每 attempt 一条 claim_aborted 审计,最终 429+Retry-After。**排队计划算好了,没人真排队**——与 TTFT 断链同款"设施半截"缺陷。

**sub2api(87dfc66,§16 默认对齐)**:池满≠错误,请求进等待队列阻塞轮询等槽(100ms→2s 指数退避±20% jitter,gateway_helper.go:291/378/386-403),流式等待期发 SSE ping 保活;队列容量=并发数+20,溢出立即 429"Too many pending requests";等待超时(fallback 30s/sticky 45-120s)429"Concurrency limit exceeded";全程无 Retry-After。彻底无账号(全禁用/配额尽)才直接 503。

**new-api(8874d19)**:无排队,默认不重试(RetryTimes=0),重试=优先级层下移换渠道无退避;入口无渠道 503、途中耗尽 500;全程无 Retry-After。

**裁定**:对齐 sub2api 补建等待执行层(HUAKAI 自己的 schema 从 0001 起就是这个设计意图,现在的立即重试是执行层缺失的退化行为);**保留 HUAKAI 的 Retry-After 头**(两镜都没有,生态升级 delta);**v1 不做等待期 SSE ping**(我们默认等待 5s/30s 远短于 sub2api 的 120s,且 ping 需先写 200+SSE 头,会破坏"预交付失败返回 JSON 429"的错误语义——刻意 delta,记录之)。

## 设计

1. **等待执行点**:dispatch 的 `selRes.WaitPlan != nil` 分支。不再立即 abort,而是:
   a. 入队守卫:per-account 在制等待 gauge(进程内计数器,单二进制部署无需 Redis;镜像 SessionCapRegistry 模式)。`gauge[WaitPlan.AccountID] >= MaxWaiting` → 立即走现行 429+Retry-After(溢出语义,不等待)。
   b. 等待循环:`ctx = WithTimeout(reqCtx, min(TimeoutMS, 剩余请求预算))`;退避轮询(100ms 起 ×1.5 封顶 2s ±20% jitter)**重跑完整池选号**(而非钉死 WaitPlan.AccountID 抢槽——重选拿最新健康/优先级视图,别的账号先空出来也能上;这是对 sub2api 钉账号等槽的算法升级 delta)。选到号 → 正常继续。
   c. 超时 → 走现行 abort+retryable 429 queue_wait(**语义不变**:attempt 循环仍可换池/model-fallback,RetryableEndClasses 白名单零改动,爆炸半径=纯增量)。
   d. gauge 严格 defer 递减;等待期间 claim 保持 reserving(fallback 30s << lease TTL 120s,不会被 LeaseSweeper 误扫,需测试坐实)。
2. **零新旋钮**:激活既有 schema 列(死配置复活);pool 配置 timeout=0 且 max_waiting=0 时 fallbackPlan 本就返回 nil=保持现行为,per-pool 配置即开关。
3. **代码位置**:新文件 internal/gatewayhttp/chat_completions_queue_wait.go(codebudget:dispatch.go 已近预算);gauge 可放 internal/pool 或 gatewayhttp,倾向 gatewayhttp(消费侧)。

## 测试(§17 重测+必测并发)

- e2e_concurrency 扩展:cap=1+等待配置:N 并发,断言等待者在槽释放后**真成功**(而非 429)、在途峰值≤cap、无槽泄漏、hold 只 released 一次;
- 溢出:waiting>MaxWaiting 的请求立即 429+Retry-After(不等待,时延断言<退避初值);
- 超时:mock 上游延迟>wait timeout → 429+Retry-After,claim 干净 abort,gauge 归零;
- 等待期间客户端断连:gauge 归还、claim abort 走 detachedAbort 不泄漏;
- 变异:删 gauge 递减→泄漏测试红;删超时→测试红;删重选→等待者永不成功红。

## 风险

- 等待占住 HTTP worker goroutine(Go 每连接 goroutine,30s 等待×MaxWaiting=8×账号数,可控);
- 等待期间 reservation 占配额窗口(本就是预扣语义,超时 abort 归还,与现状一致);
- 行为变化:满池请求从"立即 429"变"最多等 30s"——这正是 schema 设计意图与 sub2api 成熟语义,已按 Owner 授权拍板。

---

## 双轨交叉综合裁定(2026-07-05,Claude PM)

**采纳 codex 轨(2026-07-05-queue-wait-executor-codex.md)为实现蓝本**,其三处优于本轨:①等待期钉住 WaitPlan.AccountID(复用 PinnedAccountID gate,gates.go:351)而非重跑完整选号——per-account MaxWaiting 队列语义只有钉住才自洽,且账号中途被 gate 排除时 selector 返回非 WaitPlan 错误自然退出不盲等;②Tracker 键 {tenant_id, pool_group_id, account_id};③selector_error 区分透出不伪装成 queue_wait。本轨的镜像对照与两个 delta(保留 Retry-After / v1 不做等待期 SSE ping)作为产品语义输入并入。

**codex 三个待确认点按会话授权裁定**:①lease 事实以真码为准(slot 90s/claim 30min,本轨 prompt 里的 120s 是过时记忆),实现只需保证等待预算(默认 30s)不逼近任何活跃 lease,不动常量;②v1 MaxWaiting 进程内计数接受(单二进制默认部署形态;多副本全局严格上限=follow-up 另开计划);③本切片只落 chat 主链,queuewait 做可复用包,embeddings/completions/image/audio/rerank 等端点后续批按同模式对齐。

实现按 codex 计划「先单测红点→实现→e2e 改造」次序执行;既有 TestAccountSlotConcurrencyE2E 的"5 个全 429"断言按计划拆成等待成功/溢出/超时/断连四场景。
