# W11 + W12 execution summary (post-§14b.5, post-Codex-review S1-fix)

Last updated: 2026-05-27 UTC (post Codex review S1-fix)
Branch: `claude/rust-hardening` (head: see latest commit on branch — running
total after the S1-fix commit. The Codex reviewer 2026-05-27 caught that
earlier "head: 99138ae" was stale; updating this line at each commit is
itself an S1-1 requirement.)
Validation: Docker `cargo test --features mimicry-boring --locked` →
**462 passed, 0 failed, 1 ignored** (the 1 ignored is an env-gated network
smoke). Codex reviewer S1-3 / S1-4 added new payload-discriminating tests
(`gemini_advanced_boring_client_hello_emits_chrome_payloads` +
`brotli_compressor_decompress_round_trip` +
`brotli_compressor_decompress_rejects_malformed_input`) that lift the
sample count by 3.

**Important scope caveat (per Codex review 2026-05-27)**: "complete" in
this doc means **code-complete + tests green at all 3 feature combos**.
It does NOT mean "production canary unblocked for all 4 vendors" —
canary unlock is gated on F-2.5 real-upstream handshake evidence which
is Owner-coordination work (see §5 below). The earlier wording conflated
these two and the reviewer correctly flagged that as misleading.

This doc consolidates the post-execution state of every P0 / P1 work item
from `docs/process/plans/2026-05-23-rust-tree-closure-synthesis.md §5`. It
does NOT replace the per-slice history docs (`W11-F-F1-status.md`,
`W11-F-F2-5-status.md`, `W11-F-section14b-status.md`) which remain the
detailed evidence trail; this is the cross-cutting roll-up Owner asked
for during the §14b.5 wrap-up.

## §1 P0 — all 10 items closed

| # | Synthesis ID | Module / file | Commit(s) | Verification |
|---|---|---|---|---|
| **P0-1** | W11-D-2 B1 + B2 + TI-1 | `config.rs` + `listener.rs` + `attempt_reporter/types.rs` | `48a8af8` | listener mock 分支 emit explicit attempt event with `error_class` containing "mock"; production fail-fast on accidental mock pin |
| **P0-2** | W11-A D-1a | `listener.rs` + `account_planner.rs` | `c0c2c0d` | body parse 一次 → `requested_model` / `stream` 从 body 派生；header 不再 authoritative |
| **P0-3** | W11-C D-3 | `proxy_engine/endpoint_guard.rs` + `account_planner.rs` | `8d46be7` | https-only + 私网/loopback/reserved IP 阻断 + DNS rebinding guard |
| **P0-4** | W11-D D-6 | `proxy_engine/headers.rs` | `820342b` | strip `openai-organization` / `openai-project` / 残留 `authorization` / `x-api-key`；route plan 注入版本到达上游 |
| **P0-5** | W11-E D-10 | `mimicry/backend_resolver.rs` | `add5295` | resolver 先调 `backend_intent()`；feature 旗子不能绕过 KnownGap / UnsupportedTemplate；mutation marker at line 86 |
| **P0-6** | W11-F precondition | `tests/mimicry_*.rs` (3 files) | §14b.4 (4865a41) + §14b.5 (99138ae) | 6+ red dispatch / profile / capture-diff tests realigned to new F-2.2 D-S3 + §14b.2 contract |
| **P0-7** | W12-A D-4 (3 sub-slices) | `attempt_reporter/spool.rs` + worker | `62b304f` + `052cd61` + `3cbe402` + `a5244ad` + `7b4c1da` + `165f9bc` + `7475f68` | durable spool + replay worker + reserve() pre-commit + post-commit loud + 413 attempt 补漏 + disk-full quarantine + file integrity |
| **P0-8** | W12-B D-5 | `proxy_engine/relay.rs` + `stream_pipeline.rs` | `0776aaa` | 非流式 2xx body 抽 OpenAI / Anthropic usage → `tokens_used.source = response_body` 或 `pending_reconciliation` |
| **P0-9** | W12-C D-7 | `heartbeat.rs` + `attempt_reporter` + `resource_limits.rs` | `6933e0c` | 拉真 in-flight + 真 attempt_report_queue_depth + started_at |
| **P0-10** | W12-E D-9 + O-2 | `proxy_engine` + `resource_limits` + metrics | `be38b0f` | 真实 inbound body 字节计量（包装 body）+ `ACTIVE_CONNECTIONS` / `OPEN_UPSTREAM_CONNECTIONS` gauge lifecycle |

## §2 P1 — all 7 items closed

| # | Synthesis ID | Module / file | Commit | Notes |
|---|---|---|---|---|
| **P1-1** | W11-A D-1b Phase 1 (+ Phase 2A) | `client_auth/credential.rs` + Phase 2A.1-2A.4b | `7e10069` + `a579517` + `4977915` + `3a84bdd` + `1d537ff` + `5073550` | client credential 派生字段 + Manual First flag + Go control plane skeleton + dual-write 对账 + 6 reconciliation 场景集成测试。**Phase 2B (claim contract) deferred per Owner OD-2** — needs Go control plane consumer commit before enabling read side. |
| **P1-2** | W12-D D-8 | `attempt_reporter` retry classifier | `ffcd932` | 429 / 408 retryable; 401 / 403 保持 non-retryable |
| **P1-3** | W11-F L1 canary fail-closed | `mimicry/dispatch.rs` | `5c28e80` | profile 缺 L1 capture 证据 → 生产 dispatch fail-closed |
| **P1-4** | W11-F L2 not-wired gate | `mimicry/http2_adapter.rs` + `proxy_engine` | `5c28e80` (combined with P1-3) | 显式标 HTTP/2 fork 未接进 ProxyEngine；生产 dispatch 拒绝 L2-only profile |
| **P1-5** | mock fault knobs | `mock_control_plane.rs` + spool fixtures | `7475f68` | disk-full / real-load / bad-JSON / replay duplicate ack 测试钩子（test code only） |
| **P1-6** | Feature-matrix CI | `Cargo.toml` + CI scripts | `2c6d0c4` + `3391d3c` + `07a51bb` | cargo test matrix：default + `tls` + `mimicry-boring` + `mimicry-http2-fork` 各跑一次 |
| **P1-7** | M1 L1.B mock 凭据剥除 | `listener.rs` redaction | `3a0377a` | mock 分支前剥除 `Authorization` / `x-api-key` / `cookie` + 共享 redaction 名单 + counter 审计 |

## §3 P2 — intentionally deferred

Per synthesis §5.3, P2 items are 暂缓 (deferred). They are NOT pending
blockers; they're conscious de-scope. Listed here so Owner can re-prioritize
when a future wave wants to pick any up:

- F-3 自动切换（roadmap）
- mTLS hot-reload
- `route_cache_ttl` 复用
- heartbeat p95 / error_rate 直方图
- `attempt_reporter` worker 多线程
- 文档化未源的 heartbeat 字段（p95 / error_rate）注释

## §4 W11-F fingerprint wave — vendor profile status

This supersedes the §1 Feature Preservation Mapping in `W11-F-status.md`
(which was 2026-05-24-vintage and pre-§14b).

| Profile | L1 wire byte-level (boring) | L1 runtime preflight | Resolver decision | Production builder gate | Real-upstream capture | Canary unlock |
|---|---|---|---|---|---|---|
| **Anthropic** | ✅ PASS (local + 2026-05-06 historical) | ✅ NotRequired baseline | `AllowBoring` | ✅ `try_build_http_client_with_profile` returns Ok | ✅ historical 2026-05-06 | ✅ unblocked |
| **Codex CLI** | ✅ PASS (local) | ✅ Gate wired (F-2.3+) | `AllowBoring` via Codex exception (L2-A8 KnownGap downgrade) | ✅ Ok (gate sees `OpenSslAdapter` intent after L2-A8 path) | ⚠️ F-2.5 partial (chatgpt.com Windows pcap, Linux re-capture pending) | ⚠️ waiting on Linux re-capture |
| **Kiro CLI** | ⚠ **WEAK locked** (local in-memory only; `W11-F-F1-status.md:619` calls this WEAK, NOT PASS) | ❌ Failed (`KnownGapBlocked` — `real_upstream_capture` pending per `tls_profile.rs:177`) | `KnownGapBlocked` (gap dominates per F-2.2 D-S3) | ❌ fail-closed (gate returns `Err(MimicryProductionCanaryError::KnownGap)`) | ❌ F-2.5 pending — Owner has no Kiro subscription (permanent KnownGap per F-2 task #20) | ❌ waiting on F-2.5 (Owner-coordination) |
| **Gemini Advanced** | ✅ PASS local-in-memory + new payload assertions (`boring_wire::gemini_advanced_boring_client_hello_emits_chrome_payloads` locks ext 27 = `[2]` + ext 17513 = `["h2"]`) | ⚠️ `Pending` (L1 status is `Pending` until runtime preflight at first-connect site is wired by F-2.3a) | `AllowBoring` (cert_compression + ALPS + brotli + key_share_groups GREASE bypass) | ⚠️ **fail-closed** today because `try_build_http_client_with_profile` rejects `Pending` (`http_client.rs:99-102`). F-2.3a runtime preflight at first-connect site is the gap that flips this from Err to Ok. | ❌ F-2.5 pending — needs Gemini OAuth credentials + staging | ❌ waiting on F-2.5 + runtime preflight wire (F-2.3a) |

**Codex review S1-2 correction (2026-05-27)**: an earlier version of this
table wrote Gemini's "Production dispatch" as `✅ AllowBoring`. That was
a resolver-level statement. The production **builder**
(`try_build_http_client_with_profile`) currently rejects any profile
whose L1 preflight status is `Pending`, and Gemini is `Pending` until
F-2.3a fires runtime preflight at the first-connect site. So at the
**builder** layer Gemini is fail-closed today; it only becomes
production-dispatchable after F-2.3a lands (separate from §14b
implementation). The matrix above now splits "Resolver decision" and
"Production builder gate" as distinct columns to keep this honest.

**Codex review S1-5 correction (2026-05-27)**: Kiro was previously listed
as `✅ PASS (local in-memory)` in the L1 wire byte-level column. That
conflicted with `W11-F-F1-status.md:614-619` Gate 1 audit which
explicitly tags Kiro as **WEAK locked — permanent KnownGap** (no real
upstream evidence, Owner has no Kiro subscription, no fresh first-party
capture possible). The local boring-wire byte-match against the
2026-05-14 template stays a useful regression guard, but it is **not**
equivalent to method-tag PASS. The Kiro row now reads `⚠ WEAK locked`.

§13 + §14b notes:
- §13 (2026-05-26) re-captured all 4 vendors via passive admin-PowerShell pcap.
  Anthropic clean, Codex TLS 1.2 drift documented (platform-divergence gap),
  Gemini Chrome impersonation shape captured, Kiro permanent KnownGap.
- §14b.1 (`0a8551c`) added Chrome-style schema fields.
- §14b.1-fix (`275eb79`) gated TLS 1.3-only fields + codex JSON revert.
- §14b.2 (`3ee4e55`) wired cert_compression (RFC 8879) + ALPS (TLS ext 17513).
- §14b.3 (`4865a41`) found + fixed Gemini "ClientHello emits 0 bytes" root
  cause: BoringSSL's `ssl_setup_key_shares` default picks
  `supported_group_list[0]` which is GREASE for Chrome impersonation
  profiles → `SSLKeyShare::Create(GREASE)` returns nullptr → silent abort.
  Added Rust wrapper for stock `SSL_set1_client_key_shares` API to pass
  real (non-GREASE) groups explicitly.
- §14c (`4865a41`) added real `brotli` runtime dep + decompressor (Owner-
  approved). Stub replaced with `brotli::BrotliDecompress`.
- §14b.4 + §14b.5 realigned 9 integration tests to the new
  F-2.2 D-S3 + §14b.2 resolver classifications.

## §5 What's left — needs Owner / external coordination

These items are NOT writable by Rust solo; surfaced here so Owner can
decide cadence:

1. **F-2.5 real-upstream staging capture** for Codex (Linux re-capture),
   Kiro (Owner has no subscription), Gemini (needs OAuth credentials).
   Without these the Canary gate stays closed per synthesis D-S9.
2. **W11-A Phase 2B claim contract** (task #19) — deferred per Owner
   OD-2. Requires Go control plane committing the `derived_tenant_id`
   consumer side. Rust producer side (Phase 2A) is fully wired and
   feature-gated OFF until Phase 2B unlocks.
3. **§14c production integration test** against
   `cloudcode-pa.googleapis.com` real handshake — env-gated, needs
   Gemini OAuth creds and Linux/staging environment. Code path is
   complete; only the env wiring is missing.
4. **Phase 1 → main merge decision** (OD-5) — branch `claude/rust-
   hardening` has been pushed to `github/claude/rust-hardening` per
   Owner's "只推 github 别动 ssh origin" directive. When Owner wants
   to fold this work back into `claude/phase-1` main line, a PR / merge
   needs to be opened.

## §6 Quality gates (post-§14b.5)

| Gate | Status | Evidence |
|---|---|---|
| Full test suite (`cargo test --features mimicry-boring --locked`) | ✅ 462/462 + 1 intentionally ignored | Docker run 2026-05-27 (post-§14b.6); S1-fix commit adds 3 new payload-discriminating tests → 465/465 expected after re-run |
| Owner-reproduction (`cargo test mimicry --locked --no-default-features`) | ✅ 42 mimicry tests pass | Owner's original failing command verified clean post-§14b.6 |
| Per-vendor boring wire byte-level | ✅ 4/4 anthropic / codex / kiro / gemini wire-level JA3 match; ⚠ Kiro is local-in-memory only (see §4 Kiro WEAK note) | `boring_wire.rs` 4 tests pass after §14b.3 unlocked gemini |
| Per-vendor payload-level (ext 27 + ext 17513) | ✅ Gemini PASS (post-S1-3 fix) | `boring_wire::gemini_advanced_boring_client_hello_emits_chrome_payloads` (added 2026-05-27 per Codex review S1-3) |
| Brotli decompression unit test (§14c) | ✅ PASS (round-trip + malformed-input) | `cert_compressor.rs::tests::brotli_compressor_decompress_*` (added 2026-05-27 per Codex review S1-4) |
| `#[ignore]` regression markers | 1 intentional (env-gated network smoke) | All `#[ignore]` markers documented |
| Clippy (warnings inventory; no `-D warnings` since legacy boring-sys bindgen warnings exist outside HUAKAI control) | ✅ Builds clean | Docker run 2026-05-27: only style warnings remain (`is_multiple_of`, `io::Error::other`); the one §14b.4-introduced unreachable arm was cleared in `6cd980e` |
| `cargo deny` license / advisory | ✅ `advisories ok, bans ok, licenses ok, sources ok` | Docker run 2026-05-27 with `cargo install --locked cargo-deny` (latest); deny.toml fixed for 0.16+ keys in `6cd980e` |
| feature-matrix CI | ✅ wired (P1-6); 3 combos verified | `--no-default-features` / `--features mimicry-boring` / `--features mimicry-openssl` all green post-§14b.6 |

## §7 Owner decision points still open

| ID | Decision | Recommendation | Notes |
|---|---|---|---|
| OD-2 | Unblock Phase 2B claim contract? | Defer until Go control plane consumer is ready | Rust producer side is feature-flagged OFF; no risk in keeping deferred |
| OD-5 | Merge `claude/rust-hardening` into `claude/phase-1` main line? | Recommend after F-2.5 real-upstream capture lands | Phase 1 main line should absorb 100% of W11+W12 P0/P1 once Canary gate is unblocked |
| OD-W11F-1 | F-2.5 staging environment ready? | Owner to confirm | Blocks Canary for Codex/Kiro/Gemini |
| OD-W11F-3 | F-3 profile schema upgrade — current roadmap or remote? | Remote (P2) | sub2api 字段级 DB 启发；can wait |
