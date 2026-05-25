# W11-F F-1 — L2 HTTP/2 fork → ProxyEngine 真接线: Status

**Date**: 2026-05-25 UTC (F-1.a evidence + fixture contract phase landing)
**Owner**: HUAKAI
**Plan reference**: `docs/process/plans/2026-05-25-w11f-f1-l2-http2-jiexian-synthesis.md`
(Owner approved 全推荐 D1-D10 default path on 2026-05-25)
**Branch**: `claude/rust-hardening` (not pushed)

## 1. F-1 epic scope

Per the synthesis (Owner-approved 2026-05-25), F-1 covers 7 sub-phases:

| sub | scope | status |
|---|---|---|
| **F-1.a** | Evidence + fixture contract | **IN PROGRESS** (this commit) |
| **F-1.b** | Adapter true-IO extraction (`http2_adapter.rs` accepts generic AsyncRead+AsyncWrite) | OPEN |
| **F-1.c** | L2 preflight module (typed gate, byte comparison) | OPEN |
| **F-1.d** | ProxyEngine transport boundary (behavior-preserving seam) | OPEN |
| **F-1.e** | HTTP/2 fork outbound client (real TCP/TLS via boring_tls_connector) | OPEN |
| **F-1.f** | Builder + dispatch wiring (build-time L2 preflight) | OPEN |
| **F-1.g** | Profile backfill + byte tests + release evidence | OPEN |

## 2. Per-profile H2 capture state (this commit's truth-first audit)

The F-1 epic's Released status depends on per-profile real-upstream HTTP/2
fixture evidence. Audit run 2026-05-25 (`scripts/check_h2_availability.py`-style
inline Python over the 4 built-in profile JSONs):

| BuiltinProfile | h2_settings_frame.available | h2_pseudo_header_order.available | fixture under tests/fixtures/http2_fingerprint/ | F-1 Released eligibility |
|---|---|---|---|---|
| `AnthropicClaudeCode` | `false` (limitation_note: "collector v1 未捕获 HTTP/2 SETTINGS order/value") | `false` | `anthropic-claude-code-h2.json` absent | **NOT ELIGIBLE** — needs capture, blocked on F-1.g |
| `CodexCli` | not declared (treated as `false`) | not declared | `codex-cli-h2.json` absent | **NOT ELIGIBLE** — needs capture, blocked on F-1.g |
| `KiroCli` | not declared (treated as `false`) | not declared | `kiro-cli-h2.json` absent | **NOT ELIGIBLE** — needs capture (Kiro CLI account required) |
| `GeminiAdvanced` | not declared (treated as `false`) | not declared | `gemini-advanced-h2.json` absent | **NOT ELIGIBLE** — needs capture, blocked on F-1.g |

**Bottom line**: 0 of 4 built-in profiles currently have real HTTP/2 capture
evidence. F-1.b-f code work can proceed against synthetic
`codex_profile_with_h2_order` helper (per `tests/mimicry_http2_adapter_test.rs:79-109`,
explicitly labeled "synthetic L2-A6"), but **NO profile can be marked F-1
Released** until a real-upstream fixture lands under `tests/fixtures/http2_fingerprint/`
AND the cross-check tests in `tests/mimicry_http2_fixture_test.rs` go green
without trivial pass.

## 3. F-1.a deliverables (this commit)

1. **`crates/core_gateway/tests/fixtures/http2_fingerprint/README.md`** (NEW) —
   fixture schema, filename convention, cross-check test description, capture
   method, non-goals (no source mining, no synthetic top-up, no historical
   backfill without re-capture).

2. **`docs/process/release-readiness/W11-F-F1-status.md`** (this file) — initial
   state, per-profile audit, F-1.g pre-requisite block.

3. **`crates/core_gateway/tests/mimicry_http2_fixture_test.rs`** (NEW) —
   cross-profile consistency tests:
   - `fixture_exists_when_profile_marks_available` (4 cases, one per builtin):
     for each profile, if `h2_settings_frame.available=true`, fixture file MUST
     exist + deserialize + match profile fields. Today: all 4 profiles
     `available=false`, all 4 cases pass trivially.
   - `fixture_absent_when_profile_marks_unavailable` (4 cases): for each profile
     with `available=false`, fixture file MUST NOT exist (protects against
     stale fixtures lingering after a profile regression).
   - `synthetic_helper_remains_for_adapter_tests`: documents that
     `codex_profile_with_h2_order` synthetic helper in
     `tests/mimicry_http2_adapter_test.rs:79-109` is preserved for
     adapter-level regression coverage but does not satisfy F-1 Released
     evidence.

## 4. Acceptance for F-1.a

- ✅ All 3 deliverables landed in this commit
- ✅ Tests in `mimicry_http2_fixture_test.rs` pass under default features AND
  under `--features mimicry-boring,mimicry-http2-fork`
- ✅ No profile JSON modified (per CLAUDE.md #14 mutation: the fixture-existence
  invariant is the discriminator, not profile content)
- ✅ Codex per-commit review on F-1.a commit returns 0 P1 findings, P2 ≤ 2 rounds

## 5. F-1 cumulative Released criteria (re-stated from synthesis §4.3)

F-1 epic Released = ALL of:

1. `cargo test -p core_gateway --no-default-features` green (lib + integration
   + bin + doc 默认全跑, NO `--lib` filter)
2. `cargo test -p core_gateway --features mimicry-http2-fork` green — **NO `--lib`
   filter** (round 2 Codex P1 fix: `--lib` 过滤会跳过 `tests/` 下的 integration
   suites — `mimicry_http2_fixture_test` 已在 tests/, F-1.b 的 loopback h2 server
   测试 + F-1.c 的 preflight 测试也都将在 tests/. 同步 synthesis §4.3 #2/#3)
3. `cargo test -p core_gateway --features mimicry-boring,mimicry-http2-fork` green
   — **NO `--lib` filter**, 同上理由
4. ≥1 profile has real-upstream H2 fixture under `tests/fixtures/http2_fingerprint/`
   AND `mimicry_http2_fixture_test::fixture_exists_when_profile_marks_available`
   runs non-vacuously (i.e., at least 1 profile has `h2_settings_frame.available=true`
   AND backing fixture file with matching `raw_order` + `values` + pseudo-header
   order)
5. **For every F-1 Released profile**: `profile.alpn_protocols` MUST include
   `"h2"` AND F-1.g real-upstream capture evidence MUST show ALPN was negotiated
   to `h2` on the live HTTPS handshake (round 2 Codex P1 fix: the current
   anthropic_claude_code.json:99-100 has alpn_protocols=`["http/1.1"]` only;
   without h2 in the negotiable list, the BoringSSL ALPN selection at
   `boring_tls_connector.rs:176-178/249-258` will pick h1 and the fork client
   will reject the connection — every Anthropic real request fails-closed
   regardless of how perfect the SETTINGS / pseudo-header bytes are. F-1.g
   MUST refresh `alpn_protocols` from real capture AND record the negotiated
   ALPN in the fixture's `tls_alpn_negotiated` field per the F-1.a schema)
6. L2 preflight gate (F-1.c, F-1.f) wired into combined-feature builder
7. `build_mimicry_action` maps L2 errors to `Block*` actions, no panic
8. `tools/feature-matrix/verify.sh` extended with `mimicry-boring,mimicry-http2-fork`
   combination matrix, all green
9. Perf gate: p99 relay latency increase ≤ 5% (vs baseline) after F-1.e + F-1.f
10. Per-profile state declared in this doc: Released / Feature Flag / Safe
    Equivalent / Mandatory Roadmap

## 6. Open follow-ups (from synthesis §4.5)

- **OOS-A** Vendor `0x676e67/http2` fork (~0.5 day)
- **OOS-B** H2 GREASE (RFC 8701) (~1 day)
- **OOS-C** F-1-platform-h2-divergence (if Windows schannel leaks into h2 path) (~0.5 day)
- **OOS-D** F-2.5-Gemini-h2 capture (HTTP/2-triggering gemini operation) (~0.5 day)
- **OOS-E** F-2.5-Kiro upstream capture (Kiro CLI account required) (~1 day)
- **OOS-F** H2 connection pooling (D7-B upgrade)
- **OOS-G** Template revision across CLIs based on real capture
- **OOS-H** First-request L2 preflight + periodic recert (D10-B upgrade)
- **OOS-I** Bench harness (hdrhistogram p50/p95/p99 auto-bench + regression gate) (~1 day)

Cumulative follow-up ~5-6 codex-day, not in F-1 epic commitment.

## 7. Mutation discipline (CLAUDE.md #14) applied to this doc

This is an evidence / status document, not code. Mutation discrimination format
does not apply directly. The discriminating property is: if the cross-check
tests in `mimicry_http2_fixture_test.rs` are deleted or weakened, a future
implementer could add `h2_settings_frame.available=true` to a profile without
adding the corresponding fixture file — and nothing would fail. The tests in
this commit prevent that.
