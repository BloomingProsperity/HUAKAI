#!/usr/bin/env bash
# 声明或刷新编辑锁，并广播编辑意图；命令会先检查冲突。
# 用法：claim.sh "<agent>" "<file1,file2,...>" "<core_feature>" "<purpose>"
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" claim "$@"
