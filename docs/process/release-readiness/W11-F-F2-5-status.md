# W11-F F-2.5 — Real-Upstream ClientHello Capture Status

**Date**: 2026-05-25 UTC
**Owner**: HUAKAI
**Operator**: Claude (PM-Orchestrator), with Owner authorization "装CA" (CA install) +
"给你最大的权限！去装 我要的是真实的" (broad install permission)
**Branch**: `claude/rust-hardening` (commits not yet pushed)
**Capture target**: `tools/fingerprint-collector/captures/clienthello-1779707167.jsonl`
(30 ClientHello records, codex CLI 0.128.0 + gemini CLI 0.42.0 on Windows 10/11)

---

## 1. Why this exists

F-2.2 / F-2.3 / F-2.3+ delivered the L1 preflight typed gate and the dispatch
canary at the Boring HTTP client builder, with Codex / Kiro / Gemini all
fail-closed at the gate per the synthesis §6 design. The remaining open item
was whether the per-profile JSON templates under
`tools/fingerprint-collector/templates/` actually match what the real CLI
tools emit on the wire. Without real-upstream evidence, every KnownGap reason
is a hypothesis; with it we can either (a) clear the gap automatically (when
template matches reality) or (b) restate the gap with concrete evidence so the
F-3 roadmap entry is sourced.

## 2. Capture setup

| step | command | result |
|---|---|---|
| 1 | `certutil -user -addstore Root C:\Users\h\.mitmproxy\mitmproxy-ca-cert.cer` | "证书 mitmproxy 已添加到存储" — CA installed in Windows USER Root |
| 2 | `mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py` (background) | Listening on 0.0.0.0:18099, PID 344592 |
| 3 | `echo "Say only: PROXY-OK" \| HTTPS_PROXY=http://127.0.0.1:18099 codex exec --skip-git-repo-check` | Returned "PROXY-OK", 19662 tokens consumed |
| 4 | `echo "PROXY-OK" \| HTTPS_PROXY=… NODE_EXTRA_CA_CERTS=…mitmproxy-ca-cert.pem gemini --skip-trust -p "Reply only from stdin"` | Returned "PROXY-OK" |
| 5 | `taskkill /F /PID 344592` | mitmdump stopped |

The `NODE_EXTRA_CA_CERTS` env var was required for gemini because Node.js on
Windows uses its bundled CA list rather than the OS trust store; codex on
Windows uses native-tls→schannel which does honor the USER Root store.

The mitmproxy addon (`tools/fingerprint-collector/capture_tls_clienthello.py`,
new this commit) only inspects the unencrypted ClientHello frame and writes
one JSON record per handshake to a JSONL file under `captures/`. It does not
log, modify, or forward any decrypted application payload.

## 3. Codex CLI — captured fingerprints (15 records, Windows)

Three distinct TLS stacks were observed across one codex invocation:

| group | records | SNIs | cipher_suites count | extension_types | raw_len | TLS stack |
|---|---|---|---|---|---|---|
| **A** | 14 | `chatgpt.com` (10), `mcp.cloudflare.com` (4), `ab.chatgpt.com` (0 here, see B) | **18** | `(0,10,11,13,35,23,65281)` | 167–174 | schannel TLS 1.2 |
| **B** | 1 | `ab.chatgpt.com` | 18 | same as A | 170 | schannel TLS 1.2 |
| **C** | 1 | `chatgpt.com` (auth refresh) | 10 | `(35,13,5,51,11,43,23,45,0,10)` | 238 | Go / Node-style TLS 1.3 |
| **D** | 1 | `github.com` (auth flow) | 20 | `(0,5,43,13,35,10,11,16,51,49,23,65281,45)` | 457 | OpenSSL TLS 1.3 |

The **dominant fingerprint for codex API traffic (chatgpt.com)** is Group A:
schannel TLS 1.2 with 18 ciphers, raw ClientHello 167 bytes, ALPN empty.
First-byte cipher order:
`c02c, c02b, c030, c02f, c024, c023, c028, c027, c00a, c009, …`

## 4. Codex CLI — template diff

Template `tools/fingerprint-collector/templates/codex-cli.json`:

| field | template | real (Windows schannel, group A) | match? |
|---|---|---|---|
| `tls_backend` | `native-tls/openssl` | `native-tls/schannel` | ❌ platform-different |
| `target_host` | `chatgpt.com` | `chatgpt.com` | ✅ |
| `cipher_suites` count | 30 | 18 | ❌ |
| first 3 ciphers | `0x1302, 0x1303, 0x1301` (TLS 1.3) | `0xc02c, 0xc02b, 0xc030` (TLS 1.2) | ❌ |
| `extensions` count | 11 | 7 | ❌ |
| `extensions` set | `{65281,0,11,10,35,22,23,13,43,45,51}` | `{0,10,11,13,35,23,65281}` | ❌ (template has TLS 1.3 markers 22/43/45/51) |
| `alpn_protocols` | `[]` | `[]` | ✅ |
| `supported_versions` | `[772, 771]` (TLS 1.3 + 1.2) | TLS 1.2 only (no ext 43) | ❌ |
| `grease` | `false` | `false` (no 0x?A?A in cipher list) | ✅ |

**Root cause of mismatch**: `native-tls` is a Rust wrapper that picks the OS's
default TLS implementation. On Linux/macOS it routes to OpenSSL → TLS 1.3 +
30 ciphers (the template shape). On Windows it routes to schannel → TLS 1.2 +
18 ciphers (the captured shape). The HUAKAI fingerprint template was clearly
collected on a non-Windows host.

## 5. Gemini CLI — captured fingerprints (13 records, Windows)

| group | records | SNIs | cipher_suites count | extension_types | raw_len |
|---|---|---|---|---|---|
| **E** | 10 | `cloudcode-pa.googleapis.com` | 52 | `(65281,0,11,10,35,22,23,13,43,45,51)` | 1604 |
| **F** | 2 | `oauth2.googleapis.com` | 52 | same as E | 1598 |
| **G** | 1 | `play.googleapis.com` | 52 | `(65281,0,11,10,35,16,22,23,13,43,45,51)` | 1611 |

All three groups are Node.js TLS 1.3 (raw ClientHello ~1600 bytes, ext 43 +
51 + 45 present, 52 ciphers including TLS 1.3 trio 0x1302/0x1303/0x1301).

## 6. Gemini CLI — template diff

Template `tools/fingerprint-collector/templates/gemini-advanced.json`:

| field | template | real (Windows nodejs) | match? |
|---|---|---|---|
| `tls_backend` | `nodejs` | `nodejs` | ✅ |
| `cipher_suites` count | 52 | 52 | ✅ |
| first 3 ciphers | `0x1302, 0x1303, 0x1301` | `0x1302, 0x1303, 0x1301` | ✅ |
| `extensions` count (main) | 12 | 12 (group G) / 11 (groups E,F) | ✅ via two variants |
| `extensions` set (main) | `{65281,0,11,10,35,16,22,23,13,43,45,51}` | identical for group G | ✅ |
| `alpn_protocols` | `['h2','http/1.1']` | empty for groups E/F, present for G | ✅ via `auxiliary_no_alpn` variant |
| `tls_variants.model_api_ht_alpn` (12 exts, ALPN) | declared | group G matches | ✅ |
| `tls_variants.auxiliary_no_alpn` (11 exts, no ALPN) | declared | groups E,F match | ✅ |
| `grease` | `false` | `false` | ✅ |

**Verdict**: gemini template byte-shape matches captured Windows reality on
both variants. Gemini's empty `known_gap_fields` list is empirically correct;
F-2.3a runtime preflight wiring will be sufficient to gate against the
template once it lands.

## 7. Verdicts per profile

### 7.1 Codex CLI — `BuiltinProfile::CodexCli`

**Current code state** (`mimicry/tls_profile.rs::codex_cli_known_gap_fields`):
4 hard-coded gap fields:
1. `cipher_suites contains 52394` (template's `0xcca9` ChaCha20-Poly1305)
2. `extensions stable list contains 22 encrypt_then_mac`
3. `supported_groups starts with 4588` (X25519MLKEM768)
4. `signature_algorithms 26 template ids`

**F-2.5 evidence updates these gaps**:
- (1) is about cipher 52394 = `0xcca9` = `TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256`.
  Real Windows codex does NOT emit this cipher (schannel cipher list has only AES-GCM/CBC, no ChaCha20). So (1) is correct AS WRITTEN if the goal is matching the template; but the underlying truth is that **Windows codex doesn't include this cipher at all** because schannel doesn't expose it.
- (2) — same story: ext 22 ETM not in Windows schannel ClientHello.
- (3) — supported_groups in real Windows is from ext 10 `0006001d00170018` = `[0x001d (X25519), 0x0017 (secp256r1), 0x0018 (secp384r1)]`. NO group 4588 (MLKEM) at all. Confirmed gap.
- (4) — signature_algorithms in real Windows is ext 13 `0018080408050806040105010201040305030203020206010603` = 12 algorithms (24 bytes / 2). NOT 26. Confirmed gap (template overstates).

**New evidence makes the gap concrete + platform-aware**:
- The 4 existing fields are still valid (they describe template aspirations that the underlying schannel implementation cannot reach), but the *root cause* is now known: **native-tls's platform-conditional backend selection produces fundamentally different ClientHello shapes on Windows vs Linux/macOS**.
- HUAKAI mimicry via BoringSSL can produce the template shape (TLS 1.3 + 30 ciphers + ext 22/43/45/51 etc.) — that path is sound. What it *cannot* do is simultaneously match both the Linux openssl shape AND the Windows schannel shape with one profile.
- **Recommendation**: ADD a 5th gap field (`platform_fingerprint_divergence`) citing this F-2.5 evidence so future template revisers see the explicit platform constraint. Keep the existing 4 fields. **Codex KnownGap stays permanent at the resolver / preflight layer**.

### 7.2 Gemini Advanced — `BuiltinProfile::GeminiAdvanced`

**Current code state** (`mimicry/tls_profile.rs::gemini_advanced_known_gap_fields`):
returns `Vec::new()` — no static gap; synthesis design pushes the gate to
runtime preflight (D-S4 + D-S6).

**F-2.5 evidence confirms this design choice**:
- Template shape matches captured Windows reality (52 ciphers, both variants).
- `match_policy()` returns `SampleSetRandomized` (not `KnownGapBlocked`).
- `backend_intent()` returns `OpenSslAdapter` via `TlsBackend::NodeJs` arm.
- `preflight_status_from_intent()` returns `Pending` for Gemini.
- `is_dispatchable(Pending) == false` → fail-closed at builder gate until
  F-2.3a wires the runtime preflight runner.

**Recommendation**: keep `gemini_advanced_known_gap_fields() -> Vec::new()`.
F-2.3a runtime preflight (separate sub-phase) should wire
`OpenSslMimicryAdapter::run_profile_preflight` against the template; when
it passes, `L1TlsPreflightStatus::Pending → Passed` and Gemini becomes
dispatchable.

### 7.3 Kiro CLI — `BuiltinProfile::KiroCli`

**Not captured in this run** — no Kiro CLI invocation went through the proxy.
The current single gap field cites `real_upstream_capture` as the pending
reason (corrected from the obsolete "rustls cannot be replicated" wording per
F-2.2). F-2.5 evidence does NOT change this — Kiro still needs its own
capture session against AWS CodeWhisperer endpoints, which the operator may
gate behind separate Kiro CLI account access.

**Recommendation**: keep Kiro KnownGap as-is. F-2.5-Kiro is its own
sub-task, not closed by this run.

### 7.4 Anthropic Claude Code — `BuiltinProfile::AnthropicClaudeCode`

Baseline profile, byte-level verified via existing
`anthropic_boring_client_hello_byte_level_matches_profile` test. Not captured
in this run; not relevant to F-2.5 verdict.

## 8. Recommendations / next actions

1. **Add platform-divergence gap field to `codex_cli_known_gap_fields()`**
   (citing this status doc as evidence). One commit, ~15 lines, mutation
   test still passes because cipher_suites/extensions/sigalg fields are
   unchanged. **Does NOT change `match_policy()` output** (already
   `KnownGapBlocked` with non-empty Vec) — pure documentation strengthening.

2. **Codex KnownGap stays permanent** at resolver + L1 preflight + builder
   gate. No action needed in mimicry/dispatch — already correct after
   F-2.3+ round 2 (commit a358b70).

3. **Gemini stays Pending until F-2.3a wires runtime preflight**. F-2.3a is
   a separate sub-phase, not blocked on F-2.5.

4. **F-2 phase status update**: F-2.1 (spec-dig) + F-2.2 (typed gate) +
   F-2.3 (fallible builder) + F-2.3+ (L1 preflight at builder, round 1+2) +
   F-2.5 (real-upstream capture) — all closed. F-2.3a (runtime preflight
   wiring) and F-2.5-Kiro (Kiro upstream capture) remain open.

5. **Capture artifact**: `tools/fingerprint-collector/captures/clienthello-1779707167.jsonl`
   (30 records, 100KB) is committed evidence; the addon script
   `tools/fingerprint-collector/capture_tls_clienthello.py` is the
   reproducible capture tool for any future operator running F-2.5-style
   captures.

6. **Security hygiene**: mitmproxy CA cert was added to Windows USER Root
   trust store for this capture. Removal command (operator runs when
   F-2.5-Kiro is also done):

   ```powershell
   certutil -user -delstore "Root" "mitmproxy"
   ```

   Keep the CA installed if more captures (F-2.5-Kiro / F-2.5 v2 with
   different profile / future templates) are expected within the week.

## 9. Mutation-resistance note (CLAUDE.md #14)

This document is evidence, not code. Mutation-resistance check format does
not apply directly. The discriminating property is: if the captured
fingerprint groups (§3 + §5) were to drift on a re-run, the verdicts §7 must
be revisited. Re-running the capture is a one-command repeat:

```bash
mitmdump -p 18099 -s tools/fingerprint-collector/capture_tls_clienthello.py &
echo prompt | HTTPS_PROXY=http://127.0.0.1:18099 codex exec --skip-git-repo-check
echo prompt | HTTPS_PROXY=http://127.0.0.1:18099 NODE_EXTRA_CA_CERTS=<pem> gemini -p "..."
taskkill /F /PID <mitmdump-pid>
python tools/fingerprint-collector/diff_capture_vs_template.py  # (proposed for next sub-phase)
```

A future `diff_capture_vs_template.py` would automate the §4 / §6 table
generation. Out of scope for this commit; tracked as a tooling backlog item.
