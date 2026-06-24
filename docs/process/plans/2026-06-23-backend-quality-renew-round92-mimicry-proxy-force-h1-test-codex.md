# 2026-06-23 backend-quality-renew-round92-mimicry-proxy-force-h1-test-codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| --- | --- |
| Scope | 仅补 `backend/internal/transport/mimicry` 的 uTLS 代理路径 HTTP/2 关闭回归测试；生产逻辑保持不变。 |
| Out of scope | 不改传输策略、不启用 Rust sidecar、不补 H2、不删除 Rust H2 dead-code、不碰鉴权/计费/配额/schema/部署/密钥。 |
| Success criteria | `WithProxy` 生成的代理 `roundTripper` 内层 `http.Transport.ForceAttemptHTTP2` 被测试明确断言为 `false`；现有直连测试继续保留；静态检查无空白错误。 |
| Time estimate | 约 15-25 分钟。 |
| Blast radius | 仅测试文件；失败只影响测试门，不改变生产出口行为。 |
| Failure modes | 代理 URL 构造触发真实网络：测试只构造 RoundTripper，不发请求；误测外层类型：用 package 内白盒取 `*roundTripper.inner`；Go 工具缺失：用静态检查和源码审阅兜底并记录限制。 |
| Decision points | 若要真正改生产出口策略或删除 Rust H2 文件，需另开计划；本轮不扩大。 |
| Pre-execution checklist | 1. 已重读目标文件；2. 已读取 `api-gateway-risk-review` 技能；3. 已核 `NewRoundTripper` 与 `WithProxy` 当前源码；4. 已确认本轮只补测试。 |

## 执行顺序

1. 在 `utls_dialer_force_h1_test.go` 增加代理路径断言。
2. 使用本地 `http://127.0.0.1:8080` URL 构造代理 RoundTripper，不发网络请求。
3. 运行可用静态检查；若 `go test`/`gofmt` 因工具链缺失失败，记录真实结果。
