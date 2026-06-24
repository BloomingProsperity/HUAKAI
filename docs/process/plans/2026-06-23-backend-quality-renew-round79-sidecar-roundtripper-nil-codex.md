# 2026-06-23 backend quality renew round79 sidecar roundtripper nil

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅限 `backend/internal/transport/mimicry/sidecar_client.go` 与对应测试；修正 `sidecarRoundTripper.DialTLSContext` nil receiver / nil client 的 fail-loud 行为。明确不读取、不编辑另一个 backend security 目标文件。 |
| Success criteria | `DialTLSContext` 遇到 nil receiver 或 nil client 时返回明确错误，不 panic；新增判别式测试覆盖两种 nil 构造错误。 |
| Time estimate | 10-20 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 仅 sidecar RoundTripper 的错误构造路径；正常 sidecar client 和生产 uTLS 主出口不受影响。 |
| Failure modes | guard 放在解析 network/addr 后导致错误优先级不清；测试未真实调用 nil receiver；本机缺 Go 工具链无法执行 `gofmt` / `go test`。缓解：直接白盒调用 nil receiver 和 nil client，`git diff --check` 与敏感词扫描兜底。 |
| Decision points | 若需要改变 sidecar 启用策略、H1/H2 策略或 Rust 部署策略，停止等待 Owner；本轮不触碰这些。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `DialTLSContext` 当前直接调用 `rt.client.DialTLS`；4. 本轮仅改构造错误路径。 |

## Concrete execution order

1. 在 `sidecarRoundTripper.DialTLSContext` 开头增加 nil receiver / nil client guard。
2. 新增测试：nil receiver 不 panic 且返回 `nil round tripper`；nil client 不 panic 且返回 `nil client`。
3. 运行 `git diff --check`、clean-room/敏感词扫描；尝试 `gofmt` / `go test`，如工具缺失则记录。
