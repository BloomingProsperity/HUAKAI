# 2026-06-23 backend-quality-renew-round56-mediatask-money-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮只审查 `backend/internal/mediatask`、相关 HTTP/client 包和第二条媒体任务计费链；重点是 claim/hold/abort/idempotency/mediaClaimKey 与主链 billing 状态机是否一致。不修改生产代码，不碰另一个目标计划。 |
| Success criteria | 输出中文 findings，逐条带绝对路径和行号；能说明媒体任务在成功、失败、重复提交、worker 停机、余额/hold 更新下是否可恢复、可审计、可测试。 |
| Time estimate | 约 60-90 分钟人工审查等价时间；本代理本轮完成一个独立切片。 |
| Blast radius | 只读审查 + 新增计划文件；不会改变运行时行为。 |
| Failure modes | 只看 API 表面不看 SQL/状态机；把安全专项问题展开过深；把 integration_pg 假绿当覆盖；忽略 worker 停机和任务重放。 |
| Decision points | 如果发现需要修改 billing ledger、余额表、数据库 schema、真实支付/扣款逻辑，只作为 finding 记录，本轮不直接改。 |
| Pre-execution checklist | 1. 读取目标文件和相关技能。2. 定位 mediatask 入口、store_money、worker、HTTP/client。3. 跟踪成功/失败/重复提交的余额与 task 状态。4. 查测试是否判别式并是否在 CI 运行。5. 运行可用检查；工具缺失如实记录。 |
| Concrete execution order | 先 `rg` 定位 `mediaClaimKey`、`hold`、`balance`、`abort`、`idempotency`，再读 service/store/worker/test，最后输出按 S0/S1/S2/S3 分区的中文审查结果。 |
