# 2026-05-15 R-D Smoke Scaffold Codex Plan

| Owner directive | Build HUAKAI R-D 烟雾测试 scaffolding — mock-only stage; live upstream waits for Owner credentials. |
| --- | --- |
| Scope | In: mock-only R-D smoke scaffold, 15-cell vendor/auth-mode matrix, live credential gate documentation, runbook, tool README, executable script. Out: live upstream calls, real credentials, reference-project source reading, production dispatch wiring. |
| Success criteria | `tools/r-d-smoke/run.sh` produces a deterministic mock report for all 15 cells, Anthropic cells are marked `PendingLane2b`, live cells are silently skipped unless `HUAKAI_RD_LIVE=1` and the matching secret file exists, and docs explain the operator path. |
| Time estimate | 45-75 minutes wall clock; one Codex implementation pass plus shell verification. |
| Blast radius | Low/medium. New docs and a new tool directory only; no backend core, auth, billing, quota, schema, deployment, or `LICENSE` changes. |
| Failure modes | Script may overclaim real gateway coverage; mitigate by labeling it mock-only and artifact-backed scaffold. Matrix may drift from Owner credential layout; mitigate by one-line `credential_path`/live hook location. Bash portability issues; keep POSIX-ish Bash and verify with `bash -n` plus a dry run. |
| Decision points | Owner must later confirm live credentials and real upstream execution. Owner must separately release Anthropic Lane 2b before those five cells can become runnable. |
| Pre-execution checklist | Read AGENTS/RULES constraints; read existing capture artifact/diff helpers; read recapture runbook; read mimicry profile/backend files; confirm no reference source is read; create only low-risk docs/tool files; run syntax/dry-run checks; report git status. |
| Concrete execution order | 1. Create `exploratory/rust-core-gateway/merged/tools/r-d-smoke/`. 2. Add `run.sh` with matrix, mock artifact writer, and live gate placeholders. 3. Add tool README. 4. Add `docs/runbooks/r-d-smoke-runbook.md`. 5. Verify with `bash -n` and mock dry run using `< /dev/null`. 6. Report changed files, matrix, gate behavior, sources read. |

## Clean-Room Lane

- Lane: IMPLEMENTER
- Reference source read: none
- Allowed evidence: HUAKAI internal Rust/test/docs files only
- License posture: safe equivalent scaffold; no copied non-MIT source, schemas, comments, file layout, or implementation details

