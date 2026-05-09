# 2026-05-09 M-rust-1 Codex lane
| Owner directive | “实施 M-rust-1: 建立独立 cargo workspace、config、error type、request ids、基础 tracing; 不实现业务 forwarding。” |
| Scope | In: `exploratory/rust-core-gateway/codex-lane/` 独立 Rust workspace、typed ENV config、统一错误、UUIDv7 request id、tracing 初始化、Axum `/healthz`、smoke tests。Out: 主线 Go、`claude-lane/`、Go control plane RPC、业务 forwarding、vendor proxy、stream parser。 |
| Success criteria | `cargo build`、`cargo test`、`cargo clippy -- -D warnings`、`cargo fmt --check` 全部通过；本地启动后 `GET /healthz` 返回 `{"status":"ok"}`。 |
| Time estimate | 1 个 Codex atom；预计 30-60 分钟墙钟时间，取决于 Rust crate 下载和编译缓存。 |
| Blast radius | 限定在探索 fork 的 `codex-lane/`；额外仅新增本计划文档。失败不会影响主线 Go 服务、数据库、认证、计费或配额。 |
| Failure modes | 依赖版本不兼容导致编译失败：选择稳定 public API 并固定版本；tracing OTLP feature 编译复杂：先做安全 stub，不启用真实 export；端口冲突：测试与验证使用可配置端口或 ephemeral listener；ENV 缺失：保持 fail-fast 并用测试隔离环境。 |
| Decision points | 本 atom 不需要 Owner 再确认；不会引入数据库 schema、认证、计费、配额、生产部署或 `LICENSE` 变更。OTLP 真实 exporter 与 gRPC control plane 留给后续 milestone。 |
| Pre-execution checklist | 1. 不读取 `claude-lane/`。2. 只读取共享 `PLAN.md` 与 `codex-lane/`。3. 使用 public crate API，不读外部 Rust gateway 源码。4. 注释中文、标识符英文。5. 验收命令完成后记录结果。 |

## Concrete execution order

1. 创建 cargo workspace 与 `core_gateway` binary crate。
2. 实现 `config.rs`、`error.rs`、`request_id.rs`、`tracing_init.rs`、`main.rs`。
3. 添加 smoke tests 覆盖 config、health、request id 唯一性。
4. 运行 `cargo fmt --check`、`cargo build`、`cargo test`、`cargo clippy -- -D warnings`。
5. 启动 binary 并用 `curl` 验证 `/healthz`。
6. 汇总 tree、LoC、测试数量、health 验证描述与风险说明。
