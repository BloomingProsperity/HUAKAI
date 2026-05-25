# 2026-05-22 HUAKAI full-codebase deep audit remediation-wave plan - Codex parallel draft

| 字段 | 内容 |
|---|---|
| Owner directive | "Independently draft a remediation-wave plan for the HUAKAI full-codebase deep audit. This is a PARALLEL-DRAFT exercise." |
| 范围 | 只覆盖四份 raw findings 文档中的 53 项：Zone A `GW-01..GW-10`、Zone B `B-01..B-15`、Zone C `C-01..C-18`、Zone D `D-01..D-10`，再加题面补充 `O-1..O-3`，合计 56 项。 |
| 明确不做 | 不执行代码修复；不读取 `docs/process/plans/2026-05-22-audit-remediation-wave-claude.md`；不读取参考项目源码。 |
| 成功标准 | 56 项 finding 每项只落入一个主题、一个 remediation wave；所有 HIGH 先于对应 MED/LOW 依赖被处理；每个 wave 是串行、可提交、可测试单元。 |
| 节奏 | SERIAL。一次只执行一个 wave。 |
| 关键排序事实 | Go `backend/internal/gatewayhttp` 等是 live production data plane；Rust `exploratory/rust-core-gateway/merged/` 当前 exploratory，Zone D + `O-2` 放在最后作为 pre-production hardening。 |
| 估算单位 | 1 codex-day = 一名 Codex worker 完成实现、测试、review 修正和提交说明的一天；不含 Owner 等待时间。 |

## 结论摘要

我把 56 项分成 8 个主题、12 个串行 remediation waves，总估算 30.0 codex-days。最高优先级不是 Rust，而是 live Go 数据面里会立即造成安全泄露、跨租户读取、账务脱链或 trust-chain 空洞的项。

单个最重要的 SYSTEMIC finding：**安全/金钱/trust-chain 写入被设计成 optional 或 best-effort，且失败后仍允许用户可见成功**。这个模式跨越 `gatewayhttp`、`billing`、`eventbus`、`auditledger`、`auth`、`credentialstore`、`channelhealth`、`gateway/forwarder` 和 Rust attempt reporter；必须用一个统一的 "durable fact before success" 策略修，而不是逐个 `_ = audit(...)` 打补丁。

## 主题分组

每个 finding 只出现在一个主题内。

| Theme | 名称 | Findings | 依据 |
|---|---|---|---|
| T1 | 外部安全边界与公开错误面 | `C-01`, `C-02`, `B-14`, `O-1`, `GW-02`, `GW-04`, `GW-05`, `GW-06`, `GW-09`, `C-18`, `B-11`, `C-12` | SSRF、secret redaction、跨租户 audit verify、panic、raw error/body/header/SSE 泄露和错误分类。见 Zone A lines 14-17, 32-45, 59；Zone B lines 79-84, 100-105；Zone C lines 9-19, 75-79, 111-115。 |
| T2 | 缓存与协议身份隔离 | `GW-01`, `C-06`, `C-15`, `C-16` | endpoint/client protocol 未进 cache key、生产 mock credential、HCSF raw fallback、未验证 session protocols 生产注册。见 Zone A lines 8-12；Zone C lines 39-43, 93-103。 |
| T3 | Trust ledger、audit durability 与读取完整性 | `GW-07`, `B-12`, `B-13`, `B-15`, `C-13`, `C-14`, `GW-10`, `C-03`, `C-04`, `C-05`, `C-10` | ledger/signer 可缺、request completion 不强制 audit ref、sanitizer/read corruption 被吞、stream ledger 早签或失败不阻断、admin/credential/channel health audit 非原子或误标。见 Zone A lines 24-28, 47-50；Zone B lines 86-98, 107-112；Zone C lines 21-37, 63-67, 81-91。 |
| T4 | Billing 金钱事实与幂等正确性 | `B-01`, `B-02`, `B-03`, `B-04`, `B-05` | claim folding、synthetic audit id、slot release 回滚账、token overflow、refund replay 金额错误。见 Zone B lines 9-42。 |
| T5 | Routing capacity、health gate 与 routing perf | `C-07`, `C-08`, `C-09`, `C-17`, `O-3` | PASR 无真实 slot 写 claim、slot acquire 无 retry、health fail-open、vendor mapping 空、RoutePlan cache disabled perf。见 Zone C lines 45-61, 105-109。 |
| T6 | Usage/evidence provenance 与计费来源可信度 | `GW-03`, `GW-08`, `C-11` | real traffic 标 mock、usage confidence 假 1.0、streaming 有输出无 usage 却 reported/zero。见 Zone A lines 19-22, 52-55；Zone C lines 69-73。 |
| T7 | Cross-protocol streaming tool-use 正确性 | `B-06`, `B-07`, `B-08`, `B-09`, `B-10` | canonical delta enum 漂移、tool slot 错绑、opaque id 丢失、Responses continuation 和 streaming tool-use 缺失。见 Zone B lines 44-77。 |
| T8 | Rust exploratory pre-production hardening | `D-01`, `D-02`, `D-03`, `D-04`, `D-05`, `D-06`, `D-07`, `D-08`, `D-09`, `D-10`, `O-2` | Rust 未接 production，但存在 header 可伪造、mock bypass、HTTP credential leak、report drop、usage missing、dead metric 等上线前阻断项。见 Zone D lines 9-156。 |

## Wave Breakdown

| Wave | Commit unit | Findings | 估算 | 为什么是一个可提交单元 |
|---|---|---:|---:|---|
| W1 | `security 收紧外部信任边界` | `C-01`, `C-02`, `B-14`, `O-1` | 2.0 | SSRF、secret 泄露、cross-tenant ledger read 和 production panic 都是 live security/reliability 边界；不依赖其他 wave。 |
| W2 | `gatewaycache 隔离协议缓存键` | `GW-01` | 1.0 | 单点修 `cache.KeyInput`、key version 和 gatewayhttp call sites；尽快阻断跨端点 cache 污染。 |
| W3 | `errors 建立公开错误安全模型` | `GW-02`, `GW-04`, `GW-05`, `GW-06`, `GW-09`, `C-18`, `B-11`, `C-12` | 2.0 | 同一类 "raw internal error crosses boundary"；应先建 typed public error/header/trailer/DLQ failure code，再替换所有 leak sites。 |
| W4 | `trustledger 强制账本引用与完整性` | `GW-07`, `B-12`, `B-13`, `B-15`, `C-13`, `C-14` | 3.0 | 定义 request completion、streaming ledger、ledger append/read 的 fail-closed/degraded-mode contract；后续 money/audit wave 依赖此 contract。 |
| W5 | `audit 原子化敏感变更审计` | `GW-10`, `C-03`, `C-04`, `C-05`, `C-10` | 2.5 | 同一系统病：mutation 成功而 audit 可丢；应统一 transaction/DLQ/no-op 禁止策略。 |
| W6 | `billing 修复金钱事实幂等与结算` | `B-01`, `B-02`, `B-03`, `B-04`, `B-05` | 4.0 | 全部在 money-path claim/settle/refund 边界；需要一次性对齐 fingerprint、audit ref、slot release、token validator 和 replay semantics。 |
| W7 | `routing 收紧容量与健康门控` | `C-07`, `C-08`, `C-09`, `C-17`, `O-3` | 3.0 | PASR slot、DBSlotManager retry、channel health gate、vendor mapping、RoutePlan cache 属同一 routing correctness/perf 面。 |
| W8 | `usage 修正用量证据来源` | `GW-03`, `GW-08`, `C-11` | 2.0 | 统一 request evidence label、usage confidence/source、stream missing usage reconciliation，避免真实流量被当 mock 或 zero-token billed。 |
| W9 | `proto 修复跨协议流式工具调用` | `B-06`, `B-07`, `B-08`, `B-09`, `B-10` | 3.5 | 必须先定义唯一 canonical tool delta/id/session 语义，再修 OpenAI Chat、Anthropic Messages、OpenAI Responses renderers。 |
| W10 | `protocols 生产协议注册与投影收口` | `C-06`, `C-15`, `C-16` | 1.5 | 移除 production mock/test-only payload、HCSF control injection fail-closed、session protocol feature-flag。 |
| W11 | `rust 安全边界预生产硬化` | `D-01`, `D-02`, `D-03`, `D-06`, `D-10` | 3.0 | Rust 虽非 live，但这些是上线前 hard blockers：可伪造 metadata、mock bypass、HTTP token leak、provider header pollution、feature bypass。 |
| W12 | `rust 账务遥测预生产硬化` | `D-04`, `D-05`, `D-07`, `D-08`, `D-09`, `O-2` | 2.5 | Rust attempt report、usage、heartbeat、retry class、bytes_in、ACTIVE_CONNECTIONS 都是 control-plane/accounting telemetry 面。 |

Total: **30.0 codex-days**.

## Execution Order And Rationale

1. **W1 security**：`C-01` SSRF 会带 OAuth secrets 出网，`B-14` 是公开 verify 的 cross-tenant read，`C-02` 会漏 `client_secret`，`O-1` 是 production panic。它们都是 "security/reliability bleeding now"。
2. **W2 cache**：`GW-01` 可把 OpenAI/Anthropic/Responses 响应跨 endpoint family 复用，属于 live correctness/trust 风险；改动小，早落地风险低。
3. **W3 errors**：`GW-02/GW-04/GW-05/C-18/B-11` 会把 provider/SQL/internal state 泄给 client 或 operator surfaces。先建公共错误模型，避免后续 waves 继续扩散 `err.Error()`。
4. **W4 trustledger**：`GW-07/B-12/C-14` 这类 ledger missing 仍成功的路径会直接破坏 "商家不能做假"。先定义强 contract，后续 money 和 audit 原子化才能引用同一个语义。
5. **W5 audit**：admin pool、credential lifecycle、Antigravity refresh、channel health audit 都是 "状态已变但审计可丢"。排在 W4 后，因为需要 W4 的 fail-closed/degraded-mode 规则。
6. **W6 billing**：`B-01..B-05` 是直接 money-path。排在 trust/audit contract 后，是因为 `B-02` 必须依赖真实 ledger id/signature policy，`B-03` 也要和 durable financial fact policy 对齐。
7. **W7 routing**：`C-07` 会绕并发 cap，`C-09` fail-open 会继续选坏通道；它们影响成本和可靠性，但多数是 misconfig/health-state latent，排在已确认 money ledger 后。
8. **W8 usage**：`GW-03/GW-08/C-11` 修真实用量和 evidence provenance；排在 billing validator 后，避免新的 usage source 绕过 W6 的 token/value 边界。
9. **W9 proto**：tool-use streaming 是生产功能正确性 HIGH，但不如 security/money/trust-chain 紧急。该 wave 较大，应在数据面基础安全收口后做。
10. **W10 protocols**：mock/test-only config、HCSF fallback、session protocols feature gate 是 latent-on-misconfig 和 conformance 风险，放在 Go live data plane 的后半段。
11. **W11 rust-security**：Rust 是 exploratory，不是当前 production data plane；但安全 hard blockers 必须先于任何 Rust canary。
12. **W12 rust-telemetry**：Rust accounting/telemetry correctness 最后做；没有 W11 的安全边界，W12 的 report/usage 数据也可能围绕错误 route 生成。

我同意 Zone D + `O-2` 可以最后处理，原因不是 severity 低，而是当前未接 production。若 Owner 决定 Rust 进入 canary，本计划必须把 W11/W12 提前到 W7 之前，并新增 "Rust cannot receive production traffic before W11/W12 green" release gate。

## Cross-Wave File-Overlap Matrix

### Go overlap hotspots

| Package / file cluster | W1 | W2 | W3 | W4 | W5 | W6 | W7 | W8 | W9 | W10 | Overlap risk |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|
| `backend/internal/gatewayhttp/audit_verify_handler.go` | X |  |  |  |  |  |  |  |  |  | W1 only；tenant-scoped lookup may add auditledger interface used by W4 tests. |
| `backend/internal/cache/key.go` + gatewayhttp cache call sites |  | X |  |  |  |  |  |  |  |  | W2 only；bump key version before broad gatewayhttp edits to reduce conflict. |
| `backend/internal/gatewayhttp/chat_completions_{handler,dispatch,stream,attempt,handler_headers}.go` |  | X | X |  |  |  |  | X |  |  | W2/W3/W8 all touch hot path；serial order should be cache key -> error model -> usage/evidence. |
| `backend/internal/gatewayhttp/chat_completions_billing.go` |  |  |  | X |  |  |  | X |  |  | W4 `GW-07` and W8 `GW-03/GW-08` overlap; ledger policy lands first. |
| `backend/internal/gatewayhttp/admin_pools_handler.go`, `admin_pool_accounts_handler.go` |  |  |  |  | X |  |  |  |  |  | W5 only, but requires shared audit transaction helper if introduced. |
| `backend/internal/gateway/forwarder*.go` |  |  | X | X |  |  |  | X |  |  | High overlap: W3 sanitizes streaming errors, W4 ledger timing/fail-closed, W8 missing usage. Keep commits ordered and tests narrow. |
| `backend/internal/gateway/event_scanner.go` |  |  | X |  |  |  |  |  |  |  | W3 only. |
| `backend/internal/gateway/upstream_dispatcher_hcsf.go`, `protocol_selector.go`, `stream_scanner.go` |  |  |  |  |  |  |  |  |  | X | W10 only. |
| `backend/internal/billing/{claim_gate.go,settler.go}` |  |  |  |  |  | X |  |  |  |  | W6 only, but uses W4 audit-ref contract. |
| `backend/internal/eventbus/{types.go,bus.go}` |  |  | X | X |  |  |  |  |  |  | W3 sanitizes handler failure/DLQ; W4 tightens RequestCompletionEvent validity. |
| `backend/internal/auditledger/{ledger.go,postgres.go,privacy.go}` | X |  |  | X |  |  |  |  |  |  | W1 tenant-scoped verify and W4 append/read integrity must be sequenced W1 then W4. |
| `backend/internal/auth/{antigravity_token_provider.go,sanitizer.go,storm_controller.go}` | X |  |  |  | X |  |  |  |  |  | W1 endpoint/redaction/panic first; W5 audit durability later in same provider file. |
| `backend/internal/credentialstore/{postgres_store.go,types.go}` |  |  |  |  | X |  |  |  |  | X | W5 audit semantics and W10 Azure mock validation overlap package but different files mostly. |
| `backend/internal/channelhealth/{failover.go,store_postgres.go,types.go}` |  |  |  |  | X |  | X |  |  |  | W5 signed audit and W7 fail-open/vendor health should not be combined; W5 first. |
| `backend/internal/pool/**` |  |  |  |  |  |  | X |  |  |  | W7 only; includes PASR, DBSlotManager, vendor mapping, route-plan cache. |
| `backend/internal/proto/**` |  |  |  |  |  |  |  |  | X |  | W9 only but broad across OpenAI/Anthropic/Gemini renderers. |

### Rust overlap hotspots

| Rust cluster | W11 | W12 | Overlap risk |
|---|---:|---:|---|
| `exploratory/rust-core-gateway/merged/src/listener.rs` | X | X | W11 removes mock/header trust bypass; W12 may adjust report paths. W11 must land first. |
| `exploratory/rust-core-gateway/merged/src/account_planner.rs` | X |  | W11 only. |
| `exploratory/rust-core-gateway/merged/src/proxy_engine/{mod.rs,relay.rs,headers.rs,http_client.rs,auth.rs}` | X | X | W11 secures endpoint/header/auth; W12 changes usage/status/bytes report. W11 first to prevent telemetry on unsafe route. |
| `exploratory/rust-core-gateway/merged/src/attempt_reporter/**` |  | X | W12 only. |
| `exploratory/rust-core-gateway/merged/src/heartbeat.rs`, `resource_limits.rs` |  | X | W12 only. |
| `exploratory/rust-core-gateway/merged/src/mimicry/**` | X |  | W11 only. |

## Dependency Constraints

| Dependency | 必须先落地 | 后续依赖 | 原因 |
|---|---|---|---|
| Public error model | W3 | W10 and later gateway changes should use it opportunistically | HCSF fail-closed、protocol registration errors、new billing/gateway errors should return stable `public_code/public_message` rather than `err.Error()`. |
| Ledger/audit-ref contract | W4 | W5, W6, W8 | W5 audit mutation and W6 money event cannot decide fail-closed vs degraded-mode without W4 的 required ledger/signature semantics。 |
| Auditledger tenant-scoped lookup | W1 | W4 tests and verify hardening | `B-14` changes ledger interface/API surface; W4 corruption/read tests should use the new scoped lookup. |
| Token/count validation helper | W6 | W8 | `C-11/GW-08` may introduce estimated/ambiguous usage values; those values must pass W6 的 centralized bounds/source validation。 |
| Routing slot invariants | W7 | Any later PASR/health performance work | `O-3` route-plan cache should not cache or accelerate plans that bypass slot/health gates. |
| Canonical tool-use enum/id contract | first half of W9 | second half of W9 | Renderer fixes are not correct until upstream adapters and canonical event types agree on one enum and opaque id rule. |
| Rust route/security hardening | W11 | W12 and any Rust canary | Telemetry/report correctness is meaningless if request planning can still be forged or mock-bypassed. |

## Systemic Findings

1. **Durable fact before success is missing.** Audit ledger append, audit event insert, DLQ enqueue, channel health signer, credential audit, admin mutation audit, streaming ledger append, billing audit request id, and Rust terminal report are all allowed to fail softly in different ways. Root cause is not one missing `if err != nil`; it is absence of a shared policy: which facts are required before client success, which can enter durable reconciliation, and which are dev-only optional.
2. **Trust boundaries are inferred from nullable dependencies or client-controlled metadata.** Examples: nil ledger/signer/store means continue, OAuth endpoint comes from credential JSON, Rust route plan trusts headers, provider org/project headers pass through, health record missing means allow. The safe default should be "untrusted until authenticated/configured by control plane".
3. **Internal error/detail and public protocol surfaces are not separated.** `err.Error()` reaches JSON bodies, headers, SSE frames, eventbus state, and DLQ payloads. This calls for one error taxonomy and redaction path shared by gateway, eventbus, auth, and protocol adapters.
4. **Protocol canonicalization lacks single-owner contracts.** Tool deltas, tool call IDs, session continuation, Responses streaming, and placeholder protocol registration drift independently. W9/W10 should produce conformance tests that make "supported" mean cross-protocol round-trip tested.
5. **Test stubs hide real production constraints.** Findings mention nullable audit ids, transaction rollback, serializable slot conflicts, missing tenant `WHERE`, ignored DB corruption, and no-op audit writers. Unit stubs that return nil will keep passing while production loses money or trust evidence.

## Test Discipline

### Waves requiring real PostgreSQL integration tests

| Wave | Required real-PG coverage | Why stubs are insufficient |
|---|---|---|
| W1 | `B-14` tenant-scoped audit verify query; `O-1` panic regression if backed by real auth/session rows. | Need real `WHERE tenant_scope_ref` behavior and duplicate/request-id collision rows. |
| W4 | `B-12/B-13/B-15/C-13/C-14/GW-07` ledger append/read/fail-closed paths. | Real JSONB decode, Merkle root bytes, nullable columns, transaction behavior, signer absence and ledger DB errors matter. |
| W5 | `GW-10/C-03/C-04/C-05/C-10` mutation + audit transaction/DLQ behavior. | Need prove mutation rolls back or durable recovery row exists when audit insert/signature fails. |
| W6 | `B-01..B-05` claim reserve/settle/refund. | Unique indexes, committed-claim replay, transaction rollback, slot release rows, signed cost fields and nullable audit ids are DB realities. |
| W7 | `C-07/C-08/C-09/C-17/O-3` PASR slot acquisition, serializable retry, health gate, route plan cache. | Serializable conflicts and slot/account in-flight counters cannot be trusted from in-memory stubs. |
| W8 | `GW-03/GW-08/C-11` usage provenance through request -> ledger -> billing rows. | Must prove real rows record `evidence_label`, `usage_source`, confidence, pending reconciliation and token values. |

### Mandatory adversarial negative tests

| Wave | Negative tests |
|---|---|
| W1 | SSRF rejects `169.254.169.254`, loopback, private CIDR, non-HTTPS, and redirect-to-internal; OAuth error body containing `client_secret`, `client_assertion`, `password` is redacted; audit verify with missing/wrong `tenant_scope_ref` cannot read another tenant; storm controller production path cannot panic on malformed or absent state. |
| W2 | Same tenant/vendor/model/body across `/v1/chat/completions`, `/v1/messages`, `/v1/responses` must produce different L2 keys; old key version is ignored; cross-protocol cached body is never served. |
| W3 | Upstream 4xx/5xx body with provider account hints never appears in JSON body, headers, SSE `event:error`, DLQ public payload, or eventbus state; scanner TCP reset is not classified as overflow; stream-start-after-error metadata is either trailer-declared or server-log-only. |
| W4 | Missing signer/ledger/audit ref fails closed or creates explicit durable degraded item; redactor failure cannot sign raw payload; malformed ledger JSON/root returns corruption error; streaming final ledger duration uses actual completion, not first byte. |
| W5 | Audit insert failure during pool/credential/channel-health mutation leaves no committed mutation unless durable recovery item exists; `SetState(active)` is not recorded as `credential_disabled`; audit writer nil/no-op is rejected in production mode. |
| W6 | Cross-user claim folding attempt with same API key/idempotency/payload must conflict or produce distinct claim; synthetic `audit-refund-*` is rejected; slot release miss cannot erase committed usage/billing; negative and `>math.MaxInt32` tokens are rejected; refund replay returns stored capped amount. |
| W7 | PASR actual/canary with nil/unavailable slot manager fails closed; serialization failure retries then succeeds without double slot; unknown/missing health state is blocked in production; empty vendor request is rejected; route-plan cache cannot bypass health/slot changes. |
| W8 | Real upstream dispatch cannot emit `EvidenceMock`; estimated or missing usage cannot have confidence `1.0/reported`; stream with delivered chunks and no usage enters pending reconciliation or configured minimum billing path. |
| W9 | Tool argument chunks survive OpenAI -> canonical -> Anthropic/Responses render; text block before tool block does not shift tool slot; opaque non-hex tool id round-trips; Responses `previous_response_id` output-only request is accepted when session context exists and rejected when truly orphan; unsupported streaming tool path fails before stream or emits full Responses events. |
| W10 | Azure credential containing only `mock_token_endpoint` is rejected in production; HCSF control injection failure does not raw-forward inbound body; unverified session families are feature-flagged/fail-loud without conformance tests. |
| W11 | Rust route planning ignores client tenant/model/stream headers unless trusted internally; production with `HUAKAI_MOCK_UPSTREAM_ENDPOINT` fails fast; `http://` planned vendor endpoint with Bearer is rejected; OpenAI org/project headers from client are stripped; `mimicry-boring` cannot bypass unsupported profile intent. |
| W12 | Attempt reporter full queue cannot silently drop successful billable reports; non-stream JSON usage is parsed into report; heartbeat reports real or `unknown` values, never hardcoded healthy zero; 429/408 are retryable/rate-limited; chunked/H2 request bytes are counted; `ACTIVE_CONNECTIONS` changes during request lifecycle. |

## Coverage Check

| Zone | Covered IDs |
|---|---|
| Zone A | `GW-01`, `GW-02`, `GW-03`, `GW-04`, `GW-05`, `GW-06`, `GW-07`, `GW-08`, `GW-09`, `GW-10` |
| Zone B | `B-01`, `B-02`, `B-03`, `B-04`, `B-05`, `B-06`, `B-07`, `B-08`, `B-09`, `B-10`, `B-11`, `B-12`, `B-13`, `B-14`, `B-15` |
| Zone C | `C-01`, `C-02`, `C-03`, `C-04`, `C-05`, `C-06`, `C-07`, `C-08`, `C-09`, `C-10`, `C-11`, `C-12`, `C-13`, `C-14`, `C-15`, `C-16`, `C-17`, `C-18` |
| Zone D | `D-01`, `D-02`, `D-03`, `D-04`, `D-05`, `D-06`, `D-07`, `D-08`, `D-09`, `D-10` |
| First-round carryover | `O-1`, `O-2`, `O-3` |

No feature is dropped. No clean-room/reference-project source is involved. High-risk implementation areas for future execution include auth core, billing ledger, quota/slot enforcement, database schema/migrations, and production deployment; this document is a plan only and does not authorize those high-risk changes without Owner confirmation where project rules require it.
