# 2026-05-17 Rust Wave R-2 Rquest BoringSSL - Codex

| Owner directive | "你是 HUAKAI codex executor lane, 任务 = Rust Wave R-2: rquest + BoringSSL data plane 集成" |
| --- | --- |
| Scope | In: Rust core gateway dependency wiring, outbound HTTPS data-plane client, mimicry backend resolver/test updates, removal of unused hyper-rustls data-plane dependency. Out: frontend, Go backend, `LICENSE`, billing/auth/quota/database/deploy, control-plane tonic TLS behavior, rquest/curl_cffi/BoringSSL/reference project source. |
| Success criteria | `core_gateway` builds with rquest as the outbound HTTPS client path; existing request method/header/body/stream response contract remains compatible; mimicry resolver can select an rquest emulator backend for Anthropic profile; R-1 deferred Anthropic mimicry test is no longer ignored if exact public API support is available; requested cargo checks/tests pass or failures are reported honestly. |
| Time estimate | 2-4 hours agent time, plus cargo build/test time. |
| Blast radius | Medium: `exploratory/rust-core-gateway/merged` Rust workspace dependency graph, `core_gateway::proxy_engine`, and feature-gated mimicry tests. Control-plane rustls through tonic must remain intact. |
| Failure modes | rquest public API or feature names may differ from the requested feature labels; vendored BoringSSL may require network/toolchain pieces unavailable in sandbox; crate license/transitive license may need follow-up audit; exact JA3/JA4 byte assertions may require APIs not exposed without reading prohibited source; cargo may fail because new crates are not cached and network is restricted. |
| Mitigations | Use only crates.io/docs.rs/public rustdoc metadata, never source/README/examples; keep API adaptation narrow and contract-preserving; do not delete tonic/rustls control-plane dependencies; if exact emulator API cannot be confirmed safely, land a compile-safe abstraction or report the blocker instead of fabricating behavior. |
| Decision points | Stop for Owner confirmation before changing high-risk files, adding non-rquest runtime dependencies, touching Go/frontend/business logic, or reading prohibited source. Record any deferred JA3/JA4 exact-match gap as a blocker rather than silently shrinking functionality. |
| Pre-execution checklist | Confirm clean-room restrictions; read Claude roadmap and relevant HUAKAI Rust files only; verify current dependency usage; confirm rquest latest stable/version/features from official package metadata without reading source; implement smallest viable data-plane integration; run cargo check/build/tests as far as sandbox permits. |

## Concrete Execution Order

1. Inspect workspace and `core_gateway` Cargo manifests for current HTTP/TLS dependency boundaries.
2. Inspect `proxy_engine/http_client.rs`, mimicry backend resolver, and Anthropic mimicry tests.
3. Confirm rquest crate version/features from official package metadata without reading rquest source, README, examples, or vendored code.
4. Add rquest dependency at workspace and crate level, keeping tonic control-plane TLS dependencies intact.
5. Replace the outbound HTTPS adapter with rquest while preserving local request/response contract.
6. Add `RquestEmulator` mimicry backend selection for Anthropic profile with OpenSSL as a safe fallback where unsupported.
7. Remove hyper-rustls only after no local Rust code references it.
8. Un-ignore and update the deferred Anthropic test only if exact public API support is available within clean-room constraints; otherwise leave a transparent blocker with no fake pass.
9. Run `cargo check`, `cargo build --workspace`, `cargo test --features mimicry-openssl -p core_gateway --lib`, and `cargo test --workspace` when feasible.

## Execution Note 2026-05-17

Codex stopped before modifying Rust manifests or data-plane code because the required `rquest` dependency did not have a selectable non-yanked 5.x release in the local crates.io index, and the sandbox could not refresh crates.io (`Failed to connect to 127.0.0.1 port 8118`). A temporary resolver check with `rquest = "5"` failed on `5.0.0`, `5.1.0`, and `5.2.0` all being yanked; `rquest-util = "2.2.1"` also failed because it depends on the same yanked `rquest` range and is already rejected for production by `R-LIC-003`.

Baseline `cargo check -p core_gateway` passed before any Rust implementation change. The R-2 feature target is not removed; it is blocked pending Owner dependency decision recorded as `R-DEP-001` in `docs/10_RISK_REGISTER.md`.
