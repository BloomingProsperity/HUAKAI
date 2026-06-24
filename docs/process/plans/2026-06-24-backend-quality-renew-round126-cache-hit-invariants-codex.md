# 2026-06-24 backend quality renew round126 cache hit invariants

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；继续处理目标文件中 L2 cache 命中后的 money/cache 一致性不变量。 |
| Scope | 仅增强 `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go` 中 L2 cache 命中测试，验证 `CommitCacheHit` 的 `billing.SettleRequest` 不携带 provider/account/acquisition 字段。不修改生产 money path、不改 billing ledger、不改 quota。 |
| Success criteria | 1. 测试记录完整 `CommitCacheHit` 请求而不是只记录 `ClaimID`；2. cache 命中测试断言 `AccountID`、`ProviderAccountID`、`AttemptSeq` 为 0，`AcquisitionToken` 为零值，`EmitSchedulerOutbox` 为 false；3. 保持已有 cache hit 行为断言；4. 静态检查通过，可用 Go 检查如环境缺工具链则如实记录。 |
| Time estimate | 约 15-25 分钟。 |
| Blast radius | 单个 gatewayhttp 测试文件。行为面为测试覆盖增强，不触碰生产代码。 |
| Failure modes | 1. 误把有 pool slot 的后置 cache-hit 分支也断成 provider-less；缓解：本轮只覆盖 acquire 前命中走 `CommitCacheHit` 的现有测试。2. 测试文件变大；当前 544 行，远低于 1000 行巨型测试门。3. Go 工具链缺失无法实际执行；缓解：做文本检查并记录。 |
| Decision points | 如果 Owner 要求强制生产代码显式清零字段，可另起实现轮；本轮先补判别式测试，避免 money path 大改。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已核实 `serveL2CacheHit` acquire 前分支构造 `cacheHitReq` 时未设置 provider/account/acquisition 字段；3. 已确认现有测试只记录 `ClaimID`，缺少字段级断言；4. 不触碰另一个目标计划文件。 |

## 执行顺序

1. 修改 `recordingSettler.cacheHitCommits` 为完整 `[]billing.SettleRequest`。
2. 更新现有长度断言仍保持语义。
3. 在首个 L2 cache hit 测试中断言 provider-less / no acquisition / no scheduler outbox 不变量。
4. 运行文本检查、clean-room 禁词扫描、可用 Go 命令。
