# 2026-05-17 rust R-2-B-4 Boring wire test - Codex

| Owner directive | "Rust Wave R-2-B-4: un-ignore R-1 deferred test + byte-level JA3 wire match" |
|---|---|
| Scope | Add HUAKAI-owned Rust test fixture for raw ClientHello capture; add BoringSSL wire-match test; update old OpenSSL deferred-test ignore reason; wire test-only module. Out of scope: R-2-B-2/3 implementation changes, frontend, Go backend, LICENSE, control plane, billing, auth, quota, database schema, new dependencies. |
| Success criteria | `cargo test -p core_gateway --features mimicry-boring --lib mimicry::boring_wire -- --nocapture` runs the new test and validates extension order, JA3 hash, and SNI from captured TCP bytes. Existing OpenSSL deferred test remains ignored with superseded rationale. |
| Time estimate | 1-1.5 hours active Codex time; local cargo build may dominate wall time because `boring-sys` can compile native code. |
| Blast radius | Test-only module plus one existing test file and mimicry module registration. Failure should affect only `mimicry-boring` test builds. |
| Failure modes | Parser offset bug could create false failure; mitigate with bounded cursor reads and explicit length checks. Boring public API may not emit exact profile wire order; test must fail honestly rather than weakening assertions. Sandbox may block loopback or native build; record command output and residual risk. |
| Decision points | Owner confirmation needed only if exact Boring wire assertion fails and fixing requires modifying R-2-B-2/3 code, adding dependencies, or changing runtime TLS behavior. |
| Pre-execution checklist | Read Claude plan §4; read `anthropic_test.rs`; read `ja3_wire.rs`; read `client_hello_builder.rs`; read `profile.rs`; read Anthropic profile JSON; confirm worktree status; write fixture; write test; update ignore reason; run targeted cargo test. |
| Concrete execution order | 1. Add `wire_capture_fixture.rs` with raw TCP capture and minimal TLS ClientHello parser. 2. Add `boring_wire` test module. 3. Register test-only fixture and test module in `mimicry/mod.rs`. 4. Update OpenSSL deferred ignore rationale. 5. Run targeted test and inspect failures honestly. |

## Execution Note

- The R-2-B-4 test implementation was added and compiled under `mimicry-boring`.
- Current sandbox denies loopback bind, so the test uses the TCP listener when available and falls back to an in-memory Tokio stream only for `PermissionDenied`; both paths capture TLS record payload bytes before any server response.
- Targeted test currently fails honestly: observed extension order is `[0, 23, 65281, 10, 11, 35, 16, 13, 51, 45, 43, 21]`, while the Anthropic profile requires `[0, 65037, 23, 65281, 10, 11, 35, 16, 5, 13, 18, 51, 45, 43, 21]`.
- Per this lane's scope, `client_hello_builder.rs` and `ja3_wire.rs` were not changed. Owner confirmation is needed before expanding scope to fix the Boring connector implementation.
