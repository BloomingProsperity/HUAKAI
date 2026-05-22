# 2026-05-21 juice-transparency-refcompare-codex

| Owner directive | "参照对比任务 —— HUAKAI 要做 juice 透明版(HUAKAI 自己对模型的路由/映射/替换,如实显示给用户)。开工前强制前置:对比 sub2api + CLIProxyAPI 这个模块的细节和小功能。specifier lane 调研,不写 HUAKAI 代码。" |
| Scope | In: read local latest source trees at `~/refs/sub2api-latest/` and `~/refs/CLIProxyAPI-latest/`; investigate model alias/mapping/routing/substitution behavior, caller-visible transparency, admin configuration, logs/panel traces, and overlooked small features; write a Chinese clean-room report to `docs/process/research/2026-05-21-juice-transparency-refcompare.md`. Out: HUAKAI implementation, dependency changes, schema/auth/billing/quota edits, git operations, downloading or refreshing references, vendoring reference code. |
| Success criteria | Report names the two local top-level directories as the version口径; every behavior claim about reference projects is anchored to read source or public docs with file:line citations; report clearly answers whether each project transparently discloses model mapping or hides it; report lists HUAKAI juice transparency absorb/upgrade deltas; tail includes source files read, lane, agent, UTC timestamp. |
| Time estimate | 60-120 minutes wall clock; one Codex specifier-lane research pass. |
| Blast radius | Low. Mutations are limited to this plan document and the requested research document. The reference trees are read-only inputs for this task. |
| Failure modes | Relevant behavior is split across many modules: mitigate with broad `rg` keyword sweeps before focused reads. A source path uses protocol-level names that resemble upstream identifiers: only cite file:line and paraphrase behavior in HUAKAI vocabulary. A feature is absent: document search evidence and mark as not observed rather than asserting impossible global absence. Admin UI/log behavior is implemented outside obvious backend paths: search frontend/docs/API routes before concluding. |
| Decision points | Owner confirmation is required only if the task expands into HUAKAI code, high-risk files, deleting files, downloading/fetching refs, or changing schema/auth/billing/quota. No such expansion is planned. |
| Pre-execution checklist | 1. Confirm local reference directories exist and record their top-level names. 2. Search both trees for model, mapping, alias, fallback, substitution, router/provider, AMP rewrite, admin config, and logs/usage paths. 3. Read only relevant source regions before making claims. 4. Keep the output behavior-only and Chinese. 5. Verify required report sections and clean-room tail. |

## Concrete Execution Order

1. Inspect `~/refs/sub2api-latest/` and `~/refs/CLIProxyAPI-latest/` root metadata to establish version口径 without network access.
2. Run focused `rg` searches for model mapping, aliasing, fallback/substitution, response model rewriting, admin configuration, logs, usage, and UI exposure.
3. Read backend routing/config/translator files first, then admin/frontend/log paths if searches indicate coverage.
4. Record evidence as observed behavior, inferred behavior, or open question; avoid speculation.
5. Draft the requested report with the six required sections and a comparison table.
6. Verify no upstream code blocks, comments, distinctive structures, or copied implementation details entered the report.

## Clean-Room Self-Guard

Lane: specifier. sub2api is treated as non-MIT license risk for this task; CLIProxyAPI is MIT, but the same paraphrase discipline applies. Source may be read, but HUAKAI artifacts may contain only behavior summaries, citations, risks, and feature ideas. No code, comments, copied internal structures, or line-by-line algorithmic translations may flow into the report.
