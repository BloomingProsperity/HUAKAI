# W11-A D-1b Phase 2 计划 (Claude draft, 2026-05-24)

> CLAUDE.md #10 parallel-draft: 本文件是 Claude 独立草稿, 同步与 Codex draft 平行编写
> (互不可见), 最终 synthesis 由 Owner 决策对齐。

## §1 Phase 2 目标 + Phase 1 兜底回顾

**Phase 2 目标**: Go 控制面成为客户端凭据→tenant 的**唯一权威派源**, Rust 数据面进入
"双写对账"阶段验证 Go 派生的正确性, 为 Phase 3 Manual First 永久下线做数据驱动准备。

**Phase 1 已落地** (commit 7e10069):
- Rust 写 `RouteQueryRequest.client_credential` 字段 (canonical "bearer:<token>" /
  "x-api-key:<key>"), Go 控制面**完全忽略**
- Manual First 静态 hash→tenant 在 dev/staging 兜底; production 模式 startup
  fail-fast 禁启用
- 7 个守门已 mutation-tested (A1/A3/A4/A5/D-9/D-11/D-12)
- β scheme 锁: Rust 永不持身份权威, 永不读 x-tenant-id header

**Phase 2 必须保留的 Phase 1 不变量**:
- A3: x-tenant-id 永不被信任 (即使 Phase 2 提供 Go 派 tenant)
- A4: raw credential 永不入 log / Debug / span field
- β scheme: Rust 仍不持身份权威 — 派 tenant 的责任完全在 Go

## §2 现状评估 (Go / Rust 各自需要改什么)

### 2.1 Go 控制面 (当前状态)

**关键事实**: HUAKAI 仓库当前 **没有真正实现 huakai.route.v1.RouteQueryService 的 Go
gRPC server**。Rust 数据面只与 `mock_control_plane.rs` 通信 (这是 Rust 内部的 mock,
不是 Go server)。

证据 (grep `huakai.route.v1` `RouteQuery` 在 `backend/internal/`): 0 命中。Go 侧
`backend/internal/router/route_plan.go` 是 Go 内部 router/路由代理实现, 与 proto
RoutePlan 同名但是**完全独立的 Go 类型**, 不是 proto generated。

**Phase 2 Go 侧 = 从零起 RouteQueryService** (大量新代码), 含:
- gRPC server bootstrap (tonic-equivalent Go = grpc-go)
- RouteQueryService 实现读取 `client_credential` (canonical "kind:secret") 字段
- credential → tenant 派生算法 (查 DB / cache / external auth)
- RoutePlan 回填 vendor + endpoint + auth material + **新派 tenant 字段**
- UDS / mTLS / HTTP-loopback 三种 transport baseline 与 Rust 数据面对齐

### 2.2 Rust 数据面 (Phase 2 待改)

- 接收 RoutePlan 中 Go 派的 tenant 字段, 与 Manual First 派 tenant 比对
- 选择信任策略 (D-14): 信 Go / 二者一致才用 / per-tenant 配置
- 不一致时记录 telemetry (D-15): counter / log / attempt_report 字段
- 测试 mock_control_plane 升级 (D-19): mock 须能 emit 新派 tenant 字段
- 灰度 flag 让 Rust 在 staging 先夹生 (D-16)
- attempt_report 上报源 tenant: Manual First 派 vs Go 派 (审计需要可追溯)

### 2.3 proto schema 演进

`RoutePlan` 当前字段 (route.proto):
```proto
message RoutePlan {
  string route_plan_id = 1;
  string account_id = 2;
  bytes acquisition_token = 3;
  string vendor = 4;
  // ... 等
}
```

**Phase 2 增字段 (D-13)**: 待 Owner 决策的 proto 演进。

## §3 Owner 决策点

### D-13: RoutePlan 中 Go 派 tenant 的回流路径

- (a) **新增 `string derived_tenant_id = 15;`** (显式新字段, Rust 双写对账时清晰)
- (b) 复用 `RoutePlan.account_id` 同义为 tenant_id (语义重载)
- (c) 占用现有未用字段 (无)
- **Claude 推荐 (a)**: Phase 3 可以纯粹删字段不破坏旧 client; account_id ≠ tenant_id
  (一个 account 可有多 tenant), 重载会污染语义; (a) 让对账逻辑 trivial。
- 或 ☐ **全部默认推荐**

### D-14: Rust 对账策略 (Manual First vs Go-derived)

- (a) **始终信任 Go** (Manual First 派只用于 telemetry alert)
- (b) 二者一致 → 用; 不一致 → fail-closed 401
- (c) Phase 2 staging 信 Manual First, observation 后 switch 信 Go
- (d) Per-tenant 配置 (新接入信 Go, 老接入信 Manual First)
- **Claude 推荐 (a)**: Phase 2 目标就是验证 Go 派, 数据 driven 决定切换; (b) 在 Manual
  First 表过期时会误伤 (因 Manual First 是兜底, 不该是 ground truth); (c) 增加流程
  复杂度, (a) + 监控 + Owner 看 drift 后 manual 决定切是更轻。
- 或 ☐ **全部默认推荐**

### D-15: 不一致告警 telemetry

- (a) **Prometheus counter** `huakai_client_credential_tenant_mismatch_total{kind, source}`
  (kind = bearer / x-api-key; source = manual_first_only / go_only / both_present)
- (b) tracing warn 含 `manual_tenant_fp` + `go_tenant_fp` (fingerprint 防 PII)
- (c) attempt_report 字段 `tenant_drift: bool` 透到 Go
- **Claude 推荐 (a) + (b) 并存**, 不做 (c): counter 监控大盘, log 调查细节; attempt_report
  字段不必加 (Go 已经知道自己派的 tenant, 再 echo 回去无新信息)。
- 或 ☐ **全部默认推荐**

### D-16: Go 侧凭据→tenant 派算法

- (a) **Static map 镜像 Rust Manual First** (Phase 2 = pure migration, 不引入新数据源)
- (b) **DB lookup** with new `tenant_credentials` 表 (Phase 2 = real source of truth)
- (c) Two-tier: in-memory cache + DB fallback
- **Claude 推荐 (b) + (c)**: Phase 2 的真正价值是让 Go 成为有权威数据源, Manual First
  只是过渡桥; (a) 只是把 Rust 表迁过去, Phase 3 时 Manual First 删了等同没派源; (c) 性能
  考量, route_query QPS 高时 DB 直查会卡。
- 或 ☐ **全部默认推荐**

### D-17: Phase 2 → Phase 3 退出条件 (Manual First 永久下线时机)

- (a) **数据驱动**: 连续 7 天 mismatch rate < 0.01% + 所有现 tenant 都已迁入 Go 表
- (b) Owner 显式 manual 决定
- (c) Mismatch alert 0 累计 1 周 + Manual First 默认 OFF 已生效 30 天
- **Claude 推荐 (a) + Owner sign-off**: 量化标准 + Owner gate; (b) 纯 manual 缺数据
  支撑, 风险高。
- 或 ☐ **全部默认推荐**

### D-18: Go 侧 spec 拆分

- (a) Phase 2 plan **同时盖 Go + Rust**, 一份 plan
- (b) **Phase 2-Go 单独 spec, Phase 2-Rust 单独 spec, 两份 plan 并行**
- **Claude 推荐 (b)**: Go 团队接力 / 平行进行; 各自 plan 清晰; synthesis 层做 cross-validation;
  当前 Phase 2 plan (这份 + Codex draft) 定 cross-cutting (proto / telemetry / 退出条件),
  Go-specific 算法 / DB schema / cache 由后续 phase2-go plan 起草。
- 或 ☐ **全部默认推荐**

### D-19: Rust 集成测试 (mock_control_plane) 是否升级

- (a) **Yes**: `mock_control_plane.rs::mock_route_plan` 增 `derived_tenant_id` 字段,
  让 Rust 集成测试能验证 e2e dual-write 对账逻辑
- (b) No: Phase 2 测试只跑 unit test, account_planner 模块直接验
- **Claude 推荐 (a)**: 单元测试只验 logic, 不验 wiring; mock 升级是守门 listener →
  account_planner → control_plane 全链路对账的唯一手段; mutation 测试 (CLAUDE.md #14)
  也需要 e2e fixture。
- 或 ☐ **全部默认推荐**

### D-20: 凭据轮换 (rotation) 行为

- (a) Manual First file watch 让 keys file 修改后 resolver 自动 reload
- (b) **Rotation 完全走 Go control plane (DB update), Manual First 不刷**
- **Claude 推荐 (b)**: Manual First 是 Phase 2 过渡桥, 不引入 file watch 复杂度;
  Phase 2 期内 Manual First 当作 immutable snapshot, 改 keys 要重启 Rust 进程; 真正
  的 rotation 走 Go DB update (Phase 2 既有 D-16 (b) DB 路径自然支持)。
- 或 ☐ **全部默认推荐**

## §4 双稿可能已收敛的项 (不需 Owner 决策)

- **保留 Phase 1 β scheme**: Rust 仍不持身份权威 — 双稿应一致
- **保留 A1/A3/A4 守门**: Phase 2 不改这三个守门
- **proto 字段类型用 string**: derived_tenant_id 与 tenant_id 同类型 (D-1)
- **sha2 dep 不再新增**: Phase 1 已加入, Phase 2 复用
- **Cargo.lock 不重新 audit**: Phase 1 dependency 已审过
- **现 7 个守门测试不动**: Phase 2 只加新测试, 不改旧测试 (向后兼容)

## §5 文件触点 (Go + Rust 全列)

### 5.1 Rust 数据面

```text
proto/route.proto                                            -- + derived_tenant_id field (D-13)
crates/core_gateway/src/account_planner.rs                  -- 对账逻辑 + telemetry emit (D-14/D-15)
crates/core_gateway/src/listener.rs                          -- 选择信任策略后 attempt_report 写源 tenant
crates/core_gateway/src/mock_control_plane.rs                -- mock_route_plan 增 derived_tenant_id (D-19)
crates/core_gateway/src/metrics.rs                           -- 注册 mismatch counter (D-15)
crates/core_gateway/src/attempt_reporter.rs                  -- 上报源 tenant 字段
crates/core_gateway/src/lib.rs                               -- (可能) GatewayState 增 reconciliation policy enum (D-14)
crates/core_gateway/src/config.rs                            -- (可能) HUAKAI_CLIENT_AUTH_RECONCILE_POLICY (D-14 c/d 走 config)
crates/core_gateway/tests/route_client_test.rs              -- 新增对账 e2e 测试 (mismatch / match / sole-source 三场景)
crates/core_gateway/tests/proxy_engine_test.rs              -- (按需) 调用 build_route_query 测试更新签名
crates/core_gateway/src/route_proto/redacting_debug.rs       -- (按需) RoutePlan 新字段 Debug 渲染
```

### 5.2 Go 控制面 (Phase 2-Go 单独 spec, 占位)

```text
backend/internal/proto/route/v1/                              -- proto generated Go bindings
backend/internal/router/route_query_service.go (新文件)       -- gRPC server impl
backend/internal/router/credential_tenant_resolver.go (新)    -- 凭据 → tenant 派算法
backend/internal/db/auth/tenant_credentials.sql.go (新)       -- DB schema for tenant_credentials 表 (D-16 b)
backend/internal/router/credential_tenant_cache.go (新)       -- in-memory cache (D-16 c)
backend/internal/router/route_query_service_test.go (新)      -- e2e + unit
backend/cmd/<server>/main.go                                  -- bootstrap RouteQueryService 上线
deployments/*/route_query.yaml                                -- Helm / k8s / config
```

## §6 子计划 (Sub-phases)

### Phase 2a: proto + Rust 数据面 dual-write 接收 (1-2 day)

1. proto.RoutePlan 加 `derived_tenant_id` 字段 (D-13 a, field 16)
2. `mock_control_plane.rs::mock_route_plan` 升级 (D-19)
3. `account_planner.rs::planned_attempt` 接收 derived_tenant_id (旧 mock 行为: 空字符串)
4. Rust 单元测试: 验证 derived_tenant_id 透到 PlannedAttempt
5. cargo test + codex review + commit + push

### Phase 2b: Rust 对账策略 + telemetry (1 day)

1. `config.rs::HUAKAI_CLIENT_AUTH_RECONCILE_POLICY` 枚举 (D-14 选项)
2. `account_planner.rs` 对账逻辑: Manual First tenant vs Go derived tenant
3. `metrics.rs` 注册 mismatch counter (D-15)
4. `listener.rs` 选 final tenant 后 attempt_report 透传源
5. 新增 e2e 测试: 3 场景 (match / mismatch / sole-source-go-only / sole-source-manual-only)
6. cargo test + codex review + commit + push

### Phase 2c: Go 控制面 Phase 2-Go spec 启动 (delegate to Go team)

1. 单独 plan 文件: `docs/process/plans/YYYY-MM-DD-w11a-d1b-phase2-go-{claude,codex,synthesis}.md`
2. RouteQueryService gRPC server impl (含 D-16 a/b/c 选型 + DB schema + cache)
3. Go-Rust cross-validation test (Rust 用真实 Go server 而非 mock 跑端到端)
4. Phase 2c 完成 = Phase 2 全 ready

### Phase 2 退出 → Phase 3 (按 D-17 决策启)

数据 driven 验证退出条件后, 启动 Phase 3 plan (Manual First 永久下线 + RoutePlan
旧 tenant_id 字段 (Phase 2 期间 Go 仍可能写) 也下线)。

## §7 验收门 (Phase 2 完成 + Phase 3 entry 标准)

### B1: 双写对账正确性
- Manual First tenant 与 Go derived tenant 一致时: 选用 (按 D-14 a) Go tenant, 不告警
- 不一致时: counter +1 + log warn + 按 D-14 走 (a/b/c/d) 策略
- mutation: 删对账逻辑 → mismatch counter 不增 → 测试红

### B2: telemetry 完整性
- `huakai_client_credential_tenant_mismatch_total` counter 在 /metrics endpoint 可读
- counter 维度含 kind + source (没 cardinality 爆炸: 仅 2*3=6 个时间序列)
- mutation: 删 counter inc → 集成测试断言 metric 增量 = 0 → 红

### B3: Rust 测试 e2e 覆盖
- mock control plane 能返回任意 derived_tenant_id (D-19)
- 3 场景集成测试都跑过: match (用 Go tenant) / mismatch (counter +1, 按策略走) /
  sole-source

### B4: A1/A3/A4/A5/D-9/D-11/D-12 不退化
- Phase 1 7 个守门测试全部继续通过
- A3 不退化: 即便 Phase 2 提供 derived_tenant_id, x-tenant-id header 仍永不读
- A4 不退化: raw credential 仍不入 log/Debug

### B5: Phase 3 entry 满足
- 连续 7 天 mismatch rate < 0.01% (按 D-17 a)
- Manual First 所有 entry 都已迁入 Go DB
- Owner sign-off 后启 Phase 3

## §8 引证表 (fusion-upgrade delta, CLAUDE.md #12)

| 借鉴源 | 模式 | HUAKAI delta | 维度 |
|---|---|---|---|
| **agentgateway**@HEAD-2026-05-24:auth_plane | control plane 派 tenant + 数据面 dual-write 对账 | + Manual First 桥接期 + 数据驱动 sunset SLO (D-17 a) + counter 维度精确到 kind+source 而非 tenant_id (PII safe) | 架构 + 生态 |
| **Helicone/ai-gateway**@HEAD-2026-05-24:control_plane_state | key auth 中 control plane 是唯一 tenant 派 source | + β scheme (Rust 完全不持身份, 比 Helicone Rust 缓存 key→tenant 设计更严) + 凭据 hash fingerprint 入 metric 而非 raw key | 架构 + 算法 |
| **labiium/routiium**@HEAD-2026-05-24:KeyStore | static map + cache 兜底 | + Manual First 限 dev/staging + production fail-fast (HUAKAI 把过渡桥与永久路径明确分开) | 架构 |
| **LiteLLM**@HEAD-2026-05-24:user_api_key_auth | api_key → user_id 派, with DB + cache (这就是 D-16 (b) + (c) 推荐源) | + 显式 reconciliation policy enum (D-14 选择题) + telemetry 双维度 counter + 加固 PII handling (fingerprint not raw key) | 算法 + 生态 |
| **envoy-ai-gateway**@HEAD-2026-05-24:xds_credential_provider | xDS 推 credential 配置 + control plane 派 | + Manual First file → DB 演进路径 + 双写对账期间不强制 Go 永远对 (allow drift, log, count) | 架构 |

Source files read:
- exploratory/rust-core-gateway/merged/proto/route.proto (Phase 1 状态)
- exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs:1-810
- exploratory/rust-core-gateway/merged/crates/core_gateway/src/client_auth/mod.rs
- docs/process/plans/2026-05-24-w11a-d1b-phase1-synthesis.md §4.5 + §7-H/J
- backend/internal/ (grep 确认 huakai.route.v1 Go server 当前不存在)

## §9 风险 + 回滚策略

### R-Phase2-1: Go 派 tenant 算法 bug 让所有请求被错派

- 触发: D-16 (b) DB 表配错 / cache 失效 / Phase 2-Go spec 实现有错
- 防御: D-14 选 (a) 但 counter 监控 mismatch_total{source=both_present}, 持续 >0
  且 Manual First tenant 是已知合法值时, alert + fall back to Manual First (临时
  per-tenant override via D-14 d?)
- 回滚: Owner 紧急切 Manual First 回 staging-default-on (临时模式) + Go RouteQueryService
  暂时返回空 derived_tenant_id 让 Rust 全走 Manual First

### R-Phase2-2: telemetry cardinality 爆炸

- 触发: D-15 counter 维度若包含 tenant_id 会爆 (HUAKAI 多租户 → N 个 tenant *
  3 个 source * 2 个 kind = 6N 时间序列)
- 防御: D-15 推荐项只用 kind + source (6 个固定时间序列), 不含 tenant_id

### R-Phase2-3: 双写期间 Rust 选用 Manual First (D-14 b) 但 Go 已切换 → audit 漂移

- 触发: D-14 选 (b) fail-closed, 但 Manual First 在 Phase 3 切换前未刷
- 防御: D-14 推荐 (a) 信 Go (Manual First 只 telemetry alert), 避免 b 的死锁风险

### R-Phase2-4: Phase 2 → Phase 3 退出条件 (D-17) 数据 driven 但 mismatch 隐藏

- 触发: D-17 (a) 0.01% SLO 满足但实际有 silent corruption (e.g., counter bug)
- 防御: D-17 (a) + Owner sign-off (a' = a + manual gate), 双重门; Phase 3 进入前
  额外做 1 周 dual-write observation 不切

### R-Phase2-5: Go 控制面尚未实现 (现状), 时间线被 Go 团队 capacity 卡

- 触发: Phase 2-Go spec 还没起, Go 团队 capacity 紧
- 缓解: Phase 2a + 2b (Rust 侧) 可独立先做 (用 mock control plane 验对账逻辑),
  不阻塞 Go 团队 spec 起草; Phase 2c 是 Go 团队真正接力点
- 推荐: Phase 2a + 2b 先 land (我推进), Phase 2c 单开 plan + Go team handover

### 回滚策略全图

- Phase 2a (proto + Rust 接 derived_tenant_id mock): 回滚 = revert proto field +
  account_planner 接口签名, 风险低
- Phase 2b (对账逻辑): 回滚 = HUAKAI_CLIENT_AUTH_RECONCILE_POLICY=manual_first_only
  (D-14 d 的极端配置), 即 Phase 1 行为
- Phase 2c (Go server 上线): 回滚 = Go 团队部署回滚 + Rust 自动 fall back (D-14
  (a) 选项让 Go 派为空时仍走 Manual First, 自动降级)

---

(草稿结束; Owner 决策 D-13..D-20 八项后, 合成 synthesis 文件并启动 Phase 2a 执行)
