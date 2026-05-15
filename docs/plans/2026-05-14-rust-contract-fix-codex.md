# 2026-05-14 Go/Rust 合约修复计划 Codex 独立草案

| Owner directive | 「先修 Go/Rust 合约再谈主线接入」；本任务要求独立起草 Rust 数据面原型 `exploratory/rust-core-gateway/merged/` 的 Go/Rust 合约修复计划。 |
|---|---|
| Scope | 只规划 `route.proto` + Rust merged 原型 + Rust mock control plane 的合约修复；中文计划，不写实现代码。 |
| Out of scope | 不改 Go 主线、不接 R-E Go gRPC control plane、不改 DB schema/auth core/billing/quota/部署脚本、不新增运行时依赖、不运行任何 `cargo`/`rustc`/`clippy`/`fmt` 命令。 |
| Success criteria | 三个远端 review 合约问题都有明确修复路径、proto diff 草案、文件 blast radius、测试验收、R-C/R-E sequencing、风险与 Owner 决策点。 |
| Time estimate | 计划：0.5-1 小时；后续实现建议 1-1.5 天，其中 proto/Rust mock 0.5 天、缓存/fail-closed/test 修复 0.5 天、review 与文档 0.5 天。 |
| Blast radius | 中等：会影响 Rust 数据面合约、auth 注入、route cache 行为、listener 失败语义和相关测试；理论上本次不碰 Go。 |
| Clean-room | 本轮只读 HUAKAI 内部 `exploratory/`、`docs/`、`CLAUDE.md`，不涉及非 MIT 参考项目源码，无 clean-room lane guard。 |

## 1. 已观察事实

- `RoutePlan` 现有字段把 `acquisition_token` 放在 field 3，`credentials_handle` 放在 field 7，没有真实上游凭据材料字段；`AttemptReportRequest` field 4 继续回传 `acquisition_token`。证据：`exploratory/rust-core-gateway/merged/proto/route.proto:40`、`:43`、`:47`、`:69`、`:73`。
- `proxy_engine.rs` 的 `apply_plan_auth()` 当前把 `planned.acquisition_token` 解成 UTF-8 并拼 `Authorization: Bearer ...`。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine.rs:743`、`:754`、`:761`。
- `attempt_reporter.rs` 把 `planned.acquisition_token` 用于 idempotency key，并原样写入 `AttemptReportRequest.acquisition_token`。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter.rs:234`、`:236`、`:242`、`:337`、`:342`。
- `account_planner.rs` 缓存整个 `RoutePlan`，并从缓存命中直接构造新的 `PlannedAttempt`，复用同一个 `acquisition_token`。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:191`、`:203`、`:228`、`:232`、`:270`、`:292`。
- `route_client.rs` 也缓存整个 `RoutePlan`，命中后跳过 control plane route query。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs:118`、`:119`、`:134`、`:136`、`:241`、`:268`、`:271`。
- listener 在 control plane 不可用时调用 `echo_response()`，把请求体作为成功响应返回。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs:87`、`:96`；`echo_response()` 本身在 `proxy_engine.rs:415`。
- `mock_control_plane.rs` 当前给同一 plan 填 `acquisition_token` 和 `credentials_handle`，但没有独立上游 secret。证据：`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs:335`、`:339`、`:343`。

## 2. 核心建议

推荐路线：**控制面在每次 `RoutePlan` 中下发已解析的上游 auth material，但必须放入独立字段；Rust 数据面绝不把 `acquisition_token` 当上游凭据。**

理由：

- Rust 数据面最终必须把某种 secret 放进上游请求头；如果只给 `credentials_handle`，Rust 仍需要额外 resolution。
- 单独新增 credential-resolution RPC 会把一次转发拆成 route query + credential resolve 两次 RPC，增加 latency、超时点和 TOCTOU 风险；lease 与凭据版本若不在同一 control-plane 决策里绑定，失败/释放/幂等更难证明。
- Go control plane/credential hub 才应拥有真实凭据解析权；Rust 数据面保持无持久 credential store，只消费 per-attempt resolved material。
- 把 material 与 `acquisition_token` 拆开后，`acquisition_token` 继续只服务 lease/settle/idempotency，上游鉴权只读 `upstream_auth`。

不推荐路线：

- 不推荐把 `credentials_handle` 直接当 bearer token；它是引用，不是 secret material。
- 不推荐 Rust 在本轮新增 credential store 或读取 Go/DB credential；这会越过 R-E 边界。
- 不推荐继续复用 `acquisition_token`，即使 mock 环境能通过，也会泄漏内部 lease token 给 vendor。

## 3. Proto Diff 草案

草案采用可扩展 message，而不是只加裸 `bytes upstream_auth_material`。裸 bytes 足够修 bug，但下一步 API key header、query signing、OAuth token expiry、provider-specific auth 都会继续扩字段；message 让 field 13 保持稳定。

```proto
message UpstreamAuthMaterial {
  // bearer_token/header_value/query_param/signing_context/no_auth
  string material_kind = 1;
  // For bearer_token: raw token bytes, without "Bearer ".
  // For header_value: raw header value, header name below.
  bytes material = 2;
  // Optional. Required when material_kind == "header_value".
  string header_name = 3;
  // 0 means unknown/not provided. Data plane must not cache beyond this.
  uint64 expires_at_unix_ms = 4;
}

message RoutePlan {
  string route_plan_id = 1;
  string account_id = 2;
  bytes acquisition_token = 3;     // internal lease/settle token only
  string vendor = 4;
  string upstream_model = 5;
  string vendor_endpoint = 6;
  string credentials_handle = 7;   // control-plane/audit reference only
  string auth_mode = 8;
  uint64 route_ttl_ms = 9;
  uint64 attempt_deadline_ms = 10;
  uint64 max_body_bytes = 11;
  uint64 max_stream_frame_bytes = 12;
  UpstreamAuthMaterial upstream_auth = 13;
}
```

合约语义：

- `acquisition_token`：必须非空；只能用于 attempt report、lease settle、idempotency、release/reconcile，不得进入 upstream HTTP header/body/query/log。
- `credentials_handle`：必须非空；只用于审计、调试和未来 resolution 追踪，不得当 secret 使用。
- `upstream_auth.material`：对 `auth_mode=bearer` 必须非空；`apply_plan_auth()` 只从这里构造 `Authorization`。
- `upstream_auth.expires_at_unix_ms`：当前 Rust 不做长期缓存，但应在 invalid plan 校验里拒绝明显过期 material；允许 0 表示 mock/未知。
- 所有 redaction 规则把 `upstream_auth.material` 视为 secret，等价或高于 `acquisition_token`。

## 4. 修复 1 执行计划：拆开 lease token 与上游凭据

执行顺序：

1. 修改 `proto/route.proto`，新增 `UpstreamAuthMaterial` 与 `RoutePlan.upstream_auth = 13`，不改 field 3/7 的含义，不重排字段。
2. 更新 Rust 生成/引用后的结构使用点：`PlannedAttempt` 可保留完整 `route_plan`，但显式增加或读取 `upstream_auth`，避免调用方继续摸 `acquisition_token`。
3. 修改 `planned_attempt()` 校验：
   - `account_id` 非空。
   - `acquisition_token` 非空。
   - `credentials_handle` 非空。
   - `auth_mode=bearer` 时 `upstream_auth.material_kind == "bearer_token"` 且 material 非空。
   - `upstream_auth.material` 不得等于 `acquisition_token`，用于防止 mock/test 继续混用。
4. 修改 `apply_plan_auth()`：只读 `upstream_auth.material`，拼 `Authorization: Bearer <material>`；完全禁止从 `acquisition_token` 构造 header。
5. 修改 `mock_control_plane.rs` 和测试 fixture：mock plan 必须使用不同值，例如 `acquisition_token=b"lease-token-mock-1"`、`upstream_auth.material=b"upstream-secret-mock-1"`。
6. 修改测试断言：
   - upstream 收到 `Bearer upstream-secret-*`。
   - attempt report 回传 `lease-token-*`。
   - 构造一个 `upstream_auth.material == acquisition_token` 的 bad plan，期望 `bad_route_plan`。

## 5. 修复 2 执行计划：RoutePlan Cache 禁用与未来拆分

推荐：**本轮禁用整个 `RoutePlan` 生产缓存；不要在当前 proto 下做“半安全缓存”。**

理由：

- 当前 `RoutePlan` 同时包含 per-request lease token 和即将新增的 per-attempt upstream secret。缓存整个 plan 无论 TTL 多短，都会复用 lease/secret。
- Rust 本地无法在只缓存 vendor/endpoint/model/credentials_handle 后重新获取 lease，因为现有 RPC 只有 `RouteQuery -> RoutePlan`，没有 `AcquireAttempt` 或 `ResolveCredential`。
- route cache 当前默认 `0`，禁用缓存是行为上最小、合约上最正确的修复；性能问题留给 R-E control plane 设计，而不是在原型里制造错误语义。

本轮动作：

1. `route_client.rs`：`query_route()` 每次都调用 control plane；删除或 no-op `cache_get/cache_put` 路径，保留 circuit breaker/retry。
2. `account_planner.rs`：`plan()` 每次都调用 `route_client.query_route()`；删除或 no-op planner 侧 `DashMap`。
3. `config.rs/lib.rs`：`route_cache_ttl_ms` 可以暂时保留为 ignored/deprecated 配置，启动日志提示“RoutePlan cache disabled because plans carry per-attempt lease/auth material”；不要在本轮删除配置，避免测试和脚本大范围 churn。
4. 测试更新：原 `route_cache_ttl_enabled_reuses_plan` 和 `account_planner_extracts_fields_and_reuses_short_ttl_cache` 改为“即使 TTL > 0，同一请求也会 query control plane 两次，且 acquisition token 不复用”。

未来 split-cache 方向（不在本轮实现）：

- 新增 cacheable `RouteDecision`：`vendor/upstream_model/vendor_endpoint/credentials_handle/auth_mode/body limits/stream limits`，不含 `route_plan_id/acquisition_token/upstream_auth`。
- 新增 per-attempt `AttemptLease` 或 `AcquireAttempt`：返回 `route_plan_id/account_id/acquisition_token/upstream_auth/attempt_deadline_ms`。
- 若 Owner 要现在就保留缓存，必须把本轮 scope 升级为 proto/Rust mock 的双 RPC contract 设计；否则不可安全缓存。

## 6. 修复 3 执行计划：删除 Echo Fallback，Fail Closed

推荐：**生产 listener 不提供 fail-open/echo 开关；control plane 不可用时返回 503 标准降级错误。**

具体行为：

- `PlanningError::ControlPlane(_)`：返回 `503 Service Unavailable`。
- 响应头：`content-type: application/json`、`x-huakai-request-id: <request_id>`；可选 `retry-after: 1` 仅在 circuit breaker open 或超时场景加。
- 响应体第一版保持轻量：`{"error":"control_plane_unavailable","request_id":"..."}`。如果要严格沿用现有 helper，可先 `{"error":"control_plane_unavailable"}`，但推荐把 request_id 放入 body 便于客户端排障。
- attempt report：synthetic planning error 的 `http_status` 应从当前 BAD_GATEWAY 语义修为 `503`，`error_class=control_plane_error` 保持。
- `PlanningError::InvalidRoutePlan(_)`：继续 `502 Bad Gateway` + `bad_route_plan`，因为这是控制面返回非法计划，不是临时不可用。
- `echo_response()`：从 listener 路径移除；如仅测试需要，可保留私有/test helper，但不要被生产请求引用。
- 配置开关：不新增 `fail_open_echo`。现有 `HUAKAI_MOCK_UPSTREAM_ENDPOINT` 已能覆盖本地 mock upstream 测试，不应把“伪造成功响应”做成生产配置。

测试更新：

- `listener_falls_back_to_echo_when_control_plane_is_down` 改名为 `listener_fails_closed_when_control_plane_is_down`。
- 断言 status 503、body 不是原请求体、包含 `control_plane_unavailable`、request id header 存在。
- 增加一条“请求体带敏感字段时 503 body 不回显”的场景，防止回归。

## 7. Blast Radius

预期会碰：

- `exploratory/rust-core-gateway/merged/proto/route.proto`：新增 `UpstreamAuthMaterial` 与 field 13。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`：`PlannedAttempt` 校验、cache no-op、fixture 期望调整。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine.rs`：`apply_plan_auth()` 使用 `upstream_auth`；listener 不再 import/use `echo_response` 后可考虑收窄可见性。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`：route plan cache no-op/删除，保留 retry/circuit breaker。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs`：control-plane error fail-closed 503。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter.rs`：理论上不改语义；只可能改 synthetic status 测试或增加“不把 auth material 写入 report”的断言。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`：mock route plan 填 distinct lease token 与 upstream auth material。
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`、`src/lib.rs`：`route_cache_ttl_ms` deprecated/no-op 提示或构造参数清理。
- 相关 Rust tests：`proxy_engine_test.rs`、`route_client_test.rs`、`attempt_reporter_test.rs`、`listener_test.rs`、`smoke.rs`、`load_smoke.rs`、`observability_test.rs`。

理论上不碰：

- Go 主线 `backend/`：真实 Go gRPC control plane 属于 R-E scope，本轮只改 proto + Rust + mock。注意：proto 是跨语言合约，R-E Go 实现必须后续对齐，但本轮不直接改 Go。
- `LICENSE`、真实 secrets、DB schema、auth core、billing ledger、quota enforcement、deployment scripts。

## 8. R-C Lane 2/3 与 Mimicry Sequencing

- 合约修复必须排在 R-C Lane 2/3 的 ProxyEngine transport 抽象接入之前。原因：transport/mimicry 最终复用 `normalize_upstream_headers()`/`apply_plan_auth()` 这条路径；若先接 mimicry，会把内部 lease token 通过更真实的 transport 泄漏给 vendor。
- 可并行的只有不触碰真实转发路径的工作：profile loader、template validation、capture diff harness、transport trait 草图。
- 不可并行的工作：把 mimicry backend 接入 `forward_planned()`、跑真实 upstream smoke、R-E mainline shadow/canary。它们必须等三项合约修复落地并经测试确认。
- R-E Go control plane 实现也应等待 proto field 13 定稿；否则 Go/Rust 两边会围绕错误 `RoutePlan` 生成代码。

## 9. Failure Modes 与 Mitigation

| Failure mode | Impact | Mitigation |
|---|---|---|
| 继续把 `acquisition_token` 当 bearer | 内部 lease token 泄漏给 vendor，settle/idempotency 语义被破坏 | `apply_plan_auth()` 只读 `upstream_auth`; bad plan 测试禁止两个 token 相等 |
| RoutePlan 仍被缓存 | 并发 slot、计费 release、attempt report 幂等全部可能错位 | 本轮禁用 full-plan cache；TTL 配置只保留兼容，不生效 |
| `upstream_auth.material` 被日志/metrics 暴露 | 真实上游凭据泄漏 | redaction 增加 upstream auth secret；测试检查 debug 字符串/错误不含 material |
| 禁用 cache 后 control plane 压力上升 | Rust 原型吞吐下降 | 接受为正确性代价；未来用 `RouteDecision` + `AttemptLease` split-cache 解决 |
| 503 body 回显用户请求 | 请求内容泄漏且伪造成功 | 删除 echo fallback；新增敏感 body 不回显测试 |
| Proto message 太窄 | 后续 API key/header/query signing 又要破坏性变更 | 用 `UpstreamAuthMaterial` message 而非裸 bytes |

## 10. Owner 决策点

1. 是否接受推荐方案：在 `RoutePlan` 内新增 `upstream_auth` resolved material，而不是新增 Rust credential-resolution RPC？
2. 是否接受 `UpstreamAuthMaterial` message 形态？我建议不要只加裸 `bytes upstream_auth_material`。
3. 是否同意本轮完全禁用 full `RoutePlan` cache？如果必须保留缓存，需要把 scope 扩大到 `RouteDecision`/`AttemptLease` 双合约。
4. fail-closed 错误体是否采用 `{"error":"control_plane_unavailable","request_id":"..."}`？还是先沿用最小 `{"error":"control_plane_unavailable"}`？
5. R-E 前是否要求 control plane gRPC 使用 mTLS/Unix socket/本机绑定等 secret-in-transit 防护？本轮原型可不实现，但主线接入前应定。

## 11. Pre-Execution Checklist

1. Owner 批准 synthesized plan，明确上述 5 个决策点。
2. 确认本轮只改 proto/Rust/mock/tests，不改 Go 主线。
3. 暂停 R-C Lane 2/3 对 `ProxyEngine.forward_planned()` 的接入，允许 profile/capture-only 工作继续。
4. 先改 proto 与 mock fixture，使测试数据中 lease token 和 upstream secret 明显不同。
5. 再改 auth 注入与 plan 校验，确保错误路径 fail closed。
6. 再禁用 full-plan cache，并更新 TTL 相关测试。
7. 最后改 listener fallback 测试与 synthetic report status。
8. 实现阶段才运行 Rust checks；本计划阶段严格不运行 `cargo build/check/test/clippy/fmt` 或 `rustc`。

## 12. 本计划阶段已读文件

- `CLAUDE.md`
- `docs/01_PROJECT_BRIEF.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `exploratory/rust-core-gateway/merged/proto/route.proto`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/listener.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/attempt_reporter.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mock_control_plane.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/config.rs`
- sampled tests under `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/`
