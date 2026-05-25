# 2026-05-15 R-C Lane 2 L2-A0 依赖与许可审计 - Codex

| Field | Value |
| --- | --- |
| Lane | SPECIFIER |
| Work unit | L2-A0 dependency/license audit + risk register |
| Scope | 只读 HUAKAI 内部、crates.io/Cargo 官方 metadata、已 clone 的官方 MIT/Apache 仓库；只改文档，不改 `Cargo.toml`、`src/`、测试或生产代码 |
| Clean-room boundary | 未读 sub2api/new-api/portkey/helicone/litellm/all-api-hub 源码 |
| Owner decision basis | Owner 2026-05-15 对 R-C Lane 2 D1/D2/D3 的确认：`对的，同意` |
| Existing plan basis | `docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md` 记录 D1=(c) native OpenSSL + HUAKAI adapter + MIT `http2` fork，D2=窄 patch 允许，D3=KnownGapBlocked 严格派 |
| UTC timestamp | 2026-05-15T03:22:18Z |

## 1. Owner-confirmed baseline

本 atom 只锁定拟采用 baseline，不落地依赖。L2-A4 才允许修改 `Cargo.toml`，且必须重新跑 license audit 和 capture gate。

| Decision | Owner-confirmed baseline | L2-A0 audit verdict |
| --- | --- | --- |
| D1 | 选 (c) native OpenSSL + HUAKAI profile-scoped ClientHello adapter + MIT `http2` fork | 可作为 Owner-approved baseline；`openssl` crate 当前稳定版为 `0.10.79`，`http2` fork HEAD 为 `a33b27e469434a99105f35670c9970f22112e892` |
| D2 | 允许窄范围 HUAKAI patch，必须 feature flag + capture gate + audit | 可进入后续 atom；patch 只能用于 transport-expression gap，不能带入 GPL/LGPL utility preset |
| D3 | KnownGapBlocked 严格派：plumbing 可 merge，生产 dispatch 须 exact PASS | 保持阻塞；Codex profile 仍按 KnownGapBlocked 处理，不能接 `ProxyEngine` 生产 dispatch |
| Anthropic | Lane 2b 并行 | 不阻塞 Codex/Kiro/Gemini L2-A0；Anthropic backend 处方在 Lane 2b real capture 后再定 |

## 2. Version lock baseline

### 2.1 拟采用或继续保留的运行时依赖

| crate | version | license | source SHA / checksum | reason for inclusion | L2-A0 disposition |
| --- | --- | --- | --- | --- | --- |
| `openssl` | `0.10.79` | Apache-2.0 | crates.io checksum `bf0b434746ee2832f4f0baf10137e1cabb18cbe6912c69e2e33263c45250f542`; local repo HEAD `rust-openssl/rust-openssl@b460eb378c335610df5395a251408ad70bb60d42` | Codex exact TLS candidate; template declares `native-tls/openssl`; official crate exposes `vendored` feature and OpenSSL binding surface | Baseline approve for L2-A4, but not added now |
| `openssl-sys` | `0.9.115` | MIT | crates.io checksum `158fe5b292746440aa6e7a7e690e55aeb72d41505e2804c23c6973ad0e9c9781`; same local repo HEAD `b460eb378c335610df5395a251408ad70bb60d42` | Transitive FFI binding used by `openssl` | Baseline approve if pulled by `openssl` |
| `openssl-src` | `300.6.0+3.6.2` current max stable, only if `vendored` resolves today | MIT/Apache-2.0 | crates.io checksum `a8e8cbfd3a4a8c8f089147fd7aaa33cf8c7450c4d09f8f80698a0cf093abeff4`; repository `alexcrichton/openssl-src-rs` | Static/vendored OpenSSL option through `openssl-sys` optional `vendored` feature | Allowed only if L2-A4 chooses `vendored`; must be locked by actual `Cargo.lock` then |
| `http2` | `0.5.17` from `0x676e67/http2` repo | MIT | local repo HEAD `0x676e67/http2@a33b27e469434a99105f35670c9970f22112e892` | MIT fork planned for HTTP/2 SETTINGS/pseudo-header order control | Baseline approve behind feature flag/capture tests |
| `hyper` | `1.9.0` | MIT | current `Cargo.lock` checksum `6299f016b246a94207e63da54dbe807655bf9e00044f73ded42c3ac5305fbcca` | Existing client stack; remains transport chassis/non-mimicry path | Keep existing |
| `hyper-util` | `0.1.20` | MIT | current `Cargo.lock` checksum `96547c2556ec9d12fb1578c4eaf448b04993e7fb79cbaad930a656880a6bdfa0` | Existing legacy client connector utilities | Keep existing |
| `http` | `1.4.0` | MIT OR Apache-2.0 | current `Cargo.lock` checksum `e3ba2a386d7f85a81f119ad7498ebe444d2e22c2af0b86b069416ace48b3311a` | Existing request/response types shared by hyper stack | Keep existing |
| `h2` | `0.4.14` | MIT | current `Cargo.lock` checksum `171fefbc92fe4a4de27e0698d6a5b392d6a0e333506bc49133760b3bcf948733`; local upstream repo HEAD `hyperium/h2@d361b75762868f51fb85e39e0a6c3c79958b42ea` | Existing transitive H2 baseline through hyper; comparison baseline against `http2` fork | Keep existing until fork adapter replaces exact-profile H2 path |
| `hyper-rustls` | `0.27.9` | Apache-2.0 OR ISC OR MIT | current `Cargo.lock` checksum `33ca68d021ef39cf6463ab54c1d0f5daf03377b70561305bb89a8f83aab66e0f` | Existing Rust TLS client for non-Codex/default path and Kiro current backend context | Keep existing; not Codex exact backend |
| `rustls` | `0.23.40` | Apache-2.0 OR ISC OR MIT | current `Cargo.lock` checksum `ef86cd5876211988985292b91c96a8f2d298df24e75989a43a3c73f2d4d8168b`; local repo HEAD `rustls/rustls@cb9a749ec8476cd0154468b247038451fb86f4a2` | Existing `hyper-rustls` TLS backend; Kiro profile currently declares `rustls` | Keep existing for Kiro/non-mimicry path |

License gate result: all approved baseline entries are Apache-2.0 / MIT / ISC-compatible / BSD-family-compatible. No GPL/LGPL/AGPL runtime dependency is approved by L2-A0.

### 2.2 crates.io current metadata notes

- `openssl` crates.io current stable/default/newest is `0.10.79`, updated `2026-05-04T00:19:55.653111Z`; features include `default=[]`, `vendored=["ffi/vendored"]`, `bindgen`, `unstable_boringssl`, `aws-lc`, and `aws-lc-fips`.
- `openssl` local official repo HEAD is `b460eb378c335610df5395a251408ad70bb60d42`, commit `Prefer Homebrew openssl@4 and stop looking for openssl@1.1 (#2633)`.
- `0x676e67/http2` local official repo HEAD is `a33b27e469434a99105f35670c9970f22112e892`, commit `Merge remote-tracking branch 'upstream/master'`.
- Existing merged Rust core still declares `hyper`, `hyper-util`, `http`, and `hyper-rustls` in `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`; exact versions above come from `exploratory/rust-core-gateway/merged/Cargo.lock`.

## 3. Rejected utility preset packages

| crate | crates.io version observed | license observed | decision | reason |
| --- | --- | --- | --- | --- |
| `wreq-util` | max stable `2.2.6`; newest pre-release `3.0.0-rc.10` | stable metadata shows GPL-3.0; newest pre-release metadata shows LGPL-3.0 | REJECT for production runtime dependency | GPL/LGPL is outside allowed production runtime licenses; preset utility outcome must be recreated from HUAKAI-owned templates or permissive crates |
| `rquest-util` | `2.2.1` | GPL-3.0 | REJECT for production runtime dependency | GPL is outside allowed production runtime licenses |

This rejection does not delete the product capability. It changes the implementation method: HUAKAI may preserve preset-like behavior through internal templates, feature-flagged adapters, and capture gates without importing GPL/LGPL utility packages.

## 4. OpenSSL system requirements

| Environment | Requirement | L2-A0 recommendation |
| --- | --- | --- |
| Linux build host | `pkg-config`, C toolchain, OpenSSL headers; package name commonly `libssl-dev` on Debian/Ubuntu or `openssl-dev` on Alpine | Require OpenSSL >= 1.1.1; prefer OpenSSL 3.x. Capture artifacts must record `openssl version -a`. Current audit host reports OpenSSL `3.5.5` on Ubuntu `26.04`. |
| macOS dev | Homebrew OpenSSL package, normally `brew install openssl@3`; set `OPENSSL_DIR` if pkg-config cannot find it | Prefer OpenSSL 3.x; do not assume system LibreSSL is acceptable for exact capture. |
| Docker Debian/Ubuntu base | Build image: `apt-get install -y --no-install-recommends ca-certificates pkg-config libssl-dev`; runtime image: include matching `libssl3`/`openssl` package if dynamically linked | Preferred for exact Codex profile because package versions are easier to pin and audit than developer laptops. |
| Docker Alpine base | Build image: `apk add --no-cache ca-certificates pkgconf openssl-dev`; runtime image: include `libssl3`, `libcrypto3`, `ca-certificates` when dynamically linked | Allowed only if capture proves exact; musl/OpenSSL package differences must be treated as R-REL-002 recapture triggers. Existing Go `backend/Dockerfile` uses `alpine:3.20`, but Rust data-plane image is not defined in this atom. |
| `openssl` crate `vendored` feature | `openssl` feature `vendored` maps to `openssl-sys` `vendored`, which optionally pulls `openssl-src` | Do not make `vendored` the default baseline in L2-A0. Prefer system OpenSSL pinned in Docker for initial exact capture, because production should expose and record the same dynamic OpenSSL surface operators run. Keep `vendored` as an allowed escape hatch for reproducible CI/dev builds if system package drift blocks repeatability. |

Trade-off: system OpenSSL has operational visibility and simpler CVE patching through OS packages, but fingerprint can drift by distro/package. `vendored` improves build repeatability but makes HUAKAI responsible for tracking the vendored OpenSSL source crate and rebuilding promptly on CVEs. Either path must record OpenSSL version, crate checksum, image/base digest, and capture artifact before production dispatch.

## 5. Per-profile backend prescription

| profile | observed template/backend state | L2-A0 prescription | dispatch gate |
| --- | --- | --- | --- |
| `codex` / `openai_codex_cli` | Template declares `tls_backend = native-tls/openssl`; Rust match policy currently treats Codex as `KnownGapBlocked` | Use OpenSSL exact adapter + MIT `http2` fork as D1 baseline | No production dispatch until local exact capture PASS and R-D Owner real-upstream PASS |
| `kiro` / `kiro_cli` | Template declares `tls_backend = rustls` and randomized extension order | Continue `rustls`/`hyper-rustls` path for Kiro unless capture evidence later proves OpenSSL is required | Sample-set policy allowed, but real dispatch still must respect provider-specific capture gates |
| `gemini` / `gemini_advanced` | Template declares `tls_backend = nodejs`; note says Node.js wraps OpenSSL; two TLS variants are present | Backend remains TBD; do not force Codex OpenSSL adapter onto Gemini until separate capture/backend atom validates it | No production exact-claim until Gemini-specific adapter and capture gate exist |
| `anthropic` / `anthropic-claude-code` | Template remains in `_pending-backfill`; Lane 2 plan says Anthropic is Lane 2b | Decide after Lane 2b recapture/backfill; not part of L2-A0 dependency baseline | Anthropic mimicry enablement blocked until Lane 2b promotion criteria pass |

## 6. Risk register update

Updated `docs/10_RISK_REGISTER.md` with five new rows and preserved existing `R-SEC-002` unchanged:

| Risk ID | Severity | Summary |
| --- | --- | --- |
| `R-TRANSPORT-001` | HIGH | Exact TLS mimicry may need patch/unsafe adapter; mitigation is feature flag + local capture gate + Owner real-upstream gate + no production dispatch until PASS. |
| `R-LIC-003` | HIGH | Browser/TLS mimicry preset utilities may carry LGPL/GPL; mitigation is rejecting `wreq-util`/`rquest-util` for production and auditing any runtime dependency before addition. |
| `R-REL-002` | MED | OpenSSL fingerprint drift across OS/package versions; mitigation is pinned build/runtime surface, recorded OpenSSL version, and recapture on upgrade. |
| `R-TEST-001` | MED | Local capture pass may not equal real provider/WAF pass; mitigation is mandatory R-D Owner real-upstream gate. |
| `R-MAINT-001` | MED | HUAKAI-owned crypto patch creates rebase/CVE burden; mitigation is patch ledger, minimal scope, upstream HEAD verification, and capture tests on every rebase. |

## 7. Next atom start conditions

| Atom | May start when | Must not do |
| --- | --- | --- |
| L2-A1 transport backend selection model | Owner accepts this L2-A0 baseline and risk rows; no GPL/LGPL utility dependency is proposed | Must not connect exact backend to `ProxyEngine` production dispatch |
| L2-A2 local TLS ClientHello capture helper | L2-A0 baseline is accepted; helper is test-only and fixture redaction rules are written before capture data lands | Must not store real secrets or treat local capture as release evidence |
| L2-A4 OpenSSL adapter skeleton | L2-A1/A2 provide backend intent and capture harness; Owner accepts whether first build uses system OpenSSL or `vendored` | Must not silently enable Codex production dispatch; must rerun license audit after actual Cargo changes |

## 8. Audit conclusion

L2-A0 passes as a documentation-level verification gate: the proposed D1 baseline can use only permissive runtime dependencies, and the known GPL/LGPL utility preset packages are explicitly rejected. The remaining blockers are operational and verification gates, not license blockers: exact capture, Owner real-upstream validation, OpenSSL drift control, and patch maintenance.

No feature was reduced. Exact mimicry remains preserved as a gated backend, `KnownGapBlocked` remains visible and blocked, and utility preset rejection is handled by safe equivalent implementation strategy.

## 9. Source coverage proof

Observed:

- HUAKAI risk register format and existing `R-SEC-002`: `docs/10_RISK_REGISTER.md`.
- Lane 2 D1/D2/D3 architecture plan and L2 atom sequence: `docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md`.
- Existing Rust dependency declarations: `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`.
- Existing Rust lockfile versions and checksums: `exploratory/rust-core-gateway/merged/Cargo.lock`.
- Existing Rust workspace license: `exploratory/rust-core-gateway/merged/Cargo.toml`.
- Existing profile policies/tests: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs`, `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs`.
- Existing fingerprint templates: `tools/fingerprint-collector/templates/codex-cli.json`, `tools/fingerprint-collector/templates/kiro-cli.json`, `tools/fingerprint-collector/templates/gemini-advanced.json`, `tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json`.
- Current Go Docker baseline context: `backend/Dockerfile`.
- Official local repos: `/home/codex/refs/rust-openssl`, `/home/codex/refs/http2`, `/home/codex/refs/h2`, `/home/codex/refs/rustls`, `/home/codex/refs/wreq`, `/home/codex/refs/boring`.
- Official crates.io metadata/API pages: `openssl/0.10.79`, `openssl-sys/0.9.115`, `openssl-src/300.6.0+3.6.2`, `hyper/1.9.0`, `hyper-util/0.1.20`, `http/1.4.0`, `h2/0.4.14`, `hyper-rustls/0.27.9`, `rustls/0.23.40`, `wreq-util/2.2.6`, `wreq-util/3.0.0-rc.10`, `rquest-util/2.2.1`.
- Local system OpenSSL evidence: `openssl version -a` reported OpenSSL `3.5.5` on Ubuntu `26.04`.

Inferences:

- `vendored` is allowed as an escape hatch, but system OpenSSL pinned in Docker is the initial recommendation because production drift is easier to observe and patch through OS packages.
- Gemini backend remains TBD because current evidence says Node.js/OpenSSL shape, but no Gemini-specific Rust exact adapter has been selected.

Open questions:

- Owner must confirm whether L2-A4 should start with system OpenSSL only or also add a CI/dev `vendored` feature variant.
- Owner real-upstream R-D gate still needs artifact format and execution owner before production dispatch can be released.

Source files read: `docs/RULES.md`; `docs/10_RISK_REGISTER.md`; `docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md`; `exploratory/rust-core-gateway/merged/Cargo.toml`; `exploratory/rust-core-gateway/merged/Cargo.lock`; `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs`; `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs`; `tools/fingerprint-collector/templates/codex-cli.json`; `tools/fingerprint-collector/templates/kiro-cli.json`; `tools/fingerprint-collector/templates/gemini-advanced.json`; `tools/fingerprint-collector/templates/_pending-backfill/anthropic-claude-code.json`; `backend/Dockerfile`; `/home/codex/refs/rust-openssl/openssl/Cargo.toml`; `/home/codex/refs/rust-openssl/openssl-sys/Cargo.toml`; `/home/codex/refs/http2/Cargo.toml`; crates.io official metadata URLs listed above.

Lane: specifier

Agent: GPT-5 Codex

UTC timestamp: 2026-05-15T03:22:18Z

Owner 中文摘要：本次 L2-A0 做的是依赖和许可证基线审计，真实观察包括当前 `openssl`/`http2`/`hyper`/`hyper-util`/`http`/`h2`/`hyper-rustls`/`rustls` 版本、crate license、Cargo.lock checksum、本地官方仓库 HEAD、现有 profile backend 状态和风险登记格式；合理推断包括默认优先 system OpenSSL、`vendored` 作为可选逃生路径、Gemini backend 暂定 TBD；open question 还有 2 个，主要是 L2-A4 是否同时做 `vendored` feature 和 R-D real-upstream gate artifact owner。没有功能缩水；没有读取受限非 MIT reference 源码；GPL/LGPL utility preset 已明确拒用，后续实现只能走 permissive 依赖或 HUAKAI-owned safe equivalent。
