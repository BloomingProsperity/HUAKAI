# 2026-06-23 backend quality renew round76 sidecar frame io

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅限 `backend/internal/transport/mimicry/sidecar_client.go` 与对应测试；收敛 sidecar 帧读写 I/O 语义。明确不读取、不编辑另一个 backend security 目标文件。 |
| Success criteria | 读帧 helper 显式处理异常零进展读并返回标准 `io.ErrNoProgress`；写帧短写返回标准 `io.ErrShortWrite`；新增判别式测试覆盖零进展读，现有短写测试继续覆盖写满行为。 |
| Time estimate | 15-25 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 仅 sidecar 控制帧协议辅助函数；默认 sidecar 未启用，生产 uTLS 出口不应受影响。 |
| Failure modes | 错误语义变化导致测试断言不匹配；测试 fake conn 不能稳定复现零进展读；本机缺 Go 工具链无法执行 `gofmt` / `go test`。缓解：使用 `io.ErrShortWrite` / `io.ErrNoProgress` 等标准错误，`git diff --check` 与敏感词扫描兜底，无法运行的检查如实记录。 |
| Decision points | 若需要改变 sidecar 是否启用、H1/H2 策略、Rust sidecar 部署策略，停止等待 Owner；本轮不触碰这些。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `api-gateway-risk-review` 技能；3. 已确认本轮只修改 mimicry 局部；4. 已核 `sidecar_client.go` 当前 `readFullConn` / `writeFullConn` 实现。 |

## Concrete execution order

1. 在 `readFullConn` 内为 `Read` 返回 `0,nil` 增加 `io.ErrNoProgress` fail-loud 分支。
2. 将 `writeFullConn` 的零写入错误改为 `io.ErrShortWrite`。
3. 在 `sidecar_client_test.go` 增加零进展读 fake conn 与 `TestReadSidecarFrameRejectsNoProgressRead`。
4. 中文化本轮接触到的 Go 注释。
5. 运行 `git diff --check`、clean-room/敏感词扫描；尝试 `gofmt` / `go test`，如工具缺失则记录。
