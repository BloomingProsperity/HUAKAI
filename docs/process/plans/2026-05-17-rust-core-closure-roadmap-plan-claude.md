# Rust Core Closure Roadmap Plan (首期目标)

| Owner directive | "首期目标给核心 rust 部分闭环" — Owner 2026-05-17 |
| --- | --- |
| Scope | In: `exploratory/rust-core-gateway/merged/crates/core_gateway/` 数据面 + 反封禁 stack Rust impl + 8 vendor mode Rust adapter + 真 upstream smoke. Out: frontend (Owner 冻结) / Go backend (Phase 6 商业已闭环, 不再动) / LICENSE / 计费核心 |
| Success criteria | (a) `cargo test --workspace` PASS + 1k 并发 load smoke P99 < 100ms; (b) 8 vendor (Anthropic/OpenAI/Gemini/Cursor/Codex/Antigravity/Bedrock/Vertex) Rust mimicry profile + stream_pipeline 全 done; (c) Anthropic Lane 2b reattach (KnownGapBlocked 解); (d) rquest+BoringSSL 替 hyper-rustls 作 outbound; (e) L3/L4/L5/L6 反封禁 layer Rust impl + AT 覆盖; (f) Owner 本机真账号 smoke 通过 |
| Time estimate | 8 wave × 1-3 hr codex (后台) = 总 16-30 hr codex 时间 + Owner 本机真 upstream smoke 验证 |
| Blast radius | Rust 数据面 (16K 行 + 6K 测试). 改动主要在 mimicry / stream_pipeline / proxy_engine / 新 crate (request_pacing / outbound_ip_pool / device_fingerprint / anti_detect_orchestrator) |
| Failure modes | (a) rquest BoringSSL 编译环境依赖 (sandbox 可能限制); (b) http2 fork 跟 stock h2 行为分歧致 stream 解析 break; (c) 6 vendor SSE 协议各异 (e.g. Gemini server-sent JSON-streaming 不同 OpenAI delta); (d) 反封禁 Rust 实施量大, 单 wave 不能 over-scope; (e) 真 upstream smoke 需 Owner 本机 (sandbox 不可) |
| Mitigations | 每 wave 单一目标 sub-scope (避 over-scope); rquest 在 dev env 先验; http2 fork 跟 stock 双 path 兼容; vendor SSE 各 vendor 写测 fixture; Owner 真 upstream smoke 作 release gate, sandbox 内 mock 兜底 |
| Decision points | (a) wave 之间 Owner 是否 surface 复审 (推: 每 wave done 后短报); (b) 真 upstream smoke 何时跑 (Owner 本机时机); (c) Anthropic 反封禁 OCAW 是否解锁 (反代敏感, 但 spec 已 commit 158c421 + a122a16, impl 可推) |
| Pre-execution checklist | (a) 当前 Rust crate state 已 explore (本 plan §4 锚定); (b) 7 反封禁 spec 全 commit (L0-L6); (c) ≤3 codex 并行; (d) plan artifact 写 (本文件); (e) verify build/test 后 commit + push 每 wave |

## Rust 现状 (commit 96bb888 基线)

**已闭环**:
- ✓ Core data plane: listener → route_client → proxy_engine → stream_pipeline (15.8K Rust + 5.8K test)
- ✓ gRPC control plane (UDS / mTLS, 4 RPC method: RouteQuery / AttemptReport / HealthCheck / Heartbeat)
- ✓ TransportBaseline {UDS, mTLS} 真 wire 进 GatewayState
- ✓ tonic TLS feature 激活 (Cargo.toml + cert/key/CA 解析)
- ✓ 68 unit test + 15 integration test PASS
- ✓ 1k 并发 load smoke (READINESS.md M-rust-10) — 1000/1000 PASS, P99=2843ms
- ✓ Mimicry profile 3 builtin (CodexCli / KiroCli / GeminiAdvanced)
- ✓ http2 fork 集成 (feature-gated `mimicry-http2-fork`)
- ✓ OpenSSL adapter 骨架 (backend_resolver.rs)

**未闭环 (本 plan 目标)**:
- ✗ **Anthropic mimicry KnownGapBlocked** (backend_resolver.rs:48-50: "pending Lane 2b reattach")
- ✗ **rquest crate 集成** (Cargo.toml:33-35 comment "R-E+1 切 rquest+BoringSSL", 当前 hyper-rustls 占位)
- ✗ **6 vendor mimicry profile** 未实施 (Anthropic/Cursor/Antigravity/Bedrock/Vertex)
- ✗ **stream_pipeline 仅 2 vendor** (Anthropic/OpenAI), 缺 6 (Gemini/Cursor/Codex/Antigravity/Bedrock/Vertex)
- ✗ **7 反封禁 stack Rust impl 全 0** (L3 F-FP-001 / L4 F-PACE-001 / L5 F-NET-001 / L6 F-ADV-001)
- ✗ **真 upstream smoke** (Owner 本机时机)

## 8 Wave 闭环顺序 (按 dependency + blast radius)

### Wave R-1 (P0, 1-2 hr codex): Anthropic Lane 2b reattach + OpenSSL adapter 完整
- 解 backend_resolver.rs:48-50 KnownGapBlocked
- Anthropic profile JSON 加进 mimicry/profile.rs (跟现有 3 builtin 同 pattern)
- OpenSSL adapter 完整 impl (当前是骨架)
- 测试: `cargo test --features mimicry-openssl` PASS
- AT-MIMICRY-001 (Anthropic 指纹真用 OpenSSL adapter + TLS handshake 跟 真 Claude Code 一致)
- 不引新 dep (复用 openssl crate)

### Wave R-2 (P0, 2-3 hr codex): rquest + BoringSSL 数据面集成 (R-E+1)
- 加 rquest crate (Cargo.toml)
- proxy_engine/http_client.rs 替 hyper-rustls 为 rquest
- 兼容 mimicry profile (rquest 自带 Chrome/Firefox/Safari 模板, mimicry profile 选 builtin emulator)
- AT-RQUEST-001..003 (outbound TLS handshake JA3/JA4 跟 Chrome 137+ 一致 + h2 settings + alpn 顺序)
- hyper-rustls Cargo.toml 移除 (burn-the-boats per D3 memory)
- 测试: 现有 68 unit + 15 integration 全 PASS

### Wave R-3 (P1, 2-4 hr codex): 6 vendor mimicry profile + stream_pipeline parser
- 加 6 builtin profile JSON (Gemini / Cursor / Codex / Antigravity / Bedrock / Vertex)
- stream_pipeline/mod.rs 加 6 vendor StreamProtocol 解析 (Gemini server-sent JSON / Cursor proprietary / Codex stream / Antigravity custom / Bedrock EventStream / Vertex chunked JSON)
- 各 vendor SSE fixture 测试
- ProfileVendor enum 扩展 (当前仅 3, 加 6 = 9 total)
- AT-STREAM-001..006 (per vendor parse correctness)

### Wave R-4 (P1, 2-3 hr codex): L3 F-FP-001 device fingerprint Rust impl
- 新 crate `crates/device_fingerprint/`
- per-account profile binding (跟 spec docs/specs/device-fingerprint-binding.md 一致)
- 12 dimension impl (HTTP-layer 1-3 + part 8 真注入, JS-runtime 4-12 metadata only)
- DR-001 tenant_id 强制
- AT-FP-001-001..010 全 PASS (spec line 145-160)
- 跟 L1 rquest 集成 (Wave R-2 完成后)

### Wave R-5 (P1, 2-3 hr codex): L4 F-PACE-001 节奏 Rust impl
- 新 crate `crates/request_pacing/`
- profile_registry + pacing_planner + streaming_consumer + burst_controller + session_tracker + diurnal_modulator (跟 spec docs/specs/request-pacing-mimicry.md 一致)
- 7 vendor profile 真采样 placeholder (待 Owner 真数据回填)
- AT-PACE-001-001..010

### Wave R-6 (P1, 2-3 hr codex): L5 F-NET-001 IP 池 Rust impl
- 新 crate `crates/outbound_ip_pool/`
- pool_registry + binding_allocator + proxy_client + health_probe + burn_correlator (跟 spec docs/specs/outbound-ip-pool.md 一致)
- 4 binding 策略 (stable_per_account 默认 / stable_per_session / rotate_per_request / manual_pin)
- proxy_client wrap rquest http client (Wave R-2 完成后)
- Go control plane 同步 binding state (gRPC RPC 加)
- AT-NET-001-001..012

### Wave R-7 (P1, 2-3 hr codex): L6 F-ADV-001 主动对抗 Rust impl
- 新 crate `crates/anti_detect_orchestrator/`
- detector + policy_engine + rotator + drift_monitor + policy_listener + ban_correlator (跟 spec docs/specs/active-anti-detection.md 一致)
- 7 detection class × 7 action class rule engine
- 跨层联动 (L1 rotate + L3 burn + L4 adjust + L5 IP burn + F-CH cooldown)
- AT-ADV-001-001..012

### Wave R-8 (P2, Owner 本机 smoke): 真 upstream 验证
- Owner 本机跑 真 Anthropic / OpenAI / Gemini account
- 验 mimicry profile 跟真 vendor client 一致 (TLS handshake / h2 settings / header order / SSE format / pacing distribution)
- 验 IP pool binding 真 outbound IP 稳定
- 验 L6 active detection 真 vendor probe 检测
- 验 P99 latency 跟 baseline (无 mimicry 直 HTTP) 增 < 30%
- 验 release gate: critical security + critical bug 全 0

## Recommended Execution Order (按 ROI + dependency)

1. **Wave R-1** (Anthropic 解锁) — 立刻 dispatch, 1-2 hr
2. **Wave R-2** (rquest 集成) — 拓宽数据面, 2-3 hr
3. **Wave R-3** (6 vendor) — 8 vendor 全闭环, 2-4 hr
4. **Wave R-4** (L3 device fingerprint) — 反封禁基础, 2-3 hr
5. **Wave R-5** (L4 节奏) — 行为伪装, 2-3 hr
6. **Wave R-6** (L5 IP 池) — 网络层伪装, 2-3 hr
7. **Wave R-7** (L6 主动对抗) — 顶层 orchestrator, 2-3 hr
8. **Wave R-8** (Owner 真 upstream smoke) — release gate, Owner 排期

总 16-21 hr codex + Owner 真 smoke. 每 wave 完独立 commit + push.

## Critical Files

- **Wave R-1**: `exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/backend_resolver.rs` + `openssl_adapter.rs` + 新 `mimicry/profiles/anthropic_claude_code.json`
- **Wave R-2**: `Cargo.toml` (加 rquest dep + 移 hyper-rustls) + `proxy_engine/http_client.rs`
- **Wave R-3**: `mimicry/profile.rs` (ProfileVendor enum 扩 9) + `stream_pipeline/mod.rs` + 6 个新 vendor parser
- **Wave R-4**: 新 `crates/device_fingerprint/` (full crate)
- **Wave R-5**: 新 `crates/request_pacing/` (full crate)
- **Wave R-6**: 新 `crates/outbound_ip_pool/` + Go control plane gRPC RPC 加
- **Wave R-7**: 新 `crates/anti_detect_orchestrator/` (full crate)
- **Wave R-8**: Owner 本机 smoke runner + report

## Verification

每 wave 完成:
1. `cd exploratory/rust-core-gateway/merged && cargo build --workspace` PASS
2. `cargo test --workspace --all-features` PASS
3. AT 列表 PASS in 11 matrix sync
4. codex per-commit review (`codex exec review --uncommitted`)
5. commit + push 到 origin/claude/phase-1

最终 wave R-8 完成后:
1. Owner 本机真账号 smoke 通过 8 vendor
2. README 更新 production-ready
3. release tag

## 中文摘要

Rust 核心闭环 plan (8 wave). Owner directive "首期目标 Rust 闭环". 当前 Rust crate 已闭环 control plane (commit 96bb888), 数据面缺 Anthropic / rquest / 6 vendor / 7 反封禁层 / Owner 真 smoke. 按 dependency 顺序 8 wave: R-1 Anthropic 解锁 → R-2 rquest → R-3 6 vendor → R-4-7 反封禁 stack 4 层 → R-8 真 upstream smoke. 总 16-21 hr codex + Owner 真 smoke. Frontend 冻结. Go backend 已闭环 Phase 6 不动. 每 wave 独立 commit + push, Owner 可随时 redirect.
