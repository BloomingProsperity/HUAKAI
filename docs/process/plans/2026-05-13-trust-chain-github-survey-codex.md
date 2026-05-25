# 2026-05-13 trust-chain GitHub survey codex

| Owner directive | "HUAKAI 信任链 GitHub 调研。HUAKAI 是 user-verifiable AI gateway。... 产出 docs/research/2026-05-13-trust-chain-github-survey-codex.md，含 5 节..." |
| Scope | In: WebSearch + GitHub source read for AI gateway / LLM proxy, API gateway / service mesh, transparency log, and vendor-adjacent observability projects. Out: HUAKAI implementation changes, schema changes, dependency changes, LICENSE changes. |
| Success criteria | Produce the requested Chinese research report with five sections: method, discovered project table, HUAKAI six-requirement mapping, upgrade recommendations, and required file:line reads. Every non-HUAKAI project behavior claim is tied to source file:line evidence or marked absent / open. |
| Time estimate | 45-90 minutes wall clock, depending on clone availability and source search hit quality. |
| Blast radius | Low: research docs and `/tmp` progress file only. Main risk is overclaiming from README or copying upstream identifiers. |
| Failure modes | Search results are noisy; mitigate by using source search and file:line citations. Repos may be too large; mitigate by reading only narrow files around redaction, audit, signing, token, cache, and transparency paths. License contamination; mitigate by behavior-only paraphrase and no code blocks from refs. |
| Decision points | No Owner sign-off needed unless the task would require high-risk repo edits or copying restricted upstream material, neither of which is planned. |
| Pre-execution checklist | 1. Write `/tmp/codex-trust-search.txt` stub. 2. Use WebSearch for candidate discovery. 3. Clone or inspect only public source needed for behavior claims. 4. Capture commit SHA and line evidence. 5. Write report and append section completion markers to `/tmp/codex-trust-search.txt`. |

Concrete execution order:

1. Discover candidate GitHub projects with web search across the requested categories.
2. For each promising candidate, read targeted source regions for redaction, signing, audit ledger, token accounting, cache integrity, and model routing / response metadata behavior.
3. Map observed / absent / weak evidence against the six HUAKAI trust-chain requirements.
4. Draft the Chinese report with clean-room paraphrase, source citations, and required tail metadata.
5. Verify the report exists and append final progress status.
