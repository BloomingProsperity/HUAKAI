# 2026-05-24 BoringSSL Fork Backend Implementation Plan — Codex Lane

> **For agentic workers:** this file is a plan artifact only. Do not execute it until Owner approves a synthesized plan after cross-discussion. If execution starts later, use a fresh execution plan session and preserve every clean-room citation boundary in this file.

| Field | Value |
|---|---|
| Owner directive | “[OWNER AUTHORIZED 2026-05-24T08:00Z workspace-write — Rust sidecar BoringSSL plan] … 走 Rust 子层用 cloudflare/boring crate(Rust → BoringSSL C FFI) + Rust sidecar 接 Go gatewayhttp 出站。不走 Go cgo,不走 wreq vendor。” |
| Plan path | `/home/codex/HUAKAI/docs/process/plans/2026-05-24-boringssl-fork-backend-codex.md` |
| Lane | Codex independent plan lane |
| Scope | Plan only; no implementation; no `git add`; no commit; no push |
| Current UTC | 2026-05-24T08:36:04Z |
| Observed regions | 35 |
| Inferences | 14 |
| Open questions | 9 |

Process note: a broad local `rg` search accidentally returned snippets from `/home/codex/HUAKAI/docs/process/plans/2026-05-24-boringssl-fork-backend-claude.md`. I did not open that file, did not cite it, and do not rely on it below. The evidence spine for this plan is the locked Owner docs, HUAKAI source files, and the reference source regions listed in §9.

=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: cloudflare/boring / hyperium/h2 / 0x676e67/wreq / envoyproxy/ai-gateway

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
  Source files read: see §9
  Lane: specifier
  Agent: Codex GPT-5 lane
  UTC timestamp: 2026-05-24T08:36:04Z

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===

---

## §1 目标范围

### 1.1 Goal

- Build a Rust sidecar transport layer that the Go production gateway can select for outbound mimicry traffic.
- The sidecar owns upstream TCP connect, TLS handshake, ALPN, HTTP/2 connection preface, HTTP/2 SETTINGS, request header ordering, response streaming, and transport error classification.
- Go stays the production gateway control/data plane for request admission, account selection, credential resolution, billing, quota, and response forwarding.
- Rust is the controlled transport sub-layer, not the new product gateway.
- The Owner-locked backend choice is Rust → cloudflare/boring → BoringSSL C FFI, with no Go cgo and no wreq vendor.
- BoringSSL is the C/ASM TLS engine; the Rust crate is the binding/adaptor surface.
- The plan treats current Go uTLS as a Stage 0 dual-track baseline, not as the final L1 backend.

### 1.2 Why this exists now

- Owner locked AS-D1 to connect Anthropic OAuth transport mimicry now, so the transport backend blocks the C1+ Anthropic slices. Evidence: `HUAKAI:docs/process/plans/2026-05-24-decisions-locked.md:8`, `HUAKAI:docs/process/plans/2026-05-24-decisions-locked.md:50`.
- The same locked doc says STAGE 0 transport remains the blocking prerequisite for Anthropic OAuth work. Evidence: `HUAKAI:docs/process/plans/2026-05-24-decisions-locked.md:52`, `HUAKAI:docs/process/plans/2026-05-24-decisions-locked.md:69`.
- Current HUAKAI Go dispatcher already selects a `RoundTripper` by provider plus transport mode before request execution. Evidence: `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:122`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:127`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:139`.
- Current policy already treats provider × mode as a fail-closed matrix rather than a best-effort fallback. Evidence: `HUAKAI:backend/internal/transport/policy.go:95`, `HUAKAI:backend/internal/transport/policy.go:207`, `HUAKAI:backend/internal/transport/policy.go:217`.
- Current mimicry factory already rejects missing/stub templates unless an explicit debug env flag opts into fallback. Evidence: `HUAKAI:backend/internal/transport/factory.go:159`, `HUAKAI:backend/internal/transport/factory.go:167`, `HUAKAI:backend/internal/transport/factory.go:181`, `HUAKAI:backend/internal/transport/factory.go:188`.

### 1.3 Fingerprint dimensions in scope

- JA3: TLS version, cipher list, extension list, supported groups, and point formats must match captured target profiles.
- JA4: not just the JA3 tuple; verification must include fields affected by ALPN, SNI, extension count/order, and transport protocol.
- HTTP/2 SETTINGS: values and ordering must be profile-controlled, because wreq treats HTTP/2 settings as a first-class emulation surface rather than deriving them from JA3/JA4 strings. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:67`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:69`.
- HTTP/2 pseudo-header order: request framing must support profile-specific pseudo ordering; wreq’s tests model pseudo-order as an explicit profile value. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:216`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:248`.
- ECH: sidecar must support both real ECH config handling and GREASE behavior, because the boring binding exposes client ECH configuration, retry config retrieval, acceptance status, and GREASE toggling. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3775`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3794`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3838`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3845`.
- PQ: sidecar must carry post-quantum group/profile configuration, because the Rust binding stack documents PQ support and its low-level build path applies a PQ patch by default. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md:10`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md:11`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/build/main.rs:467`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/build/main.rs:468`.

### 1.4 Difference from current Go uTLS path

- Go uTLS is already wired as a `RoundTripper` that uses a custom ClientHello template through `DialTLSContext`. Evidence: `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:33`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:41`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:77`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:88`.
- Go uTLS templates currently represent TLS-level fields and some HTTP/auth metadata. Evidence: `HUAKAI:backend/internal/transport/mimicry/template.go:14`, `HUAKAI:backend/internal/transport/mimicry/template.go:22`, `HUAKAI:backend/internal/transport/mimicry/template.go:35`, `HUAKAI:backend/internal/transport/mimicry/template.go:41`.
- The current uTLS extension mapping turns unknown extension IDs into generic placeholders when payload is unavailable. Evidence: `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:130`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:160`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:164`, `HUAKAI:backend/internal/transport/mimicry/utls_dialer.go:166`.
- That is enough for the verified Stage 0 JA3 mimicry baseline but not enough for full byte-level control of ECH payloads, PQ key-share behavior, HTTP/2 SETTINGS ordering, and JA4/akamai-style transport fingerprints.
- The BoringSSL sidecar therefore must be a transport owner, not just a TLS dialer injected into Go `net/http`.

### 1.5 Non-goals

- No Go cgo.
- No wreq vendoring.
- No backend schema migration in this work unit.
- No auth core rewrite.
- No billing ledger change.
- No quota enforcement change.
- No changes to `LICENSE`.
- No new files in frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- No production default-on sidecar until packet capture parity gates pass.
- No claim that Rust `core_gateway` replaces Go production gateway in this slice; the Rust tree is explicitly exploratory today. Evidence: `HUAKAI:exploratory/rust-core-gateway/README.md:1`, `HUAKAI:exploratory/rust-core-gateway/README.md:4`.

### 1.6 Success criteria

- Phase 1 can run Go gateway outbound through a local Rust sidecar over a Unix-domain IPC path with streaming request and response bodies.
- Phase 1 preserves provider × mode fail-closed behavior.
- Phase 1 preserves account proxy isolation by failing closed or carrying proxy metadata explicitly; it must not silently route proxy-bound accounts direct.
- Phase 2 can produce a captured ClientHello matching the selected target profile’s JA3.
- Phase 3 can produce HTTP/2 SETTINGS values and ordering matching the selected target profile.
- Phase 4 can compute and compare JA4/akamai-style evidence from packet capture outputs.
- Phase 5 can carry ECH/PQ profile controls behind explicit flags.
- Every phase has a mutation self-check proving the test would fail if the fingerprint dimension is ignored.

---

## §2 现状

### 2.1 Go production outbound shape

- `UpstreamDispatcher` is the current production assembly point for provider adapter, transport factory, and HTTP client. Evidence: `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:1`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:5`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:7`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:12`.
- It builds a provider request before selecting transport. Evidence: `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:111`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:112`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:117`.
- It selects transport after resolving `TransportMode`, then constructs a `http.Client` with the selected `RoundTripper` when no test client is injected. Evidence: `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:122`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:123`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:133`, `HUAKAI:backend/internal/gateway/upstream_dispatcher.go:139`.
- This is the right integration point for sidecar routing because gatewayhttp handlers do not need to learn TLS/H2 fingerprint details.
- The production handler path should continue to see a normal upstream response body and status; only the chosen transport changes.

### 2.2 Current transport factory behavior

- `Factory.For` validates provider × mode before choosing a concrete transport. Evidence: `HUAKAI:backend/internal/transport/factory.go:75`, `HUAKAI:backend/internal/transport/factory.go:82`, `HUAKAI:backend/internal/transport/factory.go:83`.
- Standard mode uses a cloned Go default transport with env proxy stripped. Evidence: `HUAKAI:backend/internal/transport/factory.go:115`, `HUAKAI:backend/internal/transport/factory.go:119`, `HUAKAI:backend/internal/transport/factory.go:138`, `HUAKAI:backend/internal/transport/factory.go:139`.
- Mimicry modes currently return either an injected mimicry transport or a per-mode uTLS transport built from the template registry. Evidence: `HUAKAI:backend/internal/transport/factory.go:89`, `HUAKAI:backend/internal/transport/factory.go:97`, `HUAKAI:backend/internal/transport/factory.go:100`, `HUAKAI:backend/internal/transport/factory.go:154`.
- The sidecar should plug into this exact mimicry selection branch.
- Do not add a new transport mode for “sidecar”; sidecar is a backend implementation of existing mimicry modes.
- A backend selector can be configured as `utls` or `boring_sidecar`, while `TransportModeMimicryChatGPT` and peers remain vendor intent.

### 2.3 Current provider/mode gating

- The policy table allows standard and selected mimicry modes per provider and rejects disallowed combinations. Evidence: `HUAKAI:backend/internal/transport/policy.go:100`, `HUAKAI:backend/internal/transport/policy.go:101`, `HUAKAI:backend/internal/transport/policy.go:106`, `HUAKAI:backend/internal/transport/policy.go:140`, `HUAKAI:backend/internal/transport/policy.go:172`.
- Unknown provider and unknown mode already produce stable errors. Evidence: `HUAKAI:backend/internal/transport/policy.go:85`, `HUAKAI:backend/internal/transport/policy.go:89`, `HUAKAI:backend/internal/transport/policy.go:92`.
- Sidecar selection must not weaken this table.
- Cross-vendor mimicry must remain a configuration error before any IPC connection is attempted.
- The plan should add tests that selecting sidecar for an allowed mimicry mode returns a sidecar-backed transport, while selecting sidecar for a disallowed provider/mode remains rejected by policy.

### 2.4 Current profile format and uTLS constraints

- `ClientHelloTemplate` stores JA3/JA4, cipher suites, extension IDs, supported versions, groups, signature algorithms, ALPN, key-share groups, PSK modes, padding, and HTTP/auth metadata. Evidence: `HUAKAI:backend/internal/transport/mimicry/template.go:14`, `HUAKAI:backend/internal/transport/mimicry/template.go:21`, `HUAKAI:backend/internal/transport/mimicry/template.go:24`, `HUAKAI:backend/internal/transport/mimicry/template.go:31`, `HUAKAI:backend/internal/transport/mimicry/template.go:35`.
- The validator requires real templates to include JA4 and parseable JA3. Evidence: `HUAKAI:backend/internal/transport/mimicry/template.go:151`, `HUAKAI:backend/internal/transport/mimicry/template.go:154`, `HUAKAI:backend/internal/transport/mimicry/template.go:157`.
- Current Phase A default template is an embedded sample for Anthropic-style transport. Evidence: `HUAKAI:backend/internal/transport/mimicry/template.go:165`, `HUAKAI:backend/internal/transport/mimicry/template.go:171`, `HUAKAI:backend/internal/transport/mimicry/template.go:178`.
- Sidecar should reuse profile validation concepts but extend the profile contract to include H2 settings order, H2 pseudo-header order, ECH, PQ, certificate compression, record size, and capture expectations.
- Do not overload `ClientHelloTemplate` until it becomes too broad; create a sidecar-specific profile type in a new file to keep `template.go` from becoming a mixed TLS/H2/IPC model.

### 2.5 Current proxy isolation behavior

- HUAKAI has account-level proxy resolution to keep account IP/proxy decisions explicit. Evidence: `HUAKAI:backend/internal/provider/proxy_resolver.go:1`, `HUAKAI:backend/internal/provider/proxy_resolver.go:23`, `HUAKAI:backend/internal/provider/proxy_resolver.go:31`.
- Current proxy injection returns the original transport for direct accounts, clones standard `http.Transport` for proxy-bound standard transports, and fails closed for non-standard transports. Evidence: `HUAKAI:backend/internal/provider/proxy_resolver.go:111`, `HUAKAI:backend/internal/provider/proxy_resolver.go:117`, `HUAKAI:backend/internal/provider/proxy_resolver.go:121`, `HUAKAI:backend/internal/provider/proxy_resolver.go:126`, `HUAKAI:backend/internal/provider/proxy_resolver.go:135`.
- This means a sidecar `RoundTripper` must either receive proxy metadata explicitly or fail closed for proxy-bound accounts.
- Silent direct routing for proxy-bound accounts is a security regression and must be a HIGH test finding.
- Phase 1 may accept fail-closed for proxy-bound sidecar accounts if the rollout flag is disabled by default.
- Phase 2 must carry proxy metadata through IPC before sidecar is used for real subscription accounts that rely on account IP isolation.

### 2.6 Current Rust side

- The exploratory Rust gateway is marked as non-production and separate from the Go backend. Evidence: `HUAKAI:exploratory/rust-core-gateway/README.md:1`, `HUAKAI:exploratory/rust-core-gateway/README.md:4`.
- The merged Rust workspace already exists and currently has one member. Evidence: `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:1`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:2`.
- That workspace already pins BoringSSL-related Rust dependencies through local paths and patch redirection. Evidence: `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:10`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:12`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:15`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:19`.
- `core_gateway` already exposes feature gates for BoringSSL mimicry and an HTTP/2 fork option. Evidence: `HUAKAI:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:8`, `HUAKAI:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:14`, `HUAKAI:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:16`.
- The readiness report still marks Rust as not ready for mainline connection without additional prerequisites. Evidence: `HUAKAI:exploratory/rust-core-gateway/merged/READINESS.md:85`, `HUAKAI:exploratory/rust-core-gateway/merged/READINESS.md:87`, `HUAKAI:exploratory/rust-core-gateway/merged/READINESS.md:91`, `HUAKAI:exploratory/rust-core-gateway/merged/READINESS.md:105`.
- Therefore, this plan should add a focused sidecar crate rather than expanding the exploratory gateway into the production hot path.

### 2.7 Current Boring fork state inside HUAKAI

- HUAKAI already has a vendored Boring crate copy and modification log under the exploratory tree. Evidence: `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:1`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:3`.
- The modification log says the vendored package came from cloudflare/boring package material and records license notes. Evidence: `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:3`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:22`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:27`.
- The same log records HUAKAI-local C/Rust API work for extension ordering, TLS 1.3 cipher ordering, raw profile fields, and profile hardening. Evidence: `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:45`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:86`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:134`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:150`, `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:170`.
- This plan should not re-vendor or rewrite the existing fork blindly.
- The first executor must verify the vendored path still builds, then decide whether the sidecar crate uses the existing local path or a fresh fork pin under the same vendor policy.

---

## §3 Ref 项目方案

### 3.1 cloudflare/boring behavior to use

- Observed: cloudflare/boring positions BoringSSL as Google’s OpenSSL fork and provides Rust bindings plus tokio/hyper adapters. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md:5`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md:7`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md:8`.
- Observed: its low-level binding crate builds or links BoringSSL and generates FFI bindings. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/README.md:5`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:11`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:12`.
- Observed: its build includes BoringSSL C/ASM/Go test material in the package include list, so the sidecar must treat this as Rust controlling a native TLS library, not a pure Rust TLS stack. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:23`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:35`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:39`.
- Observed: the higher-level Rust binding exposes pre-TLS-1.3 cipher list configuration but states TLS 1.3 cipher control is not covered by the same standard OpenSSL method in BoringSSL. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1454`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1458`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1459`.
- Observed: the binding exposes ALPN configuration using wire-format ordered protocol lists. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1588`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1590`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1593`.
- Observed: the binding exposes certificate compression registration. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1669`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1673`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1706`.
- Observed: the binding exposes key logging for packet capture decryption. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1910`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1912`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1913`.
- Observed: the binding exposes GREASE toggling and extension permutation controls. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1980`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1982`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1986`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1988`.
- Observed: its async adapter performs client-side handshakes over any async stream and leaves TLS parameter configuration in the base binding. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:tokio-boring/src/lib.rs:1`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:tokio-boring/src/lib.rs:9`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:tokio-boring/src/lib.rs:40`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:tokio-boring/src/lib.rs:52`.
- Observed: its hyper adapter configures ALPN and reports negotiated HTTP/2 through the connection metadata. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:hyper-boring/src/v1.rs:35`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:hyper-boring/src/v1.rs:107`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:hyper-boring/src/v1.rs:285`.
- HUAKAI fit: use cloudflare/boring for native TLS control and async integration; do not use hyper-boring as the final transport if its HTTP/2 stack prevents exact SETTINGS and pseudo-order control.

### 3.2 hyperium/h2 behavior to use or avoid

- Observed: the h2 client builder stores an initial settings object as part of client configuration. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:309`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:334`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:335`.
- Observed: the builder exposes value-level knobs for initial stream window, connection window, frame size, header list size, concurrent streams, push enablement, and header table size. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:699`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:734`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:773`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:808`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:857`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1110`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1143`.
- Observed: the client handshake buffers the initial settings frame from the builder into the codec. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1322`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1324`.
- Observed: settings parsing validates payload length and legal values. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:150`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:159`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:175`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:182`.
- Observed: the settings encoder emits set fields by walking a fixed internal sequence. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:213`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:229`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:232`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:256`.
- HUAKAI fit: upstream h2 can be used for state-machine behavior and validation, but it is insufficient by itself for arbitrary SETTINGS ordering.
- HUAKAI fit: Phase 3 should either extend the existing HUAKAI-owned HTTP/2 fork path or add a tiny HUAKAI-owned initial-frame writer that is tested against packet captures.
- Avoid: do not pretend h2 value setters alone solve H2 fingerprint parity.

### 3.3 wreq behavior to borrow conceptually

- Observed: wreq explicitly says simple string fingerprints are not reliable enough for modern TLS plus HTTP/2 emulation; it exposes fine-grained TLS and H2 controls. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:67`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:69`.
- Observed: wreq models TLS profile controls for ALPN, ALPS, session tickets, versions, PSK, ECH GREASE, extension permutation, GREASE, OCSP, SCT, record size, key shares, curves, signature algorithms, cipher preference, certificate compression, and extension permutation. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:129`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:142`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:150`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:165`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:189`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:197`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:228`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:251`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:256`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:261`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:273`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:278`.
- Observed: wreq’s builder applies TLS profile values into a BoringSSL connector rather than treating the profile as just metadata. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:344`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:352`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:396`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:433`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:440`.
- Observed: wreq’s per-connection path applies SNI/hostname verification, ECH GREASE, ALPN selection, ALPS, key shares, and session cache material during setup. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:137`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:140`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:146`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:155`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:179`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:191`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:200`.
- Observed: wreq exposes H2 pseudo-order, SETTINGS order, stream dependency, and window/frame values as profile data. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/lib.rs:302`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/lib.rs:305`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:227`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:241`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:249`.
- HUAKAI fit: borrow the behavior model, not code.
- HUAKAI fit: profile fields should be explicit, testable, and packet-capture verified.
- Avoid: do not vendor wreq; do not copy profile structures, names, source layout, tests, or examples.

### 3.4 Envoy AI Gateway behavior to use for decision framing

- Observed: Envoy AI Gateway has controller logic translating high-level AI gateway route backend references into gateway HTTP route backend references and filters. Evidence: `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go:245`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go:255`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go:258`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go:312`.
- Observed: it keeps filter configuration decoupled from gateway API and Kubernetes implementation details. Evidence: `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:6`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:9`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:23`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:40`.
- Observed: it has backend auth handler selection based on explicit backend auth config rather than hidden transport guesses. Evidence: `envoyproxy/ai-gateway@3d3d346d09e4:internal/backendauth/auth.go:15`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/backendauth/auth.go:17`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/backendauth/auth.go:30`.
- HUAKAI fit: sidecar transport backend selection should be explicit in transport config and visible to ops, not inferred from request host or token type.
- HUAKAI fit: sidecar profile resolver can mirror this separation by keeping route/provider mode decisions in Go and byte-level transport details in Rust.

---

## §4 文件级范围

### 4.1 Package structure rule

- `backend/internal/gatewayhttp` is frozen; no new files.
- `backend/internal/gateway` is frozen; no new files.
- `backend/internal/proto` is frozen; no new files.
- `backend/internal/transport` is not frozen and currently has small file count and line count. Current non-test source count is 5 files and roughly 1157 non-test lines, so adding a focused mimicry sidecar package/file set does not cross the project package budget.
- Existing frozen files may be modified only if unavoidable and low/medium risk.
- Preferred Go write scope is `backend/internal/transport/mimicry` plus small existing-file edits in `backend/internal/transport/factory.go` and `backend/internal/provider/proxy_resolver.go`.
- Preferred Rust write scope is `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar` plus workspace manifest edits.

### 4.2 Rust crate layout

- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/Cargo.toml`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/main.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/lib.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/config.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/ipc.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/ipc_frame.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/profile.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/profile_store.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/tls_backend.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/tls_boring.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/h2_profile.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/h2_client.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/proxy.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/response_stream.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/error.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/src/metrics.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/tests/ipc_contract_test.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/tests/profile_validation_test.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/tests/h2_profile_test.rs`.
- Create: `exploratory/rust-core-gateway/merged/crates/boring_fork_sidecar/tests/tls_capture_shape_test.rs`.
- Modify: `exploratory/rust-core-gateway/merged/Cargo.toml`.
- Modify: `exploratory/rust-core-gateway/merged/Cargo.lock`.

### 4.3 Rust file responsibilities

- `main.rs`: parse sidecar config, initialize tracing, bind Unix socket, serve requests, and handle shutdown.
- `lib.rs`: module exports for tests and minimal top-level run entry.
- `config.rs`: environment and file config, including sidecar socket path, profile directory, capture/debug keylog path, timeout knobs, and default-disabled production flag.
- `ipc.rs`: accept local IPC connections, authenticate local peer where OS supports it, enforce max request/body sizes, and dispatch per request.
- `ipc_frame.rs`: HUAKAI-owned framed protocol for request metadata, request body chunks, response headers, response body chunks, trailers, and error class frames.
- `profile.rs`: sidecar-specific profile model covering TLS, H2, ECH, PQ, capture expectations, and allowed upstream host.
- `profile_store.rs`: load profiles from sanitized JSON and map Go transport mode to sidecar profile.
- `tls_backend.rs`: trait boundary for test doubles and future backend variants.
- `tls_boring.rs`: BoringSSL binding integration for profile-to-TLS configuration.
- `h2_profile.rs`: H2 settings values, H2 settings order, pseudo-header order, stream dependency, window sizes, and HPACK policy.
- `h2_client.rs`: H2 request/response streaming implementation; Phase 3 must decide whether it uses a forked h2 state machine or HUAKAI-owned initial frame control.
- `proxy.rs`: direct, HTTP proxy, and SOCKS proxy connection setup as supported by profile/Go metadata.
- `response_stream.rs`: body streaming bridge, idle timeout, upstream header timeout, upstream body idle timeout.
- `error.rs`: stable sidecar error taxonomy mapped to Go `TransportErrorClass`.
- `metrics.rs`: local counters for sidecar attempts, fingerprint profile ID, error class, handshake latency, and H2 negotiation result.

### 4.4 Go transport layout

- Create: `backend/internal/transport/mimicry/sidecar_config.go`.
- Create: `backend/internal/transport/mimicry/sidecar_profile.go`.
- Create: `backend/internal/transport/mimicry/sidecar_roundtripper.go`.
- Create: `backend/internal/transport/mimicry/sidecar_ipc.go`.
- Create: `backend/internal/transport/mimicry/sidecar_router.go`.
- Create: `backend/internal/transport/mimicry/sidecar_errors.go`.
- Create: `backend/internal/transport/mimicry/sidecar_config_test.go`.
- Create: `backend/internal/transport/mimicry/sidecar_roundtripper_test.go`.
- Create: `backend/internal/transport/mimicry/sidecar_ipc_test.go`.
- Create: `backend/internal/transport/mimicry/sidecar_router_test.go`.
- Modify: `backend/internal/transport/factory.go`.
- Modify: `backend/internal/transport/factory_test.go`.
- Modify: `backend/internal/provider/proxy_resolver.go`.
- Modify: `backend/internal/provider/proxy_resolver_test.go` if existing; otherwise add focused cases to the closest existing provider proxy test file.

### 4.5 Go file responsibilities

- `sidecar_config.go`: parse env/config for backend choice, socket path, fail-open/fail-closed policy, request timeout, and health check interval.
- `sidecar_profile.go`: map `TransportMode` to sidecar profile ID and carry capture expectation metadata for observability.
- `sidecar_roundtripper.go`: implement `http.RoundTripper` by serializing request metadata/body into IPC and converting response frames back into `http.Response`.
- `sidecar_ipc.go`: Unix socket dialer, frame read/write, deadlines, and max-frame enforcement.
- `sidecar_router.go`: choose sidecar vs uTLS for mimicry modes based on explicit backend config, mode, profile availability, sidecar health, and rollout flag.
- `sidecar_errors.go`: map sidecar error classes into Go errors that `gateway.ClassifyAttemptDispatchError` can classify.
- `factory.go`: add a sidecar router injection point and choose it only when config says `boring_sidecar`.
- `factory_test.go`: add discriminating tests for sidecar selected vs uTLS selected vs no registry vs stub profile.
- `proxy_resolver.go`: add a proxy-aware hook so non-standard transports can explicitly accept proxy metadata instead of hitting the current unsupported-transport fail-closed branch.

### 4.6 Existing frozen file modifications

- Preferred: no new file in `backend/internal/gateway`.
- Possible modify: `backend/internal/gateway/upstream_dispatcher.go` only if a proxy-aware hook cannot be implemented entirely through `provider.WrapTransportWithProxy`.
- If modified, the change must be minimal: keep the dispatcher as adapter/transport/client assembly and do not add sidecar-specific logic.
- Do not add a gatewayhttp handler file.
- Do not add a gateway proto file.
- Do not mutate billing, quota, auth, or database schema in this sidecar slice.

### 4.7 IPC contract boundary

- Go sends request metadata: method, URI, authority, headers with original order where available, selected transport mode, profile ID, request ID, account ID, proxy URL if explicitly resolved, timeout budget, and body framing.
- Rust validates profile ID against allowed host/profile mapping.
- Rust owns TCP connect, optional proxy connect, BoringSSL configuration, TLS handshake, ALPN validation, H2 preface, H2 SETTINGS, H2 request framing, and response body streaming.
- Rust returns response status, headers, response body chunks, trailers, timing metadata, negotiated ALPN, profile ID, JA3/JA4 observed values if locally computed, and error class if failed.
- Go converts sidecar error class into existing dispatcher/gateway classification paths.
- Go must not send credentials to Rust beyond headers already required for upstream dispatch.
- IPC logs must redact Authorization, cookies, session tokens, and upstream auth material.

### 4.8 Why not raw plaintext TCP forwarding

- Raw plaintext TCP forwarding from Go to Rust would let Go own HTTP serialization and would not let Rust control H2 SETTINGS and pseudo-header order.
- The sidecar can accept a bidirectional byte stream over Unix socket, but the bytes must be HUAKAI IPC frames, not an already-serialized HTTP/2 session from Go.
- If a future mode tunnels raw HTTP/1.1 plaintext, it must be limited to HTTP/1 profiles and explicitly marked incapable of H2 fingerprint parity.
- Phase 1 should therefore define a request/response IPC envelope, not a transparent TCP proxy.

---

## §5 切片建议

### Phase 1 — Rust sidecar skeleton + IPC

- Goal: prove Go can select sidecar transport for allowed mimicry modes and receive a streamed response from a local Rust process.
- Deliverable: Rust sidecar binary under exploratory workspace.
- Deliverable: Go sidecar `RoundTripper` and router under `backend/internal/transport/mimicry`.
- Deliverable: local Unix socket IPC contract tests.
- Deliverable: sidecar disabled by default.
- Deliverable: uTLS path still works when sidecar config is off.
- Deliverable: proxy-bound sidecar requests fail closed unless proxy metadata support is implemented in the same phase.
- In scope: Unix-domain socket on Linux/macOS.
- In scope: local process health check.
- In scope: request metadata and body streaming.
- In scope: response status/header/body streaming.
- In scope: sidecar error class mapping.
- Out of scope: perfect JA3.
- Out of scope: arbitrary H2 SETTINGS order.
- Out of scope: real ECH.
- Out of scope: PQ default enablement.
- File changes: Rust crate files listed in §4.2.
- File changes: Go files listed in §4.4.
- Test: Go unit test where sidecar backend selected for `mimicry_chatgpt` returns a fake upstream response through IPC.
- Test: Go unit test where sidecar backend configured but provider/mode invalid still returns policy rejection before IPC.
- Test: Go mutation self-check by swapping requested mode to a cross-vendor mode; expected result must change from sidecar selection to policy error.
- Test: Rust IPC test where malformed frame length closes connection and returns local protocol error.
- Test: Rust IPC test where oversized request body is rejected before upstream connect.
- Test: Go proxy isolation test where proxy-bound sidecar transport either passes proxy metadata to a proxy-aware hook or returns `ErrProxyUnsupportedTransport`.
- Verification command: `cd backend && go test ./internal/transport ./internal/provider`.
- Verification command: `cd exploratory/rust-core-gateway/merged && cargo test -p boring_fork_sidecar`.
- Verification command: `cd exploratory/rust-core-gateway/merged && cargo clippy -p boring_fork_sidecar -- -D warnings`.
- Exit gate: sidecar is selectable but not production default.
- Exit gate: no new files in frozen packages.
- Exit gate: no new Go runtime dependency unless Owner approves.

### Phase 2 — BoringSSL JA3 one-key profile

- Goal: configure BoringSSL sidecar profile enough to match one captured target JA3.
- Target first profile: use a non-secret local capture profile approved by Owner.
- Rust side: map sanitized profile fields to BoringSSL context/connection configuration.
- Rust side: enable key logging only in explicit capture mode.
- Rust side: expose profile validation errors before connect.
- Rust side: reject profile values unsupported by current Boring fork instead of silently ignoring them.
- Go side: pass profile ID from mode router into IPC.
- Go side: expose selected backend/profile in debug logs with no secrets.
- Test: profile missing required cipher/group/extension data fails validation.
- Test: profile with unknown target host fails allowed-host validation.
- Test: mutation self-check by removing profile cipher mapping; packet-capture or local ClientHello parser must no longer match expected JA3.
- Test: local TLS test server captures ClientHello and compares JA3 against fixture.
- Verification command: `cd exploratory/rust-core-gateway/merged && cargo test -p boring_fork_sidecar tls_capture_shape`.
- Verification command: capture sidecar connection to a local TLS fixture with keylog disabled and JA3 parser enabled.
- Exit gate: one profile produces matching JA3 in local capture.
- Exit gate: mismatch prints observed vs expected fingerprint without raw secrets.
- Exit gate: unsupported profile features produce fail-closed errors.

### Phase 3 — HTTP/2 SETTINGS + pseudo-order

- Goal: Rust sidecar owns H2 client preface, initial SETTINGS values, initial SETTINGS order, stream dependency, window sizes, and pseudo-header ordering.
- Ref constraint: upstream h2 exposes value knobs but fixed settings encode traversal, so Phase 3 must add controlled ordering rather than relying on default h2 encoder alone. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:229`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:232`, `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:256`.
- Ref pattern: wreq treats H2 settings order and pseudo-order as explicit emulation fields. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:216`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:227`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:241`.
- Option A: extend HUAKAI’s existing HTTP/2 fork dependency path to expose settings order and pseudo-order.
- Option B: write a HUAKAI-owned initial preface/settings writer, then hand off to a state machine only if it will not duplicate initial settings.
- Option C: write a minimal H2 client for this sidecar path with a very narrow request/streaming scope.
- Recommended for Phase 3: Option A if existing fork is already accepted in Rust workspace; Option B if handoff can be proven by tests; avoid Option C unless necessary.
- Test: raw H2 frame capture asserts exact settings ID sequence and values.
- Test: mutation self-check by reversing two settings IDs; packet test must fail.
- Test: pseudo-header order fixture asserts encoded header block order where observable.
- Test: high-throughput response streaming does not buffer full response in memory.
- Test: server sends SETTINGS ACK and sidecar handles it.
- Test: server sends GOAWAY/REFUSED_STREAM and Go receives retryable sidecar class where appropriate.
- Verification command: packet capture against local H2 fixture.
- Exit gate: captured H2 fingerprint matches profile for SETTINGS values/order and pseudo-order.

### Phase 4 — JA4 calculation and capture diff

- Goal: make fingerprint verification operator-readable and hard to fake.
- Add sidecar capture diff tooling that reads pcap or fixture output and computes JA3/JA4 plus H2 settings evidence.
- Add profile expected-fingerprint block with `expected_ja3`, `expected_ja4`, `expected_h2_settings_order`, `expected_alpn`, and capture source metadata.
- Add Go debug endpoint or log event only if it does not leak secrets.
- Test: profile whose JA3 matches but H2 settings differ is marked mismatch.
- Test: profile whose JA4 differs while JA3 matches is marked mismatch.
- Test: mutation self-check by disabling ALPN order; JA4/capture test must fail.
- Test: capture diff output redacts SNI only if required by privacy mode; otherwise target host is operational metadata, not a credential.
- Verification command: local sidecar capture against `tls.peet.ws`-style local fixture or an Owner-approved external capture endpoint.
- Exit gate: no “JA3-only pass” label exists for a profile that requires JA4/H2 parity.

### Phase 5 — ECH/PQ

- Goal: add ECH and PQ controls behind explicit profile flags and rollout gates.
- ECH config source: profile file or resolver output, never hard-coded secret.
- ECH retry handling: if server rejects ECH and returns retry configs, sidecar must either retry once according to profile policy or fail with a specific class.
- ECH GREASE: support GREASE-only mode for targets where real config is unavailable.
- PQ: support configured hybrid group list and key-share behavior if the current Boring fork exposes the required controls.
- PQ default: disabled unless profile explicitly requires it.
- Test: ECH GREASE enabled changes capture vs disabled baseline.
- Test: real ECH config rejection produces retry or specific failure according to policy.
- Test: mutation self-check by dropping the hybrid group from profile; capture must show mismatch.
- Test: PQ unsupported by current build returns profile validation error before outbound connect.
- Verification command: Owner-machine packet capture because sandbox may not expose real network/ECH behavior.
- Exit gate: ECH/PQ profile cannot be marked production-ready without external capture evidence.

### Phase 6 — Production canary readiness

- Goal: turn sidecar from experimental to canary-eligible for one vendor/mode.
- Preconditions: Phases 1-5 complete for the selected profile.
- Preconditions: Go fallback policy defined and tested.
- Preconditions: sidecar process supervisor decision approved.
- Preconditions: logs and metrics reviewed for secret redaction.
- Preconditions: Owner legal/ToS acknowledgement for mimicry target remains outside this technical plan.
- Test: sidecar unavailable while configured as required returns fail-closed error and does not fall back silently.
- Test: sidecar unavailable while configured as audit-only uses uTLS or standard path only if config explicitly says so.
- Test: canary percentage of zero means no request reaches sidecar.
- Test: canary percentage nonzero only routes allowed provider/mode.
- Test: per-account proxy sidecar path uses proxy metadata or fails closed.
- Exit gate: release-readiness gate for clean-room, packet parity, security, and operations.

---

## §6 风险测试矩阵

| Risk ID | Dimension | Real defect guarded | Required test | Mutation self-check | Evidence expected |
|---|---|---|---|---|---|
| TLS-R1 | JA3 | Sidecar ignores cipher/group/extension order and still claims mimicry | Local capture compares observed JA3 to expected profile | Delete one profile field mapping; test must fail | pcap-derived JA3 diff |
| TLS-R2 | JA4 | JA3 passes but ALPN/extension count/order changes JA4 | Capture computes JA4 and compares expected | Swap ALPN order; test must fail | JA4 diff with profile ID |
| TLS-R3 | H2 settings values | H2 uses library defaults instead of target values | H2 fixture captures SETTINGS values | Remove configured initial window; test must fail | Raw SETTINGS frame decode |
| TLS-R4 | H2 settings order | H2 values match but order is wrong | Raw frame order assertion | Swap two IDs; test must fail | Ordered ID list |
| TLS-R5 | H2 pseudo-order | Header block order diverges from target client | H2 fixture decodes pseudo/header order | Use default pseudo-order; test must fail | Ordered header evidence |
| TLS-R6 | ECH GREASE | ECH GREASE flag ignored | Capture with flag on/off differs | Force flag off; test must fail | Extension presence evidence |
| TLS-R7 | Real ECH | ECH rejection handled as generic TLS failure | ECH test endpoint returns retry config; sidecar classifies retry/fail policy | Drop retry handling; test must fail | Specific ECH class |
| TLS-R8 | PQ | Hybrid group advertised incorrectly or silently omitted | Capture validates group/key-share profile | Remove hybrid group; test must fail | Supported group/key-share diff |
| IPC-R1 | IPC framing | Go and Rust disagree on body chunk boundaries | IPC contract test streams multi-chunk body | Collapse chunks into one without length; test must fail | Rust sees exact body bytes |
| IPC-R2 | Backpressure | Sidecar buffers entire streaming response | Large streaming fixture asserts bounded memory/stream chunks | Replace stream with full read; test must fail under memory/chunk assertion | Chunk timing + memory cap |
| IPC-R3 | Sidecar unavailable | Go silently falls back to weaker transport | Sidecar required config with dead socket returns fail-closed | Enable fallback in code; test must fail | Error class and no upstream hit |
| SEC-R1 | Account proxy | Proxy-bound account goes direct through sidecar | Proxy metadata or fail-closed test | Ignore proxy URL; test must fail | Proxy fixture sees connect or explicit error |
| SEC-R2 | Secrets | IPC/logs expose Authorization or cookies | Redaction test inspects logs and IPC debug dump | Log raw headers; test must fail | Redacted log snapshot |
| OPS-R1 | Observability | Operators cannot distinguish uTLS vs sidecar | Request metric/log includes backend/profile/result | Remove backend label; test must fail | metric/log assertion |
| OPS-R2 | Health | Dead sidecar causes request hangs | Deadline test with dead socket | Remove dial timeout; test must fail | bounded error latency |
| COMP-R1 | HTTP status | Sidecar response mapping corrupts status/headers | Local upstream returns status/trailer set; Go sees exact result | Drop trailer/header; test must fail | Go response assertion |
| CLEAN-R1 | Clean-room | wreq/h2 code copied into HUAKAI | Review scans for copied structures/names/source blocks | Introduce copied snippet in test branch; review flags | clean-room review finding |

### 6.1 Acceptance test rows

- AT-TLS-001: Given sidecar profile `mimicry_chatgpt`, when Go sends a streaming request through sidecar to a local TLS/H2 fixture, then packet capture must match expected JA3, JA4, ALPN, H2 SETTINGS values/order, and pseudo-order.
- AT-TLS-002: Given sidecar configured as required and the sidecar socket missing, when a mimicry request is dispatched, then Go returns a retryable transport failure and no weaker transport is used.
- AT-TLS-003: Given sidecar configured as optional/audit-only, when sidecar health is down, then behavior must match the explicitly configured fallback and log a downgrade event.
- AT-TLS-004: Given a proxy-bound account and sidecar mode, when proxy metadata support is enabled, then the fixture proxy must observe the connection before upstream TLS starts.
- AT-TLS-005: Given a proxy-bound account and sidecar mode before proxy metadata support, when dispatch starts, then the request must fail closed with proxy unsupported and not connect direct.
- AT-TLS-006: Given a profile with ECH GREASE enabled, when captured, then the ECH GREASE evidence must differ from the disabled baseline.
- AT-TLS-007: Given a profile with PQ group enabled, when captured, then supported group/key-share evidence must include the expected hybrid entry.
- AT-TLS-008: Given malformed IPC frames, when Rust receives them, then sidecar closes the IPC request and returns local protocol error without upstream connect.
- AT-TLS-009: Given sidecar returns upstream 401/429/5xx, when Go receives response, then existing HTTP classification and retry/failover paths remain unchanged.
- AT-TLS-010: Given sidecar transport class `tls_handshake_failed`, when Go classifies dispatch error, then it maps to the existing retry decision table. Evidence for table: `HUAKAI:backend/internal/gateway/attempt_error.go:15`, `HUAKAI:backend/internal/gateway/attempt_error.go:21`, `HUAKAI:backend/internal/gateway/attempt_error.go:119`, `HUAKAI:backend/internal/gateway/attempt_error.go:126`.
- AT-TLS-011: Given sidecar debug logging enabled, when request contains auth headers/cookies, then logs and IPC debug dumps redact those values.
- AT-TLS-012: Given canary percentage zero, when traffic arrives for an allowed mimicry mode, then no sidecar IPC connection is attempted.

### 6.2 Test quality requirements

- Every fingerprint test must prove discriminating power by comparing correct path to a deliberately damaged path.
- No fixture may pass only because the expected output is the same under correct and broken behavior.
- No “not equal to bad” assertion is enough; packet evidence must assert the exact expected observed fields.
- No test may skip when a critical profile field is zero; it must fail profile validation.
- No all-Allow gate chain may hide sidecar health failure, proxy failure, or policy failure.
- Tests that need real external network must be marked Owner-machine only, with local fixture equivalents for CI.
- Packet captures must not include real credentials.
- Keylog files must be generated only in explicit capture mode and must be deleted or stored under ignored temp paths.

---

## §7 D 决策点

### D1 — IPC protocol

| Option | Description | Reference comparison | Risk | Recommendation |
|---|---|---|---|---|
| A | Unix-domain socket with HUAKAI-owned framed request/response stream | Envoy AI Gateway keeps route/backend/filter config separate from implementation config, supporting explicit boundaries rather than hidden transport coupling. Evidence: `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:6`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:9`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:23`. | Requires custom framing tests; no extra Go dependency. | Recommended for Phase 1. |
| B | gRPC bidirectional stream over Unix socket | Current Rust exploratory gateway already has tonic/prost for control-plane RPC, but Go backend `go.mod` does not currently carry grpc-go; adding it is a new Go runtime dependency and needs Owner approval. HUAKAI Rust route proto exists already. Evidence: `HUAKAI:exploratory/rust-core-gateway/merged/proto/route.proto:5`, `HUAKAI:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:53`. | High dependency and operational surface in Go. | Defer until Owner explicitly wants grpc-go. |
| C | Raw bidirectional TCP-like stream | h2 and wreq evidence show H2 SETTINGS and pseudo-order are fingerprint surfaces, so raw Go-serialized HTTP bytes would not give Rust full H2 ownership. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1322`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:227`. | Fails core H2 fingerprint objective if Go owns H2 serialization. | Reject for L1 H2 profiles. |

Decision needed from Owner: approve Option A for Phase 1 unless the synthesized plan chooses gRPC and separately approves grpc-go dependency.

### D2 — Boring fork pinning

| Option | Description | Reference comparison | Risk | Recommendation |
|---|---|---|---|---|
| A | Pin to existing HUAKAI vendored Boring fork path | HUAKAI already records local Boring fork modifications and workspace patch redirection. Evidence: `HUAKAI:exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md:99`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:15`, `HUAKAI:exploratory/rust-core-gateway/merged/Cargo.toml:19`. | Local fork maintenance burden, but reproducible. | Recommended. |
| B | Floating upstream cloudflare/boring | Upstream builds/links BoringSSL and applies patches during build; floating makes packet behavior drift with upstream. Evidence: `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/README.md:5`, `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/build/main.rs:467`. | Fingerprint drift and non-reproducible captures. | Reject for production mimicry. |
| C | Fresh vendor from cloudflare/boring HEAD | Ref-anchor style recency is good for research, but execution should not throw away existing HUAKAI fork changes without audit. | Duplicates fork surfaces. | Only if existing fork cannot build. |

Decision needed from Owner: approve existing vendored fork as the Phase 1/2 pin, with a later fork-refresh plan if needed.

### D3 — wreq usage

| Option | Description | Reference comparison | Risk | Recommendation |
|---|---|---|---|---|
| A | Behavior-only reference, no vendor | wreq demonstrates explicit TLS/H2 profile fields and applies them to a BoringSSL-backed connector, but HUAKAI can independently model the same outcomes. Evidence: `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:69`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:129`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs:344`. | Requires HUAKAI implementation effort. | Required by Owner constraint. |
| B | Vendor wreq | Owner explicitly said no wreq vendor. | Violates directive and clean-room boundary. | Reject. |
| C | Optional plugin later | Possible only after license/dependency audit and if HUAKAI-owned implementation fails. | Adds product complexity. | Mandatory Roadmap only, not current slice. |

Decision status: Owner has already locked A.

### D4 — Phase 1 delivery length

| Option | Description | Reference comparison | Risk | Recommendation |
|---|---|---|---|---|
| A | 1 week skeleton, local mock only | Envoy-style explicit config separation suggests a small control boundary can be created first. Evidence: `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go:23`, `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go:245`. | May not include real capture. | Accept if Owner wants unblock planning fast. |
| B | 2 weeks skeleton + local TLS/H2 fixture | h2/wreq evidence shows H2 fingerprint needs real frame assertions, so fixture work materially lowers risk. Evidence: `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs:213`, `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs:227`. | More time, better test quality. | Recommended. |
| C | 3+ weeks Phase 1 includes JA3/H2 capture parity | Bigger slice crosses skeleton and fingerprint work. | Higher chance of mid-slice inconsistency. | Avoid; split into Phase 2/3. |

Decision needed from Owner: choose B if schedule permits; choose A only if sidecar skeleton is needed immediately for cross-team integration.

### D5 — Sidecar fallback semantics

| Option | Description | Reference comparison | Risk | Recommendation |
|---|---|---|---|---|
| A | Required sidecar fail-closed | HUAKAI transport policy already rejects missing/stub mimicry templates rather than silently falling back. Evidence: `HUAKAI:backend/internal/transport/factory.go:159`, `HUAKAI:backend/internal/transport/factory.go:173`, `HUAKAI:backend/internal/transport/factory.go:181`, `HUAKAI:backend/internal/transport/factory.go:188`. | More visible failures during rollout. | Recommended for production profile. |
| B | Optional sidecar fallback to uTLS | Current Stage 0 dual-track requires uTLS to remain available, but fallback must be explicit and observable. Evidence: `HUAKAI:backend/internal/transport/factory.go:167`, `HUAKAI:backend/internal/transport/factory.go:170`. | Hidden downgrade if not logged/tested. | Allow only audit/dev mode. |
| C | Silent fallback to standard | Violates mimicry and provider isolation expectations. | Anti-detection regression. | Reject. |

Decision needed from Owner: default Phase 1 to optional/audit-only; production profile must be required/fail-closed.

---

## §8 验证

### 8.1 Local CI verification

- `cd backend && go test ./internal/transport ./internal/provider`
- `cd backend && go test ./internal/gateway`
- `cd exploratory/rust-core-gateway/merged && cargo fmt --check`
- `cd exploratory/rust-core-gateway/merged && cargo test -p boring_fork_sidecar`
- `cd exploratory/rust-core-gateway/merged && cargo clippy -p boring_fork_sidecar -- -D warnings`
- `cd exploratory/rust-core-gateway/merged && cargo test -p core_gateway --features mimicry-boring --lib`

### 8.2 Packet verification

- Use Wireshark/tshark on loopback or a controlled network namespace.
- Capture sidecar traffic to local TLS fixture for deterministic CI.
- Capture sidecar traffic to Owner-approved real endpoints only on Owner machine.
- Store captures under ignored temp paths.
- Generate SSL key log only when capture mode is explicitly enabled.
- Compare observed JA3 to profile expected JA3.
- Compare observed JA4 to profile expected JA4.
- Compare ALPN protocol list and selected protocol.
- Compare TLS extension IDs and order.
- Compare supported groups and key-share groups.
- Compare ECH GREASE/real ECH evidence.
- Compare PQ group/key-share evidence.
- Compare H2 initial SETTINGS values.
- Compare H2 initial SETTINGS order.
- Compare H2 pseudo-header order where fixture can decode it.
- Compare stream dependency/priority fields where profile requires them.

### 8.3 Real-client baselines

- Baseline target A: real Anthropic CLI / Claude Code client capture, Owner-approved.
- Baseline target B: real Cursor client capture, Owner-approved.
- Baseline target C: real Copilot client capture, Owner-approved.
- Baseline target D: real Codex CLI / ChatGPT path capture if still in scope from 2026-05-19 mimicry verification.
- Each baseline must record capture date, client version, operating system, target host, and sanitized profile ID.
- Baselines older than 30 days must be re-captured before being used as “latest” evidence.

### 8.4 Release verification

- Run clean-room review for the plan and later patches.
- Run dependency license audit if any new Rust/Go runtime dependency is added.
- Run `codex exec review --uncommitted --full-auto` before any commit once implementation exists.
- Escalate to full reviewer-lane if schema, auth, billing, quota, or deployment scripts are touched.
- Confirm no new files under frozen packages.
- Confirm all sidecar flags default disabled.
- Confirm sidecar profile selection is visible in logs/metrics.
- Confirm sidecar unavailability does not silently downgrade production mimicry.

---

## §9 Source files

### 9.1 HUAKAI docs read

- `docs/process/2026-05-24-ref-anchor.md`
- `docs/process/plans/2026-05-24-decisions-locked.md`
- `exploratory/rust-core-gateway/README.md`
- `exploratory/rust-core-gateway/merged/README.md`
- `exploratory/rust-core-gateway/merged/READINESS.md`
- `exploratory/rust-core-gateway/vendor/boring/MODIFICATIONS.md`

### 9.2 HUAKAI source read

- `backend/internal/transport/factory.go`
- `backend/internal/transport/policy.go`
- `backend/internal/transport/mimicry/template.go`
- `backend/internal/transport/mimicry/utls_dialer.go`
- `backend/internal/gateway/upstream_dispatcher.go`
- `backend/internal/gateway/attempt_error.go`
- `backend/internal/provider/proxy_resolver.go`
- `exploratory/rust-core-gateway/merged/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/error.rs`
- `exploratory/rust-core-gateway/merged/proto/route.proto`

### 9.3 Reference source read

- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:README.md`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/README.md`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/build/main.rs`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:tokio-boring/src/lib.rs`
- `cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:hyper-boring/src/v1.rs`
- `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs`
- `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/proto/settings.rs`
- `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/frame/settings.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/lib.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn/ext.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls/conn/service.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:examples/emulate.rs`
- `0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:tests/emulate.rs`
- `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/ai_gateway_route.go`
- `envoyproxy/ai-gateway@3d3d346d09e4:internal/controller/backend_security_policy.go`
- `envoyproxy/ai-gateway@3d3d346d09e4:internal/backendauth/auth.go`
- `envoyproxy/ai-gateway@3d3d346d09e4:internal/filterapi/filterconfig.go`

### 9.4 Source coverage proof

- `docs/process/2026-05-24-ref-anchor.md` contributed latest SHA/owner expectations for same-day plan evidence.
- `docs/process/plans/2026-05-24-decisions-locked.md` contributed STAGE 0 and AS-D1 blocking context.
- `backend/internal/transport/factory.go` contributed current transport backend injection and fail-closed template behavior.
- `backend/internal/transport/policy.go` contributed provider × mode gating and cross-vendor rejection behavior.
- `backend/internal/transport/mimicry/template.go` contributed current uTLS profile schema and validation limits.
- `backend/internal/transport/mimicry/utls_dialer.go` contributed current Go uTLS handshake and generic-extension limitation.
- `backend/internal/gateway/upstream_dispatcher.go` contributed production outbound assembly and proxy application point.
- `backend/internal/provider/proxy_resolver.go` contributed account proxy fail-closed behavior for non-standard transports.
- `backend/internal/gateway/attempt_error.go` contributed shared transport error class mapping for sidecar errors.
- `exploratory/rust-core-gateway/*` contributed current Rust exploratory status, workspace state, and local Boring fork state.
- `cloudflare/boring` source contributed Rust/BoringSSL binding capability, async handshake, ALPN, keylog, GREASE, permutation, ECH, and PQ evidence.
- `hyperium/h2` source contributed H2 value knobs and the fixed settings encoding limitation.
- `0x676e67/wreq` source contributed behavior evidence for explicit TLS/H2 profile fields and capture-driven emulation.
- `envoyproxy/ai-gateway` source contributed decision comparison evidence for explicit backend/config separation.

---

## §10 Lane + UTC

Source files read: see §9.1, §9.2, and §9.3.

Lane: specifier.

Agent: Codex GPT-5 lane.

UTC timestamp: 2026-05-24T08:36:04Z.

No raw upstream code was copied.

No wreq source was vendored.

No Go cgo path is proposed.

No `LICENSE` change is proposed.

No schema/auth/billing/quota/deployment change is proposed.

No file in `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto` is proposed as a new file.

Chinese Owner summary: 本计划基于实际读取的 HUAKAI transport/gateway/proxy/Rust workspace 文件和 boring/h2/wreq/envoy-ai-gateway 参考源码，真实观察包括当前 Go `RoundTripper` 选择、provider×mode fail-closed、uTLS 模板边界、Rust exploratory 非生产状态、BoringSSL Rust 绑定能力、h2 固定 SETTINGS 编码顺序、wreq 的显式 TLS/H2 profile 模型；合理推断包括 sidecar 必须拥有 H2 序列化、Unix socket framed IPC 优先、proxy metadata 必须显式传递、Phase 1/2/3 拆分降低风险；Open questions 共 9 个，主要是 Owner 是否批准 Unix socket framed IPC、是否锁现有 vendored Boring fork、Phase 1 用 1 周还是 2 周、何时允许真实客户端抓包、以及生产 fallback/canary 策略。
