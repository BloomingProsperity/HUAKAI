# 2026-06-02 sidecar sigalgs codex plan

| Field | Value |
| --- | --- |
| Owner directive | "HUAKAI Phase2 S1a — Rust tls-sidecar 签名算法指纹保真(R-SIDECAR-001)。IMPLEMENTER lane;本 spec 的伪装目标由 Claude(PM)提供,codex 只写中性 Rust/FFI 绑定代码。" |
| Scope | In: `exploratory/rust-core-gateway/merged/crates/tls-sidecar`, and if needed narrow Rust FFI binding files under `exploratory/rust-core-gateway/vendor/boring`. Out: main backend frozen Go packages, auth, billing, quota, database schema, deployment, secrets, `LICENSE`, landing branch. |
| Success criteria | Captured sidecar ClientHello extension 13 has the same `u16` sequence as `TlsProfile.signature_algorithms`: 26 values, same order, same bytes. The test must fail if code falls back to the old 10-name string list. |
| Time estimate | 1-2 hours wall clock; one Codex implementer pass plus at most two self-review rounds. |
| Blast radius | TLS sidecar outbound ClientHello construction and local vendored Boring Rust binding. No production backend behavior outside the exploratory Rust gateway should change. |
| Failure modes | Missing raw setter binding; mitigation: expose a narrow safe wrapper for existing BoringSSL `SSL_CTX_set_signing_algorithm_prefs`/equivalent. Raw setter accepts wrong type or length; mitigation: wire-capture test parses extension 13 bytes. Vendor patch not documented; mitigation: update `vendor/boring/MODIFICATIONS.md`. |
| Decision points | Stop for Owner only if implementation requires changing upstream C/C++ BoringSSL behavior beyond a narrow exported binding, adding a runtime dependency, or touching high-risk project areas. |
| Pre-execution checklist | Confirm linked worktree and branch; inspect current profile and string setter; inspect vendored BoringSSL APIs; write RED wire test; implement minimal raw setter; run fmt/build/test/clippy; verify mutation red; run Codex self-review; commit and push `origin HEAD:work/sidecar-sigalgs`. |

## File Scope Check

- Modify `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs` for the discriminating wire test and call site.
- Modify `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs` if the raw numeric setter is not already exposed in the Rust wrapper.
- Modify generated or manual binding surface under `exploratory/rust-core-gateway/vendor/boring/boring-sys` only if the symbol is unavailable to Rust.
- Modify `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md` if any vendored boring file changes.
- Do not add files to frozen backend packages listed in `AGENTS.md`.

## Concrete Execution Order

1. Verify the existing wire test helper can parse ClientHello extensions and extend it to parse extension type 13 as a raw `Vec<u16>`.
2. Add `boring_signature_algorithms_profile_controls_wire_bytes` in `tls-sidecar` tests. It must compare captured wire extension 13 with `profile.signature_algorithms` and then damage the profile to prove the assertion is discriminating.
3. Run the new test before production changes. Expected RED: wire sequence is the old 10-name string output, not the 26-value profile sequence.
4. Add a Rust wrapper that passes a raw `&[u16]` into BoringSSL's existing signing and verification algorithm preference path. Prefer an existing exported FFI symbol; expose only the narrow missing binding if needed.
5. Change `build_connector` to prefer `profile.signature_algorithms` raw IDs and only fall back to `profile.sigalgs` when the numeric list is empty.
6. Run the new test and full `tls-sidecar` tests.
7. Run `cargo fmt --check`, `cargo build`, `cargo test -p tls-sidecar`, and `cargo clippy -p tls-sidecar --all-targets -- -D warnings`.
8. Mutation check: temporarily force the old string setter path, run the wire test, confirm it fails, then restore the raw setter implementation.
9. Run `codex exec review --uncommitted -m gpt-5.5 -c xhigh < /dev/null`; if the CLI rejects the exact flags, run the closest available self-review and record the mismatch.
10. Fix any S0/S1 findings, then commit and push to `origin HEAD:work/sidecar-sigalgs`.

## Assumptions

- The 26 `signature_algorithms` values in `profile.rs` are HUAKAI true-capture target data supplied by PM and are the authoritative neutral input for this lane.
- The existing vendored BoringSSL source is already an approved local vendored dependency; this task should expose or call existing preference APIs rather than copying reference-project implementation.

## Clean-room Note

No non-MIT reference project source is used. BoringSSL is a vendored dependency already present in this tree; any local change must be documented in `MODIFICATIONS.md` with file and reason.
