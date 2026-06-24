# 2026-06-24 backend quality renew round156 storm policy error last

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 处理 `backend/internal/gateway/storm_policy.go` 中 `StormPolicy.Acquire` 的 ST1008 返回值顺序问题，并同步当前仓内 `storm_policy_test.go` 调用解构；不改 token bucket、singleflight、拒绝/退款语义。 |
| Success criteria | `Acquire` 返回顺序变为 `(val, denied, err)`，`error` 位于最后；所有当前调用点同步；`backend/scripts/staticcheck-baseline.txt` 删除对应 ST1008；`rg` 不再发现旧 baseline 条目。 |
| Time estimate | 约 20 分钟墙钟时间，1 个 Codex 小闭环。 |
| Blast radius | 中：修改 internal API 签名与测试调用点；当前 `rg` 未发现生产 wiring 调用 `StormPolicy.Acquire`，运行时策略语义不变。 |
| Failure modes | 漏改调用点会编译失败；用 `rg` 全仓查 `Acquire(` 与 `storm_policy.go:.*ST1008`。若本地无 Go 工具链，记录无法 `gofmt/go test`。 |
| Decision points | 若发现生产调用点或需要改变拒绝/退款行为，本轮停止并请求 Owner 确认；当前只做返回值顺序等价调整。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已打开真实代码；3. 已用 `rg` 列出调用面；4. 保持 bucket 消耗、global denial refund 与 fn error 语义不变；5. 清理单条 baseline。 |

## 执行顺序

1. 调整 `StormPolicy.Acquire` 签名和返回语句，让 `error` 位于最后。
2. 同步 `storm_policy_test.go` 中的返回值解构顺序。
3. 删除 `staticcheck-baseline.txt` 中对应 ST1008。
4. 用 `rg`、`git diff --check`、clean-room 词扫描核验，并尝试 `gofmt/go test`。
