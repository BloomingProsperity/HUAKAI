# Rust hardening c942a27 Codex review

| Field | Value |
|---|---|
| Review target | `claude/rust-hardening @ c942a27304617c2770f6f486f2e134b242d637ce` |
| Review time | 2026-05-27T05:15:31Z |
| Review tool | `codex exec review --sandbox read-only` (xhigh, read-only sandbox) |
| Review lane | reviewer |
| Raw output | `/tmp/codex-rust-review-output.txt` (Owner-provided summary; raw output not re-read for this archival note) |
| Owner decision | Rust S1 交回 Rust 分支实现者修；Claude 主线继续 Go，不接 Rust 修复。 |

## Conclusion

**不合 main。** c942a27 仍有 5 个 S1：CI 真绿证据、生产 dispatch 文档、ALPS/cert_compression 判别测试、§14c 收尾、四 vendor PASS/method tag 均不足以支持合入。

## S1 Blockers

### S1-1 CI 真绿证据不足

Evidence: `docs/process/release-readiness/W11-W12-execution-summary.md:4` 记录的 branch head 是 `99138ae`，不是 review target `c942a27`；同文件 `docs/process/release-readiness/W11-W12-execution-summary.md:114` 和 `docs/process/release-readiness/W11-W12-execution-summary.md:115` 仍把 clippy `-D warnings` 与 `cargo deny` 标为 pending。

Rationale: release roll-up 不能证明 c942a27 的完整 gate 已过；required checks pending 属于 landing 前 S1。

Owner decision: 交回 Rust 实现者，用当前 Rust 分支 head 重跑并落档 clippy / cargo deny / required cargo matrix。

### S1-2 Gemini production dispatch 文档高估

Evidence: `docs/process/release-readiness/W11-W12-execution-summary.md:64` 写 Gemini production dispatch 为 `AllowBoring`；但 production builder 在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:99` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:102` 对非 dispatchable L1 status 直接返回 error；`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/l1_preflight.rs:39` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/l1_preflight.rs:42` 明确 Pending 必须 runtime preflight，未验证前 fail-closed。

Rationale: `AllowBoring` 只是 resolver 级说法；生产 builder 的 F-2.3a runtime preflight 未闭合时，文档不能宣称 Gemini 已可 production dispatch。

Owner decision: 交回 Rust 实现者，要么改文档为 fail-closed/pending，要么补齐 runtime preflight 后再宣称 production dispatch。

### S1-3 §14b ALPS / cert_compression 测试不判别 payload

Evidence: implementation 确实在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/client_hello_builder.rs:97` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/client_hello_builder.rs:100` wiring cert_compression，并在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/client_hello_builder.rs:134` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/client_hello_builder.rs:137` wiring ALPS；但 capture fixture 在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/wire_capture_fixture.rs:24` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/wire_capture_fixture.rs:35` 只暴露 parsed field 列表，没有 ALPS/cert_compression payload 字段；`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs:95` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs:110` 只断 extension order / JA3 / SNI。

Rationale: 如果 payload 写错或被空 payload 替代，当前测试仍可能绿，违反 CLAUDE.md #14 的判别性测试纪律。

Owner decision: 交回 Rust 实现者，补 payload 级 capture / assertion，不能只用 extension presence 或 JA3 证明 §14b。

### S1-4 §14c cert_compression 收尾不完整

Evidence: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/cert_compressor.rs:60` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/cert_compressor.rs:72` 只是 brotli decompression wiring；`docs/process/release-readiness/W11-W12-execution-summary.md:96` 到 `docs/process/release-readiness/W11-W12-execution-summary.md:99` 明记真实 `cloudcode-pa.googleapis.com` production integration test deferred。

Rationale: 有实现 wiring 但缺 payload 单测与真实 cloudcode-pa handshake 证据，不能把 §14c 当作 release-complete。

Owner decision: 交回 Rust 实现者，补 discriminating unit test；真实 cloudcode-pa 集成测若继续 deferred，必须降级为 pending/roadmap 而不是 PASS。

### S1-5 四 vendor PASS / method tag 不成立

Evidence: `docs/process/release-readiness/W11-W12-execution-summary.md:59` 到 `docs/process/release-readiness/W11-W12-execution-summary.md:64` 汇总表把四 vendor L1 wire byte-level 写成 PASS，其中 Kiro 行给出 local in-memory PASS；但 `docs/process/release-readiness/W11-F-F1-status.md:614` 到 `docs/process/release-readiness/W11-F-F1-status.md:619` 明确 Kiro 是 **WEAK locked**，且 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs:177` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs:185` 仍把 `real_upstream_capture` 标为 known gap。

Rationale: local in-memory boring wire PASS 不能等同 real-upstream method PASS；Kiro 的 KnownGap 与 release summary 的 PASS/method tag 冲突。

Owner decision: 交回 Rust 实现者，修正 vendor status 与 method tag；未补 real-upstream capture 前不得把 Kiro 计为 PASS。

## S2/S3 Deferred Tickets

- **S2 dispatch 负例在 boring feature 下被跳过**: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:333` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:339` 解释 boring feature 下语义模糊并 gate 到 `not(mimicry-boring)`；`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:373` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:375` 对第二个负例同样跳过。Defer: 记录为 intent-strict / AllowBoring 替代语义 follow-up。
- **S2 生产 builder 文档与代码相反**: comment 在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:58` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:63` 写 `Pending` 可返回 `Ok(client)` 并等 runtime preflight；代码在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:99` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:102` 实际 fail-closed。Defer: 行为若保持 fail-closed，注释必须改。
- **S3 覆盖来源注释引错**: `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:227` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs:233` 说 no-feature fail-closed 已由另一个 dispatch test 锁住；实际覆盖在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/anthropic_test.rs:211` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/anthropic_test.rs:237`。Defer: 注释 provenance cleanup。

## Confirmed PASS

- **clean-room PASS**: 本次落档未读 sub2api / new-api / all-api-hub / one-api 等 LGPL/AGPL/GPL reference source；Rust plan 在 `exploratory/rust-core-gateway/PLAN.md:19` 到 `exploratory/rust-core-gateway/PLAN.md:20` 明确外部源码 clean-room 边界，`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/cert_compressor.rs:17` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/cert_compressor.rs:23` 明记 original HUAKAI code + public API 约束。
- **frozen package PASS**: `AGENTS.md:591` 到 `AGENTS.md:598` 冻结的是 Go `backend/internal/gatewayhttp` / `gateway` / `proto` 新文件；`git show --name-status c942a27` 只显示修改既有 Rust 测试文件 `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs`，未向 Go frozen packages 新增文件。

## Owner Key Corrections

- Owner 之前“Rust 全部完成”的判断来自过期 roll-up：`docs/process/release-readiness/W11-W12-execution-summary.md:4` 仍是 head `99138ae`，且 `docs/process/release-readiness/W11-W12-execution-summary.md:114` 到 `docs/process/release-readiness/W11-W12-execution-summary.md:115` 仍有 required checks pending。
- Gemini `AllowBoring` 是 resolver 级状态；生产 builder 在 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:99` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs:102` 对 Pending fail-closed，F-2.3a runtime preflight 未完成。
- Kiro L1 `PASS` 与 `docs/process/release-readiness/W11-F-F1-status.md:619` 的 **WEAK locked** 冲突；`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs:177` 到 `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs:185` 仍保留 `real_upstream_capture` known gap。

## Follow-up Tracking

Rust 实现者修完 5 个 S1 后再交回 review：先 stage intended diff，跑本地 required checks，再按 CLAUDE.md #8 执行 per-commit Codex review。Landing gate 是无 unresolved S0/S1：`CLAUDE.md:57` 到 `CLAUDE.md:64` 明确 2-round cap、S0/S1 必修、S2/S3 记录 defer；`AGENTS.md:519` 到 `AGENTS.md:523` 明确 Round 1 / Round 2 条件与 stop rule。若修复 materially changed behavior / tests / clean-room posture，必须跑 Round 2；仍有 S1 时不得合 main。

## Source Files Read

- `AGENTS.md`
- `CLAUDE.md`
- `docs/process/release-readiness/W11-W12-execution-summary.md`
- `docs/process/release-readiness/W11-F-F1-status.md`
- `exploratory/rust-core-gateway/PLAN.md`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/proxy_engine/http_client.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/l1_preflight.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/client_hello_builder.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/wire_capture_fixture.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/boring_wire.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/cert_compressor.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/tls_profile.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/mimicry_dispatch_test.rs`
- `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/anthropic_test.rs`

Lane: reviewer  
Agent: Codex GPT-5  
UTC timestamp: 2026-05-27T05:15:31Z

Owner 中文摘要: 这次 Codex reviewer-lane 结论是不合 main，最高优先级是 5 个 S1：补 c942a27 当前 head 的真 CI 证据、修正 Gemini production dispatch 高估、补 ALPS/cert_compression payload 判别测试、把 §14c 从 wiring 推到可验证闭合、修正四 vendor PASS/method tag 尤其 Kiro WEAK 冲突。S2/S3 已按 defer 记票；clean-room 与 frozen package 两项 PASS。Rust 修复必须由 Rust 分支实现者完成，Claude 主线继续 Go，不接 Rust 修复；修完后按 CLAUDE.md #8 两轮 cap 重新 review，无 unresolved S0/S1 才能再谈合 main。
