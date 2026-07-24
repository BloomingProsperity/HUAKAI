#!/usr/bin/env bash
# 质量棘轮：阻止新增 staticcheck 问题和新增真死代码。
# deadcode 同时分析默认和带标签的测试形态，只保留所有形态都不可达的交集。
# 基线只能下降；本地完成真实清理后可显式执行：
# HUAKAI_ALLOW_BASELINE_REWRITE=1 scripts/quality-gate.sh --update
set -uo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)" # -> backend/
export GOFLAGS=-buildvcs=false
SC_BASE="scripts/staticcheck-baseline.txt"
DC_BASE="scripts/deadcode-baseline.txt"
# 基线行数硬上限只能随清债调低；调高必须在变更中显式说明原因。
SC_MAX=0
DC_MAX=0
GOBIN="$(go env GOPATH)/bin"
command -v "$GOBIN/staticcheck" >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@2025.1.1 >/dev/null 2>&1
command -v "$GOBIN/deadcode" >/dev/null 2>&1 || go install golang.org/x/tools/cmd/deadcode@v0.47.0 >/dev/null 2>&1

# 常规集成、调试与真实上游测试可能使用不同的测试支架。
# 生图与视频的构建约束互斥，所以额外保留纯视频形态。
DC_ALL_TAGS="integration_pg,integration_redis,debug,smoke,e2e_concurrency,e2e_upstream,e2e_chatgpt_session,e2e_codex_live,e2e_gemini_video_live,e2e_grok_live,e2e_openai_image_live,e2e_grok_image_live,e2e_grok_video_live,live_upstream"
DC_VIDEO_TAGS="e2e_grok_video_live,e2e_gemini_video_live"
QUALITY_CACHE_ROOT="${HUAKAI_QUALITY_GATE_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/huakai-quality-gate}"
mkdir -p "$QUALITY_CACHE_ROOT"
QUALITY_WORKDIR="$(mktemp -d "$QUALITY_CACHE_ROOT/run.XXXXXX")"
trap 'rm -rf "$QUALITY_WORKDIR"' EXIT

# normalize: strip :line:col so the baseline tolerates code movement; drop VCS/compile noise
norm_sc() { "$GOBIN/staticcheck" ./... 2>/dev/null | grep -vE "error obtaining VCS status|buildvcs|\(compile\)$" | sed -E "s/:[0-9]+:[0-9]+:/: /" | sort -u; }
run_dc_profile() {
  local name="$1"
  shift
  local raw="$QUALITY_WORKDIR/$name.raw"
  local normalized="$QUALITY_WORKDIR/$name.normalized"
  local errors="$QUALITY_WORKDIR/$name.stderr"
  # deadcode 找到不可达符号时以非零退出码报告诊断；这不是分析失败。
  # 只有 stderr 有真实报错时才中止，随后仍要把三种构建形态的诊断求交集。
  if ! "$GOBIN/deadcode" -test "$@" ./... >"$raw" 2>"$errors" && [ -s "$errors" ]; then
    echo "FAIL: deadcode 形态 $name 编译失败：" >&2
    sed 's/^/  /' "$errors" >&2
    return 1
  fi
  sed -E "s/:[0-9]+:[0-9]+:/: /" "$raw" | sort -u >"$normalized"
}
norm_dc() {
  run_dc_profile default || return 1
  run_dc_profile all-tags -tags="$DC_ALL_TAGS" || return 1
  run_dc_profile video-tags -tags="$DC_VIDEO_TAGS" || return 1
  comm -12 \
    <(comm -12 "$QUALITY_WORKDIR/default.normalized" "$QUALITY_WORKDIR/all-tags.normalized" | sort -u) \
    "$QUALITY_WORKDIR/video-tags.normalized"
}

if ! norm_dc >"$QUALITY_WORKDIR/deadcode-current.txt"; then
  exit 2
fi

if [ "${1:-}" = "--update" ]; then
  # CI 中一律拒绝重写 baseline(GitHub Actions 自动设 CI=true),杜绝在流水线里洗债。
  if [ -n "${CI:-}" ]; then
    echo "REFUSED: 禁止在 CI 中用 --update 重写 baseline(会把债务洗白)。请本地修复后再提交。"
    exit 2
  fi
  # 本地也要显式 opt-in,避免随手 --update 把债务祖父化。
  if [ "${HUAKAI_ALLOW_BASELINE_REWRITE:-}" != "1" ]; then
    echo "REFUSED: 重写 baseline 需显式 HUAKAI_ALLOW_BASELINE_REWRITE=1(确保是清债后的有意为之)。"
    exit 2
  fi
  norm_sc >"$SC_BASE"
  cp "$QUALITY_WORKDIR/deadcode-current.txt" "$DC_BASE"
  sc_n=$(wc -l <"$SC_BASE")
  dc_n=$(wc -l <"$DC_BASE")
  echo "updated baselines: staticcheck=$sc_n deadcode=$dc_n"
  if [ "$sc_n" -gt "$SC_MAX" ] || [ "$dc_n" -gt "$DC_MAX" ]; then
    echo "WARNING: 重写后 baseline 超过上限(SC_MAX=$SC_MAX DC_MAX=$DC_MAX),正常 gate 会失败。"
    echo "         请改为清债;若确需放宽,显式调高脚本中 SC_MAX/DC_MAX 并在 DEFERRED 文档记录理由。"
  fi
  exit 0
fi

fail=0

# 上限闸:baseline 行数不得超过硬上限(只能降)。膨胀(洗债)立即拦下。
sc_n=$(wc -l <"$SC_BASE" 2>/dev/null || echo 0)
dc_n=$(wc -l <"$DC_BASE" 2>/dev/null || echo 0)
if [ "$sc_n" -gt "$SC_MAX" ]; then
  echo "FAIL: staticcheck baseline 膨胀($sc_n > 上限 $SC_MAX)。baseline 只能下降;修复新增 finding,勿洗进基线。"
  fail=1
else echo "OK staticcheck baseline 行数 $sc_n ≤ 上限 $SC_MAX"; fi
if [ "$dc_n" -gt "$DC_MAX" ]; then
  echo "FAIL: deadcode baseline 膨胀($dc_n > 上限 $DC_MAX)。baseline 只能下降;接线/删除死代码,勿洗进基线。"
  fail=1
else echo "OK deadcode baseline 行数 $dc_n ≤ 上限 $DC_MAX"; fi

new_sc=$(comm -23 <(norm_sc) <(sort -u "$SC_BASE" 2>/dev/null))
if [ -n "$new_sc" ]; then
  echo "FAIL: new staticcheck findings (not in baseline):"
  echo "$new_sc" | sed "s/^/  + /"
  fail=1
else echo "OK staticcheck: no new findings (baseline $sc_n)"; fi
new_dc=$(comm -23 "$QUALITY_WORKDIR/deadcode-current.txt" <(sort -u "$DC_BASE" 2>/dev/null))
if [ -n "$new_dc" ]; then
  echo "FAIL: new deadcode (not in baseline):"
  echo "$new_dc" | sed "s/^/  + /"
  fail=1
else echo "OK deadcode: no new unreachable symbols (baseline $dc_n)"; fi
[ "$fail" = 0 ] && echo "quality-gate PASS" || echo "quality-gate FAIL — fix new issues or justify+rebaseline"
exit $fail
