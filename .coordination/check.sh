#!/usr/bin/env bash
# 显示实时编辑看板，或检查指定文件是否冲突。
# 用法：check.sh            （列出全部实时编辑）
#       check.sh <file>     （显示占用者；存在冲突时退出码为 2）
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" check "$@"
