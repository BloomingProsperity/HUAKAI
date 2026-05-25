# 2026-04-29 litellm cooldown retry R3
| Owner directive | "拆 7 项目深度必须够 + 保证真实，不造假" |
| Scope | Produce one source-verified decomposition for LiteLLM cooldown handler and retry policy hierarchy at `docs/decompositions/litellm/cooldown-retry-hierarchy-source-verified.md`. Read critic first, read upstream source regions, avoid upstream implementation names in output. |
| Success criteria | Output has metadata with observed region/inference/open-question counts; §2 claims cite §10 regions; critic findings are addressed; no speculative behavior claims; final Chinese Owner summary included. |
| Time estimate | 60-90 minutes wall clock, one Codex lane. |
| Blast radius | Documentation-only update. If wrong, downstream HUAKAI implementation could inherit false reliability, retry, or clean-room assumptions. |
| Failure modes | Insufficient source access: mark open questions instead of inventing. Clean-room leakage: avoid upstream names, paths, structures, and code-shaped prose. Over-broad claims: remove or cite only observed regions. |
| Decision points | Stop for Owner only if high-risk files or unavailable source make the requested artifact impossible. |
| Pre-execution checklist | 1. Read critic. 2. Read glossary and applicable rules. 3. Locate LiteLLM source regions. 4. Read at least 12 distinct regions if available. 5. Draft source-region ledger before behavior claims. 6. Write decomposition. 7. Self-check §2 citations against §10. |
