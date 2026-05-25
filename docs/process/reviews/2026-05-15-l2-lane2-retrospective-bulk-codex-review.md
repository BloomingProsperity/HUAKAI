# R-C Lane 2 Retrospective Bulk Cross-Review

Coverage: 7 commits (`b369aba` -> `4a26f6d`) 之间的 R-C Lane 2 slices。
Method: retrospective (commit 后补跑)。
Reviewer: Codex GPT-5。
Sandbox: workspace-write（只用于写本文件，未跑 cargo / 未改源码）。

Clean-room boundary: 本次只读 HUAKAI 内部 `docs/**`、`exploratory/**`、`tools/**`、plan、git log/show/diff；未阅读 `sub2api` / `new-api` / `portkey` / `helicone` / `litellm` / `all-api-hub` / `envoy-ai-gateway` reference repos。

## Summary Table

| Slice | Commit | Verdict | HIGH | MEDIUM | LOW |
|---|---|---|---|---|---|
| L2-A0 (kickoff) | `b369aba` | APPROVE | 0 | 0 | 0 |
| L2-A1 + L2-A2 | `91c00e2` | APPROVE | 0 | 0 | 0 |
| L2-A3 + L2-A4 + L2-A10 | `91bb92c` | APPROVE_WITH_NOTES | 0 | 0 | 1 |
| (test fixture) | `994ad58` | APPROVE | 0 | 0 | 0 |
| L2-A5.1 | `fcdedcd` | APPROVE_WITH_NOTES | 0 | 0 | 1 |
| L2-A5.2 + L2-A6 | `17ca1a3` | APPROVE_WITH_NOTES | 0 | 0 | 1 |
| L2-A5.3 + L2-A7 | `4a26f6d` | APPROVE_WITH_NOTES | 0 | 1 | 0 |

## Per-slice findings

### 1. L2-A0 kickoff — `b369aba`

- Verdict: APPROVE
- Findings: none.
- Positive checks:
  - 架构 plan 明确 D1 选择 OpenSSL + MIT `http2` fork，D2 仅允许窄范围 feature-gated adapter/patch，D3 要求生产 dispatch 直到 exact capture PASS 前保持阻断。Evidence: `b369aba:docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md:17`, `:18`, `:19`
  - L2-A0 dep/license audit 明确本 atom 不落地依赖，`Cargo.toml` 留给后续 atom；baseline 依赖许可证为 permissive，`wreq-util` / `rquest-util` 被拒绝进入生产 runtime。Evidence: `b369aba:docs/process/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md:15`, `:41`, `:54`, `:55`, `:57`
  - Risk register 补齐 R-TRANSPORT-001 / R-LIC-003 / R-REL-002 / R-TEST-001 / R-MAINT-001，且不把 license/security 风险当成功能删除理由。Evidence: `b369aba:docs/10_RISK_REGISTER.md:29`, `:30`, `:31`, `:32`, `:33`
  - `git show --check b369aba` 未报 whitespace 错误；本 commit 只改 docs/risk/plan，无 runtime 代码或 `LICENSE` 变更。

### 2. L2-A1 + L2-A2 — `91c00e2`

- Verdict: APPROVE
- Findings: none.
- Positive checks:
  - L2-A2 plan scope 明确 test-only capture helper，不碰 `src/`、`Cargo.toml`、生产行为或非 MIT reference projects。Evidence: `91c00e2:docs/process/plans/2026-05-15-l2-a2-tls-clienthello-capture-codex.md:4`
  - Backend intent 先阻断 KnownGap，再按 `tls_backend` 分发 OpenSSL/Rustls/UnsupportedTemplate，避免 Codex known gap 静默进入 dispatch。Evidence: `91c00e2:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:155`, `:156`, `:163`, `:166`, `:167`, `:168`, `:169`
  - 测试覆盖 Codex blocked、Kiro Rustls、Gemini unsupported、synthetic stable OpenSSL 四类 backend intent。Evidence: `91c00e2:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_profile_test.rs:201`, `:220`, `:227`, `:243`
  - TLS parser 使用 checked cursor / explicit `CaptureError`，没有在 parser 主体里用 `unwrap` / `unsafe`；负例覆盖 truncated body、bad handshake length、odd cipher list、extension overflow、nested list overflow、EC point overflow、non-ClientHello。Evidence: `91c00e2:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/tls_capture.rs:291`, `:292`, `:293`; `91c00e2:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_capture_test.rs:65`, `:83`, `:100`, `:118`, `:143`, `:165`, `:187`
  - `git show --check 91c00e2` 未报 whitespace 错误。

### 3. L2-A3 + L2-A4 + L2-A10 — `91bb92c`

- Verdict: APPROVE_WITH_NOTES
- Findings:
  - LOW: A3 test-only diff helper 新增 `unreachable!`，当前路径看起来由 synthetic scalar sentinel 保护，不是 production panic risk，但它属于 reviewer checklist 点名要看的一类。Evidence: `91bb92c:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:114`, `:125` — Impact: 后续扩展 synthetic diff case 时可能把 coverage gap 变成 panic，而不是可审计状态。Suggested follow-up: 改成 explicit `FieldStatus` 或 typed error，保持 diff normalizer 全路径可报告。
- Positive checks:
  - A3 plan 要求每个 expected field 都有状态，blocked/unsupported profile 也能完成 diff；实现覆盖 scalar/list status、ordered/set status、blocked 标记。Evidence: `91bb92c:docs/process/plans/2026-05-15-l2-a3-capture-diff-normalizer-codex.md:7`; `91bb92c:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:10`, `:23`, `:30`, `:38`, `:79`
  - A3 测试覆盖 KnownGapBlocked、SampleSetRandomized、ExactStable、UnsupportedTemplate、scalar mismatch、NotInTemplate/NotCaptured。Evidence: `91bb92c:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_capture_diff_test.rs:11`, `:33`, `:72`, `:126`, `:187`, `:205`
  - A4 optional OpenSSL 依赖 feature-gated，workspace 仍 MIT / `unsafe_code = "forbid"`，OpenSSL adapter 显式 `SslVerifyMode::PEER`、default verify paths、SNI + hostname verification。Evidence: `91bb92c:exploratory/rust-core-gateway/merged/Cargo.toml:5`, `:7`, `:11`; `91bb92c:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:11`, `:32`, `:39`; `91bb92c:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:36`, `:38`, `:72`, `:73`, `:85`, `:88`
  - A10 recapture runbook 包含 secret redaction / drop gate，要求 R-D 不能只靠 local capture release。Evidence: `91bb92c:exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:157`, `:270`, `:279`, `4a26f6d:exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:463`, `:467`, `:474`
  - `git show --check 91bb92c` 未报 whitespace 错误。

### 4. test fixture fix — `994ad58`

- Verdict: APPROVE
- Findings: none.
- Positive checks:
  - 变更只在 feature-gated OpenSSL test fixture 内，把 owned `Asn1Time` / `Asn1Integer` 先绑定再借用，未改 library 行为。Evidence: `994ad58:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs` diff around `set_validity_and_serial`
  - 不引入依赖、secret、license 变更、production code 或 feature gate 变化。
  - `git show --check 994ad58` 未报 whitespace 错误。

### 5. L2-A5.1 — `fcdedcd`

- Verdict: APPROVE_WITH_NOTES
- Findings:
  - LOW: `UnsupportedCipher` fail-fast branch 有实现但本 commit 未新增直接负例；现有测试主要证明 Codex profile positive capture diff 和 synthetic ALPN 注入。Evidence: `fcdedcd:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:118`, `:119`, `:120`; `fcdedcd:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:55`, `:75`, `:99`, `:103` — Impact: cipher mapping 表未来回归时可能只在真实 profile 命中后才暴露。Suggested follow-up: 加一个 synthetic `0xffff` cipher profile，断言 `OpenSslAdapterError::UnsupportedCipher(0xffff)`。
- Positive checks:
  - `new_with_profile` 保留安全默认 verify paths，并按 profile 注入 cipher suites 与 ALPN；TLS 1.3 和 legacy cipher 分开调用 OpenSSL API。Evidence: `fcdedcd:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:52`, `:55`, `:57`, `:58`, `:121`, `:128`, `:139`
  - ALPN wire format有 length-prefix 测试，capture diff 断言 cipher_suites 与 alpn_protocols OrderedMatch。Evidence: `fcdedcd:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:63`, `:64`, `:65`, `:92`, `:99`, `:103`
  - 不动 `Cargo.toml`；没有新 runtime license 面。
  - `git show --check fcdedcd` 未报 whitespace 错误。

### 6. L2-A5.2 + L2-A6 — `17ca1a3`

- Verdict: APPROVE_WITH_NOTES
- Findings:
  - LOW: OpenSSL groups/sigalgs positive test 在不支持 4588 或某 sigalg 的 runtime 上会接受 typed unsupported branch 并继续通过；这是 portability-friendly，但不能被当作“该环境已证明 full Codex profile exact capture”。Evidence: `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:115`, `:119`, `:131`, `:157`, `:176` — Impact: test pass 语义依赖环境，回顾报告/CI 摘要容易把 typed unsupported 与 exact match 混淆。Suggested follow-up: CI/report 将 “full OrderedMatch” 与 “typed unsupported fail-fast” 分成两个显式 status；release gate 只接受前者或明确的 feature-blocked disposition。
- Positive checks:
  - adapter 对 unsupported group/sigalg fail-fast，且映射不做静默 fallback；0xffff negative tests 覆盖 typed error。Evidence: `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:267`, `:277`, `:286`, `:289`, `:317`; `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:193`, `:200`, `:206`, `:213`
  - `http2` fork optional feature-gated 且 pinned 到 `a33b27e...`；默认 feature 仍空。Evidence: `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml:8`, `:9`, `:13`, `:27`; `17ca1a3:exploratory/rust-core-gateway/merged/Cargo.lock:631`, `:634`
  - H2 adapter 校验 required profile fields、order/value 一致性、unsupported setting/pseudo-header；测试断言 SETTINGS id 顺序和值，以及 pseudo-header order。Evidence: `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs:66`, `:83`, `:91`, `:98`, `:204`, `:257`; `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs:25`, `:54`, `:58`, `:61`, `:76`
  - schema/profile 字段新增为 explicit H2 frame/order structures，未动 auth/billing/quota/schema migration。Evidence: `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:128`, `:129`, `:130`, `:131`, `:132`, `:133`; `17ca1a3:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http_profile.rs:52`, `:68`
  - `git show --check 17ca1a3` 未报 whitespace 错误。

### 7. L2-A5.3 + L2-A7 — `4a26f6d`

- Verdict: APPROVE_WITH_NOTES
- Findings:
  - MED: `DispatchDecision::AllowOpenSsl` 当前只基于 feature availability 与 `ec_point_formats == [0,1,2]`，没有携带完整 local capture diff / R-D Owner gate provenance；虽然此 commit 未接 `ProxyEngine`，但 `dispatch.rs` 注释称它是“生产 dispatch gate 的最终判定”，未来 wiring 若直接复用会绕过 D3 的 full exact PASS 语义。Evidence: D3 requires production dispatch stay blocked until exact capture PASS at `b369aba:docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md:19`; current gate at `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs:5`, `:18`, `:20`, `:26`, `:29`, `:31`; tests allow synthetic OpenSSL profile at `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:57`, `:74`, `:78`, `:79` — Impact: no current HIGH because no ProxyEngine wiring in this commit, but this must be fixed before A9/production transport client construction. Suggested follow-up: rename current result to “profile eligibility” or require `AllowOpenSsl` to consume a completed exact capture/R-D gate artifact; block until all relevant TLS/H2 extension checks have explicit PASS.
- Positive checks:
  - OpenSSL `new_with_profile` now runs in-memory runtime ClientHello preflight and records provenance before returning adapter. Evidence: `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:81`, `:90`, `:96`, `:97`, `:117`
  - EC point formats fail closed: only `[0,1,2]` accepted; empty/partial/wrong-order tests cover fail-fast behavior. Evidence: `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs:316`, `:320`, `:324`, `:325`; `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs:193`, `:210`, `:219`, `:258`, `:268`, `:278`
  - Dispatch tests prove Codex KnownGapBlocked and Gemini UnsupportedTemplate do not pass; feature-off OpenSSL also blocks. Evidence: `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:6`, `:12`, `:25`, `:38`, `:45`, `:54`, `:83`, `:105`, `:116`
  - Moved capture helper into feature-gated mimicry module without `unsafe`; parser still uses checked cursor and explicit errors. Evidence: `4a26f6d:exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_capture.rs:40`, `:106`, `:170`, `:181`, `:330`, `:331`
  - RUNBOOK now documents OpenSSL build/runtime as fingerprint input and R-D gate remains mandatory. Evidence: `4a26f6d:exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:476`, `:478`, `:483`, `:474`
  - `git show --check 4a26f6d` 未报 whitespace 错误。

## Aggregate Risks 观察到的横向问题

- Clean-room / license: 本次回顾未看到 reviewed commits 引入 forbidden reference repo source、复制 non-MIT distinctive file structure、或修改 `LICENSE`。L2-A0 明确拒绝 GPL/LGPL utility presets；后续实际引入的 `openssl` / `tokio-openssl` / pinned `http2` fork 均被 feature-gated，并有 L2-A0 许可证 baseline 记录。
- Runtime dependency: `openssl` 和 `tokio-openssl` 是 optional；`http2` fork 是 optional git dependency。当前风险不是 license blocker，而是后续必须把 feature matrix、OpenSSL runtime version、capture artifact checksum 纳入 release gate。
- Security / secrets: 未看到真实 secret；RUNBOOK 示例使用 `${OPENAI_API_KEY}` 占位，并有 redaction/drop checklist。真实 Owner capture artifact 仍必须先过 secret scan 才能 promotion。
- Test strength: 大多数测试是有意义的字段级断言，不是纯占位。主要弱点是某些 environment-dependent OpenSSL branches 允许 typed unsupported 后 pass，报告时必须区分“exact match pass”和“fail-fast pass”。
- Dispatch gate: 当前最大横向风险是“profile eligibility”和“production release gate”边界容易混淆。D3 要求生产 dispatch 直到 exact local + Owner real-upstream PASS 前阻断；任何未来 ProxyEngine wiring 必须以这个 gate 为准。

## Followup TODOs

- MED [Transport/Codex, 1-2h, before ProxyEngine/A9 wiring]: 收紧 `DispatchDecision::AllowOpenSsl`，让它依赖完整 capture diff/R-D gate artifact，或改名为 non-production “eligible” decision，避免误接生产 dispatch。
- LOW [Codex, 20m]: 给 L2-A5.1 添加 unsupported cipher negative test，断言 `UnsupportedCipher(0xffff)`。
- LOW [Codex, 20m]: 把 A3 test-only `unreachable!` 改成 explicit reportable status 或 typed error。
- LOW [CI/Owner, 30-60m]: feature test matrix 输出区分 `OrderedMatch`、`typed unsupported fail-fast`、`feature blocked`，避免把 portability pass 误读为 exact capture pass。

Source files read: `docs/templates/codex-reviewer.md`; `docs/RULES.md`; `docs/10_RISK_REGISTER.md`; `docs/process/plans/2026-05-15-r-c-lane-2-architecture-codex.md`; `docs/process/plans/2026-05-15-r-c-lane2-l2-a0-dep-license-audit-codex.md`; `docs/process/plans/2026-05-15-l2-a2-tls-clienthello-capture-codex.md`; `docs/process/plans/2026-05-15-l2-a2-tls-parser-negative-tests-codex.md`; `docs/process/plans/2026-05-15-l2-a3-capture-diff-normalizer-codex.md`; `docs/process/plans/2026-05-15-l2-a4-openssl-mimicry-adapter-codex.md`; `docs/process/plans/2026-05-15-l2-a5-1-openssl-profile-codex.md`; `docs/process/plans/2026-05-15-l2-a5-2-openssl-groups-sigalgs-codex.md`; `docs/process/plans/2026-05-15-l2-a6-http2-fork-adapter-codex.md`; `docs/process/plans/2026-05-15-l2-a5-3-ec-point-formats-codex.md`; `exploratory/rust-core-gateway/merged/Cargo.toml`; `exploratory/rust-core-gateway/merged/Cargo.lock`; `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`; `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend.rs`; `profile.rs`; `dispatch.rs`; `openssl_adapter.rs`; `tls_capture.rs`; `http2_adapter.rs`; `http_profile.rs`; `tests/common/tls_capture.rs`; `tests/common/capture_diff.rs`; `tests/mimicry_capture_test.rs`; `tests/mimicry_capture_diff_test.rs`; `tests/mimicry_profile_test.rs`; `tests/mimicry_openssl_adapter_test.rs`; `tests/mimicry_http2_adapter_test.rs`; `tests/mimicry_dispatch_test.rs`; `tools/fingerprint-collector/templates/SCHEMA.md`; `tools/fingerprint-collector/templates/codex-cli.json`; `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`; git commit messages/stats/diffs for `b369aba`, `91c00e2`, `91bb92c`, `994ad58`, `fcdedcd`, `17ca1a3`, `4a26f6d`.

Lane: REVIEWER + scribe (bulk retrospective)
Agent: Codex GPT-5
UTC timestamp: 2026-05-15T09:53:59Z

## Owner Summary (中文，3-5 句话)

本次补跑 R-C Lane 2 bulk retrospective review 覆盖 7 个 commit，结论是无 HIGH 阻塞，整体可以继续推进 L2-A5.5，但生产 dispatch wiring 前必须处理一个 MED：`AllowOpenSsl` 不能被误当作完整 exact capture/R-D gate。依赖和 clean-room 方面未看到 GPL/LGPL runtime 直接引入、未读 forbidden reference repos、未改 `LICENSE`、未发现真实 secret。测试总体不是占位，已经覆盖 parser negative、字段级 diff、OpenSSL/H2 profile 注入和 dispatch block；主要弱点是 OpenSSL 环境相关 pass 语义要区分 exact match 与 typed unsupported。建议下一步在 A5.5 继续前进的同时，把 dispatch gate provenance 收紧列为 A9 前置补丁。
