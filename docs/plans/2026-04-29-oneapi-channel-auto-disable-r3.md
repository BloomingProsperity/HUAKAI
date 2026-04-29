# 2026-04-29 one-api channel auto-disable R3
| Owner directive | "拆 7 项目深度必须够 + 保证真实，不造假" |
| Scope | Produce one source-verified decomposition for one-api channel auto-disable covering immediate permanent-error disable, rolling success-rate disable, scheduled-test path, and retry interaction. Do not implement product code. Do not read companion Claude deep decomposition. |
| Success criteria | Output file exists at `docs/decompositions/one-api/channel-auto-disable-source-verified.md`; at least 12 source regions are listed; every §2 behavior claim cites a region; critic findings are dispositioned; open questions are explicit. |
| Time estimate | 60-90 minutes wall clock; one Codex work unit. |
| Blast radius | Documentation/spec artifact only. If inaccurate, downstream implementer may build wrong channel health semantics. |
| Failure modes | Source unavailable or incomplete: mark open questions rather than inventing. Clean-room leakage: avoid upstream identifiers, file paths, schemas, function names, and code-shaped phrasing. Unsupported behavior claims: remove or move to open questions. |
| Decision points | No Owner sign-off expected unless source requires high-risk repo mutation, which this task does not. |
| Pre-execution checklist | 1. Read critic first. 2. Read glossary and rules. 3. Locate one-api source/evidence. 4. Read at least 12 source regions. 5. Draft decomposition with region citations. 6. Verify critic table covers every finding. 7. Run lightweight self-check for uncited §2 claims and clean-room leakage. |
