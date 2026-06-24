# 2026-06-23 backend-quality-renew-round93-pool-skip-placeholder-guard-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| --- | --- |
| Scope | 清理 `backend/internal/pool/pool_test.go` 中不会运行的 `AT-POOL-019` `t.Skip` 占位，并增加 AT-POOL 测试不得调用 `t.Skip/t.Skipf` 的元测试 guard。 |
| Out of scope | 不改 pool 生产选择逻辑，不改 billing/settler/DB schema，不伪造 Tx2 原子性单元覆盖；真正跨模块 Tx2 原子性仍由 billing/pool integration 测试证明。 |
| Success criteria | `pool_test.go` 不再含 `AT-POOL` 占位 skip；guard 能在后续有人给 `TestAT_POOL_*` 加回 `t.Skip/t.Skipf` 时失败；静态检查无空白错误。 |
| Time estimate | 约 20-30 分钟。 |
| Blast radius | 仅测试文件；不影响生产二进制。 |
| Failure modes | 误删真实覆盖：只删除无断言 skip 占位，不删除已有可执行 AT-POOL 测试；guard 误报 helper skip：只扫描函数名 `TestAT_POOL_` 前缀的 acceptance 测试；Go 工具缺失：用 parser 脚本和 `git diff --check` 兜底并记录。 |
| Decision points | 若 Owner 要把 `AT-POOL-019` 补成真实跨模块 integration 测试，需要单独授权，因为会进入 billing/settler/DB 集成范围。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 acceptance-test-writer 技能；3. 已核 `AT-POOL-019` 当前只有 `t.Skip` 且无断言；4. 已看到真实 DBClaimGate/settler 原子性在其他 integration 测试中已有覆盖线索。 |

## 执行顺序

1. 给 `pool_test.go` 增加 `go/ast`、`go/parser`、`go/token`、`strings` 导入。
2. 删除 `TestAT_POOL_019_Tx2Atomicity` 占位 skip。
3. 添加 `TestAT_POOL_NoPlaceholderSkipsRemain`，解析本文件并扫描 `TestAT_POOL_` 函数体中的 `t.Skip/t.Skipf`。
4. 运行可用检查；如 `go test`/`gofmt` 缺失，记录真实限制。
