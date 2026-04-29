# 2026-04-29 all-api-hub credential vault R3
| Owner directive | "拆 7 项目深度必须够 + 保证真实，不造假" |
| Scope | Produce one source-verified clean-room decomposition for all-api-hub credential vault, price comparison, site recognition, and secure-storage primitives at `docs/decompositions/all-api-hub/credential-vault-comparison-source-verified.md`. Do not read any companion Claude deep decomposition. |
| Success criteria | Critic findings are explicitly addressed; at least 8 distinct observed source regions are documented in source coverage proof; every §2 behavior claim has region citations; unsupported claims are moved to open questions. |
| Time estimate | 60-90 minutes wall clock; one Codex work unit. |
| Blast radius | Documentation-only change. Main risk is contaminated or overclaimed decomposition, not runtime breakage. |
| Failure modes | Upstream source is unavailable or license-sensitive; mitigate by using public docs/release notes and redacting implementation details. Source evidence may be sparse; mitigate by marking open questions instead of padding. |
| Decision points | Stop only if requested output would require copying forbidden source details or changing high-risk files. |
| Pre-execution checklist | 1. Read critic first. 2. Read glossary and clean-room constraints. 3. Locate source/documentation regions. 4. Record observed regions. 5. Draft with observed/inferred separation. 6. Verify no upstream function names, file paths, schemas, or distinctive implementation details appear. |
