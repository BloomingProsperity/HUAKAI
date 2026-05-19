# 2026-05-17 Rust Wave R-1 Anthropic OpenSSL - Codex

| Owner directive | "Rust Wave R-1: Anthropic Lane 2b reattach + OpenSSL adapter 完整 impl" |
| --- | --- |
| Scope | In: Rust core mimicry profile loader, Anthropic built-in profile, backend resolver mapping, feature-gated OpenSSL adapter/tests. Out: frontend, Go backend, `LICENSE`, billing/auth/quota/database/deploy, rquest/BoringSSL Wave R-2, other vendor profiles. |
| Success criteria | `anthropic-claude-code` loads as a builtin profile, resolver returns the OpenSSL backend instead of KnownGapBlocked when `mimicry-openssl` is available, local mock TLS handshake passes, and requested cargo checks/tests pass or failures are reported honestly. |
| Time estimate | 1-2 hours agent time, plus cargo verification time. |
| Blast radius | Limited to `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry`, HUAKAI fingerprint template copy, and Rust tests. Default builds must remain feature-gated and not enable OpenSSL by default. |
| Failure modes | Local OpenSSL may not support every sampled group/sigalg; profile may need safe public-API representation; preflight capture may reject native OpenSSL extension/point-format differences; workspace tests may expose unrelated pre-existing failures. |
| Mitigations | Use only `openssl` public APIs already present in the crate, fail loudly on unsupported exact fields, keep local mock handshake separate from real upstream, and do not read any prohibited reference source. |
| Decision points | Stop only if implementation requires a new runtime dependency, reference source reading, high-risk file changes, production secrets, or non-feature-gated transport dispatch. |
| Pre-execution checklist | Read `docs/RULES.md`, the Claude Rust closure roadmap, mimicry profile/resolver/adapter files, existing mimicry tests, and `tools/fingerprint-collector/templates/anthropic-claude-code.json`; confirm `git status`; implement and verify. |

## Concrete Execution Order

1. Add the Anthropic built-in profile using HUAKAI's collected template as source data, completing missing schema fields locally.
2. Extend `BuiltinProfile`, `ProfileMode`, and `ProfileVendor`.
3. Remove Anthropic-specific KnownGapBlocked resolver override so profile intent selects OpenSSL.
4. Reuse the existing OpenSSL adapter public-API field injection path; add only narrow support needed for the Anthropic profile if cargo/tests prove a gap.
5. Add focused Anthropic profile/resolver/OpenSSL mock handshake tests.
6. Run `cargo check --features mimicry-openssl`, `cargo test --features mimicry-openssl -p core_gateway`, and `cargo test --workspace` from the Rust workspace when feasible.
