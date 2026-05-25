# 2026-05-19 rust UDS HTTP transport fix

| Owner directive | "修 codex review v3 P2#5 (Rust UDS panic 测试) 通过添加 TransportBaselineKind::Http 变体。" |
| Scope | In: `exploratory/rust-core-gateway/merged/crates/core_gateway/` route-client construction, config parsing, and named integration tests; this plan artifact. Out: reference reverse-proxy source, Go backend, frontend, proto, billing, pool, admin, production defaults, new dependencies. |
| Success criteria | `build_router` and `GatewayState::new` return construction errors instead of panicking; tests can select HTTP control-plane transport with `HUAKAI_TRANSPORT_BASELINE=http`; requested cargo build and test commands pass with no failed tests. |
| Time estimate | 60-90 minutes wall clock; one Codex executor lane. |
| Blast radius | Rust core gateway router startup and route-client transport selection. If wrong, startup error propagation or test transport selection may break integration tests. |
| Failure modes | Missing callsite conversions after `build_router` returns `Result` mitigated by workspace build; accidentally changing UDS default mitigated by keeping `TransportBaselineKind::Uds` as fallback/default; mTLS regression mitigated by preserving existing feature-gated branch; test-only HTTP transport leaking as default mitigated by env-only selection. |
| Decision points | Stop for Owner only if a required fix touches high-risk files, adds runtime dependencies, changes auth/billing/quota/schema/deployment, or requires production default changes. |
| Pre-execution checklist | 1. Confirm working tree scope. 2. Read only HUAKAI Rust core gateway files needed for transport construction/config/tests. 3. Implement Phase A error propagation. 4. Build. 5. Implement Phase B HTTP baseline. 6. Build. 7. Implement Phase C test env/callsites. 8. Build and run requested tests plus workspace tests if feasible. 9. Stage, run required Codex uncommitted review, fix blockers, commit without push. |

## Phase A

- Change `route_client_from_transport_baseline(&config)` to return `Result<RouteClient, GatewayError>`.
- Change `GatewayState::new(config)` to return `Result<Self, GatewayError>`.
- Change `build_router(config)` to return `Result<Router, GatewayError>`.
- Let `run(config)` propagate router construction with `?`.
- Verify with `cargo build --workspace --no-default-features`.

## Phase B

- Add `Http` to `TransportBaseline` and `TransportBaselineKind`.
- Parse `HUAKAI_TRANSPORT_BASELINE=http`.
- Make HTTP baseline carry no UDS socket or mTLS paths.
- Add `RouteClientTransportConfig::http(endpoint: Uri)`.
- In `from_transport_config`, use `parts.endpoint.connect_lazy()` for HTTP without TLS configuration.
- Wire `GatewayConfig::route_transport_config()` and env parsing for HTTP.
- Verify with `cargo build --workspace --no-default-features`.

## Phase C

- Add `HUAKAI_TRANSPORT_BASELINE=http` to the seven requested test configs.
- Update all `build_router(...)` test callsites to unwrap with `.expect("build_router")` or propagate with `?` according to local test style.
- Verify:
  - `cargo build --workspace --no-default-features`
  - `cargo test --workspace --no-default-features --test attempt_reporter_test`
  - `cargo test --workspace --no-default-features --test listener_test`
  - `cargo test --workspace --no-default-features --test proxy_engine_test`
  - `cargo test --workspace --no-default-features`

## Risk Notes

- This is a medium-risk Rust architecture/test change because function signatures and transport enum matching change, but it avoids production default changes and adds no dependency.
- Clean-room risk is low because the task reads and changes only HUAKAI-internal Rust source and tests.
- Security risk is low because HTTP transport is selected explicitly for local/mock control-plane tests and does not alter UDS default or mTLS feature gates.
