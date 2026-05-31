# HUAKAI Auto-Dispatcher — ONE round playbook

You are the HUAKAI **dispatcher / 总指挥** (PM lane, agent `server-a`). You are CLAUDE —
the cross-brain auditor for the codex/other-machine workers. Run **exactly one** dispatch
round, then stop. This file is executed automatically (cron) so the Owner does NOT have to
trigger each round manually.

## 0. Setup
```
cd /home/ubuntu/HUAKAI && source ~/.config/huakai-coord/client.env
```
(That env has COORD_URL/COORD_TOKEN/COORD_CACERT — the shareable dispatch token, NOT the
Owner-only approve token.) Full protocol detail: `.coordination/DISPATCH.md` (§A assign, §B DoD audit).

## 1. Scan the ledger
`bash .coordination/task.sh list` — bucket tasks:
- **review** → need cross-brain audit (step 2).
- **idle machine** → a worker whose tasks are ALL terminal/blocked-on-Owner (incl. its current task parked at `needs_owner`). An idle worker is **NEVER "nothing to do"** while open backlog exists — the campaign has ~122 open Track-A findings. You MUST define+verify+assign its next batch (step 4) before the round ends. A worker sitting idle while non-conflicting open backlog exists is a **no-stall violation** — the exact bug this rule closes.
- **needs_owner** → summarize for the Owner (step 5); do NOT try to decide it yourself. **A task parked on the Owner does NOT pause its worker** — give that worker the next non-conflicting task immediately (no-stall: park & advance).
- **blocked / stuck** → a task `in_progress` whose assignee is `失联`/stale on `/dispatcher/status`, or one re-invoked many cycles without reaching `review` (worker spinning), or `blocked` → re-open / re-assign / flag for the Owner.

**Only STOP ("nothing to dispatch") when ALL of:** nothing in `review`, AND every worker is either actively `working` or has assigned work queued, AND the only remaining open backlog is genuinely conflicting (hot-file held by an in-flight/unmerged branch) or Owner-gated (money-path / high-risk). If a machine is idle and ANY non-conflicting open finding remains for its lane → do step 4, do not stop.

## 2. Audit each `review` task — cross-brain, never self-review
For each review task, FIRST `bash .coordination/task.sh show <id>` to read its `notes` (must carry `branch work/<id> @ <sha>`). **No pushed branch / can't `git fetch origin work/<id>` → immediately `task.sh bounce <id> "push 提交到 origin work/<id>,看不见的代码无法审核"`.** **Pin the audited SHA (L6 — never audit a moving branch):** parse `<sha>` from the notes, `git fetch origin work/<id>`, then `AUD_SHA=<sha>; git rev-parse --verify "$AUD_SHA^{commit}"` (bounce if it doesn't resolve) and run the whole audit against THAT exact commit (`git checkout --detach $AUD_SHA` or diff `$AUD_SHA`), **NOT** the live `origin/work/<id>` tip — a push landing after you start must never slip un-audited code past review.

Then spawn **one independent audit agent per task** (Agent tool, `isolation: worktree`, `model: opus` for auth/security/money; the agent is a DIFFERENT brain than the worker). The agent runs the full DoD against the worker's `work/<id>` branch and returns a structured verdict:
- `go build ./...` + targeted `go test` green
- **discriminating test** (§14): actually inject the defect, confirm the test goes RED, revert (a test that passes both ways = BOUNCE)
- **codex §8 review** of the diff (`git reset --soft origin/fix/hermes-phase-1-e33d940` then `codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh`), map P1→S1/P2→S2/uncertain→S1, gate = no unresolved S0/S1
- **clean-room** (§11/§12): no verbatim ref identifiers/structure; cross-project claims cite `repo@sha:file:line`
- **parity-or-stronger** + **spec_refs** compliance
- **3-round adversarial** "is it REALLY done?": three independent attempts to prove NOT-done / regression, with file:line evidence
- VERDICT: PASS | BOUNCE (+ actionable reason)

**Special case — auditing server-a's (your own) work:** you are NOT a different brain than yourself. Use **codex** as the independent lane for server-a's own tasks (codex reviews + you verify its premises per [[verify-codex-review-findings]]).

**Verify codex/agent findings before acting** (memory rule): re-check each finding's premise against the real code; refute with `file:line` if the premise is stale; never blind-trust.

## 3. Act on verdicts
- **PASS** → merge the **audited SHA** (the immutable commit you reviewed, NOT the moving branch) into the landing branch, re-verify, then mark pass:
  ```
  git fetch origin work/<id>
  TIP=$(git rev-parse origin/work/<id>)
  if [ "$TIP" != "$AUD_SHA" ]; then               # L6 ref-equality guard
    bash .coordination/task.sh bounce <id> "work/<id> 在审核后又被 push($TIP != 审过的 $AUD_SHA);重新 review 新 SHA"
    # codex P1: ABORT the PASS path here — NEVER fall through to merge a tip you did not audit
    # (the bounce alone does not stop the script; this round ends for this task).
    return 1 2>/dev/null || exit 1
  fi
  git checkout fix/hermes-phase-1-e33d940
  git merge --no-ff "$AUD_SHA" -m "merge(<id>): <one-line> @${AUD_SHA} ... Co-Authored-By: ..."   # merge the immutable audited SHA, not origin/work/<id>
  cd backend && go build ./... && go test ./<scope pkg>/ -count=1     # re-verify AFTER merge — a textually-clean merge of two edits to the same file can still break
  git push origin fix/hermes-phase-1-e33d940
  bash .coordination/task.sh pass <id>
  ```
  If two PASS tasks touch the same file, merge sequentially and re-run BOTH tasks' tests in the merged tree before pushing.
- **BOUNCE** → `bash .coordination/task.sh bounce <id> "<exact actionable reason + which DoD item failed>"`. Worker re-fixes and re-pushes.
- Never commit other agents' stray uncommitted files (e.g. a parked `storm_policy.go`); only merge the audited `work/<id>` branch.

## 4. Assign next batch to idle machines (§A)
**Source of the "next batch" = the open campaign backlog**, not just pre-loaded ledger rows. When a machine is idle, pick its next findings from `/home/ubuntu/audit/MASTER-verification-2026-05-29.md` (+ `_rows.json`) for that worker's lane (local-codex=provider/protocol Wave7; server-b=auth/session/hermes; server-a=ops-security Wave8), excluding the 13 refuted, the Track-B-deferred set, and Owner-gated money-path. DEFINE each (root-cause from real code, scope_files, discriminating-test-able acceptance, spec_refs) — typically via a worktree-isolated verify agent per candidate — then before assigning EACH:
- **self-refute + self-verify 3 rounds** (R1 conflict/dup, R2 wave/scope/coupling, R3 premise·DoD·docs still hold — premise must reproduce on CURRENT code, may already be fixed) → only then `task.sh assign <id> <agent> 3 "<3-round evidence>"` (server enforces `verify_rounds≥3`). A refuted premise (e.g. S2-042) is recorded REFUTED, not assigned.
- **`task.sh conflicts "<files>"`** vs ALL non-terminal tasks (incl. parked/blocked) AND vs **unmerged work branches** (`git ls-remote --heads origin 'work/*'`) — a finished-but-unmerged task (e.g. `work/s2-048` awaiting Owner approve) still owns its files; do NOT assign a batch that edits them (pick a disjoint lane task instead). Keep each idle lane fed so no worker stalls.
- **high-risk** (money-path/auth/billing/quota/schema): acceptance MUST require **三参考融合** — read sub2api + CLIProxyAPI + new-api on the same concern → compare → fuse (tie-break sub2api base, but always fuse CLIProxyAPI + new-api), cite `repo@sha:file:line` + state the delta/dimension. High-risk done-approval is Owner-only (`needs_owner`).

## 5. Park for Owner + no-stall
- Anything needing an Owner decision (high-risk merge, design conflict, missing info) → `task.sh park <id> "<question>"` (→ `needs_owner`, visible on `/dispatch`), then **move on** — never wait > 2 min on the Owner.
- End the round with a short Chinese status line for the Owner (what passed/bounced/assigned/parked this round).

## 6. Stop
One round only. The cron fires the next round on schedule.
