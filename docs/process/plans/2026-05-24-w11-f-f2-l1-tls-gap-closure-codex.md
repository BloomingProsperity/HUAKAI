# 2026-05-24 W11-F F-2 L1 TLS Gap Closure Codex Plan Draft

| Field | Value |
| --- | --- |
| Plan type | CLAUDE.md #10 independent Codex parallel draft |
| Descriptor | `w11-f-f2-l1-tls-gap-closure` |
| Saved path | `docs/process/plans/2026-05-24-w11-f-f2-l1-tls-gap-closure-codex.md` |
| Independence note | This draft is written from HUAKAI-owned docs/code only. I did not read a same-descriptor Claude draft. |
| Path note | Owner prompt ended after `保存到:` without a concrete path, so this uses the canonical CLAUDE.md #10 Codex suffix path. |
| Timestamp | 2026-05-24T14:45:22Z |

## Owner Directive

> "你是 Codex, 现在为 HUAKAI Rust 数据面 W11-F 指纹波 **F-2 L1 TLS 缺口闭环** 写独立 plan draft (CLAUDE.md #10 平行稿). 写完保存到:"

## Scope

### In Scope

- Rust data-plane fingerprint wave W11-F, focused on closing the L1 TLS production/canary gap.
- Production dispatch rules for TLS mimicry profiles: verified profiles may proceed; known gaps, unsupported templates, or failed preflight must fail closed.
- Runtime or startup L1 TLS preflight for `mimicry-boring` and `mimicry-openssl`, using HUAKAI-owned capture/parsing helpers already present in the Rust tree.
- Discriminating tests that prove a mismatched ClientHello, missing required extension, wrong extension order, or skipped preflight turns the gate red.
- Documentation updates needed to preserve release/runbook truth: recapture runbook, feature matrix notes, and implementation status notes if behavior changes.
- Verification through Rust test matrix, feature-specific tests, clippy, and per-commit Codex review before any implementation commit.

### Out of Scope

- Go backend packages, especially frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, and `backend/internal/proto`.
- Database schema, auth core, billing ledger, quota enforcement, deployment scripts, production secrets, or `LICENSE`.
- L3 device fingerprinting implementation beyond confirming no regression against the L1 dependency documented in `docs/specs/device-fingerprint-binding.md`.
- True L2 HTTP/2 ProxyEngine wiring unless it is required to keep production dispatch fail-closed for L2-only or incomplete profiles.
- Reading non-MIT reference project source. This plan does not require external reference source.
- New runtime dependencies unless Owner explicitly approves.

## Terminology Alignment

This plan treats the Owner phrase `F-2 L1 TLS 缺口闭环` as the current task label for closing the L1 TLS gap. The older W11-F notes in `docs/process/plans/2026-05-22-rust-hardening-plan.md` also discuss an F2/H2 adjacency; that H2 work remains adjacent and must not be silently marked done by this L1 plan.

## Current Observed Baseline

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs` already has an intent-first resolver shape from the W11-E D-10 fix: profile intent is resolved before backend feature availability checks.
- `src/mimicry/dispatch.rs` already distinguishes `AllowBoring`, `AllowOpenSsl`, `BlockKnownGap`, and `BlockUnsupportedTemplate`.
- `src/proxy_engine/http_client.rs` already calls `verify_profile_dispatchable_for_production` before constructing the Boring connector path.
- `src/mimicry/openssl_adapter.rs` already has local preflight behavior and exposes `preflight_passed()` / `preflight_extras()`.
- `src/mimicry/boring_wire.rs` already has feature-gated byte-level ClientHello tests for Boring-backed profiles.
- `src/mimicry/wire_capture_fixture.rs` already provides HUAKAI-owned TLS ClientHello capture/parsing test infrastructure.
- `src/mimicry/http2_adapter.rs` explicitly remains not wired into ProxyEngine; this plan must preserve production blocking for incomplete L2 evidence.
- `tools/feature-matrix/verify.sh` already covers default, `mimicry-boring`, `mimicry-openssl`, and `mimicry-http2-fork` combinations.

## Gap Statement

The current L1 risk is not that all profile routing is missing. The risk is that production/canary dispatch can be allowed by a policy-level decision while the actual runtime TLS ClientHello may not yet be proven against the profile at the point the production client is constructed. Existing byte-level tests are valuable, but the release gate should fail closed when the exact profile selected for production cannot pass L1 TLS preflight.

The closure target is therefore:

- no production or canary profile is considered dispatchable unless its L1 TLS wire behavior is verified for the selected backend;
- failing or unavailable preflight is a blocking condition, not a warning;
- known gaps remain preserved as explicit blocked features or mandatory roadmap items, not deleted features;
- tests prove the gate catches the regressions it claims to catch.

## Success Criteria

- A production dispatch path cannot return an allowed client/connector for a profile whose L1 TLS preflight fails or is unavailable.
- Boring-backed profiles have a reusable L1 preflight path derived from HUAKAI-owned capture/parsing code, not only isolated tests.
- OpenSSL-backed profiles require adapter preflight success before production dispatch is allowed.
- Known-gap and unsupported-template profiles still return explicit blocked outcomes with observable reason/provenance.
- L2 incomplete profiles remain production-blocked; this L1 plan does not create an L1-only production loophole.
- Tests include at least one discriminating negative case per gate: missing required TLS extension, wrong extension order or hash mismatch, skipped/unavailable preflight, known-gap profile, and unsupported template.
- Feature matrix and targeted tests pass under default, `mimicry-boring`, `mimicry-openssl`, and `mimicry-http2-fork`.
- `codex exec review --uncommitted --full-auto` is run after staging implementation changes and before any commit.

## Proposed File Scope

Expected Rust files to inspect or modify during implementation:

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/wire_capture_fixture.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/mod.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_backend_resolver_test.rs`

Possible new Rust file:

- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/l1_preflight.rs`

This new file is acceptable from the package-structure rule because it is inside the Rust exploratory crate, has one cohesive responsibility, and does not touch any frozen Go package. If implementation can keep this logic cohesive inside an existing small file without mixing responsibilities, a new file is optional.

Expected docs or tooling files to update only if implementation changes release semantics:

- `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`
- `exploratory/rust-core-gateway/merged/tools/feature-matrix/README.md`
- `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md` only if Owner wants the synthesized plan/status artifact updated after execution.

## Concrete Execution Order

1. Reconfirm the same-descriptor Claude draft is not read before this Codex draft is compared by the Owner or orchestrator.
2. Run a baseline targeted test pass to capture current behavior before mutation:
   - Boring L1 byte-level profile test.
   - OpenSSL preflight missing/wrong-order tests.
   - Dispatch known-gap and allowed-profile tests.
   - Quick feature matrix if current workspace state allows.
3. Add failing tests first:
   - a profile with a deliberately missing required TLS extension must fail L1 production dispatch;
   - a profile with wrong extension ordering or mismatched JA3/hash evidence must fail L1 production dispatch;
   - a path that skips or cannot run preflight must fail closed;
   - a known-gap template remains explicitly blocked;
   - an L2-incomplete profile cannot become production allowed through an L1-only pass.
4. Introduce a cohesive L1 preflight abstraction if needed:
   - `L1TlsPreflightStatus` or equivalent success metadata;
   - `L1TlsPreflightError` or equivalent typed failure reason;
   - backend-specific runners for Boring and OpenSSL behind feature gates;
   - no new dependency unless Owner approves.
5. Wire the production dispatch/client construction path:
   - keep policy decision and runtime preflight distinct enough for tests to prove both;
   - prefer a fallible builder such as `try_build_http_client_with_profile` for testability;
   - keep existing fail-fast behavior only as a wrapper if broad API changes are not approved.
6. Preserve feature states:
   - verified L1 profile: allowed only if backend and preflight pass;
   - known gap: blocked with reason;
   - unsupported template: blocked with reason;
   - L2 incomplete: blocked for production/canary unless Owner explicitly approves a non-production feature flag.
7. Update runbook/status docs if the gate semantics or commands change.
8. Run full verification commands listed below.
9. Stage changes and run `codex exec review --uncommitted --full-auto`.
10. Fix any HIGH findings, document MED deferrals, and only then proceed to commit if Owner has approved execution.

## Test Quality Requirements

Every new test must answer: "what concrete defect makes this test fail?"

Required mutation self-checks:

- If the production dispatch gate ignores the preflight result, the negative L1 mismatch test must fail.
- If the fixture is non-discriminating and status/policy alone would produce the same result, the test must fail its own baseline comparison.
- If a known-gap template is accidentally reclassified as allowed because a backend feature is enabled, the known-gap test must fail.
- If L2 missing evidence is accidentally treated as sufficient after L1 passes, the L2-incomplete production-block test must fail.

Preferred pattern:

- In tests, compare a fully checked path against a deliberately weakened baseline such as "policy-only dispatch" or "status-only classification" and assert they differ for the discriminating fixture.

## Verification Commands

Run from `exploratory/rust-core-gateway/merged` unless noted.

```bash
export CARGO_TARGET_DIR="$HOME/.cache/huakai-rust-target"
cargo test -p core_gateway
cargo test -p core_gateway --features mimicry-boring
cargo test -p core_gateway --features mimicry-openssl
cargo test -p core_gateway --features mimicry-http2-fork
cargo clippy -p core_gateway --all-features -- -D warnings
bash tools/feature-matrix/verify.sh
```

If the full matrix is too slow during iteration, run targeted tests first and then run the full matrix before declaring completion.

Suggested targeted commands:

```bash
export CARGO_TARGET_DIR="$HOME/.cache/huakai-rust-target"
cargo test -p core_gateway --features mimicry-boring boring_client_hello -- --nocapture
cargo test -p core_gateway --features mimicry-openssl preflight -- --nocapture
cargo test -p core_gateway dispatch -- --nocapture
```

Review gate before commit:

```bash
codex exec review --uncommitted --full-auto
```

## Blast Radius

- Primary blast radius: Rust exploratory data-plane mimicry and proxy client construction.
- Default build risk: low to medium, depending on how much code is feature-gated and whether shared dispatch APIs change.
- Production safety impact: positive if implemented correctly, because failed or unverified TLS mimicry becomes fail-closed.
- Regression risk: medium for feature-gated mimicry connectors; low for unrelated Go/backend/admin code if file scope is followed.
- Package-structure risk: low if no files are added to frozen Go packages and any new Rust module has one cohesive responsibility.

## Failure Modes And Mitigations

| Failure mode | Mitigation |
| --- | --- |
| Preflight is only tested in unit tests and not wired into production dispatch | Add a production/client-construction gate and test the builder/dispatch path directly. |
| Test fixture is non-discriminating | Include a weakened-baseline assertion proving the bad path would differ from the checked path. |
| Boring and OpenSSL paths drift in semantics | Use a shared L1 status/error shape and backend-specific runners behind feature gates. |
| Startup preflight requires async/runtime handling that does not fit the current constructor | Prefer a fallible builder or narrow current-thread preflight helper; escalate if broad API changes are needed. |
| L1 pass accidentally allows L2-incomplete profile into production | Keep L2 state as an independent production gate; test L1-pass/L2-missing as blocked. |
| Known-gap profiles disappear from feature mapping | Preserve explicit blocked status and update docs/status rather than deleting profiles or tests. |
| New dependency seems convenient for TLS capture | Do not add it without Owner confirmation; reuse HUAKAI-owned capture helpers first. |
| Feature matrix becomes too slow for each edit loop | Use targeted tests during iteration, but require full matrix before completion. |

## Decision Points Needing Owner Sign-Off

- **D-F2-1 Startup error shape:** whether to propagate a typed error through `GatewayState::new` / client construction or keep existing fail-fast wrappers while adding fallible testable helpers. Recommendation: prefer typed `Result` where blast radius stays small; otherwise keep wrapper and document fail-fast behavior.
- **D-F2-2 Runtime preflight mechanism:** whether local loopback/in-memory ClientHello capture is acceptable in production startup images. Recommendation: use HUAKAI-owned in-process or loopback preflight only at startup/canary, fail closed if unavailable.
- **D-F2-3 L1-only production status:** whether any profile with L1 pass but L2 missing can be production enabled. Recommendation: no; keep production/canary blocked until L2 is wired or explicitly feature-flagged non-production.
- **D-F2-4 Capture recency:** whether stale but stable HUAKAI capture artifacts can pass release. Recommendation: require recapture or explicit Owner waiver before release if capture evidence is older than the project threshold.
- **D-F2-5 New module file:** whether implementation may add `src/mimicry/l1_preflight.rs`. Recommendation: yes, because it keeps one cohesive responsibility and avoids mixing dispatch policy with wire capture.

## Pre-Execution Checklist

- [ ] Owner/orchestrator compares this Codex draft with the independent Claude draft.
- [ ] A synthesized plan exists or Owner explicitly approves one draft as authoritative.
- [ ] No same-descriptor Claude draft has been read before comparison.
- [ ] Implementation file scope avoids frozen Go packages.
- [ ] No new dependency is planned without Owner approval.
- [ ] Baseline targeted tests are run and recorded.
- [ ] Negative test fixtures are designed to be discriminating before implementation.
- [ ] L2 incomplete behavior remains blocked in production/canary.
- [ ] Review command is reserved for staged implementation changes before commit.

## Feature Preservation Mapping

| Feature state | Planned outcome |
| --- | --- |
| L1 TLS profile with backend support and passing preflight | Implemented / production-allowed if all adjacent gates also pass |
| L1 TLS profile with backend support but failed preflight | Safe Equivalent: fail closed with explicit reason |
| Known-gap TLS template | Mandatory Roadmap or Feature Flag off; not deleted |
| Unsupported TLS template | Safe Equivalent: fail closed with explicit reason |
| L1-passing but L2-incomplete profile | Feature Flag off / production-blocked until L2 closure |
| Existing tests and profiles | Preserved or strengthened; no silent removal |

## Assumptions

- The task is plan-only; no implementation should begin until CLAUDE.md #10 comparison/synthesis happens.
- HUAKAI-owned Rust capture helpers are acceptable evidence for local preflight implementation.
- Current dirty worktree items outside this doc are unrelated and must not be reverted or normalized by this plan.
- W11-F F-2 naming in the Owner prompt supersedes older local numbering for this particular plan label, while older H2 adjacency remains a separate risk.

## Risks

- Security risk if an L1 preflight can be bypassed or downgraded to warning.
- Parity risk if known-gap profiles are removed instead of preserved as blocked roadmap items.
- Operations risk if startup preflight is expensive or flaky; should be measured and isolated to profile-enabled paths.
- Clean-room risk remains low as long as implementation continues to use HUAKAI-owned code/docs and does not read non-MIT reference source.

## Source Coverage Proof

HUAKAI-owned files read or used as context:

- `CLAUDE.md`
- `docs/RULES.md`
- `docs/process/plans/2026-05-22-rust-hardening-plan.md`
- `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md`
- `docs/specs/device-fingerprint-binding.md`
- `docs/process/research/2026-05-22-deep-audit-rust.md`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/Cargo.toml`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/lib.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/dispatch.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/openssl_adapter.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/wire_capture_fixture.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/http2_adapter.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/boring_tls_connector.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_http2_adapter_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_openssl_adapter_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_backend_resolver_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs`
- `exploratory/rust-core-gateway/merged/tools/feature-matrix/README.md`
- `exploratory/rust-core-gateway/merged/tools/feature-matrix/verify.sh`
- `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md`

No external reference project source was read for this plan.

## Owner Summary

1. 做了什么: 形成 W11-F F-2 L1 TLS 缺口闭环的 Codex 独立平行 plan draft。
2. 改了哪些文件: 计划保存为 `docs/process/plans/2026-05-24-w11-f-f2-l1-tls-gap-closure-codex.md`。
3. 为什么这样做: CLAUDE.md #10 要求 Claude/Codex 独立成稿后再交叉讨论合成，且该任务涉及生产 TLS 指纹门禁，属于非平凡计划工作。
4. 有没有功能缩水: 没有。计划要求 known-gap / unsupported / L2-incomplete 都显式保留为阻断、Feature Flag off 或 Mandatory Roadmap，不删除功能。
5. 有没有 clean-room 风险: 当前低。计划只使用 HUAKAI 内部 docs/code，未读外部 reference project source。
6. 有没有安全风险: 文档本身无运行时安全风险；实现阶段的主要安全点是 L1 preflight 必须 fail-closed，不能降级为 warning。
7. 哪些地方需要 Owner 确认: D-F2-1 到 D-F2-5，尤其是启动错误形态、生产启动 preflight 机制、L1-only 是否允许生产、capture recency、是否新增 `l1_preflight.rs`。
8. 下一步建议: 与 Claude 独立稿对比，记录 agreements/conflicts/gaps，合成无后缀 authoritative plan 后再执行。
