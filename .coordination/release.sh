#!/usr/bin/env bash
# 完成编辑后释放当前执行者的文件锁。
# 用法：release.sh "<agent>"
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" release "$@"
