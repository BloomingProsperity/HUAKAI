# 2026-05-15 L2-A3 Capture Diff Normalizer

| Field | Plan |
|---|---|
| Owner directive | "L2-A3: capture diff normalizer (test-only)" |
| Scope | In: add a test-only capture diff helper under `crates/core_gateway/tests/common/`, wire it from `tests/common/mod.rs`, and add integration tests in `tests/mimicry_capture_diff_test.rs`. Out: no `src/` edits, no `Cargo.toml` edits, no reference-project reads. |
| Success criteria | `diff_capture_against_profile` emits a status for every expected field; KnownGapBlocked and unsupported profiles still return a completed diff with `profile_blocked=true`; ExactStable uses ordered comparison; SampleSetRandomized uses set comparison; `diff_has_mismatch` treats all mismatch/not-captured/not-in-template states as mismatches; `cd exploratory/rust-core-gateway/merged && cargo test` passes. |
| Time estimate | 20-35 minutes wall clock; one Codex implementation pass plus one test/fix pass. |
| Blast radius | Test-only Rust files. The risk is compile failure or incorrect helper semantics in tests, not production behavior. |
| Failure modes | Misreading profile field names: mitigate by reading `src/mimicry/tls_profile.rs` and `src/mimicry/profile.rs` only. Ambiguous absent-list handling: encode empty capture as `NotCaptured` for fields whose profile expects values. Set comparison hiding duplicates: use deterministic sorted/dedup sets for SampleSet status. |
| Decision points | None expected. High-risk paths (`src/`, `Cargo.toml`, schema, auth, billing, quota, deployment, secrets, `LICENSE`) are out of scope and require Owner confirmation if unexpectedly needed. |
| Pre-execution checklist | Confirm clean-room scope; inspect HUAKAI TLS capture/profile field definitions; add helper with Chinese comments; add focused tests for KnownGapBlocked, SampleSetRandomized, and ExactStable branches; run full Rust workspace tests from `exploratory/rust-core-gateway/merged`. |
| Concrete execution order | 1. Add `capture_diff.rs`. 2. Export it from `tests/common/mod.rs`. 3. Add `mimicry_capture_diff_test.rs`. 4. Run `cargo test`. 5. Fix test-only compile/logic issues if any. 6. Report changed files, line counts, and test summary. |
