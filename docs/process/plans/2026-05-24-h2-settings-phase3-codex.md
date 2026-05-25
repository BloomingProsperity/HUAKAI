# 2026-05-24 H2 SETTINGS Phase 3 Codex Plan

| Owner directive | "Rust TLS sidecar Phase 3 H2 SETTINGS frame 拼装" |
| Scope | In: `tls-sidecar` H2 SETTINGS frame serialization/parsing tests, profile placeholder validation, and ALPN-gated client preface + SETTINGS send after TLS handshake. Out: real Anthropic H2 capture, pseudo-header ordering, HPACK behavior, Go production transport integration, schema/database/auth/billing/quota changes, git add/commit/push. |
| Success criteria | `cargo build -p tls-sidecar` succeeds; `cargo test -p tls-sidecar -- --nocapture` succeeds; tests cover six standard SETTINGS IDs, wire frame round-trip parse of self-emitted bytes, profile placeholder remains empty with TODO, and connect layer sends H2 startup bytes only when negotiated ALPN is `h2`. |
| Time estimate | 1-2 hours wall clock; agent time mostly code reading, TDD, and build/test/review loops. |
| Blast radius | Medium: exploratory Rust sidecar only. Incorrect bytes can break HTTP/2 negotiation in sidecar experiments; no production Go path, database, auth, billing, quota, or secrets touched. |
| Failure modes | H2 frame payload order wrong; invalid value validation too weak; ALPN-gated send accidentally fires for HTTP/1.1; h2 library handshake and manual startup bytes conflict; tests pass without detecting omitted fields. Mitigation: discriminating fixtures compare all six IDs/values, parse full wire frame, and assert non-h2 path remains empty. |
| Decision points | No Owner decision needed for this slice if using existing `h2` dependency plus local minimal wire builder. Stop if implementation requires new runtime dependency, production IPC contract change, or complete request framing. |
| Pre-execution checklist | Read `docs/RULES.md`, `CLAUDE.md`, target Rust files, and allowed reference source; write failing tests before implementation; avoid adding files to frozen Go packages; do not stage/commit/push. |

## Clean-Room Lane Guard

LANE: specifier.

PRIOR LANES ON THIS ARTIFACT: none observed for this specific H2 SETTINGS Phase 3 patch.

REFERENCE PROJECTS IN SCOPE: `hyperium/h2` at `d361b75762868f51fb85e39e0a6c3c79958b42ea` (MIT) and `0x676e67/wreq` at `68c4a8868a64a79c43554d16e890b2f2a9f69a4d` (Apache-2.0 in local checkout, user described BSD). Both are permissive; implementation remains local and behavior-focused.

HARD PROHIBITIONS:
- Do not copy upstream source code, comments, identifiers, file structure, or tests into HUAKAI.
- Use reference reads only to confirm HTTP/2 behavior: 9-byte frame header, 6-byte setting entries, initial client preface before SETTINGS, ACK handling expectations, and configurable order.
- Cite source regions in reports; keep implementation names aligned with HUAKAI-local profile vocabulary.

## Reference Observations

- `hyperium/h2` encodes SETTINGS as a frame with type 4, stream 0, and 6-byte id/value entries; it accepts only ACK frames with empty payload and validates ENABLE_PUSH, INITIAL_WINDOW_SIZE, and MAX_FRAME_SIZE ranges. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:128`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:150`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:164`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:175`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:182`.
- `hyperium/h2` client startup sends the HTTP/2 client connection preface before queuing the initial SETTINGS frame; its handshake completion does not require waiting for the server's initial SETTINGS. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1161`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1292`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1322`.
- `wreq` exposes HTTP/2 profile options for settings order and uses ALPN negotiation to select HTTP/2 connection handling. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:227`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:241`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/client/layer/client.rs:529`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/client/layer/client.rs:552`.

## Execution Order

1. Add failing tests in `h2_settings.rs` for six-field wire serialization order and parse round-trip, including a mutation-style assertion that omitted or wrong IDs cannot pass.
2. Add failing tests in `connect.rs` for ALPN-gated H2 startup send using a local mock stream that captures writes.
3. Implement local minimal H2 frame builder/parser in `h2_settings.rs` without changing the existing h2 crate builder path.
4. Implement `send_profile_h2_startup` in `connect.rs` and call it after TLS handshake only when negotiated ALPN is exactly `h2`.
5. Keep built-in `anthropic-cli-mimicry-v1` `h2_settings` empty with an explicit TODO comment until Phase 3 real capture supplies values.
6. Run targeted tests, full `tls-sidecar` tests, build, then uncommitted review if the local Codex CLI supports it without staging.

## Package/File Structure Check

- Modify only existing Rust files under `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/`.
- No new files in frozen Go packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- No dependency addition planned because `h2 = "0.4"` is already present in `tls-sidecar/Cargo.toml`.

## Source Files Read

- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/h2_settings.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/profile.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/connect.rs`
- `exploratory/rust-core-gateway/merged/crates/tls-sidecar/src/boring_ctx.rs`
- `/home/codex/refs/h2/src/frame/settings.rs`
- `/home/codex/refs/h2/src/frame/head.rs`
- `/home/codex/refs/h2/src/client.rs`
- `/home/codex/refs/wreq/tests/emulate.rs`
- `/home/codex/refs/wreq/src/client/layer/client.rs`

Lane: specifier
Agent: Codex GPT-5
UTC timestamp: 2026-05-24T23:44:16Z
