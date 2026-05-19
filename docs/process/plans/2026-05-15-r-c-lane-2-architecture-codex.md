# 2026-05-15 R-C Lane 2 transport backend architecture - Codex

| Field | Value |
| --- | --- |
| Lane | SPECIFIER |
| Scope | Architectural recommendation only; no production code, no schema/auth/billing/LICENSE change |
| Clean-room boundary | Read HUAKAI internal code and official crate/repo docs only; did not read sub2api/new-api/portkey/helicone/litellm/all-api-hub source |
| Owner directive | "R-C Phase Lane 2 架构方案独立起草" |
| Timestamp | 2026-05-15T03:02:43Z |
| Observed evidence | HUAKAI internal files plus official wreq/boring/h2/http2/rustls/rust-openssl repos and crate metadata |
| Inference policy | Recommendation/trade-off statements are marked as recommendation or inference from cited evidence |

## 0. Decision summary

| Decision | Recommendation | Blocking rule |
| --- | --- | --- |
| D1 transport backend | Choose **(c) native OpenSSL + HUAKAI profile-scoped ClientHello adapter**, paired with the MIT `http2` fork for HTTP/2 SETTINGS and pseudo-header order. Treat (b) HUAKAI BoringSSL patch as fallback if OpenSSL cannot hit exact capture. Do not choose (a) as the exact production backend; keep it as diagnostic/KnownGap fallback only. Keep (d) raw ClientHello + h2 builder as Mandatory Roadmap / experimental escalation, not R-C default. | Exact local capture PASS is required before any profile can be connected to `ProxyEngine`. |
| D2 HUAKAI patch allowance | Allow a narrow HUAKAI-maintained patch/adapter **only for transport-expression gaps** and only behind feature flag, capture gate, and dependency audit. | Patch must not copy GPL/AGPL/LGPL source or non-MIT reference implementation details. |
| D3 KnownGap merge policy | Allow KnownGap plumbing to merge only as `KnownGapBlocked`: profiles, tests, docs, local capture diff, and operator visibility may merge; production dispatch through `ProxyEngine` must stay blocked until exact capture PASS. | KnownGap must never silently route real upstream traffic. |

The three Owner decisions are not perfectly orthogonal. D1 option (a) explicitly accepts partial known gaps, while D3 "exact capture PASS before ProxyEngine" rejects routing with known gaps. The coherent combination is: **D1=(c)+`http2` fork, D2=yes with narrow patch policy, D3=KnownGap allowed only as blocked diagnostics**.

## 1. Reference freshness and license check

First reference to each official crate/repo below records `pushed_at`, HEAD SHA, and latest commit message as required. These are MIT/Apache-family official projects, not the restricted non-MIT reference projects. All `pushed_at` values are within 90 days of 2026-05-15.

| Project | pushed_at | HEAD SHA | HEAD commit message | License evidence | Use in this lane |
| --- | --- | --- | --- | --- | --- |
| `0x676e67/wreq` | 2026-05-11T11:20:14Z | `68c4a8868a64a79c43554d16e890b2f2a9f69a4d` | `chore(proxy): fmt tests` | Apache-2.0 in `Cargo.toml` [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:Cargo.toml:11] | Candidate (a), diagnostic/KnownGap fallback |
| `cloudflare/boring` | 2026-05-06T14:04:51Z | `3921f35aa406c4cbff6efca9688f1fc9ead2508f` | `Expose DTLS version constants in SslVersion` | `boring` Apache-2.0 [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/Cargo.toml:5], sys crate MIT [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring-sys/Cargo.toml:7] | Candidate (b), patch fallback |
| `hyperium/h2` | 2026-05-12T16:28:39Z | `d361b75762868f51fb85e39e0a6c3c79958b42ea` | `fix: Reject frames on streams whose HEADERS haven't been sent (#899)` | MIT [hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:Cargo.toml:7] | Baseline H2 comparison |
| `0x676e67/http2` | 2026-05-12T22:59:44Z | `a33b27e469434a99105f35670c9970f22112e892` | `Merge remote-tracking branch 'upstream/master'` | MIT [0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:Cargo.toml:7] | Recommended H2 order backend |
| `rustls/rustls` | 2026-05-14T14:03:00Z | `cb9a749ec8476cd0154468b247038451fb86f4a2` | `docs: fix duplicated "the" in fips manual page` | Apache-2.0 OR ISC OR MIT [rustls/rustls@cb9a749ec8476cd0154468b247038451fb86f4a2:rustls/Cargo.toml:6] | Rejected as exact Codex mimicry backend; still current default stack context |
| `rust-openssl/rust-openssl` | 2026-05-10T14:41:05Z | `b460eb378c335610df5395a251408ad70bb60d42` | `Prefer Homebrew openssl@4 and stop looking for openssl@1.1 (#2633)` | Apache-2.0 [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/Cargo.toml:5] | Recommended TLS exact candidate |

No GPL/AGPL direct main dependency was found for the recommended OpenSSL + `http2` path. A separate utility-package hazard exists: `wreq-util` stable `2.2.6` metadata reports GPL-3.0, its newest pre-release `3.0.0-rc.10` metadata reports LGPL-3.0, and `rquest-util` metadata reports GPL-3.0. Because wreq's README points emulation profiles toward the utility package family [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:69], HUAKAI should **REJECT utility preset packages** from the production dependency path and use only audited direct crates plus HUAKAI-owned templates.

## 2. HUAKAI constraints observed

The existing R-C/R-D/R-E plan says the wreq/rquest spike cannot be the sole exact layer because Codex capture has fields the safe public APIs did not reproduce: cipher `52394`, extension `22`, group `4588`, EC point formats `[0,1,2]`, signature algorithm breadth, and partial H2/HTTP/1 order gaps [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:38]. The same plan already frames R-C as a backend decision and explicit spike-to-implementation phase, not a feature-parity debate [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:52].

The Rust merged core currently uses `hyper-rustls` as its normal client stack [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:23] and builds one reusable `GatewayHttpClient` through a `hyper_rustls` connector [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:4]. `ProxyEngine` holds that single client [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:55] and dispatches upstream requests through it [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:238]. Therefore R-C must insert a transport selection seam before real routing; it cannot just change template parsing.

The Codex built-in profile is currently modeled as `KnownGapBlocked` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:136], and its local tests assert the exact gap fields as expected blockers [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs:154]. The template itself records cipher `52394` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:37], extension `22` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:66], group `4588` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:88], 26 signature algorithm values [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:97], and EC point formats `[0,1,2]` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:154].

The Go-side Phase A transport proves the product requirement is not just theoretical: it already has a template-driven mimicry transport with per-mode policy, fail-closed validation, and a custom ClientHello expression path [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:backend/internal/transport/factory.go:73], [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:backend/internal/transport/policy.go:94], [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:backend/internal/transport/mimicry/utls_dialer.go:59]. Rust R-C should preserve that behavior class while moving to a cleaner Rust structure, per Owner's fixed direction.

## 3. D1 backend candidates

### 3.1 Candidate (a): wreq + MIT `http2` fork

**Recommendation:** do not make this the exact production backend for Codex. It is acceptable as a diagnostic/KnownGap fallback and may accelerate capture harness work.

Evidence for value: wreq explicitly targets high-fidelity protocol matching and names TLS/JA3/JA4/HTTP2 customization as a goal [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:14]. It uses BoringSSL for HTTPS and HTTP/2-over-TLS parity [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:26]. Its TLS options expose ALPN, GREASE, extension permutation, key shares, curves, signature algorithms, and cipher list fields [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:130], [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:189], [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:251]. It depends on the MIT `http2` fork [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:Cargo.toml:86].

Evidence for limitation: HUAKAI's own spike already concluded that wreq/BoringSSL public APIs did not reproduce the Codex-specific gap fields exactly [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:38]. The package's emulation-profile story also points at a utility package path [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:README.md:69], which is a license hazard for HUAKAI because local metadata for that utility family showed LGPL/GPL licensing. That does not reject `wreq` itself; it rejects importing preset utilities.

Trade-off: low time cost, good HTTP/2 ergonomics, acceptable license for the direct crate, but insufficient exactness for Codex and a utility-package license trap. Under D3, this route cannot reach production dispatch unless the local capture proves exact, so selecting it as D1 would conflict with the capture gate.

### 3.2 Candidate (b): HUAKAI-owned BoringSSL patch

**Recommendation:** keep as fallback after OpenSSL, not first choice.

Evidence for value: the Boring binding exposes cipher list selection for pre-TLS1.3 [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1454], GREASE and extension permutation controls [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1980], verification algorithm preferences [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1992], supported curve list control [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:2020], and per-connection ALPN/extension permutation controls [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:3098].

Evidence for limitation: the binding comments state BoringSSL does not implement the TLS1.3 ciphersuite setter used in OpenSSL [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1480]. Its ClientHello extension getter is inspection-oriented around a received ClientHello, not an arbitrary outbound-client extension construction API [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:2418].

Trade-off: best long-term control if HUAKAI owns the patch, BoringSSL-class behavior is close to wreq's backend, and Apache/MIT licensing is acceptable for the binding. The cost is a crypto-adjacent patch maintenance burden, upstream rebase work, and security update responsibility. This should be a planned fallback only if OpenSSL exact adapter cannot express the target without worse risk.

### 3.3 Candidate (c): native OpenSSL + HUAKAI ClientHello adapter

**Recommendation:** choose this as D1 default.

Evidence for fit: the Codex template declares a native-tls/OpenSSL backend class [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/codex-cli.json:15]. The OpenSSL Rust binding exposes separate controls for legacy cipher list and TLS1.3 ciphersuites [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1068], ALPN protocols [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1218], custom TLS extensions for client/server contexts [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1603], signature algorithm preferences [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1705], and group list control [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1718].

Evidence for caveat: OpenSSL also exposes a ClientHello callback for received ClientHello inspection on the server side [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1672], so outbound exactness must be proven by local capture, not assumed from API names. HUAKAI's current Rust client is `hyper-rustls`, so OpenSSL integration is a new runtime dependency and triggers release-gate scrutiny [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/15_RELEASE_GATES.md:46].

Trade-off: best near-term path to exact Codex fields with official public APIs; lower patch burden than BoringSSL; cleaner than raw TLS. Risks are OpenSSL version drift, platform packaging differences, and needing a small adapter around profile fields plus local capture parser. The mitigation is to pin the OpenSSL build surface for exact profiles and run capture tests on every dependency upgrade.

### 3.4 Candidate (d): HUAKAI raw ClientHello + h2 builder

**Recommendation:** do not select for R-C Lane 2 default; put behind Mandatory Roadmap / experimental module if (c) and (b) fail exact capture.

Evidence for why it is attractive: HUAKAI needs exact expression of fields that current safe public APIs failed to reproduce [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:38]. The `http2` fork can preserve SETTINGS order and pseudo-header order [0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1208], [0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/frame/settings.rs:471], [0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/frame/headers.rs:826], so a raw TLS layer plus h2 builder would maximize control.

Evidence for why it is too large: the default `h2` crate controls settings values but not the same order surfaces [hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:699], [hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1077], [hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1625]. The TLS half would become HUAKAI-owned protocol construction rather than a crate adapter, increasing security and maintenance risk beyond this lane.

Trade-off: maximum exactness and independence, but highest security review burden, longest schedule, and largest chance of creating a bespoke TLS implementation that must be audited like security-critical infrastructure. This option should require Owner sign-off as a separate work unit.

## 4. Fusion-upgrade mapping

This table uses only official crate/repo evidence. Restricted non-MIT project source was not read, so it is intentionally absent.

| feature | upstream A cite | upstream B cite | HUAKAI delta | dimension(s) |
| --- | --- | --- | --- | --- |
| TLS profile knobs for JA3/JA4-like shape | wreq TLS options expose protocol-shape knobs [0x676e67/wreq@68c4a8868a64a79c43554d16e890b2f2a9f69a4d:src/tls.rs:130] | Boring exposes GREASE/permutation and curve/signature controls [cloudflare/boring@3921f35aa406c4cbff6efca9688f1fc9ead2508f:boring/src/ssl/mod.rs:1980] | HUAKAI keeps profiles as data and only enables dispatch after capture PASS | architecture, operations |
| Custom extension expression | OpenSSL exposes custom extension hooks [rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42:openssl/src/ssl/mod.rs:1603] | rustls manual says arbitrary TLS extensions are not supported there [rustls/rustls@cb9a749ec8476cd0154468b247038451fb86f4a2:rustls/src/manual/features.rs:68] | Prefer OpenSSL exact adapter over rustls for Codex exact backend | architecture, algorithm |
| HTTP/2 order mimicry | `http2` fork exposes SETTINGS and pseudo-header order setters [0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892:src/client.rs:1208] | upstream `h2` exposes value controls but internally builds pseudo headers [hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea:src/client.rs:1625] | Use MIT `http2` fork only behind dependency audit and capture tests | ecosystem, algorithm |
| Profile gap state | HUAKAI Codex profile is `KnownGapBlocked` [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:136] | R-C plan says exact capture is a success criterion [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:103] | KnownGap may merge as visible blocked state, not production routing | operations, safety |
| Existing transport injection point | Rust `ProxyEngine` currently owns one HTTP client [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs:55] | Go transport factory already selects by mode and fails closed [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:backend/internal/transport/factory.go:73] | Rust should add explicit transport selection without piling features into profile parsing | architecture |

## 5. D2 patch policy

Allowing a HUAKAI patch is the right decision only if the scope is narrow:

| Patch rule | Recommendation |
| --- | --- |
| Location | Isolate in a transport-exact crate/module, not in `ProxyEngine` business logic. |
| Enablement | Compile and runtime feature flag, disabled by default until local capture PASS. |
| Patch purpose | Protocol expression only: cipher/extension/group/signature/ALPN/order surfaces needed by templates. |
| Forbidden | No copied non-MIT source; no GPL/LGPL utility presets; no auth/billing/schema coupling; no real secret material in fixtures. |
| Review gate | Dependency license audit, local capture diff, and per-commit Codex review before landing. |
| Upgrade rule | Any OpenSSL/BoringSSL/http2 upgrade reruns exact capture and records upstream HEAD metadata. |

This keeps Owner's "Rust 代码结构清晰禁止堆功能" constraint intact: the patch is a small transport backend boundary, not a grab-bag of mimicry features.

## 6. D3 KnownGap policy

KnownGap should be allowed to merge in R-C because the repo already models it explicitly and tests it [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs:129]. However, KnownGap must stay operationally blocked:

| Allowed under KnownGap | Not allowed under KnownGap |
| --- | --- |
| Loading profile templates and surfacing known gaps | Dispatching real upstream traffic through `ProxyEngine` |
| Local capture tests and field-by-field diffs | Claiming `Implemented` parity |
| UI/docs/operator status as blocked | Falling back silently to `hyper-rustls` for a mimicry profile |
| Roadmap item for exact backend | Treating a partial fingerprint as stable/strong mimicry |

The release gates already say hidden gaps are not allowed [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/15_RELEASE_GATES.md:26], and feature preservation requires risky features to be converted to Safe Equivalent / Feature Flag / Mandatory Roadmap rather than removed [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/15_RELEASE_GATES.md:48]. `KnownGapBlocked` is the correct safe equivalent until exact capture passes.

## 7. Anthropic pending-backfill recommendation

`anthropic-claude-code.json` is under `_pending-backfill/`, not top-level production templates [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json:1]. Rust tests require non-pending top-level templates to map to built-in profiles while intentionally excluding `_pending-backfill` from that production coverage check [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs:60]. The Go policy also keeps Anthropic mimicry present but says callers should not enable it while paused [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:backend/internal/transport/policy.go:97].

Recommendation: treat Anthropic recapture/backfill as **Lane 2b**, a sibling data-backfill atom that shares the capture harness but does not block the D1/D2/D3 backend decision for Codex/Kiro/Gemini. It must block only Anthropic mimicry enablement. Promotion criteria are: Owner-provided real capture, redaction/secret scan, top-level template promotion, built-in profile mapping, and exact/known-gap policy update.

## 8. Atomic Lane 2 execution plan

Each atom is intentionally scoped under roughly 100 LoC of production change. Test fixtures/docs may be separate, but each implementation step should remain reviewable.

| Atom | Work | Verification gate | Parallelism |
| --- | --- | --- | --- |
| L2-A0 | Record dependency metadata, license posture, and selected D1/D2/D3 outcome. | No GPL/AGPL/LGPL runtime utility dependency; Owner approves decision. | Can run first. |
| L2-A1 | Add transport backend selection model around existing profile state, without routing. | Unit test: exact/known-gap profiles map to backend intent; no `ProxyEngine` dispatch change. | After A0. |
| L2-A2 | Add test-only local TLS ClientHello capture helper. | Captures current `hyper-rustls` baseline locally; no secrets in fixture. | Parallel with A1. |
| L2-A3 | Add capture diff normalizer for cipher/extensions/groups/signatures/EC points/ALPN. | Fixture test asserts template fields are compared positively, not just "not bad". | After A2. |
| L2-A4 | Add OpenSSL exact adapter skeleton behind feature flag, no production routing. | Build/test passes with feature; disabled default path unchanged. | After A1/A2. |
| L2-A5 | Implement one OpenSSL field family at a time. | After each family, local capture diff shrinks or records explicit blocker. | Serial within OpenSSL adapter. |
| L2-A6 | Add MIT `http2` fork adapter for H2 SETTINGS/pseudo order, behind feature flag. | Local H2 capture or encoder test proves order, not just values. | Parallel with A4/A5. |
| L2-A7 | Enforce `KnownGapBlocked` at dispatch boundary. | Test proves KnownGap profile cannot create production transport client. | After A1, before A9. |
| L2-A8 | Add non-mimicry fallback path preserving current `hyper-rustls` behavior. | Existing Rust tests still pass; no silent mimicry fallback. | After A1. |
| L2-A9 | Connect exact backend to `ProxyEngine` only after exact local capture PASS. | Profile-specific exact capture gate green; KnownGap still blocked. | After A5/A6/A7. |
| L2-A10 | Prepare Owner recapture artifact template for R-D real upstream validation. | Redaction checklist and artifact schema pass tests/docs review. | Parallel after A2/A3. |

This plan keeps R-C as transport backend spike-to-implementation plus local capture tests. R-D remains the Owner real upstream capture gate described by the existing phase plan [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md:135].

## 9. Risk register recommendations

Keep `R-SEC-002` active because route planning carries acquisition and upstream auth material over control-plane transport [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/10_RISK_REGISTER.md:24], and the Rust proto still includes acquisition and upstream auth fields in route planning/reporting structures [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/proto/route.proto:40], [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:exploratory/rust-core-gateway/merged/proto/route.proto:77].

Add the following new risk entries before implementation:

| Risk ID | Risk | Severity | Mitigation |
| --- | --- | --- | --- |
| R-TRANSPORT-001 | Exact TLS mimicry may require custom/unsafe adapter or crypto-library patch, creating detection and security maintenance risk. | HIGH | Feature flag, local capture gate, Owner real capture gate, isolated transport module, no production dispatch until PASS. |
| R-LIC-003 | Utility/preset packages around browser/TLS mimicry may carry LGPL/GPL terms even when the core crate is Apache/MIT. | HIGH | Reject `wreq-util`/`rquest-util` production use; dependency license audit before adding runtime deps. |
| R-REL-002 | OpenSSL/Node/OpenSSL-class fingerprints can drift across OS, package, or upstream library versions. | MED | Pin build/runtime OpenSSL surface for exact profiles; record version in capture artifact; recapture on upgrade. |
| R-TEST-001 | Local capture can pass while real provider/WAF behavior differs because of network path, proxy, ALPN, or endpoint variance. | MED | R-D Owner real upstream gate; no release based on local-only evidence. |
| R-MAINT-001 | HUAKAI-owned BoringSSL/OpenSSL patch may create long-term rebase and CVE response burden. | MED | Patch ledger, minimal patch scope, upstream HEAD verification, capture tests on every rebase. |

No existing risk should be downgraded. The risk register rule already says license/security risk may change implementation method but must not silently drop a feature [HUAKAI@7a88d9e0a0c5d4b73de8d6dc471bccd813e9560a:docs/10_RISK_REGISTER.md:7].

## 10. Time estimate and lane parallelism

| Path | Estimate | Notes |
| --- | --- | --- |
| D1=(c) OpenSSL + `http2` fork | 3-5 engineering days | Fits R-C Lane 2 if atoms stay small and capture parser is test-only first. |
| Escalate to (b) BoringSSL patch | Add 3-7 engineering days | Requires patch design, upstream rebase plan, and extra security/license review. |
| Escalate to (d) raw ClientHello + h2 builder | Add 2-4 weeks minimum | Treat as separate Owner-approved work unit, not Lane 2 default. |
| Anthropic Lane 2b recapture | 0.5-1 day of Owner-assisted capture plus test/docs update | Parallel with backend work, but blocks only Anthropic mimicry enablement. |

Parallelizable tracks:

| Track | Can run in parallel with | Stop condition |
| --- | --- | --- |
| Dependency/license audit | Capture harness | GPL/AGPL main dependency, or LGPL/GPL utility dependency proposed for runtime. |
| Local capture parser | Backend adapter selection | Parser cannot distinguish expected-good from known-bad fields. |
| HTTP/2 order adapter | OpenSSL TLS adapter | `http2` fork audit fails or exact order cannot be verified locally. |
| Anthropic recapture | Codex/Kiro/Gemini backend work | Real capture unavailable or secret redaction cannot be proven. |

## 11. Open questions for Owner

1. Confirm D1 as OpenSSL exact adapter plus MIT `http2` fork, with BoringSSL patch as fallback.
2. Confirm D2 narrow patch policy and whether OpenSSL vendoring/pinning is acceptable.
3. Confirm D3 KnownGapBlocked policy: merge diagnostics, block production dispatch.
4. Confirm Anthropic `_pending-backfill` as Lane 2b rather than a blocker for Codex/Kiro/Gemini transport.

## 12. Source coverage proof

Observed source regions supported the following claims:

| Region | Contribution |
| --- | --- |
| R-C plan | Existing phase scope, spike gaps, D1/D2/D3 decision framing, exact capture gate. |
| Rust `core_gateway` transport/proxy code | Current hyper-rustls client shape and ProxyEngine dispatch point. |
| Rust mimicry profiles/tests/templates | KnownGapBlocked state, exact Codex fields, pending-backfill behavior. |
| Go transport mimicry code | Existing product behavior class for mode-based mimicry and fail-closed policy. |
| Template schema | Redaction, source tracking, real-vs-stub requirements. |
| wreq/boring/http2/h2/rustls/rust-openssl official repos | Backend capability, public API limits, license and freshness evidence. |

No restricted reference project source was read. No raw upstream code block was copied into this artifact. No implementation code was written.

Source files read:

- `docs/process/plans/2026-05-14-r3-on-merged-closure-codex.md`
- `docs/10_RISK_REGISTER.md`
- `docs/12_AGENT_WORKFLOW.md`
- `docs/15_RELEASE_GATES.md`
- `exploratory/rust-core-gateway/merged/READINESS.md`
- `exploratory/rust-core-gateway/merged/proto/route.proto`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http_profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/account_planner.rs`
- `backend/internal/transport/factory.go`
- `backend/internal/transport/policy.go`
- `backend/internal/transport/mimicry/utls_dialer.go`
- `backend/internal/transport/mimicry/registry.go`
- `backend/internal/transport/mimicry/template.go`
- `backend/cmd/gateway/main.go`
- `tools/fingerprint-collector/templates/SCHEMA.md`
- `tools/fingerprint-collector/templates/codex-cli.json`
- `tools/fingerprint-collector/templates/kiro-cli.json`
- `tools/fingerprint-collector/templates/gemini-advanced.json`
- `tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json`
- `/home/codex/refs/wreq/README.md`
- `/home/codex/refs/wreq/Cargo.toml`
- `/home/codex/refs/wreq/src/tls.rs`
- `/home/codex/refs/wreq/src/client.rs`
- `/home/codex/refs/wreq/src/client/emulate.rs`
- `/home/codex/refs/boring/Cargo.toml`
- `/home/codex/refs/boring/boring/Cargo.toml`
- `/home/codex/refs/boring/boring-sys/Cargo.toml`
- `/home/codex/refs/boring/tokio-boring/Cargo.toml`
- `/home/codex/refs/boring/boring/src/ssl/mod.rs`
- `/home/codex/refs/h2/Cargo.toml`
- `/home/codex/refs/h2/src/client.rs`
- `/home/codex/refs/http2/Cargo.toml`
- `/home/codex/refs/http2/src/client.rs`
- `/home/codex/refs/http2/src/frame/settings.rs`
- `/home/codex/refs/http2/src/frame/headers.rs`
- `/home/codex/refs/rustls/rustls/Cargo.toml`
- `/home/codex/refs/rustls/rustls/src/client/config.rs`
- `/home/codex/refs/rustls/rustls/src/client/hs.rs`
- `/home/codex/refs/rustls/rustls/src/crypto/mod.rs`
- `/home/codex/refs/rustls/rustls/src/manual/features.rs`
- `/home/codex/refs/rust-openssl/openssl/Cargo.toml`
- `/home/codex/refs/rust-openssl/openssl/src/ssl/mod.rs`
- `/home/codex/refs/rust-openssl/openssl-sys/src/handwritten/ssl.rs`
- `/home/codex/refs/rust-openssl/openssl-sys/src/ssl.rs`

Lane: specifier

Agent: Codex GPT-5

UTC timestamp: 2026-05-15T03:02:43Z
