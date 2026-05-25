# HTTP/2 Fingerprint Fixtures

W11-F F-1.a (evidence + fixture contract, 2026-05-25).

## Scope

This directory holds **real-upstream captured** HTTP/2 frame fixtures used by the
F-1 byte-level mimicry test suite. A fixture pins what HUAKAI's outbound HTTP/2
fork client must produce, byte-for-byte, when mimicking a given upstream CLI
tool.

**A fixture under this directory MUST come from real upstream capture, never from
synthetic / hand-written values.** Synthetic profile values are allowed for
adapter-level regression tests (see `tests/mimicry_http2_adapter_test.rs` line
79-109 `codex_profile_with_h2_order` helper labeled "synthetic"), but they are
**not** sufficient evidence for F-1 Released status per
`docs/process/release-readiness/W11-F-F1-status.md`.

## Filename convention

`<builtin-profile-template-name-base>-h2.json`, where the base matches the
`tools/fingerprint-collector/templates/<name>.json` filename (without extension)
or, for Anthropic, the `src/mimicry/profiles/anthropic_claude_code.json` base.

Fixture filename = `BuiltinProfile::template_name()` with `.json` stripped, plus
`-h2.json` (so all entries are hyphenated to match `template_name()` regardless
of the on-disk profile JSON filename convention):

| BuiltinProfile | template_name() | fixture path (when capture lands) |
|---|---|---|
| `AnthropicClaudeCode` | `anthropic-claude-code.json` | `tests/fixtures/http2_fingerprint/anthropic-claude-code-h2.json` |
| `CodexCli` | `codex-cli.json` | `tests/fixtures/http2_fingerprint/codex-cli-h2.json` |
| `KiroCli` | `kiro-cli.json` | `tests/fixtures/http2_fingerprint/kiro-cli-h2.json` |
| `GeminiAdvanced` | `gemini-advanced.json` | `tests/fixtures/http2_fingerprint/gemini-advanced-h2.json` |

Note: the on-disk profile JSON for Anthropic is `anthropic_claude_code.json`
(underscored, at `src/mimicry/profiles/`) but `template_name()` returns the
hyphenated label `anthropic-claude-code.json`. Tests use `template_name()` so
they don't double-bind to the on-disk filename.

## Required fixture schema

```json
{
  "_comment": "F-1.a fixture schema; real-upstream H2 evidence",
  "captured_at": "2026-05-25T00:00:00Z",
  "capture_source": "mitmproxy 12.x + addon",
  "capture_addon": "tools/fingerprint-collector/capture_h2_settings.py",
  "target_authority": "api.anthropic.com",
  "target_path": "/v1/messages",
  "tls_alpn_negotiated": "h2",
  "h2_settings_frame": {
    "raw_order": [4, 1, 6, 5, 2, 3],
    "values": {
      "1": 4096,
      "2": 0,
      "3": 100,
      "4": 65535,
      "5": 16384,
      "6": 262144
    },
    "raw_frame_hex": "<full 9-byte header + payload, hex>"
  },
  "h2_pseudo_header_order": {
    "order": [":method", ":authority", ":scheme", ":path"],
    "headers_frame_hex_prefix": "<first 64 bytes of HEADERS frame, hex>"
  },
  "limitation_note": null
}
```

All keys are required when the fixture exists. `limitation_note` may be a string
documenting an open caveat (e.g. "only model_api_ht_alpn variant captured;
auxiliary_no_alpn variant pending").

## Cross-check tests

`tests/mimicry_http2_fixture_test.rs` enforces the consistency invariants:

1. If a builtin profile sets `h2_settings_frame.available = true`, the fixture
   file under this directory MUST exist (or test red).
2. The fixture's `h2_settings_frame.raw_order` MUST match the profile's
   `h2_settings_frame.raw_order` byte-for-byte (or test red).
3. The fixture's `h2_settings_frame.values` MUST be a superset of the profile's
   `h2_settings_frame.values` (or test red).
4. The fixture's `h2_pseudo_header_order.order` MUST match the profile's
   `h2_pseudo_header_capture.order` byte-for-byte (or test red).
5. If a profile sets `available = false`, NO fixture file should exist for it
   (or test red — protects against stale fixtures lingering after a profile
   regression).

These tests pass trivially today because all 4 builtin profiles have
`available = false`. When F-1.g lands the first real Anthropic H2 capture and
flips `available = true` for the Anthropic profile, the corresponding fixture
file under this directory becomes mandatory.

## Capture method (when F-1.g runs)

Reuse the F-2.5 mitmproxy CA-install workflow
(`docs/process/release-readiness/W11-F-F2-5-status.md` §2) but with a different
addon (`tools/fingerprint-collector/capture_h2_settings.py`) that hooks
mitmproxy's `tls_established_client` / `http2_start_client` events to capture
the first SETTINGS and HEADERS frames after handshake (in mitmproxy 12.x, those
events fire post-TLS-decryption and pre-application). The CA must be
**uninstalled immediately after capture** per F-2.5 §8.6 security hygiene.

## Non-goals

- **No source mining** of non-MIT reference projects to populate fixtures.
  Fixtures come from real wire capture only; clean-room boundary preserved.
- **No synthetic top-up**: a fixture is either fully real-captured or it does
  not exist. Half-real / half-synthetic fixtures are explicitly forbidden by
  the cross-check test (a partial fixture would fail invariant #2 or #3).
- **No backfill of historical profiles** without re-capture. The
  `anthropic_claude_code.json` `limitation_note` "collector v1 未捕获" stays
  honest until F-1.g re-captures with the v2 addon.
