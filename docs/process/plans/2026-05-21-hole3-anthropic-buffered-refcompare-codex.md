# 2026-05-21 hole3-anthropic-buffered-refcompare-codex

| Owner directive | "参照对比任务 —— HUAKAI 方向 1 Phase 2 洞③(非流式 Anthropic Messages 响应翻译)开工前的强制前置步骤:拉最新 sub2api + CLIProxyAPI,对比这个模块他们有哪些细节和小功能。这是 specifier lane 调研,不写 HUAKAI 代码。" |
| Scope | In: download latest codeload tarballs for sub2api and CLIProxyAPI if network allows; unpack under `~/refs`; locate non-streaming/buffered Anthropic Messages response handling; write a Chinese clean-room behavior comparison report to `docs/process/research/2026-05-21-hole3-anthropic-buffered-refcompare.md`. Out: HUAKAI code changes, vendoring reference code, copying upstream identifiers/comments/source blocks, git clone/fetch operations, implementation commits. |
| Success criteria | Report contains latest-source status, source-file evidence with file:line anchors, per-project detail lists, comparison table, HUAKAI absorb/upgrade recommendations, source files read, lane marker, and UTC timestamp. Claims about upstream behavior are either observed from read source regions or explicitly marked as inferred/open question. |
| Time estimate | 45-90 minutes wall clock depending on codeload availability and module depth; one Codex research pass. |
| Blast radius | Low. Expected mutations are limited to one plan doc and one research doc. Reference tarballs are written outside the repo under `~/refs`; no HUAKAI runtime files are touched. |
| Failure modes | Network download fails: record failure explicitly and use no stale claims unless local extracted latest source is available and version is stated. Module not present: document absence with searched paths/keywords and mark open questions. Large or minified files obscure behavior: cite only confidently read regions. Clean-room risk: paraphrase behavior, avoid raw code, avoid copied upstream internal names where not protocol-level. |
| Decision points | Owner confirmation would be needed only if the task expands into HUAKAI implementation, dependency changes, schema/auth/billing/quota edits, deleting files, or modifying `LICENSE`. No such expansion is planned. |
| Pre-execution checklist | 1. Use codeload tarballs, not git clone/fetch. 2. Capture tarball top-level directory and available commit/version hints. 3. Search only for relevant Anthropic/Messages/non-stream response paths. 4. Read source regions before making behavior claims. 5. Write Chinese report with clean-room tail metadata. 6. Verify report file exists and contains required sections. |

## Concrete Execution Order

1. Create `~/refs` if missing.
2. Download `sub2api` main tarball, retry master only if main fails.
3. Download `CLIProxyAPI` main tarball.
4. Unpack each tarball into a fresh `~/refs/*-latest/` directory.
5. Record top-level tarball directory names and any README/CHANGELOG version/date hints found during inspection.
6. Use `rg` to locate Anthropic/Claude/Messages response handling in each extracted tree.
7. Read only the files needed to understand buffered/non-stream response translation.
8. Draft the report in Chinese, using behavior summaries and file:line citations.
9. Run basic verification: file exists, required section headings present, and no raw upstream code blocks were pasted.

## Clean-Room Lane Guard

本调研任务读取了 non-MIT 参考项目源码,按 AGENTS.md「Clean-Room Codex Prompt Template」与 CLAUDE.md #11 留档规范块如下:

```
=== CLEAN-ROOM LANE GUARD (DR-000 Option C carve-out + 05_CLEAN_ROOM_POLICY) ===

LANE: specifier
  - specifier = read source files, produce behavior-only summary
  - reviewer  = verify behavior summary WITHOUT re-reading source
  - On the same artifact, lane MUST be a different agent session than
    any prior lane (no same agent doing both lanes on same file).

PRIOR LANES ON THIS ARTIFACT: none

REFERENCE PROJECTS IN SCOPE: sub2api (non-MIT, license-risk) / CLIProxyAPI (MIT)

HARD PROHIBITIONS:
  - NEVER copy function names verbatim
  - NEVER copy struct field names verbatim
  - NEVER copy comments verbatim
  - NEVER do line-by-line algorithmic translation; behaviors must be
    expressed in different sentence structure than upstream code ordering
  - NEVER paste raw upstream code blocks (even small snippets)
  - When upstream uses a distinctive identifier, rename in summary

CITATION POLICY (reconciled 2026-05-10 with CLAUDE.md #12):
  - file:line citations are ALLOWED in prose as evidence anchors —
    `<repo>@<sha>:<file>:<line>` style satisfies #12 per-claim citation
  - the cited identifier itself must NOT appear verbatim in the prose
    surrounding the citation; reference it by paraphrased role only
  - "Source files read" tail block remains required (see below)

REQUIRED OUTPUT TAIL (must appear at end of every artifact):
  Source files read: <relative paths>
  Lane: <specifier | reviewer>
  Agent: <model + ID>
  UTC timestamp: <ISO 8601>

ESCALATION: if you cannot honestly produce behavior summary without
violating the prohibitions, RETURN A NO-OP "cannot summarize within
clean-room" rather than violating. The Owner prefers a partial gap to
a clean-room breach.

=== END CLEAN-ROOM LANE GUARD ===
```

研究报告 `docs/process/research/2026-05-21-hole3-anthropic-buffered-refcompare.md` 已按本块产出:specifier lane、行为级转述、`<repo>@<sha>:<file>:<line>` 形式引用、含 Source files read 尾块。
