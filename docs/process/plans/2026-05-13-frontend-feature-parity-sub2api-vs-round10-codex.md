# 2026-05-13 frontend feature parity sub2api vs Round 10

| Owner directive | Owner 2026-05-13 quote: "sub2api里面的功能，显示都不能少"; user requested a Codex research doc comparing sub2api Dashboard display items against HUAKAI Round 10 dashboard. |
| Scope | In: source-backed inventory of sub2api dashboard/sidebar/header display items, Round 10 dashboard inventory, gap table, HUAKAI semantic mappings, Round 11 implementation checklist. Out: UI color study, backend changes, mock schema edits, implementation code. |
| Success criteria | `docs/research/2026-05-13-frontend-feature-parity-sub2api-vs-round10-codex.md` exists; every sub2api claim cites a file/line from the prior decomposition or direct source read; Round 10 claims cite HUAKAI frontend files; gaps are classified and prioritized. |
| Time estimate | 45-75 minutes wall clock; one Codex lane. |
| Blast radius | Low: documentation-only output plus required `/tmp` stub and this plan artifact. |
| Failure modes | Missing sub2api item due to incomplete source read: mitigate by reading §7-§8/§10 and targeted component files. Clean-room leakage: mitigate by paraphrasing behavior only and avoiding source snippets. Overclaiming HUAKAI state: mitigate by citing concrete frontend lines. |
| Decision points | None for execution; Owner confirmation needed later before implementing P0/P1 UI or changing mock/backend schemas. |
| Pre-execution checklist | 1. Stub file written. 2. Read relevant skill rules. 3. Read prior sub2api decomposition sections. 4. Optionally inspect current sub2api dashboard component source for missing details. 5. Read Round 10 frontend files. 6. Draft cited parity doc. |

Concrete execution order:

1. Gather cited sub2api dashboard evidence from the existing decomposition.
2. Validate or supplement with direct source reads from the local sub2api clone if present.
3. Gather HUAKAI Round 10 evidence from dashboard page/components/sidebar.
4. Build exhaustive inventories, then classify gaps and Round 11 priorities.
5. Write the research doc in Chinese and run lightweight sanity checks.
