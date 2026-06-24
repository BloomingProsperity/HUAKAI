# 2026-06-23 backend-quality-renew-round55-cache-money-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/cache*`、缓存路由/计划/指标、以及 gateway 中 `CommitCacheHit` 相关账务接线；不修改生产代码，不碰另一个目标的计划文件。 |
| Success criteria | 输出直接面向 Owner 的中文 findings，逐条带绝对路径和行号；能说明缓存命中路径是否保持 provider account、quota、billing、audit、recovery 不变量。 |
| Time estimate | 约 45-75 分钟人工审查等价时间；本代理本轮完成一个独立切片。 |
| Blast radius | 只读审查 + 新增计划文件；若误判，会影响后续重构优先级，但不会改变运行时行为。 |
| Failure modes | 误把安全专项展开成 renew 结论：仅点到并转 security；只看测试不看真码：必须读真实 Go/SQL；把 cache 命中当普通性能路径：必须核 money path。 |
| Decision points | 若发现需要修改 billing ledger、quota enforcement、数据库 schema 或生产缓存策略，先作为 finding 记录，不在本轮直接改。 |
| Pre-execution checklist | 1. 读取目标文件和相关技能。2. 定位 cache/cache_routing/cacheplan/cachemetrics 与 gateway billing 接线。3. 核对 `CommitCacheHit` 的输入不变量和错误处理。4. 核对测试是否能让坏实现变红。5. 运行可用检查；工具链缺失要如实记录。 |
| Concrete execution order | 先 `rg` 定位 `CommitCacheHit`/`response_cache_l2`/`provider_account_id`，再读 gateway cache 命中路径、billing settler、cache store、测试与 CI，最后输出按 S0/S1/S2/S3 分区的中文审查结果。 |
