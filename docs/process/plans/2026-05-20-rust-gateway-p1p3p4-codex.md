# 2026-05-20 Rust Gateway P1/P3/P4 Implementation Plan - Codex

| Field | Content |
|---|---|
| Owner directive | "HUAKAI Rust 网关 (exploratory/rust-core-gateway/merged) — 规划任务。只产出一份 plan md,不写任何 .rs 实现代码,不执行 git,不读参考项目源码。" |
| Scope | Plan only for P1 drain-mode request gating, P3 circuit-breaker half-open behavior, and P4 non-blocking logging. |
| Out of scope | Any `.rs` implementation, `git` commands, reference-project source reads, schema changes, auth/billing/quota logic, deployment changes. |
| Success criteria | The later implementation can land as 3 independent commits, each with focused tests, no feature shrinkage, and explicit Owner confirmation points for high-risk changes. |
| Time estimate | Planning: complete in this file. Later implementation estimate: P1 2-3h, P3 4-6h, P4 2-3h, plus 1-2h for checks/review. |
| Blast radius | P1 affects HTTP ingress and LB health behavior; P3 affects control-plane routing availability under failures; P4 affects process-wide logging/tracing initialization and dependency graph. |
| Failure modes | P1 may accidentally block `/metrics` or leak `DRAIN_MODE` between tests; P3 may allow more than one half-open probe or let heartbeat close the breaker; P4 may drop `WorkerGuard` early or introduce license/dependency risk. Mitigations are listed per item below. |
| Decision points | Owner confirmation is needed before adding the P4 runtime dependency. Owner should also decide whether P3 should expand now into time-window failure-rate semantics; Codex recommends deferring that expansion. |

## Source Context Read

No reference-project source was read. Internal source regions used for this plan:

- `crates/core_gateway/src/lib.rs:152`: `build_router` currently mounts `/healthz`, `/metrics`, listener router, and `RequestBodyLimitLayer`.
- `crates/core_gateway/src/lib.rs:170`: `run` spawns `HeartbeatWorker`, then serves the router at `axum::serve`.
- `crates/core_gateway/src/lib.rs:191`: `/healthz` always returns OK today.
- `crates/core_gateway/src/heartbeat.rs:23`: `is_drain_mode()` and `set_drain_mode()` already exist around a global atomic.
- `crates/core_gateway/src/heartbeat.rs:85`: heartbeat ack updates `DRAIN_MODE`.
- `crates/core_gateway/src/route_client.rs:167`: `RouteClientOptions` has threshold and cooldown, but no half-open options.
- `crates/core_gateway/src/route_client.rs:195`: breaker state is currently only consecutive failures plus `open_until`.
- `crates/core_gateway/src/route_client.rs:273`: `query_route` fails fast while circuit is open.
- `crates/core_gateway/src/route_client.rs:391`: success resets the breaker; failure opens it after threshold.
- `crates/core_gateway/src/route_client.rs:559`: control-plane endpoint keepalive is already configured with HTTP/2 keepalive interval, timeout, and while-idle enabled.
- `crates/core_gateway/src/tracing_init.rs:26`: `install` currently returns only the optional OTLP provider.
- `crates/core_gateway/src/tracing_init.rs:33`: JSON/text fmt layers are built without `with_writer`.
- `crates/core_gateway/src/main.rs:12`: tracing install return value is currently not held.
- `crates/core_gateway/Cargo.toml:56`: tracing dependencies currently include `tracing` and `tracing-subscriber`, but not `tracing-appender`.

## Pre-Execution Checklist

1. Confirm Owner approval for actual implementation and for adding `tracing-appender`.
2. Re-read the touched files before editing, because this plan intentionally did not run `git` and the worktree may change.
3. Keep commits independent: P1, P3, P4 in that order.
4. Run targeted tests after each commit candidate, then full `cargo test -p core_gateway` before review.
5. Stage each independent change and run the required Codex review workflow before committing.

## P1 - Drain Mode Request Path Wiring

### Recommended Implementation

Add a small dedicated module instead of piling middleware into `lib.rs`:

- Add `crates/core_gateway/src/drain.rs`.
- Update `crates/core_gateway/src/lib.rs` to expose/use the module and to make `/healthz` drain-aware.
- Update `crates/core_gateway/tests/observability_test.rs` and possibly `crates/core_gateway/tests/route_client_test.rs` for ingress behavior.

Implementation details:

- Build a tower/axum middleware layer that checks `heartbeat::is_drain_mode()` before proxy/listener routes execute.
- Return `503 Service Unavailable` with `Connection: close` for new data-plane requests when drain mode is true.
- Keep `/healthz` explicit: in drain mode it should return unhealthy status, preferably HTTP 503 with JSON such as `{"status":"draining"}`.
- Keep `/metrics` reachable during drain so operators can scrape the draining node. If Owner wants every path including `/metrics` to short-circuit, that is a product decision; my recommendation is to exempt `/metrics`.
- Attach the drain layer as the outermost router layer, before body limiting and route handling in observable behavior. A test must prove that a drained request does not call the control plane or upstream.
- Ensure the middleware only gates new requests. Existing in-flight streaming requests should not be cancelled by the flag flip; this falls out naturally if the check runs only at request entry.

### Tests

- Add a test that sets `set_drain_mode(true)`, calls `/healthz`, and asserts HTTP 503 plus unhealthy/draining JSON; reset the global flag with a guard to avoid cross-test leakage.
- Add a data-plane request test with mock control plane and mock upstream: while drained, `POST /v1/messages` returns 503, includes `Connection: close`, and leaves `route_queries_seen()` and upstream request count at zero.
- Add a regression test that `/metrics` still returns Prometheus text while drained, unless Owner chooses to block metrics too.
- Add or update a slow/streaming scenario if existing helpers make it cheap: start one request, flip drain, assert the in-flight request completes and the next request gets 503.

### Risks

- Global `DRAIN_MODE` is shared across tests; every test touching it needs cleanup.
- Middleware ordering can be subtle in axum/tower; the no-control-plane-call test is the acceptance guard.
- Blocking `/metrics` would reduce operator visibility during drain; do not do that without Owner confirmation.

### Owner Confirmation

No extra confirmation required for the default P1 plan. Owner confirmation only needed if `/metrics` should also be blocked during drain.

## P3 - Circuit Breaker Half-Open Probe

### Recommended Implementation

Split breaker behavior into a small module:

- Add `crates/core_gateway/src/circuit_breaker.rs` for state transitions.
- Update `crates/core_gateway/src/lib.rs` with `mod circuit_breaker;` or `pub(crate)` equivalent.
- Update `crates/core_gateway/src/route_client.rs` to delegate breaker state to the new module.
- Update `crates/core_gateway/src/mock_control_plane.rs` only if needed for scripted route failures in integration tests.
- Update `crates/core_gateway/tests/route_client_test.rs` and internal module tests.

State model:

- `Closed`: normal requests allowed; retryable final failures increment failure count.
- `Open`: requests fail fast until cooldown expires.
- `HalfOpen`: exactly one probe request is allowed after cooldown. Other concurrent requests fail fast while the probe is in flight.
- Probe success closes the breaker and resets failure count.
- Probe failure immediately reopens the breaker for a fresh cooldown.

Important behavioral constraints:

- Count failures at the top-level `query_route` call, not per retry attempt. The current loop records failure inside each retry attempt, which can burn the threshold faster than intended when retries are enabled.
- Count only retryable control-plane availability failures toward the breaker. Non-retryable request/contract errors should not poison global routing availability.
- Do not let heartbeat or health-check success close a half-open route breaker. Otherwise the heartbeat loop can reopen the floodgate without a real route probe.
- Keep `report_attempt` behavior conservative: it may still fail fast while the control-plane channel is considered unavailable, but its success should not close the route-query breaker unless that is an explicit design decision.
- Preserve the existing endpoint keepalive settings already present in `configure_endpoint`; add a test/comment only where useful. If Owner wants configurable keepalive values, treat that as a separate config change.

### Consecutive Failures vs Time-Window Failure Rate

Recommendation: do not replace consecutive-failure semantics in this P3 commit. Implement half-open first, fix per-request failure accounting, and keep the existing threshold/cooldown knobs.

Reason:

- Half-open directly fixes the stampede bug with the smallest behavior change.
- A time-window failure-rate breaker needs additional choices: window length, bucket size, minimum sample count, failure ratio threshold, retry interaction, and low-traffic behavior. Those are product/SRE policy decisions, not just a patch.
- Consecutive failures are deterministic and easy to test; once failures are counted per top-level route query and only for retryable availability errors, the current model is less sensitive to tiny transient jitter.
- Recommended follow-up P3b: add a windowed-rate breaker behind new config, for example `min_samples`, `window_ms`, and `failure_ratio`, with metrics to calibrate before enabling by default.

### Tests

- Unit tests in `circuit_breaker.rs` with an injected clock:
  - below threshold stays closed;
  - threshold opens;
  - open before cooldown denies;
  - after cooldown exactly one probe is acquired under concurrent attempts;
  - probe success closes;
  - probe failure reopens.
- Integration tests in `route_client_test.rs`:
  - after threshold failure, immediate next query fails fast and does not increment mock route query count;
  - after cooldown, only one concurrent request reaches the mock control plane;
  - successful probe closes the breaker and allows later requests;
  - failed probe reopens immediately and the following request is fast-failed.
- Regression test for retry accounting: with `retry_attempts > 0`, one top-level failing `query_route` should count as one breaker failure, not one failure per retry attempt.
- Regression test that heartbeat success during open/half-open does not close the route-query breaker.

### Risks

- Concurrent half-open logic must be atomic; a mutex-free CAS state machine is preferred, but a small lock is acceptable if tests prove no probe stampede and no async lock is held across await.
- Changing failure counting from per-attempt to per-request alters breaker sensitivity; document this in the commit body.
- Existing tests that poke `RouteClientInner` internals should be rewritten to test public/snapshot behavior rather than duplicating breaker internals.

### Owner Confirmation

Owner should confirm whether time-window failure-rate semantics are required in this same implementation round. My recommendation is: no, defer to P3b after half-open lands and metrics are observed.

## P4 - Non-Blocking Logging

### Recommended Implementation

Affected files:

- Update `crates/core_gateway/Cargo.toml` to add `tracing-appender`.
- Update `Cargo.lock` as a normal result of dependency resolution during implementation.
- Update `crates/core_gateway/src/tracing_init.rs`.
- Update `crates/core_gateway/src/main.rs`.

Implementation details:

- Use `tracing_appender::non_blocking(std::io::stdout())` and pass the returned writer to both JSON and compact fmt layers with `.with_writer(...)`.
- Replace the current `install` return type with a guard struct, for example `TracingGuards`, that owns:
  - the `tracing_appender::non_blocking::WorkerGuard`;
  - the optional `opentelemetry_sdk::trace::TracerProvider`.
- Bind the guard in `main.rs`, for example as a local variable that lives until `runtime.block_on(core_gateway::run(config))` returns.
- Update comments so they state that the returned guard protects both non-blocking log flushing and optional OTLP provider lifetime.
- Keep stdout as the sink; do not introduce file logging or log rotation in this patch.

### Tests

- Update existing tracing init tests to handle the new guard return type.
- Add a focused unit test around a helper that creates the non-blocking stdout writer and returns a guard, so the guard lifetime is explicit even when global subscriber initialization cannot be repeated.
- Keep the existing "multiple install calls may return Internal but must not panic" behavior.
- Run a smoke test that starts the binary path enough to ensure `main.rs` holds the guard variable through runtime execution; if direct binary testing is too heavy, cover it by compile plus existing smoke tests.

### Risks

- Dropping `WorkerGuard` immediately silently reintroduces log loss/blocking risk; `main.rs` must hold it.
- New dependency means license and supply-chain review is required. Expected dependency is a permissive Tokio tracing ecosystem crate, but implementation must verify direct and transitive licenses before commit.
- Non-blocking logging uses an internal queue; under sustained stdout backpressure it may drop logs depending on crate behavior. Record this operational tradeoff in comments or docs if the crate exposes a lossy mode/default.

### Owner Confirmation

Required before implementation: adding `tracing-appender` is a new runtime dependency and must be approved under the project's high-risk dependency rule. If Owner rejects the dependency, the safe equivalent is a small owned `MakeWriter` bridge to a bounded background stdout task, but that is more custom code and should not be preferred without a reason.

## Implementation and Commit Order

1. Commit `P1 drain mode ingress gate`.
   - Files: `crates/core_gateway/src/drain.rs`, `crates/core_gateway/src/lib.rs`, `crates/core_gateway/tests/observability_test.rs`, possibly `crates/core_gateway/tests/route_client_test.rs`.
   - Checks: targeted observability/route tests, then `cargo fmt --check`.

2. Commit `P3 circuit breaker half-open`.
   - Files: `crates/core_gateway/src/circuit_breaker.rs`, `crates/core_gateway/src/lib.rs`, `crates/core_gateway/src/route_client.rs`, optionally `crates/core_gateway/src/mock_control_plane.rs`, `crates/core_gateway/tests/route_client_test.rs`.
   - Checks: route client unit/integration tests, concurrency half-open test, then `cargo fmt --check`.

3. Commit `P4 non-blocking tracing stdout`.
   - Files: `crates/core_gateway/Cargo.toml`, `Cargo.lock`, `crates/core_gateway/src/tracing_init.rs`, `crates/core_gateway/src/main.rs`.
   - Checks: dependency/license audit, tracing init tests, smoke tests, `cargo fmt --check`, full `cargo test -p core_gateway`.

After each staged commit candidate, run the required `codex exec review --uncommitted --full-auto` from repo root before committing.

## Required Follow-Up Records

- If P3 time-window failure-rate is deferred, create a roadmap or risk-register note so the requirement is not silently dropped.
- If P4 dependency is approved, record license findings for `tracing-appender` and any new transitive crates.
- If `/metrics` remains available during drain, note that as an intentional operator-visibility choice.

## Owner Summary

做了什么: 独立起草 P1/P3/P4 的实施计划, 未写 Rust 实现。改了哪些文件: 仅新增本计划文件。为什么这样做: 三个问题分别影响排空正确性、控制面故障恢复和 Tokio worker 阻塞风险, 需要按独立 commit 降低回滚与审查成本。有没有功能缩水: 没有, P3 的时间窗口失败率建议作为 P3b 明确跟进而不是删除。有没有 clean-room 风险: 没有, 未读参考项目源码。有没有安全风险: P4 新增运行时依赖需 Owner 确认和许可证审计; P1/P3 是可测试的可靠性修复。哪些地方需要 Owner 确认: P4 `tracing-appender` 依赖、P3 是否立即扩大为时间窗口失败率、P1 是否要在 drain 时也拦截 `/metrics`。下一步建议: Owner 确认依赖和 P3 取舍后, 按 P1 -> P3 -> P4 三个独立 commit 执行并逐个跑测试和 Codex review。
