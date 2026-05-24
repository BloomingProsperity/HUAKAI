# 2026-05-24 mimicry review fixes

| Field | Plan |
| --- | --- |
| Owner directive | "codex review on uncommitted mimicry changes 给出 3 项 finding 必须在 commit 前修复" |
| Scope | Fix the Anthropic CLI mimicry SNI capture/template mismatch, sync the runtime template loaded by gateway startup, and split `backend/tools/clienthello-collector/main.go` by responsibility. Do not change frozen packages, add dependencies, stage, commit, or push. |
| Success criteria | `AnthropicCLIMimicryV1Template()` matches the regenerated SNI-bearing fixture; `tools/fingerprint-collector/templates/anthropic-claude-code.json` loads the same runtime template; collector source files are each under 300 lines and single-responsibility; requested build/test/vet/mutation checks are run and reported. |
| Time estimate | 45-75 minutes wall clock; one Codex execution pass. |
| Blast radius | Limited to mimicry template data, mimicry tests, and the local ClientHello collector tool. Gateway runtime template loading can fail if the synced JSON is malformed, so tests must load the real template directory. |
| Failure modes | Local Node capture may not emit SNI or may fail to connect; mitigation is to fall back to explicit builtin SNI insertion only if path A fails and record that choice. Splitting collector may accidentally move helpers across files with missing imports; mitigation is `go vet` and `go build ./tools/clienthello-collector`. Runtime template sync may leave old assumptions in tests; mitigation is updating tests to compare runtime, builtin, and captured values. |
| Decision points | No Owner sign-off expected unless path A cannot run locally or a high-risk file/dependency/schema/auth/billing/quota change becomes necessary. |

## 参考项目对照

No exact non-HUAKAI reference-project equivalent was observed for this review-fix bundle because it is enforcing HUAKAI-internal rules: SNI-bearing first-party capture, runtime template schema sync, and collector file split. Adjacent references show different concern boundaries:

- CLIProxyAPI anchor cite: `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/helps/utls_client.go:87` prepares the TLS server name inside its Go transport, and `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/helps/utls_client.go:88` constructs the TLS client in-process. Summary: the adjacent reference has TLS mimicry logic, but not HUAKAI's normalized runtime-template-plus-raw-fixture split.
- Envoy AI Gateway `envoyproxy/ai-gateway@4d3eae8b35c4:README.md:7` describes a two-tier gateway pattern, and `envoyproxy/ai-gateway@4d3eae8b35c4:docs/proposals/006-mcp-gateway/proposal.md:55` places protocol-specific proxy logic inside the AIGW sidecar process. Summary: the adjacent reference is an Envoy-centered gateway architecture, not a local fingerprint collector/template schema enforcement path.
- HUAKAI internal basis: `tools/fingerprint-collector/templates/SCHEMA.md:43` defines normalized `ja3` as a flat string, `tools/fingerprint-collector/templates/SCHEMA.md:75` requires numeric `ec_point_formats`, and `AGENTS.md:538` requires responsibility-based package/file organization. Summary: this plan's decisions are primarily internal rule compliance, with no feature reduction.

| Pre-execution checklist | Verify current diff; confirm target packages are not frozen; reproduce the no-SNI fixture state; regenerate fixture with explicit `servername`; update builtin and runtime JSON; split collector; run requested checks; run mutation self-check and restore. |

## Concrete execution order

1. Run the collector on `127.0.0.1:9443` and trigger Node TLS with `servername: "api.anthropic.com"` so the captured ClientHello includes extension `0`.
2. Replace `backend/internal/transport/mimicry/testdata/anthropic-cli-mimicry-v1.json` with the regenerated output.
3. Update `backend/internal/transport/mimicry/template.go` so the builtin values match the regenerated capture.
4. Sync the same TLS fields into `tools/fingerprint-collector/templates/anthropic-claude-code.json`, preserving the runtime filename loaded by existing gateway startup code.
5. Tighten mimicry tests so captured, builtin, and runtime templates agree on the discriminating TLS fields, including extension `0` and `t13d` JA4.
6. Split `backend/tools/clienthello-collector/main.go` into `main.go`, `record.go`, `extensions.go`, `fingerprint.go`, and `output.go`, all in package `main`, each with one Chinese responsibility comment.
7. Run `go build ./...`, `go test ./internal/transport/mimicry/... -count=1 -race`, and `go vet ./internal/transport/mimicry/... ./tools/clienthello-collector` from `backend/`.
8. Temporarily mutate the builtin first extension from `0` to `99`, confirm `TestAnthropicCLIMimicryV1BuiltinMatchesCapturedTemplate` fails, then restore and rerun the test.

## Clean-room Provenance

- Source files read: `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/helps/utls_client.go:87`; `luispater/CLIProxyAPI@50d19e204fedd92f8f15b97e8e89bd0317f5e0b6:internal/runtime/executor/helps/utls_client.go:88`; `envoyproxy/ai-gateway@4d3eae8b35c4:README.md:7`; `envoyproxy/ai-gateway@4d3eae8b35c4:docs/proposals/006-mcp-gateway/proposal.md:55`
- Lane: specifier
- Agent: codex-cli
- UTC: 2026-05-24T1407Z
