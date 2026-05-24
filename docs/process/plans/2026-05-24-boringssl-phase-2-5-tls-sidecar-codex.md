# 2026-05-24 boringssl phase 2.5 tls-sidecar codex

| Owner directive | `[OWNER AUTHORIZED 2026-05-24T11:15Z workspace-write — 接通 HUAKAI vendor boring R-3-A-fix-2..5 API 到 tls-sidecar]` |
| Scope | In: `tls-sidecar` profile parsing, built-in profile setter fields, `boring_ctx.rs` conditional setter calls, wire ClientHello tests. Out: `vendor/boring/`, frozen Go packages, git staging/commit/push. |
| Success criteria | `cargo build -p tls-sidecar` and `cargo test -p tls-sidecar` pass from `exploratory/rust-core-gateway/merged`; targeted mutation checks demonstrate the new tests fail when each guarded setter path is damaged; final command prints `DONE`. |
| Time estimate | 1 work session, mostly test and parser wiring. |
| Blast radius | TLS sidecar ClientHello construction only. Incorrect logic can silently change JA3/JA4 mimicry or force custom BoringSSL paths for old profiles. |
| Failure modes | Setter fields still tied to expectation fields; default profiles unexpectedly call HUAKAI local setters; tests compare non-discriminating fixtures; serde migration breaks existing built-in profile or H2 settings table parsing. Mitigation: wire-level tests parse ClientHello tables and backward-compat tests remove new fields. |
| Decision points | No new Owner decision expected. The Owner already authorized using the HUAKAI vendored boring APIs and forbade modifying `vendor/boring/` or git staging/commits. |

## 参考项目对照

- CLIProxyAPI anchor cite: `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/auth/claude/utls_transport.go:107` sets the TLS server name in the same Go process, and `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/auth/claude/utls_transport.go:108` builds the outbound TLS client with a browser-like preset. Summary: the adjacent reference keeps TLS mimicry in-process; it does not introduce a Go-to-Rust TLS sidecar boundary.
- Envoy AI Gateway `envoyproxy/ai-gateway@4d3eae8b35c4:README.md:3` describes the project as using Envoy Gateway for generative-AI traffic, and `envoyproxy/ai-gateway@4d3eae8b35c4:docs/proposals/006-mcp-gateway/proposal.md:63` says local protocol work is still proxied through Envoy and Envoy Gateway TLS configuration. Summary: the adjacent reference delegates transport control to Envoy rather than a HUAKAI-style Rust BoringSSL sidecar connected from Go.

| Pre-execution checklist | 1. Read `vendor/boring/MODIFICATIONS.md` for R-3-A-fix-2 through fix-5 state. 2. Read `boring/src/ssl/mod.rs` setter signatures. 3. Read `tls-sidecar/src/boring_ctx.rs` and `profile.rs`. 4. Confirm existing plan §D Phase 1-3 says HUAKAI vendor boring is the locked path. 5. Add tests before implementation edits. |

## Execution Order

1. Add profile parsing tests proving the new setter fields are optional for old TOML and populated for `anthropic-cli-mimicry-v1`.
2. Add wire ClientHello tests that independently guard extension order, TLS 1.3 cipher order, raw cipher/group profile, EC point formats, and the all-empty default path.
3. Run the new tests and confirm they fail for the expected missing fields / unconditional setter behavior.
4. Update `profile.rs` to parse the new unsigned vector fields with serde-backed TOML deserialization while preserving existing `ProfileStore` API and H2 settings shape.
5. Update `boring_ctx.rs` so it calls `set_extension_order`, `set_tls13_cipher_order`, and `set_client_hello_profile` only when the corresponding profile fields are non-empty.
6. Run targeted tests, then mutation self-checks by temporarily damaging each guarded input and verifying the specific test fails.
7. Restore the intended code and run `cargo build -p tls-sidecar`, `cargo test -p tls-sidecar`, and `echo DONE`.

## Clean-room Provenance

- Source files read: `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/auth/claude/utls_transport.go:107`; `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/auth/claude/utls_transport.go:108`; `envoyproxy/ai-gateway@4d3eae8b35c4:README.md:3`; `envoyproxy/ai-gateway@4d3eae8b35c4:docs/proposals/006-mcp-gateway/proposal.md:63`
- Lane: specifier
- Agent: codex-cli
- UTC: 2026-05-24T1407Z
