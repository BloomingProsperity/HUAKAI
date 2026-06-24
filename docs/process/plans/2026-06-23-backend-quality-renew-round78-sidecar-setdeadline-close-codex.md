# 2026-06-23 backend quality renew round78 sidecar setdeadline close

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅限 `backend/internal/transport/mimicry/sidecar_client.go` 与对应测试；修正 sidecar 初始 deadline 设置失败时的连接关闭职责。明确不读取、不编辑另一个 backend security 目标文件。 |
| Success criteria | `setDeadlineFromContext` 只负责返回错误，不在 helper 内关闭连接；`DialTLS` 统一关闭连接且测试证明初始 `SetDeadline` 失败时只关闭一次。 |
| Time estimate | 10-20 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 仅 sidecar 拨号后、控制帧发送前的错误路径；默认 sidecar 未启用，生产 uTLS 主出口不受影响。 |
| Failure modes | helper 改动导致错误路径漏关连接；测试 fake conn 未覆盖带 ctx deadline 的分支；本机缺 Go 工具链无法执行 `gofmt` / `go test`。缓解：fake conn 统计 `SetDeadline` 与 `Close`，`git diff --check` 与敏感词扫描兜底。 |
| Decision points | 若需要改变 sidecar 启用策略、H1/H2 策略或 Rust 部署策略，停止等待 Owner；本轮不触碰这些。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `setDeadlineFromContext` 当前会关闭连接且调用方也会关闭；4. 本轮仅改局部错误路径。 |

## Concrete execution order

1. 从 `setDeadlineFromContext` 删除内部 `conn.Close()`。
2. 新增测试：`sidecarDialContext` 返回会在非零 deadline 上失败的 fake conn，`DialTLS` 应返回 `set deadline` 错误并只调用一次 `Close`。
3. 运行 `git diff --check`、clean-room/敏感词扫描；尝试 `gofmt` / `go test`，如工具缺失则记录。
