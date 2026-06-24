# 2026-06-23 credentialstore scan 去重

| Owner directive | "根据我给你的文档继续刚刚未完成的renew"；目标文件要求处理 `internal/credentialstore/postgres_store.go` 三处逐字段扫描重复 |
| Scope | 仅限 `backend/internal/credentialstore/postgres_store.go` 中 `scanRecord` / `scanRecordWithCount` / `scanRecordForRefresh` 以及同列族 `ResolveActive` 直接扫描的字段扫描去重；不改 SQL 语义、不改数据库 schema、不改解密/审计/刷新状态逻辑 |
| Success criteria | 三个入口和 `ResolveActive` 继续返回原有错误与附加字段；26 个公共字段只保留一份扫描落位逻辑；新增 helper 判别式单元测试；`git diff --check` 通过；若 Go 工具链可用则运行定向测试 |
| Time estimate | 约 20-30 分钟墙钟时间；单个 Codex 小补丁 |
| Blast radius | 凭据解析、刷新 worker 载入凭据、测试模式凭据载入、活跃凭据解析会经过共享 helper；若字段顺序写错会影响凭据状态与时间字段 |
| Failure modes | 额外列顺序接错导致 `refresh_lead_seconds` 或 `COUNT(*)` 读取错误；`pgx.ErrNoRows` 映射丢失；时间字段未赋回 `CredentialRecord` |
| Mitigation | 保留三层 wrapper 分别处理 ErrNoRows 和附加返回值；共享 helper 只负责公共字段扫描与时间字段赋值；补充 `rg` 和 diff 检查定位所有调用点 |
| Decision points | 本轮不拆 `credentialstore` 包、不改 schema、不改高风险凭据语义；若发现需要 schema 或刷新策略变更，停止并请 Owner 确认 |
| Pre-execution checklist | 1. 已读取目标 objective；2. 已核对三处扫描函数和调用点；3. 已确认另一个目标 plan 不读不改；4. 编辑前记录本计划；5. 编辑后跑可用检查 |
