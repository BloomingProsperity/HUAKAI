# B0 结算失败四终局——设计与 Owner 决策点 — 2026-07-10

**调研**:codex(auditor lane),§17 三镜对照(sub2api@e316ebf / new-api@262ab93 / CLIProxyAPI@8c2bf2c)+ HUAKAI@2c7452ee。
**裁定**:四条均 money-path 高风险;部分**需 Owner 决策**,且缺口3的正解**推翻 Owner D4(2026-05-24 "只 alert 不做 disk spool")**。故 B0 **surface 给 Owner,未擅自实现**。

## 2026-07-11 补充:三镜结算失败补偿逐行复核(Owner 要求"看三家怎么做")

specifier lane 复核最新 HEAD(sub2api@12d811bd / new-api@246d62aa / CLIProxyAPI@26d45fd4)。三家是三种计费形态:

- **sub2api = 后扣制**(先出响应,后异步扣费)。常规文本/同步图片扣费失败**仅日志、不重试、不回滚 → 白吃全额**(`gateway_handler.go:539-548` 只记 `record_usage_failed`);幂等靠去重表+原子事务(`usage_billing_repo.go:65-110`)。**唯一完整持久补偿环在异步批量图片**:预冻结→捕获→释放,失败按 retry_count≤5 重试,耗尽才释放冻结转 failed,另有后台清扫器扫卡死 job 释放冻结、释放再失败入重试队列,且释放前校验"确实冻结过"防幻影造币(`batch_image_settlement.go:185-250` + `batch_image_billing_recovery.go:26-103`)。
- **new-api = 预扣制**(转发前扣估算,响应后结差额)。差额结算失败**仅日志**(`text_quota.go:387-389`),但预扣主体金额保持已扣 → **失败只丢"实际-预估"差额,不白吃全额**;成功路径永不退款(退款只挂 error defer,`relay.go:170-179`);三态守卫 settled/refunded/fundingSettled 防双退(`billing_session.go:82-145`)。异步任务有任务表+轮询退还/差额,但每步资金调整失败仍仅日志(~1.5 层)。
- **CLIProxyAPI = 无计费**,结算失败补偿无等价物(用量记录 fire-and-forget,`manager.go:304-311` recover 吞掉)。

**关键结论(改写本设计的取舍依据)**:
1. **三镜的同步主计费路径失败补偿都只有日志、没有第二层持久环**——这是三家共同薄弱点。HUAKAI 缺口1/2/3 若建持久补偿环 = **领先三镜**(不是抄谁,是补三家共同短板)。
2. **缺口4(图片/异步)有最佳范本 = sub2api 批量图片的"预冻结-捕获-释放 + retry≤5 + 后台清扫 + 防幻影释放校验"**,HUAKAI 异步/图片结算应照它建。
3. **可直接借鉴 new-api 两点**:预扣把主体金额提前锁定(即使结算失败也只丢差额,不白吃全额)+ 结算/退款三态守卫防双退。
4. 我们现在是**后扣制(像 sub2api)**,所以缺口的白吃后果是"全额"而非"差额"——这放大了建持久补偿环的必要性。

**这份复核把 B0 从"抄某一家"变成"补三家共同短板 + 在图片子域照 sub2api 建"**,四条缺口的修复设计(下文)据此仍成立,且证明了缺口3第二持久环的正当性(三镜同步链路都缺,不是我们过度设计)。

## 2026-07-11 Owner 裁决 + 官方计费模型调研 → 定稿方案(取代上文"第二持久环"路线)

**Owner 裁决原话**:「结算兜底这个东西,你看一下官方是怎么做的。假如我们使用官方 API,请求发送到了官方,官方是怎么计费的,我们就按官方那个来好了呀。」

**官方计费模型(公开契约,非三镜)**:三家官方(Anthropic/OpenAI/Gemini)一律**纯后付费、按响应报告的实际用量计量、无 reservation/hold、已交付永不反悔**;流中断按断点前已生成 token 收,零输出不收。用量来源:Anthropic 流式权威 output_tokens 在 message_delta 累积;OpenAI 流式须 stream_options.include_usage(我们已在 streamusage.Inject 强制注入);Gemini usageMetadata。

**HUAKAI 现状核实(file:line 见调研)**:我们**最终计费口径已经是官方那套**——Capture 按上游权威 usage 实扣(balancehold.go:111),forwarder/proto 已正确解析三家权威用量并落 draft(forwarder.go:544 finishDraft / chat_completions_billing.go:120);Reserve/hold(dispatch.go:342)只是预付费准入闸+差额退回。**四个缺口全是「结算时序先于交付」和「记账兜底」问题,与用量来源无关。**

**定稿方案 = 「先交付、后按权威用量计量、已交付永不反悔」一条原则贯穿**:
1. **缺口1(非流式)**:缓冲完整响应体→先完整写给客户端→再 settle;settle 失败入 post-delivery 恢复(用户已拿内容,补偿方向正确);零/部分写失败才 Abort。
2. **缺口2(流式)**:已交付流**永不 Abort**;ledger+DLQ 双失败→审计+结算 bundle recovery,trailer 标 deferred,不把已交付流写成 failed。反转锁错终局的现有测试。
3. **缺口3(双失败兜底)**:**不建第二持久环**——官方也没有第二环,且 Owner D4(只 alert 不 disk spool)维持不推翻。做现有 schema 内的止血:sweeper 排除未解决 post-delivery 结算行(杜绝「已交付被零成本 abort」)、该事件种类封顶退避持续重试、加 delivered_unsettled_age 告警。终极双失败=ERROR 告警(维持 D4)。
4. **缺口4(图片)**:与缺口1同构——先完整写响应体→再 settle;失败入恢复(新 SourceImagesDelivered 复用现有 recovery worker);零/部分写失败立即 Abort。
5. **Reserve/hold 准入闸保留**(现状不动):官方能纯后付费是因为有信用卡事后追偿;我们客户是预充值,hold 是预付费模型的固有准入保护,且最终扣费口径不受影响(Capture 按权威用量)。去掉 hold=激进默认行为翻转,不做。
6. **交付定义**:完整业务体成功写出=交付(非流式/图片 json 全量写成功;流式=已发出的帧);部分写/零写≠交付→Abort。keepalive 裸换行不算交付。

**该方案不再触碰 D4、不新增 schema、不建外部队列**——比原"第二持久环"路线轻一个数量级,且语义与官方对齐。剩余实现均在现有 schema 内,按安全网(对抗审+零S0/S1+变异证)实施。

## 四条缺口 + 修复设计

### 缺口1 非流式 settle-before-write 误扣未交付
现状:`chat_completions_billing.go:128` 在写客户端响应前 `settleCompletionWithRecovery`;settle 失败返 500(无内容)但已入恢复队列→worker 重结算=用户没拿到内容却被扣。
三镜:sub2api/new-api 普通请求**先写响应再后结算**。
设计:非流式改成**完整写响应后再结算**,settle 失败入 post-delivery 恢复(用户已拿内容,补偿正确);零/部分写失败则 Abort。**依赖"完整业务体写成功才算交付"政策(Owner 确认)。**

### 缺口2 流式 ledger 双失败白吃
现状:流数据逐帧已交付,ledger 到流末提交;ledger+DLQ 双失败时 `chat_completions_stream.go:302` 把已交付流改 failed 并 **Abort**→用户已拿内容但 hold 释放=白吃。
设计:**已交付流永不 Abort**;ledger 失败进审计+结算 bundle recovery;trailer 显示 deferred/recovery 而非把真实已交付流写成 failed。**必须反转现有锁错终局测试 `chat_completions_stream_test.go:626`。**

### 缺口3 settle+recovery DLQ 双失败:sweep 不是补偿
现状:双失败只打 ERROR,无第二持久环。claim 30 分钟租约后 sweeper 零成本 Abort→已交付变永久未收费;slot orphan SQL 排除 reserving claim(不提前释放);quota sweep 跳过 reserving。**sweep 会把"可恢复未结算"关成"无法自动恢复的零成本终局"。**
三镜:三镜普通请求都无第二持久环;sub2api 批量图片有"持久状态+有限重试+耗尽释放"参考。
设计(分两层):
- **PG 环内立即修**(可在现有 schema):claim sweeper **排除所有未解决 post_delivery_settlement 行**;该事件种类专用重试(达告警阈值仍封顶退避继续,而非停);加 delivered_unsettled_age 等告警;recovery 完成才允许普通 orphan sweep。
- **第二失败域**(⚠️Owner 决策 + 推翻 D4):同库再建表不算第二环(库不可写两表同失败)。推荐 `EmergencySettlementJournal` 独立持久队列(payload 复用脱敏 DTO+幂等键,不存 prompt/响应/凭据);HA 用独立队列服务、本地 append-only WAL 仅社区版 fallback(fsync/加密/限额/启动重放)。第二环也失败→摘 paid-traffic readiness、停收费新请求、内存持续重试,**不得 Abort 已交付 claim**。**这重新打开 docs/process/plans/2026-05-24-post-delivery-settle-recovery 的 Owner D4。**

### 缺口4 Replicate 图片 settle-before-write 不释放
现状:`imageshttp/attempt.go:164` settle 失败返 500(响应未交付),**不调已有 Abort helper**(`imageshttp/billing.go:199`)→claim/hold/slot 冻到 ~30 分钟 sweep。影响所有同步图片 family。
设计:与非流式 chat 统一——**先完整写业务响应再结算**,settle 失败入恢复(新 SourceImagesDelivered,复用三证 worker);零/部分写失败立即 Abort。若只紧急止血不改顺序,则 settle 失败需"查 claim 状态→reserving 才 Abort、committed 要 Refund"——**不能盲目 Abort**(提交应答不明可能已扣费)。

## 建议实现顺序(codex)
1. Owner 确认"完整业务体写成功才算交付"+ 部分写政策。2. 先写/反转锁错终局测试。3. 缺口3(sweep 排除+专用重试+第二环)。4. 缺口2(已交付流禁 Abort+bundle recovery)。5. 缺口1(非流式写后结算)。6. 缺口4(图片复用 buffered-delivery+recovery)。7. 真 PG 故障注入(commit 应答不明/主库不可写/双 DLQ 失败/重启+sweep 竞态)。

## Owner 决策点(执行前必须)
1. **"完整业务体写成功才算交付"政策 + 部分写(部分图片 JSON / keepalive 裸换行不算交付)政策**。
2. **重新打开 D4:第二持久补偿环——独立外部队列 vs 本地 WAL**(HA vs 社区版)。
3. 是否把 audit append 与 Tx2 原子化;是否新增 delivery/settlement intent schema。
4. 反转两个锁定当前坏终局的测试(stream_test:626、images handler_test:256)——确认当前行为非有意。

## 可在现有 schema 内实现(仍 money 高风险,需 Owner 批准执行)
完整写入判定、非流式/图片 post-delivery 接线、来源语义加强、已交付流禁 Abort、sweep 排除现有 recovery 行、按事件种类区分重试策略。

**clean-room**:仅提取行为与失败策略,未复制三镜代码/标识符/结构/测试。
