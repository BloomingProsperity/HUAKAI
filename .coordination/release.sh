#!/usr/bin/env bash
# Release your edit lock (call when you finish editing).
# Usage: release.sh "<agent>"
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" release "$@"
