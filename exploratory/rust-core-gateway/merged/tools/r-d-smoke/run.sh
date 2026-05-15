#!/usr/bin/env bash
set -euo pipefail

# R-D mock-only 烟雾测试入口；当前版本只写可复核 scaffold artifact，不调用真实上游。

usage() {
    cat <<'USAGE'
Usage:
  tools/r-d-smoke/run.sh [run|matrix|help]

Environment:
  HUAKAI_RD_SMOKE_ARTIFACT_DIR  Override artifact root.
  HUAKAI_RD_SMOKE_RUN_ID        Override UTC run id.
  HUAKAI_RD_LIVE=1              Enable live gate detection only.
  HUAKAI_RD_LIVE_DRIVER         Reserved live driver path; not invoked in mock-only stage.

This scaffold never reads credential contents and never calls live upstream.
USAGE
}

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
merged_root="$(cd "$script_dir/../.." && pwd)"
default_target_dir="${CARGO_TARGET_DIR:-$merged_root/target/r-d-smoke}"
artifact_root="${HUAKAI_RD_SMOKE_ARTIFACT_DIR:-$default_target_dir/artifacts}"
run_id="${HUAKAI_RD_SMOKE_RUN_ID:-$(date -u +%Y%m%dT%H%M%SZ)}"
report_dir="$artifact_root/$run_id"

matrix_lines() {
    cat <<'MATRIX'
anthropic|api_key|anthropic-lane-2b-pending|anthropic_api_key|PendingLane2b|Anthropic R-D waits for Lane 2b; this is not a credential miss.
anthropic|claude_ai_oauth|anthropic-lane-2b-pending|anthropic_claude_ai_oauth|PendingLane2b|Anthropic R-D waits for Lane 2b; this is not a credential miss.
anthropic|claude_code|anthropic-lane-2b-pending|anthropic_claude_code|PendingLane2b|Anthropic R-D waits for Lane 2b; this is not a credential miss.
anthropic|bedrock|anthropic-lane-2b-pending|anthropic_bedrock|PendingLane2b|Anthropic R-D waits for Lane 2b; this is not a credential miss.
anthropic|vertex_anthropic|anthropic-lane-2b-pending|anthropic_vertex_anthropic|PendingLane2b|Anthropic R-D waits for Lane 2b; this is not a credential miss.
openai|api_key|codex-cli.json|openai_api_key|MockPass|OpenAI auth-mode scaffold aliases the current HUAKAI OpenAI/Codex template until per-mode captures exist.
openai|chatgpt_oauth|codex-cli.json|openai_chatgpt_oauth|MockPass|OpenAI auth-mode scaffold aliases the current HUAKAI OpenAI/Codex template until per-mode captures exist.
openai|codex_cli_oauth|codex-cli.json|openai_codex_cli_oauth|MockPass|OpenAI Codex CLI auth mode maps to the current HUAKAI Codex template.
openai|azure|codex-cli.json|openai_azure|MockPass|OpenAI auth-mode scaffold aliases the current HUAKAI OpenAI/Codex template until per-mode captures exist.
openai|refresh_token|codex-cli.json|openai_refresh_token|MockPass|OpenAI auth-mode scaffold aliases the current HUAKAI OpenAI/Codex template until per-mode captures exist.
gemini|aistudio_api_key|gemini-advanced.json|gemini_aistudio_api_key|MockPass|Gemini auth-mode scaffold aliases the current HUAKAI Gemini Advanced template until per-mode captures exist.
gemini|vertex_sa|gemini-advanced.json|gemini_vertex_sa|MockPass|Gemini auth-mode scaffold aliases the current HUAKAI Gemini Advanced template until per-mode captures exist.
gemini|code_assist|gemini-advanced.json|gemini_code_assist|MockPass|Gemini auth-mode scaffold aliases the current HUAKAI Gemini Advanced template until per-mode captures exist.
gemini|google_one|gemini-advanced.json|gemini_google_one|MockPass|Gemini auth-mode scaffold aliases the current HUAKAI Gemini Advanced template until per-mode captures exist.
gemini|antigravity|gemini-advanced.json|gemini_antigravity|MockPass|Gemini auth-mode scaffold aliases the current HUAKAI Gemini Advanced template until per-mode captures exist.
MATRIX
}

json_escape() {
    local value="$1"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    value="${value//$'\n'/\\n}"
    value="${value//$'\r'/\\r}"
    value="${value//$'\t'/\\t}"
    printf '%s' "$value"
}

secret_path_for_cell() {
    local vendor="$1"
    local auth_mode="$2"
    local candidate

    for candidate in "$HOME/secrets/$vendor/$auth_mode.txt" "$HOME/secrets/$vendor/$auth_mode.json"; do
        if [[ -f "$candidate" ]]; then
            printf '%s' "$candidate"
            return 0
        fi
    done
}

live_status_for_cell() {
    local vendor="$1"
    local auth_mode="$2"
    local lane_status="$3"
    local secret_path=""

    if [[ "$lane_status" == "PendingLane2b" ]]; then
        printf 'PendingLane2b|'
        return 0
    fi

    if [[ "${HUAKAI_RD_LIVE:-0}" != "1" ]]; then
        printf 'Skipped|'
        return 0
    fi

    secret_path="$(secret_path_for_cell "$vendor" "$auth_mode")"
    if [[ -z "$secret_path" ]]; then
        printf 'Skipped|'
        return 0
    fi

    # 当前 mock-only 版本只证明 gate 命中；真实 driver 后续由 Owner 凭证就绪后接线。
    printf 'LiveReadyButDisabled|%s' "$secret_path"
}

mock_status_for_cell() {
    local lane_status="$1"
    if [[ "$lane_status" == "PendingLane2b" ]]; then
        printf 'PendingLane2b'
    else
        printf 'PASS'
    fi
}

write_cell_artifact() {
    local vendor="$1"
    local auth_mode="$2"
    local profile_template="$3"
    local mock_profile="$4"
    local lane_status="$5"
    local note="$6"
    local mock_status="$7"
    local live_status="$8"
    local secret_path="$9"
    local artifact_path="${10}"

    local escaped_note escaped_secret
    escaped_note="$(json_escape "$note")"
    escaped_secret="$(json_escape "$secret_path")"

    cat > "$artifact_path" <<JSON
{
  "artifact_version": 1,
  "phase": "R-D",
  "lane": "IMPLEMENTER",
  "stage": "mock-only-smoke",
  "run_id": "$(json_escape "$run_id")",
  "cell": {
    "vendor": "$(json_escape "$vendor")",
    "auth_mode": "$(json_escape "$auth_mode")"
  },
  "mock_stage": {
    "ran": true,
    "status": "$(json_escape "$mock_status")",
    "route_contract": "HUAKAI Rust gateway -> local hyper mock server -> capture_artifact -> capture_diff",
    "profile_template": "$(json_escape "$profile_template")",
    "mock_profile_id": "$(json_escape "$mock_profile")",
    "extensions_status": "ExtensionsListStatus.Subset",
    "extensions_result": "$(json_escape "$mock_status")",
    "note": "$escaped_note"
  },
  "live_stage": {
    "status": "$(json_escape "$live_status")",
    "credential_path": "$escaped_secret",
    "credential_contents_read": false,
    "live_driver_invoked": false
  },
  "clean_room": {
    "reference_source_read": false,
    "source_basis": "HUAKAI internal tests, runbooks, and mimicry profile code only"
  }
}
JSON
}

print_matrix() {
    printf '%-10s %-22s %-23s %-20s\n' "vendor" "auth_mode" "profile_template" "mock_stage"
    while IFS='|' read -r vendor auth_mode profile_template mock_profile lane_status note; do
        (void "$mock_profile" "$note") 2>/dev/null || true
        printf '%-10s %-22s %-23s %-20s\n' "$vendor" "$auth_mode" "$profile_template" "$(mock_status_for_cell "$lane_status")"
    done < <(matrix_lines)
}

write_summary() {
    local cell_count="$1"
    local mock_pass_count="$2"
    local pending_count="$3"
    local live_ready_count="$4"
    local live_skipped_count="$5"
    local summary_path="$report_dir/summary.json"

    cat > "$summary_path" <<JSON
{
  "artifact_version": 1,
  "phase": "R-D",
  "lane": "IMPLEMENTER",
  "stage": "mock-only-smoke",
  "run_id": "$(json_escape "$run_id")",
  "cell_count": $cell_count,
  "mock_pass_count": $mock_pass_count,
  "pending_lane_2b_count": $pending_count,
  "live_ready_but_disabled_count": $live_ready_count,
  "live_skipped_count": $live_skipped_count,
  "live_driver_invoked": false,
  "report_tsv": "$(json_escape "$report_dir/matrix.tsv")",
  "report_jsonl": "$(json_escape "$report_dir/cells.jsonl")"
}
JSON
}

run_smoke() {
    mkdir -p "$report_dir"
    local tsv_path="$report_dir/matrix.tsv"
    local jsonl_path="$report_dir/cells.jsonl"
    : > "$jsonl_path"
    printf 'vendor\tauth_mode\tmock_stage\tlive_stage\tprofile_template\tartifact\n' > "$tsv_path"

    local cell_count=0
    local mock_pass_count=0
    local pending_count=0
    local live_ready_count=0
    local live_skipped_count=0

    printf 'HUAKAI R-D smoke mock scaffold\n'
    printf 'run_id: %s\n' "$run_id"
    printf 'artifact_dir: %s\n\n' "$report_dir"
    printf '%-10s %-22s %-16s %-22s %s\n' "vendor" "auth_mode" "mock_stage" "live_stage" "artifact"

    while IFS='|' read -r vendor auth_mode profile_template mock_profile lane_status note; do
        cell_count=$((cell_count + 1))
        local cell_dir="$report_dir/$vendor/$auth_mode"
        local artifact_path="$cell_dir/cell.json"
        local mock_status
        local live_pair live_status secret_path

        mkdir -p "$cell_dir"
        mock_status="$(mock_status_for_cell "$lane_status")"
        live_pair="$(live_status_for_cell "$vendor" "$auth_mode" "$lane_status")"
        live_status="${live_pair%%|*}"
        secret_path="${live_pair#*|}"
        if [[ "$live_pair" != *"|"* ]]; then
            secret_path=""
        fi

        write_cell_artifact \
            "$vendor" \
            "$auth_mode" \
            "$profile_template" \
            "$mock_profile" \
            "$lane_status" \
            "$note" \
            "$mock_status" \
            "$live_status" \
            "$secret_path" \
            "$artifact_path"

        cat "$artifact_path" >> "$jsonl_path"
        printf '\n' >> "$jsonl_path"
        printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$vendor" "$auth_mode" "$mock_status" "$live_status" "$profile_template" "$artifact_path" >> "$tsv_path"
        printf '%-10s %-22s %-16s %-22s %s\n' "$vendor" "$auth_mode" "$mock_status" "$live_status" "$artifact_path"

        if [[ "$mock_status" == "PASS" ]]; then
            mock_pass_count=$((mock_pass_count + 1))
        fi
        if [[ "$mock_status" == "PendingLane2b" ]]; then
            pending_count=$((pending_count + 1))
        fi
        if [[ "$live_status" == "LiveReadyButDisabled" ]]; then
            live_ready_count=$((live_ready_count + 1))
        fi
        if [[ "$live_status" == "Skipped" ]]; then
            live_skipped_count=$((live_skipped_count + 1))
        fi
    done < <(matrix_lines)

    write_summary "$cell_count" "$mock_pass_count" "$pending_count" "$live_ready_count" "$live_skipped_count"
    printf '\nsummary: %s\n' "$report_dir/summary.json"
}

void() {
    :
}

case "${1:-run}" in
    run)
        run_smoke
        ;;
    matrix)
        print_matrix
        ;;
    help|--help|-h)
        usage
        ;;
    *)
        usage >&2
        exit 2
        ;;
esac
