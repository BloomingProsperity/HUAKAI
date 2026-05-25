# R-D Smoke Runbook

## Lane Header

=== CLEAN-ROOM LANE GUARD ===

LANE: IMPLEMENTER

REFERENCE PROJECTS IN SCOPE: none. The 15 auth-mode names are Owner-provided task input for this scaffold.

HARD PROHIBITIONS: do not read or copy non-MIT reference-project source; do not include real credentials; do not run live upstream tests in this mock-only stage.

CITATION POLICY: source anchors below cite HUAKAI internal files only.

=== END CLEAN-ROOM LANE GUARD ===

## Purpose

R-D verification needs 15 cells: Anthropic, OpenAI, and Gemini across five auth modes each. This runbook covers the mock-only scaffold. Live upstream validation waits for Owner credentials in `~/secrets/{anthropic,openai,gemini}/`.

Existing HUAKAI R-D artifact code records local TLS capture fields and deliberately does not pretend JA3/JA4 hashes were computed when they were not (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_artifact.rs:13-60`). The smoke scaffold follows that truth-first boundary: it writes scaffold artifacts, not live validation claims.

## Run Mock Stage

```bash
cd exploratory/rust-core-gateway/merged
tools/r-d-smoke/run.sh < /dev/null
```

Optional:

```bash
cd exploratory/rust-core-gateway/merged
HUAKAI_RD_SMOKE_ARTIFACT_DIR="$PWD/target/r-d-smoke/artifacts" \
  tools/r-d-smoke/run.sh < /dev/null
```

Expected output:

- `summary.json`
- `matrix.tsv`
- `cells.jsonl`
- `vendor/auth_mode/cell.json` for all 15 cells

## 15 Cell Matrix

| vendor | auth mode | mock status |
| --- | --- | --- |
| anthropic | `api_key` | `PendingLane2b` |
| anthropic | `claude_ai_oauth` | `PendingLane2b` |
| anthropic | `claude_code` | `PendingLane2b` |
| anthropic | `bedrock` | `PendingLane2b` |
| anthropic | `vertex_anthropic` | `PendingLane2b` |
| openai | `api_key` | `PASS` |
| openai | `chatgpt_oauth` | `PASS` |
| openai | `codex_cli_oauth` | `PASS` |
| openai | `azure` | `PASS` |
| openai | `refresh_token` | `PASS` |
| gemini | `aistudio_api_key` | `PASS` |
| gemini | `vertex_sa` | `PASS` |
| gemini | `code_assist` | `PASS` |
| gemini | `google_one` | `PASS` |
| gemini | `antigravity` | `PASS` |

## Mock Gate

For every cell, the scaffold writes a mock-stage artifact with this route contract:

```text
HUAKAI Rust gateway -> local hyper mock server -> capture_artifact -> capture_diff
```

The PASS condition is `ExtensionsListStatus.Subset`. HUAKAI's diff helper defines the `Subset` variant and maps missing-free extension comparisons to it (`exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:41-58`, `exploratory/rust-core-gateway/merged/crates/core_gateway/tests/common/capture_diff.rs:179-238`).

Current HUAKAI builtin mimicry templates are OpenAI/Codex CLI, Kiro CLI, and Gemini Advanced (`exploratory/rust-core-gateway/merged/crates/core_gateway/src/mimicry/profile.rs:18-55`). The OpenAI and Gemini auth-mode cells therefore use mock template aliases until per-auth-mode captures are added. Anthropic cells stay `PendingLane2b` and must not be labeled as credential misses.

## Live Gate

The mock-only script never reads credential contents and never calls live upstream.

Later live execution should use this gate:

```bash
export HUAKAI_RD_LIVE=1
test -f "$HOME/secrets/<vendor>/<mode>.txt" -o -f "$HOME/secrets/<vendor>/<mode>.json"
```

If the gate is enabled and a matching secret exists, the current scaffold records `LiveReadyButDisabled`. If the gate is disabled or no matching secret exists, it records `Skipped`. It does not emit `missing credential`; skipped live cells are not failures in the mock stage.

Real upstream capture remains Owner-local. The existing recapture runbook states that Owner uses their own account/network, keeps raw secrets/prompts out of artifacts, and only preserves fingerprint metadata (`exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:1-8`, `exploratory/rust-core-gateway/merged/tools/recapture/RUNBOOK.md:27-33`).

## Owner Next Step

When credentials are ready:

1. Put files under `~/secrets/<vendor>/<mode>.txt` or `~/secrets/<vendor>/<mode>.json`.
2. Run the mock scaffold with `HUAKAI_RD_LIVE=1` to confirm which cells are gate-ready.
3. Approve the live driver line that turns `LiveReadyButDisabled` into actual live execution.
4. Keep Anthropic in `PendingLane2b` until the Lane 2b failure mode is released separately.

