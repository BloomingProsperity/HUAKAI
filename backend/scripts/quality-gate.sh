#!/usr/bin/env bash
# quality-gate.sh — block NEW staticcheck findings or NEW deadcode vs committed baseline.
# Ratchet: existing findings grandfathered (baseline files); only NEW issues fail CI.
# Regenerate baseline after a cleanup:  scripts/quality-gate.sh --update
set -uo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)"   # -> backend/
export GOFLAGS=-buildvcs=false
SC_BASE="scripts/staticcheck-baseline.txt"
DC_BASE="scripts/deadcode-baseline.txt"
GOBIN="$(go env GOPATH)/bin"
command -v "$GOBIN/staticcheck" >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@2025.1.1 >/dev/null 2>&1
command -v "$GOBIN/deadcode"    >/dev/null 2>&1 || go install golang.org/x/tools/cmd/deadcode@latest >/dev/null 2>&1
# normalize: strip :line:col so the baseline tolerates code movement; drop VCS/compile noise
norm_sc(){ "$GOBIN/staticcheck" ./... 2>/dev/null | grep -vE "error obtaining VCS status|buildvcs|\(compile\)$" | sed -E "s/:[0-9]+:[0-9]+:/: /" | sort -u; }
norm_dc(){ "$GOBIN/deadcode" ./... 2>/dev/null | sed -E "s/:[0-9]+:[0-9]+:/: /" | sort -u; }
if [ "${1:-}" = "--update" ]; then
  norm_sc > "$SC_BASE"; norm_dc > "$DC_BASE"
  echo "updated baselines: staticcheck=$(wc -l < "$SC_BASE") deadcode=$(wc -l < "$DC_BASE")"; exit 0
fi
fail=0
new_sc=$(comm -23 <(norm_sc) <(sort -u "$SC_BASE" 2>/dev/null))
if [ -n "$new_sc" ]; then echo "FAIL: new staticcheck findings (not in baseline):"; echo "$new_sc" | sed "s/^/  + /"; fail=1
else echo "OK staticcheck: no new findings (baseline $(wc -l < "$SC_BASE"))"; fi
new_dc=$(comm -23 <(norm_dc) <(sort -u "$DC_BASE" 2>/dev/null))
if [ -n "$new_dc" ]; then echo "FAIL: new deadcode (not in baseline):"; echo "$new_dc" | sed "s/^/  + /"; fail=1
else echo "OK deadcode: no new unreachable symbols (baseline $(wc -l < "$DC_BASE"))"; fi
[ "$fail" = 0 ] && echo "quality-gate PASS" || echo "quality-gate FAIL — fix new issues or justify+rebaseline"
exit $fail
