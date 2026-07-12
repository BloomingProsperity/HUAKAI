# 2026-07-05 media 孤儿追扣与 claim 状态机配合修 Codex 计划

| Owner directive | “media 孤儿追扣子系统与 claim 状态机配合修(MEDIA-1 账本一致 + MEDIA-2 孤儿线索 + MEDIA-3 观测标注)” |
| --- | --- |
| Scope | 只修改 `internal/mediatask` 内与孤儿追扣、任务 abort、对应测试相关的代码；只调用 `internal/billing` 现有导出函数，不改其生产码；不触碰禁改目录和文件。 |
| Out of scope | 不新增向用户再次扣款或退款路径；不改 `internal/billing/`、`internal/quota/`、`internal/billingreconhttp/`、`cmd/gateway/routes.go`、`frontend/`、`internal/transport/`、`internal/gatewayhttp/`；不执行 `git commit` 或 `git push`。 |
| Success criteria | MEDIA-1 追扣成功后 claim 与 billing event 原子进入 committed；rows==0 时不扣款且返回冲突 outcome；MEDIA-2 自超时且已有上游任务 ID 时落孤儿线索；MEDIA-3 swept 已释放 hold 只返回人工处理 outcome，余额与账本事件不变；指定 Go 门禁与 integration_pg 测试通过；逐条完成变异证红。 |
| Time estimate | 约 1.5-2.5 小时墙钟；主要时间在阅读现有事务形状、补 integration_pg 测试、跑 PostgreSQL 集成门禁和变异验证。 |
| Blast radius | `internal/mediatask` money 事务路径、孤儿追扣 outcome、孤儿线索生成、相关测试。 |
| Failure modes | 事务顺序错误导致 Capture 与 claim 状态不一致：采用先 `UpdateClaimCommitted` 成功后再 Capture 与写事件；测试 fixture 未覆盖 sweeper 竞争：补 claim 终态和后续 sweeper 不再 abort 断言；误触二次 charge：MEDIA-3 加余额零变化和无新 billing event 断言；参考源码污染：只记录行为级 file:line 证据，不复制代码、注释、结构或内部命名到生产代码。 |
| Decision points | 若发现必须改禁改目录、schema、billing/quota 核心或新增 runtime dependency，停止并请求 Owner 确认；若 swept 真实追扣需要新 charge，仅记录为 Owner-gated，不实现。 |
| Pre-execution checklist | 1. 阅读指定 HUAKAI 代码区域。2. 读取 New-API 与至少一个其它镜像的相关源码证据并记录 file:line。3. 定位现有 mediatask 测试夹具和 helper。4. 设计最小补丁并确认不触碰禁改路径。5. 写判别测试。6. 跑单包测试、integration_pg、build/vet。7. 逐条做变异证红并还原。 |
| Concrete execution order | 先读 `store_orphan_backcharge.go`、`store_money.go` 与现有测试；再取证参考项目同路径资金调整+记账的原子性；随后修改 `captureOrphanHold` 的 claim committed 顺序和 outcome，修改 `abortTask` 正常 Release 后的孤儿线索；补 MEDIA-1/2/3 测试；跑门禁；做三个定向变异并记录结果；最后输出中文报告。 |

## 交叉计划说明

当前会话只有 Codex 执行环境，不能独立启动 Claude 侧并行计划或完成双方综合计划。为避免阻塞 Owner 已明确授权的修复，本计划作为 Codex 独立计划落盘；执行中若遇到高风险决策点会停止等待 Owner。
