# core_gateway — merged lane (M-rust-1)

## 整合策略

| 模块 | 来源 | 说明 |
|------|------|------|
| **工程结构** | codex-lane | Cargo workspace + `crates/core_gateway/` 子 crate; main.rs 极简 16 行 |
| **error.rs** | claude-lane | 6 variants (Config/Network/Upstream/ControlPlane/Stream/Internal) + `error_class()` helper; `Network` 保留 codex-lane 的 `From<io::Error>` |
| **tracing_init.rs** | claude-lane 设计 + 类型修正 | JSON / 紧凑双分支; OTLP provider 构建 + graceful degradation; M-rust-1 只构建 provider 不挂 span exporter (规避 tracing-subscriber 泛型分支类型不兼容问题, M-rust-2 接入) |
| **config.rs** | codex-lane (强类型) + claude-lane (validate + otlp_endpoint) | `StartupConfig` 持有 `SocketAddr`/`Uri`/`LevelFilter` 强类型字段; `otlp_endpoint: Option<Uri>`; `from_env_iter()` 供测试注入; `validate()` 方法 |
| **request_id.rs** | 两 lane 合并 | `RequestId` 结构体 (codex-lane, Arc<str>, header 解析) + `new_request_id`/`format_request_id`/`parse_or_generate` 函数 (claude-lane) |
| **lib.rs** | codex-lane | `run()` 异步入口 + `build_router()` + `GatewayState` (Arc<config> 注入) |
| **main.rs** | codex-lane | 16 行极简: 解析配置 → 安装 tracing → 构建 runtime → block_on(run) |
| **tests/smoke.rs** | 两 lane 合并 | 12 个集成测试 (含 TCP e2e); 单元测试 18 个; 合计 30 个 |

## 验证结果

```
cargo build   → Finished (dev)        ✓
cargo test    → 30 passed, 0 failed   ✓
cargo clippy  → 0 warnings            ✓
cargo fmt     → clean                 ✓

curl localhost:<port>/healthz → {"status":"ok"}  (health_endpoint_tcp_e2e 测试覆盖)
```

## 后续

- M-rust-2+ 在 `merged/` 目录推进
- `codex-lane/` 和 `claude-lane/` 保留为历史参考, 不再修改
- OTLP span exporter 挂载留 M-rust-2 (`tracing_init::install` 中 `build_otel_layer_for_json` 已预留接口)
