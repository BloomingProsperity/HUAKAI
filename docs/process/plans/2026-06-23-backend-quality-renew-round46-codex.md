# 2026-06-23 backend quality renew round46 codex

| Owner directive | “根据我给你的文档继续刚刚未完成的renew” |
| Scope | 本轮审查生产传输伪装层: `backend/internal/transport/mimicry/`、其在 `cmd/gateway` 的接线、以及 parked Rust `exploratory/rust-core-gateway/merged/crates/tls-sidecar` 中与当前出口决策冲突的死代码/边界质量。 |
| Out of scope | 不读取非 HUAKAI 参考项目源码; 不建议上线 Rust sidecar; 不补 H2; 不触碰 `docs/process/plans/2026-06-23-backend-security-scan-codex.md`; 不修改生产代码。 |
| Success criteria | 输出中文 findings, 每条含 `file:line`、严重度、问题、修法; 核对 Go uTLS 是否符合“强制 H1、不做 H2”; 核对 sidecar 默认关闭/桥接资源释放; 核对 Rust H2 模块是否仍保留/被引用; 核对测试是否判别式。 |
| Time estimate | 约 45-75 分钟墙钟; 1 个 Codex 审查分片。 |
| Blast radius | 只读审查与新增计划文件, 不影响运行时代码。 |
| Failure modes | 把旧状态文档当事实: 以 `.go` / `.rs` 真码为准; 混入 clean-room 风险: 不读取参考项目源码; 过度建议上 Rust sidecar: 按 Owner 决策只评 parked 质量和死代码。 |
| Decision points | 若确认 H2 仍在生产 Go uTLS 路径启用, 标 S1/S2 并建议 Owner 决定修复窗口; 本轮不直接改出口行为。 |
| Pre-execution checklist | 1. 已重读 objective; 2. 已读取 `api-gateway-risk-review`; 3. 确认 round46 计划不存在; 4. 先量化 Go/Rust 文件; 5. 再逐项核 ALPN/ForceAttemptHTTP2、sidecar framing、Rust H2 引用、测试覆盖。 |
| Concrete execution order | 1. `rg --files` 与 `wc` 量化 mimicry 和 tls-sidecar; 2. 读 `utls_dialer.go` / `template.go` / `registry.go`; 3. 读 `sidecar_client.go` / `attempt_error.go`; 4. 搜 Rust `h2_bridge` / `h2_settings` 引用; 5. 搜测试断言; 6. 尝试运行相关 Go/Rust 测试并记录环境限制。 |
