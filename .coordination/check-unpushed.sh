#!/usr/bin/env bash
# Run ON server-b (inside the HUAKAI repo) BEFORE decommissioning, to surface any
# work that exists only locally and is NOT on GitHub. READ-ONLY — mutates nothing.
#   Usage:  git -C <repo> fetch origin && bash .coordination/check-unpushed.sh
#   Or without a clean checkout:
#     git fetch origin && git show origin/fix/hermes-phase-1-e33d940:.coordination/check-unpushed.sh | bash
set -uo pipefail
REPO="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$REPO"
git fetch origin --quiet 2>/dev/null || echo "(fetch failed — offline? results still valid for local state)"

hit=0
echo "============================================================"
echo "REPO: $REPO"
echo "============================================================"

echo; echo "### 1) Uncommitted changes in the MAIN worktree"
if [ -n "$(git status --porcelain)" ]; then git status --short; hit=1; else echo "  clean"; fi

echo; echo "### 2) Stashes (often where in-progress work hides)"
if [ -n "$(git stash list)" ]; then git stash list; hit=1; else echo "  none"; fi

echo; echo "### 3) Local branches with commits NOT on ANY remote (unpushed)"
for b in $(git for-each-ref --format='%(refname:short)' refs/heads 2>/dev/null); do
  n=$(git rev-list --count --not --remotes "$b" 2>/dev/null || echo 0)
  if [ "${n:-0}" -gt 0 ]; then
    echo "  ⚠️  $b : $n unpushed commit(s)"
    git log --oneline --not --remotes "$b" 2>/dev/null | sed 's/^/        /' | head -10
    hit=1
  fi
done
[ "$hit" -eq 0 ] && echo "  (none — or covered below)"

echo; echo "### 4) ALL git worktrees — each checked for uncommitted changes"
git worktree list 2>/dev/null
git worktree list --porcelain 2>/dev/null | awk '/^worktree /{print $2}' | while read -r wt; do
  [ -d "$wt" ] || continue
  d=$(git -C "$wt" status --porcelain 2>/dev/null | wc -l | tr -d ' ')
  if [ "${d:-0}" -gt 0 ]; then echo "  ⚠️  WORKTREE $wt has $d uncommitted change(s):"; git -C "$wt" status --short 2>/dev/null | sed 's/^/        /' | head -20; fi
done

echo; echo "### 5) Specifically hunt for S2-077 (user self-key audit table) work"
git log --all --oneline 2>/dev/null | grep -iE 's2.?077|self.?key.?audit|user_self_key' | sed 's/^/  commit: /' | head
find . -path ./.git -prune -o \( -name '*user_self_key*' -o -name '*0061_user_self*' -o -name '*self_key_audit*' \) -print 2>/dev/null | sed 's/^/  file:   /' | head
grep -rniE 'user_self_key_audit|self_key_audit_events' --include='*.sql' --include='*.go' . 2>/dev/null | grep -v '/.git/' | sed 's/^/  ref:    /' | head

echo; echo "============================================================"
echo "DONE. Anything under ⚠️ above is LOCAL-ONLY and NOT on GitHub."
echo "To save a branch:   git push origin <branch-name>"
echo "To save loose edits: commit to a branch then push, or git stash + report."
echo "If everything says clean/none, server-b has nothing unpushed — safe to stop."
echo "============================================================"
