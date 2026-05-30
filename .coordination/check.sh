#!/usr/bin/env bash
# Show the live-edit board, or check one file for conflicts.
# Usage: check.sh            (list all live edits)
#        check.sh <file>     (report who is editing <file>; exit 2 if any)
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" check "$@"
