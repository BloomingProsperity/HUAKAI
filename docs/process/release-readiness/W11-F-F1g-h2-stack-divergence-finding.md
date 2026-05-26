# W11-F F-1.g — h2 stack divergence finding (cross-validation result)

- **Date**: 2026-05-26
- **Lane**: clean-room L0 (behavior parity claim, public-source reads of MIT repos + local synthetic captures)
- **Author**: Claude PM-Orchestrator
- **Supersedes**: an earlier draft `W11-F-F1g-undici-vs-sdk-finding.md` (removed in same commit)
- **Related**: [W11-F-F1-status.md §9](W11-F-F1-status.md), [AGENTS.md §"Per-Vendor Fingerprint Capture Discipline"](../../../AGENTS.md), [2026-05-26-f1g-server-side-capture.md](../plans/2026-05-26-f1g-server-side-capture.md)

## TL;DR

**Two independent HTTP/2 client libraries (undici v7 in Node, httpx 0.28.1 in
Python) produce drastically different on-wire h2 fingerprints on the same
machine.** All six standard SETTINGS parameters either differ in presence or
in value. Pseudo-header order differs. Auto-added regular headers differ.

The practical consequence: **there is no "h2 baseline" that generalizes
across clients**. Per-vendor real-capture is non-negotiable. The AGENTS.md
rule codifying this is the durable artifact; this doc is the evidence.

## Recency check (CLAUDE.md #12)

| Repo | License | HEAD | committed | pushed | fresh? |
|---|---|---|---|---|---|
| `anthropics/anthropic-sdk-typescript` | MIT | `32ce8c0` | 2026-05-21 | 2026-05-23 | 2d ✓ |
| `anthropics/anthropic-sdk-python` | MIT | `5db69c6` | 2026-05-22 | 2026-05-23 | 2d ✓ |
| `router-for-me/CLIProxyAPI` | MIT | `21fad9d` | 2026-05-21 | 2026-05-25 | 0d ✓ |
| `Xerxes-2/clewdr` | AGPL-3.0 | `5762680` | 2026-05-09 | 2026-05-09 | 16d ✓ |
| `Wei-Shaw/sub2api` | LGPL-3.0 | `f59d9a5` | 2026-05-22 | 2026-05-26 | 0d ✓ |

All clones under `~/refs/<repo>/`.

## Part A: Official SDK source confirms h1.1 defaults

### A.1 Anthropic Node SDK — `anthropic-sdk-typescript@32ce8c0`

`anthropics/anthropic-sdk-typescript@32ce8c0:src/internal/shims.ts:13-21` ([github](https://github.com/anthropics/anthropic-sdk-typescript/blob/32ce8c0/src/internal/shims.ts#L13-L21)):
```ts
export function getDefaultFetch(): Fetch {
  if (typeof fetch !== 'undefined') {
    return fetch as any;
  }
  throw new Error('`fetch` is not defined as a global; ...');
}
```

`anthropics/anthropic-sdk-typescript@32ce8c0:src/client.ts:528-530` ([github](https://github.com/anthropics/anthropic-sdk-typescript/blob/32ce8c0/src/client.ts#L528-L530)):
```ts
this.fetchOptions = options.fetchOptions;
this.maxRetries = options.maxRetries ?? 2;
this.fetch = options.fetch ?? Shims.getDefaultFetch();
```

`anthropics/anthropic-sdk-typescript@32ce8c0:src/client.ts:1301-1302` ([github](https://github.com/anthropics/anthropic-sdk-typescript/blob/32ce8c0/src/client.ts#L1301-L1302)) — fetchOptions merge is user pass-through; SDK never injects a Dispatcher / Agent / allowH2.

Grep `allowH2|http2|undici|Dispatcher|HttpAgent|HttpsAgent` over `src/`: zero
matches in production transport code (the 26 cross-file matches are tests,
yarn.lock, examples, MIGRATION.md, shim doc comments).

**→ Default Anthropic Node SDK traffic = HTTP/1.1.**

### A.2 Anthropic Python SDK — `anthropic-sdk-python@5db69c6`

`anthropics/anthropic-sdk-python@5db69c6:src/anthropic/_base_client.py:39-94` ([github](https://github.com/anthropics/anthropic-sdk-python/blob/5db69c6/src/anthropic/_base_client.py#L39-L94)) imports `httpx` directly; constructs `httpx.Client` / `httpx.AsyncClient` with
default config.

Grep `http2\s*=|http2=True|http2=False|allow_h2|HTTP2` over `src/`: zero
matches.

**→ Default Anthropic Python SDK traffic = HTTP/1.1** (httpx default;
`http2=True` opt-in is never set).

## Part B: Cross-library h2 capture diverges

### B.1 Capture method

Both probes drive the local h2 capture server
(`tools/fingerprint-collector/h2_capture_server.py`, MIT, this repo)
which terminates TLS with a self-signed cert + ALPN=h2 and logs raw
SETTINGS + HEADERS frame bytes.

- **undici probe** (`clients/undici_probe.mjs`, Node v25.3.0, undici v7
  with `allowH2: true`): output `captures/h2-server-1779761396.jsonl`
  (committed in `dcee914`).
- **httpx probe** (`clients/httpx_h2_probe.py`, Python 3.12.10, httpx
  0.28.1 + h2 4.3.0 with `http2=True`): output
  `captures/h2-server-1779774012.jsonl` (this commit).

### B.2 Side-by-side

| Field | undici | httpx |
|---|---|---|
| `tls_version` | TLSv1.3 | TLSv1.3 |
| `tls_cipher` | TLS_AES_256_GCM_SHA384 | TLS_AES_256_GCM_SHA384 |
| `alpn_negotiated` | h2 | h2 |
| SETTINGS frame raw payload | `000200000000000400040000` (12 B) | `00010000100000020000000000040000ffff000500004000000300000064000600010000` (36 B) |
| SETTINGS param count | **2** | **6** |
| HEADER_TABLE_SIZE (id 1) | (omitted, defaults to 4096) | **4096** explicit |
| ENABLE_PUSH (id 2) | 0 | 0 |
| MAX_CONCURRENT_STREAMS (id 3) | (omitted) | **100** explicit |
| INITIAL_WINDOW_SIZE (id 4) | **262144** | **65535** |
| MAX_FRAME_SIZE (id 5) | (omitted, defaults to 16384) | **16384** explicit |
| MAX_HEADER_LIST_SIZE (id 6) | (omitted) | **65536** explicit |
| SETTINGS on-wire order | id 2, id 4 | id 1, id 2, id 4, id 5, id 3, id 6 |
| Pseudo-header order | `:authority, :method, :path, :scheme` (alphabetical) | `:method, :authority, :scheme, :path` (HTTP semantic) |
| Auto-added headers | content-length | **accept-encoding: `gzip, deflate, br, zstd`** + content-length |

### B.3 Why this matters

1. **INITIAL_WINDOW_SIZE differs by 4×** (262144 vs 65535). A server that
   fingerprints by SETTINGS value alone can trivially tell undici from httpx.
2. **Parameter PRESENCE differs**. undici sends 2 params; httpx sends all
   6 standard. A server-side detector counting parameters can distinguish
   them at the frame level.
3. **On-wire ORDER of params differs even where both send the param**
   (id 1, 2, 4, 5, 3, 6 vs 2, 4) — not just alphabetical sort.
4. **Pseudo-header order differs** — alphabetical vs semantic. This is
   another wire-level discriminator unrelated to SETTINGS.
5. **httpx auto-injects `accept-encoding`** with a specific value
   (`gzip, deflate, br, zstd`) including modern codecs (`br`, `zstd`) that
   undici does NOT add automatically. Browser-like behavior — but distinct
   from what undici sends.
6. **Both run BoringSSL-derived or OpenSSL TLS on the same OS** — the TLS
   layer (1.3, AES-256-GCM-SHA384, ALPN h2) is identical. The DIFFERENCE
   is purely at the HTTP/2 layer, library-specific.

**Implication**: "library X produces fingerprint similar to library Y" is
WRONG by default. Every h2 client library must be captured individually if
its fingerprint is going into a HUAKAI profile.

## Part C: Reference projects converge on Chrome utls (different reason)

For comparison, what the AI-gateway / proxy projects do:

| Project | License | Strategy for `api.anthropic.com` | Citation |
|---|---|---|---|
| **cliproxyapi** | MIT | utls Chrome fingerprint + `http2.Transport` | `router-for-me/CLIProxyAPI@21fad9d:internal/runtime/executor/helps/utls_client.go:81-103,131-150` ([github](https://github.com/router-for-me/CLIProxyAPI/blob/21fad9d/internal/runtime/executor/helps/utls_client.go#L81-L150)) |
| **clewdr** | AGPL | wreq with `Emulation::Chrome145` | `Xerxes-2/clewdr@5762680:src/utils/mod.rs:61` ([github](https://github.com/Xerxes-2/clewdr/blob/5762680/src/utils/mod.rs#L61)) |
| **rquest** (lib) | MIT/Apache | default `alpn_protocols: [HTTP2, HTTP1]` + Chrome/Firefox profiles | `0x676e67/rquest@e8781fb:src/tls.rs:551` ([github](https://github.com/0x676e67/rquest/blob/e8781fb/src/tls.rs#L551)) |

For **Antigravity** vendor specifically, cliproxyapi overrides:
`router-for-me/CLIProxyAPI@21fad9d:internal/runtime/executor/antigravity_executor.go:195-225` ([github](https://github.com/router-for-me/CLIProxyAPI/blob/21fad9d/internal/runtime/executor/antigravity_executor.go#L195-L225)) —
`ForceAttemptHTTP2=false` + `NextProtos=["http/1.1"]`, with comment "to
perfectly mimic Node.js https defaults".

### C.1 Why these don't directly inform HUAKAI's profile

- **cliproxyapi's comment claims "match real Claude Code's TLS behavior"**
  but cites no capture. The implementation uses `tls.HelloChrome_Auto` —
  a generic "look like Chrome browser" hello, not a CC-specific fingerprint.
- **clewdr's `Emulation::Chrome145`** is also generic Chrome emulation,
  not a CC-derived fingerprint.
- Both projects may be using "Chrome utls + h2" because:
  (a) Cloudflare bot detection at api.anthropic.com whitelists browser-like
      TLS, OR
  (b) they assume CC CLI uses BoringSSL (Chromium's TLS) via SEA so the
      fingerprint is naturally Chrome-like — UNVERIFIED assumption.

Neither path provides direct evidence about what bytes Claude Code CLI
actually sends.

### C.2 Cross-validation outcome

The Part B comparison proves that even within "h2 over BoringSSL/OpenSSL on
Linux", different libraries diverge byte-for-byte. So the cliproxyapi /
clewdr Chrome utls approach is *probably* close to what CC sends *if* CC
uses BoringSSL — but the only way to know is direct capture of CC CLI's
actual on-wire bytes.

## Part D: Net effect on F-1.g + `anthropic-claude-code.json`

| Question | Answer |
|---|---|
| Does HUAKAI's existing `alpn_protocols: ["http/1.1"]` match the Anthropic Node SDK default? | **Yes** (confirmed Part A.1) |
| Does it match the Anthropic Python SDK default? | **Yes** (confirmed Part A.2) |
| Does it match real Claude Code CLI on-wire? | **Unknown** — needs option (b) capture |
| Should the undici-h2 baseline (dcee914 jsonl) be applied to the Anthropic profile? | **No** — synthetic, doesn't match SDK defaults, may not match CC either |
| Should the httpx-h2 baseline (this commit) be applied? | **No** — same reason; httpx default is h1.1, the `http2=True` capture is explicit opt-in only |
| Is "promote `h2_settings.available` to true" possible right now? | **No** — no real CC capture available; SDK source says no h2; reference-project consensus is not source-cited evidence |
| Does F-1.e (HTTP/2 fork outbound code) become obsolete? | **No** — the fork code is fine. The question is "which profile uses it" — currently no Anthropic-tier profile is justified to use it without further evidence. |

## Part E: What this commit changes vs preserves

### Changes

- AGENTS.md: new section "Per-Vendor Fingerprint Capture Discipline"
  codifies the per-vendor real-capture rule + enforcement via codex review.
- `W11-F-F1-status.md`: new §9 with cross-validation table + status of
  F-1.g closure steps.
- This file: new evidence doc.
- `tools/fingerprint-collector/clients/httpx_h2_probe.py`: new (synthetic
  probe, NOT promoted to profile).
- `tools/fingerprint-collector/captures/h2-server-1779774012.jsonl`: new
  (httpx baseline, NOT promoted to profile).
- `docs/process/release-readiness/W11-F-F1g-undici-vs-sdk-finding.md`:
  **removed** (superseded by this doc).

### Preserves (unchanged)

- `templates/anthropic-claude-code.json` — `alpn_protocols: ["http/1.1"]`
  stays, `h2_settings.available: false` stays. Aligned with SDK defaults
  per Part A.
- `tools/fingerprint-collector/h2_capture_server.py` — unchanged.
- `tools/fingerprint-collector/captures/h2-server-1779761396.jsonl` —
  unchanged (undici baseline from dcee914).
- F-1.b / F-1.c / F-1.d / F-1.f code — unchanged (dormant for Anthropic
  profile but valid infrastructure).

## Part F: Mutation discriminator for the AGENTS.md rule

If a future commit changes any `templates/<vendor>.json` field WITHOUT
citing a `captures/<vendor>-*.jsonl` or `output/<vendor>/clienthello-template.json`
file in the commit message / `_field_sources`, codex per-commit review MUST
flag HIGH and block. Concrete examples:

- ❌ "changed `alpn_protocols` to `[\"h2\", \"http/1.1\"]` because cliproxyapi
  does this" — fails: no capture citation.
- ❌ "changed `h2_settings.available` to true based on undici baseline" —
  fails: undici probe is in `clients/`, not a real CC capture.
- ✅ "changed `alpn_protocols` per `captures/anthropic-cc-cli-real-1779800000.jsonl`
  capture driven by `claude --version 2.1.112` through `h2_capture_server.py`
  on 2026-05-27 via hosts override" — passes: real CLI cited.
