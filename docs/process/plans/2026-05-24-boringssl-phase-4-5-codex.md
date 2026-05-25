# 2026-05-24 BoringSSL R-3-A-fix-2..5 Status Review + Phase 4-5 Plan

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: cloudflare/boring vendored 5.1.0 / google BoringSSL upstream HEAD / router-for-me/CLIProxyAPI / Wei-Shaw/sub2api / 0x676e67/wreq / hyperium/h2 / envoyproxy/ai-gateway

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

> For implementation workers: do not execute this plan until Owner approves a synthesized Claude x Codex plan. This document is a plan lane and status review only.

## §0 Metadata And Start Gate

| Field | Value |
|---|---|
| Owner directive | `[OWNER AUTHORIZED 2026-05-24T11:25Z workspace-write — BoringSSL R-3-A-fix-2..5 现状审查 + Phase 4-5 plan lane]` |
| Artifact path | `docs/process/plans/2026-05-24-boringssl-phase-4-5-codex.md` |
| Lane | Codex specifier lane |
| Execution status | Plan only. No code implementation. No `git add`. No commit. No push. |
| Clean-room status | Behavior summary only. No reference source copied. Non-MIT sub2api was read only for behavior comparison and is cited as evidence, not implementation source. |
| Observed regions | 34 local/source regions read |
| Inferences | 11 marked as inference |
| Open questions | 14 |

**Goal:** independently review HUAKAI's BoringSSL fork backend status after R-3-A-fix-2 through R-3-A-fix-5-deeper, then plan Phase 4 ECH and Phase 5 PQ key share work.

**Architecture:** keep the Rust tls-sidecar as the only transport-mimicry execution layer for this path; keep Go backend integration outside frozen packages; keep BoringSSL fork changes narrow and documented in `MODIFICATIONS.md`.

**Tech Stack:** Rust workspace under `exploratory/rust-core-gateway/merged`, vendored cloudflare/boring 5.1.0 under `exploratory/rust-core-gateway/vendor/boring`, tokio-boring, hyperium/h2, Unix socket IPC.

**Scope In:**

- Review R-3-A-fix-2, fix-2-deeper, fix-3-deeper, fix-4-deeper, fix-5-deeper.
- Confirm Rust API surface exposed by vendored boring crate.
- Confirm tls-sidecar Phase 1-3 status from current source tree.
- Plan Phase 4 ECH using current boring `ech.rs` and `SslRef` methods.
- Plan Phase 5 PQ X25519MLKEM768 using current vendored BoringSSL and upstream path.
- Provide file-level scope and slice plan.
- Provide risk and test matrix with fail-closed/fallback strategies.
- Provide D decision table with reference-project citations.

**Scope Out:**

- No implementation.
- No schema migration.
- No runtime dependency addition without later Owner approval.
- No backend auth, billing, quota, or database core changes.
- No claim that current dirty tls-sidecar tests pass.
- No Claude plan read for this exact topic.

**Success Criteria For This Plan:**

- Every claimed R-3-A-fix status is anchored to `MODIFICATIONS.md` and current source lines.
- Every Phase 4/5 file target is listed and checked against frozen package rules.
- ECH and PQ have concrete test plans with mutation-style discriminators.
- D decisions include reference-project comparison citations.
- Open questions are explicit instead of padded into speculation.

**Blast Radius If Implemented Later:**

- TLS handshake bytes from HUAKAI sidecar.
- Provider reachability for Anthropic-like targets.
- Memory and CPU cost of PQ ClientHello key shares.
- Operational behavior when ECH DNS config is stale.
- Rust crate dependency graph if DNS resolver support is added.

**Failure Modes And Mitigations:**

- Profile says ECH required but config list is stale: fail closed by default; retry once only if BoringSSL returns replacement configs.
- Profile says PQ required but BoringSSL build rejects the group: fail closed for required profiles; allow audit-only classical fallback only under explicit profile flag.
- DNS HTTPS/SVCB fetch is implemented too early and adds unstable dependency surface: first ship inline ECH config support, then add DNS resolver as separate Owner-approved slice.
- Current sidecar fixture mismatch hides real status: repair Phase 1-3 tests before starting Phase 4.
- BoringSSL patch pile drifts from upstream: create R-3-A-fix-6 only if local wrapper or patch refresh is proven necessary.

**Pre-Execution Checklist For Future Workers:**

- Confirm synthesized plan exists and Owner approved it.
- Run `git status --short`; preserve unrelated user or other-lane changes.
- Confirm `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs` and `boring_ctx.rs` fixture state.
- Run tls-sidecar tests before editing Phase 4/5.
- Confirm no new files are added to frozen Go packages.
- Confirm any new Rust dependency gets Owner approval first.
- Update `MODIFICATIONS.md` for any vendor boring change.
- Run clean-room review before commit.
- Run per-commit `codex exec review --uncommitted --full-auto` after staging, if code is later changed.

---

## §1 R-3-A-fix-2..5 实施现状审查

### §1.1 Source Lock And Attribution Baseline

- Observed: vendored boring comes from cloudflare/boring crates.io package 5.1.0, VCS commit `3acc9820eb7117f0b36078bf119c81c5ea337e6a`; HUAKAI vendor date is 2026-05-17. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:3`.
- Observed: the first vendor pass recorded no upstream source modification in R-3-A-fix-1. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:6` and `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:11`.
- Observed: license notes are preserved for Apache-2.0 boring, MIT boring-sys, and BoringSSL license in the vendored C tree. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:22`.
- Observed: `.cargo_vcs_info.json` in both vendored Rust crates points at the same `3acc9820eb7117f0b36078bf119c81c5ea337e6a` SHA. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/.cargo_vcs_info.json:3` and `exploratory/rust-core-gateway/vendor/boring/boring-sys/.cargo_vcs_info.json:3`.
- Observed: workspace path dependency and crates.io patch redirect point tls-sidecar/core workspace at the vendored boring and boring-sys copies. Evidence: `exploratory/rust-core-gateway/merged/Cargo.toml:10` through `exploratory/rust-core-gateway/merged/Cargo.toml:19`.
- Inference: attribution state is currently acceptable for a local fork as long as every new BoringSSL C/Rust wrapper modification continues to update `MODIFICATIONS.md`; this follows from the existing attribution entries, not from a legal review.

### §1.2 R-3-A-fix-2: Extension Order Public API

| Dimension | Status |
|---|---|
| Implementation verdict | Implemented in vendored BoringSSL C layer and boring Rust wrapper. |
| API surface | Rust wrapper exposes `set_extension_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`. |
| C surface | Local exported C entry is present and documented as HUAKAI patch, not upstream. |
| Attribution | Recorded as HUAKAI codex executor lane, 2026-05-17 UTC. |
| Sidecar usage | Used in current `build_connector` when profile `extension_order` is non-empty. |
| Coverage | Wire-level sidecar test checks explicit order and extension type 22 after deeper fix. |

- Observed: R-3-A-fix-2 intended to expose ClientHello extension ordering as a narrow API. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:31`.
- Observed: implementation summary says C validates unknown and duplicate extension types and maps IANA IDs into BoringSSL internal extension table order. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:47`.
- Observed: original R-3-A-fix-2 still appended omitted internal extensions by default, with GREASE/padding/PSK kept on special paths. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:49` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:51`.
- Observed: Rust wrapper exists in `boring/src/ssl/mod.rs` at the recorded line. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1975`.
- Observed: C header declares the HUAKAI local extension-order API and marks it non-upstream. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:5234`.
- Observed: C implementation handles null input and clears custom order on empty input. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:3115`.
- Observed: tls-sidecar calls the wrapper only when `profile.extension_order` is present. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:58`.
- Risk: the Rust wrapper doc comment still says omitted extensions are appended. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1972`. That is stale after fix-2-deeper strict mode and should be corrected in a small docs-only vendor patch if Owner wants exact API docs.

### §1.3 R-3-A-fix-2-deeper: Strict Extension Order + Extension 22

| Dimension | Status |
|---|---|
| Implementation verdict | Implemented as deeper hardening over fix-2. |
| API surface | Same Rust extension-order wrapper, changed semantics when non-empty input is set. |
| Wire behavior | Strict mode writes only explicitly listed internal extensions, while GREASE/padding/PSK stay special. |
| ETM extension | Extension type 22 was added for strict-mode ClientHello output. |
| Attribution | Recorded as HUAKAI codex executor lane, 2026-05-17 UTC. |
| Sidecar usage | Current profile includes extension 22 and current test asserts it appears only through explicit order. |

- Observed: fix-2-deeper was triggered because the first ordering API still caused extra extension 65281 in a target profile. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:113` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:116`.
- Observed: extension 22 was added to the local BoringSSL extension table for validation and sorting. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:120` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:123`.
- Observed: strict mode under the C layer skips permutation and avoids filling omitted internal extensions. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:124`.
- Observed: strict mode is recorded as context/config state and is copied into new SSL objects. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:126`.
- Observed: current extension table includes encrypted ClientHello and local type 22 in sequence before renegotiation. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3558` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3587`.
- Observed: current permutation setup returns early when strict order is active. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3780`.
- Observed: local conversion skips GREASE, padding, and PSK while failing unknown and duplicate internal extensions. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3818` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3848`.
- Observed: ClientHelloInner and outer ClientHello loops both use the strict-order length when strict mode is active. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:3894` and `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:4008`.
- Observed: tls-sidecar test verifies profile extension order and asserts extension 22 is a discriminating fixture. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:298` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:318`.
- Coverage strength: good for wire extension order and extension 22 mutation.
- Coverage gap: no current sidecar test appears to assert duplicate/unknown extension errors from the setter. That should be added in a small hardening test slice before Phase 4.

### §1.4 R-3-A-fix-3-deeper: TLS 1.3 Cipher Order

| Dimension | Status |
|---|---|
| Implementation verdict | Implemented. |
| API surface | Rust wrapper exposes `set_tls13_cipher_order(&mut self, types: &[u16]) -> Result<(), ErrorStack>`. |
| C validation | Accepts only supported TLS 1.3 AEAD IDs and rejects duplicates. |
| Sidecar usage | Used when `profile.tls13_cipher_order` is non-empty. |
| Attribution | Recorded as HUAKAI codex executor lane, 2026-05-18 UTC. |
| Coverage | Wire-level sidecar test asserts TLS 1.3 cipher prefix follows profile and changes under mutation. |

- Observed: fix-3-deeper added a local C API for TLS 1.3 cipher order and a Rust wrapper. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:134` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:145`.
- Observed: current wrapper exists in `boring/src/ssl/mod.rs`. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1987`.
- Observed: C implementation rejects null non-empty input, unsupported TLS 1.3 cipher IDs, and duplicates. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:3134` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:3166`.
- Observed: tls-sidecar calls this wrapper before curves and raw ClientHello profile setup. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:21`.
- Observed: tls-sidecar test mutates TLS 1.3 cipher order and confirms wire cipher list changes. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:320` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:340`.
- Coverage strength: good for profile-to-wire order.
- Coverage gap: add direct negative tests for duplicate TLS 1.3 cipher values and unsupported IDs through `build_connector`.

### §1.5 R-3-A-fix-4-deeper: Raw ClientHello Profile Fields

| Dimension | Status |
|---|---|
| Implementation verdict | Implemented. |
| API surface | Rust wrapper exposes `set_client_hello_profile(&mut self, ciphers, groups, ec_points)`. |
| Controlled fields | Raw cipher advertisement, supported groups, EC point formats. |
| Sidecar usage | Used when `client_hello_profile` is non-empty. |
| Attribution | Recorded as HUAKAI codex executor lane, 2026-05-18 UTC. |
| Coverage | Wire-level sidecar test checks ciphers, groups, and EC point formats. |

- Observed: fix-4-deeper diagnosed remaining JA3 mismatch as TLS 1.2 cipher advertisement, supported groups, and EC point formats. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:150` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:155`.
- Observed: it added local C/Rust profile setter and explicitly states the setter affects wire advertisement while BoringSSL still validates the later handshake. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:157` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:163`.
- Observed: current wrapper is present in `boring/src/ssl/mod.rs`. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:1999`.
- Observed: current C header exposes the raw ClientHello profile API. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:5248` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:5253`.
- Observed: current C implementation stages ciphers, groups, and EC point formats before commit, dedupes ciphers, and validates EC point format values. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:3169` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc:3208`.
- Observed: EC point extension writer uses explicit EC point formats when set. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:1793` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc:1810`.
- Observed: tls-sidecar converts profile EC points to `u8` and calls the wrapper. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:31` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:39`.
- Observed: tls-sidecar wire-level test asserts ciphers, groups, and EC point formats match profile, then mutates EC point formats to prove the fixture is discriminating. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:342` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:360`.
- Coverage strength: good for raw field control at the sidecar level.
- Coverage gap: currently no observed test proves raw supported-groups mutation changes key-share content; that becomes mandatory before Phase 5.

### §1.6 R-3-A-fix-5-deeper: Profile Setter Hardening

| Dimension | Status |
|---|---|
| Implementation verdict | Implemented in C hardening summary; needs direct sidecar negative tests. |
| Hardening areas | Staged commit, cipher dedupe, EC point validation, strict GREASE group handling. |
| API surface | No new public Rust method beyond fix-4 wrapper. |
| Attribution | Recorded as HUAKAI codex executor lane, 2026-05-18 UTC. |
| Sidecar usage | Indirect through existing raw profile wrapper. |
| Coverage | Partially covered by positive wire tests; negative validation coverage still weak. |

- Observed: fix-5-deeper records staged commit, cipher dedupe, EC point validation, GREASE group handling, and local reason data. Evidence: `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:170` through `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:173`.
- Observed: tls-sidecar catches profile EC point values that cannot fit `u8` before calling the C wrapper. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:207` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:212`.
- Inference: C-level EC point validation and Rust-side `u8` conversion are complementary; Rust catches values above 255, C catches 3..255. This inference follows from the Rust conversion and C validation cited above.
- Required hardening tests before Phase 4:
- Add a sidecar unit test where `client_hello_profile.ec_points = [3]` fails through BoringSSL with a clear error.
- Add a sidecar unit test where duplicate ciphers are deduped only if the expected profile intentionally permits that; otherwise reject duplicates at profile parse to avoid silent mismatch.
- Add a sidecar unit test where unsupported TLS 1.3 cipher ID fails before any upstream TCP connect.
- Add a sidecar unit test where unknown extension ID in strict order fails before any upstream TCP connect.

### §1.7 Current Phase 1-3 tls-sidecar Usage Map

| Capability | Current status | Evidence |
|---|---|---|
| Unix socket sidecar main loop | Present | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs:13` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs:35` |
| Length-prefixed JSON control frame | Present | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/proto.rs:47` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/proto.rs:88` |
| Profile parser | Present, custom minimal TOML-ish parser | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:96` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:149` |
| Extension-order setter | Called | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:58` |
| TLS 1.3 cipher-order setter | Called | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:21` |
| Raw ClientHello setter | Called | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:31` |
| JA4 pre-connect validation | Present | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:59` |
| H2 profile settings | Present but connect path currently defaults to TLS copy path unless H2-specific entry point is used | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:67` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs:103` |
| ECH grease | Present when profile extension list contains type 65037 | `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:66` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:72` |
| Real ECH config | Not connected | No `ech.rs` module in tls-sidecar source list; boring wrapper method exists but sidecar does not call it. |
| PQ group/key-share | Not connected | Profile defaults use `X25519:P-256:P-384` and no PQ profile fields. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:22`. |

### §1.8 Dirty Worktree And Fixture Risk

- Observed: worktree is dirty in `profile.rs` and `boring_ctx.rs`, with additional untracked plan files. Evidence: local `git status --short` returned modified `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs` and `profile.rs`.
- Observed: current built-in profile has `expected_ja3` ending with EC point list `0-1`. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:25`.
- Observed: current test expects a different `expected_ja3` value ending with `0`. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:348` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:351`.
- Observed: current built-in `client_hello_profile.ciphers` excludes TLS 1.3 ciphers. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:31` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:34`.
- Observed: current test expects `client_hello_profile.ciphers` to include the TLS 1.3 ciphers and expects only EC point 0. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:358` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:363`.
- Inference: current dirty sidecar tests may be red or in mid-lane handoff. This must be resolved before Phase 4/5 work starts.
- Owner note: I did not run cargo tests in this plan lane, because the requested output was a written plan and another lane is actively wiring Phase 1-3.

---

## §2 Phase 4 ECH Implementation Plan

### §2.1 Current HUAKAI Vendor boring ECH Capability

| Capability | Current status | Evidence |
|---|---|---|
| Server-side ECH key builder | Present in boring `ech.rs`; mostly server/key registration side | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/ech.rs:9` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/ech.rs:63` |
| Client ECH config list setter | Present on `SslRef` | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3793` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3810` |
| Retry config accessor | Present on `SslRef` | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3812` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3832` |
| Public-name override accessor | Present on `SslRef` | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3834` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3854` |
| Accepted-state accessor | Present on `SslRef` | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3856` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3861` |
| ECH GREASE | Present on `SslRef`; sidecar already toggles it when extension 65037 is listed | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3863` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3870` and `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs:69` |
| Per-connection access | `ConnectConfiguration` derefs to mutable `SslRef`, so sidecar can call ECH setter before handing it to tokio-boring | `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs:156` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs:257` |

- Observed: boring's ECH client method documentation says rejection falls back to cleartext outer parameters, then clients should use retry configs and fail if retry also fails. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3793` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3799`.
- Inference: HUAKAI should not add a new C-layer API for Phase 4 unless we discover tokio-boring hides the `SslRef` state after handshake. Current `ConnectConfiguration` deref means the initial config list can be set without vendor changes.

### §2.2 Phase 4 File-Level Scope

| File | Action | Frozen package check | Responsibility |
|---|---|---|---|
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/ech.rs` | Create | Not frozen | Decode/resolve ECH config list, apply policy, classify retry/failure reason. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs` | Modify | Not frozen | Add ECH profile fields with backwards-compatible defaults. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs` | Modify | Not frozen | Apply ECH config to `ConnectConfiguration` before connect; capture ECH wire test ClientHello. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs` | Modify only if retry loop must live above TLS config | Not frozen | Enforce fail-closed vs audit-only fallback and single retry with returned configs. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs` | Modify | Not frozen | Add `mod ech;`. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/Cargo.toml` | Avoid in first slice | Not frozen | Only change if Owner approves DNS resolver dependency. |
| `exploratory/rust-core-gateway/vendor/boring/...` | Avoid in first slice | Not frozen but vendor risk | Only touch if BoringSSL ECH APIs are insufficient after tests. |

### §2.3 Proposed Profile Fields

Add a backwards-compatible nested block:

- `ech.mode`: enum-like string with values `off`, `grease_only`, `require`, `audit`.
- `ech.config_list_base64`: optional serialized ECHConfigList.
- `ech.public_name`: optional public name expected when BoringSSL exposes rejection override.
- `ech.retry_once`: bool default true for `require`, false for `audit`.
- `ech.source`: enum-like string `inline`, `dns_https`, `none`.
- `ech.max_age_seconds`: optional cache guard if DNS source is later implemented.

Behavior:

- If `mode = off`, no ECH setter is called.
- If `mode = grease_only`, sidecar uses only current GREASE behavior.
- If `mode = require`, sidecar must set non-empty `config_list_base64`; parse failure fails before TCP connect.
- If `mode = audit`, parse or handshake ECH rejection is surfaced in telemetry but the transport may continue only under explicit audit flag.
- If `source = dns_https`, Phase 4 first implementation should return a clear unsupported error unless the DNS resolver slice has been approved.

Rationale:

- The current parser already supports nested profile tables such as `client_hello_profile` and `h2_settings`. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:113` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:120`.
- The current profile model already treats new JA4/H2 fields as optional for backwards compatibility. Evidence: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:182` through `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:198`.
- The ECH block should follow the same defaulting pattern and include a backwards-compatibility test.

### §2.4 DNS HTTPS Record Fetch Versus Hardcode

Option A: inline/hardcoded serialized ECHConfigList.

- Lowest dependency risk.
- Works with captured provider profile fixtures.
- Best first slice for wire proof.
- Requires manual refresh when provider rotates configs.
- Can fail closed when config expires if `max_age_seconds` and `captured_at` are present.

Option B: DNS HTTPS/SVCB resolver in sidecar.

- More operationally correct.
- Requires a resolver implementation or new Rust dependency.
- New runtime dependency is high-risk under Owner rules.
- Needs caching, TTL handling, DNSSEC/non-DNSSEC stance, proxy interaction, and split-horizon behavior.
- Should be a separate Owner-approved slice after inline ECH is proven.

Option C: external config refresh job outside sidecar.

- Sidecar remains dependency-light.
- Ops or backend stores the latest config list.
- Requires future profile DB or config distribution path.
- Good fit for Phase 6 profile DB rather than Phase 4 core handshake.

Recommendation for Codex lane:

- Phase 4A: implement inline/hardcoded ECHConfigList support first.
- Phase 4B: add DNS HTTPS fetch only after Owner approves dependency and cache policy.
- Phase 6: move captured config into profile DB/dashboard if the product needs operator-managed refresh.

### §2.5 ECH Failure Strategy

Production default:

- If a profile says ECH is required, parse failure, missing config list, stale config, ECH rejection after retry, or certificate/public-name mismatch must fail closed.

Audit-only default:

- If a profile says ECH audit-only, sidecar may continue classical TLS after logging/returning an explicit policy reason.
- Audit-only must never be silent fallback.

Why:

- Boring's own client-facing API documentation points clients toward retry using returned configs and failure if retry also fails. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3812` through `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs:3817`.
- Latest CLIProxyAPI demonstrates host-gated transport selection and does not show target-host TLS handshake fallback inside that special path. Evidence: `router-for-me/CLIProxyAPI@50d19e204fed:internal/runtime/executor/helps/utls_client.go:143` through `router-for-me/CLIProxyAPI@50d19e204fed:internal/runtime/executor/helps/utls_client.go:149`.
- Latest sub2api has a profile fallback pattern for missing per-account TLS profile. Evidence: `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171` through `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:194`. HUAKAI can use that as an audit-only/manual profile fallback reference, but not as production silent fallback for required ECH.

### §2.6 Phase 4 Test Plan

Test group A: profile parsing.

- `ech` fields absent: loads as `mode = off`.
- `mode = require` with no config: parser succeeds but `connect_config` fails closed with clear error.
- invalid base64 config: fails before upstream TCP connect.
- `mode = audit` invalid config: returns explicit audit/fallback policy result only if connect layer asks for audit mode.

Test group B: BoringSSL setter plumbing.

- Valid fixture config list makes ClientHello contain ECH extension 65037 in non-GREASE mode.
- Mutation: remove `config_list_base64`; ECH extension must no longer be encrypted ECH, or connect must fail if required.
- Mutation: set `mode = grease_only`; wire must include GREASE behavior but no real config list.
- Assertion must compare good wire and damaged wire, not just `!= bad`.

Test group C: retry/failure.

- Fake server or mocked Boring error path returns retry configs; sidecar attempts exactly one retry.
- Retry config absent under required mode: fail closed.
- Retry config present but second attempt fails: fail closed.
- Audit mode records explicit `ech_rejected` reason and continues only if the profile says audit.

Test group D: stale config.

- `captured_at` older than `max_age_seconds`: required mode fails before TCP connect.
- Audit mode emits stale-config reason.
- No timestamp on required inline config is invalid unless Owner picks manual hardcode without expiry.

Test group E: wireshark/manual validation.

- Capture ClientHello to a controlled ECH-capable endpoint.
- Verify outer ClientHello carries ECH extension.
- Verify SNI exposure behavior matches the expected public name.
- Verify `ech_accepted()` after handshake for a successful endpoint.

---

## §3 Phase 5 PQ X25519MLKEM768 Plan

### §3.1 Current Vendor PQ Support

| Capability | Current status | Evidence |
|---|---|---|
| boring crate advertises PQ-related features | Present | `exploratory/rust-core-gateway/vendor/boring/boring/Cargo.toml:54` through `exploratory/rust-core-gateway/vendor/boring/boring/Cargo.toml:62` |
| boring crate docs mention X25519MLKEM768 | Present | `exploratory/rust-core-gateway/vendor/boring/boring/src/lib.rs:78` through `exploratory/rust-core-gateway/vendor/boring/boring/src/lib.rs:97` |
| BoringSSL group constant | Present | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2545` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2552` |
| group list APIs | Present | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2554` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2622` |
| key-share selection API | Present in C | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2629` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2667` |
| named group includes X25519MLKEM768 | Present | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:438` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:447` |
| key-share implementation can create X25519MLKEM768 | Present | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:465` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:480` |
| default vendor source group list includes MLKEM | Not in checked source copy | `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:456` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc:463` |
| build patch can add default PQ groups and second key-share behavior | Patch exists and build applies it unless configured otherwise | `exploratory/rust-core-gateway/vendor/boring/boring-sys/build/main.rs:467` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/build/main.rs:468`; `exploratory/rust-core-gateway/vendor/boring/boring-sys/patches/boring-pq.patch:356` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/patches/boring-pq.patch:364` |
| official BoringSSL HEAD still has X25519MLKEM768 support | Observed via official Gitiles web check | `https://boringssl.googlesource.com/boringssl/+/main/ssl/ssl_key_share.cc` lines 370-405 from the 2026-05-24 web check |

Important status:

- Current vendored source has MLKEM primitives and X25519MLKEM768 group/key-share support.
- Current source's default supported group list remains classical only before the Cloudflare PQ patch is applied.
- The boring-sys build script applies `boring-pq.patch` to the copied BoringSSL source during build unless a pre-patched/native source path is supplied.
- Current tls-sidecar does not request `X25519MLKEM768` in its profile.
- Current tls-sidecar raw profile setter overwrites the context supported group list after `set_curves_list`, so Phase 5 must update both the string group list and the raw `client_hello_profile.groups`.

### §3.2 Phase 5 File-Level Scope

| File | Action | Frozen package check | Responsibility |
|---|---|---|---|
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/pq.rs` | Create | Not frozen | PQ group constants, policy validation, wire key-share parser helpers for tests. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs` | Modify | Not frozen | Add PQ profile fields or extend existing curves/groups fields with required/fallback policy. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs` | Modify | Not frozen | Apply PQ policy and test key_share wire bytes. |
| `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs` | Modify | Not frozen | Add `mod pq;`. |
| `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs` | Only if needed | Vendor patch | Potential R-3-A-fix-6 wrapper for C key-share selection API. |
| `exploratory/rust-core-gateway/vendor/boring/boring-sys/...` | Only if needed | Vendor patch | Patch refresh if upstream BoringSSL changes MLKEM names/codepoints or if generated bindings do not expose needed C APIs. |

### §3.3 Proposed Profile Fields

Minimal Phase 5A:

- Use existing `curves` string with `X25519MLKEM768:X25519:P-256:P-384`.
- Include `0x11ec` in `supported_groups`.
- Include `0x11ec` in `client_hello_profile.groups`.
- Add `pq_required = true` or nested `pq.mode = require|audit|off`.

More explicit Phase 5B:

- `pq.mode`: `off`, `require`, `audit`.
- `pq.groups`: list of group IDs or names, default empty.
- `pq.expected_key_shares`: list of group IDs expected in ClientHello key_share.
- `pq.classical_fallback_groups`: optional list for audit fallback only.

Recommendation:

- Phase 5A should use existing fields first to avoid R-3-A-fix-6.
- Phase 5B should add explicit `pq` fields only if tests show the existing fields cannot express policy safely.

### §3.4 Key Share Control Strategy

Path 1: no vendor Rust wrapper, use existing group list behavior.

- Set supported group list to put `X25519MLKEM768` first and include `X25519`.
- Let BoringSSL choose first PQ and first classical key shares.
- Verify the ClientHello key_share extension contains the expected group IDs.
- Advantage: no new C/Rust API.
- Risk: exact key-share sequence may be affected by BoringSSL default selection rules and server hints.

Path 2: R-3-A-fix-6 wrapper for explicit client key shares.

- Add a Rust wrapper for the existing C API that configures exact client key-share group sequence on `SslRef`.
- Use it only when `pq.expected_key_shares` is present.
- Advantage: deterministic wire proof.
- Risk: another local vendor API surface and attribution burden.

Recommendation:

- Start with Path 1.
- Escalate to R-3-A-fix-6 only if Path 1 cannot produce deterministic wire bytes in tests.

### §3.5 PQ Fallback Strategy

Production required mode:

- If the BoringSSL build does not know `X25519MLKEM768`, fail before upstream connect.
- If the profile omits the PQ group while `pq.mode = require`, fail parse or build.
- If ClientHello key_share does not contain the expected PQ group, fail before real upstream connect.
- If server negotiates classical despite required PQ, classify as upstream unsupported and fail closed unless Owner explicitly chooses classical fallback.

Audit mode:

- Attempt PQ first.
- If local build rejects the group or server HRR/handshake path indicates unsupported PQ, log/return explicit `pq_unsupported` reason and optionally retry classical once.
- Retry must be visible in metrics; no silent downgrade.

Off mode:

- Preserve current Phase 1-3 classical behavior.

Reference alignment:

- BoringSSL's C API documentation says extra key shares can waste bandwidth, especially for PQ, so key shares should match likely server support. Evidence: `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2659` through `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h:2664`.
- Latest official BoringSSL HEAD retains X25519MLKEM768 key-share creation. Evidence: official web check of `ssl_key_share.cc` showed named group and creation cases on 2026-05-24.

### §3.6 Phase 5 Test Plan

Test group A: local build capability.

- Unit test `pq::x25519_mlkem768_group_id_is_supported_by_boring`.
- It should call group-name lookup indirectly through `set_curves_list("X25519MLKEM768:X25519")`.
- Mutation: use invalid group name; assert build fails before TCP connect.

Test group B: supported_groups and key_share wire.

- Capture ClientHello from a PQ profile.
- Parse supported_groups extension and assert `0x11ec` appears at the configured position.
- Parse key_share extension and assert `0x11ec` appears when required.
- Mutation: remove `0x11ec` from `client_hello_profile.groups`; required mode must fail before connect or wire assertion must turn red.

Test group C: raw profile setter interaction.

- Good profile includes `0x11ec` in `curves`, `supported_groups`, and `client_hello_profile.groups`.
- Damaged profile includes `0x11ec` only in `curves` but not raw groups; assert PQ group disappears or required mode fails.
- This specifically guards the current ordering where `set_client_hello_profile` writes supported groups after curves.

Test group D: fallback policy.

- Required mode with local unsupported group: fail closed.
- Audit mode with local unsupported group: explicit fallback reason.
- Off mode: no PQ group in wire.

Test group E: external capture.

- Use Wireshark or `tshark` to capture ClientHello.
- Display filter: TLS handshake ClientHello plus key_share extension.
- Assert group ID `0x11ec` appears in supported_groups and key_share.
- Compare against classical profile capture to prove the fixture is discriminating.

---

## §4 Consolidated File-Level Scope

### §4.1 Create

- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/ech.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/pq.rs`

Neither target is in a frozen package.

### §4.2 Modify

- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs`, only if retry/fallback must cross the connect boundary.

### §4.3 Conditional Vendor Patch Scope

R-3-A-fix-6 is only justified if:

- ECH setter cannot be applied safely through existing `ConnectConfiguration` deref.
- PQ requires exact client key-share ordering and current BoringSSL default selection cannot guarantee it.
- Current `boring-pq.patch` no longer applies against a refreshed BoringSSL base.
- Generated bindings do not expose needed C APIs.

If R-3-A-fix-6 happens:

- Update `MODIFICATIONS.md` immediately.
- Keep the API narrow.
- Add Rust wrapper tests or sidecar wire tests.
- Record Apache-2.0 attribution and local patch reason.
- Do not edit HUAKAI root `LICENSE`.

---

## §5 切片建议

### Slice 0: Stabilize Phase 1-3 Baseline

- Fix current profile/test fixture mismatch.
- Run `cargo test -p tls-sidecar` from `exploratory/rust-core-gateway/merged`.
- Add negative setter tests for extension unknown/duplicate, TLS 1.3 invalid/duplicate, and EC point invalid.
- This slice must finish before ECH/PQ implementation.

### Slice 4A: ECH Inline Config

- Add `ech.rs`.
- Add profile `ech` block.
- Add `connect_config` application through `ConnectConfiguration` deref.
- Add parser and pre-connect fail-closed tests.
- Add ClientHello capture test proving real ECH config changes wire behavior versus GREASE-only.

### Slice 4B: ECH Retry And Failure Policy

- Add required/audit fallback policy in connect layer.
- Add single retry using returned configs.
- Add stale config handling.
- Add operator-visible reason codes.

### Slice 4C: DNS HTTPS/SVCB Resolver, Owner Optional

- Only after Owner approves dependency or no-dependency resolver design.
- Add cache TTL.
- Add split-horizon/proxy policy.
- Add stale fail-closed test.

### Slice 5A: PQ Existing-Fields Path

- Add `pq.rs` constants and wire parser helpers.
- Add PQ profile fixture using existing `curves` and `client_hello_profile.groups`.
- Add supported_groups/key_share tests for `0x11ec`.
- No vendor patch unless tests prove necessary.

### Slice 5B: Explicit PQ Policy Fields

- Add `pq.mode`, `pq.expected_key_shares`, and fallback controls.
- Add required/audit/off tests.
- Add classical fallback retry only under explicit audit mode.

### Slice 5C: R-3-A-fix-6 Vendor Wrapper, Optional

- Expose exact client key-share setting only if Slice 5A/5B cannot guarantee deterministic wire behavior.
- Update vendor attribution and rerun all sidecar wire tests.

---

## §6 风险测试矩阵

| Risk | Phase | Test | Good fixture | Damaged fixture | Expected failure |
|---|---|---|---|---|---|
| ECH config missing under required mode | 4A | profile/build test | valid base64 config | empty config | fail before TCP connect |
| ECH config stale | 4B | policy test | fresh timestamp | expired timestamp | fail closed in require mode |
| ECH GREASE mistaken for real ECH | 4A | wire diff test | real config list | grease_only | ClientHello differs; accepted state only valid for real ECH |
| ECH retry loops forever | 4B | mocked retry test | one retry succeeds | retry repeatedly rejected | exactly one retry then failure |
| ECH silent downgrade | 4B | connect policy test | required mode | rejection + no audit | no classical fallback |
| PQ group unsupported locally | 5A | build profile test | `X25519MLKEM768` known | invalid group name | fail before TCP connect |
| PQ group in curves but not raw profile | 5A | wire parser test | group in all relevant fields | group omitted from raw groups | key_share missing or required-mode failure |
| PQ key_share absent | 5A | wire parser test | key_share includes `0x11ec` | classical-only profile | required mode fails |
| PQ fallback silent | 5B | policy test | audit mode with explicit fallback | required mode with same failure | audit emits reason; required fails |
| Vendor patch drift | 5C | build/test | patch applies | patch rejected | build fails loud; no partial fallback |
| Test fixture non-discriminating | 0/4/5 | mutation self-check | good != damaged | damaged same as good | test must fail and fixture must be redesigned |

---

## §7 D 决策点 With Reference Comparisons

| ID | Decision | Option | Consequence | Reference comparison | Codex recommendation |
|---|---|---|---|---|---|
| BS45-D1 | ECH failure policy | A: required mode fail-closed | Highest correctness for profiles that require ECH; outages are visible. | Boring client API says rejected ECH should use retry configs and report failure if retry also fails. Cite: `cloudflare/boring@3acc9820eb71:boring/src/ssl/mod.rs:3812`. CLIProxyAPI host-gates special TLS handling rather than silently using it for all hosts. Cite: `router-for-me/CLIProxyAPI@50d19e204fed:internal/runtime/executor/helps/utls_client.go:143`. | Recommended for production. |
| BS45-D1 | ECH failure policy | B: audit-only fallback | Useful during bring-up; must emit explicit reason. | sub2api has a TLS profile fallback pattern when enabled but no bound profile; use only as audit/manual analogy, not silent production behavior. Cite: `Wei-Shaw/sub2api@63b0631a5827:backend/internal/service/tls_fingerprint_profile_service.go:171`. | Allow only in Phase 4 test/audit mode. |
| BS45-D1 | ECH failure policy | C: silent classical fallback | Maximizes availability but corrupts fingerprint parity and hides stale configs. | No read reference supports silent target-host ECH downgrade as a production-safe behavior. Boring docs point to retry then failure, not silent downgrade. Cite: `cloudflare/boring@3acc9820eb71:boring/src/ssl/mod.rs:3797`. | Reject. |
| BS45-D2 | ECH config source | A: inline/hardcoded config first | No new dependency; enough for first wire proof; manual expiry required. | wreq test fixture models TLS options including ECH extension as part of an emulation profile. Cite: `0x676e67/wreq@68c4a8868a64:tests/emulate.rs:200`. | Recommended first. |
| BS45-D2 | ECH config source | B: DNS HTTPS/SVCB in sidecar now | More correct but adds resolver/cache/TTL dependency risk. | Envoy AI Gateway keeps filter configuration decoupled from implementation and k8s so it can be iterated independently. Cite: `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:6`. That supports separating config refresh from transport core. | Defer until Owner approves dependency/cache design. |
| BS45-D2 | ECH config source | C: external refresh job later | Keeps sidecar narrow; fits profile DB/dashboard later. | Envoy comparison above supports config-plane separation. Cite: `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:23`. | Good Phase 6 path. |
| BS45-D3 | PQ implementation path | A: follow BoringSSL upstream + existing group list first | Minimal HUAKAI vendor patch; prove with wire parser. | Vendored BoringSSL already has X25519MLKEM768 group and key-share creation. Cite: `cloudflare/boring@3acc9820eb71:boring-sys/deps/boringssl/ssl/ssl_key_share.cc:438`. Official BoringSSL HEAD still exposes the same family in web check. | Recommended first. |
| BS45-D3 | PQ implementation path | B: R-3-A-fix-6 explicit key-share wrapper | More deterministic but increases local patch surface. | BoringSSL C has a key-share selection API; current boring Rust wrapper search did not show a Rust method. Cite: `cloudflare/boring@3acc9820eb71:boring-sys/deps/boringssl/include/openssl/ssl.h:2629`. | Conditional if existing fields cannot lock key_share. |
| BS45-D3 | PQ implementation path | C: self-maintain PQ crypto | Highest clean-room and crypto risk; unnecessary when BoringSSL already has the primitive path. | BoringSSL has the group and key-share implementation. Cite: `cloudflare/boring@3acc9820eb71:boring-sys/deps/boringssl/ssl/ssl_key_share.cc:465`. | Reject. |
| BS45-D4 | Phase 4-5 timeline | A: Phase 4 after Phase 1-3 baseline fixed, then Phase 5 | Reduces compounding fixture drift; ECH and PQ each get wire proof. | Current sidecar profile fixture mismatch is observed in HUAKAI source. Cite: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:25` and `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs:350`. | Recommended. |
| BS45-D4 | Phase 4-5 timeline | B: Parallel ECH and PQ | Faster but risks both features depending on red baseline tests. | hyperium/h2 exposes configurable client builder behavior, but HUAKAI current baseline already has pending fixture risk; cite for the need to keep transport knobs testable: `hyperium/h2@d361b7576286:src/client.rs:655`. | Only if separate workers and disjoint files are enforced. |
| BS45-D4 | Phase 4-5 timeline | C: PQ before ECH | PQ may be simpler, but ECH is already surfaced in boring wrapper and in current extension order table. | Boring wrapper has ECH client setter now. Cite: `cloudflare/boring@3acc9820eb71:boring/src/ssl/mod.rs:3793`. | Acceptable only if Owner prioritizes PQ capture. |

---

## §8 Verification Plan

### §8.1 Commands Before Any Future Implementation Claim

- `cd exploratory/rust-core-gateway/merged && cargo test -p tls-sidecar`
- `cd exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-boring --lib`, if core_gateway still depends on the same workspace state.
- `cd exploratory/rust-core-gateway/merged && cargo build -p tls-sidecar`
- `git diff -- exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md` after any vendor patch.

### §8.2 ECH Capture

- Use controlled ECH-capable endpoint or local BoringSSL test server.
- Capture with `tcpdump`/Wireshark.
- Verify ClientHello has ECH extension type 65037.
- Verify outer ClientHello public name matches profile.
- Verify `ech_accepted()` after handshake where endpoint supports ECH.
- Negative capture: grease-only profile must not be treated as accepted ECH.

### §8.3 PQ Capture

- Capture ClientHello for PQ profile and classical profile.
- Parse supported_groups; assert `0x11ec` appears only in PQ profile.
- Parse key_share; assert `0x11ec` appears in PQ required profile.
- Compare payload size increase against expected PQ key-share cost.
- Confirm fallback/audit metrics if server does not negotiate PQ.

### §8.4 Clean-Room And Review

- Do not copy reference implementation code into HUAKAI.
- Cite behavior evidence only.
- Run `codex exec review --uncommitted --full-auto` after staging future code.
- Escalate to full reviewer-lane if R-3-A-fix-6 touches vendor C or generated bindings.

---

## §9 Source Files

HUAKAI files read:

- `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md`
- `docs/process/plans/2026-05-24-boringssl-fork-backend-synthesis.md`
- `docs/process/plans/2026-05-24-decisions-locked.md`
- `docs/process/2026-05-24-ref-anchor.md`
- `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs`
- `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs`
- `exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/ech.rs`
- `exploratory/rust-core-gateway/vendor/boring/boring/src/lib.rs`
- `exploratory/rust-core-gateway/vendor/boring/boring/Cargo.toml`
- `exploratory/rust-core-gateway/vendor/boring/boring/.cargo_vcs_info.json`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/.cargo_vcs_info.json`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/build/main.rs`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/patches/boring-pq.patch`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/internal.h`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/handshake_client.cc`
- `exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc`
- `exploratory/rust-core-gateway/merged/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/proto.rs`

Reference files read:

- `/home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/runtime/executor/helps/utls_client.go`
- `/home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/tls_fingerprint_profile_service.go`
- `/home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/gateway_forward_as_chat_completions.go`
- `/home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/gateway_forward_as_responses.go`
- `/home/codex/refs-latest/envoy-ai-gateway-extracted/ai-gateway-main/internal/filterapi/filterconfig.go`
- `/home/codex/refs/wreq/tests/emulate.rs`
- `/home/codex/refs/h2/src/client.rs`
- `/home/codex/refs/h2/src/proto/settings.rs`

Official web source checked:

- `https://boringssl.googlesource.com/boringssl/+/HEAD/include/openssl/ssl.h`
- `https://boringssl.googlesource.com/boringssl/+/main/ssl/ssl_key_share.cc`

---

## §10 Lane + UTC

- Lane: specifier
- Agent: Codex (GPT-5)
- UTC timestamp: 2026-05-24T11:34:54Z
- No git add/commit/push performed.

Source files read: exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md; docs/process/plans/2026-05-24-boringssl-fork-backend-synthesis.md; docs/process/plans/2026-05-24-decisions-locked.md; docs/process/2026-05-24-ref-anchor.md; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/mod.rs; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/connector.rs; exploratory/rust-core-gateway/vendor/boring/boring/src/ssl/ech.rs; exploratory/rust-core-gateway/vendor/boring/boring/src/lib.rs; exploratory/rust-core-gateway/vendor/boring/boring/Cargo.toml; exploratory/rust-core-gateway/vendor/boring/boring/.cargo_vcs_info.json; exploratory/rust-core-gateway/vendor/boring/boring-sys/.cargo_vcs_info.json; exploratory/rust-core-gateway/vendor/boring/boring-sys/build/main.rs; exploratory/rust-core-gateway/vendor/boring/boring-sys/patches/boring-pq.patch; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/include/openssl/ssl.h; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_lib.cc; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/extensions.cc; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/internal.h; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/handshake_client.cc; exploratory/rust-core-gateway/vendor/boring/boring-sys/deps/boringssl/ssl/ssl_key_share.cc; exploratory/rust-core-gateway/merged/Cargo.toml; exploratory/rust-core-gateway/merged/crates/tls-sidecar/Cargo.toml; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/main.rs; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs; exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/proto.rs; /home/codex/refs-latest/cliproxyapi-extracted/CLIProxyAPI-main/internal/runtime/executor/helps/utls_client.go; /home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/tls_fingerprint_profile_service.go; /home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/gateway_forward_as_chat_completions.go; /home/codex/refs-latest/sub2api-extracted/sub2api-main/backend/internal/service/gateway_forward_as_responses.go; /home/codex/refs-latest/envoy-ai-gateway-extracted/ai-gateway-main/internal/filterapi/filterconfig.go; /home/codex/refs/wreq/tests/emulate.rs; /home/codex/refs/h2/src/client.rs; /home/codex/refs/h2/src/proto/settings.rs; https://boringssl.googlesource.com/boringssl/+/HEAD/include/openssl/ssl.h; https://boringssl.googlesource.com/boringssl/+/main/ssl/ssl_key_share.cc
Lane: specifier
Agent: Codex (GPT-5)
UTC timestamp: 2026-05-24T11:34:54Z

Owner 中文摘要: 本文基于已读源码确认 R-3-A-fix-2..5 的主要 C 层和 Rust wrapper 已存在, tls-sidecar 当前已调用 extension order / TLS1.3 cipher order / raw ClientHello 三组 setter,但当前工作树里的 profile fixture 与测试断言有不一致风险,所以 Phase 4/5 前应先稳定 Phase 1-3 baseline。Phase 4 建议先做 inline ECHConfigList + required fail-closed, DNS HTTPS/SVCB 抓取延后单独拍依赖和缓存策略。Phase 5 建议先用现有 BoringSSL X25519MLKEM768 group/key-share 能力和现有 profile 字段做 wire proof,只有无法锁定 key_share 时才做 R-3-A-fix-6。本文没有功能缩水,没有复制参考项目实现,但读了 LGPL sub2api 行为作 fallback 对照,需继续保持 clean-room;安全风险集中在 silent downgrade、过期 ECH config、PQ 不支持时误降级,都已列 fail-closed/audit-only 策略。Owner 需要确认 ECH 失败策略、DNS 抓取是否进入 Phase 4、PQ 是否允许 R-3-A-fix-6、以及 Phase 4/5 时间顺序。
