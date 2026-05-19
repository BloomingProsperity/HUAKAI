# 2026-05-15 Mandatory Roadmap Priority Codex Plan

| Owner directive | "你是 Mandatory Roadmap 优先级排序 reviewer + scribe lane (workspace-write)." |
| --- | --- |
| Scope | In: read HUAKAI internal docs, enumerate real `Mandatory Roadmap` rows in `docs/03_FEATURE_PARITY_MATRIX.md`, score and classify them, write `docs/process/reviews/2026-05-15-mandatory-roadmap-priority-codex.md`, append triage note and L2-A5 closed markers to the matrix status cells. Out: implementing roadmap items, touching risk register, deleting rows, changing Disposition, reading prohibited reference repos, staging/committing/pushing. |
| Success criteria | Review doc lists every real Mandatory Roadmap item with 1-5 score reasons, Top 5 launch order, three readiness buckets, five Owner decision points, and required tail metadata; matrix links the review doc and only annotates relevant Status cells with `L2-A5 closed 2026-05-15`. |
| Time estimate | 45-75 minutes wall clock; single Codex reviewer/scribe pass. |
| Blast radius | Documentation-only. Main failure risk is misclassifying priority, accidentally counting the template row, or modifying the wrong table column. |
| Failure modes | Wrong row count: verify with `rg` and table extraction. Wrong column edit: inspect line-numbered matrix before patching. Unsupported score rationale: tie every score to matrix status/local capability/risk text and nearby internal planning docs when read. Clean-room breach: do not open prohibited reference repos or source. |
| Decision points | Owner must decide exact next execution order, whether Phase 4.5 items preempt Phase 6 commercial work, whether ToS/legal-sensitive auth bootstrap can start, whether Phase 8 hardening should gate SaaS earlier, and whether L4 SaaS items remain roadmap-only. |
| Pre-execution checklist | Read `docs/RULES.md`; extract Mandatory Roadmap rows from `docs/03_FEATURE_PARITY_MATRIX.md`; exclude `TBD` template row; identify L2-A5 series rows needing status annotation; write review doc; patch matrix; verify line counts and diffs without staging. |
| Concrete execution order | 1. Create this plan. 2. Extract real roadmap rows. 3. Read only internal context needed for dependency/phase scoring. 4. Draft scoring table and triage buckets. 5. Apply matrix status/triage edits. 6. Run text checks and summarize in Chinese. |

