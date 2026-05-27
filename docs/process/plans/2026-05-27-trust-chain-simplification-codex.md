# 2026-05-27 trust-chain simplification Codex evaluation plan

| Owner directive | "我觉得检测有没有掺假很简单, 不用搞什么复杂的信任链。直接在用户面板每条 API 返回内容带上最上游的提供商就行了。" |
| Scope | In: read HUAKAI trust-chain docs, read source evidence from at least three reference projects, write a behavior-level evaluation to `docs/process/decisions/2026-05-27-trust-chain-simplification-codex-eval.md`. Out: code changes, commits, schema changes, dependency changes, implementation. |
| Success criteria | The decision document has sections §1-§7, includes source citations for at least three reference projects, compares Owner A / signing-lite B / Merkle C / direct SDK D, evaluates SaaS merchant-middleman attack surfaces, and ends with clean-room provenance. |
| Time estimate | 60-90 minutes wall clock; one Codex session. |
| Blast radius | Low: docs-only write under `docs/process/decisions/`; reference reads are local and behavior-level. |
| Failure modes | Missing or stale reference checkout: record the gap and use the remaining local projects. Unsupported upstream claims: remove or move to open questions. Clean-room contamination: avoid code snippets, upstream identifiers in prose except citation paths, distinctive structures, or algorithmic translation. |
| Decision points | No Owner sign-off needed for docs-only evaluation. Owner sign-off would be needed before replacing F-TRUST-001 or implementing a reduced trust-chain design. |
| Pre-execution checklist | 1. Confirm working tree state and avoid unrelated files. 2. Read HUAKAI parity row and trust-chain spec. 3. Capture current reference SHAs. 4. Read source regions from at least three allowed reference projects. 5. Draft evaluation with explicit observed/inferred/open-question separation. 6. Verify citations and clean-room tail. |

## Concrete execution order

1. Read `docs/03_FEATURE_PARITY_MATRIX.md` around F-TRUST-001 and `docs/specs/trust-chain-user-verifiable-ledger.md`.
2. Inspect local reference repositories under `~/refs/` and capture their current commit SHAs.
3. Search only for response-visible upstream/provider metadata behavior and read the minimal source regions needed for evidence.
4. Write the evaluation document with the requested section structure.
5. Run focused checks: file exists, section headings present, source citations present, no accidental code blocks from upstream, no git commit.
