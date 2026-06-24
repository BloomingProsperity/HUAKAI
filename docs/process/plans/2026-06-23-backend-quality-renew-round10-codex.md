# 2026-06-23 backend quality renew round10

| Owner directive | "根据我给你的文档继续刚刚未完成的renew" |
| Scope | 本轮只审传输伪装层: 生产 Go `backend/internal/transport/mimicry/`、Go sidecar 桥接文件、canonical Rust `exploratory/rust-core-gateway/merged/crates/tls-sidecar`。不审 Rust 草稿 lane 细节、不催 H2、不写 findings 报告 `.md`、不碰 backend-security-scan 计划文件。 |
| Success criteria | 输出中文增量 findings: Go uTLS 是否与"ClientHello 伪装 + 强制 H1 + 不做 H2"一致; sidecar 是否默认 park 且边界资源释放/帧限制可验证; Rust H2 相关文件是否仍被 canonical crate 引用; 每条都有当前 `file:line` 证据与可执行修法。 |
| Time estimate | 约 60-90 分钟墙钟;本回合完成一轮闭环审查。 |
| Blast radius | 只读审查 + 新增本计划文件;不改生产代码、不删 Rust 文件、不改配置。 |
| Failure modes | 把 parked Rust 当生产路径误判;把 H2 决策误解成要补 H2;重复报告旧结论而不核实当前代码;读取另一个目标文件。缓解:只用当前源码证据,明确 Go 活出口/Rust park 边界,不读安全扫描计划。 |
| Decision points | 若发现清理 H2 文件/旧 lane 需要删除代码,本轮只报告,是否删除由 Owner 后续确认。若发现纯安全问题只指向 security 专项。 |
| Pre-execution checklist | 1. 列出 mimicry 与 tls-sidecar 文件;2. 核 Go transport 是否 ForceAttemptHTTP2=false/ALPN H1-only;3. 核 sidecar socket 默认启用条件与帧协议错误释放;4. 核 Rust h2_bridge/h2_settings 引用关系与 unwrap/expect;5. 输出 round10 findings。 |
| Concrete execution order | 先读 Go mimicry transport/test,再读 sidecar_client 和 gateway wiring/env,然后读 Rust `Cargo.toml`/`src` mod 引用与 H2 文件,最后按 S1/S2/S3 归纳。 |
