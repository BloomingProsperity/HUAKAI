# Plan: F-1.g attempt-2 — server-side h2 SETTINGS frame capture

- **Date**: 2026-05-26
- **Author**: Claude PM-Orchestrator
- **Status**: Owner picked A+D from in-chat menu 2026-05-26 (attempt-1 artifacts discarded; build server-side capture)
- **Supersedes**: F-1.g attempt-1 (mitmproxy 12 addon — deleted)
- **CLAUDE.md compliance**: #9 plan-before-execute; #14 test discipline (mutation-discriminating parser tests baked into success criteria)

## Why attempt-1 failed (recap)

The mitmproxy 12 addon path (`tools/fingerprint-collector/capture_h2_settings.py`,
deleted) hit two blockers:

1. mitmproxy 12 does NOT expose the client's original SETTINGS frame bytes via
   any addon API surface — every defensive introspection path (`flow.client_conn.h2`,
   `_h2_conn`, `protocol`, `metadata.h2_settings`) returned None or unavailable.
2. The driving client was `python-httpx/0.28.1` (not Claude Code), so even the
   captured header / pseudo-header order was an httpx baseline, not the
   intended Anthropic-SDK on-wire shape.

The captured JSONL therefore had `h2_settings: "<UNAVAILABLE>"` — insufficient
to upgrade the `anthropic-claude-code` profile from `available: false` to
`available: true` against F-1.a evidence-contract invariants.

## Why this is needed at all (the existing collector gap)

`tools/fingerprint-collector/cmd/` is a Go libpcap/npcap **passive** capture
tool. It can read TLS ClientHello (unencrypted) but cannot decrypt post-handshake
H2 SETTINGS frames without TLS key material. That's why
`templates/anthropic-claude-code.json` ships with
`h2_settings.available=false` + the `limitation_note "collector v1 无 TLS 解密
材料"`. F-1.g needs to fill that gap via a different capture method.

## Goal

Capture the on-wire HTTP/2 SETTINGS frame bytes (parsed + raw hex) AND the
HEADERS frame pseudo-header order, sent by undici (Node h2 client library;
underlying HTTP stack of `@anthropic-ai/sdk` and Claude Code CLI), with:

- **No** real api.anthropic.com requests
- **No** API key required
- **No** mitmproxy
- **No** system CA install

## Approach (Owner pick: A)

### Server (Python)

`tools/fingerprint-collector/h2_capture_server.py` — local listener:

1. Self-signed TLS cert + key (auto-generated in `tls_cert/` on first run via
   openssl shell-out). `tls_cert/` gitignored.
2. Listens on `127.0.0.1:18099` with `ssl.set_alpn_protocols(['h2'])`.
3. Per inbound connection:
   - Read raw bytes of HTTP/2 connection preface (24 bytes); record verbatim.
   - Read 9-byte SETTINGS frame header from socket; record raw hex.
   - Read SETTINGS payload bytes; record raw hex AND parse into ordered
     `[{id, name, value}, ...]` (on-wire order, NOT sorted by ID).
   - Replay preface+SETTINGS into an `h2.connection.H2Connection` so the
     h2 state machine can drive the rest of the conversation properly
     (we send our own SETTINGS, ACK client SETTINGS, etc.).
   - On HEADERS frame: parse via in-script HPACK decoder BEFORE letting h2
     re-order; record raw frame hex + pseudo-header order + regular-header
     order.
   - Send a trivial 200 response, close stream.
4. Output: JSONL append at `tools/fingerprint-collector/captures/h2-server-<unix-ts>.jsonl`.

### Client probe (Node)

`tools/fingerprint-collector/clients/undici_probe.mjs` — minimal driver:

- `import { Client } from "undici"` with `allowH2: true`, `rejectUnauthorized: false`
- One POST to `https://127.0.0.1:18099/v1/messages`
- Logs status + body so operator can verify round-trip
- Exits 0 on success

### Parser unit test

`tools/fingerprint-collector/tests/test_settings_parser.py`:

- Feeds a known SETTINGS payload, asserts parsed output matches.
- Asserts on-wire order is preserved when params arrive non-canonical
  (param ID 4 BEFORE param ID 1 must round-trip in that order; this kills
  any "silently sorted by ID" defect).
- Asserts mutating the LSB of a parameter value produces different output
  (CLAUDE.md #14 mutation discipline — proves the test discriminates).
- Asserts payload length not divisible by 6 raises `ValueError`.

## Success criteria

| # | Criterion | How verified |
|---|-----------|--------------|
| 1 | Server captures ≥1 client SETTINGS frame end-to-end | JSONL record has `client_settings_frame.parameters_in_order` non-empty |
| 2 | SETTINGS raw bytes preserved | JSONL has `frame_header_hex` (9 bytes) + `payload_hex` (multiple of 6) |
| 3 | On-wire SETTINGS parameter order is real, not normalized | parser tests assert non-canonical order survives |
| 4 | Mutation discipline | flipping 1 byte in a parameter value changes the parser output (test asserts the diff explicitly) |
| 5 | HEADERS pseudo-order is real, not implied | JSONL `client_headers_frame.pseudo_header_order` is the HPACK-decoded sequence, distinct from `regular_header_order` |
| 6 | Zero API cost | server never opens an outbound socket to anywhere other than the kernel; client never reaches api.anthropic.com |

## Out of scope (explicit deferrals — follow-up)

1. **Promoting capture into `templates/anthropic-claude-code.json`.** Reason:
   confirming undici h2 fingerprint == Claude Code CLI on-wire fingerprint
   requires either reading `@anthropic-ai/sdk` source for transport config OR
   running `claude` CLI through the server via hosts override + NODE_EXTRA_CA_CERTS.
   For this commit, capture lands as `undici-default-h2-baseline`, NOT
   promoted to the Anthropic profile.
2. Multi-client coverage (Python httpx, Go net/http, Rust hyper baselines).
3. Updating F-1.a Anthropic fixture cross-check expectations.
4. F-1.a profile gate flipping `available: false → true`.

## Blast radius

- New files only under `tools/fingerprint-collector/` (server, client probe,
  test) and `docs/process/{plans,release-readiness}/` (this plan + status doc).
- **Zero edits** to production code in `backend/` or anywhere else.
- **Zero outbound network calls** to non-localhost destinations.
- No API key used. No system CA modified.
- Self-signed cert + private key live in `tools/fingerprint-collector/tls_cert/`
  (added to `.gitignore`).

## Risks / what could go wrong

| Risk | Mitigation |
|------|-----------|
| Python `h2`/`hpack` not installed in operator's env | Script prints `pip install h2 hpack` hint and exits 2 on import failure |
| undici v7 `allowH2` API moved | Probe will fall back to h1; server will see ALPN != "h2" and write an `error: ALPN negotiated 'http/1.1'` record so operator immediately knows |
| Self-signed cert + ALPN handshake fails on Windows | Use Python stdlib `ssl.SSLContext.set_alpn_protocols` (well-supported on Win); cert generated via openssl with explicit SAN for `localhost` + `127.0.0.1` |
| `openssl` not in PATH on Windows | Script prints clear error and exits; operator installs Git-for-Windows or scoop openssl |
| Port 18099 still in use from prior session | Script binds with `SO_REUSEADDR`; if bind still fails, error message names the port |

## Owner decision points (before commit)

1. Capture label remains conservative (`undici-default-h2-baseline`), not
   promoted to `anthropic-claude-code` profile in this commit. ✓ planned this way; confirm OK.
2. Status doc at `docs/process/release-readiness/W11-F-F1-status.md` (new
   file) — confirm OK or specify alternate location.

## Clean-room note (CLAUDE.md #11)

**L0** — no non-MIT reference project source read. Tool is HUAKAI-original
implementation of a standard Python h2 server. Dependencies: `h2`
(BSD-licensed), `hpack` (BSD-licensed), Python stdlib `ssl`/`socket` — all
MIT-compatible. No upstream specifier session required.

## Time estimate

- Build (server + client + test): 50 min
- Run + verify capture: 15 min
- Status doc: 10 min
- Codex per-commit review (CLAUDE.md #8): 15 min
- Commit + push: 5 min
- **Total: ~95 min**

## Execution order

1. Write this plan + server + client + test (current turn)
2. Run server in background; run probe; verify JSONL
3. Iterate if SETTINGS frame not captured (debug raw socket read)
4. Write status doc to `docs/process/release-readiness/W11-F-F1-status.md`
5. Add `tools/fingerprint-collector/.gitignore` for `tls_cert/` + `__pycache__/`
6. `codex exec review --uncommitted --full-auto`
7. Address HIGH findings
8. `git commit` + `git push github claude/rust-hardening`
