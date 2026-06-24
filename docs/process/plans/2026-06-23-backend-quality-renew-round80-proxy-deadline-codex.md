# 2026-06-23 backend quality renew round80 proxy deadline

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 仅限 `backend/internal/transport/mimicry/proxy_dialer.go` 与对应 proxy 测试；修正 HTTP CONNECT / SOCKS5 proxy 链路的 deadline 设置与清理错误处理。明确不读取、不编辑另一个 backend security 目标文件。 |
| Success criteria | proxy 拨号阶段 `SetDeadline` 或清 deadline 失败时 fail-loud 并关闭连接；新增 HTTP CONNECT 判别测试覆盖初始设置失败与清理失败；本轮接触的 Go 注释中文化且不再保留 socks5 不支持的陈旧说明。 |
| Time estimate | 25-40 分钟墙钟；1 个 Codex 小切片。 |
| Blast radius | 仅 mimicry proxy 拨号错误路径；正常 HTTP CONNECT / SOCKS5 成功路径语义不变。 |
| Failure modes | 测试 fake conn 未真实走 CONNECT 响应后清理 deadline 分支；引入测试 seam 影响生产拨号；本机缺 Go 工具链无法执行 `gofmt` / `go test`。缓解：测试 seam 默认仍创建 30s timeout 的 `net.Dialer`，测试后恢复；`git diff --check` 与敏感词扫描兜底。 |
| Decision points | 若需要改变代理支持矩阵、H1/H2 策略、sidecar 部署策略或真实凭据处理，停止等待 Owner；本轮不触碰这些。 |
| Pre-execution checklist | 1. 已重读 goal objective；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `proxy_dialer.go` 当前忽略 `SetDeadline` / 清 deadline 错误；4. 本轮只改传输 proxy 错误路径。 |

## Concrete execution order

1. 增加可测试的 proxy TCP 拨号 seam，默认行为保持 30s timeout 的 `net.Dialer`。
2. 抽 `setProxyDeadlineFromContext` / `clearProxyDeadline`，HTTP CONNECT 与 SOCKS5 都复用；出错时调用方关闭连接并返回错误。
3. 增加 HTTP CONNECT 测试：初始 deadline 设置失败只关闭一次；CONNECT 200 后清 deadline 失败只关闭一次。
4. 中文化本轮接触的 proxy 测试注释并修正 socks5 支持状态注释。
5. 运行 `git diff --check`、clean-room/敏感词扫描；尝试 `gofmt` / `go test`，如工具缺失则记录。
