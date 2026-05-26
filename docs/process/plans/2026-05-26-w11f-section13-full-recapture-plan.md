# §13 Full re-capture plan for 4 vendor profiles (2026-05-26)

> Owner-approved 2026-05-26 ("A": §13 全重抓启动). Goal: flip every
> profile's §12.2 Gate 1 verdict from WEAK → PASS by capturing fresh
> first-party TLS ClientHello + (where applicable) HTTP/2 wire bytes,
> committing the artifacts under `tools/fingerprint-collector/captures/`
> so paths are reproducible (not ephemeral `/tmp` like the lost 2026-05-14
> set).

## Tooling already in place

| Tool | Purpose | State |
|---|---|---|
| `tools/fingerprint-collector/capture_tls_clienthello.py` | mitmproxy 12 addon — captures TLS ClientHello (offered ALPN, ciphers, extensions, JA3 inputs, raw bytes) | EXISTS, used 2026-05-14 successfully |
| `tools/fingerprint-collector/h2_capture_server.py` | local Python h2 server — captures HTTP/2 SETTINGS frame + HEADERS bytes after TLS negotiation | EXISTS (this session); validated against undici, httpx, real Claude Code CLI |
| `tools/fingerprint-collector/cmd/` (Go libpcap) | passive ClientHello sniffer | EXISTS; used for original 2026-05-06/14 captures; can serve as backup if mitmproxy not available |

## Two-pass capture per vendor

Each vendor needs (potentially) **two** captures because the toolchain
splits TLS-layer and h2-layer responsibilities:

**Pass 1 — ClientHello (every vendor)**: drive the CLI through mitmproxy
with `capture_tls_clienthello.py` addon. Output:
`captures/clienthello-<vendor>-<ts>.jsonl`. Gate 1 (a)/(d) satisfied for
all 4 profiles.

**Pass 2 — h2 SETTINGS + HEADERS (only profiles that actually negotiate
h2)**: drive the CLI through `h2_capture_server.py` (via env override +
self-signed CA). Output:
`captures/h2-server-<vendor>-<ts>.jsonl`. Only needed if Pass 1 jsonl
shows the vendor advertises h2 AND its target server negotiates h2.

Based on 2026-05-14 data + this session's evidence:
- **AnthropicClaudeCode**: Pass 1 only (CC doesn't advertise h2 per
  option-(b) capture this session). Pass 2 skipped.
- **CodexCli**: Pass 1 only (alpn=[]).
- **KiroCli**: Pass 1 only (alpn=[]).
- **GeminiAdvanced**: Pass 1 mandatory (alpn=["h2","http/1.1"]
  advertised); Pass 2 OPTIONAL — Google's cloudcode-pa picks h1.1 in
  practice, so h2 SETTINGS may never fire. If it does, capture it.

## Pre-flight checklist (Owner does once)

1. `pip install mitmproxy` (already installed for prior captures).
2. Verify mitmproxy CA is installed for HTTPS interception:
   - Linux/macOS: `mitmdump` starts, visit `http://mitm.it` from any
     browser using the proxy, install the CA per OS.
   - Windows: same, OR `certutil -addstore -user "Root" mitmproxy-ca-cert.cer`.
3. Verify each CLI's auth still works without proxy (sanity).

## Per-vendor capture procedure

### Anthropic / Claude Code CLI

```bash
# Shell A: start mitmproxy with the ClientHello addon
mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py

# Shell B: drive one real CC CLI call through it
HTTPS_PROXY=http://127.0.0.1:18099 \
HTTP_PROXY=http://127.0.0.1:18099 \
SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \
NODE_EXTRA_CA_CERTS=~/.mitmproxy/mitmproxy-ca-cert.pem \
claude --bare -p "say hi" --no-session-persistence --model claude-3-haiku-20240307
```

Expected jsonl: `captures/clienthello-<ts>.jsonl` with at least 1 record
where `sni=api.anthropic.com`. Capture `alpn_protocols` offered list +
`cipher_suites` + `extensions` + `raw_hex` (raw ClientHello bytes).

After capture, **stop mitmdump** (Ctrl+C). Rename the jsonl to
`captures/anthropic-cc-cli-clienthello-<ts>.jsonl` (vendor prefix for
clarity).

### OpenAI / Codex CLI

```bash
mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py

HTTPS_PROXY=http://127.0.0.1:18099 \
SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \
codex exec "Return: ping for §13 recapture."
```

Same expected behavior. Rename to `captures/codex-cli-clienthello-<ts>.jsonl`.

**Special concern (per §12.3 + Owner 2026-05-26 decision "重抓")**: the
`_pending-backfill/openai_codex-real-20260519T055201Z.json` shows
`legacy_version 0x0303` drift vs the 2026-05-14 production capture's
`0x0304`. The fresh capture from §13 will resolve this: if §13 jsonl shows
0x0304 → 2026-05-14 was the steady-state, backfill was a fluke; if 0x0303
→ Codex CLI evolved, production needs the update.

### Google / Gemini Advanced (CLI or Chrome)

```bash
mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py

# CLI path:
HTTPS_PROXY=http://127.0.0.1:18099 \
SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \
gemini "Return: ping for §13 recapture."

# OR Chrome path: configure Chrome to use http://127.0.0.1:18099 as
# HTTPS proxy, open Gemini Advanced in browser, send one prompt.
```

Rename to `captures/gemini-advanced-clienthello-<ts>.jsonl`.

### Amazon / Kiro CLI (AmazonQ-For-CLI)

```bash
mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py

HTTPS_PROXY=http://127.0.0.1:18099 \
SSL_CERT_FILE=~/.mitmproxy/mitmproxy-ca-cert.pem \
AWS_CA_BUNDLE=~/.mitmproxy/mitmproxy-ca-cert.pem \
kiro "Return: ping for §13 recapture."
```

`AWS_CA_BUNDLE` is needed because aws-sdk-rust may not honor
`SSL_CERT_FILE` alone. If still fails: temporary network-level CA trust
required.

Rename to `captures/kiro-cli-clienthello-<ts>.jsonl`.

## After Owner finishes the 4 captures

Hand the 4 jsonl files back. I (Claude) then per profile:

1. Diff the new jsonl against the existing profile JSON's TLS fields:
   - `alpn_protocols`, `cipher_suites`, `extensions`, `supported_versions`,
     `signature_algorithms`, `supported_groups`, `ec_point_formats`,
     `key_share_groups`, `psk_modes`, `padding_len`, `early_data_enabled`
   - Derive new JA3 / JA4 from the raw ClientHello bytes if drift is
     detected.
2. Update profile JSON for each drifted field. Citation in commit
   message + `_field_sources` block points to the new committed jsonl.
3. Flip `§12.2` table verdict: WEAK → PASS, citing the new jsonl path.
4. For GeminiAdvanced: if mitmproxy log shows h2 negotiation with
   cloudcode-pa, run Pass 2 through h2_capture_server.py (or extend
   mitmproxy capture to also dump SETTINGS via its h2 hooks — depends
   on mitmproxy version capability).
5. Per-vendor commit; each commit is a discrete slice (Owner "一个一个来"
   cadence preserved).

## Owner decisions surfaced in advance

1. **Network path**: any of the 4 vendor APIs may detect or rate-limit
   mitmproxy interception. If a vendor refuses requests through the
   proxy, fallback to the Go libpcap passive sniffer (no CA needed but
   needs admin/sudo for raw socket access).
2. **Account exposure**: each capture sends one real prompt via your
   actual auth credentials. Mitmproxy decrypts headers; the addon does
   NOT log application data per its scope statement (addon line 22-28),
   but real bearer tokens DO pass through mitmproxy in memory during
   the request. Use throwaway prompts.
3. **CLI version pinning**: record `claude --version` / `codex --version`
   etc. at capture time; the addon doesn't see this, you'll need to
   note it manually so I can write it into `_field_sources.b`.

## Scope NOT in this plan

- F-1.e implementation (still NOT STARTED, per Gate 4 spec).
- Vendor expansion beyond the 4 currently-deployed profiles.
- Continuous re-capture cadence (one-time §13 only; future drift detection
  is a separate concern).

## What I (Claude) do NEXT (without waiting for Owner)

Nothing on the recapture itself — that's Owner-driven. But I CAN:

- (Optional A) Pre-verify `capture_tls_clienthello.py` still works
  against the current mitmproxy 12.x install on Owner's machine — by
  asking Owner to run a 1-line smoke test (mitmdump + curl) and paste
  output. If addon API drifted, fix it before the real captures.
- (Optional B) Extend the addon to also capture h2 SETTINGS via
  mitmproxy's `tcp_message` or h2-frame hooks (if mitmproxy 12 exposes
  any). Earlier session attempt (F-1.g attempt-1, dcee914 predecessor)
  showed mitmproxy 12 doesn't expose SETTINGS via the addon API surface
  — so this would likely fail again. Lower priority.

## Mutation discriminator for this plan

If a future commit updates a profile JSON's TLS field but the commit
message cites only the old 2026-05-14 ephemeral `/tmp` path (now
SUPERSEDED per §12-method-tags), codex per-commit review must HIGH-block:
the §13 procedure mandates citing the new committed
`captures/<vendor>-clienthello-<ts>.jsonl` path.
