# W11-A D-1b Phase 2 Synthesis (2026-05-24)

> CLAUDE.md #10 parallel-draft synthesis: 合并 Claude draft (8 decisions D-13..D-20) +
> Codex draft (5 acceptance gates P2-A1..A6 + 5 decisions OD-1..OD-5 + critical 2A/2B
> split) + Specifier-dig L1 report (5 source projects recency + behavior paraphrase +
> 2 missing fusion-upgrade citations)。
>
> **Owner 决策**: 看 §3 Critical Finding (Phase 2 拆 2A + 2B 的根本结构) + §4 决策矩阵
> (10 项 D-13..D-22 / OD-1..OD-5, 部分已合并)。

---

## §1 Phase 2 目标 + Phase 1 兜底回顾

**Phase 2 目标**: Go 控制面成为客户端凭据→tenant 的**唯一权威派源**, Rust 数据面进入
"双写对账"阶段验证 Go 派生的正确性, 为 Phase 3 Manual First 永久下线做数据驱动准备。

**Phase 1 已落地** (commits `3391d3c`, `7e10069`, `07a51bb`, `33dafd7`):
- Rust 写 `RouteQueryRequest.client_credential` 字段 (canonical "bearer:<token>" /
  "x-api-key:<key>"), Go 控制面**完全忽略**
- Manual First 静态 hash→tenant 在 dev/staging 兜底; production 模式 startup fail-fast
- 7 个守门已 mutation-tested (A1/A3/A4/A5/D-9/D-11/D-12)
- β scheme 锁: Rust 永不持身份权威, 永不读 x-tenant-id header
- BoringTlsConnector HybridStream: dev/test mock upstream (http) 也能跑

**Phase 2 必须保留的 Phase 1 不变量**:
- A3: x-tenant-id 永不被信任 (即使 Phase 2 提供 Go 派 tenant)
- A4: raw credential 永不入 log / Debug / span field
- β scheme: Rust 仍不持身份权威 — 派 tenant 的责任完全在 Go

---

## §2 现状评估 (Go / Rust 各自需要改什么)

### 2.1 Go 控制面 (Codex 实读源码确认的当前状态)

- HUAKAI 仓库**没有真正实现 `huakai.route.v1.RouteQueryService` 的 Go gRPC server**
- `backend/internal/router/route_plan.go` 是 Go 内部 router 实现, 与 proto RoutePlan
  同名但**完全独立**, 不是 proto generated
- Go 已有 `auth.APIKeyResolver.Resolve(ctx, *http.Request)` 复用现成接口 (table-backed
  `api_keys` + `users` + `tenants`), 接受 `Authorization: Bearer <key>` 形式
- `backend/internal/router/router.DefaultRouter` 已要求 `RequestContext.TenantID != 0`
- `backend/go.mod` 当前**没有** `google.golang.org/grpc` 或 `google.golang.org/protobuf`
- 冻结包 (不能加新文件): `backend/internal/gatewayhttp`, `gateway`, `proto`

**Phase 2 Go 侧 = 从零起 RouteQueryService**, 但可复用大量现有 auth/router/registry/
selector/vault 模块。Codex 建议新包 `backend/internal/routecontrol/`。

### 2.2 Rust 数据面 (Phase 2 待改)

- 接收 RoutePlan 中 Go 派的 tenant 字段, 与 Manual First 派 tenant 比对
- 选择信任策略 (D-14): 信 Go / 二者一致才用 / per-tenant 配置
- 不一致时记录 telemetry (D-15)
- 测试 mock_control_plane 升级 (D-19): mock 须能 emit 新派 tenant 字段
- 灰度 flag 让 Rust 在 staging 先夹生 (D-16)

---

## §3 ⚠️ Critical Finding: Phase 2 必须拆 2A / 2B (Codex 独立发现)

**Codex 实读 `backend/internal/billing/billing.go` 后发现**:
`RouteQueryRequest` 当前字段含 request_id, tenant_id, requested_model, request_protocol,
stream, client_deadline_ms, previous_attempts, capability_hints, client_credential。
**不含** `billing.ClaimGate.Reserve` 所需的: normalized_payload_hash, idempotency_key,
billing_policy_version, request_class, predicted_cost inputs。

**含义**: Phase 2 不能一次性做完。必须拆:

### Phase 2A — Identity-authoritative RouteQuery gate (安全可立做)
- Go 认证 `client_credential`, 拒 mismatch, 派 tenant 给 registry/router/selector
- 实现不返回真实可计费的 RoutePlan; production 模式下若被调返
  `codes.FailedPrecondition` + `route_contract_incomplete`
- 单独可上线作为 W11-A D-1b 阶段验收门, 不动 money path

### Phase 2B — Production billable RoutePlan (需 Owner 决 claim contract)
- 添加 proto 字段让 RouteQuery 携带 / 派生 `ClaimGate.Reserve` 所需输入
- 真实完成 reserve/claim/select/resolve credential 序列, map 到 `route.v1.RoutePlan`
- 涉及 billing ledger + quota + acquisition 账户, 高风险, 需独立 spec + Owner 批

**推荐**: Phase 2A 先 land 证明 identity 路径; Phase 2B 后续单独 plan + Owner 批准
claim contract 后才动。

---

## §4 Owner 决策矩阵 (合并 Claude + Codex)

Owner 请逐项 ☐ 选项或 ☐ **全部默认推荐** (双稿大量重合处已默认推荐)。

### D-13 / OD-2: RoutePlan 中 Go 派 tenant 的回流路径 (Phase 2A) + Phase 2B 字段 (待 OD-2 决)

**Phase 2A**:
- (a) **新增 `string derived_tenant_id = 15;` proto 字段** (显式新字段, Rust 双写对账时清晰)
- (b) 复用 `RoutePlan.account_id` 同义为 tenant_id
- 推荐 (a) — Phase 3 可删字段不破坏旧 client; account_id ≠ tenant_id 语义复用会污染

**Phase 2B (OD-2)**: 是否批准添加以下 proto 字段让 RouteQuery 可走 production billable
- (a) **批准添加**: `endpoint_family`, `idempotency_key`, `normalized_payload_hash`,
  `billing_policy_version`, `request_class`, 及成本/定价输入字段
- (b) 不批准 — Phase 2 永远只完成 Phase 2A identity gate; Phase 2B 留 Phase 3+ 决定
- (c) 拆 RouteQuery 与 billing — 走独立 settlement 设计证明 acquisition token 不会
  bypass quota/ledger
- 推荐: **后续单独 plan**, Phase 2 本身先只做 (a) (Phase 2A field), Phase 2B 字段
  待 Owner 单独 plan 决定; **不推荐 (c)** 因 money-path 重设计 risk 极高

### D-14 / OD-4: Rust 对账策略 (Manual First vs Go-derived) — 双稿已收敛

**双稿一致**: 拒 mismatch (fail-closed):
- 空 legacy tenant: 接受, 用 Go 派 tenant
- 数字 legacy tenant = Go 派: 接受, count reconciliation match
- 非空 mismatch / 非数字: **reject (`codes.PermissionDenied` / `tenant_id_mismatch`)**

**Owner 选项**:
- (a) **接受双稿一致推荐 — reject mismatch**
- (b) "warn but continue" 不拒 — 静默保留 Rust 数据面 identity authority (反 β scheme)
- 推荐 (a) — 实质双稿一致, (b) 反 β scheme

### D-15: 不一致告警 telemetry

- (a) **Prometheus counter** `huakai_client_credential_tenant_mismatch_total{kind, source}`
  (kind = bearer / x-api-key; source = match / mismatch / sole-go / sole-manual)
- (b) tracing warn 含 `manual_tenant_fp` + `go_tenant_fp` (fingerprint 防 PII)
- 推荐 (a) + (b) 并存; attempt_report 不加 `tenant_drift` 字段 (Go 已知道自己派的, 无新信息)

### D-16 / OD-3: Go 侧凭据→tenant 派算法 — Codex 推荐有详细路径

**双稿一致 Phase 2A 实现**:
- **复用 `auth.APIKeyResolver.Resolve`** 通过 routecontrol adapter, 把
  `bearer:<secret>` 和 `x-api-key:<secret>` 都 normalize 成
  `Authorization: Bearer <secret>` 进 resolver
- 不动 `internal/auth` core, 不加 `ResolveToken` 方法
- Phase 2A 不引入新 DB schema (`tenant_credentials` 表)
- Phase 2B 若需要 DB lookup + cache 两层, 单独 spec

**Owner 选项**:
- (a) **接受双稿一致 — Phase 2A 用 adapter 复用 APIKeyResolver, 不动 auth core**
- (b) 允许 Phase 2A 改 `internal/auth` core 增 `ResolveToken` (高风险)
- 推荐 (a)

### D-17 / OD-5: Phase 2 → Phase 3 退出条件 (Manual First 永久下线时机)

- (a) **数据驱动**: 连续 7 天 mismatch rate < 0.01% + 所有现 tenant 已迁入 Go DB
- (b) Owner 显式 manual 决定
- (c) Mismatch alert 0 累计 1 周 + Manual First 默认 OFF 已生效 30 天
- (d) **混合 (推荐)**: 数据指标 (a) + Owner sign-off, 双重门
- 推荐 (d) — 量化 + manual gate; smoke 窗口长度 Owner 设

### D-18: Go 侧 spec 拆分

- (a) Phase 2 plan 同时盖 Go + Rust, 一份 plan
- (b) **Phase 2 (本文件) 已盖 cross-cutting (proto / telemetry / 退出条件) + Codex 同时
  在 §5-§9 提供 Go spec 的执行细节**
- 推荐 (b) **变种**: 本 synthesis = Phase 2A Go + Rust 共享决策, Phase 2B 单独 plan 启动

### D-19: Rust 集成测试 mock_control_plane 是否升级

- (a) **Yes**: `mock_control_plane.rs::mock_route_plan` 增 derived_tenant_id 字段, 让
  Rust e2e 测试能验证对账逻辑
- (b) No: 仅单元测试
- 推荐 (a) — 单元测试守门 wiring 不够, mutation 测试 (CLAUDE.md #14) 需 fixture

### D-20: 凭据轮换 (rotation) 行为

- (a) Manual First file watch 让 keys file 修改后 resolver 自动 reload
- (b) **Rotation 完全走 Go control plane DB update, Manual First 不刷**
- 推荐 (b) — Manual First 是 Phase 2 过渡桥, 不引入 file watch 复杂度

### OD-1: 新 Go runtime 依赖 (grpc + protobuf)

- (a) **批准** `google.golang.org/grpc` + `google.golang.org/protobuf` — 启用真实 gRPC server
- (b) 推迟到 Phase 2B 才加, Phase 2A 只用 package-level service tests (不启网络 server)
- (c) 完全不加, 永远用 Rust mock control plane 测 Rust 侧
- 推荐 (a) — Phase 2A identity gate 的真实价值在 Rust-Go integration, mock-only 测试
  无法守门生产路径; AGENTS.md 跑 dependency-license-auditor + cargo-deny 前置审批

### D-21 (新, 从 Codex P2-A2 提取): mismatch error 类型

- (a) **`codes.PermissionDenied`** + error code `tenant_id_mismatch`
- (b) `codes.Unauthenticated`
- 推荐 (a) — Codex L1 spec 一致, `PermissionDenied` 更精确表达 "你的凭据合法但派的 tenant
  与你声称的不符"

### D-22 (新, 从 Codex P2-A3 提取): matching legacy tenant 是否 emit reconciliation 计数

- (a) **Yes** — count `tenant_match_total` 作为 Phase 3 退出条件的数据来源
- (b) No, 仅认证通过
- 推荐 (a) — D-17 (a) 数据驱动退出需要这个 metric

---

## §5 双稿已 lock, Owner 不需决策的项

- **保留 Phase 1 β scheme**: Rust 仍不持身份权威 — 双稿一致
- **保留 A1/A3/A4 守门**: Phase 2 不改这三个守门
- **proto 字段类型用 string**: derived_tenant_id 与 tenant_id 同类型
- **不删 Rust Manual First in Phase 2**: 留 Phase 3 cutover gate 后才删
- **冻结包不动**: Phase 2 不在 `backend/internal/{gatewayhttp, gateway, proto}` 加新文件
- **生成 proto 不放 `internal/proto`**: 用新包 `backend/internal/routepb`
- **新增 cohesive Go 包**: `backend/internal/routecontrol` 一个 package 一个职责
- **Default-off feature flag**: `HUAKAI_ROUTE_CONTROL_ENABLED=false` (production 启用需
  UDS socket path; dev/test 允许 HTTP loopback)
- **`x-api-key` 在 Go 侧 normalize 成 `Authorization: Bearer <secret>`** (复用现有 resolver)
- **Phase 1 全部 7 个守门测试不动** (向后兼容)

---

## §6 文件触点 (Go + Rust 全列)

### 6.1 Go 控制面 — Phase 2A (Codex §File Structure)

**Create**:
```
backend/internal/routecontrol/credential.go         -- canonical 解析 + 不导出 secret + fingerprint 助手
backend/internal/routecontrol/service.go            -- RouteQuery orchestration: 解析→认证→对账→registry→router
backend/internal/routecontrol/errors.go             -- 稳定错误码 (missing/invalid/mismatch/auth_backend/route_contract_incomplete)
backend/internal/routecontrol/plan_mapper.go        -- 把 Go router 输出 map 成 route.v1.RoutePlan (Phase 2A: 仅 test-stub plan)
backend/internal/routecontrol/credential_test.go    -- parser + redaction 单元测试
backend/internal/routecontrol/service_test.go       -- stub auth/registry/router 验 P2-A1..A5 acceptance
```

**Create if OD-1 approved** (gRPC server):
```
backend/internal/routepb/route.pb.go                -- proto generated (NOT in frozen internal/proto)
backend/internal/routepb/route_grpc.pb.go
backend/internal/routecontrol/grpc_test.go          -- gRPC server e2e
```

**Modify**:
```
backend/go.mod                                      -- +grpc +protobuf (only if OD-1 approved)
backend/internal/config/route_control.go (新)        -- focused 配置文件: HUAKAI_ROUTE_CONTROL_ENABLED 等
backend/internal/config/config.go                   -- 只加 route_control 字段拼接
backend/cmd/gateway/wiring.go                       -- DI auth/registry/router/selector/vault 给 routecontrol
backend/cmd/gateway/control_plane_server.go (新)    -- gRPC server lifecycle (only if OD-1 approved)
```

**Do NOT create** (冻结包):
- `backend/internal/gatewayhttp/*`
- `backend/internal/gateway/*`
- `backend/internal/proto/*`

### 6.2 Rust 数据面 — Phase 2A

```
proto/route.proto                                            -- +derived_tenant_id field 15 (D-13 a)
crates/core_gateway/src/account_planner.rs                  -- 对账逻辑 + 选 final tenant (D-14)
crates/core_gateway/src/mock_control_plane.rs                -- mock_route_plan 增 derived_tenant_id (D-19)
crates/core_gateway/src/metrics.rs                           -- 注册 mismatch counter (D-15)
crates/core_gateway/src/listener.rs                          -- attempt_report 上报源 tenant
crates/core_gateway/src/attempt_reporter.rs                  -- 上报源 tenant 字段
crates/core_gateway/src/config.rs                            -- HUAKAI_CLIENT_AUTH_RECONCILE_POLICY (D-14 / D-21)
crates/core_gateway/src/route_proto/redacting_debug.rs       -- RoutePlan 新字段 Debug 渲染
crates/core_gateway/tests/route_client_test.rs              -- 4 新 e2e (match / mismatch / sole-go / sole-manual)
```

---

## §7 子计划 (Sub-phases) — 合并 Codex 执行序

### Phase 2A.1: Go credential parser + identity errors (Codex Commit 1)

1. 写 parser tests (TDD): empty/bearer/x-api-key/bad-prefix/missing-colon/PII-redact
2. 实现 `ClientCredential` struct (unexported secret) + `ParseClientCredential`
3. 实现 `ResolverRequest()` 把 bearer / x-api-key 都 normalize 成 `Authorization: Bearer`
4. 实现 `Fingerprint()` 助手 (SHA-256 前 8 hex)
5. 错误码: `missing_client_credential`, `invalid_client_credential`, `tenant_id_mismatch`,
   `auth_backend_error`, `route_contract_incomplete`
6. `go test ./internal/routecontrol`

### Phase 2A.2: Go RouteQuery service (no network server, Codex Commit 2)

1. 定义 `routecontrol` 小接口: `AuthResolver` (复用 `auth.APIKeyResolver`)
2. Service 方法接受 local request struct (mirror RouteQueryRequest 字段)
3. 实现身份流: 解析 → 认证 → 对账 legacy tenant → resolve_model → router.Plan
4. 写测试 P2-A1..A5 (TDD)
5. `go test ./internal/routecontrol`

### Phase 2A.3: Rust 数据面 dual-write 接收 (proto + mock_control_plane)

1. proto.RoutePlan 加 `derived_tenant_id = 15` (D-13 a)
2. `mock_control_plane.rs::mock_route_plan` 升级 (D-19)
3. `account_planner.rs::planned_attempt` 接收 derived_tenant_id (旧 mock 行为: 空字符串)
4. Rust 单元测试: 验证 derived_tenant_id 透到 PlannedAttempt
5. cargo test + codex review + commit + push

### Phase 2A.4: Rust 对账逻辑 + telemetry

1. `config.rs::HUAKAI_CLIENT_AUTH_RECONCILE_POLICY` 枚举 (D-14 选项)
2. `account_planner.rs` 对账逻辑: Manual First tenant vs Go derived tenant
3. `metrics.rs` 注册 mismatch counter (D-15)
4. `listener.rs` 选 final tenant 后 attempt_report 透传源
5. 新增 e2e 测试: 4 场景 (match / mismatch / sole-go / sole-manual)
6. cargo test + codex review + commit + push

### Phase 2A.5: Go gRPC server (only if OD-1 approved, Codex Commit 3)

1. 生成 Go proto into `backend/internal/routepb`
2. `routecontrol.Service` impl `routepb.RouteServiceServer`
3. RouteQuery / HealthCheck / Heartbeat impl
4. config.route_control.go 实现 + tests
5. `cmd/gateway/control_plane_server.go` 启动 server
6. Rust-Go cross-validation: Rust 数据面用真实 Go server 而非 mock 跑端到端

### Phase 2B: Production billable RoutePlan (单独 plan, 需 OD-2 批准)

待 Owner 批准 claim contract 后单独 spec + 单独 plan 启动。

### Phase 3 entry (D-17 / OD-5)

按 D-17 (d) 数据 + manual gate 满足后启 Phase 3。

---

## §8 验收门 — 合并 Codex P2-A1..A6 + Claude B1..B5

### P2-A1 Go derives tenant from client credential
Fixture: auth stub returns `Identity{TenantID:7, UserID:70, APIKeyID:700}` for
`bearer:hk_test_route_phase2_good`; RouteQuery has `tenant_id=""`.
Expected: registry/router 收到 tenant=7。无路径用空 tenant 或 Rust legacy tenant。
Mutation: 若 service 传 `req.TenantId` 给 registry → 红。

### P2-A2 Legacy tenant mismatch fails closed
Fixture: auth 派 tenant=7, RouteQuery 带 `tenant_id="8"`。
Expected: 返 `codes.PermissionDenied` + `tenant_id_mismatch`; 下游全不调用; 错误消息无 raw credential。
Mutation: 若 mismatch 降级为 warn → 下游 counter 非零 → 红。

### P2-A3 Matching legacy tenant 双写期允许 (reconciliation)
Fixture: auth 派 tenant=7, RouteQuery 带 `tenant_id="7"`。
Expected: 通过, count `tenant_match_total` 计数 (D-22), 标 "temp until Phase 3"。
Mutation: 若 service 全拒非空 legacy tenant → Phase 1 Manual First 兼容性测试红。

### P2-A4 Canonical `x-api-key:` 走 HUAKAI key 路径
Fixture: 同一假 HUAKAI 明文 key 一次 `bearer:<key>`, 一次 `x-api-key:<key>`。
Expected: 两种 canonical kind 都 normalize 成 `Authorization: Bearer <key>` 进 resolver;
未知前缀 (e.g., `cookie:<key>`) 在 auth 前 fail。
Mutation: 若 x-api-key 被忽略或被当 tenant hint → 红。

### P2-A5 Raw credential 永不入 logs / status / debug
Fixture: raw credential `hk_test_PHASE2_RAW_SECRET_NEVER_LOG_1234567890` 触发 parse error +
mismatch + auth backend error 三类错误。
Expected: `err.Error()`, gRPC status msg, zap log, `%+v` 格式化均不含 raw secret;
可含 `kind=bearer` + SHA-256 前 8 hex。
Mutation: 若错误格式化含 `req.ClientCredential` 或 `secret` 字段 → 红。

### P2-A6 Production billable RouteQuery 不可 bypass claim
Fixture: production config enabled; 缺已批准 claim contract 字段。
Expected: 返 `codes.FailedPrecondition` + `route_contract_incomplete` 在返 RoutePlan 前;
test name 显式说 risk: 没 claim 输入 = 没 billable production plan。
Mutation: 若 code 返 usable RoutePlan 无 claim reservation → 红。

### B-R1 Rust 双写对账正确性 (Claude)
Fixture: Manual First ON 派 tenant `t1`, Go RoutePlan 返 `derived_tenant_id = "t1"`。
Expected: D-14 (a) 信 Go, `tenant_match_total{source=both_match}` +1, 不告警。
Mutation: 删对账逻辑 → counter 不增 → 红。

### B-R2 Rust telemetry 完整性
Fixture: `huakai_client_credential_tenant_mismatch_total` 在 /metrics 可读, 维度 = kind+source。
Expected: counter 维度无 tenant_id (无 cardinality 爆炸); 2 * 4 = 8 个时间序列固定。
Mutation: 删 counter inc → 集成测试断言 metric 增量 = 0 → 红。

### B-R3 Rust mock control plane 能 emit derived_tenant_id (D-19)
Fixture: `mock_route_plan(derived_tenant_id="t-derived")` 返 plan。
Expected: account_planner.PlannedAttempt 含 derived_tenant_id。

### B-R4 Phase 1 A1/A3/A4/A5/D-9/D-11/D-12 不退化
全部 7 个 Phase 1 守门测试在 Phase 2A 改动后继续通过。

---

## §9 引证表 (specifier-dig L1 已 verified, fusion-upgrade delta) — Claude §8 修订

CLAUDE.md #12 first-cite recency check 全部通过 (5/5 90 天内 + 非 archived), specifier-dig
agent 真读了 LiteLLM + envoy-ai-gateway 源码并写了 paraphrased L1 spec:

| 借鉴源 | repo@SHA | 模式 | HUAKAI delta | 维度 |
|---|---|---|---|---|
| **LiteLLM** | `BerriAI/litellm@d04373f4 (branch litellm_internal_staging, pushed 2026-05-24)` | api_key → user_id 通过 SHA-256 digest → in-memory cache → Prisma DB ladder; 二跳 cache→DB 派 user object + db_access throttle (`_types.py:218`, `user_api_key_auth.py:1322-1338`, `auth_checks.py:2519-2576`, `auth_checks.py:1632-1733`) | **HUAKAI delta = 两进程 reconciliation (Go control plane + Rust 数据面双写对账)**, LiteLLM 是单进程 (已做 SHA-256 PII hygiene, 不能 frame 为 PII delta) | 架构 |
| **envoy-ai-gateway** | `envoyproxy/ai-gateway@3b98ccbd (pushed 2026-05-23)` | **NOT 派 tenant from credential** (Claude draft §8 误描) — 实际是 control-plane 把预绑 k8s Secret 中的 credential **物化**到 ExtProc data plane, OIDC 类型走 pre-rotation worker; 数据面 0 lookup, 0 fingerprint, 仅注 `Authorization: Bearer <key>` 到上游 (`internal/controller/backend_security_policy.go:99-275`, `internal/backendauth/api_key.go:18-32`) | HUAKAI delta = 真正 client-credential → tenant 派 vs envoy 的预绑物化; HUAKAI 数据面与控制面 RPC 解耦, envoy 是 xDS 推送 (一次推送, 数据面 cache, 不实时 query) | 架构 |
| **agentgateway** | `agentgateway/agentgateway@0651db0e (pushed 2026-05-23)` | control plane 派 tenant (其 `auth_plane` 模式 — specifier 未深读, 待 follow-up) | (待 specifier 补) Manual First 桥接期 + 数据驱动 sunset SLO (D-17 d) + counter 维度精确 kind+source 不含 tenant_id | 架构 + 生态 |
| **Helicone (helicone)** | `Helicone/helicone@094b210b (pushed 2026-05-18)` | key auth, control plane 是唯一 tenant 派 source (与 `Helicone/ai-gateway` 子 repo 区别 — 后者其实是 helicone 主 repo 的别名指向, specifier 确认 ai-gateway 不存在独立 repo) | β scheme (Rust 完全不持 identity, 比 Helicone Rust 缓存 key→tenant 设计更严) + 凭据 hash fingerprint 入 metric | 架构 + 算法 |
| **labiium/routiium** | `labiium/routiium@12f95de1 (pushed 2026-04-25, 29 天内)` | static map + cache 兜底 (KeyStore 符号, specifier 未深读, 待 follow-up) | Manual First 限 dev/staging + production startup fail-fast (HUAKAI 把过渡桥与永久路径明确分开) | 架构 |
| **one-api** (**漏引补充**) | `songquanpeng/one-api@8df4a267 (pushed 2025-02-21, stale-but-stable)` | 单进程 Gin middleware: peel `sk-` prefix, split `-` 取 channel-override, 调 `ValidateUserToken(key)` 获 `UserId` → push 进 Gin ctx → `Distribute` middleware 用 UserId 查 `CacheGetUserGroup` → 改写 `Authorization: Bearer <channel.Key>` 上游 (`middleware/auth.go:91-150`, `middleware/distributor.go:20-73`) | **直接对比对象**: one-api 是教科书 "control plane 派 tenant from credential + data plane 用 derived identity 选 upstream credential" 但**单进程**; HUAKAI delta = **三阶段架构** (Rust 数据面 + Go 控制面 + dual-write reconciliation), 多进程 skew 是 HUAKAI 的核心架构升级 (one-api 单进程无 skew 可调和) | 架构 |
| **cliproxyapi** (**漏引补充**) | `router-for-me/CLIProxyAPI@21fad9db (pushed 2026-05-21)` | 每个 `Auth` 记录持确定性 `Index` = SHA-256 over provider + path + base_url + api_key, 前 8 字节 hex 作 stable internal identity 跨重启 (`sdk/cliproxy/auth/types.go:246-326`); 纯哈希派 identity 无 DB lookup | **算法升级**: 二者都派 stable ID, 但 HUAKAI DB-backed lookup 让 identity 与 secret material **解耦** — credential 轮换 / 撤销不影响 identity (cliproxyapi seed 含 key 本身 → 轮换 = identity 失效) | 算法 |

Source files read (specifier session):
- `~/refs/litellm/litellm/proxy/_types.py:215-225`
- `~/refs/litellm/litellm/proxy/auth/user_api_key_auth.py:1310-1395`
- `~/refs/litellm/litellm/proxy/auth/auth_checks.py:1620-1739, 2500-2578`
- `~/refs/envoy-ai-gateway/internal/controller/backend_security_policy.go:1-435`
- `~/refs/envoy-ai-gateway/internal/controller/secret.go:1-78`
- `~/refs/envoy-ai-gateway/internal/backendauth/api_key.go:1-33`
- `~/refs/envoy-ai-gateway/internal/extproc/processor_impl.go:62-133`
- `~/refs/one-api/middleware/auth.go:1-167`, `middleware/distributor.go:1-103`
- `~/refs/cliproxyapi/sdk/cliproxy/auth/types.go:1-676`
- `~/refs/portkey-gateway/src/*` (grep only, confirmed no relevant pattern)

Specifier follow-up needed (Phase 2A.1 启动前应补): agentgateway `auth_plane` + routiium
`KeyStore` 深度 specifier read。

---

## §10 风险 + 回滚策略

### R-Phase2-1: Go 派 tenant 算法 bug 让所有请求错派
- 触发: D-16 adapter 配错 / APIKeyResolver 接口 misuse
- 防御: D-14 (a) reject mismatch + counter 监控 `mismatch_total{source=both_present}`,
  持续 >0 时 alert
- 回滚: Owner 紧急切 Manual First 回 staging-default-on + Go RouteQueryService 返
  空 derived_tenant_id 让 Rust 全走 Manual First

### R-Phase2-2: Raw credential 泄漏 (P2-A5 守门破)
- 防御: `ClientCredential.secret` unexported; 所有 status/log/error 走 fingerprint 助手;
  测试用 distinctive raw secret 触发各种错误验证不泄漏
- 回滚: 紧急 patch 把所有 error.Error() 改成静态消息

### R-Phase2-3: Generated proto 不慎落 frozen `internal/proto` 包
- 防御: Codex spec 显式 ban; Phase 2A.5 实施前 reviewer 检查 import 路径; cargo-deny 不
  会有 grpc 包冲突 (检验过)
- 回滚: 移动到 `routepb`, 更新所有 import

### R-Phase2-4: telemetry cardinality 爆炸
- 防御: D-15 counter 仅 kind+source 维度, 永不含 tenant_id (8 时间序列固定)

### R-Phase2-5: Phase 2 → Phase 3 退出条件 (D-17) 数据驱动但 mismatch 隐藏
- 防御: D-17 (d) 数据 + Owner sign-off 双重门; Phase 3 进入前再做 1 周 dual-write
  observation 不切

### R-Phase2-6: Go 控制面 grpc 依赖审批延迟 (OD-1 卡)
- 缓解: Phase 2A.1 + 2A.2 + 2A.3 + 2A.4 可独立先做 (mock control plane 验对账逻辑);
  Phase 2A.5 是 Go 团队真正接力点, 等 OD-1 审批后启

### R-Phase2-7: Rust 误以为 Manual First sunset 时机已到, 提前删 (Phase 3 跳级)
- 防御: Phase 2 plan 显式 scope out "do not delete Rust Manual First"; Phase 3 独立
  cutover gate (D-17 d) 由 Owner 显式启动

### 回滚策略全图
- Phase 2A.3 (proto + Rust mock 接 derived_tenant_id): revert proto field +
  account_planner 接口签名, 风险低
- Phase 2A.4 (对账逻辑): `HUAKAI_CLIENT_AUTH_RECONCILE_POLICY=manual_first_only` 配置
  即 Phase 1 行为
- Phase 2A.5 (Go gRPC server 上线): Go 团队部署回滚 + Rust 自动降级 (Go 派为空时
  fall back to Manual First, D-14 (a) 选项天然支持)

---

## §11 Codex parallel-draft attestation (CLAUDE.md #10)

- **Claude draft** (`2026-05-24-w11a-d1b-phase2-claude.md`): 8 决策, 17 KB, 写在
  2026-05-24 19:23 UTC, **没读 Codex draft 即完成**
- **Codex draft** (`2026-05-24-w11a-d1b-phase2-codex.md`): 5 gates + 5 OD, 29 KB,
  写在 2026-05-24 20:26 UTC, Codex 显式声明 "did not read claude draft"
- **Specifier-dig L1 report** (general-purpose agent, 2026-05-24): 真读 LiteLLM +
  envoy-ai-gateway 源码 + 5 项 recency check + 2 项漏引补充 (one-api + cliproxyapi)
- **Synthesis** (本文件): Claude 在两稿都 ready 后合成, 含 Codex 关键发现 (Phase 2A/2B
  拆分 critical finding) + specifier 实读修订 (envoy 重新 frame, LiteLLM delta 改 PII →
  multi-process)

---

## §12 Owner 决策总览 (最小信息密度)

请选项, 标 ☐ 默认推荐 或具体选项:

| # | 决策 | 推荐 | Owner 选择 |
|---|---|---|---|
| D-13/OD-2 | Phase 2A proto 加 `derived_tenant_id` field 15? | ✓ (a) | ☐ |
| D-14/OD-4 | 双稿一致: reject mismatch | ✓ (a) | ☐ |
| D-15 | counter + tracing warn 并存 | ✓ | ☐ |
| D-16/OD-3 | 复用 APIKeyResolver adapter, 不动 auth core | ✓ (a) | ☐ |
| D-17/OD-5 | 数据 + Owner 双重门 | ✓ (d) | ☐ |
| D-18 | 本 synthesis 即 Phase 2A 共享决策 plan | ✓ (b 变种) | ☐ |
| D-19 | mock_control_plane.rs 升级支持 derived_tenant_id | ✓ (a) | ☐ |
| D-20 | rotation 走 Go DB, Manual First 不刷 | ✓ (b) | ☐ |
| D-21 | mismatch error type = PermissionDenied | ✓ (a) | ☐ |
| D-22 | matching legacy tenant emit reconciliation counter | ✓ (a) | ☐ |
| OD-1 | 批准 grpc + protobuf Go deps (启用 Phase 2A.5) | ✓ (a) | ☐ |
| OD-2 | Phase 2B claim contract 字段 | **延后单独 plan** | ☐ |

**或 ☐ 全部默认推荐** (Claude 推荐), Phase 2A 全部 12 项 lock 后启 Phase 2A.1。

Phase 2B 单独 plan, 等 OD-2 决定。
