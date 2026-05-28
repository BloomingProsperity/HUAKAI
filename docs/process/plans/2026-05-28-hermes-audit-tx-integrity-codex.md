# 2026-05-28 Hermes message+audit fail-closed tx integrity (Codex)
| Owner directive | "DECISION (Owner chose fail-closed, same transaction): A Hermes chat message and its PRIMARY audit entry must commit or roll back together."
| Scope | 只改 `backend/internal/hermeschat` 中的 message.send 持久化链路：`bridge_persist.go`、`bridge_audit.go` 与 hermeschat 测试；不改其他路径（enable/profile 等已正确）
| Success criteria | 1) 当 audit 插入失败时，`Send` 持久化返回错误并触发 rollback；2) 成功场景仍保持原有提交行为；3) 新增测试能覆盖 commit/rollback 与持久化计数差异
| Time estimate | 约 20 分钟执行 + 15 分钟测试/修正
| Blast radius | 回归 message.send 的事务一致性；错误情况下可见持久化行为变更（此前成功提交，现改为回滚）
| Failure modes | audit 错误未向上冒泡导致仍提交；savepoint 逻辑继续吞掉错误；测试桩未能准确捕获 commit/rollback；修复引入非目标路径影响（例如 profile/enable 路径）
| Decision points | 1) 是否将 `recordMessageAudit` 中保存点语义删除完全移除；2) 是否保留 DLQ enqueue 仅作观测副本；3) 是否只限 message.send 分支
| Pre-execution checklist | 1) 阅读 `bridge_persist.go` 与 `bridge_audit.go` 现有控制流；2) 复用 hermeshttp 的 tx-recorder 测试技巧；3) 编写带坏插入与成功插入两类测试；4) 运行要求命令；5) 输出变更文件与结果
