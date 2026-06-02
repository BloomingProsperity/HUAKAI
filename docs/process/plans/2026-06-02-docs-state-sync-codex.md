# 2026-06-02 docs-state-sync-codex

| Owner directive | "HUAKAI 文档过时状态标记同步任务。IMPLEMENTER lane...只改 docs 状态/标记类文字,不改任何代码/schema/spec 行为...自主:改→commit→push。" |
| Scope | In: read HUAKAI internal code and decision/planning docs, grep outdated status markers in `docs/` and `exploratory/`, append `【2026-06-02 已更新】...;以下为历史` notes, preserve old text. Out: code changes, schema changes, spec behavior changes, external reference source reading, deleting old historical text. |
| Success criteria | Targeted stale markers are either updated with source-verified 2026-06-02 notes or left untouched only when no conflict with the verified truth is found; git diff contains docs/status text only; review/check commands run; commit and push to `origin HEAD:work/docs-state-sync`. |
| Time estimate | Wall clock 1-2 hours; agent time one implementation pass plus review. |
| Blast radius | Low: stale-status documentation can mislead AI/Owner; incorrect updates could create new false state. No runtime blast radius because code/schema/spec behavior are out of scope. |
| Failure modes | Over-updating historical context; mitigation: preserve original text and add timestamped notes only. Unsupported truth claim; mitigation: read the cited code/decision region before editing. Clean-room leakage; mitigation: read only HUAKAI internal code/docs, no external reference source. |
| Decision points | Stop for Owner only if a required update would touch code, schema, high-risk files, delete content, or alter spec behavior. |
| Pre-execution checklist | 1. Confirm branch/worktree. 2. Read project rules. 3. Read decision docs for data-plane direction and frontend/P0 state. 4. Read internal code for tls-sidecar/Go mimicry/Go 7-hole truth. 5. Grep requested stale markers. 6. Apply timestamped notes only. 7. Review diff and run status-marker grep. 8. Stage, run Codex review, commit, push. |
