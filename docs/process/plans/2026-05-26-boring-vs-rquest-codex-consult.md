# HUAKAI Chrome-impersonation library decision — Codex consult (2026-05-26)

Owner directive: "和 codex 讨论下" — get independent view on whether to extend
boring crate vs add rquest, after §13 found Gemini CLI 0.42.0 uses Chrome-style
TLS impersonation that HUAKAI may need to mimic.

## Claude's parallel view (recorded before dispatch)

**Recommendation: extend boring crate, do NOT add rquest yet.**

Reasoning:
- HUAKAI already has `mimicry::boring_tls_connector` + `client_hello_builder` +
  `openssl_adapter` + `boring_wire` modules using `boring` crate. Cipher list,
  extensions order, ALPN, sigalgs, groups all already wired.
- BoringSSL **is** Chrome's TLS stack — Chrome impersonation is native to it.
  GREASE is enabled by default in boring.
- The 3 unknowns are: ext 27 (compress_certificate, RFC 8879),
  ext 17513 (application_settings, ALPS — Chrome-private), and ext 21 padding
  with specific 192-byte length.
- Adding rquest = 2 outbound TLS stacks running side-by-side in HUAKAI =
  maintenance burden. Better to extend the one we have.

Risks of "extend boring":
- ext 17513 ALPS may require FFI to raw BoringSSL C API if boring crate
  doesn't expose `SSL_add_application_settings`-style call.
- Same for cert compression and padding callback.

Risks of "add rquest":
- Whole new dependency tree (rquest pulls boring transitively but with its
  own pinned version → potential dep conflicts).
- Different abstractions vs current `mimicry::profile` → schema bridge work.
- Maintenance: two HTTP client stacks.

Open question Codex should weigh:
- Is the §14a-boring-capability-audit (extend) cheaper than the §14-rquest-add
  (replace) for the actual deliverable (Gemini Chrome mimicry on outbound)?

## Prompt sent to codex

(See `.codex-boring-vs-rquest-prompt.md` for raw prompt; piped via stdin.)

## Codex's reply (received 2026-05-26)

**Pick: D — audit-first gated decision.** Both Claude (above) and Codex
independently converged on option D: run `§14a-boring-capability-audit`
first, extend HUAKAI's existing vendored `boring` if the three gaps are
small, keep `rquest` as a Gemini-only fallback if padding/ALPS cannot be
made byte-stable without an ugly fork. Do NOT pick C (port to wreq) now.

### Key findings Codex surfaced that strengthen the decision

1. **HUAKAI already vendors boring crate**, not just uses crates.io
   default. `exploratory/rust-core-gateway/vendor/boring/` +
   `boring-sys/`. Adding thin wrappers / new bindings is consistent with
   existing architecture, not a new pattern. Cargo.toml:11 confirms the
   vendor relationship.
2. **Vendored BoringSSL headers** at
   `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:3369`
   already contain `SSL_add_application_settings` (for ALPS ext 17513)
   and `SSL_CTX_add_cert_compression_alg` (for cert compression ext 27).
   So 2 of the 3 unknowns have native BoringSSL C APIs — just need Rust
   bindings.
3. **Vendored boring crate already exposes** cert-compression concepts
   and extension constants for cert compression, padding, application
   settings at `vendor/boring/boring/src/ssl/mod.rs:590`.
4. **The hard piece is padding (ext 21) to exactly 192 bytes.** HUAKAI's
   own `mimicry/boring_wire.rs:158` already documents this limitation —
   boring's public API lacks a forced padding setter; current behavior
   tolerates omitted padding.

### License clarifications (Codex web-searched)

- `rquest`: documented as BoringSSL-based HTTP/2 emulation, **Apache-2.0**.
  MIT-compatible per CLAUDE.md #11 permitted-license vendoring policy.
- `wreq`: Cargo metadata says **Apache-2.0**, depends on `boring2` with
  cert-compression features.
- Neither is a license blocker if Owner ultimately needs the fallback.

### Acceptance criteria Codex sketched (for HUAKAI's Gemini Chrome mimicry to be Released)

HUAKAI-generated ClientHello to `cloudcode-pa.googleapis.com` must match
the §13 captured fingerprint on:
- JA3 hash = `59686f806cae30344b525e99af5b655d`
- Extension order = 18 IDs in §13-captured order
- ext 27 (compress_certificate) advertising matching algorithm bytes
- ext 17513 (ALPS) with matching ALPN string + h2 settings bytes
- ext 21 padding length = 192 bytes exactly
- ALPN = `["h2", "http/1.1"]`
- supported_groups = `[35466 GREASE, 29, 23, 24]`
- sigalgs = `[1027, 2052, 1025, 1283, 2053, 1281, 2054, 1537]`
- GREASE positions correct
- HTTP/2 SETTINGS frame also matches (if HUAKAI initiates h2 connection)

Mutation discriminator: removing ALPS / cert-compression / 192-byte
padding from the builder must turn the byte-level acceptance test red.
cargo tests pass. License / NOTICE / risk register updated if any new
dependency is added.

### Risks Codex flagged

- Deterministic 192-byte padding may require deeper BoringSSL patching.
- ALPS must match actual h2 SETTINGS frame bytes (which depend on h2
  client behavior).
- cert-compression advertised algorithms must match the captured bytes
  exactly.
- Vendored Boring patches raise maintenance cost.
- Gemini CLI 0.42.0 → 0.43+ can drift again; need re-capture cadence.

## Owner-approved next slice

**§14a-boring-capability-audit** (per Owner 2026-05-26 directive
"存档再调研"). Scope: dig the 3 unknowns end-to-end:
1. Bind `SSL_add_application_settings` from Rust via vendored boring-sys
2. Bind `SSL_CTX_add_cert_compression_alg` from Rust via vendored boring-sys
3. Probe how to force ext 21 padding to exactly 192 bytes (likely needs
   BoringSSL patch or custom extension write)
4. Byte-level prototype: HUAKAI emit one ClientHello to
   cloudcode-pa.googleapis.com, diff against §13 captures (capture must
   match JA3 hash + extension order + the 3 Chrome-specific extensions
   per acceptance criteria above).
5. Decision gate: if all 3 unknowns resolved cleanly → continue to
   §14b-gemini-impersonate-impl; if 192-byte padding can't be made
   byte-stable in boring → open §14c-rquest-fallback adding rquest as
   Gemini-only client.
