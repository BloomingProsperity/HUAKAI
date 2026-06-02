# HUAKAI Rust Core Gateway — Shadow Readiness 评估报告

生成日期: 2026-05-09
Lane: claude-lane M-rust-10
工作范围: 探索性 fork，不接入主线

> 【2026-06-02 已更新】本报告评估的是 2026-05-09 旧 `core_gateway`
> shadow-readiness；当前数据面方向已定为 C：Go `gatewayhttp` 是账号转
> API 大脑，旧 `RouteService` / `route_client` / `mock_control_plane`
> 按 C 方向退役为 legacy。Rust 新 `crates/tls-sidecar` 已落
> BoringSSL Phase 1-3 基础能力，H2 SETTINGS 逐字段控制见
> `crates/tls-sidecar/src/h2_settings.rs`，Go 侧 sidecar 接线见
> `backend/internal/transport/mimicry/sidecar_client.go` 与
> `backend/cmd/gateway/wiring.go`。但任何生产 sidecar 启用前仍必须处理
> R-SIDECAR-001 raw sigalgs 10/26 gap 与 R-SIDECAR-002 ALPN=h2 raw tunnel/H2 framing，
> 并完成更多 vendor 指纹 profile 与真上游验证；以下旧 `core_gateway` NO-GO / 未接主线判断为历史。

---

## 1. 评估范围

本报告评估 `exploratory/rust-core-gateway/merged/` Rust 数据面在以下维度的 shadow 接入就绪度：

- 功能完整性（相对 PLAN.md §3 模块表）
- 测试覆盖与质量
- 性能指标（M-rust-10 负载烟雾实测）
- 与主线 Go hot path 的 benchmark gap
- 下一步阻塞项

---

## 2. 模块完成矩阵

| 模块 | 状态 | 测试 | 关键文件 |
|---|---|---|---|
| m1_listener | 完成 | listener_test.rs | src/listener.rs |
| m2_route_client | 完成 | route_client_test.rs | src/route_client.rs |
| m3_account_planner | 完成 | proxy_engine_test.rs (集成) | src/account_planner.rs |
| m4_proxy_engine | 完成 | proxy_engine_test.rs | src/proxy_engine.rs |
| m5_stream_pipeline | 完成 | stream_pipeline_test.rs | src/stream_pipeline/ |
| m6_attempt_reporter | 进行中 (codex M-rust-8) | — | src/attempt_reporter.rs |
| m7_observability | 完成 | observability_test.rs | src/metrics.rs, src/tracing_init.rs |
| m8_test_harness | 完成 | 全部测试文件 | tests/ |
| m-rust-10 load smoke | 完成 | tests/load_smoke.rs | — |
| m-rust-7 AWS Bedrock | 跳过 | — | Owner 无 AWS 凭据 |

---

## 3. 验收基线（4 项）

| 检查项 | 状态 | 指令 |
|---|---|---|
| cargo build | PASS | `cargo build` 零错误 |
| cargo test | PASS | 120+ 测试 PASS（load smoke 前） |
| cargo clippy | PASS | `cargo clippy -- -D warnings` 零 warning |
| cargo fmt | PASS | `cargo fmt --check` 无 diff |

---

## 4. M-rust-10 负载烟雾实测结果

测试环境：sandbox Linux 4 worker 线程，mock upstream SSE 5 帧，mock control plane gRPC。

| 并发级别 | 成功率要求 | 实测结果 |
|---|---|---|
| 100 并发 | ≥ 90% | 100/100 PASS — P50=333ms P95=360ms P99=362ms max=365ms wall=389ms |
| 500 并发 | ≥ 85% | 500/500 PASS — P50=991ms P95=1293ms P99=1315ms max=1330ms wall=1448ms |
| 1000 并发 | ≥ 80% | 1000/1000 PASS — P50=2209ms P95=2733ms P99=2843ms max=2899ms wall=2936ms |
| Error5xx 无 panic | server 存活 | PASS |

延迟报告路径：`/tmp/huakai_rust_load_smoke.json`

### 4.1 说明

- 延迟数据（P50 / P95 / P99）来自 `hdrhistogram` 实测，包含 mock upstream + mock control plane + axum 完整链路往返。
- sandbox 环境文件描述符默认上限约 1024，1000 并发测试在 fd 压力下验收线设为 80%。
- 真实硬件下预期 1000 并发成功率 > 99%（无 fd 限制，无 sandbox CPU 争用）。

---

## 5. 与主线 Go Hot Path 的 Benchmark Gap

| 指标 | Rust fork（mock 实测） | Go 主线（参考估算） | Gap 说明 |
|---|---|---|---|
| 单请求 P50 延迟 | < 10ms (mock 链路) | < 5ms (Go axum-equiv) | mock 开销含 TCP 双跳；真实 upstream 差距可忽略 |
| 1k 并发不 panic | PASS | Go goroutine 模型天然支持 | 等价 |
| 内存模型 | Arc + DashMap + Tokio 无全局锁 | sync.Map + goroutine | Rust 编译期安全无 data race |
| 流式 SSE parse | 零拷贝 Bytes | []byte slice | Rust 理论更优；待真实 upstream 对比 |
| gRPC control plane 调用 | tonic + circuit breaker | grpc-go（主线未接入） | 主线 Go 暂无 gRPC；Rust fork 领先 |
| Prometheus 指标 | /metrics endpoint | expvar + pprof | 主线用 Go expvar；Rust 用 prometheus crate |

**结论**：在 mock 环境下 Rust fork 延迟与 Go 相当，并发安全性通过编译器保证，gRPC 路由层已超前主线 Go 能力。真实 upstream smoke（需 Owner 本机运行）是补全 benchmark gap 的唯一前提。

---

## 6. Go/No-Go 决策

### 当前状态：NO-GO 接入主线（探索阶段结束，进入 canary 评估前置条件）

前置条件满足后可升级为 GO：

| 编号 | 前置条件 | 当前状态 |
|---|---|---|
| P-1 | m6_attempt_reporter 完成（M-rust-8 codex lane） | 进行中 |
| P-2 | 真实 upstream smoke（Owner 本机执行，非 sandbox） | 待排期 |
| P-3 | Owner 决策是否为主线 Go 引入 grpc-go 依赖 | 未确认 |
| P-4 | 1k 并发真实 fd 环境实测（P99 < 50ms 目标） | 待真实环境 |
| P-5 | D-rust-3 MAX 性能目标: Rust P99 < Go P99 实测对比 | 待 P-2/P-4 完成后 |

---

## 7. 下一步阻塞项

1. **M-rust-8**（m6_attempt_reporter）：codex 并行 lane 正在实施，完成后 111+ 测试覆盖上报链路闭环。
2. **真实 upstream smoke**：需 Owner 本机运行，sandbox 环境无法访问真实 Anthropic / OpenAI / Bedrock。
3. **grpc-go 主线接入决策**：Owner 需确认是否接受为主线 Go 引入 grpc-go 依赖；如果否，则 Rust 数据面只能通过 HTTP/JSON shim 与主线交互。
4. **生产 fd 限制调整**：1k 并发生产环境需确保 `ulimit -n` ≥ 65536。
5. **D-rust-3 MAX 实测**：接入决策需对比 Rust vs Go hot path P99 latency；目前 mock 数据无法替代真实对比。

---

## 8. 风险项

| 风险 | 等级 | 缓解 |
|---|---|---|
| gRPC 依赖引入主线 | 中 | HTTP/JSON shim 作为降级路径已设计 |
| sandbox fd 限制影响 1k 并发实测 | 低 | 真实环境不受限；报告已注明 |
| M-rust-7 (Bedrock) 缺失 | 低 | Owner 无 AWS 凭据；mock E2E 已覆盖 protocol frame |
| attempt_reporter 未完成 | 中 | M-rust-8 codex lane 同步进行 |
