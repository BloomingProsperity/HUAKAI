# 2026-05-15 L2-A4 OpenSSL TLS Mimicry Adapter Skeleton - Codex

| Field | Value |
| --- | --- |
| Owner directive | "L2-A4: OpenSSL TLS mimicry adapter skeleton (production, feature-gated)" |
| Scope | Add optional `openssl`/`tokio-openssl` dependencies, add a feature-gated OpenSSL adapter skeleton, expose it only behind `mimicry-openssl`, and add one local handshake smoke test. Out of scope: profile-driven TLS field injection, `ProxyEngine` wiring, `build_http_client` changes, vendored OpenSSL, `wreq`, `boring`, utility presets, and external reference source reads. |
| Success criteria | Default build/test remain green without compiling OpenSSL; `--features mimicry-openssl` build/test are green; new smoke test proves the OpenSSL client adapter can complete a local TLS handshake; `Cargo.lock` records only the needed new dependency graph. |
| Time estimate | 45-90 minutes wall clock, mostly dependency resolution and full Rust test runtime. |
| Blast radius | Limited to exploratory Rust core gateway crate, one new gated production module, one gated integration test, and Cargo metadata/lockfile. No Go backend, DB schema, auth, billing, quota, deployment, or `LICENSE` changes. |
| Failure modes | System OpenSSL headers/libs may be missing; surface stderr and do not force `vendored`. Bare `SslContext` may need explicit client state/SNI setup; verify against local official rust-openssl API. Smoke server certificate verification may fail if client defaults change; keep test aligned with the skeleton rather than adding trust/profile plumbing. |
| Decision points | Owner already chose system-linked OpenSSL baseline via L2-A0. Stop only if OpenSSL linking fails, new non-permissive dependencies appear, or implementation would require touching high-risk files. |
| Pre-execution checklist | Read current Cargo feature/dependency layout; read current `mimicry` module exports; read allowed local rust-openssl API for `SslContext`, `Ssl`, SNI, and handshake state; confirm no existing `mimicry-openssl` plan/file; avoid prohibited reference project source. |
| Cross-discussion note | This is the Codex independent plan artifact for the current Codex execution. No Claude L2-A4 draft was read or used in this session. |
| UTC timestamp | 2026-05-15T04:04:37Z |

## Concrete Execution Order

1. Add optional dependencies and `mimicry-openssl` feature in `crates/core_gateway/Cargo.toml`.
2. Add `src/mimicry/openssl_adapter.rs` with `#[cfg(feature = "mimicry-openssl")]`, bare TLS context construction, SNI setup, TCP connect, and async OpenSSL handshake.
3. Gate-export the module from `src/mimicry/mod.rs`.
4. Add `tests/mimicry_openssl_adapter_test.rs` behind `#![cfg(feature = "mimicry-openssl")]` with a local TLS-terminating server and one handshake smoke assertion.
5. Run formatting, default build/test, feature build/test, and inspect `Cargo.lock` deltas.

## Clean-Room And Dependency Notes

- Reads are restricted to HUAKAI internal files and `/home/codex/refs/rust-openssl` official API material.
- No non-MIT reference project source is read.
- L2-A0 already recorded `openssl 0.10.79` as Apache-2.0 and `openssl-sys 0.9.115` as MIT-compatible baseline. This atom verifies the actual lockfile delta after Cargo resolves dependencies.

Owner 中文摘要：这是 Codex 对 L2-A4 的独立执行计划。计划范围只覆盖 feature-gated OpenSSL adapter skeleton、Cargo 可选依赖和本地 smoke test；不接 `ProxyEngine`，不做 profile 注入，不读受限参考项目源码，不启用 vendored。预期风险主要是系统 OpenSSL 链接和裸 `SslContext` 默认 TLS 行为，若失败会直接暴露错误而不是绕过。
