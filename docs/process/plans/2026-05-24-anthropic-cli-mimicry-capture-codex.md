# 2026-05-24 anthropic-cli-mimicry capture codex plan

| Owner directive | "[OWNER AUTHORIZED 2026-05-24T11:55Z workspace-write — Anthropic CLI fingerprint 抓包验证]" |
| Scope | In: add a temporary Go ClientHello collector, write `anthropic-cli-mimicry-v1.json`, add focused mimicry template tests, compare JA3/JA4 against existing Codex CLI templates, update loader support for parsed key-share/PSK metadata, and update the Anthropic builtin as an independent template if hashes differ. Out: no frozen-package new files, no new dependency, no schema/auth/billing/quota changes, no git add/commit/push. |
| Success criteria | Collector captures a real Node v22.22.2/OpenSSL 3.5.5 ClientHello from the Owner `https.get` path or, if sandbox blocks TCP listen, from an equivalent Node TLS custom-Duplex wire record using the same ALPN options; `backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json` is produced from wire bytes; JA3 and JA4 are compared against `backend/internal/transport/mimicry/testdata/clienthello-template.json` and `tools/fingerprint-collector/templates/codex-cli.json`; focused tests pass; `go build`, `go test`, and relevant `cargo build` pass; final output includes `DONE` and hash comparison. |
| Time estimate | 45-75 minutes wall clock; one Codex executor session. |
| Blast radius | Low-medium. New files are a tool under `backend/tools/clienthello-collector`, a test fixture under `backend/internal/transport/mimicry/testdata`, and a focused test file in non-frozen `backend/internal/transport/mimicry`. `registry.go` changes are limited to reading collector metadata already emitted by the new template schema. |
| Failure modes | Port 9443 occupied: use `ss` to identify and stop only our own stale process or retry after process exits. Sandbox denies TCP listen: verify with a minimal Node listener, then capture the same Node/OpenSSL ClientHello bytes through a local `tls.connect` custom Duplex and feed them to collector `-stdin`. Node sends no ClientHello: rerun with the exact Owner ALPN options and verify listener or stdin bytes. Parser bug: inspect captured bytes and fix collector, keeping output schema aligned with existing `clienthello-template.json`. JA4 uncertainty: use a conservative deterministic JA4 implementation matching the local collector formulas where available; record any limitation instead of fabricating. Existing unrelated dirty files: leave untouched. |
| Decision points | If JA3/JA4 match Codex CLI, alias `anthropic-cli-mimicry-v1` to the existing ChatGPT/Codex template path as directed. If they differ, keep a standalone JSON template and do not alias. If a required change touches high-risk files (`LICENSE`, secrets, auth core, billing ledger, quota, schema, deployment), stop for Owner confirmation. |
| Reference project comparison | Not applicable: this plan does not ask Owner to choose among product behavior options and does not make claims about non-MIT reference project behavior. Evidence is local wire capture plus HUAKAI-local templates. |
| Pre-execution checklist | 1. Read existing mimicry registry/template loaders and current testdata schema. 2. Write failing test for `anthropic-cli-mimicry-v1.json` loading and TLS 1.3 cipher presence. 3. Build focused collector with no external dependencies. 4. Run collector listener and trigger Node `https.get` against `127.0.0.1:9443`; if sandbox blocks TCP listen, use Node TLS custom-Duplex capture with the same ALPN options. 5. Write captured JSON. 6. Compare JA3/JA4 with both local Codex/legacy templates. 7. Apply registry alias change only if hashes match; otherwise update the builtin Anthropic template to the standalone capture. 8. Run focused `go test`, backend `go build`, backend `go test`, and relevant `cargo build`. 9. Report Chinese Owner summary with shrinkage, clean-room risk, security risk, and next steps. |

## Concrete execution order

1. Add `backend/internal/transport/mimicry/anthropic_template_test.go` first and run the focused test to observe the expected red failure before the fixture exists.
2. Add `backend/tools/clienthello-collector/main.go` with a minimal TCP listener, TLS record reader, ClientHello parser, JA3/JA4/hash calculation, and JSON encoder matching the legacy collector schema.
3. Build the collector from `backend/`.
4. Run the collector against `127.0.0.1:9443`, trigger the Owner-provided Node command, and save output to `backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json`. If the sandbox denies TCP listen, use Node `tls.connect` over a custom Duplex to emit the same ClientHello record to stdout and feed collector `-stdin`.
5. Compare the new template's JA3 hash and JA4 hash against `backend/internal/transport/mimicry/testdata/clienthello-template.json` and `tools/fingerprint-collector/templates/codex-cli.json`.
6. If the hashes match, update `backend/internal/transport/mimicry/registry.go` to alias the Anthropic CLI mode to the existing ChatGPT/Codex template mechanism. If they differ, keep the standalone captured template and update the builtin Anthropic template to match it.
7. Run `gofmt`, focused tests, collector build, and broader Go tests that are available in the backend module.

## Self-check

- No plan step creates files in frozen packages `backend/internal/gatewayhttp`, `backend/internal/gateway`, or `backend/internal/proto`.
- No new runtime dependency is introduced.
- The test fixture is discriminating: before capture it fails because the required template file is absent; after capture it asserts real parsed fingerprint fields and TLS 1.3 ciphers.
- No reference-project source is read or summarized.
- User's absolute prohibition on `git add`, `git commit`, and `git push` is binding.
