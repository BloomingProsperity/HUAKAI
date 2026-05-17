# R-3-A Vendor Profile Audit Notes

UTC: 2026-05-17T12:17:12Z

Scope: HUAKAI internal templates only:

- `tools/fingerprint-collector/templates/codex-cli.json`
- `tools/fingerprint-collector/templates/kiro-cli.json`
- `tools/fingerprint-collector/templates/gemini-advanced.json`

Schema completeness checked for each profile: `ja3_hash`, `cipher_suites`, `extensions`,
`supported_groups`, `ec_point_formats`, `supported_versions`, `signature_algorithms`,
and `alpn_protocols` are present.

| Builtin profile | Vendor | JA3 hash | `tls.extensions` | ECH 65037 | OCSP 5 | SCT 18 |
|---|---|---|---|---|---|---|
| `CodexCli` | OpenAI | `0e0088de64e0c3adf8e9d8c19c811eb3` | `[65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51]` | no | no | no |
| `KiroCli` | Kiro | `ed5338278fb7f0fb5cfd4ad58a98241f` | `[10, 43, 51, 0, 45, 11, 5, 35, 23, 13]` | no | yes | no |
| `GeminiAdvanced` | Gemini | `55ba290366f110228d176d92fe6f6180` | `[65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]` | no | no | no |

R-3-A conclusion: ECH grease must not be enabled for these three profiles. OCSP is only
enabled for Kiro. SCT is not enabled for these three profiles. This keeps Boring extension
injection profile-aware per plan risk `R-MIMICRY-003`.

## Boring wire verification status

After extending the Boring path, the byte-level tests intentionally still fail for these
three profiles rather than masking the mismatch:

| Builtin profile | Observed Boring JA3 | Expected profile JA3 | Result |
|---|---|---|---|
| `CodexCli` | `895cf525f29f1814456a344ddd401799` | `0e0088de64e0c3adf8e9d8c19c811eb3` | mismatch: Boring order is `[0, 23, 65281, 10, 11, 35, 13, 51, 45, 43]`; profile expects `[65281, 0, 11, 10, 35, 22, 23, 13, 43, 45, 51]` |
| `KiroCli` | `b50f741d6acb3d692cd670021afc98ce` | `ed5338278fb7f0fb5cfd4ad58a98241f` | mismatch: Boring order is `[0, 23, 65281, 10, 11, 35, 5, 13, 51, 45, 43]`; profile expects `[10, 43, 51, 0, 45, 11, 5, 35, 23, 13]` |
| `GeminiAdvanced` | `6734cb909105d91f131ced408995d016` | `55ba290366f110228d176d92fe6f6180` | mismatch: Boring order is `[0, 23, 65281, 10, 11, 35, 16, 13, 51, 45, 43]`; profile expects `[65281, 0, 11, 10, 35, 16, 22, 23, 13, 43, 45, 51]` |

Current Boring 5.1 public API also rejects some profile groups/signature algorithms during
connector construction. The builder skips those unsupported public-API tokens so tests can
capture and report the actual wire mismatch instead of stopping before ClientHello capture.
