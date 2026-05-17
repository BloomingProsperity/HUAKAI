#!/usr/bin/env bash
# Fast test entry — 默认 PR / CI 跑这个。
#
# 跑 backend 全部不依赖外部资源的 Go 测试：
#   - 不带 build tag → integration_pg / smoke 文件不会被编译进来
#   - 自动用 $HOME/.cache/go-build 作 GOCACHE，避免 /tmp 配额爆
#   - 默认带 -race，timeout 180s
#
# 用法：
#   scripts/run-tests.sh
#   scripts/run-tests.sh ./internal/rate/...    # 跑某个子目录
#
# 完整集成测试 (含真 PG)：scripts/run-integration-tests.sh
# 文档：docs/dev-tests.md
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}/backend"

export GOCACHE="${GOCACHE:-${HOME}/.cache/go-build}"
mkdir -p "${GOCACHE}"

# 默认 target 是 ./... ；命令行传参覆盖。
TARGET=("$@")
if [[ ${#TARGET[@]} -eq 0 ]]; then
  TARGET=("./...")
fi

echo "[run-tests] GOCACHE=${GOCACHE}"
echo "[run-tests] go test -race -count=1 -timeout 180s ${TARGET[*]}"
exec go test -race -count=1 -timeout 180s "${TARGET[@]}"
