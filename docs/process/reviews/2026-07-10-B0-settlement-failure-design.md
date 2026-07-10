# B0 结算失败四终局——设计与 Owner 决策点 — 2026-07-10

**调研**:codex(auditor lane),§17 三镜对照(sub2api@e316ebf / new-api@262ab93 / CLIProxyAPI@8c2bf2c)+ HUAKAI@2c7452ee。
**裁定**:四条均 money-path 高风险;部分**需 Owner 决策**,且缺口3的正解**推翻 Owner D4(2026-05-24 "只 alert 不做 disk spool")**。故 B0 **surface 给 Owner,未擅自实现**。

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
