# 2026-05-15 R-3 R-E Mainline Rust Data Plane Codex Plan

| Owner directive | “你是 R-3 R-E mainline 接入 planner lane (workspace-write, 只写一个 plan doc，不动代码)。” |
|---|---|
| Scope | 规划 Phase R-E: 在 R-C/R-D 通过后，把 `exploratory/rust-core-gateway/merged/` 的 Rust 数据面接入生产主线。 |
| Out of scope | 不写实施代码；不移动目录；不改 `docs/10_RISK_REGISTER.md`；不改 `docs/03_FEATURE_PARITY_MATRIX.md`；不改 deployment scripts；不新增依赖；不 stage/commit/push。 |
| Success criteria | 给出 R-SEC-002 传输保护选择、fallback 生命周期、exploratory -> mainline GO/NO-GO gate、atom 拆分、Owner OCAW 决策点。 |
| Time estimate | R-E 总体 5-8 天工程期；本 planner lane 仅产出本 plan doc。 |
| Blast radius | 未来实施会影响 Rust 数据面主线目录、Go control plane RPC server、Go gateway traffic switch、observability、rollback；本计划本身只新增文档。 |
| Clean-room | 本轮不读禁用 reference repos；只读 HUAKAI 内部 docs/exploratory/backend/tools 搜索结果和 Rust gateway 内部文件。 |

## 1. Current Facts

- R-3 主 plan 把 Phase R-E 定义为 Mainline Rust 数据面，要求 R-D 通过后才接生产主线，并保留 `hyper-rustls` fallback。
- `docs/10_RISK_REGISTER.md` 已把 `R-SEC-002` 标为 HIGH：`RoutePlan` 携带 per-attempt upstream auth material，Rust 数据面和 Go control plane 之间不能走未保护网络传输。
- `exploratory/rust-core-gateway/merged/READINESS.md` 当前结论仍是 NO-GO 接主线：缺 Owner 本机真实 upstream smoke、主线 Go gRPC 依赖决策、真实 fd/benchmark 对比。
- `route.proto` 已有 `RouteService` 的 `RouteQuery`、`AttemptReport`、`HealthCheck`、`Heartbeat`，其中 `RoutePlan.upstream_auth` 和 `AttemptReportRequest.acquisition_token` 是 R-SEC-002 的直接保护对象。
- 当前 Rust `RouteClient` 使用 tonic `Endpoint`/`Channel` over URI；R-E 不能把这个形态直接提升为生产主线，必须先补传输认证/本地化边界。
- `Cargo.toml` 中 `hyper-rustls` 已是当前 upstream client fallback；`mimicry-openssl` 和 MIT `http2` fork 仍是 feature-gated，不是默认路径。

> feedback_chinese_comments: R-E 不是“把目录搬进 backend 就算完成”。它是一次生产切换，安全传输、真实上游验真、双路径计费一致性、回滚开关都必须先过。

## 2. Scope

In scope:

- 设计 Rust 数据面进入主线后的目录、构建、运行和流量切换顺序。
- 把 R-SEC-002 作为 R-E 的第一硬闸门，明确 mTLS / Unix domain socket / shared secret 三类方案。
- 定义 `off -> shadow -> canary -> on` 的生产切换语义和 GO/NO-GO 条件。
- 规划 Rust 数据面与 Go control plane 的安全 RPC contract，保留 gRPC 与 HTTP/JSON shim 的 Owner 决策点。
- 规划 attempt report、billing/quota、observability、rollback、R-D capture provenance 的主线 gate。
- 规划保留 `hyper-rustls` fallback 的时长、用途和删除前置条件。

Out of scope:

- 不实施 R-E。
- 不决定 Owner OCAW。
- 不读取禁用 reference repos。
- 不扩大 Rust 数据面的权威职责：PASR、quota、billing ledger、auth core、DB schema 继续由 Go/PG 控制面拥有。
- 不把 shadow 模式变成隐形双计费或隐形真实上游重放。
- 不把 R-C/R-D 的 `KnownGap` 或 local-only capture 当作 production dispatch 通过条件。

## 3. Success Criteria

R-E 可以声明完成，仅当以下全部满足：

- `R-SEC-002` 关闭或降级为明确可接受的残余风险：Rust-Go control-plane channel 已使用 Owner 批准的 mTLS、UDS、或等效本地认证传输。
- R-D gate 通过：每个 production mimicry vendor 都有 Owner 本机真实上游样本，stable profile hash 全匹配；local capture 不能单独放行。
- Rust 主线目录 build/test/clippy/fmt 通过，且默认 feature 不启用高风险 mimicry backend。
- Go control plane 能按 v0 contract 返回 route plan、接受 attempt report、health check 和 heartbeat，并覆盖 deadline/cancel/error mapping。
- `off/shadow/canary/on` 四态 feature flag 可观测、可回滚，且 canary/on 不会绕过 R-D 和 R-SEC-002。
- shadow 不产生重复计费；如果 Owner 批准真实 upstream shadow，必须有双写隔离、attempt report 防重、预算上限和 kill switch。
- Rust 与 Go hot path benchmark 在 Owner 确认的真实环境中达标；至少不能显著恶化 p95/p99、RSS、CPU/token、stream error rate。
- `hyper-rustls` fallback 仍可启用，且 fallback 触发原因被 metrics/logs 记录。
- Rollback runbook 经演练：从 `on` 或 `canary` 回到 Go hot path 不需要 DB surgery，不丢 billing/quota/audit。

## 4. R-SEC-002 Design Options

| Option | Pros | Cons | Best fit | Security verdict |
|---|---|---|---|---|
| mTLS over TCP | 支持跨 host / K8s / 独立扩缩容；证书身份清晰；可保留标准 gRPC tooling；适合未来 SaaS 多节点拓扑。 | CA、证书轮换、过期告警、SAN 校验、镜像 secret mount 都要运维；配置错会暴露网络面；实施和测试成本最高。 | Rust data plane 与 Go control plane 不在同一 host / pod / VM，或 Owner 要提前铺 SaaS 分布式拓扑。 | 可作为生产方案，但必须配证书轮换 runbook、fail-closed 校验和证书泄漏演练。 |
| Unix domain socket | OS 内核本地边界；无 TCP sniffing 面；文件权限和容器 volume 可控；无需证书生命周期；同机部署最简单。 | 只适合同机/同 pod；跨 host 需换方案；socket 文件权限、stale socket、容器 user/group 要测试；LB/服务发现能力弱。 | Personal Edition 或单机生产：Go control plane 与 Rust data plane 同机/同 pod。 | 推荐作为 R-E 首选 baseline，但要叠加 per-RPC auth 防止本机非授权进程误用 socket。 |
| Shared secret verification | 实施最轻；可用 gRPC metadata 或 HTTP header；便于轮换；可作为 UDS/mTLS 的第二因子。 | 单独使用不加密；secret 可被 env/log/core dump 泄漏；如果没有 nonce/timestamp/idempotency，存在 replay 风险；不能保护未可信网络。 | 作为 UDS 或 mTLS 的 application-layer 认证；或 loopback-only dev/test 临时方案。 | 不建议单独用于生产 TCP。可以作为 UDS baseline 的 HMAC/nonce layer。 |

Codex recommendation:

- R-E 默认推荐 **UDS + per-RPC shared-secret/HMAC metadata**：满足 Personal Edition 同机主线接入，安全面比裸 TCP 小，实施复杂度低于 mTLS。
- 如果 Owner 明确要求 R-E 一步到跨 host / K8s / 多节点 topology，则选择 **mTLS over TCP + optional shared-secret metadata**。
- **Shared secret only over TCP 不应作为 production GO 条件**；最多用于 local dev 或 loopback-only emergency shim。

> feedback_chinese_comments: 推荐不是替 Owner 拍板。真正选择取决于 R-E 的部署拓扑：同机主线优先 UDS，跨机器主线必须 mTLS。

## 5. Fallback Strategy

`hyper-rustls` fallback 保留策略：

- R-E 全程必须保留代码路径和运行时开关，不能因为 mimicry backend 通过 local preflight 就删除。
- `shadow` 和 `canary` 阶段，fallback 默认启用，但每次 fallback 必须记录 reason、vendor、profile、request class、R-D gate provenance。
- `on` 后至少保留 **30 个自然日且跨 2 个 release gates**；如果期间发生任一 fingerprint drift、R-D recapture fail、secret redaction fail、stream regression、or rollback drill fail，30 天窗口重新开始。
- 30 天后也不建议删除，只能降级为 emergency feature flag；真正删除 fallback 需要新的 Owner OCAW、release gate、risk review。
- fallback 不能被用来掩盖 mimicry production dispatch 不满足 R-D 的事实：某 vendor 未过 R-D，就对该 vendor 禁用 mimicry dispatch，而不是默默走“接近但未验真”的路径。

## 6. GO/NO-GO Gate: Exploratory to Mainline Production

NO-GO if any is true:

- R-D 未通过 Owner 本机真实上游验真。
- R-SEC-002 未落地，或 control-plane channel 仍可经未认证 TCP 传输 auth material。
- `AttemptReport` 不能证明 every terminal attempt 都有 ack/idempotency/retry behavior。
- shadow 会发真实 upstream 请求但没有 Owner 批准、预算上限、去重账务、kill switch。
- Rust path 与 Go path 的 request id / trace id / tenant id / route plan id 无法对齐。
- Canary/on 的 fallback reason 不可观测，或 operator 无法区分 Rust failure 与 upstream failure。
- dependency license audit 未覆盖 R-E 默认 feature 与 optional mimicry feature。
- rollback 需要手工 DB surgery 或会丢 usage/billing/audit。

GO to `shadow` when:

- R-SEC-002 baseline 已实现并有攻击/误配置测试。
- Rust mainline build/test/clippy/fmt PASS。
- Go control plane conformance tests 覆盖 route query、attempt report、health、heartbeat。
- Shadow policy 明确是 route-only/mock/local capture，或真实 upstream shadow 已由 Owner OCAW 批准。
- Observability dashboard 能看见 Rust in-flight、control-plane RPC p95、attempt report queue depth、fallback count、redaction violations。

GO to `canary` when:

- Shadow 连续 7 天无计费重复、无 secret exposure、无 trace mismatch、无 unexplained fallback spike。
- Owner 允许的真实 vendor 范围内，anthropic/openai/gemini/codex 至少完成指定样本和流式 parity；不具备真实凭据的 vendor 不能进入 canary 依据。
- Rust 与 Go hot path 对比达到 Owner 设定阈值。
- Canary blast radius 可按 tenant/model/percentage 限定，且可秒级回滚。

GO to `on` when:

- Canary 分阶段完成，例如 5% -> 25% -> 50% -> 100%，每阶段至少覆盖 streaming、non-streaming、timeout、client cancel、upstream 4xx/5xx。
- `hyper-rustls` fallback 仍可启用，回滚演练已通过。
- Release gates 中 Security/Billing/Clean-room/Acceptance/Scenario 均无 HIGH。

## 7. Atoms

| Atom | Scope | Success condition | Time | Risk |
|---|---|---|---:|---|
| R-E-A1 Transport decision + threat model freeze | 把 R-SEC-002 的 UDS/mTLS/shared-secret 方案落成 Owner-approved design；产出 threat model、negative tests、config contract。 | Owner OCAW-1 完成；生产 baseline 不再允许 plain TCP with auth material。 | 0.5-1d | HIGH: 若跳过，凭据可能在 Rust-Go channel 泄漏。 |
| R-E-A2 Secure control-plane channel implementation | Rust route client 与 Go control-plane server 支持 Owner 选定安全传输；加 misconfig fail-closed。 | 未授权 peer、错误 secret、错误 cert、错误 socket permission 全部失败；正常 RPC 全通过。 | 1-1.5d | HIGH: 触及 auth material transport，但不改 auth core/billing/quota。 |
| R-E-A3 Mainline repo integration | 将 merged Rust data plane 按 Owner 目录决策接入主线 build/workspace；默认 features 保守。 | 主线 `cargo build/test/clippy/fmt` PASS；Go build 不受影响；默认不启用 high-risk mimicry features。 | 0.5-1d | MED: workspace/build churn；不应改 DB/deploy scripts。 |
| R-E-A4 Go control-plane service conformance | Go 侧实现或 shim `RouteService` contract；对齐 deadline/cancel/status/error/redaction。 | Rust mock 与 Go real control plane 跑同一 conformance suite；route/attempt/heartbeat 语义一致。 | 1-1.5d | HIGH if grpc-go dependency chosen; otherwise MED for HTTP/JSON shim drift。 |
| R-E-A5 Feature flag traffic switch | `off/shadow/canary/on` 四态；按 tenant/model/percentage 控制；kill switch 秒级生效。 | Shadow 不影响用户响应；canary/on 可回滚；所有状态都有 metrics 和 audit。 | 1-1.5d | HIGH: 双路径容易造成重复上游请求或重复 attempt report。 |
| R-E-A6 Billing/quota/attempt safety drill | 验证 Rust attempt report 与 Go billing/quota 闭环；覆盖 success/cancel/timeout/protocol_error/retryable failure。 | 每个 terminal path 有 exactly-once logical settlement；失败上报可恢复；无 silent usage loss。 | 0.75-1d | HIGH: 钱路径和 quota 一致性，必须 fail-closed。 |
| R-E-A7 Shadow/canary validation run | 真实环境 7d shadow + staged canary；只用 Owner 允许真实 vendor；收集 SLO/trace/fallback/secret metrics。 | 满足 GO to canary/on gate；异常自动 NO-GO 并回滚。 | 2-7d calendar, 0.5-1d agent time | HIGH: 真实流量与上游风控风险。 |
| R-E-A8 Release gate + rollback package | 汇总 R-D provenance、security/billing/clean-room/license/acceptance gate、rollback runbook。 | Owner 可看到 GO/NO-GO matrix；rollback 演练 PASS；fallback retention timer 开始。 | 0.5d | MED: 文档不完整会导致生产操作不可复核。 |

Parallelization note:

- A1 blocks A2/A4/A5 production semantics.
- A3 can run after Owner directory decision, but should not merge before A1.
- A6/A7 depend on A4/A5.
- A8 closes the release gate; it cannot be replaced by “tests passed”.

## 8. Failure Modes and Mitigation

| Failure mode | Impact | Mitigation |
|---|---|---|
| Control-plane channel accidentally exposed over TCP | Upstream auth material disclosure; cross-tenant credential blast radius. | Default deny TCP in production config unless mTLS enabled; startup fail-closed; config test asserts endpoint scheme. |
| UDS socket permission too broad | Local unauthorized process can query route plans. | Dedicated user/group, `0600`/`0660` socket mode, parent dir permissions, per-RPC HMAC, audit caller identity where available. |
| Shared secret leaked via env/log/core dump | RPC spoofing and replay risk. | Secret source redaction, no Debug output, nonce/timestamp/idempotency, rotation runbook, prefer file descriptor/secret manager over plain env where feasible. |
| mTLS cert expiry or SAN mismatch | Rust data plane loses control-plane access; outage. | Expiry alerts, overlap rotation, health check exposes cert time-to-expiry, runbook for emergency rollback to Go hot path. |
| Shadow duplicates real upstream requests | Extra cost, account ban risk, duplicated user-visible side effects. | Default route-only/mock shadow; real upstream shadow only by Owner OCAW with budget cap and no client response use. |
| Attempt report lost under Rust crash | Billing/quota/audit drift. | Bounded retry queue, fail-closed once queue over threshold, future durable queue decision if Owner requires. |
| Fallback silently hides mimicry drift | False production confidence. | Fallback metrics mandatory; vendor dispatch disabled on R-D drift; alerts on fallback rate threshold. |
| Rust/Go trace ID mismatch | Operator cannot investigate incidents. | Contract conformance test for request_id/route_plan_id/attempt_id propagation and log correlation. |
| Dependency/license surprise from optional transport features | MIT release risk. | Dependency license audit before enabling any optional feature in production image; default feature remains conservative. |

## 9. Decision Points

Non-OCAW implementation decisions future agents may make if inside approved scope:

- Keep Go/PG as authority for route selection, quota, billing, account state, and auth core.
- Keep Rust route-plan cache TTL zero or extremely short until Owner approves a cache strategy.
- Keep fallback visible, not silent.
- Treat local capture PASS as necessary but never sufficient for production dispatch.

High-risk decisions that require Owner OCAW are listed below.

## 10. Owner OCAW Decisions

OCAW-1: Control-plane transport baseline

- Options: UDS + per-RPC shared secret/HMAC; mTLS over TCP; shared secret only over loopback/dev.
- Consequences: UDS is simplest and safest for same-host Personal Edition; mTLS is required for cross-host/K8s; shared-secret-only over production TCP leaves encryption gap.
- Ask: Owner chooses R-E production topology and required baseline.
- Wait: Do not implement R-E secure channel until this is confirmed.

OCAW-2: Mainline RPC runtime

- Options: accept grpc-go and gRPC as production contract; require HTTP/JSON shim; dual-stack during migration.
- Consequences: gRPC preserves typed deadlines/status and matches existing Rust tonic client; HTTP/JSON avoids a new Go runtime dependency but increases drift/test burden.
- Ask: Owner decides whether grpc-go is acceptable in mainline.
- Wait: Do not wire Go production control-plane server until runtime choice is confirmed.

OCAW-3: Shadow traffic policy

- Options: route-only shadow; mock/local-capture shadow; real upstream shadow for selected vendors.
- Consequences: route-only/mock avoids duplicate cost and provider risk; real upstream gives stronger evidence but can double spend, trigger upstream risk, and complicate billing.
- Ask: Owner decides whether R-E shadow may send real upstream requests, and for which vendors/accounts/budget.
- Wait: Default plan assumes no real upstream shadow unless Owner explicitly approves.

OCAW-4: Promotion and fallback-retention policy

- Options: conservative 7d shadow + staged canary + 30d fallback retention; faster promotion with smaller windows; longer retention across more releases.
- Consequences: shorter windows speed delivery but weaken drift detection; longer windows preserve rollback confidence but keep dual-path complexity.
- Ask: Owner sets minimum shadow/canary windows and confirms whether `hyper-rustls` fallback may ever be deleted.
- Wait: Do not claim R-E production completion or remove fallback before Owner confirms.

## 11. Checks for Future Execution

- Rust: `cargo fmt --check`, `cargo test`, `cargo clippy -- -D warnings`, feature-specific tests with and without mimicry features.
- Go: unit tests for control-plane server/shim; conformance against the proto/JSON contract; cancellation/deadline/status mapping.
- Security: unauthorized peer rejection; wrong secret/cert rejection; socket permission tests; redaction grep for auth material.
- Billing/quota: every terminal attempt writes one logical settlement path; retry/cancel/error cases covered.
- Ops: metrics/heartbeat/drain/rollback smoke; fallback alert threshold; R-D provenance displayed in logs or release report.
- Release: clean-room, dependency license, acceptance, scenario, billing, security gates all checked before `on`.

## 12. Assumptions

- R-C/R-D exact mimicry work completes before R-E production dispatch.
- Personal Edition can start with same-host Go control plane + Rust data plane unless Owner chooses distributed topology.
- Rust data plane continues to consume short-lived auth material from Go; Rust does not read secrets tables or become account authority.
- Real upstream validation remains Owner-machine gated because sandbox evidence is insufficient.
- Bedrock remains excluded from mainline canary evidence unless Owner later provides real AWS credentials.

## 13. Open Risks

- R-SEC-002 is a hard blocker: without a protected control-plane channel, R-E cannot safely proceed.
- The grpc-go decision can change implementation cost and schedule.
- Real upstream shadow can create cost/provider-account risk if Owner opts into it.
- `hyper-rustls` fallback retention keeps dual-path complexity longer, but deleting it early would remove the safest rollback path.
- Optional mimicry transport features may require fresh dependency license audit before production image inclusion.

## 14. Source Coverage Proof

- `docs/plans/2026-05-14-r3-on-merged-closure-codex.md`: R-C/R-D/R-E scope, fallback requirement, Owner decision context.
- `docs/10_RISK_REGISTER.md`: R-SEC-002 HIGH risk and related transport/security risks.
- `exploratory/rust-core-gateway/merged/READINESS.md`: current NO-GO status, benchmark gaps, grpc-go decision gap.
- `exploratory/rust-core-gateway/merged/proto/route.proto`: route/attempt/health/heartbeat contract and auth-material fields.
- `exploratory/rust-core-gateway/PLAN.md`: Rust data plane boundaries, Go control-plane ownership, RPC tradeoffs.
- `exploratory/rust-core-gateway/SUMMARY.md`: real vendor shadow constraints and mainline next-step gaps.
- `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`: R-D real-upstream gate and fail-closed rules.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`: current transport dependencies and feature gates.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs`: current tonic endpoint/channel shape that needs R-SEC-002 protection before mainline.
- `docs/01_PROJECT_BRIEF.md`, `docs/03_FEATURE_PARITY_MATRIX.md`, `docs/12_AGENT_WORKFLOW.md`, `docs/15_RELEASE_GATES.md`: project success criteria, feature preservation, role/scope rules, release gate rules.
- Skill instructions read: `.agents/skills/pm-orchestrator/SKILL.md`, `.agents/skills/api-gateway-risk-review/SKILL.md`, `.agents/skills/production-scenario-review/SKILL.md`.
- Search-only scanned paths via `rg`: `docs/`, `exploratory/rust-core-gateway/`, `backend/`, `tools/`.

Source files read: docs/plans/2026-05-14-r3-on-merged-closure-codex.md; docs/01_PROJECT_BRIEF.md; docs/03_FEATURE_PARITY_MATRIX.md; docs/10_RISK_REGISTER.md; docs/12_AGENT_WORKFLOW.md; docs/15_RELEASE_GATES.md; exploratory/rust-core-gateway/PLAN.md; exploratory/rust-core-gateway/SUMMARY.md; exploratory/rust-core-gateway/merged/READINESS.md; exploratory/rust-core-gateway/merged/proto/route.proto; exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md; exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml; exploratory/rust-core-gateway/merged/crates/core_gateway/src/route_client.rs; .agents/skills/pm-orchestrator/SKILL.md; .agents/skills/api-gateway-risk-review/SKILL.md; .agents/skills/production-scenario-review/SKILL.md; search-only scanned paths: docs/, exploratory/rust-core-gateway/, backend/, tools/
Lane: scribe
Agent: Codex GPT-5
UTC timestamp: 2026-05-15T12:06:05Z
