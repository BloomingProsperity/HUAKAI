# 2026-06-24 backend quality renew round125 usersession cache sync

| Owner directive | “根据我给你的文档继续刚刚未完成的renew”；继续处理目标文件中 `usersession/store.go` 写路径 best-effort 同步 MemoryStore 且裸 `_` 忽略错误的问题。 |
| Scope | 仅收敛 `backend/internal/usersession/store.go` 中 Postgres 权威写入后的内存 cache 同步错误纪律；不修改数据库 schema、不改变登录/登出/刷新 token 的成功失败语义、不改 `rotation.go` 的安全分支。 |
| Success criteria | 1. 生产代码中不再出现 `_ = s.cache...` 或 `_, _ = s.cache...` 裸忽略；2. 新增命名 helper 明确说明 Postgres 是权威源、cache 同步为 best-effort；3. 新增 codebudget guard 防止裸忽略回归；4. 静态检查通过，可用 Go 检查如环境缺工具链则如实记录。 |
| Time estimate | 约 20-30 分钟。 |
| Blast radius | `usersession` 的 cache 同步表达方式与 `codebudget` guard。行为应保持一致：cache 同步失败仍不阻断权威 DB 写入结果。 |
| Failure modes | 1. 误把 cache 同步错误冒泡，导致旁路 cache 故障阻断用户会话写路径；缓解：helper 明确吞掉错误。2. 改动让 `store.go` 继续膨胀；缓解：只新增一个小 helper，后续仍应拆分 usersession store。3. Go 工具链缺失无法实际跑测试；缓解：做文本检查并记录限制。 |
| Decision points | 是否把 MemoryStore 同步失败改成可观测指标或日志：本轮不做，避免引入 logger/metrics 依赖；后续可在 observability slice 单独处理。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已核实 `store.go` 中 8 处裸忽略 cache 同步错误；3. 已确认 `store.go` 当前 936 行，baseline 为 937，新增 helper 在 5% 余量内；4. 不触碰另一个目标计划文件。 |

## 执行顺序

1. 在 `PostgresStore` 附近新增 `syncCacheBestEffort` helper。
2. 替换写路径中的 `_ = s.cache...` / `_, _ = s.cache...` 为 helper 调用。
3. 新增 `codebudget/usersession_cache_sync_guard_test.go`，禁止裸忽略回归。
4. 运行文本检查、clean-room 禁词扫描、可用 Go 命令。
