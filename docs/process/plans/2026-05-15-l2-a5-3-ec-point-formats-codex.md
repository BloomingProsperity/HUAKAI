# 2026-05-15 L2-A5.3 EC point formats injection

| Owner directive | "L2-A5.3: OpenSSL EC point formats injection (feature-gated mimicry-openssl)" |
| Scope | In: HUAKAI `exploratory/rust-core-gateway/merged` OpenSSL mimicry adapter, feature-gated capture/preflight helper, dispatch fail-closed behavior when the feature is absent, fail-fast tests, RUNBOOK note, local tests. Out: ProxyEngine signature changes, non-allowed reference projects. |
| Success criteria | `mimicry-openssl` profile construction runs a production-runtime ClientHello preflight and records provenance; only `[0,1,2]` `ec_point_formats` is accepted; default tests remain green; requested feature-combo tests and `git diff --cached --check` are clean. |
| Time estimate | 20-35 minutes wall clock, one Codex pass. |
| Blast radius | Limited to Rust OpenSSL mimicry path, dispatch gating, and tests; feature-off builds now explicitly block `native-tls/openssl` profiles instead of treating an unavailable adapter as dispatchable. |
| Failure modes | rust-openssl lacks direct API, custom extension callback shape differs, OpenSSL rejects duplicate/unsupported extension, capture timing flakes, sync preflight could hang if capture/client ordering regresses. Mitigation: fail closed, accept only native `[0,1,2]`, use a bounded local capture timeout, verify with fail-fast and feature-combo tests. |
| Decision points | Stop only if implementation requires high-risk files, new runtime dependencies, schema/auth/billing/quota changes, or reading disallowed reference projects. |
| Pre-execution checklist | Confirm working tree state; inspect rust-openssl `mod.rs` for public EC point format API and custom extension callback shape; inspect current adapter/profile/diff tests; apply scoped patch; run requested test commands and whitespace check. |
| Concrete execution order | 1. Search rust-openssl for EC point format API. 2. Read local adapter/test helpers. 3. Add feature-gated helper and call order. 4. Add capture/diff test. 5. Run requested verification. 6. Summarize result in Chinese. |

Risk note: medium-risk small implementation change, feature-gated and not connected to production routing. Clean-room note: only rust-openssl public API surface is inspected; no non-allowed reference projects are read.

Review remediation note: the first implementation left a review-blocking production drift gap and an empty-list escape. The remediated implementation moves the OpenSSL-path capture/preflight helper into `src/` behind `mimicry-openssl`, runs preflight during `new_with_profile`, records `preflight_passed`, rejects every `ec_point_formats` value except `[0, 1, 2]`, blocks unbuildable or feature-unavailable OpenSSL profiles at dispatch, removes the resolved EC-format known-gap status, and documents the production image/runtime gate in RUNBOOK. Scope note: one broad local cargo-registry grep emitted unrelated crate matches; no disallowed reference project source was read or used.
