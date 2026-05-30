#!/usr/bin/env bash
# Claim/refresh an edit lock + broadcast intent. Runs a conflict check first.
# Usage: claim.sh "<agent>" "<file1,file2,...>" "<core_feature>" "<purpose>"
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$DIR/_coord.py" claim "$@"
