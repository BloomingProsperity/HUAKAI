# 2026-05-15 R-E transport baseline config switch

| Owner directive | "R-E transport 用 UDS + mTLS 双 baseline,配置开关切换。Default UDS (单机 / personal edition),mTLS (多机 / SaaS)。" |
| Scope | In: HUAKAI internal merged Rust exploratory crate route-client config, endpoint builder tests, short runbook, fixture/example config. Out: real certificate issuance/rotation/validation, production traffic wiring, LICENSE, billing, quota, auth core, new dependencies. |
| Success criteria | `transport_baseline` defaults to `uds`; `uds` builds a tonic endpoint from a socket path; `mtls` builds endpoint plus `ClientTlsConfig` from placeholder cert files; invalid baseline fails fast; cargo tests pass with `CARGO_TARGET_DIR=/home/codex/cargo-targets/r-e-switch`. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation lane. |
| Blast radius | Rust data-plane route client construction and startup config only. Existing TCP mock-control-plane tests must remain compatible. |
| Failure modes | Tonic UDS connector API may require runtime connector wiring; mitigate by isolating endpoint/TLS construction behind testable helpers and preserving existing `RouteClient::new` TCP path. Placeholder cert parsing may reject invalid PEM; mitigate with syntactically valid test PEM fixtures. |
| Decision points | No Owner sign-off needed unless implementation would require new dependency, database/auth/billing/quota change, or real certificate lifecycle logic. |
| Pre-execution checklist | Read HUAKAI rules and round-1 synthesis; inspect `config.rs`, `route_client.rs`, existing route client tests, crate features/dependencies; confirm no reference repos are read; avoid staging/committing. |
| Concrete execution order | Add config types/defaults; add route-client endpoint/TLS builder helpers; add tests and placeholder fixture files; add short runbook/config example; run formatter and cargo test; report git status and source-read tail. |

Execution note: current crate does not enable tonic TLS and Owner forbids new dependencies in this lane, so the mTLS path builds endpoint parts plus local TLS config inputs from files; actual tonic `ClientTlsConfig` activation remains an R-SEC-002 dependency-approval item.
