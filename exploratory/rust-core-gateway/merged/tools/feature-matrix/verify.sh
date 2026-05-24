#!/usr/bin/env bash
#
# P1-6 (W12-F feature-matrix CI) 2026-05-24:
# 验证 core_gateway crate 在所有 feature 组合下都能编译 + 通过测试。
#
# 背景: HUAKAI 默认 build 不含 mimicry-boring / mimicry-openssl / mimicry-http2-fork,
# 但生产 dispatch + L1/L2 守门测试只在对应 feature 编入时生效。CI 必须分别跑这几个
# feature 才能保证 byte-level mimicry 路径没被静默破坏 — 否则 default build 通过 +
# mimicry feature 上线时炸。
#
# 退出码: 任一 cargo invocation 失败即非 0, 让 CI 红。
#
# 用法:
#   bash tools/feature-matrix/verify.sh           # 跑完整 matrix
#   bash tools/feature-matrix/verify.sh quick     # 只跑 default + 一个代表 feature
#
# 环境:
#   CARGO_TARGET_DIR  可覆盖共享 target 目录加速 incremental compile
#

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
merged_root="$(cd "$script_dir/../.." && pwd)"

# Required cargo test invocations (mutation marker: 删任一条 -> feature-matrix CI 漏覆盖某个 feature)。
# 必须包含这 4 个:
#   1) default build (无 feature)
#   2) mimicry-boring (生产 Boring TLS 路径)
#   3) mimicry-openssl (OpenSSL exact adapter 路径)
#   4) mimicry-http2-fork (L2-A6 SETTINGS / pseudo-header 顺序路径)
declare -a MATRIX=(
  "default::"
  "mimicry-boring:--features mimicry-boring"
  "mimicry-openssl:--features mimicry-openssl"
  "mimicry-http2-fork:--features mimicry-http2-fork"
)

mode="${1:-full}"
if [[ "$mode" == "quick" ]]; then
  # Smoke 仅跑 default + boring (R-E-A+1 主路径), 用于 PR 预审快速绿
  MATRIX=("default::" "mimicry-boring:--features mimicry-boring")
fi

cd "$merged_root"

printf 'HUAKAI feature-matrix verification (P1-6)\n'
printf 'merged_root: %s\n' "$merged_root"
printf 'mode: %s\n\n' "$mode"

failures=()

for entry in "${MATRIX[@]}"; do
  label="${entry%%:*}"
  features="${entry#*:}"
  features="${features#:}"  # strip leading colon when no features

  printf '────────────────────────────────────────────────────\n'
  printf '[feature-matrix] %s\n' "$label"
  printf '  cargo test -p core_gateway %s\n' "${features:-(default)}"
  printf '────────────────────────────────────────────────────\n'

  if cargo test -p core_gateway ${features:-} --tests --lib 2>&1; then
    printf '[feature-matrix] %s: PASS\n\n' "$label"
  else
    printf '[feature-matrix] %s: FAIL\n\n' "$label"
    failures+=("$label")
  fi
done

printf '═══════════════════════════════════════════════════\n'
if [[ ${#failures[@]} -eq 0 ]]; then
  printf '[feature-matrix] all %d combinations PASS\n' "${#MATRIX[@]}"
  exit 0
else
  printf '[feature-matrix] %d failure(s): %s\n' "${#failures[@]}" "${failures[*]}"
  exit 1
fi
