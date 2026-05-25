# 2026-05-25 R-3-A-fix-6 sigalgs raw API investigation

| Owner directive | `CONTEXT: R-3-A-fix-6 boring set_sigalgs_raw API 调研 ... 不实施, 仅起草调研报告` |
| Scope | In: feasibility report for raw `signature_algorithms` ClientHello emission in the Rust sidecar BoringSSL path. Out: implementation, tests, `git add`, commit, push. |
| Success criteria | Report states current setter surface, C/Rust feasibility, option comparison, recommendation, complexity estimate, and Phase 4/5 blocking status. |
| Output nature | Investigation only. No implementation performed. |
| Observed regions | 12 |
| Inferences | 6 |
| Open questions | 2 |

## 1. Bottom line

R-3-A-fix-6 is feasible and should be treated as a small vendor patch plus a focused Rust-sidecar wiring change, not as a large TLS rewrite.

Recommended option: **A, but narrow it to a ClientHello/verify-side raw sigalg setter**. The name can remain `SSL_CTX_set_sigalgs_raw` if the team wants the TODO wording, but the behavior should be documented as "raw `signature_algorithms` advertisement / verify preference bytes", not as a general signing preference API.

Reason: the existing C layer already stores ClientHello signature algorithm advertisement in `verify_sigalgs`, copies that array from `SSL_CTX` to per-connection config, and writes each `uint16_t` directly into extension 13. The missing piece is that the public setters validate against BoringSSL's known algorithm table before populating that array. A raw setter can bypass only that validation while keeping the existing writer path.

This does **not** block Phase 4 ECH or Phase 5 PQ existing-fields wire proof. It **does** block any final claim that the Anthropic CLI sidecar ClientHello is byte-level exact for extension 13, and should be fixed before Go-Rust production socket wiring or before closing the sidecar fingerprint slice as production-ready.

## 2. Current evidence

### 2.1 Existing R-3-A-fix-2..5 vendor patch pattern

The existing local Boring patch sequence already uses narrow public C setters plus Rust `SslContextBuilder` methods. `MODIFICATIONS.md` records `SSL_CTX_set_extension_order`, `SSL_CTX_set_tls13_cipher_order`, and `SSL_CTX_set_client_hello_profile`, with Rust wrappers in `boring/src/ssl/mod.rs` rather than new primary logic in `connector.rs` (`exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:41`, `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:126`, `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:137`, `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:147`).

The current Rust wrapper surface matches that pattern: `set_sigalgs_list` is string-based, while HUAKAI local setters for extension order, TLS 1.3 cipher order, and raw ClientHello profile fields live on `SslContextBuilder` (`exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1946`, `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1975`, `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1987`, `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1999`). `SslConnectorBuilder` derefs to `SslContextBuilder`, so a new `SslContextBuilder::set_sigalgs_raw` is sufficient unless the team wants a convenience method in `connector.rs` (`exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs:131`, `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs:142`).

### 2.2 Current sidecar gap

The sidecar built-in profile already stores all 26 captured numeric signature algorithm IDs in `signature_algorithms`, but the active Boring configuration still uses a 10-name `sigalgs` string. The TODO explicitly states that the string only covers Boring-supported standard names and that full fidelity needs a raw setter before production sidecar connection (`exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:19`, `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:24`, `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:26`).

`boring_ctx.rs` currently calls `.set_sigalgs_list(&profile.sigalgs)` and never uses `profile.signature_algorithms` for the Boring ClientHello (`exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:44`). Existing wire tests parse ciphers, extensions, groups, versions, and EC point formats, but not extension 13 contents, so a green JA3/JA4 test can still hide this mismatch (`exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:494`, `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:539`).

### 2.3 BoringSSL C layer

No `SSL_CTX_set_sigalgs_raw`, `SSL_set_sigalgs_raw`, or equivalent raw-named API is present in the observed header. The available public APIs are:

- `SSL_CTX_set_signing_algorithm_prefs` / `SSL_set_signing_algorithm_prefs`, which accept `const uint16_t *` but are for private-key signing preferences (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:1187`).
- `SSL_CTX_set_verify_algorithm_prefs` / `SSL_set_verify_algorithm_prefs`, which accept `const uint16_t *` and control peer signature verification preferences (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:3006`).
- OpenSSL-compatible list APIs `SSL_CTX_set1_sigalgs_list` / `SSL_set1_sigalgs_list`, which parse textual names (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:5560`).

However, the raw-looking preference APIs still reject unknown IDs through `set_sigalg_prefs`, which calls `get_signature_algorithm` and returns `SSL_R_INVALID_SIGNATURE_ALGORITHM` for IDs absent from BoringSSL's table (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_privkey.cc:103`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_privkey.cc:556`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_privkey.cc:575`). That table does not include Ed448, DSA, RSA-PSS-PSS, ML-DSA, or the SHA224 legacy IDs in the captured 26-item list (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_privkey.cc:51`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:1129`).

The useful part is the writer path. `SSL_new` copies `ctx->verify_sigalgs` into per-connection config (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:540`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:550`). Extension 13 writes each value from `tls12_get_verify_sigalgs` via `CBB_add_u16` (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:308`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:315`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:1010`). Therefore, a raw setter can be small: copy caller-supplied IDs into `ctx->verify_sigalgs`, bypassing only `get_signature_algorithm` validation.

## 3. Option comparison

| Option | Description | Pros | Cons | Verdict |
|---|---|---|---|---|
| A | Add a HUAKAI local raw sigalg setter in Boring C + Rust wrapper, then call it from sidecar using the 26 numeric IDs. | Closest to captured wire; reuses existing extension 13 writer; matches existing R-3-A-fix-2..5 patch style; no new runtime dependency. | Increases vendor delta; must document verify-only semantics; requires discriminating tests for extension 13. | **Recommended before production socket wiring.** |
| B | Keep the 10 supported string sigalgs and try to splice unknown IDs via raw/unknown extension assembly. | Avoids adding the exact raw setter name if implemented outside vendor. | Not viable as a clean Boring-level workaround: extension 13 is already a built-in `kExtensions[]` entry, duplicate TLS extensions are rejected, and no observed Boring public custom-extension API can safely override it (`exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:48`, `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3633`). Byte-splicing a serialized ClientHello would also desynchronize BoringSSL's transcript unless the handshake internals are patched. | **Do not choose.** Higher risk than A. |
| C | Accept the current 10-item sigalgs string and defer raw sigalgs until Phase 5+ Owner gate. | Zero vendor change now; acceptable while sidecar is not connected to production; aligns with existing deferred raw tunnel posture. | Leaves a known fidelity gap; current tests can pass without checking extension 13; cannot honestly claim exact Anthropic CLI ClientHello. | **Acceptable temporary posture only.** |

## 4. Recommended implementation shape if Owner later approves A

This section is implementation guidance only; no patch was made.

1. Add C API in the vendored header near the other HUAKAI local setters:
   `SSL_CTX_set_sigalgs_raw(SSL_CTX *ctx, const uint16_t *ids, size_t ids_len)`.
   Optional but useful: also add `SSL_set_sigalgs_raw(SSL *ssl, const uint16_t *ids, size_t ids_len)` for per-connection overrides.

2. Implement in `ssl/ssl_privkey.cc`, not as a wrapper around `SSL_CTX_set_verify_algorithm_prefs`, because that wrapper intentionally rejects unknown IDs. The minimal context function should:
   - reject `ids == nullptr && ids_len != 0`;
   - optionally reject duplicate IDs to avoid malformed profile data;
   - copy the raw IDs into `ctx->verify_sigalgs`;
   - not touch signing prefs unless a future client-cert slice needs that explicitly.

3. Add Rust wrapper in `boring/src/ssl/mod.rs`:
   `pub fn set_sigalgs_raw(&mut self, ids: &[u16]) -> Result<(), ErrorStack>`.
   This follows the existing `set_tls13_cipher_order` / `set_client_hello_profile` pattern. A `connector.rs` method is not required because `SslConnectorBuilder` derefs to `SslContextBuilder`.

4. Update `tls-sidecar/src/boring_ctx.rs` to prefer `profile.signature_algorithms` through the raw setter and retire the `sigalgs` string as either compatibility-only or remove it in a separately planned cleanup. The profile already has the 26 IDs.

5. Extend `tls-sidecar` wire parser/tests to parse extension 13 and assert equality with `profile.signature_algorithms`. Mutation check: remove `0x0905` or reorder the list and prove the test turns red.

6. Update `vendor/boring/MODIFICATIONS.md` with R-3-A-fix-6 attribution and exact files touched.

## 5. Complexity estimate

| Work item | Estimated size | Notes |
|---|---:|---|
| C header + C implementation | 30-60 LoC | 30 LoC if context-only and no duplicate check; 50-60 LoC if adding `SSL_set_*` and duplicate validation. |
| Rust boring wrapper | 8-15 LoC | Add one `SslContextBuilder` method. Bindgen should expose the symbol from the public header as with earlier local setters. |
| Sidecar wiring | 10-25 LoC | Replace `set_sigalgs_list` call path with raw IDs; keep string fallback only if backwards compatibility is needed. |
| Discriminating tests | 50-100 LoC | Parse extension 13 and assert 26 IDs; add mutation-sensitive check. This is the most important quality gate. |
| Vendor attribution docs | 10-20 LoC | Append R-3-A-fix-6 to `MODIFICATIONS.md`. |
| Total | 0.5-1 engineering day | Add another 0.5 day if C-level BoringSSL tests are required in addition to sidecar wire tests. |

## 6. Phase 4/5 dependency status

- **Phase 4 ECH**: not blocked. ECH config/retry/fail-closed work can proceed without raw sigalgs.
- **Phase 5 PQ existing-fields wire proof**: not blocked if the test scope is supported_groups/key_share and does not claim full ClientHello exactness.
- **Phase 5C / pre-production sidecar gate**: blocked for exact Anthropic CLI TLS fidelity. Before Go-Rust production socket wiring, the sidecar should either implement A or explicitly run in a documented Safe Equivalent mode that does not claim byte-level signature_algorithms parity.
- **Current production impact**: none observed. The deferred raw tunnel note says sidecar is not currently connected to production and the raw-tunnel H2 issue is scheduled for the production connection slice (`docs/process/reviews/DEFERRED-sidecar-raw-tunnel-h2-alpn.md:12`, `docs/process/reviews/DEFERRED-sidecar-raw-tunnel-h2-alpn.md:16`).

## 7. Open questions

1. Should the public C name be generic `SSL_CTX_set_sigalgs_raw` or more precise `SSL_CTX_set_client_hello_sigalgs_raw` / `SSL_CTX_set_verify_sigalgs_raw`? Recommendation: use the precise name if Owner allows changing the TODO wording; otherwise document verify-only semantics beside the generic name.
2. Does HUAKAI need an `SSL_set_*` per-connection variant now? Recommendation: no for the current sidecar, because `SslConnectorBuilder` context setup is enough. Add it only if later per-request profile switching reuses one context.

## Owner 中文摘要

本次只做调研并创建报告，没有实施补丁、没有 staging/commit/push。真实观察是：sidecar profile 已保存 26 个 sigalg 数字 ID，但当前 Boring 配置只用 10 项字符串；Boring C 层没有 raw-named setter，已有 `uint16_t*` prefs API 仍会按已知算法表校验并拒绝非标 ID；ClientHello 扩展 13 的实际 writer 会直接从 `verify_sigalgs` 写 `uint16_t`，所以 A 方案可用一个很窄的 raw setter 补齐。合理推断是：R-3-A-fix-6 不阻塞 Phase 4 ECH / Phase 5 PQ proof，但阻塞生产 sidecar exact-fidelity 出口。没有功能缩水；clean-room 风险低，因为只读 HUAKAI vendored BoringSSL 和 HUAKAI 自有 sidecar 代码；安全风险主要是 vendor delta 增加和 raw ID 可能影响证书签名算法协商，需要测试和 Owner gate。需要 Owner 后续确认的是 raw setter 命名、是否加 per-SSL variant、以及是否把它放进 Phase 5C/pre-production gate。

## Source coverage proof

- `docs/RULES.md`: Owner start gate, truth-first, low-risk docs posture.
- `docs/process/reviews/DEFERRED-sidecar-raw-tunnel-h2-alpn.md`: sidecar production connection is deferred; raw H2 tunnel issue is non-production today.
- `docs/process/plans/2026-05-24-boringssl-phase-4-5-synthesis.md`: Phase 4/5 sequence and optional 5C gate context.
- `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md`: R-3-A-fix-2..5 local setter pattern and attribution requirements.
- `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs`: current Rust setter surface and existing local wrapper placement.
- `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs`: `SslConnectorBuilder` deref behavior.
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h`: current C public sigalg APIs and local HUAKAI setter region.
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc`: context-to-connection config copy for `verify_sigalgs` and local setter style.
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_privkey.cc`: signature algorithm validation table and validating setter path.
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc`: extension 13 writer and duplicate-extension constraints.
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs`: Anthropic built-in profile, 26 raw IDs, 10-name TODO string.
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs`: current sidecar Boring config and wire test parser coverage.
