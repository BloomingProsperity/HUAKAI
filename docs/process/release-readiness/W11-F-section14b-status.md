# W11-F §14b: Chrome impersonation (Gemini CLI 0.42.0) — implementation status

Last updated: 2026-05-26 UTC

## Origin

§13 passive pcap (2026-05-26) observed Gemini CLI 0.42.0 emitting Chrome-style
TLS impersonation against `cloudcode-pa.googleapis.com` (`ja3_hash =
59686f806cae30344b525e99af5b655d`, `ja4 = t13d1516_h2_dea800f94266_
0041c1594faa`). HUAKAI's existing boring builder targets the prior 0.41.2
Node-stock TLS shape; matching the new Chrome shape requires three additional
TLS features:

1. Ext 27 — `compress_certificate` (RFC 8879) advertising brotli (alg id 2)
2. Ext 17513 — `application_settings` (ALPS, Chrome's legacy codepoint)
3. Ext 21 — padding to exactly 192 bytes (Chrome's specific size, vs
   BoringSSL's default round-to-512 logic)

Plan source: `docs/process/plans/2026-05-26-boring-vs-rquest-codex-consult.md`
(Owner-approved synthesis Pick D — extend boring crate, no `rquest` add).

## §14b.1 — data layer / schema (commit 0a8551c + fix 275eb79, COMPLETE)

Added `cert_compression_algorithms: Vec<u16>` and `alps_protocols:
Vec<String>` fields to:

- `TlsProfile` (`crates/core_gateway/src/mimicry/tls_profile.rs:75-81`)
- `RawFingerprintProfile` deserialization
  (`crates/core_gateway/src/mimicry/profile.rs:537-541`) with
  `#[serde(default)]` so anthropic / codex / kiro profiles (empty defaults)
  stay valid.
- TryFrom forwarding from raw → typed profile

Schema bug fix in 275eb79: validator gates `key_share_groups` / `psk_modes`
non-empty requirement on `supported_versions.contains(0x0304)` (TLS 1.3 only).
Required because codex 0.128.0 §13-observed profile is TLS 1.2-only.

Mimicry test suite: **28 passed; 0 failed**.

## §14b.2 — boring builder wiring (this commit, COMPLETE)

### Vendored boring Rust wrappers (`vendor/boring/boring/src/ssl/mod.rs`)

Two thin safe wrappers over existing BoringSSL public C APIs (no C-level
patch needed — `SSL_add_application_settings` and
`SSL_set_alps_use_new_codepoint` are already exported by BoringSSL).
Documented in `vendor/boring/MODIFICATIONS.md` per Apache-2.0 §4
attribution.

- `SslRef::add_application_settings(&mut self, proto: &[u8], settings:
  &[u8]) -> Result<(), ErrorStack>` — calls
  `ffi::SSL_add_application_settings`. Per-SSL: must be called on the
  `ConnectConfiguration` returned by `connector.configure()`, which derefs
  to `SslRef`.
- `SslRef::set_alps_use_new_codepoint(&mut self, use_new: bool)` — calls
  `ffi::SSL_set_alps_use_new_codepoint`. `false` selects Chrome's legacy
  17513 codepoint, `true` the standard 17613.

### HUAKAI stub brotli compressor (`mimicry/cert_compressor.rs`, NEW)

`StubBrotliCompressor` implements boring's public `CertificateCompressor`
trait with `ALGORITHM = CertificateCompressionAlgorithm::BROTLI` (id 2),
`CAN_COMPRESS = false`, `CAN_DECOMPRESS = true`. The `decompress` impl
uses the trait default (returns `io::Error::other("not implemented")`).

Stub is sufficient because:

1. Chrome impersonation is judged on the ClientHello bytes (JA3, extension
   order). The brotli compression of server-side cert chains only matters if
   the server actually picks the algorithm.
2. The offline wire-level acceptance test in `boring_wire.rs` captures
   ClientHello bytes via local TCP listener with a 3-second timeout. Server
   never picks brotli because no real handshake completes.
3. **Production use against `cloudcode-pa.googleapis.com` will need a real
   brotli decompressor** — that's §14c (requires `brotli` crate runtime
   dep, Owner approval per Risk-Based Confirmation Rule).

### Wiring (`mimicry/client_hello_builder.rs`)

- `build_boring_connector`: if profile lists ext 27 AND
  `cert_compression_algorithms` non-empty → register HUAKAI compressor for
  each id (only `2` = brotli supported in §14b.2;
  `UnsupportedCertCompressionAlgorithm` surfaced for others, no silent
  drop).
- `configure_boring_connection`: if profile lists ext 17513 AND
  `alps_protocols` non-empty → force legacy codepoint
  (`set_alps_use_new_codepoint(false)`) + register each protocol name via
  `add_application_settings(proto_bytes, &[])` (empty settings — Chrome's
  observed wire shape per §13 capture).

### Test status

`cargo test -p core_gateway --features mimicry-boring --lib mimicry` (Docker
`rust:latest` + cmake + build-essential, 2026-05-26):
- **39 passed; 0 failed; 1 ignored**
- The 1 ignored is `gemini_advanced_boring_client_hello_byte_level_matches_
  profile` — moved to `#[ignore]` because of pre-existing failure (see
  §14b.3 below). Bisect (2026-05-26) confirmed disabling both §14b.2
  branches reproduces the same `client 必须发出 ClientHello bytes` panic,
  so this is NOT a §14b.2 regression — it's an open §13 gap that §14b.2
  alone doesn't close.
- The 3 non-gemini wire tests (anthropic / codex_cli / kiro) **PASS**,
  validating that the boring builder + ext 22 / ECH / OCSP / SCT / cert
  compression / ALPS infrastructure is sound for the simpler profiles.

## §14b.3 — gemini wire-level ClientHello emit (RESOLVED 2026-05-27)

Root cause investigation:

1. Captured `tokio_boring::connect` error via diagnostic logs →
   "TLS handshake failed unexpected EOF" with empty `ErrorStack`.
2. Bisect (disable both §14b.2 branches in `client_hello_builder.rs`):
   gemini wire test STILL fails with "raw is empty" → not a §14b.2
   regression.
3. Walked BoringSSL `ssl_setup_key_shares` (extensions.cc:2215). Found
   line 2266: `default_key_shares.TryPushBack(supported_group_list[0])`.
4. Profile's `supported_groups` starts with GREASE (35466).
   `SSLKeyShare::Create(GREASE)` returns nullptr → handshake silently
   aborts at line 2304 before any wire bytes are written.

Fix:

1. **Vendored boring**: added `SslRef::set_client_key_shares` thin
   wrapper over BoringSSL's stock `SSL_set1_client_key_shares` (a
   per-SSL API documented at ssl.h:2641-2667). Documented in
   `vendor/boring/MODIFICATIONS.md` under §14b.3 with Apache-2.0 §4
   attribution.
2. **HUAKAI builder**: added `apply_real_key_shares` helper in
   `client_hello_builder.rs::configure_boring_connection`. It filters
   GREASE from `profile.tls.key_share_groups` and hands the real groups
   to `set_client_key_shares`. boring's grease_enabled mode still emits
   a fake GREASE key share automatically (extensions.cc:2294), so the
   wire pattern matches Chrome's `[GREASE_kse, X25519_kse]` shape.
3. **Test helper fix**: `assert_profile_extension_order` in
   `boring_wire.rs` was filtering padding (21) from `expected` but
   GREASE from `observed` — asymmetric and broke any profile with
   GREASE in `extensions`. Made the filter symmetric (GREASE filtered
   from both sides).

Result: gemini wire test now PASSES with JA3 hash matching the §13
captured value `59686f806cae30344b525e99af5b655d`. `#[ignore]` removed.

Hypothesis space documented earlier (BoringSSL padding-to-512 conflict)
was wrong — padding wasn't the issue. The actual issue was upstream of
even the extension writer: handshake setup aborted before any extension
processing began.

## §14c — production brotli decompression (RESOLVED 2026-05-27)

Owner approval received 2026-05-26 for `brotli` runtime crate dep.

Resolution: renamed `StubBrotliCompressor` → `BrotliCompressor`, replaced
the trait-default `decompress` with a one-line
`brotli::BrotliDecompress` call. `brotli = "8.0"` added under the
`mimicry-boring` feature in `core_gateway/Cargo.toml` (BSD-3-Clause /
MIT licensed, MIT-compatible per CLAUDE.md #11). Default-features build
stays slim (brotli only pulled in when `mimicry-boring` enabled).

Real cloudcode-pa.googleapis.com handshake integration test still
deferred (requires env-gated Gemini OAuth creds and an HTTP-2 chunk
test that's out of scope for the offline byte-level acceptance test).
Marked tracked in W11-F-section14b-status as future work; not blocking
W11-F release.

## §14b.4 — dispatch / resolver test alignment (RESOLVED 2026-05-27)

When integration tests ran with `--locked --no-default-features`
(per Owner's diagnostic Docker invocation), 4 tests in
`tests/mimicry_profile_test.rs` + 1 in `tests/mimicry_dispatch_test.rs`
failed against the new classifications introduced by W11-F F-2.2
synthesis D-S3 (kiro KnownGapBlocked) + D-S4 (gemini OpenSslAdapter
via boring) + §14b.2 wire support. The tests were locking the OLD
classifications (kiro UnsupportedTemplate, gemini UnsupportedTemplate).

Fix: updated 5 test assertions to lock the NEW behavior so future
regressions get caught. Test names also renamed where the old name
implied wrong behavior (e.g.,
`mimicry_resolver_respects_known_gap_over_boring_feature_gemini` →
`mimicry_resolver_allows_gemini_when_boring_feature_present`).

Also fixed `mimicry_dispatch_test.rs:50` — manual `TlsProfile { ... }`
construction was missing the §14b.1 `cert_compression_algorithms` and
`alps_protocols` fields. Added with empty defaults (this fixture
targets the OpenSSL native dispatch path, no Chrome features).
