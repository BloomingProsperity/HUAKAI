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

## 8. F-1.g progress — 2026-05-26 update (server-side capture pipeline)

Related plan: `docs/process/plans/2026-05-26-f1g-server-side-capture.md`.

### 8.1 Attempt-1 (DISCARDED)

mitmproxy 12 addon at `tools/fingerprint-collector/capture_h2_settings.py`
acting in proxy mode between python-httpx and api.anthropic.com.

Failed on two axes:

1. mitmproxy 12 does NOT expose original client SETTINGS frame bytes via any
   addon API surface — every defensive introspection path
   (`flow.client_conn.h2`, `_h2_conn`, `protocol`, `metadata.h2_settings`)
   returned `None` or unavailable.
2. Driving client was `python-httpx/0.28.1` (NOT Claude Code CLI), so the
   captured header order was an httpx baseline, not Anthropic-SDK on-wire.

The addon, its .pyc cache, and the resulting JSONL stub were **deleted** in
the same commit that introduced attempt-2 (this commit). Owner ratified
discarding via in-chat A+D pick on 2026-05-26.

### 8.2 Attempt-2 (LANDED, this commit)

Server-side capture: local Python h2 server
(`tools/fingerprint-collector/h2_capture_server.py`) terminates TLS with a
self-signed cert + ALPN=h2 on `127.0.0.1:18099`. Drives the conversation
just far enough to record the client's SETTINGS frame raw bytes + HEADERS
frame raw bytes BEFORE the `h2` library state machine re-orders them, then
sends a trivial 200 response so the client closes cleanly.

Client probe (`tools/fingerprint-collector/clients/undici_probe.mjs`) sends
one POST through undici v7 with `allowH2: true` and `rejectUnauthorized: false`
to drive the cert mismatch through.

Parser unit tests (`tools/fingerprint-collector/tests/test_settings_parser.py`)
cover: known-payload baseline, on-wire order preserved against canonical sort,
single-byte mutation in value, single-byte mutation in id, invalid length
raises, empty payload, known/unknown id annotation. All 8/8 pass.

### 8.3 First evidence: undici v7 default h2 baseline

File: `tools/fingerprint-collector/captures/h2-server-1779761396.jsonl`

Real on-wire captured fields:

| Field | Value |
|-------|-------|
| `tls_version` | `TLSv1.3` |
| `tls_cipher` | `TLS_AES_256_GCM_SHA384` |
| `alpn_negotiated` | `h2` |
| `preface_matches` | `true` |
| `client_settings_frame.payload_hex` | `000200000000000400040000` (raw bytes) |
| `client_settings_frame.parameters_in_order` | `[{id:2/ENABLE_PUSH, value:0}, {id:4/INITIAL_WINDOW_SIZE, value:262144}]` |
| `client_headers_frame.pseudo_header_order` | `[:authority, :method, :path, :scheme]` (alphabetical) |
| `client_headers_frame.regular_header_order` | `[user-agent, content-type, accept, content-length]` |

Notable discriminators:

- undici v7 sends only **2** SETTINGS params (omits HEADER_TABLE_SIZE,
  MAX_CONCURRENT_STREAMS, MAX_FRAME_SIZE, MAX_HEADER_LIST_SIZE → accepts h2
  defaults).
- Pseudo-header order is **alphabetical**, not the often-assumed
  `:method, :scheme, :authority, :path`.

### 8.4 What this commit does NOT do

- **Does NOT** promote `templates/anthropic-claude-code.json`
  `h2_settings.available` to `true`. The undici baseline is *likely* what
  Claude Code CLI sends but this requires explicit confirmation before
  upgrading the profile. Owner-gated decision.
- **Does NOT** modify F-1.a fixture cross-check expectations (those still
  pass trivially because all 4 profiles remain `available=false`).
- **Does NOT** touch backend/ or any production code.

### 8.5 Per-profile Released eligibility update vs §2

No change. All 4 profiles remain `available=false`. Capture pipeline now
exists; profile promotion is the next gated step.

### 8.6 Next steps to close F-1.g

1. **Confirm undici fingerprint == Claude Code CLI fingerprint.** Either:
   - (a) Read `@anthropic-ai/sdk` transport setup to confirm it uses undici
     defaults for h2 (no `Client(..., { allowH2: true, ...overrides })`), OR
   - (b) Run real `claude` CLI through the local server via hosts override
     + `NODE_EXTRA_CA_CERTS=tools/fingerprint-collector/tls_cert/server.crt`
     and diff the captured SETTINGS against the undici baseline.
2. **Promote the Anthropic profile**: update
   `templates/anthropic-claude-code.json` `h2_settings` to
   `{available: true, raw_order: [2, 4], values: {ENABLE_PUSH: 0, INITIAL_WINDOW_SIZE: 262144}}`
   and add `h2_pseudo_header_order: {available: true, order: [":authority", ":method", ":path", ":scheme"]}`.
3. **Update `crates/core_gateway/tests/fixtures/http2_fingerprint/anthropic-claude-code-h2.json`**
   (NEW file) with matching content; cross-check tests in
   `mimicry_http2_fixture_test.rs` will then become non-vacuous.
4. **Repeat capture** with `claude` CLI / Codex CLI / Gemini Advanced
   clients for the other 3 profiles (extend `clients/` with
   probes for Python httpx, Go net/http for ecosystem coverage).
5. Update §2 table once any profile flips to Released-eligible.

## 9. F-1.g cross-validation finding — 2026-05-26 (h2 stack divergence)

Related: [W11-F-F1g-h2-stack-divergence-finding.md](W11-F-F1g-h2-stack-divergence-finding.md).

After dcee914 landed (undici v7 h2 baseline captured), Owner challenged
whether the captured data was "really what F-1.g needs". Two follow-up
investigations:

### 9.1 Reading official SDK source (clean-room L0)

- **`anthropics/anthropic-sdk-typescript@32ce8c0` (MIT, 2d fresh)**:
  `src/internal/shims.ts:13-21` returns global `fetch` as-is;
  `src/client.ts:530` uses it directly. No custom undici Dispatcher, no
  `allowH2` set anywhere in `src/`. **Node SDK default = h1.1**.
- **`anthropics/anthropic-sdk-python@5db69c6` (MIT, 2d fresh)**: grep over
  `src/` for `http2=` / `HTTP2` / `allow_h2` returns zero matches. SDK uses
  `httpx.Client` / `httpx.AsyncClient` with default config. **Python SDK
  default = h1.1**.

### 9.2 Cross-library h2 fingerprint capture (Owner direction "拓 Python SDK 作交叉验证")

Drove `httpx_h2_probe.py` (httpx 0.28.1 + h2 4.3.0 with `http2=True`)
through the local capture server. Result file:
`tools/fingerprint-collector/captures/h2-server-1779774012.jsonl`.

Side-by-side comparison with dcee914's undici baseline:

| Field | undici (h2-server-1779761396.jsonl) | httpx (h2-server-1779774012.jsonl) |
|---|---|---|
| SETTINGS payload bytes | `000200000000000400040000` (12B) | `00010000100000020000000000040000ffff000500004000000300000064000600010000` (36B) |
| SETTINGS param count | **2** | **6** (all standard params present) |
| HEADER_TABLE_SIZE | (omitted) | 4096 |
| ENABLE_PUSH | 0 | 0 |
| INITIAL_WINDOW_SIZE | **262144** | **65535** |
| MAX_FRAME_SIZE | (omitted) | 16384 |
| MAX_CONCURRENT_STREAMS | (omitted) | 100 |
| MAX_HEADER_LIST_SIZE | (omitted) | 65536 |
| Pseudo-header order | `:authority, :method, :path, :scheme` (alphabetical) | `:method, :authority, :scheme, :path` (HTTP semantic) |
| Auto-added regular headers | content-length | **accept-encoding `gzip, deflate, br, zstd`** + content-length |

### 9.3 Conclusions

- **Two independent h2 libraries on the same OS / same TLS stack produce
  utterly different on-wire fingerprints.** Every SETTINGS param differs in
  presence or value; pseudo-header order differs; httpx silently injects
  `accept-encoding` that undici doesn't. There is NO "generic h2 baseline" —
  every library has its own per-byte fingerprint.
- **Anthropic Node SDK + Python SDK both default to h1.1** (per source). The
  existing `templates/anthropic-claude-code.json` `alpn_protocols: ["http/1.1"]`
  is **CONSISTENT** with what both official SDKs actually send.
- **cliproxyapi's choice to use h2 + Chrome utls for api.anthropic.com**
  (per `router-for-me/CLIProxyAPI@21fad9d:internal/runtime/executor/helps/utls_client.go:81-103,131-150`)
  is framed in their source comment as Claude Code CLI binary mimicry, NOT
  SDK mimicry. The intent is undocumented beyond that comment (no capture
  cited in their repo to ground the claim). Whether the implementation
  actually mirrors CC's wire bytes is unverified by them; the option (b)
  capture (§10) shows their h2 choice does not match what CC actually sends.
- **The undici-allowH2 capture committed in dcee914** is a synthetic
  experimental baseline. It does NOT represent: (a) Anthropic Node SDK default
  (SDK doesn't enable h2); (b) Claude Code CLI binary (CC's actual transport
  unknown, opaque SEA exe); (c) any production Anthropic-bound client we've
  confirmed. Re-labeled as `undici-h2-explicit-baseline`, NOT applied to any
  profile.

### 9.4 Effect on F-1.g closure

| Step | Status after cross-val |
|---|---|
| Promote `anthropic-claude-code.json` `h2_settings.available` to `true` | **BLOCKED** — no evidence supports it. Both SDKs use h1.1; CC CLI binary unknown. Requires option (b) real capture from `claude` CLI through local server (needs Owner API key rotation). |
| Change `anthropic-claude-code.json` `alpn_protocols` to `["h2", "http/1.1"]` | **DEFER** — current `["http/1.1"]` aligns with both SDK defaults. Re-evaluate after option (b) capture confirms whether CC binary advertises h2. |
| dcee914 capture utility | **KEEP** — pipeline is valid. Capture re-labeled. JSONL kept under `captures/` as `undici-h2-explicit-baseline`. |
| F-1.e (HTTP/2 fork outbound client integration) | **KEEP DEFERRED** — fork code itself is fine; the question of "which profile should outbound-h2 be applied to" is the gating decision, and we don't have an answer yet for Anthropic. |
| Per-vendor capture discipline | **CODIFIED** — `AGENTS.md` new section "Per-Vendor Fingerprint Capture Discipline" makes the "official-CLI real-capture only" rule reviewable + enforceable. |

### 9.5 Mutation discriminator for the new AGENTS.md rule

If a future commit changes any field in `templates/<vendor>.json` and the
commit message / `_field_sources` cannot point to a `captures/<vendor>-<ts>.jsonl`
or `output/<vendor>/clienthello-template.json` file, codex per-commit review
MUST flag HIGH and block. This is the discriminator: a commit that says
"changed alpn to [\"h2\", \"http/1.1\"] because cliproxyapi does this" with
no capture citation → must red. A commit that says "changed alpn per
`captures/anthropic-cc-cli-1779800000.jsonl` capture from `claude --version
2.1.112` on 2026-05-27" → green.

## 10. F-1.g closure — real Claude Code CLI capture (option b)

Related capture: `tools/fingerprint-collector/captures/h2-server-1779775310.jsonl`.

### 10.1 Method

Drove real Claude Code CLI binary (`/c/Users/h/.local/bin/claude.exe`, v2.1.112)
as a subprocess with env overrides, against `h2_capture_server.py` listening
on `127.0.0.1:18099` with `ALPN=["h2"]` only:

```bash
ANTHROPIC_API_KEY="<FAKE_KEY_PLACEHOLDER>" \
ANTHROPIC_BASE_URL="https://127.0.0.1:18099" \
NODE_EXTRA_CA_CERTS=".../tools/fingerprint-collector/tls_cert/server.crt" \
claude --bare -p "say hi once" --no-session-persistence \
       --model claude-3-haiku-20240307
```

The parent CC session (Claude Code driving this Claude Agent) was unaffected
— env vars scoped to the subprocess. A fake API key value was used because
the local server returns 200 regardless of auth content (avoids re-exposing
any real key in transcripts). Use any clearly non-real string.

### 10.2 Result

5 inbound TLS connections from CC subprocess, all 5 identical:

| Field | Value |
|---|---|
| `tls_version` | TLSv1.3 |
| `tls_cipher` | TLS_AES_256_GCM_SHA384 |
| `alpn_negotiated` | **`null`** (no overlap with server's h2-only) |
| `error` | "ALPN negotiated 'None', expected 'h2'" |

CC retried 5 times before giving up (each retry is an independent TLS
connection — TCP source port differs: 56305 → 56313). All 5 produced the
same `alpn_negotiated: null` result.

### 10.3 Verification that the server's h2 ALPN advertisement works

Same server, same code path, on the same OS, the prior captures show:
- undici probe → `alpn_negotiated: "h2"` ✓ (captures/h2-server-1779761396.jsonl)
- httpx probe → `alpn_negotiated: "h2"` ✓ (captures/h2-server-1779774012.jsonl)

So the server's `ssl.set_alpn_protocols(["h2"])` is functional. CC's
`alpn_negotiated: null` is therefore caused by **CC not advertising `h2`
in its ClientHello ALPN extension** — not by any server-side defect.

### 10.4 Conclusions

1. **`templates/anthropic-claude-code.json` `alpn_protocols: ["http/1.1"]`
   is CONFIRMED CORRECT** from direct real-CC capture. Not stale. Not wrong.
2. **`h2_settings.available: false`** in that profile is also correct: CC
   never establishes an h2 connection, so there are no SETTINGS bytes to
   record.
3. **cliproxyapi's source-comment intent** (`router-for-me/CLIProxyAPI@21fad9d:internal/runtime/executor/helps/utls_client.go:153-154`
   — the comment frames the utls + `http2.Transport` path as mimicking CC's
   TLS behavior) is **contradicted by direct evidence**. cliproxyapi's choice
   of h2 does not match what CC actually sends. The motivation for their
   choice is not stated in their repo.
4. The `dcee914` undici-with-allowH2 capture is NOT what CC sends. The
   `c69a034` cross-validation finding (undici vs httpx divergence) reinforced
   this; option (b) now closes the loop with direct evidence.

### 10.5 Broader effect on F-1 epic

Re-auditing all 4 currently-deployed profiles by their captured ALPN field:

| Profile | `alpn_protocols` | Actual business-protocol |
|---|---|---|
| `anthropic-claude-code` | `["http/1.1"]` ← option (b) confirmed this section | h1.1 |
| `openai_codex_cli` | `[]` (no ALPN advertised) | h2 OR h1.1 per reqwest default |
| `gemini_advanced` | `["h2", "http/1.1"]` advertised | **h1.1** (Google picks h1.1 per `http_layer.protocol`) |
| `kiro_cli` | `[]` (no ALPN advertised) | h1.1 |

**NO currently-deployed profile actually USES h2 on the wire for business
requests.** Either ALPN doesn't advertise h2, or the server picks h1.1
despite h2 being available. Gemini is the closest case (h2 advertised,
h1.1 picked) — could be promoted to h2 if Google's server agreed, but it
doesn't.

This means **F-1.e (HTTP/2 fork outbound client integration) does NOT mimic
any current first-party client behavior on any of the 4 profiles**. The h2
fork outbound code is correct infrastructure but solving a non-problem for
the current profile set.

### 10.6 Effect on the W11-F F-1 roadmap

| Sub | Pre-option-(b) status | Post-option-(b) status |
|---|---|---|
| F-1.a (fixture contract) | landed | landed; cross-check trivially passes (no profile h2_settings.available=true) |
| F-1.b (adapter true-IO) | landed | landed (dormant infrastructure) |
| F-1.c (L2 preflight) | landed | landed (dormant infrastructure) |
| F-1.d.1/d.2 (transport boundary + BoxBody) | landed | landed (still useful — transport-agnostic) |
| F-1.e (h2 fork outbound) | OPEN (planned next) | **DEFERRED indefinitely** — no profile needs it. Re-evaluate when a profile gets `alpn=["h2"]` AND a real h2 capture matching it. |
| F-1.f (combined builder + L1+L2 preflight) | landed | landed (still useful — L1 preflight applies regardless of h2 vs h1) |
| F-1.g (capture pipeline + evidence) | landed | **CLOSED by option (b)** — no profile h2 promotion happened, none needed |
| F-1 epic Released criteria #4 | "≥1 profile h2 fixture non-vacuous" | **N/A for current profiles** — re-scope criterion or accept all 4 profiles never qualify |

### 10.7 Open follow-ups (NOT in this commit)

1. **Extend `h2_capture_server.py` to optionally advertise `["h2", "http/1.1"]`
   ALPN** so we can also capture CC's actual h1.1 wire bytes (header order,
   user-agent, content negotiation). Would need an h1.1 read/respond path
   too; out of current scope.
2. **Decide F-1 epic Released criterion #4 status** — either re-scope to
   "≥1 profile satisfies its real-capture-derived ALPN, regardless of h2/h1",
   or accept the F-1 epic ships h2 infrastructure as dormant.
3. **Roadmap audit**: with F-1.e indefinitely deferred and no profile h2
   in flight, the W11-F epic effectively reduces to "F-2 TLS mimicry +
   F-1 dormant h2 infrastructure". Recommend Owner / plan trio re-confirm
   the W11-F scope.

## 11. W11-F F-1 dormancy gates (Owner-approved 2026-05-26 post codex consult)

Source: parallel codex consult, prompt + reply preserved at
`docs/process/plans/2026-05-26-w11f-f1-scope-decision-codex-consult.md`.
Both Claude and Codex independently recommended option A (accept dormant);
Codex's stricter version added 5 explicit dormancy gates. Owner approved
all 5 on 2026-05-26 ("是的").

These 5 gates **replace** F-1 epic Released criterion §5 #4 and define the
discipline required to ever activate the h2 fork outbound code.

### Gate 1 — Profile provenance is reproducible

Every vendor profile JSON under `tools/fingerprint-collector/templates/`
**must** carry a `_field_sources` block pointing to:

- The real first-party capture artifact path (jsonl or pcap-derived
  clienthello template).
- The driving CLI / desktop app version (e.g. `claude --version 2.1.112`).
- Capture timestamp (UTC).
- Capture method (`h2_capture_server.py` + env-override subprocess /
  passive npcap / mitmproxy + addon / etc.).

A profile lacking any of those four pieces fails Gate 1. Codex per-commit
review must flag HIGH and block any profile that lands without complete
`_field_sources`.

### Gate 2 — Tests assert exact per-profile ALPN behavior

`crates/core_gateway/tests/mimicry_http2_fixture_test.rs` (and any peer
test covering the L1 ALPN preflight) **must** assert each profile's exact
ALPN observation, covering ALL three observed patterns:

- `alpn_protocols: ["http/1.1"]` only (e.g. `anthropic-claude-code`)
- `alpn_protocols: []` (no ALPN extension advertised) (e.g. `openai_codex_cli`, `kiro_cli`)
- `alpn_protocols: ["h2", "http/1.1"]` advertised but server picks h1.1
  (e.g. `gemini_advanced`)

The test for each profile must FAIL if the profile's ALPN is silently
changed without a new capture citation. Mutation discriminator: removing
any per-profile assertion or replacing it with `assert!(alpn.is_some())`
must turn the test red.

### Gate 3 — h2 outbound is hard-unreachable without a captured h2 profile

This is the structural gate codified in `AGENTS.md` §"Dormant h2 outbound
infrastructure gate". The HUAKAI production code path **must not** reach
`http2_adapter::drive_request<T>`, the h2 branch of
`try_build_gateway_transport_with_profile`, or F-1.e implementations
unless ALL of:

- The active profile has `h2_settings.available=true`, AND
- That profile's `h2_settings` fields trace via `_field_sources` to a real
  first-party CLI capture (jsonl or fixture), AND
- The capture's `alpn_negotiated` is `h2` (not `null`, not `http/1.1`).

No profile currently meets these conditions. The dormant code therefore
cannot execute on production traffic. Codex per-commit review must HIGH-
block any wiring change that violates this.

### Gate 4 — F-1.e classification: Mandatory Roadmap / Feature Flag

F-1.e (HTTP/2 fork outbound client real connection) is **NOT** dropped.
It moves to Mandatory Roadmap status, behind a Feature Flag that:

- Defaults to OFF.
- Is per-profile opt-in (cannot be globally enabled).
- Cannot be opted in for a profile until Gate 1 + Gate 2 + Gate 3 are
  satisfied for that profile.
- Documentation accompanying the flag explicitly states:
  "Activation requires real first-party h2 capture for the target profile;
  implementation precedes capture is forbidden."

### Gate 5 — Release notes explicit on h2 absence

When W11-F F-1 (or any aggregate that includes it) ships, release notes
must state:

> No currently-deployed profile uses HTTP/2 on the wire for business
> requests. The F-1 epic ships h2 outbound infrastructure as a dormant
> capability, gated on real first-party capture per
> `AGENTS.md` §"Dormant h2 outbound infrastructure gate". The earlier
> F-1 Released criterion "≥1 profile h2 fixture non-vacuous" is
> intentionally N/A for this release, not silently skipped.

### Replacement of F-1 epic Released criterion §5 #4

Section §5 #4 (`≥1 profile has real-upstream H2 fixture ... non-vacuously`)
is **superseded** by the conjunction of Gates 1-5 above. The new criterion
text is:

> Every deployed profile satisfies Gate 1 (provenance) + Gate 2 (per-
> profile ALPN assertion). The h2 fork outbound path satisfies Gate 3
> (hard-unreachable without captured h2 profile). F-1.e is in Mandatory
> Roadmap / Feature Flag state per Gate 4. Release notes match Gate 5
> language.

This is the actual criterion to test against for any future
"W11-F F-1 Released" claim.

### Acceptance evidence in THIS commit

This commit lands the gates **as policy**, not yet as implementation.
Specifically:

- ✅ Gate 1 partially: anthropic-claude-code.json now has option (b)
  capture in §10; other 3 profiles still need `_field_sources` audit /
  update — recorded as follow-up below.
- ✅ Gate 3 by construction: F-1.e was never implemented, so the
  unreachability is trivially true today. Maintaining it requires the
  AGENTS.md rule enforcement on future commits.
- ✅ Gate 5 language drafted; release notes for next aggregate release
  must adopt it verbatim or equivalent.
- ⏸ Gate 2: per-profile ALPN assertion needs `mimicry_http2_fixture_test.rs`
  patch (the existing fixture test only covers the `available=true` arm).
  Tracked as follow-up; non-trivial test change, separate slice.
- ⏸ Gate 4: F-1.e Feature Flag scaffold not yet added (no flag exists).
  Tracked as follow-up; minimal — feature-flag manifest entry +
  documentation, no runtime code.

### Follow-up slices opened by this acceptance

1. **§11-Gate2-slice**: extend `mimicry_http2_fixture_test.rs` to assert
   per-profile ALPN for all 4 currently-deployed profiles, with mutation
   discriminator per CLAUDE.md #14.
2. **§11-Gate1-audit**: audit codex-cli.json / gemini-advanced.json /
   kiro-cli.json `_field_sources` blocks. Where any of the 4 metadata
   pieces (artifact path, CLI version, timestamp, method) is missing,
   add it or mark "pending re-capture" with a tracked follow-up entry.
3. **§11-Gate4-flag**: register F-1.e behind a feature flag (default OFF,
   per-profile opt-in, gated on Gate 1+2+3 satisfaction for that profile).
   Documentation lands with the flag.
4. **§11-Gate5-release-template**: add Gate 5 language to the next
   release-notes template change.
