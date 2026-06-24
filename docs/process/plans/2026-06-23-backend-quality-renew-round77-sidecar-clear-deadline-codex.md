# 2026-06-23 backend quality renew round77 sidecar clear deadline

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅限 `backend/internal/transport/mimicry/sidecar_client.go` 与对应测试；处理 sidecar ACK 成功后清理 deadline 的错误路径。明确不读取、不编辑另一个 backend security 目标文件。 |
| Success criteria | ACK 成功后若 `SetDeadline(time.Time{})` 失败，`DialTLS` 必须关闭连接并返回明确错误，不把带潜在旧 deadline 的连接交给上层；新增判别式测试覆盖该路径。 |
| Time estimate | 15-25 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 仅 sidecar 成功握手后的连接状态清理；默认 sidecar 未启用，生产 uTLS 主出口不受影响。 |
| Failure modes | 测试 fake conn 未真正走完 ACK 成功路径；错误路径未关闭连接；本机缺 Go 工具链导致无法执行 `gofmt` / `go test`。缓解：用 net.Pipe 完整跑控制帧，fake conn 统计 `SetDeadline` 与 `Close`，`git diff --check` 和敏感词扫描兜底。 |
| Decision points | 若需要改变 sidecar 启用策略、H1/H2 策略或 Rust 部署策略，停止等待 Owner；本轮不触碰这些。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `DialTLS` 当前 `_ = conn.SetDeadline(time.Time{})`；4. 本轮仅改局部生命周期错误路径。 |

## Concrete execution order

1. 抽 `clearDeadline` helper，`SetDeadline(time.Time{})` 出错时返回 `mimicry sidecar: clear deadline`。
2. `DialTLS` 在 ACK 成功后调用 helper；若失败则关闭连接并返回错误。
3. 增加测试：fake conn 首次设置 deadline 成功、清理 deadline 失败，服务端返回 OK ACK，断言返回错误且连接被关闭。
4. 运行 `git diff --check`、clean-room/敏感词扫描；尝试 `gofmt` / `go test`，如工具缺失则记录。
