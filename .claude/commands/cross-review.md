---
description: Run Codex reviewer-lane against committed slice acceptance-test coverage
allowed-tools: Bash, Read, Edit, Write, Glob, Grep
---

# /cross-review — HUAKAI cross-validation protocol

Argument format: `/cross-review <slice-id> <feature-id> <spec-path>`

Examples:
- `/cross-review "Phase 4 v0.1 slice 1" F-AUTH-005 docs/specs/upstream-credential-management.md`
- `/cross-review "Phase 4 v0.1 slice 2" F-POOL-001 docs/specs/pool-routing.md`

Arguments parsed from `$ARGUMENTS`: $ARGUMENTS

## Workflow you MUST follow

1. **Parse arguments** from `$ARGUMENTS` — extract slice ID, feature ID, spec path
2. **Locate test + impl files** under `backend/internal/<feature_pkg>/`:
   - Test files: `*_test.go` in the feature's package
   - Impl files: production `.go` files
3. **Read template** at `docs/templates/codex-reviewer.md` (NEVER skip this; it encodes the cross-review protocol)
4. **Substitute placeholders** in the template with actual paths
5. **Dispatch** Codex with `codex exec --full-auto --sandbox read-only -C $REPO -` reading from the substituted template piped via stdin (`run_in_background: true`)
6. **Wait** for the background notification (do NOT poll)
7. **On completion**:
   - Trim Codex CLI noise (intermediate stream + duplicate sections); keep only the final report (find last `^# {Slice}` heading)
   - Save to `docs/reviews/{YYYY-MM-DD}-{slice-id-slug}-coverage-audit.md`
   - Add a header with reviewer / audit date / scope / verdict before the report body
8. **Report to Owner** in concise Chinese:
   - Final verdict (APPROVE / APPROVE-WITH-FIXES / REJECT)
   - Top 3 HIGH-severity findings with file:line citations
   - Whether forward progression to next slice is blocked
   - File path to the saved review

## Hard rules — NOT NEGOTIABLE

- **Never** dispatch Codex without the read-only sandbox flag (the reviewer must NOT be able to edit files)
- **Never** hand-write the prompt body; you MUST cat the template
- **Never** claim a slice is "covered" because tests pass — only the reviewer's verdict counts
- If the reviewer returns REJECT, you MUST NOT proceed to the next slice; surface the verdict to Owner and ask for direction
- If template is missing or unreadable, FAIL CLOSED — abort and tell Owner the template is broken

## Why this command exists

Owner asked: "你怎么保证每个 AI 都会阅读这个 md?" Slash commands are stronger enforcement than auto-loaded docs because the template is **physically piped into the model's context** at dispatch time — there is no "read it later" failure mode.

Begin now.
