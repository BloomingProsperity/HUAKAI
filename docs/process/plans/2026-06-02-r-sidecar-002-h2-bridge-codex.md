# 2026-06-02 R-SIDECAR-002 H2 bridge Codex plan

| Owner directive | "HUAKAI Phase2 S1b — Rust tls-sidecar H2 出站桥 (R-SIDECAR-002)。IMPLEMENTER lane;本 spec 的伪装目标由 Claude(PM)提供,codex 只写中性 Rust 传输层代码,不做 provider 语义翻译。" |
| Scope | In: `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs`, new `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/h2_bridge.rs`, module registration in `main.rs`, direct public-API dependency only if Rust requires it for `h2` request/response types, focused tests inside tls-sidecar. Out: Go changes, IPC control-frame contract changes, provider semantic translation, vendor/boring changes, auth/billing/quota/schema/LICENSE, landing branch. |
| Success criteria | When ALPN is `h2`, Rust sends profile-driven H2 client preface/SETTINGS/WINDOW_UPDATE via existing `start_profile_h2_connection`, translates exactly one HTTP/1.1 request from IPC to upstream H2, and serializes one upstream H2 response back as HTTP/1.1. When ALPN is not `h2`, existing raw tunnel behavior remains. `cargo fmt --check`, `cargo build -p tls-sidecar`, `cargo test -p tls-sidecar`, `cargo clippy -p tls-sidecar --all-targets -- -D warnings`, mutation checks, Codex review, commit, and push complete. |
| Time estimate | 1 focused work session: plan/read/TDD, implementation, mutation checks, full verification, review, commit, push. |
| Blast radius | Medium. The change affects the tls-sidecar post-TLS byte path for H2-negotiated upstreams. A bug can fail real requests, silently weaken transport fingerprinting, or deadlock the IPC pipe. The raw HTTP/1.1 ALPN path should remain isolated. |
| Failure modes | Parser accepts unsupported multi-request/streaming and hides Stage-2 gaps; H2 driver is not spawned and requests hang; response serialization omits body or required headers; tests only prove code runs but do not fail under raw-tunnel mutation; new dependency is broader than required. Mitigation: write discriminating tests first, keep bridge single-request fail-loud, use existing `h2` engine for frames, avoid Go/proto changes, and record Stage-2 boundaries in code comments and RESULT. |
| Decision points | Stop and return `NEEDS_PM` if implementation requires changing Go<->Rust control frames, modifying Go transport behavior, adding a broad parser/runtime dependency beyond direct `http` API access, or changing vendor/boring. No reference-project comparison is included because this is an implementer-lane neutral Rust transport task and no reference-project behavior claim is needed or read. |
| Pre-execution checklist | 1. Confirm worktree branch is `work/sidecar-h2-bridge`. 2. Read `docs/RULES.md`, `AGENTS.md`, `.coordination/README.md`. 3. Read Go `backend/internal/transport/mimicry/sidecar_client.go` only to verify HTTP/1.1 over pipe. 4. Read Rust `connect.rs`, `h2_settings.rs`, `profile.rs`, `proto.rs`, `main.rs`, `Cargo.toml`. 5. Claim coordination locks. 6. Write failing tests before production code. 7. Do not read non-MIT reference source. |

## Execution order

1. Add failing bridge tests in tls-sidecar:
   - H2 fingerprint test captures upstream bytes and asserts preface, first SETTINGS payload order and values, and connection WINDOW_UPDATE.
   - End-to-end GET and POST tests feed HTTP/1.1 over IPC and assert HTTP/1.1 responses from a stub H2 upstream.
   - Raw tunnel regression verifies non-H2 ALPN still returns the original IO without H2 startup bytes.
2. Run targeted tests and record RED failures.
3. Implement `h2_bridge.rs` with:
   - single HTTP/1.1 request parser for request line, headers, and fixed `Content-Length` body;
   - fail-loud rejection for chunked bodies, trailers, extra pipelined bytes, and unsupported malformed input;
   - H2 request send/response receive using existing `h2` connection handle;
   - HTTP/1.1 response serializer.
4. Update `connect.rs` to branch on `selected_alpn == b"h2"`, run `start_profile_h2_connection`, spawn the H2 driver task, then call the Stage-1 bridge after the existing OK ack. Non-H2 stays raw tunnel.
5. Register `mod h2_bridge;` and add only the direct `http` dependency if required by Rust compilation for `h2` public API types.
6. Run targeted tests until green, then full `cargo fmt --check`, `cargo build -p tls-sidecar`, `cargo test -p tls-sidecar`, and `cargo clippy -p tls-sidecar --all-targets -- -D warnings`.
7. Perform mutation checks:
   - temporarily raw-tunnel the H2 path or skip the H2 bridge and confirm the fingerprint test fails;
   - temporarily damage response bridging and confirm end-to-end tests fail;
   - restore intended code and rerun verification.
8. Stage intended diff, run `codex exec review --uncommitted --full-auto --sandbox read-only`, normalize findings, fix S0/S1 within the two-round budget.
9. Commit with rules/review notes and push `HEAD:work/sidecar-h2-bridge`.

## Assumptions and risks

- Observed: Go sidecar client sets `ForceAttemptHTTP2: false` and returns the sidecar Unix connection from `DialTLSContext`, so Go writes HTTP/1.1 bytes over the pipe after Rust ACK.
- Observed: existing Rust `h2_settings::client_handshake` drives profile settings through the `h2` crate and existing tests already inspect H2 startup frames.
- Inference: direct `http = "1"` may be required because `h2::client::SendRequest` consumes `http::Request`; this is a narrow public-API dependency already present transitively in the workspace lockfile, not a new parser/runtime engine.
- Clean-room: no reference-project source will be read; implementation is derived from PM-provided spec and HUAKAI-internal code only.
