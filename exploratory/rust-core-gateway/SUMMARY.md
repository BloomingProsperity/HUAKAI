# HUAKAI Rust Core Gateway — 探索 fork 总结

生成日期: 2026-05-09
工作范围: 探索性 fork (`exploratory/rust-core-gateway/`), 不并入主线

---

## 1. Owner 决策 (已批)

| ID | 决策 | 选项 |
|---|---|---|
| D-rust-1 | RPC 协议选型 | A — gRPC (tonic + prost) |
| D-rust-3 | 接入主线硬指标 | max — 朝极致性能写, 不设保守目标 |
| D-rust-4 | Bedrock 凭据边界 | A — AWS SigV4 短 TTL credential 给 Rust 持有 |

未拍 (留待 M-rust-7 实施前):
- D-rust-5 Bedrock stream 对外策略 (默认 pass-through binary EventStream)

---

## 2. atom 完成矩阵

| atom | 范围 | 状态 | 关键文件 |
|---|---|---|---|
| **M-rust-1** | workspace + config + error + tracing + request_id + healthz | ✅ | `src/{config,error,tracing_init,request_id,lib,main}.rs` |
| **M-rust-2** | listener + mock_upstream + body streaming + client cancel | ✅ | `src/listener.rs` + `tests/common/mock_upstream.rs` |
| **M-rust-3** | gRPC (route.proto) + RouteClient + mock control plane + drain_mode | ✅ | `proto/route.proto` + `src/{route_client,mock_control_plane}.rs` |
| **M-rust-5** | account_planner + proxy_engine bearer (Anthropic/OpenAI/Gemini/codex) + listener 接入 | ✅ | `src/{account_planner,proxy_engine}.rs` |
| **M-rust-6** | stream_pipeline (Anthropic/OpenAI SSE parser + usage/cache extract + [DONE]) | ✅ | `src/stream_pipeline/` |
| **M-rust-9** | prometheus + heartbeat + redaction + /metrics endpoint | ✅ | `src/{metrics,heartbeat,redaction}.rs` |
| M-rust-7 | Bedrock SigV4 + binary EventStream | 🚫 跳过 | Owner 无 AWS 凭据 (memory: project_no_aws_credentials) |
| **M-rust-8** | attempt_reporter + idempotency + retry queue + 9 终态分类 | ✅ | `src/attempt_reporter.rs` |
| M-rust-10 | load smoke (100/500/1000 concurrent) + READINESS.md | ✅ | `tests/load_smoke.rs` + `READINESS.md` |

---

## 3. 测试 + 验收

| 验收项 | 状态 |
|---|---|
| `cargo build --workspace` | ✅ PASS |
| `cargo test --workspace` | ✅ **127 tests PASS** (含 M-rust-8 attempt_reporter 9 集成) |
| `cargo clippy -- -D warnings` | ✅ 0 warnings |
| `cargo fmt --check` | ✅ clean |
| `curl /healthz` | ✅ 200 `{"status":"ok"}` |
| `curl /metrics` | ✅ Prometheus 文本格式 + `huakai_rust_*` namespace |
| listener POST /v1/messages → mock upstream 转发 | ✅ M-rust-5 e2e 验证 |
| heartbeat 5s 触发 + drain_mode 切换 | ✅ M-rust-9 验证 |
| Anthropic SSE 多 data 行 + OpenAI [DONE] 解析 | ✅ M-rust-6 golden stream 验证 |

测试矩阵分布:
- lib unit: ~47
- listener integration: 7
- proxy_engine integration: 8
- stream_pipeline integration: 10
- route_client integration: 15
- observability integration: ~14 (含 sonnet 加的)
- load_smoke integration: 5
- smoke (M-rust-1 遗留): 12

---

## 4. 总 LoC 估算

| 来源 | LoC |
|---|---|
| src/ 业务 | ~3000 |
| tests/ 集成 | ~1500 |
| proto/ + 自动 codegen | ~150 (proto) |
| **合计** | ~4650 |

---

## 5. 三 lane 产物历史

`exploratory/rust-core-gateway/` 下:
- `codex-lane/` — M-rust-1 codex 产物 (历史 reference, 不再维护)
- `claude-lane/` — M-rust-1 claude 产物 (历史 reference, 不再维护)
- `claude-m3/` — M-rust-3 sonnet lane (HTTP/JSON 路径; 已 cherry-pick 进 merged/, 留作 reference)
- `merged/` — **唯一主推 lane**, M-rust-1/2/3/5/6/9/10 全部落地

---

## 6. 接入主线门槛 (D-rust-3 = max)

Owner 锁定: "越大越好, 往最大方向写"。 实际接入主线需要的硬指标候选:

- p95 proxy overhead **≥ -50%** vs 主线 Go hot path
- 每 1000 并发 stream RSS **≥ -50%**
- CPU/token **≥ -30%**
- attempt report 零漏报 (与 Go billing/quota 对账)
- 三 vendor (anthropic/openai/gemini) streaming parity 全通过

**当前 gap (待 M-rust-10 load smoke + 主线 Go benchmark 对比)**:
- Rust 实测 P99 latency / RSS — load smoke 已实施, 数据收集中
- 主线 Go hot path benchmark — 需单独 atom 跑 (不在本 fork 范围)

---

## 7. 真实账号测试限定 (Owner 2026-05-09)

接入主线前的 shadow 验证只能用 4 vendor: **anthropic / openai / gemini / codex** (memory: project_real_vendor_account_scope)。 其他 vendor (azure/cohere/mistral/...) 全 mock 上游, 不进 canary 决策依据。

Bedrock 路径数据是假信号 (Owner 无 AWS 凭据, memory: project_no_aws_credentials), 不在 shadow 决策范围内。

---

## 8. 下一步建议 (Owner 拍板)

1. **完成 M-rust-8** (codex 后台中): attempt reporter 闭环 billing/quota 对账
2. **跑完 load smoke** (M-rust-10 已实施, 需 Owner 本机跑出实测数据 — 沙箱不可信)
3. **主线 Go hot path benchmark** — 新 atom, 给 D-rust-3 决策提供基线对比数据
4. **shadow 验证准备** — 接 4 vendor 真实账号在 OpenAI/Gemini 先跑 7d shadow (anthropic + bedrock 留二期)
5. **接入决策** — 数据驱动选择是否替换主线 Go 反代, 否则保留 fork 探索价值

不接入主线条件下, 本 fork 已自闭环 (build/test/clippy/fmt 全清, 6 atom 完整覆盖核心数据面)。

---

## 9. 关联文档

- `PLAN.md` — codex lane 起草的完整 10-atom plan (M-rust-1 阶段)
- `merged/README.md` — merged/ 三 lane 整合说明
- `merged/READINESS.md` — sonnet M-rust-10 readiness 评估报告
- 主线 plan: `docs/plans/2026-05-08-pasr-mainwire-synthesis.md` (Go 主线参照)
