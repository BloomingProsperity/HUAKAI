# 2026-05-14 ref borrow gap matrix

| Owner directive | "HUAKAI ref 项目借鉴 vs 缺失 gap analysis 总表 — 决策支持 doc。" |
| --- | --- |
| Scope | In: read committed HUAKAI research docs, feature parity matrices, phase plan, and last 30 commits; produce `docs/research/2026-05-13-ref-borrow-gap-matrix-codex.md` plus `/tmp/codex-gap-analysis-final.txt`. Out: no ref repo clone, no external dependency, no implementation change, no schema/auth/billing/quota mutation. |
| Success criteria | Output doc is Chinese, <=1200 lines, has 7 requested sections, cites file:line evidence for each gap/claim class, ranks P0/P1 gaps, includes Owner decision points, and records source files cited. |
| Time estimate | 60-90 minutes wall clock; one Codex session. |
| Blast radius | Documentation-only. Main risk is misleading Owner decisions if evidence is weak, stale, or over-inferred. |
| Failure modes | Missing evidence: mark as open/weak and avoid claiming. Clean-room leakage: cite internal research summaries and avoid copying upstream code or identifiers beyond already committed citation anchors. Over-counting features: label numerator/denominator as self-assessed based on available docs. Cross-discussion gap: current session has no Claude lane; record this limitation and continue because Owner explicitly said not to ask. |
| Decision points | Owner may need to decide: whether to expand provider breadth before enterprise observability, whether trust-chain becomes product differentiator, whether subscription-pool refresh goes P0, whether admin UI breadth ships before automation, and whether any Drop-like items are truly allowed under Feature Preservation Rule. |
| Pre-execution checklist | 1. Confirm start signal and read rules. 2. Create `/tmp/codex-gap-analysis.txt` stub. 3. Read six dir-skeleton docs, trust-chain survey, parity/level/phase docs, and recent commits. 4. Extract capabilities and source line anchors. 5. Draft output incrementally. 6. Validate line count and required sections. 7. Write final temp summary. |
| Concrete execution order | Read high-signal lines using `rg`/`nl`; build capability matrix; identify P0 completely missing gaps; identify P1 partial gaps; list strategic non-build/safe-equivalent items; write the doc; append progress to `/tmp/codex-gap-analysis.txt`; run `wc -l` and citation checks. |

## Cross-Discussion Note

AGENTS.md asks for independent Claude/Codex plans and a synthesized plan before non-trivial work. This session only exposes Codex, while the Owner request says "Claude 仅做决策，所有 doc 由 codex 写" and "不要问 Owner，按你判断走." I am recording the missing Claude parallel plan as a process limitation and proceeding with documentation-only work.
