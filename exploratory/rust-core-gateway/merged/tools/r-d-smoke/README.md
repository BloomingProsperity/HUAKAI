# HUAKAI R-D Smoke Scaffold

Lane: IMPLEMENTER. This tool is mock-only and reads no reference-project source.

The scaffold fixes the R-D verification matrix at 15 cells: 3 vendors by 5 auth modes. It writes deterministic artifacts so the live path can be wired after Owner credentials exist under `~/secrets/<vendor>/<mode>.txt` or `~/secrets/<vendor>/<mode>.json`.

## Run Mock Stage

From `exploratory/rust-core-gateway/merged`:

```bash
tools/r-d-smoke/run.sh < /dev/null
```

Optional artifact directory:

```bash
HUAKAI_RD_SMOKE_ARTIFACT_DIR="$PWD/target/r-d-smoke/artifacts" \
  tools/r-d-smoke/run.sh < /dev/null
```

The runner creates:

- `summary.json`
- `matrix.tsv`
- `cells.jsonl`
- one `cell.json` per vendor/auth-mode cell

## Matrix

| vendor | auth modes |
| --- | --- |
| anthropic | `api_key`, `claude_ai_oauth`, `claude_code`, `bedrock`, `vertex_anthropic` |
| openai | `api_key`, `chatgpt_oauth`, `codex_cli_oauth`, `azure`, `refresh_token` |
| gemini | `aistudio_api_key`, `vertex_sa`, `code_assist`, `google_one`, `antigravity` |

Anthropic cells are marked `PendingLane2b`, not `missing credential`.

## Mock Versus Live

Mock stage always writes an artifact. For non-Anthropic cells it records `ExtensionsListStatus.Subset` as the scaffold PASS condition. For Anthropic cells it records `PendingLane2b`.

Live stage is gated and disabled in this mock-only stage. The script checks only for the gate shape:

```bash
HUAKAI_RD_LIVE=1 tools/r-d-smoke/run.sh < /dev/null
```

If `HUAKAI_RD_LIVE=1` and a matching secret file exists, the cell status becomes `LiveReadyButDisabled`. The script does not read secret contents and does not call live upstream.

## Source Anchors

- R-D artifact shape and artifact directory convention: `crates/core_gateway/tests/common/capture_artifact.rs:13-60`.
- `ExtensionsListStatus.Subset` semantics: `crates/core_gateway/tests/common/capture_diff.rs:41-58` and `crates/core_gateway/tests/common/capture_diff.rs:179-238`.
- Current HUAKAI builtin mimicry templates: `crates/core_gateway/src/mimicry/profile.rs:18-55`.
- Existing real-upstream safety boundary: `tools/recapture/RUNBOOK.md:1-8` and `tools/recapture/RUNBOOK.md:27-33`.

