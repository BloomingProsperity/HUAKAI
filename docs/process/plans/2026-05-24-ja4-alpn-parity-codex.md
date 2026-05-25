# 2026-05-24 JA4 ALPN parity fix

| Field | Plan |
| --- | --- |
| Owner directive | "codex review 报 P2 finding — backend/tools/clienthello-collector/fingerprint.go 的 JA4 ALPN 编码与 HUAKAI 项目约定不一致。" |
| Scope | Investigate HUAKAI's existing JA4 ALPN convention, patch the local ClientHello collector if needed, regenerate/sync the Anthropic CLI mimicry fixture and runtime template, and verify tests. Do not change `LICENSE`, add dependencies, touch schema/auth/billing/quota core, stage, commit, or push. |
| Success criteria | `["h2","http/1.1"]` encodes consistently across Gemini and Anthropic templates; the collector has a focused test proving the convention; Anthropic captured fixture, builtin template, and runtime JSON agree; requested build/test/mutation checks are run and reported. |
| Time estimate | 45-75 minutes wall clock; one Codex execution pass. |
| Blast radius | Limited to `backend/tools/clienthello-collector`, `backend/internal/transport/mimicry` fixture/builtin data, and `tools/fingerprint-collector/templates/anthropic-claude-code.json`. No new files will be added to frozen packages. |
| Failure modes | Existing fixture may be hand-edited rather than generated; mitigation is to inspect metadata and derive the project convention from both source and fixture evidence. Local capture may fail due network/TLS environment; mitigation is to report the blocker and only make deterministic fixture edits from observed collector output if capture is unavailable. ALPN rule may conflict with official JA4; mitigation is to state whether this is a HUAKAI compatibility convention or spec correction. |
| Decision points | If source evidence shows Gemini's `ht` is a stale fixture bug, Owner confirmation would be needed before changing Gemini runtime behavior. Otherwise follow Owner's preferred path X and preserve the pre-existing Gemini convention. Reference-project comparison: no non-HUAKAI reference project behavior is being used for this decision; the comparison source is HUAKAI's own existing Gemini template plus, if local source lacks a rule, the public JA4 specification as protocol context. |
| Pre-execution checklist | Check current diff; grep for JA4 ALPN rules; inspect Gemini/Codex/Anthropic template metadata; add a discriminating collector test before production edits; patch only existing files; regenerate/sync Anthropic fixture data; run requested checks and mutation self-check; leave all changes unstaged. |

## Concrete execution order

1. Inspect `backend/tools/clienthello-collector/fingerprint.go`, collector tests, template JSON, and mimicry tests for the current ALPN token rule.
2. If local source lacks an explicit rule, check the public JA4 specification only as protocol context and record whether HUAKAI intentionally preserves a project convention.
3. Add a focused failing test that proves `nil`/empty ALPN maps to `00` and `["h2","http/1.1"]` maps to the Gemini-compatible token.
4. Patch `fingerprint.go` minimally to satisfy the test.
5. Rerun the Anthropic capture command with `servername: "api.anthropic.com"` and update `backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json`.
6. Sync the same JA4 value into `backend/internal/transport/mimicry/template.go` and `tools/fingerprint-collector/templates/anthropic-claude-code.json`.
7. Run `cd backend && GOCACHE=/tmp/go-build go build ./... && go test ./internal/transport/mimicry/... -count=1 -race`.
8. Temporarily mutate the Anthropic builtin JA4 field, confirm `TestAnthropicCLIMimicryV1BuiltinMatchesCapturedTemplate` fails, restore, and rerun the focused test.
