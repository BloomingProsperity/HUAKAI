# 2026-05-25 W11-F F-1 L2 HTTP/2 Fork ProxyEngine Wiring Plan - Codex

| Field | Value |
|---|---|
| Owner directive | "指纹 F-1: L2 HTTP/2 fork -> ProxyEngine 真接线 (最重最险)" |
| Plan lane | Codex independent draft for plan-trio; this file does not depend on any other draft. |
| Scope guard | Do not add files under frozen Go packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`; F-1 work stays in Rust `core_gateway` and docs/tests. |
| Clean-room / license guard | The `http2` git dependency is pinned in HUAKAI `Cargo.toml` with `features = ["unstable"]` and rev `a33b27e469434a99105f35670c9970f22112e892` (`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:16-17`, `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:32`). The checked fork declares MIT in its manifest and license text (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:Cargo.toml:6-8`, `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:LICENSE:3-15`). |
| Truth-first note | Current built-in profiles mostly do not yet carry real HTTP/2 frame data; for example Anthropic records `h2_settings_frame.available=false` and pseudo-header capture unavailable (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json:113-127`). F-1 cannot be called Released until a real upstream HTTP/2 fixture exists and the new tests compare against it. |

## 1. Goal

F-1 upgrades the feature-gated HTTP/2 fork adapter from local encoder capture into a real ProxyEngine outbound path: `mimicry-boring + mimicry-http2-fork` builds must use the pinned MIT `http2` fork for outbound HTTP/2 request encoding, must preserve profile-defined SETTINGS order/value and pseudo-header order on the actual wire, and must fail closed through an L2 preflight gate when profile bytes or runtime fork output do not match the real upstream fixture. The current adapter explicitly says it only exposes feature-gated local byte capture and is not wired into ProxyEngine (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:1-3`); current `ProxyEngine` still stores a hyper-util client and calls `self.client.request(...)` (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:97-103`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:345-348`).

## 2. Scope In / Out

**In scope**

- True outbound wiring: introduce a narrow transport boundary so ProxyEngine can route through either existing hyper-util or the new HTTP/2 fork client without changing handler/business logic; current construction is tied to `GatewayHttpClient = Client<GatewayHttpConnector, Body>` and a hyper connector builder (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:22-27`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:157-166`).
- L2 preflight: add a typed HTTP/2 preflight that checks required profile fields, captures real fork bytes over loopback TCP, compares SETTINGS order/values and pseudo-header order to profile/fixture, then maps failure into the same fail-closed production surface used by the L1 builder gate (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:78-103`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:172-180`).
- Byte-level tests: add L2 equivalents of the existing Boring ClientHello tests, but for HTTP/2 SETTINGS and HEADERS. The L1 pattern already runs per-profile byte tests and prints diagnostics on mismatch (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs:14-38`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs:72-99`); current HTTP/2 tests only cover in-memory duplex capture (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:25-77`).
- Request/response streaming compatibility: preserve the existing relay path and observability behavior by boxing or adapting response bodies into the existing generic `relay_body` shape; the relay already accepts a generic body internally, but the public helper currently fixes the upstream response to `hyper::body::Incoming` (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:54-61`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:120-133`).

**Out of scope**

- No changes to auth core, quota enforcement, billing ledger, database schema, deployment scripts, or `LICENSE`.
- No new files in frozen Go packages. F-1 creates or edits Rust files under `exploratory/rust-core-gateway/merged/crates/core_gateway/src/{mimicry,proxy_engine}` and tests/docs only.
- No feature shrinkage: if a real upstream HTTP/2 fixture is missing for a profile, that profile becomes `KnownGap` / `Mandatory Roadmap` for F-1 release status rather than silently falling back to default hyper HTTP/2. Current profile validation already treats unavailable H2 capture as a limitation that must be recorded (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:377-403`).
- No connection pooling in the first released F-1 cut unless Owner explicitly chooses it. Existing hyper path pools (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:161-165`), but per-request HTTP/2 fork handshakes are simpler to prove byte-level correctness because the fork emits the client preface and initial SETTINGS during handshake (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1383-1422`).

## 3. Sequence of Sub-Phases

a. Evidence and fixture contract: define the real HTTP/2 fingerprint fixture shape and block Released status if only synthetic data exists.

b. Adapter true-IO extraction: split the current `duplex`-only capture into reusable "drive over any AsyncRead/AsyncWrite" logic and add a loopback TCP capture test.

c. L2 preflight module: add typed statuses/errors and profile-vs-runtime byte comparison, gated behind `mimicry-http2-fork`.

d. ProxyEngine transport boundary: wrap the existing hyper client behind a response-body-compatible transport enum without changing behavior.

e. HTTP/2 fork outbound client: implement the real outbound HTTP/2 path over the shared Boring/plain connector, including request body pump and response body adapter.

f. Builder/dispatch wiring: when both `mimicry-boring` and `mimicry-http2-fork` are enabled, run L1 then L2 preflight and return an HTTP/2 fork transport only on pass; map failures to structured `Block*` actions.

g. Profile backfill, byte tests, and release evidence: update profiles only from real upstream HTTP/2 capture, add per-profile byte-level tests, update release/risk docs, and run feature matrix.

Each sub-phase is independently committable. Each commit must stage changes, run targeted tests, then run `codex exec review --uncommitted --full-auto` before commit per project discipline.

## 4. Per Sub-Phase Plan

### a. Evidence and Fixture Contract

**Scope.** Define a small fixture schema for captured upstream HTTP/2 bytes: initial SETTINGS raw order, SETTINGS id/value map, request pseudo-header order, target authority/path, capture source, and SHA/time metadata. This is required because the profile model already has `h2_settings_frame.raw_order`, `h2_settings_frame.values`, and `h2_pseudo_header_order.order` fields (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http_profile.rs:58-86`), and the schema document states these are wire-order capture fields (`tools/fingerprint-collector/templates/SCHEMA.md:92-103`).

**Files.**

- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/fixtures/http2_fingerprint/README.md`
- Create after real capture exists: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/fixtures/http2_fingerprint/<profile>-h2.json`
- Modify only if real data exists: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/*.json`
- Modify docs: `docs/process/release-readiness/W11-F-F1-status.md`

**Success criteria.**

- The fixture README states that synthetic L2-A6 data may be used for adapter regression tests but not for Released-spec F-1.
- If a built-in profile claims `h2_settings_frame.available=true`, its fixture must exist and the raw order/value fields must be non-empty; profile validation already enforces non-empty values when available (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:385-403`).
- If no real fixture exists, F-1 remains blocked at acceptance, not silently released. This mirrors the evidence-driven F-2.5 style where unmatched or unobserved variants remain explicitly open (`docs/process/release-readiness/W11-F-F2-5-status.md:118-132`, `docs/process/release-readiness/W11-F-F2-5-status.md:200-206`).

**Mutation discriminator.** If an implementer changes a profile to `h2_settings_frame.available=true` without adding matching fixture bytes, the new fixture/profile consistency test must fail with "available profile requires real fixture". If the fixture order is edited from `[4,1,6,...]` to another order while profile remains unchanged, the byte fixture parser test must fail on exact order equality.

**Blast radius.** Docs/tests/profile data only. No runtime behavior changes in this sub-phase.

### b. Adapter True-IO Extraction

**Scope.** Refactor `HttpTwoMimicryAdapter` so the code that performs fork handshake and request emission can run over any `AsyncRead + AsyncWrite`, while keeping `encode_request_exchange` as the in-memory compatibility wrapper. The current method hard-codes `tokio::io::duplex` and captures frames from the peer side (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:139-180`); the fork itself exposes `Builder::handshake` over generic async IO (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1312-1321`).

**Files.**

- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs`

**Success criteria.**

- Existing three in-memory tests still pass (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:12-77`).
- New loopback test binds `tokio::net::TcpListener`, accepts a real `TcpStream`, captures the client preface, SETTINGS, and HEADERS, and asserts the same decoded order/value as the profile.
- The loopback test must prove the listener was hit, for example by receiving a oneshot from the accept task before assertions complete.

**Mutation discriminator.** If the new loopback test is accidentally changed to call the old in-memory wrapper, the listener oneshot never fires and the test times out/fails. If `apply_settings` stops calling `builder.settings_order(...)`, the SETTINGS order assertion fails (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:204-254`; fork support at `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1210-1215`). If `apply_pseudo_order` stops calling `headers_pseudo_order`, the pseudo-header order assertion fails (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:257-272`; fork support at `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1217-1225`).

**Blast radius.** Feature-gated `mimicry-http2-fork` adapter and its tests only.

### c. L2 Preflight Module

**Scope.** Add `Http2PreflightStatus` and `Http2PreflightError` under a new focused module. The preflight performs: profile field presence check, adapter build check, loopback true-IO capture, SETTINGS frame decode, pseudo-header decode, and exact comparison against profile/fixture. This parallels L1's builder-side guard, which currently maps `Pending` or known gaps into `MimicryProductionCanaryError` before returning a client (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:95-103`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:119-145`).

**Files.**

- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_preflight.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs` or create `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_preflight_test.rs`

**Success criteria.**

- `MissingProfileField` or unavailable `h2_settings_frame`/`h2_pseudo_header_order` becomes `Failed(KnownGap)` with a reason naming the missing field; the adapter already rejects empty H2 fields (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:65-81`).
- A synthetic matching profile returns `Passed { profile_mode, evidence }`.
- A profile with same SETTINGS ids but wrong value returns `RuntimeMismatch`, not `Passed`.
- A profile with same SETTINGS but swapped pseudo-header order returns `RuntimeMismatch`.

**Mutation discriminator.** If the comparison only checks SETTINGS ids and ignores values, the wrong-value test fails to fail and the test suite catches it with an assertion that `Failed(RuntimeMismatch)` is required. If pseudo-header order comparison is removed, the swapped-order test goes red. If missing H2 fields are treated as `Passed`, the existing Codex missing-field style test fails because `HttpTwoMimicryAdapter::new_with_profile` currently returns a missing-field error for absent H2 data (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:12-23`).

**Blast radius.** New feature-gated mimicry module and tests. No ProxyEngine behavior changes until sub-phase f wires this gate into client construction.

### d. ProxyEngine Transport Boundary

**Scope.** Replace the concrete hyper-util-only `GatewayHttpClient` alias with a small internal transport wrapper while preserving public constructor ergonomics. `ProxyEngine` currently stores `GatewayHttpClient` directly and calls `.request(...)` (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:97-103`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:345-348`). The relay path already supports generic bodies once the fixed `Response<Incoming>` signature is generalized (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:54-61`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:120-133`).

**Files.**

- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/transport.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs`
- Modify affected tests: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_test.rs`, `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/attempt_reporter_test.rs`

**Success criteria.**

- Default/no-feature and `mimicry-boring`-only builds keep using the hyper-util path and existing tests pass.
- `ProxyEngine::new(build_http_client())` still compiles because `build_http_client()` returns the wrapper type; `GatewayState::new` still constructs through `build_http_client()` (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs:81-85`).
- `upstream_response_to_client` accepts the transport's boxed/erased response body without losing timeout, stream observation, non-stream usage buffering, or drop reporting behavior (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:62-95`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:140-180`).

**Mutation discriminator.** If the wrapper accidentally buffers the entire upstream response before relay, existing stream/timeout tests should fail because `relay_body` tests assert idle timeout and downstream timeout behavior through streaming reads (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:706-760`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:867-883`). If the wrapper drops response headers/status, `proxy_engine_test` should fail on existing status/header assertions.

**Blast radius.** Medium. This touches the ProxyEngine transport seam but should be behavior-preserving before the H2 fork variant is added.

### e. HTTP/2 Fork Outbound Client

**Scope.** Implement a feature-gated HTTP/2 fork client that opens a real TCP/TLS stream, runs the configured fork builder handshake, sends request head/body through the fork, awaits the fork response, and returns a response body compatible with `relay_body`. The existing Boring connector already classifies `https` as TLS and `http` as plain TCP for mock/dev tests (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:95-140`), and records whether TLS negotiated h2 (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:163-178`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:249-258`). The fork's response future yields `Response<RecvStream>` (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1568-1576`), and `RecvStream` exposes data polling and stream implementation under its own feature (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/share.rs:406-424`, `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/share.rs:463-468`).

**Files.**

- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http2_fork_client.rs`
- Create or modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_outbound.rs` or `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs` to share raw TCP/TLS connect logic
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- Create: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/proxy_engine_http2_fork_test.rs`

**Success criteria.**

- A loopback HTTP/2 capture server receives a real TCP connection from `ProxyEngine::forward_endpoint`, captures client preface + SETTINGS + HEADERS, then returns a small JSON response that relays to the downstream client.
- The request head is forced to HTTP/2 for fork sends; the fork docs state request version controls encoding and callers should use HTTP/2 when sending normal HTTP/2 requests (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:440-452`).
- POST request bodies are streamed into the fork `SendStream<Bytes>`; the fork API returns a response future and send stream for body/trailer use (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:422-438`, `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:527-550`).
- Response streaming through `relay_body` remains incremental and cancellation-safe.

**Mutation discriminator.** If the fork client skips the request body pump, a POST test with a two-chunk body fails because the loopback server observes no DATA frames. If the fork client constructs a hyper-util client instead of `http2::client::Builder`, the capture server observes default hyper SETTINGS/pseudo-header order and exact byte assertions fail. If the connection task is not polled/spawned, the request future stalls and the loopback test times out.

**Blast radius.** High inside Rust ProxyEngine. No Go backend packages, auth, billing, quota, or schema touched.

### f. Builder and Dispatch Wiring

**Scope.** Wire the new transport into `try_build_http_client_with_profile`: for `mimicry-boring + mimicry-http2-fork`, run existing L1 dispatch/preflight first, then run L2 HTTP/2 preflight, then return `GatewayHttpClient::Http2Fork(...)` only if both pass. For `mimicry-boring` without `mimicry-http2-fork`, keep current Boring hyper behavior. `build_mimicry_action` already maps builder errors into `MimicryAction::BlockKnownGap` / `BlockUnsupportedTemplate` rather than panicking (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:151-180`).

**Files.**

- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs` only if the error enum needs an L2-specific reason string
- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs`
- Add tests in: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs` test module and `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs` test module

**Success criteria.**

- Profiles with missing H2 fields fail closed under both features, returning a `KnownGap` reason that names L2 HTTP/2 runtime preflight.
- A synthetic profile with matching H2 fixture returns the HTTP/2 fork transport under both features.
- Existing L1 KnownGap/Pending tests keep their current fail-closed semantics (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:263-316`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:318-360`).
- `build_mimicry_action` returns structured `Block*` on L2 preflight error, not a panic.

**Mutation discriminator.** If the L2 preflight call is removed from `try_build_http_client_with_profile`, a missing-H2 profile returns a client and `expect_err("L2 missing H2 fields must fail closed")` goes red. If L2 error mapping panics instead of returning `Err`, dispatch tests modeled after the existing Codex/Gemini structured block tests fail because they expect `MimicryAction::BlockKnownGap` (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:286-320`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:334-360`). If feature cfg accidentally enables the fork path without `mimicry-http2-fork`, the no-fork feature matrix fails to compile because `http2` is optional (`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:16-17`, `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:32`).

**Blast radius.** Medium/high. This is where behavior changes for the combined feature build.

### g. Profile Backfill, Byte Tests, and Release Evidence

**Scope.** Import real upstream HTTP/2 capture evidence into built-in profile(s), add per-profile byte-level tests, and write release evidence. Do not invent values from the synthetic L2-A6 test profile; the existing synthetic helper explicitly labels itself synthetic (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:79-109`). F-2.5 already records the standard for capture evidence and explicit open questions (`docs/process/release-readiness/W11-F-F2-5-status.md:25-42`, `docs/process/release-readiness/W11-F-F2-5-status.md:142-164`).

**Files.**

- Modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/*.json` only for profiles with real captured H2 evidence
- Create or modify: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_wire_test.rs`
- Modify: `docs/process/release-readiness/W11-F-F1-status.md`
- Modify if a risk state changes: `docs/10_RISK_REGISTER.md`
- Modify if acceptance matrix tracks W11-F rows: `docs/11_ACCEPTANCE_TEST_MATRIX.md`

**Success criteria.**

- New L2 byte-level tests exist for every profile marked `h2_settings_frame.available=true`.
- At least the F-1 target profile reaches byte-level match against real upstream CLI capture; if Owner requires "all four built-ins" before Released, all four must have real capture fixtures and passing tests.
- `W11-F-F1-status.md` records exact commands, feature flags, test output summary, real fixture source, and any remaining profile-level mandatory roadmap items.

**Mutation discriminator.** If a profile's SETTINGS value is changed without changing fixture evidence, the byte-level test fails on id/value tuple equality. If pseudo-header order is changed without fixture evidence, HPACK/pseudo-order assertion fails. If a test is weakened to assert only "not equal to a bad value" instead of equality to the real fixture, review must flag it as a HIGH test-quality defect under the project mutation rule.

**Blast radius.** Profiles, tests, docs. Runtime behavior changes only for profiles that now pass L2 preflight.

## 5. Risk Register

| Risk | Type | Why it matters | Mitigation |
|---|---|---|---|
| The fork's unstable API changes or behaves differently from assumptions. | Dependency risk | HUAKAI enables `http2` with `features=["unstable"]` (`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:32`), and the fork itself says unstable APIs have no compatibility guarantees (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:Cargo.toml:30-34`). | Keep the rev pinned; add compile-time and byte-level tests against the pinned rev; require Owner confirmation before rev bump. |
| Response body type mismatch breaks relay streaming. | Technical risk | Fork responses use `RecvStream` while current upstream helper accepts `Response<Incoming>` (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1568-1576`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:54-61`). | Generalize/box response body before adding the fork variant; keep relay timeout/cancel tests in the same commit. |
| L2 preflight passes synthetic data but not real upstream CLI bytes. | Compatibility risk | Current test helper populates synthetic settings and pseudo order (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:79-109`), while built-in profiles can record unavailable H2 capture (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json:113-127`). | Separate synthetic adapter tests from real fixture tests; Released acceptance requires real fixture match. |
| Per-request HTTP/2 handshakes increase latency and connection load. | Performance risk | Existing hyper path pools idle connections (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:161-165`), while per-request fork handshakes repeat preface/SETTINGS. | F-1 recommended cut uses per-request handshakes for correctness; add pooled fork transport as a later performance phase after byte behavior is locked. |
| ALPN mismatch causes HTTPS upstream to reject or negotiate HTTP/1.1 while fork expects H2. | Compatibility/security risk | Existing Boring stream records `negotiated_h2` and marks hyper connection metadata only when ALPN is h2 (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:176-178`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:249-258`). | Fork client must reject HTTPS when selected ALPN is not h2; loopback plain tests stay dev-only, production endpoint guard remains HTTPS/public. |
| Request body pump mishandles DATA end-stream or trailers. | Technical risk | The fork separates request head, response future, and send stream (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:422-438`, `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:527-550`). | Add a two-chunk POST test and an empty-body test; treat request trailers as Owner decision because current gateway request flows do not depend on upstream trailers. |
| Clean-room/license drift if future work reads non-MIT reference source for expected bytes. | License risk | This plan only uses HUAKAI code and an MIT `http2` fork license/API (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:LICENSE:3-15`). | If later capture or comparison uses non-MIT reference source, require the project lane guard and paraphrase-only behavior evidence; do not copy upstream code/tests. |

## 6. Acceptance Criteria for Whole F-1

- `cargo test -p core_gateway --no-default-features` passes from `exploratory/rust-core-gateway/merged`.
- `cargo test -p core_gateway --features mimicry-http2-fork mimicry_http2_adapter` passes and includes both old in-memory tests and new true loopback capture tests.
- `cargo test -p core_gateway --features mimicry-boring,mimicry-http2-fork` passes targeted `proxy_engine_http2_fork`, `mimicry_http2_preflight`, `mimicry_http2_wire`, `proxy_engine`, and dispatch/client builder tests.
- For each profile claimed Released in F-1, runtime fork output byte-matches real upstream CLI fixture for initial SETTINGS id/value order and request pseudo-header order. This must be exact equality, not "not bad" assertions.
- L2 preflight is wired into the combined-feature builder: missing fixture/profile fields, wrong SETTINGS value, wrong SETTINGS order, or wrong pseudo-header order all return structured fail-closed errors before production traffic is sent.
- `build_mimicry_action` maps L2 preflight errors into `MimicryAction::BlockKnownGap` / `BlockUnsupportedTemplate` without panicking, following the F-2.3+ pattern (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:151-180`).
- Release evidence doc states whether F-1 is Released, Feature Flag, Safe Equivalent, or Mandatory Roadmap per profile. No profile is silently dropped.

## 7. Time Estimate

| Sub-phase | Estimate |
|---|---:|
| a. Evidence and fixture contract | 0.3 codex-day |
| b. Adapter true-IO extraction | 0.5 codex-day |
| c. L2 preflight module | 0.7 codex-day |
| d. ProxyEngine transport boundary | 0.8 codex-day |
| e. HTTP/2 fork outbound client | 1.2 codex-day |
| f. Builder/dispatch wiring | 0.5 codex-day |
| g. Profile backfill, byte tests, docs | 0.8 codex-day if real capture exists; add 0.5-1.0 codex-day if capture tooling must be extended |
| Total | 4.8-5.8 codex-days |

The long pole is the real outbound client plus streaming/body compatibility, because `ProxyEngine` currently assumes a hyper client at the storage/call site (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:97-103`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:345-348`).

## 8. Owner Decision Points

1. **Connection model.** Option A (recommended): per-request HTTP/2 fork connection for F-1, no pooling, easiest byte proof because every request emits preface/SETTINGS. Option B: pooled HTTP/2 fork connection, closer to hyper performance but increases state/lifecycle risk and makes "initial SETTINGS per request" tests less direct. Existing hyper pools today (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:161-165`); fork handshake emits preface/SETTINGS at connection setup (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1383-1422`).

2. **Release profile set.** Option A (recommended): F-1 Released only for profiles with real HTTP/2 fixture; others remain `Mandatory Roadmap` / `KnownGap`. Option B: block the whole F-1 release until all built-in profiles have real H2 capture. Current F-2.5 precedent keeps unobserved variants open rather than overstating them (`docs/process/release-readiness/W11-F-F2-5-status.md:126-132`, `docs/process/release-readiness/W11-F-F2-5-status.md:208-218`).

3. **Fixture source.** Option A (required for Released): capture from real upstream CLI/tool traffic and cite the capture artifact. Option B: use synthetic fixture only, but classify result as adapter-level test coverage, not F-1 Released. Current synthetic HTTP/2 helper is explicitly synthetic (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:100-107`).

4. **Request trailers.** Option A (recommended): F-1 supports empty-body and DATA body streaming, rejects or logs unsupported request trailers as a KnownGap because gateway provider APIs do not rely on upstream request trailers. Option B: implement full request trailer propagation now. The fork can send trailers through the send stream path (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:431-435`, `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:498-516`), but adding it now increases test surface.

5. **Preflight timing.** Option A (recommended): run L2 preflight at client build time for fail-fast and dispatch consistency. Option B: run on first request and cache result. Existing L1 builder gate already fails before returning a production client (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:78-103`), so Option A matches local pattern.

## 9. What Could Go Wrong

- The fork client compiles but does not drive the connection future, causing all requests to hang. This is likely because the fork returns both `SendRequest` and `Connection` and the connection must be polled while requests are active (`0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1356-1359`).
- The body adapter accidentally buffers entire upstream responses, breaking SSE and timeout semantics. Relay behavior depends on polling frames incrementally (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:154-180`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs:182-213`).
- The loopback preflight proves cleartext h2 but production HTTPS path negotiates `http/1.1`; the fork client must check ALPN for HTTPS before attempting h2 (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:176-178`, `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs:249-258`).
- HPACK pseudo-header parsing in tests can be too weak and pass when order is wrong. The current adapter test includes a custom HPACK static-name parser (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:141-193`); F-1 should either harden this parser with negative fixtures or move it to a reusable test helper with mutation tests.
- Combined feature cfg may accidentally route default/no-feature builds through optional `http2` symbols. The optional dependency is only enabled by `mimicry-http2-fork` (`exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:16-17`, `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:32`), so feature-matrix compile checks are mandatory.

## 10. Out-of-Scope Follow-Ups

- **F-1-runtime-preflight-wiring beyond build time.** If Owner chooses first-request preflight or periodic runtime recertification, that is a separate phase after the build-time L2 gate lands.
- **HTTP/2 connection pooling.** Pooling should wait until per-request byte correctness is locked and release evidence is stable.
- **Template revision across all CLIs.** F-1 can update only profiles with real H2 evidence; broader template revision for Codex/Gemini/Kiro/Anthropic belongs in a capture/backfill slice.
- **Real-upstream-capture expansion.** If the required H2 fixture does not exist yet, capture work is a prerequisite for Released status. Additional captures for ancillary endpoints remain separate follow-ups.
- **Non-MIT reference behavior mining.** If later work needs to compare against non-MIT project source, it must use the project clean-room lane guard. This F-1 plan does not require non-MIT source; it relies on HUAKAI code, real network capture artifacts, and the MIT `http2` fork license/API evidence.

## Source Coverage Proof

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs`: observed current in-memory adapter, required profile fields, SETTINGS/pseudo-order application, and frame capture helpers.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http_profile.rs`: observed H2 SETTINGS and pseudo-header profile structures.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs`: observed profile field storage and validation behavior for H2 capture availability.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profiles/anthropic_claude_code.json`: observed current missing H2 capture fields.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`: observed current hyper-util builder, L1 preflight gate, and tests.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`: observed ProxyEngine storage and outbound request call site.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/relay.rs`: observed response relay body assumptions and generic body internals.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs`: observed Boring/plain route split and ALPN h2 metadata.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs`: observed structured builder error mapping into `MimicryAction::Block*`.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs`: observed current in-memory HTTP/2 tests and synthetic fixture helper.
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs`: observed L1 byte-level test style to mirror for L2.
- `docs/process/release-readiness/W11-F-F2-5-status.md`: observed evidence-driven release/readiness precedent and explicit open-item handling.
- `tools/fingerprint-collector/templates/SCHEMA.md`: observed H2 capture schema semantics.
- `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:LICENSE`, `Cargo.toml`, `src/client.rs`, `src/frame/settings.rs`, `src/frame/headers.rs`, `src/share.rs`: observed MIT license and fork APIs for SETTINGS order, pseudo-header order, handshake, request send, and response stream.
